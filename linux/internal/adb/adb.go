// Package adb wraps the platform-tools adb executable for the USB transport.
// adb itself is portable, so this package has no build tags.
package adb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/MogaleAlpheus/ShareNet/linux/internal/link"
)

// Device is one line of `adb devices -l`.
type Device struct {
	Serial      string
	State       string
	Model       string
	Description string
}

// Usable reports whether the device is ready for forwarding.
func (d Device) Usable() bool { return strings.EqualFold(d.State, "device") }

// DisplayName renders the device the way the app's messages do.
func (d Device) DisplayName() string {
	name := d.Serial
	if d.Model != "" {
		name = fmt.Sprintf("%s (%s)", d.Model, d.Serial)
	}
	if d.Usable() {
		return name
	}
	return fmt.Sprintf("%s — %s", name, d.State)
}

// Manager runs one adb executable.
type Manager struct {
	path string
}

// New wraps an adb path.
func New(path string) *Manager { return &Manager{path: path} }

// Path is the adb executable in use.
func (m *Manager) Path() string { return m.path }

// Find locates adb: SHARENET_ADB, then a tools folder beside the binary, then
// PATH.
func Find() (string, error) {
	if explicit := os.Getenv("SHARENET_ADB"); explicit != "" {
		if info, err := os.Stat(explicit); err == nil && !info.IsDir() {
			return explicit, nil
		}
	}

	if executable, err := os.Executable(); err == nil {
		dir := filepath.Dir(executable)
		for i := 0; i < 4 && dir != "" && dir != "."; i++ {
			candidate := filepath.Join(dir, "tools", "adb")
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
			dir = filepath.Dir(dir)
		}
	}

	found, err := exec.LookPath("adb")
	if err != nil {
		return "", errors.New(
			"adb was not found. Install Android platform-tools (Fedora: sudo dnf install android-tools) " +
				"or set SHARENET_ADB, and connect through Wi-Fi instead if USB is not needed",
		)
	}
	return found, nil
}

// EnsureServer starts the adb server if it is not already up.
func (m *Manager) EnsureServer(ctx context.Context) error {
	result, err := m.run(ctx, 30*time.Second, "start-server")
	if err != nil {
		return err
	}
	if result.ok() {
		return nil
	}

	detail := result.firstLine()
	if strings.Contains(strings.ToLower(detail), "version") {
		return fmt.Errorf(
			"%s Another adb server (for example from another tool) may be using port 5037. "+
				"Close it or set SHARENET_ADB", detail,
		)
	}
	return fmt.Errorf("adb could not start: %s", detail)
}

// Devices lists the attached Android devices.
func (m *Manager) Devices(ctx context.Context) ([]Device, error) {
	result, err := m.run(ctx, 20*time.Second, "devices", "-l")
	if err != nil {
		return nil, err
	}
	if !result.ok() {
		return nil, fmt.Errorf("adb devices failed: %s", result.firstLine())
	}

	var devices []Device
	for _, raw := range strings.Split(result.stdout, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" ||
			strings.HasPrefix(strings.ToLower(line), "list of devices") ||
			strings.HasPrefix(strings.ToLower(line), "* daemon") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		device := Device{Serial: fields[0], State: fields[1], Description: line}
		for _, field := range fields[2:] {
			if strings.HasPrefix(strings.ToLower(field), "model:") {
				device.Model = strings.ReplaceAll(field[len("model:"):], "_", " ")
			}
		}
		devices = append(devices, device)
	}

	return devices, nil
}

// Forward maps a local TCP port to the phone's link listener.
func (m *Manager) Forward(ctx context.Context, serial string, localPort int, remote string) error {
	result, err := m.run(
		ctx,
		20*time.Second,
		"-s", serial, "forward", fmt.Sprintf("tcp:%d", localPort), remote,
	)
	if err != nil {
		return err
	}
	if !result.ok() {
		return fmt.Errorf("adb forward failed: %s", result.firstLine())
	}
	return nil
}

// RemoveForward removes one forward; failures are ignored.
func (m *Manager) RemoveForward(ctx context.Context, serial string, localPort int) {
	_, _ = m.run(
		ctx,
		10*time.Second,
		"-s", serial, "forward", "--remove", fmt.Sprintf("tcp:%d", localPort),
	)
}

// RemoveStaleForwards drops forwards left behind by a crashed run.
func (m *Manager) RemoveStaleForwards(ctx context.Context) {
	result, err := m.run(ctx, 15*time.Second, "forward", "--list")
	if err != nil || !result.ok() {
		return
	}

	for _, raw := range strings.Split(result.stdout, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if !strings.Contains(strings.ToLower(line), "localabstract:sharenet") &&
			!strings.Contains(line, fmt.Sprintf("tcp:%d", link.UsbFallbackPort)) {
			continue
		}

		fields := regexp.MustCompile(`\s+`).Split(line, -1)
		if len(fields) < 2 {
			continue
		}
		serial, local := fields[0], fields[1]
		if !strings.HasPrefix(strings.ToLower(local), "tcp:") {
			continue
		}
		_, _ = m.run(ctx, 10*time.Second, "-s", serial, "forward", "--remove", local)
	}
}

type output struct {
	exitCode int
	stdout   string
	stderr   string
}

func (o output) ok() bool { return o.exitCode == 0 }

func (o output) firstLine() string {
	text := o.stderr
	if strings.TrimSpace(text) == "" {
		text = o.stdout
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line)
		}
	}
	return "unknown error"
}

func (m *Manager) run(ctx context.Context, timeout time.Duration, args ...string) (output, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, m.path, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := output{stdout: stdout.String(), stderr: stderr.String()}
	if cmd.ProcessState != nil {
		result.exitCode = cmd.ProcessState.ExitCode()
	}

	if err != nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return result, errors.New("adb did not respond in time")
	}
	if err != nil && result.exitCode == 0 {
		return result, fmt.Errorf("could not run adb: %w", err)
	}
	return result, nil
}
