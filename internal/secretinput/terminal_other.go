//go:build !linux && !darwin

package secretinput

import (
	"context"
	"fmt"
)

type Terminal struct{}

func Open(context.Context) (*Terminal, error) {
	return nil, fmt.Errorf("secure terminal import currently requires Linux or macOS; use config init on this platform")
}
func (*Terminal) Context() context.Context { return context.Background() }
func (*Terminal) Close() error             { return nil }
func (*Terminal) Prompt(context.Context, string) ([]byte, error) {
	return nil, fmt.Errorf("secure terminal import is unavailable")
}
