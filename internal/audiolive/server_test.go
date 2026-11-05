package audiolive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
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

func TestCompanionStopBeforeSubscriptionAllowsImmediateRetry(t *testing.T) {
	sessions := make(chan *fakeSession, 3)
	c, _ := testCompanion(t, func(context.Context, Config) (Session, error) {
		session := newFakeSession()
		sessions <- session
		return session, nil
	})
	previousID := ""
	for attempt := 0; attempt < 3; attempt++ {
		id := startCapture(t, c)
		session := <-sessions
		if previousID != "" {
			response := call(t, c, http.MethodPost, "/stop?id="+previousID, nil)
			response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				t.Fatalf("stale stop status %d", response.StatusCode)
			}
			select {
			case <-session.closed:
				t.Fatal("stale stop closed the replacement session")
			default:
			}
			response = call(t, c, http.MethodPost, "/input?id="+id+"&sequence=0", strings.NewReader("xx"))
			response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				t.Fatalf("replacement input status %d", response.StatusCode)
			}
		}
		response := call(t, c, http.MethodPost, "/stop?id="+id, nil)
		response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("stop status %d", response.StatusCode)
		}
		select {
		case <-session.closed:
		default:
			t.Fatal("stop returned before releasing the unsubscribed session")
		}
		if session.finished.Load() != 0 {
			t.Fatal("canceled setup attempted to drain without an event reader")
		}
		previousID = id
		// Repeating an old stop must not prevent the next start.
		response = call(t, c, http.MethodPost, "/stop?id="+id, nil)
		response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("repeated stop status %d", response.StatusCode)
		}
	}
}

func TestCompanionStartHeaderAllowsCancelWithoutReadingBody(t *testing.T) {
	sessions := make(chan *fakeSession, 3)
	c, _ := testCompanion(t, func(context.Context, Config) (Session, error) {
		session := newFakeSession()
		sessions <- session
		return session, nil
	})
	previousID := ""
	for attempt := 0; attempt < 3; attempt++ {
		response := call(t, c, http.MethodPost, "/start", strings.NewReader(`{"language":"en"}`))
		id := response.Header.Get("X-OpenAI-Audio-Session")
		response.Body.Close()
		if response.StatusCode != http.StatusOK || id == "" {
			t.Fatalf("start status %d, session header present: %t", response.StatusCode, id != "")
		}
		session := <-sessions
		if previousID != "" {
			if id == previousID {
				t.Fatal("start reused a previous session identity")
			}
			response = call(t, c, http.MethodPost, "/stop?id="+previousID, nil)
			response.Body.Close()
			select {
			case <-session.closed:
				t.Fatal("old header identity stopped the replacement")
			default:
			}
		}
		response = call(t, c, http.MethodPost, "/stop?id="+id, nil)
		response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("stop status %d", response.StatusCode)
		}
		select {
		case <-session.closed:
		default:
			t.Fatal("stop acknowledged before releasing the session")
		}
		previousID = id
	}
}

func TestCompanionStartHeaderMatchesBodyAndErrorsOmitIdentity(t *testing.T) {
	c, _ := testCompanion(t, func(context.Context, Config) (Session, error) { return newFakeSession(), nil })
	response := call(t, c, http.MethodPost, "/start", strings.NewReader(`{"language":"en"}`))
	defer response.Body.Close()
	var body struct{ ID string }
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.ID == "" || response.Header.Get("X-OpenAI-Audio-Session") != body.ID {
		t.Fatal("start header and body identities differ")
	}
	for _, config := range []string{`{"language":"en"}`, `{}`} {
		failed := call(t, c, http.MethodPost, "/start", strings.NewReader(config))
		failed.Body.Close()
		if failed.StatusCode < 400 || failed.Header.Get("X-OpenAI-Audio-Session") != "" {
			t.Fatal("failed start exposed another session's identity")
		}
	}
}

