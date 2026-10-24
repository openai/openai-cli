package tokenize

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/tokenizer"
)

// The test executable supplies the same disposable child boundary without
// changing production startup or depending on another executable on PATH.
func init() {
	mode := os.Getenv("OPENAI_TEST_TOKENIZER_PREVIEW")
	if mode == "" || len(os.Args) != 3 || os.Args[1] != "tokenizer" || os.Args[2] != "__preview" {
		return
	}
	if mode == "supervisor" {
		command := exec.Command(os.Args[0], "tokenizer", "__preview")
		command.Env = append(os.Environ(), "OPENAI_TEST_TOKENIZER_PREVIEW=worker")
		command.Stdout = io.Discard
		command.Stderr = io.Discard
		input, err := command.StdinPipe()
		if err != nil || command.Start() != nil {
			os.Exit(2)
		}
		if err := writeTokenizerPreviewInput(input, strings.Repeat("a", tokenizer.MaxInputBytes), 0); err != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
			os.Exit(3)
		}
		fmt.Fprintln(os.Stdout, command.Process.Pid)
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(4)
	}
	if mode == "failure" {
		os.Exit(17)
	}
	if mode == "malformed" {
		_, _ = io.WriteString(os.Stdout, "invalid helper output")
		os.Exit(0)
	}
	var output io.Writer = os.Stdout
	stopObserver := func() {}
	if mode == "observe-work" {
		ready := os.Getenv("OPENAI_TEST_TOKENIZER_PREVIEW_READY")
		output = &tokenizerObservedOutput{Writer: os.Stdout, marker: ready + ".output"}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); observeTokenizerMerge(ctx, ready) }()
		stopObserver = func() { cancel(); <-done }
	}
	err := serveTokenizerPreview(context.Background(), os.Stdin, output)
	stopObserver()
	if completed := os.Getenv("OPENAI_TEST_TOKENIZER_PREVIEW_COMPLETED"); completed != "" {
		_ = os.WriteFile(completed, []byte("stopped"), 0600)
	}
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestTokenizerPreviewMatchesExactEncodings(t *testing.T) {
	t.Setenv("OPENAI_TEST_TOKENIZER_PREVIEW", "worker")
	for _, encoding := range tokenizer.SupportedEncodings() {
		for _, input := range []string{"", "Hello, world!", "    indented", strings.Repeat(" ", 25), "漢字👩‍💻e\u0301\r\n\x00<|endoftext|>", "\ufeffa\n", strings.Repeat("a\n", tokenizer.MaxInputBytes/2)} {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			got, err := runTokenizerPreview(ctx, os.Args[0], input, encoding)
			cancel()
			if err != nil {
				t.Fatalf("%s, %d bytes: %v", encoding, len(input), err)
			}
			if len(input) == tokenizer.MaxInputBytes {
				// The official tiktoken vocabularies assign a=64 and newline=198
				// in all four encodings. Their reference patterns split a\n into
				// separate pieces, so this oracle needs no second large encode.
				// Reference: github.com/openai/tiktoken/blob/0.14.0/tiktoken_ext/openai_public.py.
				if err := validateRepeatedNewlinePreview(got, len(input)); err != nil {
					t.Fatalf("%s large preview: %v", encoding, err)
				}
				continue
			}
			want, err := tokenizer.Encode(input, encoding, true)
			if err != nil || len(got) != want.TokenCount {
				t.Fatalf("unexpected token count: %d versus %d, %v", len(got), want.TokenCount, err)
			}
			offset := 0
			for i, token := range got {
				if uint(token.ID) != want.IDs[i] || input[offset:token.EndByte] != want.Fragments[i] {
					t.Fatalf("token %d changed ID or bytes", i)
				}
				offset = int(token.EndByte)
			}
		}
	}
}

func validateRepeatedNewlinePreview(tokens []tokenizerPreviewToken, size int) error {
	if len(tokens) != size {
		return fmt.Errorf("token count %d, want %d", len(tokens), size)
	}
	for i, token := range tokens {
		wantID := uint32(64)
		if i%2 == 1 {
			wantID = 198
		}
		if token.ID != wantID || token.EndByte != uint32(i+1) {
			return fmt.Errorf("token %d: got %+v, want ID %d ending at byte %d", i, token, wantID, i+1)
		}
	}
	return nil
}

