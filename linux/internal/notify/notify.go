// Package notify shows a desktop notification for connect and failure events.
// It is the Linux equivalent of the Windows tray balloons: the window can be
// closed or behind a terminal, so the state change has to reach the user some
// other way. Failures are silent, never fatal.
package notify

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/MogaleAlpheus/ShareNet/linux/internal/session"
)

// Available reports whether a notification daemon command exists.
func Available() bool {
	_, err := exec.LookPath("notify-send")
	return err == nil
}

// Send shows a notification. urgent maps to notify-send's critical urgency.
func Send(title, body string, urgent bool) {
	if !Available() {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "notify-send", args(urgent, title, body)...)
	_ = cmd.Run()
}

// SendAs delivers the notification through the desktop user's session when the
// client runs elevated; root has no bus of its own to show it on.
func SendAs(invoker *session.User, title, body string, urgent bool) {
	if invoker == nil {
		Send(title, body, urgent)
		return
	}
	if err := session.RunAs(invoker, "notify-send", args(urgent, title, body)...); err == nil {
		return
	}
	Send(title, body, urgent)
}

func args(urgent bool, title, body string) []string {
	urgency := "normal"
	if urgent {
		urgency = "critical"
	}
	return []string{
		"--app-name", "ShareNet",
		"--urgency", urgency,
		"--icon", "network-vpn",
		strings.TrimSpace(title),
		strings.TrimSpace(body),
	}
}
