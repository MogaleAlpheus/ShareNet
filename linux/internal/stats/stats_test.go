package stats_test

import (
	"testing"
	"time"

	"github.com/MogaleAlpheus/ShareNet/linux/internal/stats"
)

func TestCountersAndSnapshot(t *testing.T) {
	counter := stats.New()
	counter.StartSession()

	counter.AddDownload(1500)
	counter.AddUpload(500)
	counter.AddDownload(-10) // ignored

	snapshot := counter.Snapshot()
	if snapshot.Download != 1500 {
		t.Errorf("Download = %d, want 1500", snapshot.Download)
	}
	if snapshot.Upload != 500 {
		t.Errorf("Upload = %d, want 500", snapshot.Upload)
	}
	if snapshot.Duration < 0 {
		t.Errorf("Duration = %v, want >= 0", snapshot.Duration)
	}
}

func TestStopSessionFreezesDuration(t *testing.T) {
	counter := stats.New()
	counter.StartSession()
	time.Sleep(10 * time.Millisecond)
	counter.StopSession()

	first := counter.Snapshot()
	time.Sleep(10 * time.Millisecond)
	second := counter.Snapshot()

	if second.Duration != first.Duration {
		t.Errorf("duration moved after StopSession: %v then %v", first.Duration, second.Duration)
	}
	if second.DownloadRate != 0 || second.UploadRate != 0 {
		t.Errorf("rates should be zero when stopped, got %v / %v", second.DownloadRate, second.UploadRate)
	}
}

func TestFormatBytes(t *testing.T) {
	cases := map[int64]string{
		0:               "0 B",
		999:             "999 B",
		1024:            "1.0 KB",
		1536:            "1.5 KB",
		1024 * 1024:     "1.0 MB",
		3 * 1024 * 1024: "3.0 MB",
	}

	for bytes, want := range cases {
		if got := stats.FormatBytes(bytes); got != want {
			t.Errorf("FormatBytes(%d) = %q, want %q", bytes, got, want)
		}
	}

	if got := stats.FormatRate(2048); got != "2.0 KB/s" {
		t.Errorf("FormatRate(2048) = %q, want %q", got, "2.0 KB/s")
	}
}
