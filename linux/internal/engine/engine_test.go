package engine

import (
	"strings"
	"testing"
)

func TestArgumentsUseDoubleDashFlags(t *testing.T) {
	args := arguments("sharenet", 41000, 1500)

	for _, arg := range args {
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") {
			t.Fatalf("single-dash flag %q would be rejected by tun2socks", arg)
		}
	}

	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--device tun://sharenet",
		"--proxy socks5://127.0.0.1:41000",
		"--loglevel info",
		"--mtu 1500",
		"--tcp-sndbuf 4MB",
		"--tcp-rcvbuf 4MB",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("arguments missing %q: %s", want, joined)
		}
	}
}

func TestParseVersion(t *testing.T) {
	cases := map[string]string{
		"tun2socks v2.7.0":                      "v2.7.0",
		"tun2socks v2.7.0-12-gabc123":           "v2.7.0",
		"2.7.0":                                 "v2.7.0",
		"tun2socks version v2.7.0 linux/amd64":  "v2.7.0",
		"tun2socks v2.10.1 built with go1.22.0": "v2.10.1",
		"not an engine":                         "",
		"":                                      "",
	}

	for input, want := range cases {
		if got := parseVersion(input); got != want {
			t.Errorf("parseVersion(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestStartRejectsBadMTU(t *testing.T) {
	if _, err := Start("tun2socks", "sharenet", 41000, 9000, func(string) {}); err == nil {
		t.Fatal("expected an out-of-range MTU to be refused")
	}
}
