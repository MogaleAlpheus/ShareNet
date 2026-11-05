//go:build linux

// Package session finds the desktop user behind an elevated launch, so the
// client can open a browser and post notifications where the user will see
// them instead of in root's empty session.
package session

import (
	"os"
	"os/exec"
	"os/user"
	"strconv"
)

// User is the desktop account that started the elevated process.
type User struct {
	Name string
	UID  int
	Home string
}

// Invoker returns the user behind sudo or pkexec, or nil when the client is not
// running elevated on their behalf.
func Invoker() *User {
	if os.Geteuid() != 0 {
		return nil
	}

	if name := os.Getenv("SUDO_USER"); name != "" && name != "root" {
		if resolved, err := user.Lookup(name); err == nil {
			if uid, err := strconv.Atoi(resolved.Uid); err == nil {
				return &User{Name: resolved.Username, UID: uid, Home: resolved.HomeDir}
			}
		}
	}

	if raw := os.Getenv("PKEXEC_UID"); raw != "" {
		if resolved, err := user.LookupId(raw); err == nil {
			if uid, err := strconv.Atoi(resolved.Uid); err == nil {
				return &User{Name: resolved.Username, UID: uid, Home: resolved.HomeDir}
			}
		}
	}

	return nil
}

// RunAs runs a command in the invoker's session: their bus, display and runtime
// directory, so the browser and notifications are theirs rather than root's.
func RunAs(invoker *User, name string, args ...string) error {
	commandArgs := append([]string{"-u", invoker.Name, "--", "env"}, sessionEnv(invoker)...)
	commandArgs = append(commandArgs, name)
	commandArgs = append(commandArgs, args...)
	return exec.Command("runuser", commandArgs...).Start()
}

func sessionEnv(invoker *User) []string {
	env := []string{
		"XDG_RUNTIME_DIR=/run/user/" + strconv.Itoa(invoker.UID),
		"HOME=" + invoker.Home,
		"USER=" + invoker.Name,
		"LOGNAME=" + invoker.Name,
	}
	if bus := os.Getenv("DBUS_SESSION_BUS_ADDRESS"); bus != "" {
		env = append(env, "DBUS_SESSION_BUS_ADDRESS="+bus)
	} else {
		env = append(env,
			"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/"+strconv.Itoa(invoker.UID)+"/bus",
		)
	}
	for _, key := range []string{"DISPLAY", "WAYLAND_DISPLAY", "XAUTHORITY", "XDG_SESSION_TYPE"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}

	// pkexec keeps DISPLAY but not always WAYLAND_DISPLAY; without it a
	// Wayland desktop has no way to show the browser.
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		runtimeDir := "/run/user/" + strconv.Itoa(invoker.UID)
		if _, err := os.Stat(runtimeDir + "/wayland-0"); err == nil {
			env = append(env, "WAYLAND_DISPLAY=wayland-0")
		}
	}

	return env
}
