package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMainBatchHelpDiscoveryIsOffline(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer server.Close()
	for _, args := range [][]string{{"batches"}, {"batches", "--help"}, {"help", "batches"}, {"explore", "batches"}, {"batches", "browse", "--help"}} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			got := runMainDispatchWithEnv(t, "bash", []string{
				"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=synthetic-batch-help-key",
			}, append([]string{"openai"}, args...)...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("offline discovery failed: %+v", got)
			}
			expected := []string{"batches browse", "batches submit requests.jsonl"}
			if len(args) == 3 && args[1] == "browse" {
				expected = []string{"Ctrl+N", "CSV text column", "Enter or Ctrl+S", "View rows with answers", "save a spreadsheet"}
			}
			output := strings.Join(strings.Fields(got.stdout), " ")
			for _, want := range expected {
				if !strings.Contains(output, want) {
					t.Errorf("discovery omitted %q:\n%s", want, got.stdout)
				}
			}
			if strings.Contains(got.stdout, "synthetic-batch-help-key") {
				t.Error("discovery exposed configured credentials")
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("offline discovery made %d API requests", requests.Load())
	}
}
