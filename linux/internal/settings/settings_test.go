package settings_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MogaleAlpheus/ShareNet/linux/internal/settings"
)

func TestDefaultsWhenMissing(t *testing.T) {
	store := settings.Open(t.TempDir())
	current := store.Current()
	if current.Mode != "usb" {
		t.Fatalf("Mode = %q, want usb", current.Mode)
	}
	if current.Password != "" {
		t.Fatalf("Password = %q, want empty", current.Password)
	}
}

func TestRoundTripKeepsPasswordOutOfPlainText(t *testing.T) {
	t.Setenv("SHARENET_NO_KEYRING", "1")
	dir := t.TempDir()

	store := settings.Open(dir)
	if err := store.Update(func(s *settings.Settings) {
		s.Mode = "wifi"
		s.WifiHost = " 192.168.43.1 "
		s.AdapterName = "wlp2s0"
		s.LastSerial = "R58M"
		s.Hotspot = true
		s.Password = "7k4p-9q2m"
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if contains(string(raw), "7k4p-9q2m") {
		t.Fatalf("the password was written in clear text: %s", raw)
	}

	info, err := os.Stat(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("settings file mode = %v, want 0600", info.Mode().Perm())
	}

	reloaded := settings.Open(dir).Current()
	if reloaded.Mode != "wifi" || reloaded.WifiHost != "192.168.43.1" || !reloaded.Hotspot {
		t.Fatalf("reloaded %+v", reloaded)
	}
	if reloaded.Password != "7k4p-9q2m" {
		t.Fatalf("reloaded password = %q", reloaded.Password)
	}
	if reloaded.LastSerial != "R58M" {
		t.Fatalf("reloaded serial = %q", reloaded.LastSerial)
	}
}

func TestDamagedPasswordIsForgottenNotFatal(t *testing.T) {
	t.Setenv("SHARENET_NO_KEYRING", "1")
	dir := t.TempDir()

	store := settings.Open(dir)
	if err := store.Update(func(s *settings.Settings) { s.Password = "abcd" }); err != nil {
		t.Fatal(err)
	}

	// Corrupt the stored reference the way a hand-edited file would.
	if err := os.WriteFile(
		filepath.Join(dir, "settings.json"),
		[]byte(`{"mode":"usb","passwordRef":"local:@@@"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	if password := settings.Open(dir).Current().Password; password != "" {
		t.Fatalf("a damaged reference produced %q", password)
	}
}

func TestUnknownModeFallsBackToUSB(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "settings.json"),
		[]byte(`{"mode":"telepathy"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	if mode := settings.Open(dir).Current().Mode; mode != "usb" {
		t.Fatalf("mode = %q, want usb", mode)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
