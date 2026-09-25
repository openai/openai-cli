package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/imagemodels"
)

type mainImageModelsReport struct {
	Source   string               `json:"source"`
	Complete bool                 `json:"complete"`
	Models   []imagemodels.Result `json:"models"`
}

func TestMainImageModelsHelpAndOfflineMakeNoRequests(t *testing.T) {
	server, requests := mainImageModelsServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("help/offline made an API request")
		http.Error(w, "synthetic unexpected request", http.StatusBadRequest)
	})
	for _, args := range [][]string{
		{"images", "models", "--help"}, {"help", "images", "models"},
		{"--format", "json", "images", "models", "--offline"}, {"--format", "json", "images", "models", "--offline", "--all"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			argv := append([]string{"openai", "--base-url", server.URL}, args...)
			// The helper removes all inherited OPENAI_* settings, including keys.
			got := runMainDispatchWithEnv(t, "bash", nil, argv...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("offline/help failed: %+v", got)
			}
			if strings.Contains(strings.Join(args, " "), "offline") {
				report := decodeMainImageModels(t, got.stdout)
				if report.Source != "offline" || report.Complete {
					t.Fatalf("offline catalog claimed verification: %+v", report)
				}
				assertMainImageModelsRows(t, report, args[len(args)-1] == "--all", imagemodels.StatusNotChecked)
			} else {
				for _, want := range []string{"openai images models", "--offline", "--all", "exact model names", "API key setup: openai help setup", "--model gpt-image-2.5-flare"} {
					if !strings.Contains(got.stdout, want) {
						t.Errorf("help missing %q: %s", want, got.stdout)
					}
				}
				for _, hidden := range []string{"CLI default", "Leaving out --model", "Or use the default"} {
					if strings.Contains(got.stdout, hidden) {
						t.Errorf("discovery advertised an unimplemented default: %q", got.stdout)
					}
				}
			}
		})
	}
	if len(requests()) != 0 {
		t.Fatal("help/offline made API requests")
	}
}

func TestMainImageModelsJSONAndHeaders(t *testing.T) {
	server, requests := mainImageModelsServer(t, func(w http.ResponseWriter, r *http.Request) {
		for name, want := range map[string]string{
			"Authorization": "Bearer synthetic-models-key", "OpenAI-Organization": "org-synthetic-models",
			"OpenAI-Project": "proj-synthetic-models", "X-Models-Check": "synthetic-header",
		} {
			if r.Header.Get(name) != want {
				t.Errorf("metadata checks did not retain %s", name)
			}
		}
		mainImageModelResponse(w, r.URL.Path, "null")
	})
	got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_API_KEY=synthetic-models-key"},
		"openai", "--base-url", server.URL, "--organization", "org-synthetic-models", "--project", "proj-synthetic-models",
		"--header", "X-Models-Check: synthetic-header", "--format", "json", "images", "models")
	if got.code != 0 || got.stderr != "" {
		t.Fatalf("JSON discovery failed: %+v", got)
	}
	report := decodeMainImageModels(t, got.stdout)
	assertMainImageModelsRows(t, report, false, imagemodels.StatusVisible)
	assertMainImageModelsRoutes(t, requests(), false)
	if !report.Complete || report.Source != "live" {
		t.Fatalf("completed checks not marked complete: %+v", report)
	}
	if strings.Contains(got.stdout, "Checking image models") {
		t.Error("progress polluted script JSON")
	}
}

func TestMainImageModelsAllIncludesExactSnapshotNames(t *testing.T) {
	server, requests := mainImageModelsServer(t, func(w http.ResponseWriter, r *http.Request) {
		mainImageModelResponse(w, r.URL.Path, "null")
	})
	got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_API_KEY=synthetic-models-key"},
		"openai", "--base-url", server.URL, "--format", "JSON", "images", "models", "--all")
	if got.code != 0 || got.stderr != "" {
		t.Fatalf("explicit JSON discovery failed: %+v", got)
	}
	assertMainImageModelsRows(t, decodeMainImageModels(t, got.stdout), true, imagemodels.StatusVisible)
	assertMainImageModelsRoutes(t, requests(), true)
}

