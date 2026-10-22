// Package audiolive serves a private browser companion for live audio translation.
package audiolive

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed translation.html translation.css translation.js capture-worklet.js
var assets embed.FS

// Config contains only the public controls used to start a translation session.
type Config struct {
	Language       string `json:"language"`
	NoiseReduction bool   `json:"noise_reduction"`
}

// Event carries only display data. It never contains credentials or raw API errors.
type Event struct {
	Type       string `json:"type"`
	Delta      string `json:"delta,omitempty"`
	Message    string `json:"message,omitempty"`
	Tokens     int64  `json:"tokens,omitempty"`
	Format     string `json:"format,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`
	Channels   int    `json:"channels,omitempty"`
}

// Failure contains an actionable message prepared by trusted application code.
// Unclassified errors never reach the browser.
type Failure string

func (e Failure) Error() string { return string(e) }

func failureMessage(err error) string {
	var failure Failure
	if errors.As(err, &failure) {
		return failure.Error()
	}
	return "Could not start live translation. Check your connection and try again."
}

// Session leaves API transport, credentials, and error classification with its caller.
type Session interface {
	Append(context.Context, []byte) error
	Receive(context.Context) (Event, error)
	FinishInput(context.Context) error
	Abort()
}

type Options struct {
	Connect     func(context.Context, Config) (Session, error)
	OpenBrowser func(context.Context, string) error
	Writer      io.Writer
}

type companion struct {
	ctx     context.Context
	options Options
	origin  string
	token   string
	mu      sync.Mutex
	active  *capture
	opening bool
	closing bool
	workers sync.WaitGroup
}

type capture struct {
	id         string
	session    Session
	ctx        context.Context
	cancel     context.CancelFunc
	events     chan Event
	done       chan struct{}
	subscribed chan struct{}
	mu         sync.Mutex
	listening  bool
	sequence   uint64
	stopping   bool
	drainTimer *time.Timer
}

var languages = map[string]string{
	"zh": "Chinese", "en": "English", "fr": "French", "de": "German", "hi": "Hindi",
	"id": "Indonesian", "it": "Italian", "ja": "Japanese", "ko": "Korean", "pt": "Portuguese",
	"ru": "Russian", "es": "Spanish", "vi": "Vietnamese",
}

