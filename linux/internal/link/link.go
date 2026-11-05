package link

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

// Error is a link failure with the phone's status byte, when there was one.
type Error struct {
	Message string
	Status  byte
	Err     error
}

func (e *Error) Error() string { return e.Message }

func (e *Error) Unwrap() error { return e.Err }

// Options describe how to reach the phone's link server.
type Options struct {
	Host  string
	Port  int
	Token string

	// BindIP pins the link's source address (Wi-Fi sharing).
	BindIP net.IP
	// BindDevice pins the link to one interface so a reconnect can never be
	// pulled into the tunnel once the half-default routes are up.
	BindDevice string

	ConnectTimeout   time.Duration
	HandshakeTimeout time.Duration
}

func (o Options) withDefaults() Options {
	if o.ConnectTimeout <= 0 {
		o.ConnectTimeout = 10 * time.Second
	}
	if o.HandshakeTimeout <= 0 {
		o.HandshakeTimeout = 10 * time.Second
	}
	return o
}

// Link is one connection to the phone. It carries exactly one TCP stream or
// one UDP association, so callers open a new Link per SOCKS request.
type Link struct {
	conn   *net.TCPConn
	reader *bufio.Reader

	writeMu sync.Mutex
	once    sync.Once
}

// Dial connects to the phone and completes the v1 handshake.
func Dial(ctx context.Context, opts Options) (*Link, error) {
	o := opts.withDefaults()

	host := o.Host
	if net.ParseIP(host) == nil {
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, &Error{Message: fmt.Sprintf("unable to resolve %s", host), Err: err}
		}
		host = ips[0].IP.String()
		for _, candidate := range ips {
			if candidate.IP.To4() != nil {
				host = candidate.IP.String()
				break
			}
		}
	}

	target := net.JoinHostPort(host, strconv.Itoa(o.Port))
	dialer := &net.Dialer{Timeout: o.ConnectTimeout, KeepAlive: 10 * time.Second}
	if len(o.BindIP) > 0 {
		dialer.LocalAddr = &net.TCPAddr{IP: o.BindIP}
	}
	if o.BindDevice != "" {
		dialer.Control = bindToDevice(o.BindDevice)
	}

	conn, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &Error{Message: fmt.Sprintf("timed out connecting to %s", target), Err: err}
		}
		return nil, &Error{
			Message: fmt.Sprintf("unable to reach the phone at %s: %v", target, err),
			Err:     err,
		}
	}

	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		conn.Close()
		return nil, &Error{Message: "unexpected connection type"}
	}

	// The link carries every tunnelled connection; bigger buffers keep
	// high-BDP transfers from stalling on the socket window.
	_ = tcp.SetReadBuffer(1 << 20)
	_ = tcp.SetWriteBuffer(1 << 20)
	tuneKeepAlive(tcp)

	reader := bufio.NewReaderSize(tcp, 32*1024)
	l := &Link{conn: tcp, reader: reader}

	deadline := time.Now().Add(o.HandshakeTimeout)
	_ = tcp.SetDeadline(deadline)
	if err := handshake(tcp, reader, o.Token); err != nil {
		tcp.Close()
		return nil, err
	}
	_ = tcp.SetDeadline(time.Time{})

	return l, nil
}

func handshake(w io.Writer, r *bufio.Reader, token string) error {
	var header [6]byte
	binary.BigEndian.PutUint32(header[0:4], Magic)
	header[4] = Version
	if token == "" {
		header[5] = AuthNone
		if _, err := w.Write(header[:]); err != nil {
			return &Error{Message: "the phone closed the connection during the handshake", Err: err}
		}
	} else {
		header[5] = AuthToken
		normalized := NormalizeToken(token)
		if len(normalized) == 0 || len(normalized) > 128 {
			return &Error{Message: "the password length is invalid"}
		}
		payload := make([]byte, 0, len(header)+1+len(normalized))
		payload = append(payload, header[:]...)
		payload = append(payload, byte(len(normalized)))
		payload = append(payload, normalized...)
		if _, err := w.Write(payload); err != nil {
			return &Error{Message: "the phone closed the connection during the handshake", Err: err}
		}
	}

	status, err := r.ReadByte()
	if err != nil {
		return &Error{Message: "the phone closed the connection during the handshake", Err: err}
	}

	switch status {
	case StatusOK:
		return nil
	case StatusBadAuth:
		return &Error{Status: status, Message: "wrong password. Check the password shown on the phone"}
	case StatusBadVersion:
		return &Error{Status: status, Message: "the phone and PC app versions are not compatible"}
	case StatusBadRequest:
		return &Error{Status: status, Message: "the phone rejected the request"}
	default:
		return &Error{Status: status, Message: fmt.Sprintf("the phone returned an unknown status (%d)", status)}
	}
}

