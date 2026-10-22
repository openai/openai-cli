package updatenotice

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (call roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return call(request)
}

func response(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func releaseBody(version string) string {
	data, _ := json.Marshal(map[string]any{"tag_name": version, "draft": false, "prerelease": false})
	return string(data)
}

func unknownInstallation(string) string { return manualGuidance }

func TestCheckGoGuidanceMatchesReleaseMajor(t *testing.T) {
	for _, tc := range []struct{ name, latest, installed, want string }{
		{"Go minor upgrade", "1.41.0", goGuidance, goGuidance},
		{"Go major upgrade", "2.0.0", goGuidance, manualGuidance},
		{"Go later major upgrade", "12.0.0", goGuidance, manualGuidance},
		{"Homebrew major upgrade", "2.0.0", brewGuidance, brewGuidance},
		{"unknown major upgrade", "2.0.0", manualGuidance, manualGuidance},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(releaseBody(tc.latest)), nil
			})}
			directory := privateCacheDirectory(t)
			now := time.Now()
			guidance := func(string) string { return tc.installed }
			want := "Update to " + tc.latest + ": " + tc.want
			if got := check(context.Background(), "1.40.1", directory, now, client, guidance); got != want {
				t.Fatalf("notice = %q; want %q", got, want)
			}
			// Later notices must retain the same guidance after throttling expires.
			if got := check(context.Background(), "1.40.1", directory, now.Add(noticeInterval), client, guidance); got != want {
				t.Fatalf("repeat notice = %q; want %q", got, want)
			}
		})
	}
}

func TestNoticeFitsStandardTerminal(t *testing.T) {
	for _, guidance := range []string{brewGuidance, goGuidance, manualGuidance} {
		t.Run(guidance, func(t *testing.T) {
			latest := "12.34.56"
			if guidance == goGuidance {
				latest = "1.234.567"
			}
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(releaseBody(latest)), nil
			})}
			text := check(context.Background(), "1.0.0", privateCacheDirectory(t), time.Now(), client, func(string) string {
				return guidance
			})
			if text == "" || len(text) > 80 || strings.ContainsAny(text, "\r\n\x1b") {
				t.Fatalf("expected one plain line within 80 columns, got %q (%d bytes)", text, len(text))
			}
		})
	}
}

func privateCacheDirectory(t *testing.T) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "notice")
	cache, err := openCache(context.Background(), directory)
	if err != nil {
		t.Fatalf("cache prerequisite failed: %v", err)
	}
	cache.close()
	return directory
}

func TestCheckRatesAttemptsAndNoticesSeparately(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "notice")
	now := time.Unix(1800000000, 0)
	version := "v1.2.0"
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.String() != releaseURL || request.Method != http.MethodGet {
			t.Errorf("unexpected request: %s %s", request.Method, request.URL)
		}
		for _, header := range []string{"Authorization", "Api-Key", "Cookie"} {
			if request.Header.Get(header) != "" {
				t.Errorf("unexpected %s header", header)
			}
		}
		return response(releaseBody(version)), nil
	})}
	checkAt := func(at time.Time) string {
		return check(context.Background(), "1.0.0", directory, at, client, unknownInstallation)
	}
	if got := checkAt(now); got != "Update to 1.2.0: "+manualGuidance {
		t.Fatalf("first notice = %q", got)
	}
	if got := checkAt(now.Add(time.Hour)); got != "" || calls != 1 {
		t.Fatalf("same-day notice %q, requests %d", got, calls)
	}
	version = "v1.3.0"
	if got := checkAt(now.Add(checkInterval)); got != "" || calls != 2 {
		t.Fatalf("changed-release notice %q, requests %d", got, calls)
	}
	version = "v1.4.0"
	if got := checkAt(now.Add(noticeInterval)); !strings.Contains(got, "1.4.0") || calls != 3 {
		t.Fatalf("weekly notice %q, requests %d", got, calls)
	}
	if got := checkAt(now.Add(noticeInterval + time.Hour)); got != "" || calls != 3 {
		t.Fatalf("repeated notice %q, requests %d", got, calls)
	}
	data, err := os.ReadFile(filepath.Join(directory, stateName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "http") {
		t.Fatal("cache retained remote URLs")
	}
}

func TestCheckFailedAttemptsAreCached(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "notice")
	now := time.Unix(1800000000, 0)
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("offline")
	})}
	for _, at := range []time.Time{now, now.Add(time.Hour)} {
		if got := check(context.Background(), "1.0.0", directory, at, client, unknownInstallation); got != "" {
			t.Fatalf("failure notice = %q", got)
		}
	}
	if calls != 1 {
		t.Fatalf("failed requests = %d", calls)
	}
	check(context.Background(), "1.0.0", directory, now.Add(checkInterval), client, unknownInstallation)
	if calls != 2 {
		t.Fatalf("next-day requests = %d", calls)
	}
}