// Run owns the listener, private launch file, and all active API sessions.
func Run(ctx context.Context, options Options) error {
	if options.Connect == nil || options.OpenBrowser == nil || options.Writer == nil {
		return Failure("Live translation could not start: browser or API configuration is missing.")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return Failure("Could not open the local translation page. Check local network permissions.")
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// A fresh hostname isolates browser storage even when the listener reuses a port.
	var hostname [16]byte
	rand.Read(hostname[:])
	origin := "http://" + net.JoinHostPort(hex.EncodeToString(hostname[:])+".localhost", strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	c := &companion{ctx: ctx, options: options, origin: origin, token: rand.Text()}
	launch, cleanup, err := c.launchFile()
	if err != nil {
		return Failure("Could not create the private browser launch page. Check temporary-folder permissions.")
	}
	defer cleanup()
	server := &http.Server{Handler: c, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute}
	ended := make(chan error, 1)
	go func() { ended <- server.Serve(listener) }()
	defer func() {
		cancel()
		server.Close()
		c.abort()
		<-ended
	}()
	openCtx, stopOpen := context.WithTimeout(ctx, 5*time.Second)
	launchPath := filepath.ToSlash(launch)
	if filepath.VolumeName(launch) != "" {
		launchPath = "/" + launchPath
	}
	err = options.OpenBrowser(openCtx, (&url.URL{Scheme: "file", Path: launchPath}).String())
	stopOpen()
	if err != nil {
		_, err = fmt.Fprintln(options.Writer, "Open this private launch page in Chrome or Edge:\n"+launch+"\nCtrl+C to close.")
	} else {
		_, err = fmt.Fprintln(options.Writer, "Live translation opened in your browser.\nIf the page does not load, open this private launch page in Chrome or Edge:\n"+launch+"\nCtrl+C to close.")
	}
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-ended:
		ended <- err
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return Failure("The local translation page stopped. Start live translation again.")
	}
}

func (c *companion) launchFile() (string, func(), error) {
	directory, err := privateDirectory()
	if err != nil {
		return "", nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		os.Remove(directory)
		return "", nil, err
	}
	cleanup := func() { root.Remove("start.html"); root.Close(); os.Remove(directory) }
	file, err := privateFile(root, "start.html")
	if err != nil {
		cleanup()
		return "", nil, err
	}
	// The capability stays in a private file and browser memory, never process arguments.
	// The launch requires desktop Chromium's built-in loopback resolution.
	// Client Hints exclude browsers that merely advertise a compatible user-agent string.
	launchURL, _ := json.Marshal(c.origin + "/#" + c.token)
	_, err = fmt.Fprintf(file, `<!doctype html><meta charset="utf-8"><meta name="referrer" content="no-referrer"><title>Open live translation</title>
<p>Open this private launch page in current Chrome or Edge.</p>
<script>
try {
  const agent = navigator.userAgentData;
  if (globalThis.isSecureContext === true && agent && agent.mobile === false &&
      ["Windows", "macOS", "Linux", "Chrome OS"].includes(agent.platform) &&
      Array.isArray(agent.brands) && agent.brands.some(entry => entry && entry.brand === "Chromium" &&
        typeof entry.version === "string" && /^[0-9]{3}$/.test(entry.version) && Number(entry.version) >= 100)) {
    location.replace(%s);
  }
} catch {}
</script>`, launchURL)
	err = errors.Join(err, file.Close())
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return filepath.Join(directory, "start.html"), cleanup, nil
}

func (c *companion) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; worker-src 'self'; media-src 'self' blob:; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
	if r.Host != strings.TrimPrefix(c.origin, "http://") {
		http.Error(w, "Use the private launch page opened by the CLI.", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodGet {
		path := map[string]string{"/": "translation.html", "/translation.css": "translation.css", "/translation.js": "translation.js", "/capture-worklet.js": "capture-worklet.js"}[r.URL.Path]
		if path != "" {
			kind := "text/javascript; charset=utf-8"
			if strings.HasSuffix(path, ".html") {
				kind = "text/html; charset=utf-8"
			}
			if strings.HasSuffix(path, ".css") {
				kind = "text/css; charset=utf-8"
			}
			w.Header().Set("Content-Type", kind)
			data, _ := assets.ReadFile(path)
			w.Write(data)
			return
		}
	}
	origin := r.Header.Get("Origin")
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-OpenAI-Audio")), []byte(c.token)) != 1 ||
		(origin != "" && origin != c.origin) || (r.Method != http.MethodGet && origin != c.origin) ||
		(r.Header.Get("Sec-Fetch-Site") != "" && r.Header.Get("Sec-Fetch-Site") != "same-origin") {
		http.Error(w, "Reopen live translation from the CLI.", http.StatusForbidden)
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/start":
		c.start(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/events":
		c.stream(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/input":
		c.input(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/stop":
		c.stop(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (c *companion) start(w http.ResponseWriter, r *http.Request) {
	var config Config
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil || languages[config.Language] == "" || decoder.Decode(new(any)) != io.EOF {
		http.Error(w, "Choose an output language before starting.", http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	if c.active != nil || c.opening || c.closing {
		c.mu.Unlock()
		http.Error(w, "A translation is already running. Stop it before starting another.", http.StatusConflict)
		return
	}
	c.opening = true
	c.workers.Add(1)
	c.mu.Unlock()
	defer c.workers.Done()
	defer func() { c.mu.Lock(); c.opening = false; c.mu.Unlock() }()
	session, err := c.options.Connect(r.Context(), config)
	if err != nil {
		http.Error(w, failureMessage(err), http.StatusBadGateway)
		return
	}
	ctx, cancel := context.WithCancel(c.ctx)
	active := &capture{id: rand.Text(), session: session, ctx: ctx, cancel: cancel,
		events: make(chan Event, 16), done: make(chan struct{}), subscribed: make(chan struct{})}
	c.mu.Lock()
	if c.closing || c.ctx.Err() != nil || r.Context().Err() != nil {
		c.mu.Unlock()
		cancel()
		session.Abort()
		return
	}
	c.active = active
	c.workers.Add(1)
	c.mu.Unlock()
	go c.receive(active)
	w.Header().Set("Content-Type", "application/json")
	// The browser must know which capture to cancel before reading the body.
	w.Header().Set("X-OpenAI-Audio-Session", active.id)
	if err := json.NewEncoder(w).Encode(map[string]string{"id": active.id}); err != nil {
		active.cancel()
		session.Abort()
	}
}

func (c *companion) receive(active *capture) {
	defer c.workers.Done()
	defer func() {
		active.session.Abort()
		active.cancel()
		active.mu.Lock()
		if active.drainTimer != nil {
			active.drainTimer.Stop()
		}
		active.mu.Unlock()
		close(active.events)
		c.mu.Lock()
		if c.active == active {
			c.active = nil
		}
		c.mu.Unlock()
		close(active.done)
	}()
	// Subscribe before consuming output, including early API failures.
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-active.subscribed:
	case <-active.ctx.Done():
		return
	case <-timer.C:
		return
	}
	for {
		event, err := active.session.Receive(active.ctx)
		if err != nil {
			if active.ctx.Err() == nil {
				select {
				case active.events <- Event{Type: "error", Message: "The translation connection ended. Received audio remains available."}:
				case <-active.ctx.Done():
				}
			}
			return
		}
		select {
		case active.events <- event:
		case <-active.ctx.Done():
			return
		}
		if event.Type == "session.closed" || event.Type == "error" {
			return
		}
	}
}

func (c *companion) find(r *http.Request) *capture {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active != nil && r.URL.Query().Get("id") == c.active.id {
		return c.active
	}
	return nil
}

func (c *companion) stream(w http.ResponseWriter, r *http.Request) {
	active := c.find(r)
	if active == nil {
		http.Error(w, "This translation has ended. Start a new session.", http.StatusGone)
		return
	}
	active.mu.Lock()
	if active.listening {
		active.mu.Unlock()
		http.Error(w, "This translation is already open.", http.StatusConflict)
		return
	}
	active.listening = true
	close(active.subscribed)
	active.mu.Unlock()
	defer func() { active.cancel(); active.session.Abort() }()
	w.Header().Set("Content-Type", "application/x-ndjson")
	encoder := json.NewEncoder(w)
	controller := http.NewResponseController(w)
	if err := controller.Flush(); err != nil {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-active.events:
			if !ok {
				return
			}
			if encoder.Encode(event) != nil || controller.Flush() != nil {
				return
			}
		}
	}
}

func (c *companion) input(w http.ResponseWriter, r *http.Request) {
	active := c.find(r)
	if active == nil {
		http.Error(w, "This translation has ended. Start a new session.", http.StatusGone)
		return
	}
	active.mu.Lock()
	defer active.mu.Unlock()
	sequence, sequenceErr := strconv.ParseUint(r.URL.Query().Get("sequence"), 10, 64)
	if sequenceErr != nil || sequence != active.sequence || active.stopping {
		http.Error(w, "Audio arrived out of order. Stop and start a new translation.", http.StatusConflict)
		return
	}
	data, err := io.ReadAll(r.Body)
	if err != nil || len(data)%2 != 0 {
		http.Error(w, "The browser could not send complete audio. Start the session again.", http.StatusBadRequest)
		return
	}
	if err := active.session.Append(r.Context(), data); err != nil {
		active.cancel()
		active.session.Abort()
		http.Error(w, "The translation connection was interrupted. Start a new session.", http.StatusBadGateway)
		return
	}
	active.sequence++
	w.WriteHeader(http.StatusNoContent)
}

func (c *companion) stop(w http.ResponseWriter, r *http.Request) {
	active := c.find(r)
	if active == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	active.mu.Lock()
	if !active.listening || r.URL.Query().Get("abort") == "1" {
		// Setup cancellation and failed captures have nothing left to drain.
		// Confirm receiver teardown before the browser can accept a retry.
		active.stopping = true
		active.cancel()
		active.session.Abort()
		active.mu.Unlock()
		select {
		case <-active.done:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	defer active.mu.Unlock()
	if !active.stopping {
		active.stopping = true
		if err := active.session.FinishInput(r.Context()); err != nil {
			active.cancel()
			active.session.Abort()
			http.Error(w, "The connection ended before translation finished. Received audio remains available.", http.StatusBadGateway)
			return
		}
		active.drainTimer = time.AfterFunc(20*time.Second, func() {
			active.cancel()
			active.session.Abort()
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *companion) abort() {
	c.mu.Lock()
	c.closing = true
	active := c.active
	c.mu.Unlock()
	if active != nil {
		active.cancel()
		active.session.Abort()
	}
	c.workers.Wait()
}
