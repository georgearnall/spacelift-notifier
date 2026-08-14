package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestObserve_ColdStartSuppressesAll(t *testing.T) {
	s := New()
	now := time.Now()

	newly := s.Observe([]string{"r1", "r2"}, now)
	if len(newly) != 0 {
		t.Errorf("cold start Observe() = %v, want no newly pending runs", newly)
	}
	if len(s.Runs) != 2 {
		t.Errorf("Runs = %v, want both r1 and r2 recorded", s.Runs)
	}
	if s.LastPoll != now {
		t.Errorf("LastPoll = %v, want %v", s.LastPoll, now)
	}
}

func TestObserve_NewRunAfterWarmStartNotifies(t *testing.T) {
	s := New()
	t0 := time.Now()
	s.Observe([]string{"r1"}, t0) // cold start, establishes LastPoll

	t1 := t0.Add(time.Minute)
	newly := s.Observe([]string{"r1", "r2"}, t1)
	if len(newly) != 1 || newly[0] != "r2" {
		t.Errorf("Observe() = %v, want only r2 (newly pending)", newly)
	}
}

func TestObserve_KnownRunNeverRenotifies(t *testing.T) {
	s := New()
	t0 := time.Now()
	s.Observe([]string{"r1"}, t0)
	s.Observe([]string{"r1"}, t0.Add(time.Minute)) // r1 already known, still pending

	newly := s.Observe([]string{"r1"}, t0.Add(2*time.Minute))
	if len(newly) != 0 {
		t.Errorf("Observe() = %v, want no notifications for an already-known run", newly)
	}
}

func TestObserve_PrunesRunsNoLongerPending(t *testing.T) {
	s := New()
	t0 := time.Now()
	s.Observe([]string{"r1", "r2"}, t0)

	// r1 got confirmed elsewhere; only r2 is still pending.
	s.Observe([]string{"r2"}, t0.Add(time.Minute))
	if _, stillThere := s.Runs["r1"]; stillThere {
		t.Errorf("Runs still contains r1 after it dropped out of the pending set")
	}
	if len(s.Runs) != 1 {
		t.Errorf("Runs = %v, want exactly {r2}", s.Runs)
	}

	// If r1's ID reappears later (a genuinely new event, since run IDs
	// are never reused), it should be treated as newly pending again.
	newly := s.Observe([]string{"r1", "r2"}, t0.Add(2*time.Minute))
	if len(newly) != 1 || newly[0] != "r1" {
		t.Errorf("Observe() after r1 reappeared = %v, want [r1]", newly)
	}
}

func TestLoad_MissingFileReturnsEmptyState(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	s, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if s.Runs == nil || len(s.Runs) != 0 {
		t.Errorf("Load() on missing file = %+v, want empty Runs map", s)
	}
	if !s.LastPoll.IsZero() {
		t.Errorf("Load() on missing file has non-zero LastPoll")
	}
}

func TestSaveLoad_Roundtrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	s := New()
	now := time.Now().Truncate(time.Second) // JSON round-trips to second precision cleanly
	s.Observe([]string{"r1", "r2"}, now)

	if err := s.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(got.Runs) != 2 {
		t.Fatalf("Load() Runs = %v, want 2 entries", got.Runs)
	}
	if !got.LastPoll.Equal(now) {
		t.Errorf("Load() LastPoll = %v, want %v", got.LastPoll, now)
	}
	if rec, ok := got.Runs["r1"]; !ok || !rec.Seen.Equal(now) {
		t.Errorf("Load() Runs[r1] = %+v, want Seen=%v", rec, now)
	}
}

func TestLoad_CorruptFileReturnsError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	confDir := filepath.Join(dir, "spacelift-notifier")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "state.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(); err == nil {
		t.Fatal("Load() on a corrupt state file: got nil error, want one")
	}
}

func TestSave_WritesAtomically(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	s := New()
	s.Observe([]string{"r1"}, time.Now())
	if err := s.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// No leftover temp files should remain after a successful save.
	matches, err := filepath.Glob(filepath.Join(dir, "spacelift-notifier", "state-*.json.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("leftover temp files after Save(): %v", matches)
	}
}