func TestCheckConcurrentCallsDoNotDuplicateRequestsOrNotices(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "notice")
	now := time.Unix(1800000000, 0)
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return response(releaseBody("v2.0.0")), nil
		case <-request.Context().Done():
			return nil, request.Context().Err()
		}
	})}
	first := make(chan string, 1)
	go func() { first <- check(context.Background(), "1.0.0", directory, now, client, unknownInstallation) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first request did not start")
	}
	if got := check(context.Background(), "1.0.0", directory, now, client, unknownInstallation); got != "" {
		t.Fatalf("concurrent notice = %q", got)
	}
	close(release)
	if got := <-first; got == "" {
		t.Fatal("first request produced no notice")
	}
	if got := check(context.Background(), "1.0.0", directory, now, client, unknownInstallation); got != "" {
		t.Fatalf("subsequent notice = %q", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent requests = %d", calls.Load())
	}
}

func TestCheckRejectsCacheFailuresBeforeHTTP(t *testing.T) {
	now := time.Unix(1800000000, 0)
	valid := `{"schema":1,"checked":1799999999,"notified":0,"latest":"2.0.0"}`
	for name, data := range map[string]string{
		"invalid": "{", "oversized": strings.Repeat(" ", 4097),
		"future check":    strings.Replace(valid, "1799999999", "1800000001", 1),
		"future notice":   strings.Replace(valid, `"notified":0`, `"notified":1800000001`, 1),
		"invalid release": strings.Replace(valid, "2.0.0", "2.0.0\\nrun-me", 1),
		"unknown schema":  strings.Replace(valid, `"schema":1`, `"schema":2`, 1),
		"duplicate":       strings.Replace(valid, `"schema":1`, `"schema":1,"schema":1`, 1),
		"null":            strings.Replace(valid, `"notified":0`, `"notified":null`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			directory := privateCacheDirectory(t)
			if err := os.WriteFile(filepath.Join(directory, stateName), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Error("unexpected request")
				return nil, errors.New("unexpected")
			})}
			if got := check(context.Background(), "1.0.0", directory, now, client, unknownInstallation); got != "" {
				t.Fatalf("invalid-cache notice = %q", got)
			}
			after, err := os.ReadFile(filepath.Join(directory, stateName))
			if err != nil || string(after) != data {
				t.Fatal("invalid cache changed")
			}
		})
	}
}

func TestCheckCancellationAndVersionsSkipState(t *testing.T) {
	for _, version := range []string{"dev", "1.0.0-beta.1", "1.0.0+local", "", "v1.2", "01.2.3"} {
		t.Run(version, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "absent")
			if got := check(context.Background(), version, directory, time.Now(), nil, unknownInstallation); got != "" {
				t.Fatalf("notice = %q", got)
			}
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Fatal("invalid version created cache")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	directory := filepath.Join(t.TempDir(), "absent")
	if got := check(ctx, "1.0.0", directory, time.Now(), nil, unknownInstallation); got != "" {
		t.Fatalf("cancelled notice = %q", got)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("cancelled check created cache")
	}
}

func TestCheckTimeoutReservesAttempt(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "notice")
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	start := time.Now()
	if got := check(context.Background(), "1.0.0", directory, start, client, unknownInstallation); got != "" {
		t.Fatalf("timeout notice = %q", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("timeout took %s", elapsed)
	}
	data, err := os.ReadFile(filepath.Join(directory, stateName))
	if err != nil || !strings.Contains(string(data), `"checked":`) {
		t.Fatal("timeout attempt was not persisted")
	}
}

func TestCheckDoesNotDowngrade(t *testing.T) {
	for _, current := range []string{"2.0.0", "2.0.1", "10.0.0", "999999999999999999999999.0.0"} {
		calls := 0
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return response(releaseBody("v2.0.0")), nil
		})}
		if got := check(context.Background(), current, privateCacheDirectory(t), time.Now(), client, unknownInstallation); got != "" {
			t.Fatalf("current %s notice = %q", current, got)
		}
		if calls != 1 {
			t.Fatalf("current %s made %d requests; expected one release comparison", current, calls)
		}
	}
}

func TestCheckTimeoutBoundsUninterruptibleWorker(t *testing.T) {
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	defer func() {
		close(release)
		select {
		case workerSlot <- struct{}{}:
			<-workerSlot
		case <-time.After(time.Second):
			t.Error("worker did not release its slot")
		}
	}()
	start := time.Now()
	got := boundedCheck(ctx, func(context.Context) string {
		close(started)
		<-release // Simulate a filesystem operation that cannot observe context.
		close(finished)
		return "late notice"
	})
	if got != "" || time.Since(start) > time.Second {
		t.Fatalf("blocked worker returned %q after %s", got, time.Since(start))
	}
	select {
	case <-started:
	default:
		t.Fatal("worker never started")
	}
	// The exported entrypoint cannot start another worker while this one blocks.
	if got := Check(context.Background(), "1.0.0"); got != "" {
		t.Fatalf("concurrent exported check returned %q", got)
	}
	select {
	case <-finished:
		t.Fatal("unreleased worker unexpectedly finished")
	default:
	}
}
