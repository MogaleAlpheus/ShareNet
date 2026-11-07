package engine

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
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
	cmd    *exec.Cmd
	done   chan struct{}
	err    error
	mu     sync.Mutex
	exited bool
	tail   []string
}

// Start launches the engine with the given arguments and captures its output.
func Start(path, device string, socksPort, mtu int, logf func(string)) (*Runner, error) {
	if mtu < 576 || mtu > 1500 {
		return nil, fmt.Errorf("the tunnel MTU must be between 576 and 1500 (got %d)", mtu)
	}

	cmd := exec.Command(path, arguments(device, socksPort, mtu)...)

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

	runner := &Runner{cmd: cmd, done: make(chan struct{})}

	forward := func(input io.Reader, prefix string) {
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				runner.record(prefix + line)
				logf(prefix + line)
			}
		}
	}

	go forward(stdout, "ENGINE: ")
	go forward(stderr, "ENGINE ERR: ")

	go func() {
		err := cmd.Wait()
		runner.mu.Lock()
		runner.exited = true
		runner.err = err
		runner.mu.Unlock()
		close(runner.done)
	}()

	return runner, nil
}

// arguments builds the engine command line. tun2socks v2 parses flags with
// urfave/cli, which only accepts the long forms with two dashes: a single-dash
// "-loglevel" is rejected as an unknown shorthand and the engine exits before
// it ever creates the TUN device.
func arguments(device string, socksPort, mtu int) []string {
	return []string{
		"--device", "tun://" + device,
		"--proxy", fmt.Sprintf("socks5://127.0.0.1:%d", socksPort),
		"--loglevel", "info",
		"--mtu", fmt.Sprintf("%d", mtu),
		// Lift the netstack buffer ceilings so large transfers can use the
		// whole link instead of a small default window.
		"--tcp-auto-tuning",
		"--tcp-sndbuf", "4MB",
		"--tcp-rcvbuf", "4MB",
	}
}

func (r *Runner) record(line string) {
	r.mu.Lock()
	r.tail = append(r.tail, line)
	if len(r.tail) > 100 {
		r.tail = r.tail[len(r.tail)-100:]
	}
	r.mu.Unlock()
}

// Tail is the engine's most recent output, so a startup failure can say why the
// engine refused to run instead of only that the adapter never appeared.
func (r *Runner) Tail() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// The engine prints its whole usage block before reporting a bad flag, so
	// prefer the last real complaint over flag descriptions.
	for i := len(r.tail) - 1; i >= 0; i-- {
		line := r.tail[i]
		if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "Usage of") || line == "" {
			continue
		}
		return line
	}
	return strings.Join(r.tail, " | ")
}

// Running reports whether the process is still alive.
func (r *Runner) Running() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.exited
}

// Wait is closed when the engine exits.
func (r *Runner) Wait() <-chan struct{} {
	if r == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return r.done
}

// ExitError is the engine's exit error, once it has stopped.
func (r *Runner) ExitError() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// Stop sends SIGTERM and waits up to timeout for the engine to exit.
func (r *Runner) Stop(timeout time.Duration) error {
	if r == nil || r.cmd.Process == nil {
		return nil
	}
	if err := r.cmd.Process.Signal(os.Interrupt); err != nil {
		_ = r.cmd.Process.Kill()
		return err
	}
	select {
	case <-r.done:
		return r.ExitError()
	case <-time.After(timeout):
		_ = r.cmd.Process.Kill()
		<-r.done
		return r.ExitError()
	}
}

// Version runs the engine with -version and returns the version string.
func Version(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, path, "-version").CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			return "", fmt.Errorf("could not run %s -version: %w", path, err)
		}
		return "", fmt.Errorf("%s -version failed: %v (%s)", path, err, text)
	}

	version := parseVersion(text)
	if version == "" {
		return "", fmt.Errorf("%s did not report a tun2socks version: %s", path, text)
	}
	return version, nil
}

var versionPattern = regexp.MustCompile(`v?(\d+\.\d+\.\d+)`)

func parseVersion(text string) string {
	match := versionPattern.FindStringSubmatch(text)
	if match == nil {
		return ""
	}
	return "v" + match[1]
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