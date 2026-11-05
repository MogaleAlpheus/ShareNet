package link_test

import (
	"bytes"
	"testing"

	"github.com/MogaleAlpheus/ShareNet/linux/internal/link"
)

func TestNormalizeToken(t *testing.T) {
	cases := map[string]string{
		"":                "",
		" 7k4p-9q2m ":     "7K4P9Q2M",
		"abc def":         "ABCDEF",
		"already-normal ": "ALREADYNORMAL",
		"---":             "",
		"K3Y":             "K3Y",
	}

	for input, want := range cases {
		if got := link.NormalizeToken(input); got != want {
			t.Errorf("NormalizeToken(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAddressRoundTrip(t *testing.T) {
	targets := []link.Address{
		{Host: "192.168.1.20", Port: 1080},
		{Host: "2001:db8::1", Port: 443},
		{Host: "example.com", Port: 53},
	}

	for _, target := range targets {
		encoded, err := link.AppendAddress(nil, target.Host, target.Port)
		if err != nil {
			t.Fatalf("AppendAddress(%v): %v", target, err)
		}

		decoded, err := link.ParseAddress(bufioReader(encoded))
		if err != nil {
			t.Fatalf("ParseAddress(%v): %v", target, err)
		}

		if decoded.Host != target.Host || decoded.Port != target.Port {
			t.Errorf("round trip of %v produced %v", target, decoded)
		}
	}
}

func TestAppendAddressRejectsBadPort(t *testing.T) {
	if _, err := link.AppendAddress(nil, "example.com", 70000); err == nil {
		t.Fatal("expected an out-of-range port to be rejected")
	}
	if _, err := link.AppendAddress(nil, "", 80); err == nil {
		t.Fatal("expected an empty host to be rejected")
	}
}

func TestUDPFrameRoundTrip(t *testing.T) {
	var buffer bytes.Buffer
	payload := []byte("dns query payload")
	target := link.Address{Host: "1.1.1.1", Port: 53}

	if err := link.WriteUDPFrame(&buffer, target, payload); err != nil {
		t.Fatalf("WriteUDPFrame: %v", err)
	}

	frame, err := link.ReadUDPFrame(bufioReader(buffer.Bytes()))
	if err != nil {
		t.Fatalf("ReadUDPFrame: %v", err)
	}

	if frame.Address.Host != target.Host || frame.Address.Port != target.Port {
		t.Errorf("address = %v, want %v", frame.Address, target)
	}
	if string(frame.Payload) != string(payload) {
		t.Errorf("payload = %q, want %q", frame.Payload, payload)
	}
}

func TestReadUDPFrameRejectsEmptyFrame(t *testing.T) {
	if _, err := link.ReadUDPFrame(bufioReader([]byte{0, 0})); err == nil {
		t.Fatal("expected a zero-length frame to be rejected")
	}
}

func TestWriteUDPFrameRejectsOversize(t *testing.T) {
	payload := make([]byte, link.MaxFrame)
	err := link.WriteUDPFrame(&bytes.Buffer{}, link.Address{Host: "1.1.1.1", Port: 53}, payload)
	if err == nil {
		t.Fatal("expected an oversized frame to be rejected")
	}
}
