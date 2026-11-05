//go:build !linux

package session

import "errors"

// User is the desktop account that started the elevated process.
type User struct {
	Name string
	UID  int
	Home string
}

// Invoker is always nil off Linux.
func Invoker() *User { return nil }

// RunAs is unavailable off Linux.
func RunAs(invoker *User, name string, args ...string) error {
	return errors.New("running in another user's session is only supported on Linux")
}
