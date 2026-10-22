// Package updatenotice checks public releases and throttles optional update notices.
package updatenotice

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	checkInterval  = 24 * time.Hour
	noticeInterval = 7 * 24 * time.Hour
)

// A blocked filesystem must not accumulate workers in an embedding process.
var workerSlot = make(chan struct{}, 1)

// Check returns one plain notice, or an empty string when optional work fails.
// The caller must restrict this to eligible interactive bare invocations.
// Check records attempts before HTTP requests and notices before returning them.
func Check(ctx context.Context, currentVersion string) string {
	current, valid := stableVersion(currentVersion)
	if !valid || ctx.Err() != nil {
		return ""
	}
	directory, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return boundedCheck(ctx, func(ctx context.Context) string {
		client := releaseClient()
		defer client.CloseIdleConnections()
		return check(ctx, current, filepath.Join(directory, "openai-cli", "update-notice"), time.Now(), client, installationGuidance)
	})
}

func boundedCheck(ctx context.Context, work func(context.Context) string) string {
	if ctx.Err() != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	select {
	case workerSlot <- struct{}{}:
	default:
		return ""
	}
	result := make(chan string, 1)
	go func() {
		defer func() { <-workerSlot }()
		result <- work(ctx)
	}()
	select {
	case text := <-result:
		if ctx.Err() == nil {
			return text
		}
	case <-ctx.Done():
	}
	return ""
}

func check(ctx context.Context, current, directory string, now time.Time, client *http.Client, guidance func(string) string) string {
	current, valid := stableVersion(current)
	if !valid || ctx.Err() != nil || now.Unix() <= 0 {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	cache, err := openCache(ctx, directory)
	if err != nil {
		return ""
	}
	defer cache.close()
	state, err := cache.load(now)
	if err != nil || ctx.Err() != nil {
		return ""
	}
	if state.Checked == 0 || now.Sub(time.Unix(state.Checked, 0)) >= checkInterval {
		// Reserve failed attempts too. A failed cache write skips the request.
		state.Checked, state.Latest = now.Unix(), ""
		if cache.save(state) != nil || ctx.Err() != nil {
			return ""
		}
		state.Latest = latestRelease(ctx, client)
		if state.Latest == "" || ctx.Err() != nil {
			return ""
		}
		if cache.save(state) != nil {
			return ""
		}
	}
	if state.Latest == "" || !newerVersion(state.Latest, current) || ctx.Err() != nil {
		return ""
	}
	if state.Notified != 0 && now.Sub(time.Unix(state.Notified, 0)) < noticeInterval {
		return ""
	}
	text := "Update to " + state.Latest + ": " + guidanceForRelease(guidance(current), state.Latest)
	state.Notified = now.Unix()
	if ctx.Err() != nil || cache.save(state) != nil {
		return ""
	}
	return text
}