func TestMainImageModelsPreservesMachineFormatsAndExtraction(t *testing.T) {
	var baseline mainImageModelsReport
	for _, format := range []string{"json", "JSON", "jsonl", "raw", "RaW"} {
		t.Run(format, func(t *testing.T) {
			got := runMainDispatchWithEnv(t, "bash", nil,
				"openai", "--format", format, "images", "models", "--offline")
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("machine output failed: %+v", got)
			}
			report := decodeMainImageModels(t, got.stdout)
			if baseline.Source == "" {
				baseline = report
			} else if !reflect.DeepEqual(report, baseline) {
				t.Fatalf("format %q changed report data: %+v", format, report)
			}
			if strings.EqualFold(format, "jsonl") && strings.Count(got.stdout, "\n") != 1 {
				t.Fatalf("jsonl must contain one report line: %q", got.stdout)
			}
		})
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"automatic extraction", []string{"--transform", "source"}, "\"offline\"\n"},
		{"explicit auto extraction", []string{"--format", "auto", "--transform", "source"}, "\"offline\"\n"},
		{"raw string", []string{"--transform", "source", "--raw-output"}, "offline\n"},
		{"text extraction", []string{"--format", "text", "--transform", "source"}, "offline\n"},
		{"exact model extraction", []string{"--transform", "models.0.id", "--raw-output"}, "gpt-image-2.5-sunburst\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"openai"}, tc.args...)
			args = append(args, "images", "models", "--offline")
			got := runMainDispatchWithEnv(t, "bash", nil, args...)
			if got.code != 0 || got.stderr != "" || got.stdout != tc.want {
				t.Fatalf("extraction got %+v; want stdout %q", got, tc.want)
			}
		})
	}
}

func TestMainImageModelsKeepsPartialResultsOnTimeoutResponse(t *testing.T) {
	server, requests := mainImageModelsServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/gpt-image-2.5-flare") {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("x-should-retry", "true")
			w.WriteHeader(http.StatusGatewayTimeout)
			fmt.Fprint(w, `{"error":{"message":"private synthetic response https://secret.invalid","type":"synthetic"}}`)
			return
		}
		mainImageModelResponse(w, r.URL.Path, "null")
	})
	got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_API_KEY=synthetic-models-key"},
		"openai", "--base-url", server.URL, "--format", "json", "images", "models")
	assertMainImageModelsPartialTimeout(t, got)
	assertMainImageModelsRoutes(t, requests(), false)
}

func TestMainImageModelsRequestDeadlineCancelsHTTP(t *testing.T) {
	canceled := make(chan struct{}, 1)
	server, requests := mainImageModelsServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/gpt-image-2.5-flare") {
			<-r.Context().Done()
			canceled <- struct{}{}
			return
		}
		mainImageModelResponse(w, r.URL.Path, "null")
	})
	got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_API_KEY=synthetic-models-key"},
		"openai", "--base-url", server.URL, "--format", "json", "images", "models")
	assertMainImageModelsPartialTimeout(t, got)
	assertMainImageModelsRoutes(t, requests(), false)
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("discovery deadline did not cancel the in-flight HTTP request")
	}
}

