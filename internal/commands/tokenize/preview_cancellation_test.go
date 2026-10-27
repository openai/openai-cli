package tokenize

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/tokenizer"
)

type tokenizerWorkEntry struct {
	Nonce    string
	PID      int
	Digest   [sha256.Size]byte
	Encoding string
	Inspect  bool
}

// This test-only child uses production parsing, pipes, cancellation, and Wait.
// Encoder entry proves that the entire invocation-specific request was validated.
func runControlledTokenizerPreview() {
	connection, err := net.DialTimeout("tcp4", os.Getenv("OPENAI_TEST_TOKENIZER_CONTROL"), 30*time.Second)
	if err != nil {
		os.Exit(2)
	}
	defer connection.Close()
	encode := func(text, encoding string, inspect bool) (tokenizer.Result, error) {
		entry := tokenizerWorkEntry{
			Nonce: os.Getenv("OPENAI_TEST_TOKENIZER_NONCE"), PID: os.Getpid(),
			Digest: sha256.Sum256([]byte(text)), Encoding: encoding, Inspect: inspect,
		}
		if err := json.NewEncoder(connection).Encode(entry); err != nil {
			os.Exit(3)
		}
		// An uncooperative synchronous encoder cannot finish or observe cancel.
		// Only the production parent/child lifecycle can terminate this process.
		select {}
	}
	output := &tokenizerControlOutput{Writer: os.Stdout, control: connection}
	err = serveTokenizerPreviewWithEncoder(context.Background(), os.Stdin, output, encode)
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

type tokenizerControlOutput struct {
	io.Writer
	control io.Writer
}

func (output *tokenizerControlOutput) Write(data []byte) (int, error) {
	if _, err := io.WriteString(output.control, "unexpected output\n"); err != nil {
		return 0, err
	}
	return output.Writer.Write(data)
}

func TestTokenizerPreviewCancellationDuringChildWork(t *testing.T) {
	for _, encoding := range []string{"o200k_base", "cl100k_base"} {
		t.Run(encoding, func(t *testing.T) {
			assertTokenizerCancellationAtEncoderEntry(t, "synthetic 漢字 input\n", encoding)
		})
	}
}

func TestTokenizerPreviewLegacyCancellationDuringNumericWork(t *testing.T) {
	for _, encoding := range []string{"r50k_base", "p50k_base"} {
		t.Run(encoding, func(t *testing.T) {
			assertTokenizerCancellationAtEncoderEntry(t, "1234567890", encoding)
		})
	}
}

func assertTokenizerCancellationAtEncoderEntry(t *testing.T, text, encoding string) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	// This watchdog contains deadlocks. It does not determine work readiness.
	watchdog, stopWatchdog := context.WithTimeout(t.Context(), 30*time.Second)
	defer stopWatchdog()
	stopListener := context.AfterFunc(watchdog, func() { _ = listener.Close() })
	defer stopListener()
	nonce := rand.Text()
	t.Setenv("OPENAI_TEST_TOKENIZER_PREVIEW", "controlled-work")
	t.Setenv("OPENAI_TEST_TOKENIZER_CONTROL", listener.Addr().String())
	t.Setenv("OPENAI_TEST_TOKENIZER_NONCE", nonce)
	ctx, cancel := context.WithCancel(watchdog)
	defer cancel()
	type result struct {
		tokens []tokenizerPreviewToken
		err    error
	}
	done := make(chan result, 1)
	go func() {
		tokens, err := runTokenizerPreview(ctx, os.Args[0], text, encoding)
		done <- result{tokens, err}
		_ = listener.Close() // An early child failure also unblocks Accept.
	}()
	waited := false
	defer func() {
		cancel()
		if !waited {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("controlled preview child did not finish cleanup")
			}
		}
	}()
	connection, err := listener.Accept()
	if err != nil {
		t.Fatalf("preview control connection failed: %v; watchdog=%v", err, watchdog.Err())
	}
	defer connection.Close()
	deadline, _ := watchdog.Deadline()
	if err := connection.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(io.LimitReader(connection, 1024))
	var entry tokenizerWorkEntry
	if err := decoder.Decode(&entry); err != nil {
		t.Fatalf("preview did not report encoder entry: %v", err)
	}
	if entry.Nonce != nonce || entry.PID <= 0 || entry.Digest != sha256.Sum256([]byte(text)) || entry.Encoding != encoding || !entry.Inspect {
		t.Fatal("encoder entry did not match this complete request")
	}
	cancel()
	select {
	case got := <-done:
		waited = true
		var exit *exec.ExitError
		if !errors.Is(got.err, context.Canceled) || got.tokens != nil || !errors.As(got.err, &exit) {
			t.Fatalf("cancellation lost its identity, published tokens, or omitted child Wait: %v", got.err)
		}
		if exit.ProcessState.Pid() != entry.PID || exit.ProcessState.Success() {
			t.Fatal("cancellation did not reap the observed computing child")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not reap the computing child")
	}
	// Check only after Wait confirms the observed child was reaped. Windows can
	// reset its socket after termination instead of sending a graceful EOF.
	if err := tokenizerControlCompletion(io.MultiReader(decoder.Buffered(), connection)); err != nil {
		t.Fatal(err)
	}
}

