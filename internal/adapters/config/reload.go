package config

import (
	"context"
	"os"
	"time"
)

// Watch polls path and emits a config when the file changes. It deliberately
// uses polling so ixr keeps minimal dependencies.
func Watch(ctx context.Context, path string, interval time.Duration, out chan<- *Config) error {
	if interval <= 0 {
		interval = time.Second
	}
	var lastMod time.Time
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			if !info.ModTime().After(lastMod) {
				continue
			}
			cfg, err := Load(path)
			if err != nil {
				continue
			}
			lastMod = info.ModTime()
			select {
			case out <- cfg:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
