// Package logx is the tiny timestamped logger the CLI writes to stderr (and an
// optional log file).
package logx

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// BufferLength is how many recent lines the UI can show.
const BufferLength = 500

// Entry is one log line kept for the UI's activity panel.
type Entry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

// Logger is safe for concurrent use by the relay and the watchdog.
type Logger struct {
	mu    sync.Mutex
	out   io.Writer
	file  *os.File
	debug bool
	ring  []Entry
}

// New writes to stderr, and also to path when it is not empty.
func New(debug bool, path string) (*Logger, error) {
	l := &Logger{out: os.Stderr, debug: debug}
	if path == "" {
		return l, nil
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, fmt.Errorf("could not open the log file: %w", err)
	}
	l.file = file
	l.out = io.MultiWriter(os.Stderr, file)
	return l, nil
}

// Info records a normal event.
func (l *Logger) Info(format string, args ...any) { l.write("INFO", format, args...) }

// Warn records something the user should know about but which does not stop
// the tunnel.
func (l *Logger) Warn(format string, args ...any) { l.write("WARN", format, args...) }

// Error records a failure.
func (l *Logger) Error(format string, args ...any) { l.write("ERROR", format, args...) }

// Debug records engine chatter; only shown with --verbose.
func (l *Logger) Debug(format string, args ...any) {
	if !l.debug {
		return
	}
	l.write("DEBUG", format, args...)
}

func (l *Logger) write(level, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.out, "%s  %-5s  %s\n", now.Format("15:04:05"), level, message)

	l.ring = append(l.ring, Entry{Time: now, Level: level, Message: message})
	if len(l.ring) > BufferLength {
		l.ring = append([]Entry(nil), l.ring[len(l.ring)-BufferLength:]...)
	}
}

// Recent returns the last n entries, oldest first, for the activity panel.
func (l *Logger) Recent(n int) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()

	if n <= 0 || n > len(l.ring) {
		n = len(l.ring)
	}
	start := len(l.ring) - n
	return append([]Entry(nil), l.ring[start:]...)
}

// Close flushes and closes the optional log file.
func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
}
