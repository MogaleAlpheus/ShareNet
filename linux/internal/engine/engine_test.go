package engine

import (
	"os"
	"path/filepath"
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

// The desktop launcher runs pkexec with a PATH that does not include the
// install prefix, so the engine has to be found beside the binary.
func TestFindLooksBesideTheExecutable(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable: %v", err)
	}

	sideBySide := filepath.Join(filepath.Dir(executable), "tun2socks")
	if _, err := os.Stat(sideBySide); err == nil {
		t.Skipf("%s already exists", sideBySide)
	}
	if err := os.WriteFile(sideBySide, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Skipf("cannot create %s: %v", sideBySide, err)
	}
	defer os.Remove(sideBySide)

	t.Setenv("SHARENET_TUN2SOCKS", "")

	found, err := Find("")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if found != sideBySide {
		t.Fatalf("Find = %q, want the engine beside the binary (%q)", found, sideBySide)
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
