//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"golang.org/x/sys/windows"
)

const caption757 = "Synthetic translated caption."
const text757 = "@synthetic.txt is literal text."
const key757 = "synthetic-757-loopback"
const device757 = "Synthetic Windows Fixture"
const private757 = "PRIVATE757SERVERBODY"

func mic757() []byte    { return bytes.Repeat([]byte{1, 0}, 4800) }
func file757() []byte   { return bytes.Repeat([]byte{3, 0}, 2400) }
func output757() []byte { return bytes.Repeat([]byte{2, 0, 4, 0}, 292800) }

func wave757(pcm []byte) []byte {
	b := []byte("RIFF\x00\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\xc0\x5d\x00\x00\x80\xbb\x00\x00\x02\x00\x10\x00data\x00\x00\x00\x00")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+len(pcm)))
	binary.LittleEndian.PutUint32(b[40:], uint32(len(pcm)))
	return append(b, pcm...)
}

// These two private executable aliases always resolve inside the fresh fixture directory.
// Their inherited environment is the shipping media helper's restricted environment.
func init() {
	path, err := os.Executable()
	if err != nil {
		return
	}
	role := strings.ToLower(filepath.Base(path))
	if role != "ffmpeg.exe" && role != "ffplay.exe" {
		return
	}
	if err := helper757(role, filepath.Dir(path)); err != nil {
		os.Exit(77)
	}
	os.Exit(0)
}

func helper757(role, folder string) error {
	if err := os.WriteFile(filepath.Join(folder, role+".started"), []byte("observed"), 0600); err != nil {
		return err
	}
	for _, setting := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(setting), "OPENAI_") {
			return errors.New("unexpected helper environment")
		}
	}
	args := strings.Join(os.Args[1:], " ")
	marker := func(name string) error {
		return os.WriteFile(filepath.Join(folder, name), []byte("observed"), 0600)
	}
	if role == "ffplay.exe" {
		if !strings.Contains(args, "-f s16le") || !strings.Contains(args, "-nofind_stream_info") {
			return errors.New("unexpected playback arguments")
		}
		pcm, err := io.ReadAll(io.LimitReader(os.Stdin, int64(len(output757())+1)))
		if err != nil || !bytes.Equal(pcm, output757()) {
			return errors.New("playback bytes differ")
		}
		return marker("played")
	}
	if strings.Contains(args, "-f dshow -i audio="+device757+" -map") {
		if err := marker("captured"); err != nil {
			return err
		}
		if _, err := os.Stdout.Write(mic757()); err != nil {
			return err
		}
		time.Sleep(time.Minute)
		return nil
	}
	if !strings.Contains(args, "-f wav -protocol_whitelist pipe -i pipe:0") {
		return errors.New("unexpected file decoder arguments")
	}
	input, err := io.ReadAll(io.LimitReader(os.Stdin, 5000))
	if err != nil || !bytes.Equal(input, wave757(file757())) {
		return errors.New("wrong input file")
	}
	if err := marker("decoded"); err != nil {
		return err
	}
	_, err = os.Stdout.Write(file757())
	return err
}

type fixture757 struct {
	mu        sync.Mutex
	events    []string
	failure   error
	scenario  string
	language  string
	attempts  int
	completed chan struct{}
}