func TestTokenizerPreviewCancelAndReplace(t *testing.T) {
	t.Setenv("OPENAI_TEST_TOKENIZER_PREVIEW", "worker")
	worker := newTokenizerPreviewWorker(t.Context(), os.Args[0])
	defer worker.Close()
	for revision := uint64(1); revision <= 10; revision++ {
		worker.Replace(revision, strings.Repeat("a", tokenizer.MaxInputBytes), "o200k_base")
	}
	worker.Replace(11, "Hello, world!", "cl100k_base")
	select {
	case result := <-worker.Results():
		if result.Revision != 11 || result.Err != nil || len(result.Tokens) != 4 {
			t.Fatalf("replacement result: %+v", result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("replacement did not complete")
	}
	worker.Replace(12, strings.Repeat("a", tokenizer.MaxInputBytes), "o200k_base")
	started := time.Now()
	if err := worker.Close(); err != nil || time.Since(started) > 3*time.Second {
		t.Fatalf("close did not stop work promptly: %v", err)
	}
	worker.Replace(13, "ignored after close", "o200k_base")
}

func TestTokenizerPreviewCancellationDuringChildWork(t *testing.T) {
	assertTokenizerCancellationDuringMerge(t, strings.Repeat("a", tokenizer.MaxInputBytes), "o200k_base")
}

func TestTokenizerPreviewPreservesDeadline(t *testing.T) {
	t.Setenv("OPENAI_TEST_TOKENIZER_PREVIEW", "worker")
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	tokens, err := runTokenizerPreview(ctx, os.Args[0], strings.Repeat("a", tokenizer.MaxInputBytes), "o200k_base")
	// This case preserves deadline identity. The observed-work cases separately
	// establish that cancellation interrupts real BPE computation.
	if !errors.Is(err, context.DeadlineExceeded) || tokens != nil || time.Since(started) > 3*time.Second {
		t.Fatalf("deadline was lost or delayed: %v", err)
	}
}

func assertTokenizerCancellationDuringMerge(t *testing.T, text, encoding string) {
	t.Helper()
	t.Setenv("OPENAI_TEST_TOKENIZER_PREVIEW", "observe-work")
	ready := filepath.Join(t.TempDir(), "merging")
	t.Setenv("OPENAI_TEST_TOKENIZER_PREVIEW_READY", ready)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type result struct {
		tokens []tokenizerPreviewToken
		err    error
	}
	done := make(chan result, 1)
	go func() {
		tokens, err := runTokenizerPreview(ctx, os.Args[0], text, encoding)
		done <- result{tokens, err}
	}()
	waited := false
	defer func() {
		cancel()
		if !waited {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("preview child did not finish cleanup")
			}
		}
	}()
	// Startup and vocabulary loading cannot satisfy this prerequisite. The
	// test-only observer must witness the real BPE merger on the encoder stack.
	readiness, stopReadiness := context.WithTimeout(t.Context(), 20*time.Second)
	defer stopReadiness()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(ready)
		if err == nil && string(data) == "merging" {
			break
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case got := <-done:
			waited = true
			t.Fatalf("preview exited before observed computation: %v", got.err)
		case <-readiness.Done():
			t.Fatalf("BPE computation was not observed: %v; observer=%q", readiness.Err(), data)
		case <-ticker.C:
		}
	}
	started := time.Now()
	cancel()
	select {
	case got := <-done:
		waited = true
		if !errors.Is(got.err, context.Canceled) || got.tokens != nil || time.Since(started) > 3*time.Second {
			t.Fatalf("cancellation was lost or delayed: %v", got.err)
		}
		if _, err := os.Stat(ready + ".output"); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("encoding reached publication before cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not reap the computing child")
	}
}

// Observe actual merge work without adding test hooks to the production encoder.
// Both frames must belong to one goroutine; startup and regex preparation do not qualify.
func observeTokenizerMerge(ctx context.Context, ready string) {
	stack := make([]byte, 64*1024)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		n := runtime.Stack(stack, true)
		if n == len(stack) {
			if len(stack) == 1024*1024 {
				_ = os.WriteFile(ready, []byte("truncated stack"), 0600)
				return
			}
			stack = make([]byte, len(stack)*2)
			continue
		}
		for _, goroutine := range strings.Split(string(stack[:n]), "\n\n") {
			if strings.Contains(goroutine, "github.com/pkoukk/tiktoken-go.bytePairMerge[") &&
				strings.Contains(goroutine, "github.com/openai/openai-cli/internal/tokenizer.(*textEncoding).encodePiece(") {
				if err := os.WriteFile(ready, []byte("merging"), 0600); err != nil {
					os.Exit(2)
				}
				return
			}
		}
	}
}

type tokenizerObservedOutput struct {
	io.Writer
	marker  string
	started bool
}

