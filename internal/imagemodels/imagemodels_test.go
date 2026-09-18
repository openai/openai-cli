package imagemodels

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

func TestCatalog(t *testing.T) {
	aliases := Catalog(false)
	if len(aliases) != 9 || aliases[0].ID != openai.ImageModelGPTImage2_5Sunburst {
		t.Fatalf("unexpected alias catalog: %+v", aliases)
	}
	all := Catalog(true)
	if len(all) != 12 {
		t.Fatalf("expected 9 aliases and 3 snapshots, got %d", len(all))
	}
	seen := map[string]bool{}
	for i, entry := range all {
		if seen[entry.ID] || entry.Snapshot != (i >= len(aliases)) {
			t.Errorf("duplicate or mislabeled catalog entry: %+v", entry)
		}
		seen[entry.ID] = true
	}
	for _, id := range []string{
		openai.ImageModelGPTImage1, openai.ImageModelGPTImage1Mini, openai.ImageModelGPTImage2,
		openai.ImageModelGPTImage2_2026_04_21, openai.ImageModelGPTImage2_5Sunburst,
		openai.ImageModelGPTImage2_5Sunburst2026_09_08, openai.ImageModelGPTImage2_5Flare,
		openai.ImageModelGPTImage2_5Flare2026_09_08, openai.ImageModelGPTImage1_5,
		openai.ImageModelChatgptImageLatest, openai.ImageModelDallE2, openai.ImageModelDallE3,
	} {
		if !seen[id] {
			t.Errorf("missing known SDK image model %q", id)
		}
	}
	aliases[0].ID = "mutated"
	if Catalog(false)[0].ID == "mutated" {
		t.Fatal("caller modified the shared catalog")
	}
}

func TestDiscoverUsesOnlyBoundedIndividualLookups(t *testing.T) {
	var active, peak atomic.Int32
	var firstThree sync.Once
	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for maximum := peak.Load(); current > maximum && !peak.CompareAndSwap(maximum, current); maximum = peak.Load() {
		}
		if current == 3 {
			firstThree.Do(func() { close(started) })
		}
		mu.Lock()
		requests[r.URL.Path]++
		mu.Unlock()
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer fake-image-model-key" || r.Header.Get("X-Model-Check") != "test" {
			t.Error("metadata lookup did not preserve method/auth/request options")
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		writeModel(w, strings.TrimPrefix(r.URL.Path, "/v1/models/"), "null")
	}))
	defer server.Close()
	service := testService(server)
	done := make(chan []Result, 1)
	go func() {
		done <- Discover(t.Context(), &service, true, option.WithHeader("X-Model-Check", "test"))
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("three concurrent requests never started")
	}
	close(release)
	results := <-done
	if peak.Load() != 3 {
		t.Errorf("concurrency = %d; want 3", peak.Load())
	}
	for i, entry := range Catalog(true) {
		if results[i].ID != entry.ID || results[i].Status != StatusVisible {
			t.Errorf("result order or visibility changed: %+v", results[i])
		}
		if requests["/v1/models/"+entry.ID] != 1 {
			t.Errorf("expected one request for %q", entry.ID)
		}
	}
	if requests["/v1/models"] != 0 || len(requests) != len(Catalog(true)) {
		t.Fatalf("unexpected catalog or extra request routes: %v", requests)
	}
}

func TestDiscoverClassifiesMetadataWithoutLeakingResponseText(t *testing.T) {
	for _, test := range []struct {
		code    int
		status  Status
		failure Failure
	}{
		{401, StatusUnknown, FailureAuthentication},
		{403, StatusUnknown, FailureForbidden},
		{404, StatusNotVisible, ""},
		{408, StatusUnknown, FailureTimeout},
		{429, StatusUnknown, FailureRateLimit},
		{500, StatusUnknown, FailureServer},
		{504, StatusUnknown, FailureTimeout},
		{400, StatusUnknown, FailureRequest},
	} {
		t.Run(fmt.Sprint(test.code), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("x-should-retry", "true")
				w.WriteHeader(test.code)
				fmt.Fprint(w, `{"error":{"message":"private response text https://secret.invalid","type":"test","code":"test"}}`)
			}))
			defer server.Close()
			service := testService(server)
			entry := Catalog(false)[0]
			results := discover(t.Context(), &service, []Entry{entry}, time.Now(), time.Second, option.WithMaxRetries(4))
			if results[0].Status != test.status || results[0].Failure != test.failure {
				t.Fatalf("unexpected result: %+v", results[0])
			}
			if calls.Load() != 1 {
				t.Fatalf("metadata lookup retried %d times", calls.Load())
			}
			encoded, err := json.Marshal(results)
			if err != nil || strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "secret.invalid") {
				t.Fatalf("unsafe results: %s, %v", encoded, err)
			}
		})
	}
}

