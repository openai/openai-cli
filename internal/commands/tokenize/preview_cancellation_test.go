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
	"strings"
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
	// Include decoder read-ahead. Any encoder publication emits a control byte.
	trailing, err := io.ReadAll(io.LimitReader(io.MultiReader(decoder.Buffered(), connection), 1024))
	if err != nil || strings.TrimSpace(string(trailing)) != "" {
		t.Fatal(fmt.Sprintf("encoding published output or retained its control connection: bytes=%d error=%v", len(trailing), err))
	}
}