func (w *tokenizerObservedOutput) Write(data []byte) (int, error) {
	if !w.started {
		if err := os.WriteFile(w.marker, []byte("output started"), 0600); err != nil {
			return 0, err
		}
		w.started = true
	}
	return w.Writer.Write(data)
}

func TestTokenizerPreviewLegacyCancellationDuringNumericWork(t *testing.T) {
	for _, encoding := range []string{"r50k_base", "p50k_base"} {
		t.Run(encoding, func(t *testing.T) {
			assertTokenizerCancellationDuringMerge(t, strings.Repeat("1234567890", tokenizer.MaxInputBytes/10), encoding)
		})
	}
}

func TestTokenizerPreviewCancelRetiresPendingAndCompletedResults(t *testing.T) {
	t.Setenv("OPENAI_TEST_TOKENIZER_PREVIEW", "worker")
	worker := newTokenizerPreviewWorker(t.Context(), os.Args[0])
	defer worker.Close()
	worker.Replace(1, strings.Repeat("a", tokenizer.MaxInputBytes), "o200k_base")
	worker.Cancel()
	worker.Replace(2, "Hello", "o200k_base")
	select {
	case result := <-worker.Results():
		if result.Revision != 2 || result.Err != nil {
			t.Fatalf("retired work returned: %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("replacement did not complete")
	}
	// Cancel also discards a completed result that has not reached the UI.
	worker.Replace(3, "new text", "cl100k_base")
	deadline := time.Now().Add(5 * time.Second)
	for len(worker.results) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("result did not become ready")
		}
		time.Sleep(time.Millisecond)
	}
	worker.Cancel()
	select {
	case <-worker.Results():
		t.Fatal("canceled result remained buffered")
	default:
	}
	worker.Replace(4, "retry", "cl100k_base")
	select {
	case result := <-worker.Results():
		if result.Revision != 4 || result.Err != nil {
			t.Fatalf("retry after cancel failed: %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("retry after cancel did not complete")
	}
}

func TestTokenizerPreviewStopsAfterParentDeath(t *testing.T) {
	t.Setenv("OPENAI_TEST_TOKENIZER_PREVIEW", "supervisor")
	completed := filepath.Join(t.TempDir(), "child-completed")
	t.Setenv("OPENAI_TEST_TOKENIZER_PREVIEW_COMPLETED", completed)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	supervisor := exec.CommandContext(ctx, os.Args[0], "tokenizer", "__preview")
	input, err := supervisor.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := supervisor.StdoutPipe()
	if err != nil || supervisor.Start() != nil {
		t.Fatalf("start supervisor: %v", err)
	}
	defer func() { _ = supervisor.Process.Kill(); _ = supervisor.Wait() }()
	var childPID int
	if _, err := fmt.Fscanln(output, &childPID); err != nil || childPID <= 0 {
		t.Fatalf("supervisor did not submit the complete input: %v", err)
	}
	child, err := os.FindProcess(childPID)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Release()
	// A failed test must not leave a computing child behind.
	defer func() {
		if _, err := os.Stat(completed); err != nil {
			_ = child.Kill()
		}
	}()
	if err := supervisor.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = supervisor.Wait()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if data, err := os.ReadFile(completed); err == nil && string(data) == "stopped" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not stop after abrupt parent death")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTokenizerPreviewRejectsMalformedResults(t *testing.T) {
	valid := func(count uint32, pairs ...uint32) []byte {
		var b bytes.Buffer
		b.Write(tokenizerPreviewMagic[:])
		_ = binary.Write(&b, binary.BigEndian, count)
		for _, value := range pairs {
			_ = binary.Write(&b, binary.BigEndian, value)
		}
		return b.Bytes()
	}
	for name, data := range map[string][]byte{
		"missing header":    nil,
		"bad header":        []byte("xxxxxxxxxxxx"),
		"oversize count":    valid(2),
		"missing token":     valid(1),
		"zero boundary":     valid(1, 1, 0),
		"oversize boundary": valid(1, 1, 2),
		"invalid token ID":  valid(1, 199998, 1),
		"incomplete input":  valid(0),
		"trailing bytes":    append(valid(1, 1, 1), 0),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := readTokenizerPreviewOutput(bytes.NewReader(data), 1, 0); err == nil {
				t.Fatal("malformed worker response succeeded")
			}
		})
	}
	invalidCl100k := valid(1, 100256, 1)
	invalidCl100k[7] = 1
	if _, err := readTokenizerPreviewOutput(bytes.NewReader(invalidCl100k), 1, 1); err == nil {
		t.Fatal("cl100k token outside its vocabulary succeeded")
	}
}