// Check opens and closes one link. It is the cheap liveness probe the watchdog
// uses while the tunnel is up.
func Check(ctx context.Context, opts Options) error {
	l, err := Dial(ctx, opts)
	if err != nil {
		return err
	}
	return l.Close()
}

// OpenTCP asks the phone to connect to host:port and returns the relayed stream.
func (l *Link) OpenTCP(ctx context.Context, host string, port int) (*TCPStream, error) {
	if l == nil || l.conn == nil {
		return nil, &Error{Message: "the link is closed"}
	}

	buf := []byte{ModeTCP}
	addr, err := AppendAddress(buf, host, port)
	if err != nil {
		return nil, &Error{Message: err.Error()}
	}
	if _, err := l.conn.Write(addr); err != nil {
		return nil, &Error{Message: err.Error(), Err: err}
	}

	var status byte
	if err := l.readStatus(ctx, 30*time.Second, &status); err != nil {
		return nil, err
	}

	switch status {
	case StatusOK:
		return &TCPStream{link: l}, nil
	case StatusConnectFailed:
		return nil, &Error{
			Status:  status,
			Message: fmt.Sprintf("the phone could not connect to %s", net.JoinHostPort(host, strconv.Itoa(port))),
		}
	case StatusBadAuth:
		return nil, &Error{Status: status, Message: "wrong password. Check the password shown on the phone"}
	default:
		return nil, &Error{
			Status:  status,
			Message: fmt.Sprintf("the phone rejected the connection (status %d)", status),
		}
	}
}

// OpenUDP opens a UDP association on this link.
func (l *Link) OpenUDP() (*UDPAssoc, error) {
	if l == nil || l.conn == nil {
		return nil, &Error{Message: "the link is closed"}
	}
	if _, err := l.conn.Write([]byte{ModeUDP}); err != nil {
		return nil, &Error{Message: err.Error(), Err: err}
	}
	return &UDPAssoc{link: l}, nil
}

func (l *Link) readStatus(ctx context.Context, timeout time.Duration, status *byte) error {
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = l.conn.SetReadDeadline(deadline)
	defer func() { _ = l.conn.SetReadDeadline(time.Time{}) }()

	value, err := l.reader.ReadByte()
	if err != nil {
		return &Error{Message: "the phone closed the link", Err: err}
	}
	*status = value
	return nil
}

// Close tears down the link. Any stream or association on it dies with it.
func (l *Link) Close() error {
	if l == nil {
		return nil
	}
	var err error
	l.once.Do(func() {
		if l.conn != nil {
			err = l.conn.Close()
		}
	})
	return err
}

// TCPStream is one relayed TCP connection: the client's bytes go out
// unframed and come back unframed.
type TCPStream struct {
	link *Link
}

func (s *TCPStream) Read(p []byte) (int, error) {
	return s.link.reader.Read(p)
}

func (s *TCPStream) Write(p []byte) (int, error) {
	return s.link.conn.Write(p)
}

// CloseWrite half-closes the stream toward the phone.
func (s *TCPStream) CloseWrite() error {
	return s.link.conn.CloseWrite()
}

func (s *TCPStream) Close() error {
	return s.link.Close()
}

// UDPAssoc is one framed UDP association on a link.
type UDPAssoc struct {
	link *Link
}

func (a *UDPAssoc) Send(addr Address, payload []byte) error {
	a.link.writeMu.Lock()
	defer a.link.writeMu.Unlock()
	return WriteUDPFrame(a.link.conn, addr, payload)
}

func (a *UDPAssoc) Receive() (*UDPFrame, error) {
	return ReadUDPFrame(a.link.reader)
}

func (a *UDPAssoc) Close() error {
	return a.link.Close()
}