func TestMainImageModelsInterruptPreservesCompletedResults(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Process.Signal does not send os.Interrupt on Windows")
	}
	ready := make(chan struct{})
	server, requests := mainImageModelsServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models/gpt-image-2.5-sunburst":
			mainImageModelResponse(w, r.URL.Path, "null")
			return
		case "/models/gpt-image-1.5":
			// The fourth lookup proves the first response was fully processed:
			// the two other workers are still blocked on their initial requests.
			close(ready)
		}
		<-r.Context().Done()
	})
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, binary, "-test.run=^TestMainDispatchProcess$", "--",
		"openai", "--format", "json", "images", "models")
	child.Env = []string{"OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "OPENAI_API_KEY=synthetic-models-key", "OPENAI_BASE_URL=" + server.URL, "FORCE_COLOR=0"}
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	waited := false
	defer func() {
		cancel()
		if !waited {
			<-done
		}
	}()
	select {
	case <-ready:
	case err := <-done:
		waited = true
		t.Fatalf("discovery exited before interrupt: %v; stderr=%q", err, stderr.String())
	case <-ctx.Done():
		t.Fatal("discovery did not start its fourth lookup")
	}
	if err := child.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	err = <-done
	waited = true
	if ctx.Err() != nil {
		t.Fatal("discovery did not finish promptly after interrupt")
	}
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 1 {
		t.Fatalf("interrupt exit = %v; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	report := decodeMainImageModels(t, stdout.String())
	if report.Complete || report.Source != "live" || len(report.Models) != 9 {
		t.Fatalf("interrupt lost report rows or claimed completion: %+v", report)
	}
	if first := report.Models[0]; first.ID != "gpt-image-2.5-sunburst" || first.Status != imagemodels.StatusVisible || first.Failure != "" {
		t.Errorf("completed model check lost after interrupt: %+v", first)
	}
	for _, row := range report.Models[1:] {
		if row.Status != imagemodels.StatusUnknown || row.Failure != imagemodels.FailureCanceled {
			t.Errorf("interrupt did not preserve cancellation category: %+v", row)
		}
	}
	if !strings.Contains(stderr.String(), "Some model checks could not be completed") || !strings.Contains(stderr.String(), "openai images models --offline") {
		t.Errorf("missing incomplete-check guidance: %q", stderr.String())
	}
	if routes := requests(); len(routes) != 4 {
		t.Errorf("discovery continued scheduling after interrupt: %v", routes)
	}
}

func TestMainImageModelsPipeIsReadableAndAllRevealsUnavailable(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(fmt.Sprintf("all=%t", all), func(t *testing.T) {
			server, requests := mainImageModelsServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/dall-e-2"):
					mainImageModelResponse(w, r.URL.Path, `"2000-01-01"`)
				case strings.HasSuffix(r.URL.Path, "/dall-e-3"):
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusNotFound)
					fmt.Fprint(w, `{"error":{"message":"synthetic invisible model","type":"test"}}`)
				default:
					mainImageModelResponse(w, r.URL.Path, "null")
				}
			})
			args := []string{"openai", "--base-url", server.URL, "images", "models"}
			if all {
				args = append(args, "--all")
			}
			got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_API_KEY=synthetic-models-key"}, args...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("readable piped discovery failed: %+v", got)
			}
			for _, want := range []string{
				"MODEL", "STATUS", "gpt-image-2.5-sunburst", "gpt-image-2.5-flare", "Visible",
				`openai images generate --prompt "A tiny orange robot" --model gpt-image-2.5-sunburst`,
				"generation permissions can differ",
			} {
				if !strings.Contains(got.stdout, want) {
					t.Errorf("readable output missing %q: %s", want, got.stdout)
				}
			}
			for _, hidden := range []string{`"id":`, `"source":`, "\x1b", "Checking image models", "default", "Leaving out --model"} {
				if strings.Contains(got.stdout, hidden) {
					t.Errorf("readable pipe exposed JSON/progress/default/control %q: %q", hidden, got.stdout)
				}
			}
			if all {
				for _, want := range []string{"dall-e-2", "dall-e-3", "Retired 2000-01-01", "Not visible to this key", "gpt-image-2.5-flare-2026-09-08"} {
					if !strings.Contains(got.stdout, want) {
						t.Errorf("--all hid %q: %q", want, got.stdout)
					}
				}
			} else {
				for _, hidden := range []string{"dall-e-2", "dall-e-3"} {
					if strings.Contains(got.stdout, hidden) {
						t.Errorf("default presentation exposed unavailable row %q", hidden)
					}
				}
				if !strings.Contains(got.stdout, "2 retired or not visible") || !strings.Contains(got.stdout, "openai images models --all") {
					t.Errorf("missing hidden-row guidance: %q", got.stdout)
				}
			}
			assertMainImageModelsRoutes(t, requests(), all)
		})
	}
}

