//go:build linux

// Package instance stops a second copy of the client from fighting the first
// over the adapter, the DNS link and the adb forward. The Windows app uses a
// named mutex; this is the flock equivalent.
package instance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Lock is one held instance lock.
type Lock struct {
	file *os.File
	path string
}

// Acquire takes the named lock without blocking. The returned error names the
// running process when the lock is already held.
func Acquire(name string) (*Lock, error) {
	path := lockPath(name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("could not create the lock file: %w", err)
	}

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		pid := readPID(file)
		_ = file.Close()

		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			owner := ""
			if pid > 0 {
				owner = fmt.Sprintf(" (pid %d)", pid)
			}
			return nil, fmt.Errorf(
				"ShareNet is already running%s. Use the window that is open, or run "+
					"\"sudo sharenet cleanup\" if it crashed", owner,
			)
		}
		return nil, fmt.Errorf("could not lock %s: %w", path, err)
	}

	if err := file.Truncate(0); err == nil {
		_, _ = file.Seek(0, 0)
		_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
	}

	return &Lock{file: file, path: path}, nil
}

// Release drops the lock. The file stays, because removing it would race a
// second process that is just opening the same path.
func (l *Lock) Release() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}

// Path is the lock file in use.
func (l *Lock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

func lockPath(name string) string {
	for _, key := range []string{"XDG_RUNTIME_DIR", "TMPDIR"} {
		if dir := os.Getenv(key); dir != "" {
			return filepath.Join(dir, "sharenet-"+name+".lock")
		}
	}
	return filepath.Join(os.TempDir(), "sharenet-"+name+".lock")
}

func readPID(file *os.File) int {
	if _, err := file.Seek(0, 0); err != nil {
		return 0
	}
	raw := make([]byte, 32)
	n, _ := file.Read(raw)
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw[:n])))
	if err != nil {
		return 0
	}
	return pid
}