func tokenizerControlCompletion(input io.Reader) error {
	// Include decoder read-ahead, including Encode's trailing newline. Reject
	// truncation so whitespace cannot hide a later publication marker.
	trailing, err := io.ReadAll(io.LimitReader(input, 1025))
	if len(trailing) > 1024 || strings.TrimSpace(string(trailing)) != "" {
		return fmt.Errorf("encoding published output or exceeded its control bound: bytes=%d", len(trailing))
	}
	if err != nil && !tokenizerControlReset(err) {
		return fmt.Errorf("control connection after child reaping: %w", err)
	}
	return nil
}

func tokenizerControlReset(err error) bool {
	reset := syscall.ECONNRESET
	if runtime.GOOS == "windows" {
		// WSAECONNRESET from Go's syscall/types_windows.go. Windows ECONNRESET
		// belongs to a different, synthetic errno range and cannot match it.
		reset = syscall.Errno(10054)
	}
	return errors.Is(err, reset)
}

func TestTokenizerPreviewControlCompletion(t *testing.T) {
	var reset error
	t.Run("forced_reset", func(t *testing.T) {
		listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		deadline := time.Now().Add(5 * time.Second)
		if err := listener.SetDeadline(deadline); err != nil {
			t.Fatal(err)
		}
		client, err := net.DialTimeout("tcp4", listener.Addr().String(), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		server, err := listener.AcceptTCP()
		if err != nil {
			t.Fatal(err)
		}
		defer server.Close()
		if err := server.SetDeadline(deadline); err != nil {
			t.Fatal(err)
		}
		if err := client.(*net.TCPConn).SetLinger(0); err != nil {
			t.Fatal(err)
		}
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
		var data [1]byte
		count, err := server.Read(data[:])
		if count != 0 || !tokenizerControlReset(err) {
			t.Fatalf("forced close did not produce a TCP reset: bytes=%d error=%v", count, err)
		}
		reset = err
	})
	if reset == nil {
		t.Fatal("reset regression requires the observed socket error")
	}
	for _, test := range []struct {
		name string
		data string
		err  error
		want bool
	}{
		{"eof", "\n", io.EOF, true},
		{"wrapped_reset", "\n", fmt.Errorf("control: %w", reset), true},
		{"unexpected_bytes", "\nunexpected output\n", reset, false},
		{"timeout", "\n", os.ErrDeadlineExceeded, false},
		{"other_error", "\n", io.ErrClosedPipe, false},
		{"oversize", strings.Repeat(" ", 1025) + "unexpected output", reset, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := io.MultiReader(strings.NewReader(test.data), tokenizerControlReadError{test.err})
			err := tokenizerControlCompletion(input)
			if (err == nil) != test.want {
				t.Fatalf("control completion success=%t, want %t: %v", err == nil, test.want, err)
			}
		})
	}
}

type tokenizerControlReadError struct{ err error }

func (input tokenizerControlReadError) Read([]byte) (int, error) { return 0, input.err }
