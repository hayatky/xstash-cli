package store

import (
	"context"
	"time"
)

// CostDetails explains historical estimates without changing prices or claiming
// access to the developer account's actual billing. A resource-day is one
// resource of a given type on one UTC day, not a unique resource for the month.
type CostDetails struct {
	ResourcePrices []ResourcePrice `json:"resource_prices"`
	Endpoints      []EndpointCost  `json:"endpoints"`
	ResourceDays   int             `json:"priced_resource_days"`
	DedupBasis     string          `json:"dedup_basis"`
}

type ResourcePrice struct {
	ResourceType      string `json:"resource_type"`
	UnitMicrousd      int64  `json:"unit_microusd"`
	ResourceDays      int    `json:"resource_days"`
	EstimatedMicrousd int64  `json:"estimated_microusd"`
}

type EndpointCost struct {
	Endpoint          string `json:"endpoint"`
	HTTPStatus        int    `json:"http_status"`
	Requests          int    `json:"requests"`
	ObservedResources int    `json:"observed_resources"`
	EstimatedMicrousd int64  `json:"estimated_microusd"`
}

func (s *Store) CostDetails(ctx context.Context, month string) (CostDetails, error) {
	out := CostDetails{
		ResourcePrices: []ResourcePrice{}, Endpoints: []EndpointCost{},
		DedupBasis: "resource type and ID per UTC day in this database; first recorded price; cross-price precedence and service-side dedup exceptions unverified",
	}
	if _, err := time.Parse("2006-01", month); err != nil {
		return out, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT resource_type,price_microusd,count(*),sum(price_microusd) FROM billable_reads WHERE substr(day,1,7)=? GROUP BY resource_type,price_microusd ORDER BY resource_type,price_microusd`, month)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r ResourcePrice
		if err := rows.Scan(&r.ResourceType, &r.UnitMicrousd, &r.ResourceDays, &r.EstimatedMicrousd); err != nil {
			rows.Close()
			return out, err
		}
		out.ResourcePrices = append(out.ResourcePrices, r)
		out.ResourceDays += r.ResourceDays
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	// Redact account IDs: the billing breakdown needs the endpoint class only.
	rows, err = s.DB.QueryContext(ctx, `SELECT CASE WHEN endpoint LIKE '/2/users/%/bookmarks' THEN '/2/users/{id}/bookmarks' WHEN endpoint LIKE '/2/users/%/liked_tweets' THEN '/2/users/{id}/liked_tweets' ELSE endpoint END AS endpoint_class,status,count(*),sum(resources),sum(estimated_microusd) FROM api_requests WHERE substr(requested_at,1,7)=? GROUP BY endpoint_class,status ORDER BY endpoint_class,status`, month)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var e EndpointCost
		if err := rows.Scan(&e.Endpoint, &e.HTTPStatus, &e.Requests, &e.ObservedResources, &e.EstimatedMicrousd); err != nil {
			return out, err
		}
		out.Endpoints = append(out.Endpoints, e)
	}
	return out, rows.Err()
}
