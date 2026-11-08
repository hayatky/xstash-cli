package syncer

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/hayatky/xstash-cli/internal/config"
	"github.com/hayatky/xstash-cli/internal/store"
	"github.com/hayatky/xstash-cli/internal/xapi"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Accountant struct {
	Store       *store.Store
	Config      config.Config
	RunID       string
	reservation int64
	at          time.Time
	postPrice   int64
	Clock       func() time.Time
}

func (a *Accountant) now() time.Time {
	if a.Clock != nil {
		return a.Clock().UTC()
	}
	return time.Now().UTC()
}

func (a *Accountant) Attach(c *xapi.Client) {
	c.BeforeRequest = a.Before
	c.AfterRequest = a.After
	c.LookupLimit = a.LookupLimit
}
func (a *Accountant) LookupLimit(ctx context.Context) (int, error) {
	if a.Config.LookupBatchSize < 1 || a.Config.LookupBatchSize > 100 {
		return 0, fmt.Errorf("lookup batch size must be 1..100")
	}
	return a.Config.LookupBatchSize, nil
}
func (a *Accountant) Before(ctx context.Context, path string, q url.Values) error {
	var count int64
	var e error
	price := a.Config.PostMicrousd
	switch {
	case strings.HasSuffix(path, "/users/me"):
		count = 1
		price = a.Config.UserMicrousd
	case strings.HasSuffix(path, "/bookmarks") || strings.HasSuffix(path, "/liked_tweets"):
		count, e = strconv.ParseInt(q.Get("max_results"), 10, 64)
		if a.Config.OwnedEligible {
			price = a.Config.OwnedPostMicrousd
		}
	case path == "/2/tweets":
		count = int64(len(strings.Split(q.Get("ids"), ",")))
	default:
		return fmt.Errorf("unrecognized endpoint for budget")
	}
	if e != nil || count < 1 || count > 100 || price <= 0 || price > 1000000000 {
		return fmt.Errorf("missing or invalid price or result limit")
	}
	total := count * price
	if q.Get("expansions") != "" {
		if a.Config.UserMicrousd <= 0 || a.Config.UserMicrousd > 1000000000 || a.Config.MediaMicrousd <= 0 || a.Config.MediaMicrousd > 1000000000 {
			return fmt.Errorf("configure user and media prices before requesting expansions")
		}
		total += count * (a.Config.UserMicrousd + 4*a.Config.MediaMicrousd)
	}
	a.at = a.now()
	a.postPrice = price
	a.reservation, e = a.Store.Reserve(ctx, a.RunID, a.at, total)
	return e
}
func (a *Accountant) After(ctx context.Context, path string, status int, body []byte, requestErr error) error {
	end := a.now()
	if end.Format("2006-01-02") != a.at.Format("2006-01-02") {
		id, e := a.Store.CarryReservation(ctx, a.reservation, end)
		if e != nil {
			return e
		}
		a.reservation = id
		a.at = end
	}
	certain := requestErr == nil && status >= 200 && status < 300
	var reads []store.Read
	if certain {
		var envelope struct {
			Data   json.RawMessage `json:"data"`
			Errors json.RawMessage `json:"errors"`
			Meta   struct {
				ResultCount *int `json:"result_count"`
			} `json:"meta"`
			Includes struct {
				Users []json.RawMessage `json:"users"`
				Media []json.RawMessage `json:"media"`
			} `json:"includes"`
		}
		if json.Unmarshal(body, &envelope) != nil {
			certain = false
		} else {
			var data []json.RawMessage
			if len(envelope.Data) > 0 && string(envelope.Data) != "null" {
				if envelope.Data[0] == '[' {
					if json.Unmarshal(envelope.Data, &data) != nil {
						certain = false
					}
				} else {
					data = []json.RawMessage{envelope.Data}
				}
			}
			if strings.HasSuffix(path, "/bookmarks") || strings.HasSuffix(path, "/liked_tweets") {
				if envelope.Meta.ResultCount == nil || *envelope.Meta.ResultCount != len(data) {
					certain = false
				}
			} else if len(data) == 0 && len(envelope.Errors) == 0 {
				certain = false
			}
			for _, raw := range data {
				var v struct {
					ID string `json:"id"`
				}
				if json.Unmarshal(raw, &v) != nil || v.ID == "" {
					certain = false
					break
				}
				kind := "post"
				if strings.HasSuffix(path, "/users/me") {
					kind = "user"
				}
				reads = append(reads, store.Read{Type: kind, ID: v.ID, Price: a.postPrice})
			}
			for _, raw := range envelope.Includes.Users {
				var v struct {
					ID string `json:"id"`
				}
				if json.Unmarshal(raw, &v) != nil || v.ID == "" {
					certain = false
					break
				}
				reads = append(reads, store.Read{Type: "user", ID: v.ID, Price: a.Config.UserMicrousd})
			}
			for _, raw := range envelope.Includes.Media {
				var v struct {
					ID string `json:"media_key"`
				}
				if json.Unmarshal(raw, &v) != nil || v.ID == "" {
					certain = false
					break
				}
				reads = append(reads, store.Read{Type: "media", ID: v.ID, Price: a.Config.MediaMicrousd})
			}
		}
	}
	return a.Store.RecordAttempt(ctx, a.reservation, a.RunID, path, a.at, status, reads, certain)
}
