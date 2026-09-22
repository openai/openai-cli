package cli_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/openai/openai-cli/internal/imagemodels"
	"github.com/openai/openai-go/v3"
)

type mainImageModelsReport struct {
	Source       string `json:"source"`
	DefaultModel string `json:"default_model"`
	Complete     bool   `json:"complete"`
	Models       []struct {
		imagemodels.Result
		Default bool `json:"default"`
	} `json:"models"`
}

func TestMainImageModelsHelpAndOfflineMakeNoRequests(t *testing.T) {
	server, requests := mainImageModelsServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("help/offline made an API request")
		http.Error(w, "synthetic unexpected request", http.StatusBadRequest)
	})
	for _, args := range [][]string{
		{"images", "models", "--help"}, {"help", "images", "models"},
		{"images", "models", "--offline"}, {"images", "models", "--offline", "--all"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			argv := append([]string{"./openai", "--base-url", server.URL}, args...)
			got := runMainDispatchWithEnv(t, "bash", nil, argv...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("offline/help failed: %+v", got)
			}
			if strings.Contains(strings.Join(args, " "), "offline") {
				report := decodeMainImageModels(t, got.stdout)
				if report.Source != "offline" || report.Complete {
					t.Fatalf("offline catalog claimed verification: %+v", report)
				}
				wantCount := 9
				if args[len(args)-1] == "--all" {
					wantCount = 12
				}
				if len(report.Models) != wantCount {
					t.Errorf("offline returned %d models; want %d", len(report.Models), wantCount)
				}
				for _, row := range report.Models {
					if row.Status != imagemodels.StatusNotChecked || row.Failure != "" {
						t.Errorf("offline entry claimed access: %+v", row)
					}
				}
			} else {
				for _, want := range []string{"./openai images models", "--offline", "--all", "exact model names", "API key setup: ./openai help setup"} {
					if !strings.Contains(got.stdout, want) {
						t.Errorf("help missing %q: %s", want, got.stdout)
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
		"./openai", "--base-url", server.URL, "--organization", "org-synthetic-models", "--project", "proj-synthetic-models",
		"--header", "X-Models-Check: synthetic-header", "images", "models")
	if got.code != 0 || got.stderr != "" {
		t.Fatalf("piped discovery failed: %+v", got)
	}
	report := decodeMainImageModels(t, got.stdout)
	assertMainImageModelsRows(t, report, false)
	assertMainImageModelsRoutes(t, requests(), false)
	if !report.Complete || report.Source != "live" {
		t.Fatalf("completed checks not marked complete: %+v", report)
	}
	if strings.Contains(got.stdout, "Checking image models") {
		t.Error("progress polluted script JSON")
	}
}

func TestMainImageModelsAllAndExplicitJSON(t *testing.T) {
	server, requests := mainImageModelsServer(t, func(w http.ResponseWriter, r *http.Request) {
		mainImageModelResponse(w, r.URL.Path, "null")
	})
	got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_API_KEY=synthetic-models-key"},
		"./openai", "--base-url", server.URL, "--format", "JSON", "images", "models", "--all")
	if got.code != 0 || got.stderr != "" {
		t.Fatalf("explicit JSON discovery failed: %+v", got)
	}
	assertMainImageModelsRows(t, decodeMainImageModels(t, got.stdout), true)
	assertMainImageModelsRoutes(t, requests(), true)
}

func TestMainImageModelsKeepsPartialResultsOnTimeout(t *testing.T) {
	server, requests := mainImageModelsServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/"+openai.ImageModelGPTImage2_5Flare) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("x-should-retry", "true")
			w.WriteHeader(http.StatusGatewayTimeout)
			fmt.Fprint(w, `{"error":{"message":"private synthetic response https://secret.invalid","type":"synthetic"}}`)
			return
		}
		mainImageModelResponse(w, r.URL.Path, "null")
	})
	got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_API_KEY=synthetic-models-key"},
		"./openai", "--base-url", server.URL, "images", "models")
	if got.code != 1 {
		t.Fatalf("partial discovery exit=%d; want 1: %+v", got.code, got)
	}
	report := decodeMainImageModels(t, got.stdout)
	if report.Complete || len(report.Models) != 9 {
		t.Fatalf("partial discovery lost rows or claims completion: %+v", report)
	}
	for _, row := range report.Models {
		if row.ID == openai.ImageModelGPTImage2_5Flare {
			if row.Status != imagemodels.StatusUnknown || row.Failure != imagemodels.FailureTimeout {
				t.Errorf("504 misclassified: %+v", row)
			}
		} else if row.Status != imagemodels.StatusVisible {
			t.Errorf("completed model check was lost: %+v", row)
		}
	}
	for _, want := range []string{"Some model checks timed out", "./openai images models --offline"} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("failure guidance missing %q: %q", want, got.stderr)
		}
	}
	for _, hidden := range []string{"private synthetic response", "secret.invalid", "synthetic-models-key"} {
		if strings.Contains(got.stdout+got.stderr, hidden) {
			t.Errorf("model discovery leaked raw detail %q", hidden)
		}
	}
	assertMainImageModelsRoutes(t, requests(), false)
}

