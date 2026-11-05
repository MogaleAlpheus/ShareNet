// Package stats counts the bytes the SOCKS bridge relays, exactly like the
// Windows and Android apps: per session, never truncated, sampled by the UI.
package stats

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// HistoryLength is how many samples the sparkline keeps: two per second for
// one minute, exactly like the Windows sparkline.
const HistoryLength = 120

// Point is one rate sample for the sparkline.
type Point struct {
	Download float64 `json:"d"`
	Upload   float64 `json:"u"`
}

// Snapshot is a point-in-time view of one tunnel session.
type Snapshot struct {
	Download     int64
	Upload       int64
	Started      time.Time
	Duration     time.Duration
	DownloadRate float64
	UploadRate   float64
	PeakDownload float64
	PeakUpload   float64
	History      []Point
}

// sampleInterval is the shortest gap between rate samples. The controller
// samples every second; tests lower it.
var sampleInterval = 50 * time.Millisecond

// Stats holds the live counters for the current tunnel session.
type Stats struct {
	download atomic.Int64
	upload   atomic.Int64

	mu       sync.Mutex
	started  time.Time
	ended    time.Time
	active   bool
	lastDown int64
	lastUp   int64
	lastAt   time.Time
	downRate float64
	upRate   float64
	peakDown float64
	peakUp   float64
	history  []Point
}

// New creates an idle counter set.
func New() *Stats { return &Stats{} }

// StartSession zeroes the counters and starts the clock.
func (s *Stats) StartSession() {
	s.download.Store(0)
	s.upload.Store(0)

	s.mu.Lock()
	s.started = time.Now()
	s.ended = time.Time{}
	s.active = true
	s.lastDown = 0
	s.lastUp = 0
	s.lastAt = s.started
	s.downRate = 0
	s.upRate = 0
	s.peakDown = 0
	s.peakUp = 0
	s.history = s.history[:0]
	s.mu.Unlock()
}

// StopSession freezes the clock; counters keep their totals.
func (s *Stats) StopSession() {
	s.mu.Lock()
	if s.active {
		s.ended = time.Now()
		s.active = false
		s.downRate = 0
		s.upRate = 0
	}
	s.mu.Unlock()
}

// Sample updates the rate estimate; call it on a timer while connected.
func (s *Stats) Sample() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return
	}

	now := time.Now()
	elapsed := now.Sub(s.lastAt).Seconds()
	if elapsed <= 0 || elapsed < sampleInterval.Seconds() {
		return
	}

	down := s.download.Load()
	up := s.upload.Load()
	s.downRate = float64(down-s.lastDown) / elapsed
	s.upRate = float64(up-s.lastUp) / elapsed
	s.lastDown = down
	s.lastUp = up
	s.lastAt = now

	if s.downRate > s.peakDown {
		s.peakDown = s.downRate
	}
	if s.upRate > s.peakUp {
		s.peakUp = s.upRate
	}

	s.history = append(s.history, Point{Download: s.downRate, Upload: s.upRate})
	if len(s.history) > HistoryLength {
		s.history = append([]Point(nil), s.history[len(s.history)-HistoryLength:]...)
	}
}

// AddDownload counts bytes received from the phone.
func (s *Stats) AddDownload(n int) {
	if n > 0 {
		s.download.Add(int64(n))
	}
}

// AddUpload counts bytes sent to the phone.
func (s *Stats) AddUpload(n int) {
	if n > 0 {
		s.upload.Add(int64(n))
	}
}

// Snapshot returns the current totals and rates.
func (s *Stats) Snapshot() Snapshot {
	s.mu.Lock()
	started := s.started
	ended := s.ended
	downRate := s.downRate
	upRate := s.upRate
	peakDown := s.peakDown
	peakUp := s.peakUp
	history := append([]Point(nil), s.history...)
	s.mu.Unlock()

	if ended.IsZero() {
		ended = time.Now()
	}

	snap := Snapshot{
		Download:     s.download.Load(),
		Upload:       s.upload.Load(),
		Started:      started,
		DownloadRate: downRate,
		UploadRate:   upRate,
		PeakDownload: peakDown,
		PeakUpload:   peakUp,
		History:      history,
	}
	if !started.IsZero() {
		snap.Duration = ended.Sub(started)
	}
	return snap
}

// FormatBytes renders a byte count the way the README examples do.
func FormatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}

	value := float64(n)
	units := []string{"KB", "MB", "GB", "TB"}
	for _, suffix := range units {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f PB", value/unit)
}

// FormatRate renders bytes per second.
func FormatRate(bytesPerSecond float64) string {
	if bytesPerSecond < 0 {
		bytesPerSecond = 0
	}
	return FormatBytes(int64(bytesPerSecond)) + "/s"
}
