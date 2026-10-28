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
	for _, args := range [][]string{{"batches"}, {"batches", "--help"}, {"help", "batches"}, {"explore", "batches"}} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			got := runMainDispatchWithEnv(t, "bash", []string{
				"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=synthetic-batch-help-key",
			}, append([]string{"openai"}, args...)...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("offline discovery failed: %+v", got)
			}
			for _, want := range []string{"batches browse", "batches submit requests.jsonl"} {
				if !strings.Contains(got.stdout, want) {
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