func TestMainImageModelsTerminalIsReadable(t *testing.T) {
	server, requests := mainImageModelsServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/"+openai.ImageModelDallE2):
			mainImageModelResponse(w, r.URL.Path, `"2000-01-01"`)
		case strings.HasSuffix(r.URL.Path, "/"+openai.ImageModelDallE3):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":{"message":"synthetic invisible model","type":"test"}}`)
		default:
			mainImageModelResponse(w, r.URL.Path, "null")
		}
	})
	got := runMainImageErrorProcess(t, "terminal", []string{"./openai", "--base-url", server.URL, "images", "models"})
	if got.code != 0 {
		t.Fatalf("terminal discovery failed: %+v", got)
	}
	text := got.stdout + got.stderr
	for _, want := range []string{
		"MODEL", "STATUS", openai.ImageModelGPTImage2_5Sunburst, openai.ImageModelGPTImage2_5Flare,
		"default", "Visible", "2 retired or not visible", "./openai images models --all",
		`./openai images generate --prompt "A tiny orange robot" --model gpt-image-2.5-sunburst`,
		"generation permissions can differ",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("readable terminal output missing %q: %s", want, text)
		}
	}
	if strings.Count(text, "Checking image models...") != 1 {
		t.Errorf("expected one progress notice: %q", text)
	}
	for _, hidden := range []string{`"id":`, `"source":`, "\x1b", openai.ImageModelDallE2, openai.ImageModelDallE3} {
		if strings.Contains(text, hidden) {
			t.Errorf("terminal output exposed hidden row/JSON/control sequence %q: %q", hidden, text)
		}
	}
	assertMainImageModelsRoutes(t, requests(), false)
}

func TestMainImageModelsAuthenticationFailureIsFriendly(t *testing.T) {
	server, requests := mainImageModelsServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-should-retry", "true")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"private synthetic authentication rejection","type":"synthetic"}}`)
	})
	got := runMainImageErrorProcess(t, "terminal", []string{"./openai", "--base-url", server.URL, "images", "models"})
	text := got.stdout + got.stderr
	if got.code != 1 || !strings.Contains(text, "did not accept authentication") || !strings.Contains(text, "./openai help setup") {
		t.Fatalf("authentication guidance failed: %+v", got)
	}
	if strings.Contains(text, "private synthetic authentication rejection") || strings.Contains(text, "synthetic-image-error-key") || strings.Contains(text, "Not visible to this key") {
		t.Errorf("authentication failure leaked details or claimed model unavailability: %q", text)
	}
	count := 0
	for route, calls := range requests() {
		count += calls
		if calls != 1 || !strings.HasPrefix(route, "GET /models/") {
			t.Errorf("unexpected authentication probe route/retry: %q x%d", route, calls)
		}
	}
	if count < 1 || count > 3 {
		t.Errorf("authentication failure did not stop scheduling after at most 3 checks: %d", count)
	}
}

func TestMainImageModelsPreservesExistingModelListing(t *testing.T) {
	const payload = `{"object":"list","data":[{"id":"synthetic-text-model","object":"model","created":1,"owned_by":"system","shutdown_date":null}]}`
	server, requests := mainImageModelsServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, payload)
	})
	got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_API_KEY=synthetic-models-key"},
		"./openai", "--base-url", server.URL, "--format", "json", "models", "list")
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
	return report
}

func assertMainImageModelsRows(t *testing.T, report mainImageModelsReport, snapshots bool) {
	t.Helper()
	catalog := imagemodels.Catalog(snapshots)
	if len(report.Models) != len(catalog) || report.DefaultModel != openai.ImageModelGPTImage2_5Sunburst {
		t.Fatalf("unexpected model/default report: %+v", report)
	}
	for i, entry := range catalog {
		row := report.Models[i]
		if row.ID != entry.ID || row.Snapshot != entry.Snapshot || row.Status != imagemodels.StatusVisible || row.Default != (entry.ID == report.DefaultModel) {
			t.Errorf("exact model name, status, snapshot, order, or default marker changed: %+v", row)
		}
	}
}

func assertMainImageModelsRoutes(t *testing.T, requests map[string]int, snapshots bool) {
	t.Helper()
	catalog := imagemodels.Catalog(snapshots)
	if len(requests) != len(catalog) {
		t.Fatalf("unexpected request routes: %v", requests)
	}
	for _, entry := range catalog {
		if requests["GET /models/"+entry.ID] != 1 {
			t.Errorf("expected exactly one individual GET for %q: %v", entry.ID, requests)
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
