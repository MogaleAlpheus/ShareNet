// Package link implements ShareNet's link protocol v1, the wire format the
// Android app speaks over adb (USB) or TCP 1080 (Wi-Fi).
//
// Handshake: "SNET" (4) | version (1) | auth mode (1) | [token length (1) | token].
// The phone replies with a status byte. The client then selects TCP mode (1) or
// UDP mode (2).
package link

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

const (
	Magic   = 0x534E4554 // "SNET"
	Version = 1

	AuthNone  = 0
	AuthToken = 1

	StatusOK            = 0
	StatusBadAuth       = 1
	StatusBadVersion    = 2
	StatusBadRequest    = 3
	StatusConnectFailed = 4

	ModeTCP = 1
	ModeUDP = 2

	AtypIPv4   = 1
	AtypDomain = 3
	AtypIPv6   = 4

	MaxFrame = 65535

	// WifiPort is the TCP port the phone's link server listens on over Wi-Fi.
	WifiPort = 1080

	// UsbFallbackPort is the phone's loopback listener, used when a device
	// cannot forward the abstract socket over adb.
	UsbFallbackPort = 30001

	// BeaconPort carries the phone's presence datagram on the local network.
	BeaconPort = 1081
)

// Address is one link target: a literal address or a domain the phone resolves.
type Address struct {
	Host string
	Port int
	IP   net.IP
}

func (a Address) String() string {
	return net.JoinHostPort(a.Host, strconv.Itoa(a.Port))
}

// UDPFrame is one length-prefixed datagram carried in UDP mode.
type UDPFrame struct {
	Address Address
	Payload []byte
}

// NormalizeToken mirrors the Android app: trim, upper case, drop dashes and
// spaces, so a password typed with grouping still matches.
func NormalizeToken(value string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(value)) {
		if r == '-' || r == ' ' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ParseAddress reads one ATYP/ADDR/PORT triple, the layout SOCKS5 also uses.
func ParseAddress(r *bufio.Reader) (Address, error) {
	atyp, err := r.ReadByte()
	if err != nil {
		return Address{}, err
	}

	var addr Address
	switch atyp {
	case AtypIPv4:
		var raw [4]byte
		if _, err := io.ReadFull(r, raw[:]); err != nil {
			return Address{}, err
		}
		addr.IP = net.IP(append([]byte(nil), raw[:]...))
		addr.Host = addr.IP.String()
	case AtypIPv6:
		var raw [16]byte
		if _, err := io.ReadFull(r, raw[:]); err != nil {
			return Address{}, err
		}
		addr.IP = net.IP(append([]byte(nil), raw[:]...))
		addr.Host = addr.IP.String()
	case AtypDomain:
		length, err := r.ReadByte()
		if err != nil {
			return Address{}, err
		}
		if length == 0 {
			return Address{}, errors.New("empty domain name")
		}
		raw := make([]byte, length)
		if _, err := io.ReadFull(r, raw); err != nil {
			return Address{}, err
		}
		addr.Host = string(raw)
	default:
		return Address{}, fmt.Errorf("unsupported address type %d", atyp)
	}

	var port [2]byte
	if _, err := io.ReadFull(r, port[:]); err != nil {
		return Address{}, err
	}
	addr.Port = int(binary.BigEndian.Uint16(port[:]))
	return addr, nil
}

// AppendAddress appends one ATYP/ADDR/PORT triple to buf.
func AppendAddress(buf []byte, host string, port int) ([]byte, error) {
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("port %d is out of range", port)
	}

	ip := net.ParseIP(host)
	if ip != nil {
		if v4 := ip.To4(); v4 != nil {
			buf = append(buf, AtypIPv4)
			buf = append(buf, v4...)
		} else {
			buf = append(buf, AtypIPv6)
			buf = append(buf, ip.To16()...)
		}
	} else {
		if len(host) == 0 || len(host) > 255 {
			return nil, errors.New("domain name must be 1-255 bytes")
		}
		buf = append(buf, AtypDomain, byte(len(host)))
		buf = append(buf, host...)
	}

	return append(buf, byte(port>>8), byte(port&0xff)), nil
}

// ReadUDPFrame reads one framed datagram. It returns io.EOF when the phone
// closed the association cleanly.
func ReadUDPFrame(r *bufio.Reader) (*UDPFrame, error) {
	var header [2]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, io.EOF
		}
		return nil, err
	}

	length := int(binary.BigEndian.Uint16(header[:]))
	if length < 1 {
		return nil, fmt.Errorf("invalid UDP frame length %d", length)
	}

	frame := make([]byte, length)
	if _, err := io.ReadFull(r, frame); err != nil {
		return nil, err
	}

	body := bufio.NewReader(bytes.NewReader(frame))
	addr, err := ParseAddress(body)
	if err != nil {
		return nil, err
	}
	payload, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}

	return &UDPFrame{Address: addr, Payload: payload}, nil
}

// WriteUDPFrame writes one framed datagram in a single write so the length
// prefix and body cannot be separated by a concurrent writer.
func WriteUDPFrame(w io.Writer, addr Address, payload []byte) error {
	body, err := AppendAddress(nil, addr.Host, addr.Port)
	if err != nil {
		return err
	}
	body = append(body, payload...)
	if len(body) > MaxFrame {
		return errors.New("UDP frame is too large")
	}

	frame := make([]byte, 2+len(body))
	binary.BigEndian.PutUint16(frame[:2], uint16(len(body)))
	copy(frame[2:], body)

	_, err = w.Write(frame)
	return err
}
