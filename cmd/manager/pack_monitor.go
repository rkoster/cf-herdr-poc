package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"time"

	"cf-herdr-poc/internal/supervisor"
)

func monitorPackState(ctx context.Context, path string, collie supervisor.Collie, pollInterval, debounce time.Duration, report func(error)) {
	last := packStateFingerprint(path)
	var pending string
	var changedAt time.Time
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current := packStateFingerprint(path)
			if current == "" || current == last {
				continue
			}
			if pending != current {
				pending, changedAt = current, time.Now()
				continue
			}
			if time.Since(changedAt) < debounce {
				continue
			}
			if err := collie.Restart(ctx); err != nil {
				report(fmt.Errorf("restart Collie after Pack state change: %w", err))
				continue
			}
			if err := collie.Ready(ctx); err != nil {
				report(fmt.Errorf("wait for Collie after Pack state change: %w", err))
				continue
			}
			last, pending = current, ""
		}
	}
}

func packStateFingerprint(path string) string {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(contents)
	return string(digest[:])
}
