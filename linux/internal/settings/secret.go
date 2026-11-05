package settings

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// label and attributes identify the keyring entry, matching secret-tool's CLI.
const (
	keyringLabel = "ShareNet phone password"
	keyringAttr  = "service"
	keyringValue = "sharenet"
	keyringKey   = "account"
	keyringPhone = "phone"
)

// Protect turns a password into the value stored in settings.json. The desktop
// keyring is used when available; otherwise the 0600 settings file holds an
// encoded copy, which is the closest Linux equivalent of DPAPI.
func Protect(password string) string {
	if password == "" {
		return ""
	}

	if keyringAvailable() {
		if err := runSecret("store", password); err == nil {
			return "keyring"
		}
	}
	return protectLocal(password)
}

// Reveal returns the readable password for a stored reference.
func Reveal(ref string) string {
	switch {
	case ref == "":
		return ""
	case ref == "keyring":
		password, err := lookupSecret()
		if err != nil {
			return ""
		}
		return password
	case strings.HasPrefix(ref, "local:"):
		password, err := revealLocal(ref)
		if err != nil {
			return ""
		}
		return password
	default:
		return ""
	}
}

func keyringAvailable() bool {
	if os.Getenv("SHARENET_NO_KEYRING") == "1" {
		return false
	}
	_, err := exec.LookPath("secret-tool")
	return err == nil
}

func runSecret(command, password string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	args := []string{
		command,
		keyringAttr, keyringValue,
		keyringKey, keyringPhone,
	}
	if command == "store" {
		args = append([]string{"store", "--label", keyringLabel}, args[1:]...)
	}

	cmd := exec.CommandContext(ctx, "secret-tool", args...)
	cmd.Stdin = strings.NewReader(password)
	return cmd.Run()
}

func lookupSecret() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "secret-tool", "lookup",
		keyringAttr, keyringValue, keyringKey, keyringPhone)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}