func TestDiscoverStopsAfterAuthenticationOrRateLimit(t *testing.T) {
	for _, status := range []int{401, 429} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			started := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 3 {
					close(started)
				}
				select {
				case <-started:
				case <-r.Context().Done():
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":{"message":"synthetic rejection","type":"test"}}`)
			}))
			defer server.Close()
			service := testService(server)
			results := Discover(t.Context(), &service, false)
			if calls.Load() != 3 {
				t.Errorf("performed %d lookups after shared rejection; want at most 3 in flight", calls.Load())
			}
			want := FailureAuthentication
			if status == 429 {
				want = FailureRateLimit
			}
			for _, result := range results {
				if result.Status != StatusUnknown || result.Failure != want {
					t.Errorf("unchecked entry not explicitly unknown: %+v", result)
				}
			}
		})
	}
}

func TestDiscoverRetirementAndResponseValidation(t *testing.T) {
	now := time.Date(2026, time.September, 18, 0, 0, 0, 0, time.UTC)
	entry := Catalog(false)[0]
	for _, test := range []struct {
		name, id, date string
		status         Status
		failure        Failure
	}{
		{"null", entry.ID, "null", StatusVisible, ""},
		{"past", entry.ID, `"2026-09-17"`, StatusRetired, ""},
		{"today", entry.ID, `"2026-09-18"`, StatusRetired, ""},
		{"future", entry.ID, `"2026-09-19"`, StatusVisible, ""},
		{"invalid date", entry.ID, `"not a date"`, StatusUnknown, FailureInvalidResponse},
		{"wrong model", "other-model", "null", StatusUnknown, FailureInvalidResponse},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeModel(w, test.id, test.date)
			}))
			defer server.Close()
			service := testService(server)
			result := discover(t.Context(), &service, []Entry{entry}, now, time.Second)[0]
			if result.Status != test.status || result.Failure != test.failure {
				t.Errorf("unexpected retirement/validation result: %+v", result)
			}
			if test.status == StatusRetired && result.ShutdownDate != strings.Trim(test.date, `"`) {
				t.Errorf("shutdown date lost: %+v", result)
			}
		})
	}
}

func TestDiscoverCancellationAndRequestDeadline(t *testing.T) {
	for _, cancelEarly := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelEarly), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if cancelEarly {
					cancel()
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			service := testService(server)
			results := discover(ctx, &service, Catalog(false), time.Now(), 20*time.Millisecond)
			want := FailureTimeout
			if cancelEarly {
				want = FailureCanceled
			}
			for _, result := range results {
				if result.Status != StatusUnknown || result.Failure != want {
					t.Errorf("context failure was misclassified: %+v", result)
				}
			}
			if cancelEarly && calls.Load() > 3 {
				t.Errorf("continued requests after cancellation: %d", calls.Load())
			}
		})
	}
}

func TestDiscoverAlreadyCanceledMakesNoRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("request made after cancellation")
	}))
	defer server.Close()
	service := testService(server)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, result := range Discover(ctx, &service, false) {
		if result.Status != StatusUnknown || result.Failure != FailureCanceled {
			t.Errorf("unexpected canceled result: %+v", result)
		}
	}
}

func TestFailureNetwork(t *testing.T) {
	err := &url.Error{Op: "Get", URL: "https://secret.invalid", Err: &testNetworkError{}}
	if classifyFailure(err) != FailureNetwork {
		t.Fatal("network error classification lost")
	}
}

type testNetworkError struct{}

func (*testNetworkError) Error() string   { return "synthetic network failure" }
func (*testNetworkError) Timeout() bool   { return false }
func (*testNetworkError) Temporary() bool { return false }

func testService(server *httptest.Server) openai.ModelService {
	return openai.NewModelService(option.WithAPIKey("fake-image-model-key"), option.WithBaseURL(server.URL+"/v1/"), option.WithHTTPClient(server.Client()))
}

func writeModel(w http.ResponseWriter, id, shutdownDateJSON string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"id":%q,"object":"model","created":1,"owned_by":"system","shutdown_date":%s}`, id, shutdownDateJSON)
}
