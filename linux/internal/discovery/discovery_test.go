package discovery

import (
	"net"
	"testing"
)

func TestParseBeacon(t *testing.T) {
	phone, ok := parseBeacon([]byte("SHARENET1|1080|Pixel 8 Pro"), net.ParseIP("192.168.1.20"))
	if !ok {
		t.Fatal("a valid beacon was rejected")
	}
	if phone.Port != 1080 || phone.Model != "Pixel 8 Pro" || !phone.IP.Equal(net.ParseIP("192.168.1.20")) {
		t.Fatalf("parsed %+v", phone)
	}

	bare, ok := parseBeacon([]byte("sharenet1|1080"), net.ParseIP("10.0.0.5"))
	if !ok || bare.Model != "" {
		t.Fatalf("a model-less beacon parsed as %+v (ok=%v)", bare, ok)
	}

	for _, payload := range []string{"", "OTHER|1080", "SHARENET1", "SHARENET1|x", "SHARENET1|0"} {
		if _, ok := parseBeacon([]byte(payload), net.ParseIP("10.0.0.5")); ok {
			t.Errorf("accepted invalid beacon %q", payload)
		}
	}
}

func TestProbeHostsStaysInsideSlash24(t *testing.T) {
	_, subnet, err := net.ParseCIDR("10.0.0.0/16")
	if err != nil {
		t.Fatal(err)
	}

	hosts := probeHosts(subnet, net.ParseIP("10.0.5.7"))
	if len(hosts) != 253 {
		t.Fatalf("probed %d hosts, want 253 (254 neighbours minus this host)", len(hosts))
	}
	if hosts[0].String() != "10.0.5.1" || hosts[len(hosts)-1].String() != "10.0.5.254" {
		t.Fatalf("probe range is %s - %s, want 10.0.5.1 - 10.0.5.254",
			hosts[0], hosts[len(hosts)-1])
	}
	for _, host := range hosts {
		if host.Equal(net.ParseIP("10.0.5.7")) {
			t.Fatal("the local address was included in the probe list")
		}
	}
}

func TestProbeHostsIgnoresIPv6(t *testing.T) {
	_, subnet, err := net.ParseCIDR("fd00::/64")
	if err != nil {
		t.Fatal(err)
	}
	if hosts := probeHosts(subnet, net.ParseIP("fd00::5")); hosts != nil {
		t.Fatalf("probed IPv6 hosts: %v", hosts)
	}
}
