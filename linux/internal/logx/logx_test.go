package logx_test

import (
	"testing"

	"github.com/MogaleAlpheus/ShareNet/linux/internal/logx"
)

func TestRecentKeepsTheNewestLines(t *testing.T) {
	logger, err := logx.New(false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	logger.Info("first")
	logger.Warn("second %d", 2)
	logger.Debug("hidden chatter")

	entries := logger.Recent(10)
	if len(entries) != 2 {
		t.Fatalf("kept %d entries, want 2 (debug is hidden)", len(entries))
	}
	if entries[0].Message != "first" || entries[1].Message != "second 2" {
		t.Fatalf("entries %+v", entries)
	}
	if entries[1].Level != "WARN" {
		t.Fatalf("level = %q, want WARN", entries[1].Level)
	}
	if entries[0].Time.IsZero() {
		t.Fatal("entries have no timestamp")
	}
}

func TestBufferIsBounded(t *testing.T) {
	logger, err := logx.New(false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	for i := 0; i < logx.BufferLength+25; i++ {
		logger.Info("line")
	}

	entries := logger.Recent(logx.BufferLength + 100)
	if len(entries) != logx.BufferLength {
		t.Fatalf("buffer holds %d entries, want %d", len(entries), logx.BufferLength)
	}
	if tail := logger.Recent(1); len(tail) != 1 || tail[0].Message != "line" {
		t.Fatalf("tail %+v", tail)
	}
}
