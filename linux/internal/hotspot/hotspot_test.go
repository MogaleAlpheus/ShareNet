package hotspot

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/MogaleAlpheus/ShareNet/linux/internal/netcfg"
)

func TestSSIDAndKey(t *testing.T) {
	ssid, err := SSID("7k4p-9q2m-3x8w")
	if err != nil {
		t.Fatal(err)
	}
	if ssid != "ShareNet-7K4P" {
		t.Fatalf("SSID = %q, want ShareNet-7K4P", ssid)
	}

	key, err := Key("7k4p-9q2m-3x8w")
	if err != nil {
		t.Fatal(err)
	}
	if key != "7K4P9Q2M3X8W" {
		t.Fatalf("Key = %q", key)
	}

	if _, err := SSID("7k"); err == nil {
		t.Fatal("a too-short password produced an SSID")
	}
	if _, err := Key("7k4p"); err == nil {
		t.Fatal("a four-character password produced a WPA2 key")
	}
}

func TestJoinNetworkWaitsForHandshake(t *testing.T) {
	attempts := 0
	route := &netcfg.Route{
		Interface: "wlp2s0",
		Source:    net.ParseIP("192.168.43.2"),
		Gateway:   net.ParseIP("192.168.43.1"),
		Prefix:    mustCIDR(t, "192.168.43.0/24"),
	}

	join, err := JoinNetwork(context.Background(), "7k4p9q2m", Ops{
		Connect: func(ctx context.Context, ssid, key string) error {
			if ssid != "ShareNet-7K4P" || key != "7K4P9Q2M" {
				t.Errorf("connect(%q, %q)", ssid, key)
			}
			return nil
		},
		Route: func(ctx context.Context) (*netcfg.Route, error) { return route, nil },
		Verify: func(ctx context.Context, r *netcfg.Route) error {
			attempts++
			if attempts < 3 {
				return context.DeadlineExceeded
			}
			return nil
		},
		Wait: func(ctx context.Context) {},
		Log:  func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("JoinNetwork: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("verified %d times, want 3", attempts)
	}
	if join.Gateway.String() != "192.168.43.1" || join.Interface != "wlp2s0" {
		t.Fatalf("joined %+v", join)
	}
}

func TestJoinNetworkGivesUpWhenSilent(t *testing.T) {
	join, err := JoinNetwork(context.Background(), "7k4p9q2m", Ops{
		Connect: func(ctx context.Context, ssid, key string) error { return nil },
		Route: func(ctx context.Context) (*netcfg.Route, error) {
			return &netcfg.Route{Interface: "wlp2s0", Gateway: net.ParseIP("10.0.0.1")}, nil
		},
		Verify:  func(ctx context.Context, r *netcfg.Route) error { return context.DeadlineExceeded },
		Wait:    func(ctx context.Context) {},
		Log:     func(string, ...any) {},
		Timeout: 20 * time.Millisecond,
	})
	if err == nil {
		t.Fatalf("a silent network produced %+v", join)
	}
	if !contains(err.Error(), "ShareNet-7K4P") {
		t.Fatalf("the error should name the network, got %v", err)
	}
}

func TestParseCurrentSSID(t *testing.T) {
	output := "no:HomeNet\nyes:ShareNet-7K4P\nno:Cafe\n"
	if got := parseCurrentSSID(output); got != "ShareNet-7K4P" {
		t.Fatalf("parsed %q, want ShareNet-7K4P", got)
	}

	if got := parseCurrentSSID("no:HomeNet\n"); got != "" {
		t.Fatalf("an unaassociated station parsed %q", got)
	}
}

func mustCIDR(t *testing.T, value string) *net.IPNet {
	t.Helper()
	_, network, err := net.ParseCIDR(value)
	if err != nil {
		t.Fatal(err)
	}
	return network
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