func (f *fixture757) record(event string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, event)
}
func (f *fixture757) check(t *testing.T, want ...string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failure != nil || !slices.Equal(f.events, want) {
		t.Fatalf("loopback mismatch: got %v want %v failure=%v", f.events, want, f.failure)
	}
}
func (f *fixture757) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	err := f.serve(w, r)
	if err != nil {
		f.mu.Lock()
		if f.failure == nil {
			f.failure = err
		}
		f.mu.Unlock()
	}
	select {
	case f.completed <- struct{}{}:
	default:
	}
}
func (f *fixture757) waitResponse(t *testing.T) {
	t.Helper()
	select {
	case <-f.completed:
	case <-time.After(6 * time.Second):
		t.Fatal("loopback handler did not finish")
	}
}
func (f *fixture757) serve(w http.ResponseWriter, r *http.Request) error {
	if r.Header.Get("Authorization") != "Bearer "+key757 {
		return errors.New("wrong fixture authorization")
	}
	if r.Method == "POST" && r.URL.Path == "/v1/responses" && f.scenario == "typed" {
		var req struct {
			Input, Model, Instructions string
			Store                      *bool
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return err
		}
		if req.Input != text757 || req.Model != "gpt-5.6-terra" || !strings.Contains(req.Instructions, "French") || req.Store == nil || *req.Store {
			return errors.New("incorrect typed request")
		}
		f.record("responses")
		w.Header().Set("Content-Type", "application/json")
		_, err := fmt.Fprintf(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":%q}]}],"usage":{"input_tokens":0,"output_tokens":4}}`, caption757)
		return err
	}
	if r.Method != "GET" || r.URL.Path != "/v1/realtime/translations" || r.URL.Query().Get("model") != "gpt-realtime-translate" {
		return errors.New("unexpected loopback route")
	}
	f.record("connect")
	f.mu.Lock()
	f.attempts++
	first := f.attempts == 1
	f.mu.Unlock()
	if first && (strings.HasPrefix(f.scenario, "retry") || f.scenario == "exit401") {
		status := 403
		if f.scenario != "retry403" {
			status = 401
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, err := fmt.Fprintf(w, `{"error":{"message":%q}}`, private757)
		return err
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return err
	}
	defer ws.CloseNow()
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	event := func(typ string, fields map[string]any) error {
		if fields == nil {
			fields = map[string]any{}
		}
		fields["type"] = typ
		return wsjson.Write(ctx, ws, fields)
	}
	if f.scenario != "opening" {
		if err := event("session.created", nil); err != nil {
			return err
		}
	}
	for {
		var msg struct {
			Type, Audio string
			Session     struct {
				Audio struct {
					Output struct{ Language string }
					Input  struct{ Transcription struct{ Model string } }
				}
			}
		}
		if err := wsjson.Read(ctx, ws, &msg); err != nil {
			if (f.scenario == "cancel" || f.scenario == "opening") && ctx.Err() == nil && (websocket.CloseStatus(err) != -1 || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || strings.Contains(err.Error(), "forcibly closed")) {
				return nil
			}
			return err
		}
		f.record(msg.Type)
		switch msg.Type {
		case "session.update":
			if msg.Session.Audio.Output.Language != f.language || msg.Session.Audio.Input.Transcription.Model != "gpt-realtime-whisper" {
				return errors.New("incorrect settings")
			}
			err = event("session.updated", nil)
		case "session.input_audio_buffer.append":
			expected := mic757()
			if f.scenario == "file" {
				expected = file757()
			}
			if msg.Audio != base64.StdEncoding.EncodeToString(expected) {
				return errors.New("incorrect capture bytes")
			}
			if err = event("session.input_transcript.delta", map[string]any{"delta": "Synthetic original caption."}); err != nil {
				return err
			}
			if err = event("session.output_transcript.delta", map[string]any{"delta": caption757}); err != nil {
				return err
			}
			if f.scenario != "cancel" {
				err = event("session.output_audio.delta", map[string]any{"delta": base64.StdEncoding.EncodeToString(output757()), "format": "pcm16", "sample_rate": 24000, "channels": 1})
			}
		case "session.close":
			if err = event("session.output_transcript.delta", map[string]any{"delta": " Final caption."}); err != nil {
				return err
			}
			return event("session.closed", nil)
		default:
			return errors.New("unexpected WebSocket event")
		}
		if err != nil {
			return err
		}
	}
}

type control757 struct {
	Action, Nonce, Text, Absent string
	Changed                     bool
}

func screen757(t *testing.T) string {
	t.Helper()
	cells, width, err := readOutputConsoleCells(os.Stderr)
	var info windows.ConsoleScreenBufferInfo
	if err != nil || windows.GetConsoleScreenBufferInfo(windows.Handle(os.Stderr.Fd()), &info) != nil || width == 0 || int(info.Size.X) != width {
		t.Fatal("cannot read native viewport")
	}
	w := info.Window
	if w.Left < 0 || w.Right < w.Left || int(w.Right) >= width || w.Top < 0 || w.Bottom < w.Top || int(w.Bottom)*width+int(w.Right) >= len(cells) {
		t.Fatal("invalid native viewport")
	}
	var rows []string
	for y := int(w.Top); y <= int(w.Bottom); y++ {
		start := y * width
		rows = append(rows, string(utf16.Decode(cells[start+int(w.Left):start+int(w.Right)+1])))
	}
	return strings.Join(rows, "\n")
}

// The witness remains inside this private ConPTY while the untouched shipping executable runs.
func TestWindows757ConsoleWitness(t *testing.T) {
	address := os.Getenv("OPENAI_ADMIN_CONSOLE_CONTROL")
	if address == "" {
		t.Skip("requires owned test console")
	}
	conn := adminConsoleDial(t, address)
	defer conn.Close()
	before := adminConsoleGetTestModes(t)
	nonce := os.Getenv("OPENAI_ADMIN_CONSOLE_NONCE")
	adminConsoleSend(t, conn, adminConsoleTestFrame{Event: "hello", Nonce: nonce, Original: before})
	var request control757
	if conn.decoder.Decode(&request) != nil || request.Action != "start" || request.Nonce != nonce {
		t.Fatal("missing authenticated start")
	}
	folder := os.Getenv("FIXTURE_757_DIR")
	var args []string
	if json.Unmarshal([]byte(os.Getenv("FIXTURE_757_ARGS")), &args) != nil {
		t.Fatal("invalid child arguments")
	}
	executable := os.Getenv("OPENAI_757_SHIPPING_EXE")
	if !filepath.IsAbs(executable) || !filepath.IsAbs(folder) {
		t.Fatal("absolute paths required")
	}
	child := exec.CommandContext(t.Context(), executable, args...)
	child.Dir = folder
	for _, setting := range adminConsoleEnvironment(folder) {
		name, _, _ := strings.Cut(setting, "=")
		switch strings.ToUpper(name) {
		case "SYSTEMROOT", "WINDIR", "SYSTEMDRIVE", "TERM", "NO_COLOR", "HOME", "APPDATA", "LOCALAPPDATA", "USERPROFILE":
			child.Env = append(child.Env, setting)
		}
	}
	child.Env = append(child.Env, "PATH="+folder, "PATHEXT=.EXE", "NoDefaultCurrentDirectoryInExePath=1", "TEMP="+folder, "TMP="+folder, "OPENAI_UNTRUSTED_STDIN=false")
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	reaped := false
	previousScreen := ""
	defer func() {
		if !reaped {
			_ = child.Process.Kill()
			<-done
		}
	}()
	for {
		var req control757
		if conn.decoder.Decode(&req) != nil {
			t.Fatal("lost fixture controller")
		}
		switch req.Action {
		case "wait":
			deadline := time.Now().Add(8 * time.Second)
			for {
				screen := screen757(t)
				if strings.Contains(screen, private757) {
					t.Fatal("private response on native screen")
				}
				if strings.Contains(screen, req.Text) && (req.Absent == "" || !strings.Contains(screen, req.Absent)) && (!req.Changed || previousScreen != screen) {
					previousScreen = screen
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("expected screen text %q", req.Text)
				}
				time.Sleep(20 * time.Millisecond)
			}
			adminConsoleSend(t, conn, adminConsoleTestFrame{Event: "screen"})
		case "ended":
			select {
			case err := <-done:
				reaped = true
				code := 0
				var exit *exec.ExitError
				if errors.As(err, &exit) {
					code = exit.ExitCode()
				} else if err != nil {
					t.Fatal(err)
				}
				if req.Text != "" && !strings.Contains(screen757(t), req.Text) {
					t.Fatal("missing command exit guidance on native viewport")
				}
				adminConsoleSend(t, conn, adminConsoleTestFrame{Event: "ended", Exit: code, Restored: adminConsoleGetTestModes(t)})
				return
			case <-time.After(8 * time.Second):
				t.Fatal("shipping executable failed to exit")
			}
		default:
			t.Fatal("unknown controller action")
		}
	}
}

func requireMarker757(t *testing.T, folder, name string, present bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(folder, name))
	if present && (err != nil || string(b) != "observed") || !present && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected %s receipt (wanted %t)", name, present)
	}
}
func assertExport757(t *testing.T, folder string, typed bool) {
	t.Helper()
	entries, err := os.ReadDir(folder)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		t.Fatal("expected one export directory")
	}
	dir := filepath.Join(folder, entries[0].Name())
	wantOriginal, wantTranslation := "Synthetic original caption.", caption757+" Final caption."
	if typed {
		wantOriginal, wantTranslation = text757, caption757
	}
	for name, want := range map[string]string{"original.txt": wantOriginal, "translation.txt": wantTranslation} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(b) != want {
			t.Fatalf("incorrect %s", name)
		}
	}
	var metadata struct{ Complete bool }
	b, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil || json.Unmarshal(b, &metadata) != nil || !metadata.Complete {
		t.Fatal("missing completion metadata")
	}
	b, err = os.ReadFile(filepath.Join(dir, "translation.wav"))
	if typed {
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatal("typed text created audio")
		}
	} else if err != nil || len(b) < 44 || !bytes.Equal(b[44:], output757()) || !bytes.Equal(b[:4], []byte("RIFF")) {
		t.Fatal("exported WAV bytes differ")
	}
}

