package audiolive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSession struct {
	events   chan Event
	closed   chan struct{}
	once     sync.Once
	mu       sync.Mutex
	audio    []byte
	finished atomic.Int32
}

func newFakeSession() *fakeSession {
	return &fakeSession{events: make(chan Event, 8), closed: make(chan struct{})}
}
func (s *fakeSession) Append(_ context.Context, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audio = append(s.audio, data...)
	return nil
}
func (s *fakeSession) Receive(ctx context.Context) (Event, error) {
	select {
	case e := <-s.events:
		return e, nil
	case <-ctx.Done():
		return Event{}, ctx.Err()
	case <-s.closed:
		return Event{}, io.EOF
	}
}
func (s *fakeSession) FinishInput(context.Context) error {
	s.finished.Add(1)
	s.events <- Event{Type: "session.output_audio.delta", Delta: "AAA="}
	s.events <- Event{Type: "session.closed"}
	return nil
}
func (s *fakeSession) Abort() { s.once.Do(func() { close(s.closed) }) }

func testCompanion(t *testing.T, connect func(context.Context, Config) (Session, error)) (*companion, *httptest.Server) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	c := &companion{ctx: ctx, token: "synthetic-local-test-token", options: Options{Connect: connect}}
	server := httptest.NewServer(c)
	c.origin = server.URL
	t.Cleanup(func() { cancel(); server.Close(); c.abort() })
	return c, server
}
func call(t *testing.T, c *companion, method, path string, body io.Reader) *http.Response {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), method, c.origin+path, body)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Origin", c.origin)
	r.Header.Set("X-OpenAI-Audio", c.token)
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
func startCapture(t *testing.T, c *companion) string {
	t.Helper()
	response := call(t, c, http.MethodPost, "/start", strings.NewReader(`{"language":"en"}`))
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("start status %d", response.StatusCode)
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result.ID
}

func TestCompanionRejectsForeignOriginsHostsAndTokensBeforeAPIUse(t *testing.T) {
	var calls atomic.Int32
	c, _ := testCompanion(t, func(context.Context, Config) (Session, error) { calls.Add(1); return newFakeSession(), nil })
	for _, kind := range []string{"origin", "host", "token", "missing-origin", "cross-site"} {
		t.Run(kind, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, c.origin+"/start", strings.NewReader(`{"language":"en"}`))
			r.Header.Set("Origin", c.origin)
			r.Header.Set("X-OpenAI-Audio", c.token)
			switch kind {
			case "origin":
				r.Header.Set("Origin", "https://untrusted.example")
			case "host":
				r.Host = "untrusted.example"
			case "token":
				r.Header.Set("X-OpenAI-Audio", "wrong-synthetic-token")
			case "missing-origin":
				r.Header.Del("Origin")
			case "cross-site":
				r.Header.Set("Sec-Fetch-Site", "cross-site")
			}
			w := httptest.NewRecorder()
			c.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("status %d", w.Code)
			}
			if w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("cross-origin access enabled")
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("unauthorized request reached API")
	}
}

func TestCompanionNeverDisplaysUnclassifiedErrors(t *testing.T) {
	c, _ := testCompanion(t, func(context.Context, Config) (Session, error) { return nil, errors.New("synthetic-private-diagnostic") })
	response := call(t, c, http.MethodPost, "/start", strings.NewReader(`{"language":"en"}`))
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if bytes.Contains(data, []byte("synthetic-private-diagnostic")) {
		t.Fatal("private error was exposed")
	}
	if !bytes.Contains(data, []byte("Check your connection")) {
		t.Fatal("missing useful recovery message")
	}
}

func TestCompanionRejectsInvalidConfigurationBeforeAPIUse(t *testing.T) {
	var calls atomic.Int32
	c, _ := testCompanion(t, func(context.Context, Config) (Session, error) { calls.Add(1); return newFakeSession(), nil })
	for _, body := range []string{`{"language":"unknown"}`, `{"language":"en","secret":"synthetic"}`, `{"language":"en"}{"language":"fr"}`, `null`} {
		response := call(t, c, http.MethodPost, "/start", strings.NewReader(body))
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid configuration accepted: status %d", response.StatusCode)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid configuration reached API")
	}
}

