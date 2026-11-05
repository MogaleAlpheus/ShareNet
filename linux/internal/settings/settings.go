// Package settings persists the choices the Windows app keeps between runs:
// transport, phone address, adapter, last serial, the hotspot preference and the
// link password. The password never lands in the JSON in readable form; it goes
// to the desktop keyring when one is available.
package settings

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Settings mirrors AppSettings in the Windows app.
type Settings struct {
	// Mode is "usb" or "wifi".
	Mode string `json:"mode"`
	// WifiHost is the last phone address used over Wi-Fi.
	WifiHost string `json:"wifiHost,omitempty"`
	// AdapterName is the interface used for Wi-Fi discovery.
	AdapterName string `json:"adapterName,omitempty"`
	// LastSerial is the adb serial last connected over USB.
	LastSerial string `json:"lastSerial,omitempty"`
	// Hotspot asks the client to join the phone's own access point.
	Hotspot bool `json:"hotspot,omitempty"`
	// PasswordRef is either "keyring" or "local:<base64>"; see secret.go.
	PasswordRef string `json:"passwordRef,omitempty"`

	// Password is only held in memory and is never serialized.
	Password string `json:"-"`
}

// Store is a loaded settings file guarded for concurrent UI access.
type Store struct {
	mu   sync.Mutex
	path string
	data Settings
}

// Load reads the settings, falling back to defaults on any problem: settings are
// a convenience and must never stop the app from starting.
func Load() *Store {
	return Open("")
}

// Open loads settings from dir, or from the standard location when dir is empty.
// It exists so tests can use a temporary directory.
func Open(dir string) *Store {
	path := ""
	if dir != "" {
		path = filepath.Join(dir, "settings.json")
	} else if standard, err := defaultPath(); err == nil {
		path = standard
	}

	store := &Store{path: path, data: defaults()}
	if path == "" {
		return store
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return store
	}

	var loaded Settings
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return store
	}

	loaded.Mode = normalizeMode(loaded.Mode)
	loaded.Password = Reveal(loaded.PasswordRef)
	store.data = loaded
	return store
}

// Current returns a copy of the settings, including the readable password.
func (s *Store) Current() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data
}

// Update applies fn and saves the result. The password is written through the
// keyring when possible.
func (s *Store) Update(fn func(*Settings)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.data
	fn(&next)
	next.Mode = normalizeMode(next.Mode)
	next.WifiHost = strings.TrimSpace(next.WifiHost)
	next.AdapterName = strings.TrimSpace(next.AdapterName)
	next.LastSerial = strings.TrimSpace(next.LastSerial)

	next.PasswordRef = Protect(next.Password)
	s.data = next
	return s.saveLocked(next)
}

// Path is the settings file in use, empty when none could be resolved.
func (s *Store) Path() string { return s.path }

func (s *Store) saveLocked(data Settings) error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}

	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')

	// Write to a sibling and rename, so a crash cannot leave half a file.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, s.path)
}

func defaults() Settings {
	// Hotspot join defaults to on, like the Windows app: with a password and no
	// typed address, Connect tries the phone's own network first.
	return Settings{Mode: "usb", Hotspot: true}
}

func normalizeMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "wifi", "wi-fi":
		return "wifi"
	default:
		return "usb"
	}
}

func defaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}

	// On Windows the filesystem is case-insensitive, so "sharenet" would be the
	// WPF app's own folder during development and would clobber its settings.
	name := "sharenet"
	if runtime.GOOS == "windows" {
		name = "sharenet-linux"
	}
	return filepath.Join(dir, name, "settings.json"), nil
}

// protectLocal is the fallback for machines without a keyring: the password is
// kept in a 0600 file, encoded only so it is not readable at a glance.
func protectLocal(password string) string {
	return "local:" + base64.StdEncoding.EncodeToString([]byte(password))
}

func revealLocal(ref string) (string, error) {
	encoded := strings.TrimPrefix(ref, "local:")
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", errors.New("the saved password is damaged")
	}
	return string(raw), nil
}