func run757(t *testing.T, exe, scenario string) {
	t.Helper()
	folder := t.TempDir()
	save := filepath.Join(folder, "saved")
	witness, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if scenario != "typed" {
		for _, name := range []string{"ffmpeg.exe", "ffplay.exe"} {
			// Both directories normally use the runner's temp volume. A copy handles split volumes.
			dst := filepath.Join(folder, name)
			if err := os.Link(witness, dst); err != nil {
				src, err := os.Open(witness)
				if err != nil {
					t.Fatal(err)
				}
				out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
				if err != nil {
					src.Close()
					t.Fatal(err)
				}
				_, err = io.Copy(out, src)
				closeErr := out.Close()
				src.Close()
				if err != nil || closeErr != nil {
					t.Fatal("helper copy failed")
				}
			}
		}
	}
	language := "en"
	switch scenario {
	case "typed", "file":
		language = "fr"
	case "retry401", "retry403", "exit401":
		language = "hi"
	}
	f := &fixture757{scenario: scenario, language: language, completed: make(chan struct{}, 4)}
	server := httptest.NewServer(f)
	defer server.Close()
	args := []string{"--api-key", key757, "--base-url", server.URL + "/v1/", "audio", "translate-live", "--output-dir", save, "--target-language", language}
	switch scenario {
	case "file":
		p := filepath.Join(folder, "synthetic input.wav")
		if err := os.WriteFile(p, wave757(file757()), 0600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "--file", p, "--no-playback")
	case "typed":
		args = append(args, "--no-playback")
	default:
		args = append(args, "--input-device", device757)
		if scenario != "microphone" {
			args = append(args, "--no-playback")
		}
	}
	encoded, _ := json.Marshal(args)
	s := adminConsoleStart(t, witness, "TestWindows757ConsoleWitness", []string{"OPENAI_757_SHIPPING_EXE=" + exe, "FIXTURE_757_DIR=" + folder, "FIXTURE_757_ARGS=" + string(encoded)})
	s.send(t, control757{Action: "start", Nonce: s.nonce})
	wait := func(text string) {
		t.Helper()
		s.send(t, control757{Action: "wait", Text: text})
		if s.receive(t).Event != "screen" {
			t.Fatal("missing screen acknowledgement")
		}
	}
	key := func(k string) { t.Helper(); s.write(t, "fixture", k) }
	initial := "Start microphone"
	if scenario == "file" {
		initial = "Translate file"
	}
	wait(initial)
	f.check(t)
	requireMarker757(t, folder, "captured", false)
	requireMarker757(t, folder, "ffmpeg.exe.started", false)
	requireMarker757(t, folder, "ffplay.exe.started", false)
	if scenario == "typed" {
		key("\x1b[D")
		wait("[Typed text]")
		key("\r")
		wait("Type or paste text")
		key(text757)
		key("\r")
		wait("Translate text")
		f.check(t)
	}
	key("\r")
	if strings.HasPrefix(scenario, "retry") || scenario == "exit401" {
		wait("Retry translation")
		if scenario == "retry403" {
			wait("This API endpoint denied access.")
		} else {
			wait("Authentication failed for this API endpoint.")
		}
		wait("Check the credential required by this API endpoint.")
		f.waitResponse(t)
		wait("Hindi")
		f.check(t, "connect")
		requireMarker757(t, folder, "captured", false)
		requireMarker757(t, folder, "ffmpeg.exe.started", false)
		requireMarker757(t, folder, "ffplay.exe.started", false)
		if scenario != "exit401" {
			key("\r")
		}
	}
	exit := 0
	switch scenario {
	case "exit401":
		key("\x1b")
		exit = 1
	case "typed":
		wait(caption757)
		wait("Input 0 tokens")
		wait("S save")
		key("s")
		wait("Saved  ")
		key("\x1b")
	case "opening":
		wait("Opening the translation session")
		deadline := time.Now().Add(6 * time.Second)
		for {
			f.mu.Lock()
			connected := len(f.events) > 0
			f.mu.Unlock()
			if connected {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("opening never connected")
			}
			time.Sleep(15 * time.Millisecond)
		}
		key("\x1b")
		wait("back to setup")
		key("\r")
		s.send(t, control757{Action: "wait", Text: initial, Absent: "back to setup", Changed: true})
		if s.receive(t).Event != "screen" {
			t.Fatal("setup did not return")
		}
		key("\x1b")
	case "cancel":
		wait(caption757)
		key("\x03")
		exit = 130
	default:
		if scenario != "file" {
			wait(caption757)
			key(" ")
		}
		wait("Final caption.")
		wait("S save")
		key("s")
		wait("Saved  ")
		key("\x1b")
	}
	expectedGuidance := ""
	if scenario == "exit401" {
		expectedGuidance = "Check the credential required by this API endpoint."
	}
	s.send(t, control757{Action: "ended", Text: expectedGuidance})
	ended := s.receive(t)
	if ended.Event != "ended" || ended.Exit != exit || ended.Restored != s.hello.Original {
		t.Fatalf("exit or native modes differ (wanted %d, got %d)", exit, ended.Exit)
	}
	stream := s.finish(t)
	if strings.Contains(stream, private757) {
		t.Fatal("private diagnostic appeared in console stream")
	}
	if scenario != "exit401" {
		f.waitResponse(t)
	}
	want := []string{"connect", "session.update", "session.input_audio_buffer.append", "session.close"}
	switch scenario {
	case "typed":
		want = []string{"responses"}
	case "opening", "exit401":
		want = []string{"connect"}
	case "cancel":
		want = want[:3]
	case "retry401", "retry403":
		want = append([]string{"connect"}, want...)
	}
	f.check(t, want...)
	requireMarker757(t, folder, "captured", scenario != "typed" && scenario != "file" && scenario != "opening" && scenario != "exit401")
	requireMarker757(t, folder, "decoded", scenario == "file")
	requireMarker757(t, folder, "played", scenario == "microphone")
	requireMarker757(t, folder, "ffplay.exe.started", scenario == "microphone")
	requireMarker757(t, folder, "ffmpeg.exe.started", scenario != "typed" && scenario != "opening" && scenario != "exit401")
	if scenario != "cancel" && scenario != "opening" && scenario != "exit401" {
		assertExport757(t, save, scenario == "typed")
	}
	t.Log("PASS757 " + scenario)
}

func TestWindows757ShippingTranslation(t *testing.T) {
	exe := os.Getenv("OPENAI_757_SHIPPING_EXE")
	if !filepath.IsAbs(exe) {
		t.Fatal("set OPENAI_757_SHIPPING_EXE to the absolute shipping executable")
	}
	if info, err := os.Stat(exe); err != nil || !info.Mode().IsRegular() {
		t.Fatal("shipping executable missing")
	}
	adminConsoleTestWorker(t, "TestWindows757ShippingTranslation", "./internal/commands/audio", func(t *testing.T) {
		for _, name := range []string{"microphone", "file", "typed", "opening", "retry401", "retry403", "exit401", "cancel"} {
			t.Run(name, func(t *testing.T) { run757(t, exe, name) })
		}
	}, "OPENAI_757_SHIPPING_EXE="+exe)
}