func TestMainImageModelsAuthenticationAndRateLimitStopChecks(t *testing.T) {
	for _, tc := range []struct {
		status  int
		failure imagemodels.Failure
		message string
	}{
		{http.StatusUnauthorized, imagemodels.FailureAuthentication, "did not accept authentication"},
		{http.StatusTooManyRequests, imagemodels.FailureRateLimit, "rate-limited model checks"},
	} {
		t.Run(string(tc.failure), func(t *testing.T) {
			server, requests := mainImageModelsServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("x-should-retry", "true")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, `{"error":{"message":"private synthetic rejection","type":"synthetic"}}`)
			})
			got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_API_KEY=synthetic-models-key"},
				"openai", "--base-url", server.URL, "--format", "json", "images", "models")
			if got.code != 1 || !strings.Contains(got.stderr, tc.message) {
				t.Fatalf("failure guidance failed: %+v", got)
			}
			report := decodeMainImageModels(t, got.stdout)
			if report.Source != "live" || report.Complete || len(report.Models) != 9 {
				t.Fatalf("failed checks lost rows or claimed completion: %+v", report)
			}
			for _, row := range report.Models {
				if row.Status != imagemodels.StatusUnknown || row.Failure != tc.failure {
					t.Errorf("failure implied unavailable model or lost safe category: %+v", row)
				}
			}
			for _, hidden := range []string{"private synthetic rejection", "synthetic-models-key", "Not visible to this key"} {
				if strings.Contains(got.stdout+got.stderr, hidden) {
					t.Errorf("failure exposed detail or claimed unavailability: %q", hidden)
				}
			}
			if tc.failure == imagemodels.FailureAuthentication && !strings.Contains(got.stderr, "openai help setup") {
				t.Errorf("missing authentication setup guidance: %q", got.stderr)
			}
			count := 0
			for route, calls := range requests() {
				count += calls
				if calls != 1 || !strings.HasPrefix(route, "GET /models/") {
					t.Errorf("unexpected probe route/retry: %q x%d", route, calls)
				}
			}
			if count < 1 || count > 3 {
				t.Errorf("failure did not stop scheduling after at most 3 checks: %d", count)
			}
		})
	}
}

func TestMainImageModelsPreservesExistingModelListing(t *testing.T) {
	const payload = `{"object":"list","data":[{"id":"synthetic-text-model","object":"model","created":1,"owned_by":"system","shutdown_date":null}]}`
	server, requests := mainImageModelsServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, payload)
	})
	got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_API_KEY=synthetic-models-key"},
		"openai", "--base-url", server.URL, "--format", "json", "models", "list")
	if got.code != 0 || got.stderr != "" {
		t.Fatalf("existing model listing changed: %+v", got)
	}
	var model map[string]any
	if err := json.Unmarshal([]byte(got.stdout), &model); err != nil || model["id"] != "synthetic-text-model" || model["object"] != "model" || model["owned_by"] != "system" || model["created"] != float64(1) {
		t.Fatalf("existing model-list API fields changed: %q, %v", got.stdout, err)
	}
	if routes := requests(); len(routes) != 1 || routes["GET /models"] != 1 {
		t.Errorf("existing list request changed: %v", routes)
	}
}

