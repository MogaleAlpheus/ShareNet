//go:build !linux

package hotspot

import (
	"context"
	"errors"

	"github.com/MogaleAlpheus/ShareNet/linux/internal/netcfg"
)

// NewOps exists so the package builds and tests run off Linux; the hotspot is
// driven by nmcli and is only meaningful there.
func NewOps(token string, logf func(string, ...any)) Ops {
	unavailable := errors.New("joining the phone's hotspot is only supported on Linux with NetworkManager")
	return Ops{
		Connect: func(ctx context.Context, ssid, key string) error { return unavailable },
		Route:   func(ctx context.Context) (*netcfg.Route, error) { return nil, unavailable },
		Verify:  func(ctx context.Context, route *netcfg.Route) error { return unavailable },
		Wait:    func(ctx context.Context) {},
		Log:     logf,
	}
}

// Leave is a no-op off Linux.
func Leave(ctx context.Context, logf func(string, ...any)) {}

// Available reports whether nmcli is usable; never off Linux.
func Available() bool { return false }
