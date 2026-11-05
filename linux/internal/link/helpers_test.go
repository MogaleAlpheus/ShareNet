package link_test

import (
	"bufio"
	"bytes"
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/MogaleAlpheus/ShareNet/linux/internal/link"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/mockphone"
)

func mockPhone(t *testing.T, token string) (*mockphone.Phone, error) {
	t.Helper()

	phone, err := mockphone.Start(token)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = phone.Close() })
	return phone, nil
}

func dial(t *testing.T, options link.Options) (*link.Link, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return link.Dial(ctx, options)
}

func contextBackground() context.Context { return context.Background() }

func bufioReader(data []byte) *bufio.Reader {
	return bufio.NewReader(bytes.NewReader(data))
}

func itoa(value int) string { return strconv.Itoa(value) }
