package stats

import (
	"testing"
	"time"
)

func TestPeaksAndHistoryCap(t *testing.T) {
	saved := sampleInterval
	sampleInterval = 0
	t.Cleanup(func() { sampleInterval = saved })

	counter := New()
	counter.StartSession()

	for i := 0; i < 400; i++ {
		counter.AddDownload(1000)
		counter.AddUpload(400)
		counter.Sample()
		time.Sleep(100 * time.Microsecond)
	}

	snapshot := counter.Snapshot()
	if len(snapshot.History) != HistoryLength {
		t.Fatalf("history holds %d points, want %d", len(snapshot.History), HistoryLength)
	}
	if snapshot.PeakDownload <= 0 || snapshot.PeakUpload <= 0 {
		t.Fatalf("peaks not recorded: %+v", snapshot)
	}
	sampled := false
	for _, point := range snapshot.History {
		if point.Download > 0 || point.Upload > 0 {
			sampled = true
			break
		}
	}
	if !sampled {
		t.Fatal("every history point is empty")
	}

	counter.StopSession()
	if after := counter.Snapshot(); len(after.History) != HistoryLength {
		t.Fatalf("StopSession dropped the history: %d", len(after.History))
	}
}

func TestStartSessionClearsPeaksAndHistory(t *testing.T) {
	saved := sampleInterval
	sampleInterval = 0
	t.Cleanup(func() { sampleInterval = saved })

	counter := New()
	counter.StartSession()
	counter.AddDownload(2048)
	for i := 0; i < 50 && len(counter.Snapshot().History) == 0; i++ {
		counter.Sample()
		time.Sleep(100 * time.Microsecond)
	}

	if len(counter.Snapshot().History) == 0 {
		t.Fatal("no history after one sample")
	}

	counter.StartSession()
	snapshot := counter.Snapshot()
	if len(snapshot.History) != 0 || snapshot.PeakDownload != 0 || snapshot.Download != 0 {
		t.Fatalf("a new session kept old data: %+v", snapshot)
	}
}
