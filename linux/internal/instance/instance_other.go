//go:build !linux

package instance

// Lock is a no-op off Linux, where only tests and cross-builds happen.
type Lock struct{}

// Acquire never blocks off Linux.
func Acquire(name string) (*Lock, error) { return &Lock{}, nil }

// Release does nothing.
func (l *Lock) Release() {}

// Path is always empty.
func (l *Lock) Path() string { return "" }
