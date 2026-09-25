package imagemodels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

func TestDiscoverKeepsOrderedPartialResults(t *testing.T) {
	entries := Catalog(false)
	first := make(chan struct{})
	last := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/v1/models/")
		switch id {
		case entries[0].ID:
			close(first)
			select {
			case <-last:
			case <-r.Context().Done():
				return
			}
			writeModel(w, id, "null")
		case entries[1].ID:
			w.WriteHeader(http.StatusNotFound)
		case entries[2].ID:
			w.WriteHeader(http.StatusInternalServerError)
		case entries[len(entries)-1].ID:
			<-first
			close(last)
			writeModel(w, id, "null")
		default:
			writeModel(w, id, "null")
		}
	}))
	defer server.Close()
	service := testService(server)
	results := Discover(t.Context(), &service, false)
	for i, result := range results {
		if result.ID != entries[i].ID {
			t.Errorf("result %d reordered: %+v", i, result)
		}
		switch i {
		case 1:
			if result.Status != StatusNotVisible || result.Failure != "" {
				t.Errorf("missing model: %+v", result)
			}
		case 2:
			if result.Status != StatusUnknown || result.Failure != FailureServer {
				t.Errorf("failed check: %+v", result)
			}
		default:
			if result.Status != StatusVisible || result.Failure != "" {
				t.Errorf("successful check lost: %+v", result)
			}
		}
	}
}

func TestDiscoverCancellationPreservesCompletedResults(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entries := Catalog(false)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		id := strings.TrimPrefix(r.URL.Path, "/v1/models/")
		if id == entries[0].ID {
			writeModel(w, id, "null")
			return
		}
		// Reaching the fourth model proves the first result was fully received:
		// the other two workers are still waiting for cancellation.
		if id == entries[3].ID {
			cancel()
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	service := testService(server)
	results := Discover(ctx, &service, false)
	if results[0].Status != StatusVisible || results[0].Failure != "" {
		t.Errorf("completed result was lost: %+v", results[0])
	}
	for _, result := range results[1:] {
		if result.Status != StatusUnknown || result.Failure != FailureCanceled {
			t.Errorf("canceled result: %+v", result)
		}
	}
	if calls.Load() > 4 {
		t.Errorf("continued requests after cancellation: %d", calls.Load())
	}
}

func TestDiscoverStopPreservesCompletedResults(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			entries := Catalog(false)
			var calls atomic.Int32
			stop := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				id := strings.TrimPrefix(r.URL.Path, "/v1/models/")
				if id == entries[0].ID {
					writeModel(w, id, "null")
					return
				}
				if id == entries[3].ID {
					close(stop)
				}
				select {
				case <-stop:
				case <-r.Context().Done():
					return
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			service := testService(server)
			results := Discover(t.Context(), &service, false)
			if results[0].Status != StatusVisible || results[0].Failure != "" {
				t.Errorf("completed result was lost: %+v", results[0])
			}
			want := FailureAuthentication
			if status == http.StatusTooManyRequests {
				want = FailureRateLimit
			}
			for _, result := range results[1:] {
				if result.Status != StatusUnknown || result.Failure != want {
					t.Errorf("stopped result: %+v", result)
				}
			}
			if calls.Load() != 4 {
				t.Errorf("continued requests after rejection: %d", calls.Load())
			}
		})
	}
}

func TestDiscoverRequestDeadlines(t *testing.T) {
	for _, shorter := range []bool{false, true} {
		t.Run(fmt.Sprint(shorter), func(t *testing.T) {
			ctx := t.Context()
			var wantDeadline time.Time
			if shorter {
				var cancel context.CancelFunc
				wantDeadline = time.Now().Add(time.Second)
				ctx, cancel = context.WithDeadline(ctx, wantDeadline)
				defer cancel()
			}
			var calls atomic.Int32
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				deadline, ok := r.Context().Deadline()
				if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 5*time.Second {
					t.Errorf("lookup has no five-second maximum deadline: %v, %v", deadline, ok)
				}
				if shorter && !deadline.Equal(wantDeadline) {
					t.Errorf("caller deadline changed: got %v, want %v", deadline, wantDeadline)
				}
				id := strings.TrimPrefix(r.URL.Path, "/v1/models/")
				body := fmt.Sprintf(`{"id":%q,"shutdown_date":null}`, id)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			service := openai.NewModelService(option.WithAPIKey("fake-image-model-key"), option.WithHTTPClient(client), option.WithBaseURL("http://localhost/v1/"))
			for _, result := range Discover(ctx, &service, false, option.WithRequestTimeout(time.Minute)) {
				if result.Status != StatusVisible {
					t.Errorf("deadline test failed: %+v", result)
				}
			}
			if int(calls.Load()) != len(Catalog(false)) {
				t.Errorf("lookups = %d", calls.Load())
			}
		})
	}
}

func TestDiscoverMalformedMetadata(t *testing.T) {
	entry := Catalog(false)[0]
	for _, body := range []string{
		``, `null`, `[]`, `"unexpected"`, `{`, `{}`, `{"id":123}`,
		fmt.Sprintf(`{"id":%q,"shutdown_date":42}`, entry.ID),
		fmt.Sprintf(`{"id":%q,"shutdown_date":{}}`, entry.ID),
		fmt.Sprintf(`{"id":%q,"object":"file"}`, entry.ID),
		fmt.Sprintf(`{"id":%q,"object":null}`, entry.ID),
		fmt.Sprintf(`{"id":%q,"object":42}`, entry.ID),
		fmt.Sprintf(`{"id":%q,"object":[]}`, entry.ID),
		fmt.Sprintf(`{"id":%q,"object":"model"`, entry.ID),
		fmt.Sprintf(`{"id":%q "object":"model"}`, entry.ID),
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, body)
			}))
			defer server.Close()
			service := testService(server)
			result := discover(t.Context(), &service, []Entry{entry}, time.Now(), time.Second)[0]
			if result.Status != StatusUnknown || result.Failure != FailureInvalidResponse {
				t.Errorf("malformed metadata accepted: %+v", result)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

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
