// Package config loads and validates ixr.yaml with environment variable interpolation.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
)

// DefaultPaths is the ordered list of locations searched when no explicit path is given.
var DefaultPaths = []string{"ixr.yaml", "/etc/ixr/ixr.yaml"}

// Load reads the config file at path, expands ${ENV_VAR} references, parses and validates it.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	expanded := os.Expand(string(raw), os.Getenv)

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	applyDefaults(&cfg)
	if err := Validate(&cfg); err != nil {
		return nil, fmt.Errorf("config: validation: %w", err)
	}
	return &cfg, nil
}

// Discover tries each path in DefaultPaths and returns the first Config found.
// Returns nil, nil if no config file exists at any default location.
func Discover() (*Config, error) {
	for _, p := range DefaultPaths {
		if _, err := os.Stat(p); err == nil {
			return Load(p)
		}
	}
	return nil, nil
}

func applyDefaults(c *Config) {
	if c.Server.Port == 0 {
		c.Server.Port = 7000
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.RateLimit.WindowSec == 0 {
		c.RateLimit.WindowSec = 60
	}
}

// Watcher watches a config file for changes and fires onChange on each valid reload.
type Watcher struct {
	path     string
	onChange func(*Config, error)
	fw       *fsnotify.Watcher
	done     chan struct{}
}

// Watch creates a Watcher that calls onChange whenever path is modified.
// The onChange callback is called asynchronously; it must be safe for concurrent use.
// Call Close() to release resources.
func Watch(path string, onChange func(*Config, error)) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("config watcher: %w", err)
	}
	if err := fw.Add(path); err != nil {
		fw.Close()
		return nil, fmt.Errorf("config watcher: watch %s: %w", path, err)
	}
	w := &Watcher{path: path, onChange: onChange, fw: fw, done: make(chan struct{})}
	go w.run()
	return w, nil
}

// Close stops the watcher and releases resources.
func (w *Watcher) Close() error {
	close(w.done)
	return w.fw.Close()
}

func (w *Watcher) run() {
	var debounce *time.Timer
	for {
		select {
		case <-w.done:
			if debounce != nil {
				debounce.Stop()
			}
			return
		case ev, ok := <-w.fw.Events:
			if !ok {
				return
			}
			if ev.Has(fsnotify.Write) || ev.Has(fsnotify.Create) {
				if debounce != nil {
					debounce.Stop()
				}
				debounce = time.AfterFunc(200*time.Millisecond, func() {
					cfg, err := Load(w.path)
					if err != nil {
						slog.Warn("config hot reload failed", "path", w.path, "err", err)
					}
					w.onChange(cfg, err)
				})
			}
		case err, ok := <-w.fw.Errors:
			if !ok {
				return
			}
			slog.Warn("config watcher error", "err", err)
		}
	}
}
