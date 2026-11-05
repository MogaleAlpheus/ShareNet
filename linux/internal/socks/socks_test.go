package socks_test

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/MogaleAlpheus/ShareNet/linux/internal/link"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/mockphone"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/socks"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/stats"
)

type testClient struct {
	conn   net.Conn
	reader *bufio.Reader
}

func startServer(t *testing.T, phone *mockphone.Phone, counter *stats.Stats) *socks.Server {
	t.Helper()

	server, err := socks.Start(
		func(ctx context.Context) (*link.Link, error) {
			return link.Dial(ctx, phone.Options(""))
		},
		func(string) {},
		counter,
	)
	if err != nil {
		t.Fatalf("socks.Start: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server
}

func connectTo(t *testing.T, server *socks.Server) *testClient {
	t.Helper()

	conn, err := net.DialTimeout(
		"tcp4",
		net.JoinHostPort("127.0.0.1", strconv.Itoa(server.Port())),
		3*time.Second,
	)
	if err != nil {
		t.Fatalf("dial bridge: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	client := &testClient{conn: conn, reader: bufio.NewReader(conn)}

	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		t.Fatalf("greeting: %v", err)
	}
	answer := make([]byte, 2)
	if _, err := io.ReadFull(client.reader, answer); err != nil {
		t.Fatalf("greeting reply: %v", err)
	}
	if answer[0] != 5 || answer[1] != 0 {
		t.Fatalf("greeting reply = %v, want [5 0]", answer)
	}

	return client
}

func (c *testClient) request(t *testing.T, command byte, host string, port int) (byte, link.Address) {
	t.Helper()

	request := []byte{5, command, 0}
	request, err := link.AppendAddress(request, host, port)
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	if _, err := c.conn.Write(request); err != nil {
		t.Fatalf("write request: %v", err)
	}

	head := make([]byte, 3)
	if _, err := io.ReadFull(c.reader, head); err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if head[0] != 5 {
		t.Fatalf("reply version = %d, want 5", head[0])
	}

	bound, err := link.ParseAddress(c.reader)
	if err != nil {
		t.Fatalf("read reply address: %v", err)
	}
	return head[1], bound
}

func TestConnectRelaysAndCounts(t *testing.T) {
	phone, err := mockphone.Start("")
	if err != nil {
		t.Fatal(err)
	}
	defer phone.Close()

	counter := stats.New()
	counter.StartSession()
	server := startServer(t, phone, counter)

	client := connectTo(t, server)
	reply, _ := client.request(t, 1, "echo.example", 80)
	if reply != 0 {
		t.Fatalf("CONNECT reply = %d, want 0", reply)
	}

	payload := []byte("ping through the tunnel")
	if _, err := client.conn.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}

	answer := make([]byte, len(payload))
	if _, err := io.ReadFull(client.reader, answer); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(answer, payload) {
		t.Fatalf("echoed %q, want %q", answer, payload)
	}

	waitFor(t, func() bool {
		snapshot := counter.Snapshot()
		return snapshot.Download >= int64(len(payload)) && snapshot.Upload >= int64(len(payload))
	})
}

func TestConnectReportsUnreachableTarget(t *testing.T) {
	phone, err := mockphone.Start("")
	if err != nil {
		t.Fatal(err)
	}
	defer phone.Close()

	server := startServer(t, phone, stats.New())
	client := connectTo(t, server)

	reply, _ := client.request(t, 1, "unreachable.example", 9)
	if reply != 5 {
		t.Fatalf("CONNECT reply = %d, want 5 (connection refused)", reply)
	}
}

func TestUDPAssociateEchoes(t *testing.T) {
	phone, err := mockphone.Start("")
	if err != nil {
		t.Fatal(err)
	}
	defer phone.Close()

	counter := stats.New()
	counter.StartSession()
	server := startServer(t, phone, counter)

	client := connectTo(t, server)
	reply, bound := client.request(t, 3, "0.0.0.0", 0)
	if reply != 0 {
		t.Fatalf("UDP ASSOCIATE reply = %d, want 0", reply)
	}
	if bound.Port == 0 {
		t.Fatal("the bridge did not report a relay port")
	}

	relay, err := net.DialTimeout("udp4", bound.String(), 3*time.Second)
	if err != nil {
		t.Fatalf("dial relay: %v", err)
	}
	defer relay.Close()
	_ = relay.SetDeadline(time.Now().Add(5 * time.Second))

	payload := []byte("dns query")
	datagram := []byte{0, 0, 0}
	datagram, err = link.AppendAddress(datagram, "example.com", 53)
	if err != nil {
		t.Fatal(err)
	}
	datagram = append(datagram, payload...)
	if _, err := relay.Write(datagram); err != nil {
		t.Fatalf("write datagram: %v", err)
	}

	buffer := make([]byte, 2048)
	n, err := relay.Read(buffer)
	if err != nil {
		t.Fatalf("read datagram: %v", err)
	}

	reader := bufio.NewReader(bytes.NewReader(buffer[:n]))
	if _, err := reader.Discard(3); err != nil {
		t.Fatalf("discard header: %v", err)
	}
	source, err := link.ParseAddress(reader)
	if err != nil {
		t.Fatalf("parse datagram address: %v", err)
	}
	rest, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}

	if source.Host != "example.com" || source.Port != 53 {
		t.Errorf("reply from %v, want example.com:53", source)
	}
	if !bytes.Equal(rest, payload) {
		t.Errorf("reply payload = %q, want %q", rest, payload)
	}

	waitFor(t, func() bool {
		snapshot := counter.Snapshot()
		return snapshot.Download >= int64(len(payload)) && snapshot.Upload >= int64(len(payload))
	})
}

func TestUnsupportedCommandIsRejected(t *testing.T) {
	phone, err := mockphone.Start("")
	if err != nil {
		t.Fatal(err)
	}
	defer phone.Close()

	server := startServer(t, phone, stats.New())
	client := connectTo(t, server)

	reply, _ := client.request(t, 2, "example.com", 80)
	if reply != 7 {
		t.Fatalf("BIND reply = %d, want 7 (command not supported)", reply)
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met in time")
}