func TestCompanionPreservesAudioOrderingAndFinalOutput(t *testing.T) {
	session := newFakeSession()
	c, _ := testCompanion(t, func(context.Context, Config) (Session, error) { return session, nil })
	id := startCapture(t, c)
	stream := call(t, c, http.MethodGet, "/events?id="+id, nil)
	defer stream.Body.Close()
	if stream.StatusCode != http.StatusOK {
		t.Fatal("event stream did not open")
	}
	pcm := bytes.Repeat([]byte{0x12, 0x34}, 1<<20)
	response := call(t, c, http.MethodPost, "/input?id="+id+"&sequence=0", bytes.NewReader(pcm))
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("audio status %d", response.StatusCode)
	}
	for _, path := range []string{"&sequence=0", "&sequence=1trailing"} {
		response = call(t, c, http.MethodPost, "/input?id="+id+path, strings.NewReader("xx"))
		response.Body.Close()
		if response.StatusCode != http.StatusConflict {
			t.Fatal("repeated or malformed sequence accepted")
		}
	}
	response = call(t, c, http.MethodPost, "/input?id="+id+"&sequence=1", strings.NewReader("x"))
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatal("partial PCM sample accepted")
	}
	session.mu.Lock()
	equal := bytes.Equal(pcm, session.audio)
	session.mu.Unlock()
	if !equal {
		t.Fatal("audio bytes changed or replayed")
	}
	response = call(t, c, http.MethodPost, "/stop?id="+id, nil)
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatal("stop failed")
	}
	decoder := json.NewDecoder(stream.Body)
	for _, kind := range []string{"session.output_audio.delta", "session.closed"} {
		var event Event
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		if event.Type != kind {
			t.Fatalf("expected %s; got %s", kind, event.Type)
		}
	}
	select {
	case <-session.closed:
	case <-time.After(time.Second):
		t.Fatal("session did not close")
	}
	if session.finished.Load() != 1 {
		t.Fatal("input close was repeated")
	}
}

func TestCompanionRetainsEarlyAPIErrorsUntilBrowserSubscribes(t *testing.T) {
	session := newFakeSession()
	session.events <- Event{Type: "error", Message: "Your project cannot start live translation."}
	c, _ := testCompanion(t, func(context.Context, Config) (Session, error) { return session, nil })
	id := startCapture(t, c)
	stream := call(t, c, http.MethodGet, "/events?id="+id, nil)
	defer stream.Body.Close()
	var event Event
	if err := json.NewDecoder(stream.Body).Decode(&event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "error" || event.Message == "" {
		t.Fatal("early API error was lost")
	}
}

func TestCompanionRejectsConcurrentSessionsAndEventReaders(t *testing.T) {
	session := newFakeSession()
	var calls atomic.Int32
	c, _ := testCompanion(t, func(context.Context, Config) (Session, error) { calls.Add(1); return session, nil })
	id := startCapture(t, c)
	response := call(t, c, http.MethodPost, "/start", strings.NewReader(`{"language":"fr"}`))
	response.Body.Close()
	if response.StatusCode != http.StatusConflict || calls.Load() != 1 {
		t.Fatal("a second paid session started")
	}
	stream := call(t, c, http.MethodGet, "/events?id="+id, nil)
	defer stream.Body.Close()
	response = call(t, c, http.MethodGet, "/events?id="+id, nil)
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatal("a second event reader opened")
	}
	stream.Body.Close()
	select {
	case <-session.closed:
	case <-time.After(time.Second):
		t.Fatal("browser disconnect did not stop API session")
	}
}

func TestCompanionRunCancellationRemovesPrivateLaunchFile(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var launch string
	err := Run(ctx, Options{
		Connect: func(context.Context, Config) (Session, error) { t.Fatal("unexpected API connection"); return nil, nil },
		Writer:  io.Discard,
		OpenBrowser: func(_ context.Context, target string) error {
			u, err := url.Parse(target)
			if err != nil {
				return err
			}
			if u.Host != "" {
				t.Error("private file URL unexpectedly contained a host")
			}
			path := u.Path
			if runtime.GOOS == "windows" {
				path = strings.TrimPrefix(path, "/")
			}
			launch = filepath.FromSlash(path)
			info, err := os.Stat(launch)
			if err != nil {
				return err
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
				t.Error("launch file was not private")
			}
			data, err := os.ReadFile(launch)
			if err != nil {
				return err
			}
			if !bytes.Contains(data, []byte("127.0.0.1:")) {
				t.Error("launch file omitted loopback address")
			}
			cancel()
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if launch == "" {
		t.Fatal("browser launcher did not run")
	}
	if _, err := os.Stat(launch); !os.IsNotExist(err) {
		t.Fatal("private launch file remained")
	}
	if _, err := os.Stat(filepath.Dir(launch)); !os.IsNotExist(err) {
		t.Fatal("private launch directory remained")
	}
}

func TestCompanionCanceledOpeningCannotPublishLateSession(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	session := newFakeSession()
	c, _ := testCompanion(t, func(context.Context, Config) (Session, error) { close(entered); <-release; return session, nil })
	ctx, cancel := context.WithCancel(t.Context())
	request := httptest.NewRequest(http.MethodPost, c.origin+"/start", strings.NewReader(`{"language":"en"}`)).WithContext(ctx)
	done := make(chan struct{})
	go func() { defer close(done); c.start(httptest.NewRecorder(), request) }()
	<-entered
	cancel()
	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("opening did not finish")
	}
	select {
	case <-session.closed:
	default:
		t.Fatal("late session was not aborted")
	}
	c.mu.Lock()
	active := c.active
	c.mu.Unlock()
	if active != nil {
		t.Fatal("canceled request published a session")
	}
}
