package link_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/MogaleAlpheus/ShareNet/linux/internal/link"
)

func TestHandshakeAndTCPRelay(t *testing.T) {
	phone, err := mockPhone(t, "")
	if err != nil {
		t.Fatal(err)
	}

	conn, err := dial(t, phone.Options(""))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	stream, err := conn.OpenTCP(contextBackground(), "echo.example", 80)
	if err != nil {
		t.Fatalf("OpenTCP: %v", err)
	}

	payload := []byte("hello sharenet")
	if _, err := stream.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}

	answer := make([]byte, len(payload))
	if _, err := io.ReadFull(stream, answer); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(answer, payload) {
		t.Fatalf("echoed %q, want %q", answer, payload)
	}
}

func TestHandshakeWithToken(t *testing.T) {
	phone, err := mockPhone(t, "7K4P-9Q2M")
	if err != nil {
		t.Fatal(err)
	}

	conn, err := dial(t, phone.Options("7k4p 9q2m"))
	if err != nil {
		t.Fatalf("Dial with the right password: %v", err)
	}
	conn.Close()

	if _, err := dial(t, phone.Options("WRONG")); err == nil {
		t.Fatal("expected a wrong password to be rejected")
	} else if linkErr, ok := err.(*link.Error); !ok || linkErr.Status != link.StatusBadAuth {
		t.Fatalf("wrong password produced %v, want status %d", err, link.StatusBadAuth)
	}

	if _, err := dial(t, phone.Options("")); err == nil {
		t.Fatal("expected a missing password to be rejected")
	}
}

func TestHandshakeRejectsOldVersion(t *testing.T) {
	phone, err := mockPhone(t, "")
	if err != nil {
		t.Fatal(err)
	}

	address := net.JoinHostPort(phone.Host(), itoa(phone.Port()))
	raw, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(3 * time.Second))

	request := make([]byte, 6)
	binary.BigEndian.PutUint32(request[:4], link.Magic)
	request[4] = link.Version + 1
	request[5] = link.AuthNone
	if _, err := raw.Write(request); err != nil {
		t.Fatal(err)
	}

	status := make([]byte, 1)
	if _, err := io.ReadFull(raw, status); err != nil {
		t.Fatal(err)
	}
	if status[0] != link.StatusBadVersion {
		t.Fatalf("status = %d, want %d", status[0], link.StatusBadVersion)
	}
}

func TestOpenTCPReportsConnectFailure(t *testing.T) {
	phone, err := mockPhone(t, "")
	if err != nil {
		t.Fatal(err)
	}

	conn, err := dial(t, phone.Options(""))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// The mock refuses port 9, the way the phone reports an unreachable host.
	_, err = conn.OpenTCP(contextBackground(), "unreachable.example", 9)
	if err == nil {
		t.Fatal("expected the connection to fail")
	}
	linkErr, ok := err.(*link.Error)
	if !ok || linkErr.Status != link.StatusConnectFailed {
		t.Fatalf("got %v, want status %d", err, link.StatusConnectFailed)
	}
}

func TestUDPAssociationEcho(t *testing.T) {
	phone, err := mockPhone(t, "")
	if err != nil {
		t.Fatal(err)
	}

	conn, err := dial(t, phone.Options(""))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	association, err := conn.OpenUDP()
	if err != nil {
		t.Fatalf("OpenUDP: %v", err)
	}

	target := link.Address{Host: "1.1.1.1", Port: 53}
	payload := []byte("query")
	if err := association.Send(target, payload); err != nil {
		t.Fatalf("Send: %v", err)
	}

	frame, err := association.Receive()
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if frame.Address.Host != target.Host || frame.Address.Port != target.Port {
		t.Errorf("reply address = %v, want %v", frame.Address, target)
	}
	if string(frame.Payload) != string(payload) {
		t.Errorf("reply payload = %q, want %q", frame.Payload, payload)
	}
}