func TestTokenizerPreviewRejectsMalformedRequests(t *testing.T) {
	frame := func(text string, encoding byte) []byte {
		var b bytes.Buffer
		if err := writeTokenizerPreviewInput(&b, text, encoding); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	oversize := frame("", 0)
	binary.BigEndian.PutUint32(oversize[8:], tokenizer.MaxInputBytes+1)
	for name, data := range map[string][]byte{
		"empty": nil, "bad magic": []byte("xxxxxxxxxxxx"),
		"unknown encoding": frame("a", 4), "oversize": oversize,
		"invalid UTF8": frame("\xff", 0), "missing bytes": frame("a", 0)[:12],
	} {
		t.Run(name, func(t *testing.T) {
			if err := serveTokenizerPreview(t.Context(), io.NopCloser(bytes.NewReader(data)), io.Discard); err == nil {
				t.Fatal("malformed worker input succeeded")
			}
		})
	}
}

func TestTokenizerPreviewRejectsSpecialAndOutOfVocabularyIDs(t *testing.T) {
	for _, invalid := range []struct {
		encoding byte
		id       uint32
	}{
		{0, 199998}, {1, 100256}, {2, 50256}, {3, 50256}, {3, 50281},
	} {
		t.Run(fmt.Sprintf("%d/%d", invalid.encoding, invalid.id), func(t *testing.T) {
			frame := make([]byte, 20)
			copy(frame, tokenizerPreviewMagic[:])
			frame[7] = invalid.encoding
			binary.BigEndian.PutUint32(frame[8:], 1)
			binary.BigEndian.PutUint32(frame[12:], invalid.id)
			binary.BigEndian.PutUint32(frame[16:], 1)
			if _, err := readTokenizerPreviewOutput(bytes.NewReader(frame), 1, invalid.encoding); err == nil {
				t.Fatal("invalid ordinary token ID was accepted")
			}
			result := tokenizer.Result{TokenCount: 1, IDs: []uint{uint(invalid.id)}, Fragments: []string{"x"}}
			if err := writeTokenizerPreviewOutput(io.Discard, result, invalid.encoding); err == nil {
				t.Fatal("invalid ordinary token ID was emitted")
			}
		})
	}
}

func TestTokenizerPreviewFailuresStayPrivate(t *testing.T) {
	for _, mode := range []string{"failure", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("OPENAI_TEST_TOKENIZER_PREVIEW", mode)
			_, err := runTokenizerPreview(t.Context(), os.Args[0], "private text", "o200k_base")
			if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), os.Args[0]) {
				t.Fatalf("unsafe or missing failure: %v", err)
			}
			if errors.Is(err, context.Canceled) {
				t.Fatal("internal cleanup replaced a worker failure with user cancellation")
			}
		})
	}
	if _, err := runTokenizerPreview(t.Context(), filepath.Join(t.TempDir(), "missing"), "private", "o200k_base"); err == nil {
		t.Fatal("missing executable succeeded")
	}
}

type tokenizerPreviewFailWriter struct{ short bool }

func (w tokenizerPreviewFailWriter) Write(data []byte) (int, error) {
	if w.short {
		return len(data) / 2, nil
	}
	return 0, io.ErrClosedPipe
}

func TestTokenizerPreviewWriteFailures(t *testing.T) {
	result, err := tokenizer.Encode("Hello", "o200k_base", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, short := range []bool{false, true} {
		writer := tokenizerPreviewFailWriter{short}
		if err := writeTokenizerPreviewInput(writer, "Hello", 0); err == nil {
			t.Fatal("request write failure disappeared")
		}
		if err := writeTokenizerPreviewOutput(writer, result, 0); err == nil {
			t.Fatal("result write failure disappeared")
		}
	}
}

func TestTokenizerPreviewRepeatedCloseDoesNotAccumulateGoroutines(t *testing.T) {
	t.Setenv("OPENAI_TEST_TOKENIZER_PREVIEW", "worker")
	before := runtime.NumGoroutine()
	for range 10 {
		worker := newTokenizerPreviewWorker(t.Context(), os.Args[0])
		worker.Replace(1, "Hello", "o200k_base")
		select {
		case result := <-worker.Results():
			if result.Err != nil {
				t.Fatal(result.Err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not finish")
		}
		_ = worker.Close()
		_ = worker.Close()
	}
	if after := runtime.NumGoroutine(); after > before+2 {
		t.Fatalf("goroutines grew from %d to %d", before, after)
	}
}
