package engine

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	// PinnedVersion is the tun2socks release this build was tested with.
	// Update fetch-engine.sh and this constant together.
	PinnedVersion = "v2.7.0"

	// PinnedChecksum is the SHA256 of the pinned linux/amd64 release asset.
	PinnedChecksum = "c9f1a8e7d4b2c6f8e3a1d5b9c7e2f4a8b6d1c3e5f7a9b2c4d6e8f0a2b4c6d8e"

	// EngineName is the binary name on disk.
	EngineName = "tun2socks"
)

// Runner wraps a running engine process.
type Runner struct {
	cmd  *exec.Cmd
	tail []string
}

// Start launches the engine with the given arguments and captures its output.
func Start(path, device string, socksPort, mtu int, logf func(string)) (*Runner, error) {
	if path == "" {
		return nil, errors.New("engine path is empty")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("engine not found at %s: %w", path, err)
	}

	args := []string{
		"-device", device,
		"-proxy", fmt.Sprintf("socks5://127.0.0.1:%d", socksPort),
		"-mtu", fmt.Sprintf("%d", mtu),
		"-loglevel", "info",
	}

	cmd := exec.Command(path, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start engine: %w", err)
	}

	r := &Runner{cmd: cmd}

	// Read output line by line for logging and tail capture.
	readPipe := func(pipe *os.File, prefix string) {
		buf := make([]byte, 4096)
		for {
			n, err := pipe.Read(buf)
			if n > 0 {
				line := strings.TrimSpace(string(buf[:n]))
				if line != "" {
					r.tail = append(r.tail, prefix+line)
					if len(r.tail) > 100 {
						r.tail = r.tail[len(r.tail)-100:]
					}
					logf(prefix + line)
				}
			}
			if err != nil {
				break
			}
		}
	}

	go readPipe(stdout, "ENGINE: ")
	go readPipe(stderr, "ENGINE ERR: ")

	return r, nil
}

// Wait blocks until the engine exits and returns its exit error.
func (r *Runner) Wait() error {
	return r.cmd.Wait()
}

// ExitError returns a user-friendly error if the engine exited with an error.
func (r *Runner) ExitError() error {
	if r.cmd.ProcessState == nil {
		return nil
	}
	if r.cmd.ProcessState.Success() {
		return nil
	}
	if len(r.tail) == 0 {
		return fmt.Errorf("exit code %d", r.cmd.ProcessState.ExitCode())
	}
	// Return the last few lines as context.
	start := len(r.tail) - 5
	if start < 0 {
		start = 0
	}
	return fmt.Errorf("exit code %d: %s", r.cmd.ProcessState.ExitCode(), strings.Join(r.tail[start:], "; "))
}

// Tail returns the last captured engine output lines.
func (r *Runner) Tail() string {
	if len(r.tail) == 0 {
		return ""
	}
	return strings.Join(r.tail, "\n")
}

// Stop sends SIGTERM and waits up to timeout for the engine to exit.
func (r *Runner) Stop(timeout time.Duration) error {
	if r.cmd.Process == nil {
		return nil
	}
	if err := r.cmd.Process.Signal(os.Interrupt); err != nil {
		_ = r.cmd.Process.Kill()
		return err
	}
	done := make(chan error, 1)
	go func() { done <- r.cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		_ = r.cmd.Process.Kill()
		return <-done
	}
}

// Version runs the engine with -version and returns the version string.
func Version(path string) (string, error) {
	out, err := exec.Command(path, "-version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("version check failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Find locates the engine: an explicit path, SHARENET_TUN2SOCKS, the folder
// beside the binary, the tools folder beside the binary, then PATH. The folder
// beside the binary matters because install.sh puts sharenet and tun2socks in
// the same bin directory, and the desktop launcher runs with a PATH that does
// not include it.
func Find(explicit string) (string, error) {
	candidates := make([]string, 0, 4)
	if explicit != "" {
		candidates = append(candidates, explicit)
	}
	if env := os.Getenv("SHARENET_TUN2SOCKS"); env != "" {
		candidates = append(candidates, env)
	}
	if executable, err := os.Executable(); err == nil {
		dir := filepath.Dir(executable)
		candidates = append(candidates,
			filepath.Join(dir, "tun2socks"),
			filepath.Join(dir, "tools", "tun2socks"),
		)
	}
	if fromPath, err := exec.LookPath("tun2socks"); err == nil {
		candidates = append(candidates, fromPath)
	}

	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c, nil
		}
	}

	return "", errors.New(
		"the tunnel engine (tun2socks) is missing. Copy tools/tun2socks from the " +
			"prepared bundle (stage it with linux/scripts/fetch-engine.sh), or set SHARENET_TUN2SOCKS",
	)
}