func TestCompanionStartHandshakeDisconnectReleasesOpeningBeforeRetry(t *testing.T) {
	entered := make(chan struct{})
	first := newFakeSession()
	var calls atomic.Int32
	c, _ := testCompanion(t, func(ctx context.Context, _ Config) (Session, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			return first, nil
		}
		return newFakeSession(), nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.origin+"/start", strings.NewReader(`{"language":"en"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", c.origin)
	request.Header.Set("X-OpenAI-Audio", c.token)
	done := make(chan error, 1)
	go func() {
		response, err := http.DefaultClient.Do(request)
		if response != nil {
			response.Body.Close()
		}
		done <- err
	}()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("start request cancellation: %v", err)
	}
	released := make(chan struct{})
	go func() { c.workers.Wait(); close(released) }()
	select {
	case <-released:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnected handshake retained an opening session")
	}
	select {
	case <-first.closed:
	default:
		t.Fatal("disconnected handshake did not abort its late session")
	}
	startCapture(t, c)
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
			if !bytes.Contains(data, []byte(".localhost:")) {
				t.Error("launch file omitted the private localhost origin")
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

func TestCompanionRunIsolatesLaunchOrigins(t *testing.T) {
	hosts := make(map[string]bool)
	for _, mode := range []string{"browser", "manual-fallback"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var output bytes.Buffer
			var calls atomic.Int32
			var launch, origin, token string
			err := Run(ctx, Options{
				Connect: func(context.Context, Config) (Session, error) {
					calls.Add(1)
					return newFakeSession(), nil
				},
				Writer: &output,
				OpenBrowser: func(_ context.Context, target string) error {
					defer cancel()
					fileURL, err := url.Parse(target)
					if err != nil {
						return err
					}
					path := fileURL.Path
					if runtime.GOOS == "windows" {
						path = strings.TrimPrefix(path, "/")
					}
					launch = filepath.FromSlash(path)
					data, err := os.ReadFile(launch)
					if err != nil {
						return err
					}
					_, navigation, ok := strings.Cut(string(data), "location.replace(")
					if !ok {
						t.Fatal("private launch page omitted its destination")
					}
					encoded, _, ok := strings.Cut(navigation, ");")
					if !ok {
						t.Fatal("private launch destination was not delimited")
					}
					var address string
					if err := json.Unmarshal([]byte(encoded), &address); err != nil {
						t.Fatal("private launch destination was not encoded safely")
					}
					u, err := url.Parse(address)
					if err != nil {
						t.Fatal("private launch destination was not a URL")
					}
					label, local := strings.CutSuffix(u.Hostname(), ".localhost")
					if !local || len(label) != 32 || strings.Trim(label, "0123456789abcdef") != "" {
						t.Fatal("launch hostname did not contain a legal random localhost label")
					}
					if hosts[u.Hostname()] {
						t.Fatal("separate launches reused a hostname")
					}
					hosts[u.Hostname()] = true
					origin, token = u.Scheme+"://"+u.Host, u.Fragment
					if u.Scheme != "http" || u.Path != "/" || u.Port() == "" || u.RawQuery != "" || token == "" || strings.Contains(u.Host, strings.ToLower(token)) {
						t.Fatal("launch URL did not separate its origin and fragment capability")
					}
					// Dial the owned IPv4 listener directly; browser DNS support is separate evidence.
					loopback := net.JoinHostPort("127.0.0.1", u.Port())
					transport := &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
						return (&net.Dialer{}).DialContext(ctx, network, loopback)
					}}
					defer transport.CloseIdleConnections()
					client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
					for _, test := range []struct {
						name, method, path, host, origin, token, site string
						status                                        int
					}{
						{"page", "GET", "/", u.Host, "", "", "", 200},
						{"worklet", "GET", "/capture-worklet.js", u.Host, "", "", "", 200},
						{"numeric-host-page", "GET", "/", loopback, "", "", "", 403},
						{"numeric-host-api", "POST", "/start", loopback, origin, token, "same-origin", 403},
						{"localhost-host", "POST", "/start", "localhost:" + u.Port(), origin, token, "same-origin", 403},
						{"other-host", "POST", "/start", "other.localhost:" + u.Port(), origin, token, "same-origin", 403},
						{"other-origin", "POST", "/start", u.Host, "http://other.localhost:" + u.Port(), token, "same-origin", 403},
						{"missing-origin", "POST", "/start", u.Host, "", token, "same-origin", 403},
						{"query-token", "POST", "/start?token=" + url.QueryEscape(token), u.Host, origin, "", "same-origin", 403},
						{"wrong-token", "POST", "/start", u.Host, origin, "wrong-synthetic-token", "same-origin", 403},
						{"cross-site", "POST", "/start", u.Host, origin, token, "cross-site", 403},
						{"authorized-start", "POST", "/start", u.Host, origin, token, "same-origin", 200},
					} {
						t.Run(test.name, func(t *testing.T) {
							if calls.Load() != 0 {
								t.Fatal("an unauthorized request reached the API")
							}
							request, err := http.NewRequestWithContext(t.Context(), test.method, origin+test.path, strings.NewReader(`{"language":"en"}`))
							if err != nil {
								t.Fatal("could not construct loopback request")
							}
							request.Host = test.host
							request.Header.Set("Origin", test.origin)
							request.Header.Set("X-OpenAI-Audio", test.token)
							request.Header.Set("Sec-Fetch-Site", test.site)
							response, err := client.Do(request)
							if err != nil {
								t.Fatal("loopback request failed")
							}
							defer response.Body.Close()
							if response.StatusCode != test.status {
								t.Fatalf("status %d, want %d", response.StatusCode, test.status)
							}
							if response.Header.Get("Access-Control-Allow-Origin") != "" {
								t.Fatal("cross-origin access enabled")
							}
							if _, err := io.Copy(io.Discard, response.Body); err != nil {
								t.Fatal("could not finish loopback response")
							}
						})
					}
					if calls.Load() != 1 {
						t.Fatal("authorized request did not reach the API exactly once")
					}
					if mode == "manual-fallback" {
						return errors.New("synthetic browser launch failure")
					}
					return nil
				},
			})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("expected cancellation, got %v", err)
			}
			if strings.Contains(output.String(), origin) || strings.Contains(output.String(), token) {
				t.Error("terminal output exposed the launch origin or capability")
			}
			if !strings.Contains(output.String(), launch) || !strings.Contains(output.String(), "Chrome or Edge") {
				t.Error("terminal output omitted the supported private-file fallback")
			}
			if _, err := os.Stat(filepath.Dir(launch)); !os.IsNotExist(err) {
				t.Error("private launch directory remained")
			}
		})
	}
}

func TestCompanionLaunchFileBrowserGate(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required to execute the private launch script")
	}
	c := &companion{origin: "http://0123456789abcdef0123456789abcdef.localhost:12345", token: "synthetic-local-test-token"}
	launch, cleanup, err := c.launchFile()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	command := exec.CommandContext(t.Context(), node, "server_launch_test.js", launch)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("private launch script failed: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
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

type heldCleanupSession struct {
	*fakeSession
	mode      string
	entered   chan struct{}
	release   chan struct{}
	announced sync.Once
}

func (s *heldCleanupSession) Receive(ctx context.Context) (Event, error) {
	event, err := s.fakeSession.Receive(ctx)
	if s.mode == "receive" && ctx.Err() != nil {
		s.announced.Do(func() { close(s.entered) })
		<-s.release
	}
	return event, err
}

func (s *heldCleanupSession) Abort() {
	s.fakeSession.Abort()
	if s.mode == "abort" {
		s.announced.Do(func() { close(s.entered) })
		<-s.release
	}
}

func TestCompanionAbortSubscribedWaitsForReleaseBeforeRetry(t *testing.T) {
	for _, mode := range []string{"receive", "abort"} {
		t.Run(mode, func(t *testing.T) {
			held := &heldCleanupSession{fakeSession: newFakeSession(), mode: mode, entered: make(chan struct{}), release: make(chan struct{})}
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(held.release) }) }
			defer release()
			replacement := newFakeSession()
			var starts atomic.Int32
			c, _ := testCompanion(t, func(context.Context, Config) (Session, error) {
				if starts.Add(1) == 1 {
					return held, nil
				}
				return replacement, nil
			})
			id := startCapture(t, c)
			stream := call(t, c, http.MethodGet, "/events?id="+id, nil)
			defer stream.Body.Close()
			if mode == "abort" {
				held.events <- Event{Type: "session.closed"}
				var terminal Event
				if err := json.NewDecoder(stream.Body).Decode(&terminal); err != nil || terminal.Type != "session.closed" {
					t.Fatalf("terminal event: %+v, %v", terminal, err)
				}
			}
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, c.origin+"/stop?id="+id+"&abort=1", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Origin", c.origin)
			request.Header.Set("X-OpenAI-Audio", c.token)
			type result struct {
				response *http.Response
				err      error
			}
			stopped := make(chan result, 1)
			go func() { response, err := http.DefaultClient.Do(request); stopped <- result{response, err} }()
			select {
			case got := <-stopped:
				if got.response != nil {
					got.response.Body.Close()
				}
				t.Fatalf("abort returned before held cleanup: %v", got.err)
			case <-held.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("abort did not reach session cleanup")
			}
			select {
			case got := <-stopped:
				if got.response != nil {
					got.response.Body.Close()
				}
				t.Fatalf("abort acknowledged while cleanup remained held: %v", got.err)
			case <-time.After(20 * time.Millisecond):
			}
			release()
			got := <-stopped
			if got.err != nil {
				t.Fatal(got.err)
			}
			got.response.Body.Close()
			if got.response.StatusCode != http.StatusNoContent || held.finished.Load() != 0 {
				t.Fatal("abort did not release without graceful drain")
			}
			next := startCapture(t, c)
			for _, stale := range []string{id, "unknown-synthetic-session", ""} {
				response := call(t, c, http.MethodPost, "/stop?id="+stale+"&abort=1", nil)
				response.Body.Close()
				if response.StatusCode != http.StatusNoContent {
					t.Fatal("stale abort failed")
				}
			}
			response := call(t, c, http.MethodPost, "/input?id="+next+"&sequence=0", strings.NewReader("xx"))
			response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				t.Fatal("stale abort stopped the replacement")
			}
		})
	}
}
