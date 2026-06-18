package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewWatcher_Current_ReturnsInitial(t *testing.T) {
	initial := &Config{Cluster: "test"}
	w := NewWatcher("/nonexistent/path.yaml", initial, nil)
	got := w.Current()
	if got != initial {
		t.Error("Current() should return the initial config")
	}
}

func TestNewWatcher_NonexistentFile_NoLastMod(t *testing.T) {
	initial := &Config{}
	w := NewWatcher("/nonexistent/path.yaml", initial, nil)
	if !w.lastMod.IsZero() {
		t.Error("lastMod should be zero for nonexistent file")
	}
}

func TestWatcher_Current_ThreadSafe(t *testing.T) {
	w := NewWatcher("/nonexistent", &Config{}, nil)
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			_ = w.Current()
			done <- struct{}{}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestWatcher_Check_NoChange_NoCallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("cluster: original\n"), 0600); err != nil {
		t.Fatal(err)
	}

	initial, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	called := false
	w := NewWatcher(path, initial, func(_ *Config) { called = true })

	// No time has passed so modtime hasn't changed — check should be a no-op
	w.check()
	if called {
		t.Error("onChange should not be called when file hasn't changed")
	}
	if w.Current().Cluster != initial.Cluster {
		t.Errorf("config should not change: want %q, got %q", initial.Cluster, w.Current().Cluster)
	}
}

func TestWatcher_Check_FileChanged_CallsCallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte("cluster: original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	initial, _ := Load(path)
	w := NewWatcher(path, initial, nil)

	// Sleep to ensure modtime changes
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte("cluster: updated\n"), 0600); err != nil {
		t.Fatal(err)
	}

	newCalled := false
	w.onChange = func(c *Config) { newCalled = true }

	w.check()

	if !newCalled {
		t.Error("onChange should be called when file has changed")
	}
	if w.Current().Cluster != "updated" {
		t.Errorf("config should be updated: want updated, got %q", w.Current().Cluster)
	}
}

func TestWatcher_Check_InvalidConfig_KeepsOld(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte("cluster: original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	initial, _ := Load(path)
	w := NewWatcher(path, initial, nil)

	// Overwrite with invalid YAML (includes a required var that doesn't exist)
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte("url: ${REQUIRED_MISSING_VAR_XYZ789}\n"), 0600); err != nil {
		t.Fatal(err)
	}

	w.check()
	// Config should remain as the old one
	if w.Current().Cluster != initial.Cluster {
		t.Errorf("config should not change on invalid reload: want %q, got %q", initial.Cluster, w.Current().Cluster)
	}
}

func TestWatcher_Check_FileStatError_NoChange(t *testing.T) {
	// Point watcher at a file that doesn't exist
	w := NewWatcher("/nonexistent/file.yaml", &Config{Cluster: "safe"}, nil)
	// Manually set lastMod to something in the past
	w.lastMod = time.Time{}

	// check() should return early on stat error without panic
	w.check()
	if w.Current().Cluster != "safe" {
		t.Error("config should remain unchanged when stat fails")
	}
}

func TestWatcher_Start_Stop(t *testing.T) {
	w := NewWatcher("/nonexistent", &Config{}, nil)
	stop := make(chan struct{})
	w.Start(stop)
	close(stop)
	// Should not hang or panic
	time.Sleep(20 * time.Millisecond)
}
