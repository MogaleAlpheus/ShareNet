// Package engine runs the tun2socks engine that owns the TUN device and turns
// IP packets into SOCKS5 requests for the loopback bridge.
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

// PinnedVersion is the tun2socks release scripts/fetch-engine.sh downloads.
const PinnedVersion = "v2.7.0"

// Runner is one tun2socks child process.
type Runner struct {
	cmd    *exec.Cmd
	done   chan struct{}
	err    error
	mu     sync.Mutex
	exited bool
	tail   []string
}

// Find locates the engine: an explicit path, SHARENET_TUN2SOCKS, the tools
// folder beside the binary, then PATH.
func Find(explicit string) (string, error) {
	candidates := make([]string, 0, 4)
	if explicit != "" {
		candidates = append(candidates, explicit)
	}
	if fromEnv := os.Getenv("SHARENET_TUN2SOCKS"); fromEnv != "" {
		candidates = append(candidates, fromEnv)
	}
	if executable, err := os.Executable(); err == nil {
		dir := filepath.Dir(executable)
		candidates = append(candidates, filepath.Join(dir, "tools", "tun2socks"))
	}
	if fromPath, err := exec.LookPath("tun2socks"); err == nil {
		candidates = append(candidates, fromPath)
	}

	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	return "", errors.New(
		"the tunnel engine (tun2socks) is missing. Run linux/scripts/fetch-engine.sh, " +
			"install it from https://github.com/xjasonlyu/tun2socks/releases, or set SHARENET_TUN2SOCKS",
	)
}

var versionPattern = regexp.MustCompile(`v?(\d+\.\d+\.\d+)`)

// Version runs the engine's --version and returns the release it reports, in
// the same "vX.Y.Z" form as PinnedVersion. A binary that prints no version, or
// cannot be run at all, returns an error.
func Version(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	output, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		if text == "" {
			return "", fmt.Errorf("could not run %s --version: %w", path, err)
		}
		return "", fmt.Errorf("%s --version failed: %v (%s)", path, err, text)
	}

	version := parseVersion(text)
	if version == "" {
		return "", fmt.Errorf("%s did not report a tun2socks version: %s", path, text)
	}
	return version, nil
}

func parseVersion(text string) string {
	match := versionPattern.FindStringSubmatch(text)
	if match == nil {
		return ""
	}
	return "v" + match[1]
}

// Start launches the engine against the loopback SOCKS bridge.
func Start(path, device string, socksPort, mtu int, logf func(string)) (*Runner, error) {
	if mtu < 576 || mtu > 1500 {
		return nil, fmt.Errorf("the tunnel MTU must be between 576 and 1500 (got %d)", mtu)
	}

	cmd := exec.Command(path, arguments(device, socksPort, mtu)...)
	cmd.Stdout = nil
	cmd.Stderr = nil

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("could not attach to the tunnel engine: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("could not attach to the tunnel engine: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("could not start the tunnel engine: %w", err)
	}

	runner := &Runner{cmd: cmd, done: make(chan struct{})}
	forwardLine := func(line string) {
		runner.record(line)
		logf("tun2socks: " + line)
	}
	go forward(stdout, forwardLine)
	go forward(stderr, forwardLine)
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

func forward(input io.Reader, logf func(string)) {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			logf(line)
		}
	}
	_, _ = io.Copy(io.Discard, input)
}

func (r *Runner) record(line string) {
	r.mu.Lock()
	r.tail = append(r.tail, line)
	if len(r.tail) > 8 {
		r.tail = r.tail[len(r.tail)-8:]
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

// Stop terminates the engine and waits up to timeout for it to exit.
func (r *Runner) Stop(timeout time.Duration) {
	if r == nil {
		return
	}

	select {
	case <-r.done:
		return
	default:
	}

	_ = r.cmd.Process.Kill()
	select {
	case <-r.done:
	case <-time.After(timeout):
	}
}