func decodeMainImageModels(t *testing.T, text string) mainImageModelsReport {
	t.Helper()
	var report mainImageModelsReport
	if err := json.Unmarshal([]byte(text), &report); err != nil {
		t.Fatalf("model report is not valid JSON: %v; %q", err, text)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["default_model"]; exists {
		t.Error("report includes an unimplemented default_model")
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(fields["models"], &rows); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if _, exists := row["default"]; exists {
			t.Error("report includes an unimplemented default marker")
		}
	}
	return report
}

func mainImageModelIDs(snapshots bool) []string {
	ids := []string{"gpt-image-2.5-sunburst", "gpt-image-2.5-flare", "gpt-image-2", "gpt-image-1.5", "gpt-image-1", "gpt-image-1-mini", "chatgpt-image-latest", "dall-e-3", "dall-e-2"}
	if snapshots {
		ids = append(ids, "gpt-image-2.5-sunburst-2026-09-08", "gpt-image-2.5-flare-2026-09-08", "gpt-image-2-2026-04-21")
	}
	return ids
}

func assertMainImageModelsRows(t *testing.T, report mainImageModelsReport, snapshots bool, status imagemodels.Status) {
	t.Helper()
	ids := mainImageModelIDs(snapshots)
	if len(report.Models) != len(ids) {
		t.Fatalf("unexpected model count: %+v", report)
	}
	for i, id := range ids {
		row := report.Models[i]
		if row.ID != id || row.Snapshot != (i >= 9) || row.Status != status || row.Failure != "" {
			t.Errorf("exact model name, status, snapshot, or order changed: %+v", row)
		}
	}
}

func assertMainImageModelsRoutes(t *testing.T, requests map[string]int, snapshots bool) {
	t.Helper()
	ids := mainImageModelIDs(snapshots)
	if len(requests) != len(ids) {
		t.Fatalf("unexpected request routes: %v", requests)
	}
	for _, id := range ids {
		if requests["GET /models/"+id] != 1 {
			t.Errorf("expected exactly one individual GET for %q: %v", id, requests)
		}
	}
}

func assertMainImageModelsPartialTimeout(t *testing.T, got mainDispatchResult) {
	t.Helper()
	if got.code != 1 {
		t.Fatalf("partial discovery exit=%d; want 1: %+v", got.code, got)
	}
	report := decodeMainImageModels(t, got.stdout)
	if report.Complete || report.Source != "live" || len(report.Models) != 9 {
		t.Fatalf("partial discovery lost rows or claims completion: %+v", report)
	}
	for _, row := range report.Models {
		if row.ID == "gpt-image-2.5-flare" {
			if row.Status != imagemodels.StatusUnknown || row.Failure != imagemodels.FailureTimeout {
				t.Errorf("timeout misclassified: %+v", row)
			}
		} else if row.Status != imagemodels.StatusVisible {
			t.Errorf("completed model check was lost: %+v", row)
		}
	}
	for _, want := range []string{"Some model checks timed out", "openai images models --offline"} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("failure guidance missing %q: %q", want, got.stderr)
		}
	}
	for _, hidden := range []string{"private synthetic response", "secret.invalid", "synthetic-models-key"} {
		if strings.Contains(got.stdout+got.stderr, hidden) {
			t.Errorf("model discovery leaked raw detail %q", hidden)
		}
	}
}

func mainImageModelsServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, func() map[string]int) {
	t.Helper()
	var mu sync.Mutex
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests[r.Method+" "+r.URL.Path]++
		mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return server, func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		copy := make(map[string]int, len(requests))
		for route, count := range requests {
			copy[route] = count
		}
		return copy
	}
}

func mainImageModelResponse(w http.ResponseWriter, path, shutdownDateJSON string) {
	w.Header().Set("Content-Type", "application/json")
	id := strings.TrimPrefix(path, "/models/")
	fmt.Fprintf(w, `{"id":%q,"object":"model","created":1,"owned_by":"system","shutdown_date":%s}`, id, shutdownDateJSON)
}
