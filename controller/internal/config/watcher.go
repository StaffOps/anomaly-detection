package config

import (
	"log/slog"
	"os"
	"sync"
	"time"
)

// Watcher monitors the config file and reloads on change.
type Watcher struct {
	path     string
	interval time.Duration
	mu       sync.RWMutex
	cfg      *Config
	lastMod  time.Time
	onChange func(*Config)
}

func NewWatcher(path string, initial *Config, onChange func(*Config)) *Watcher {
	info, _ := os.Stat(path)
	var lastMod time.Time
	if info != nil {
		lastMod = info.ModTime()
	}
	return &Watcher{
		path:     path,
		interval: 10 * time.Second,
		cfg:      initial,
		lastMod:  lastMod,
		onChange: onChange,
	}
}

// Current returns the current config (thread-safe).
func (w *Watcher) Current() *Config {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.cfg
}

// Start begins watching in a goroutine. Stops when ctx channel closes.
func (w *Watcher) Start(stop <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				w.check()
			}
		}
	}()
}

func (w *Watcher) check() {
	info, err := os.Stat(w.path)
	if err != nil {
		return
	}
	if !info.ModTime().After(w.lastMod) {
		return
	}

	newCfg, err := Load(w.path)
	if err != nil {
		slog.Error("config reload failed", "error", err)
		return
	}

	w.mu.Lock()
	w.cfg = newCfg
	w.lastMod = info.ModTime()
	w.mu.Unlock()

	slog.Info("config reloaded", "path", w.path)
	if w.onChange != nil {
		w.onChange(newCfg)
	}
}
