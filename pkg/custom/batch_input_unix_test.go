//go:build !windows

package custom

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func batchInputSocketPair(t *testing.T, blocking bool) (*os.File, *os.File) {
	t.Helper()
	descriptors, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range descriptors {
		unix.CloseOnExec(descriptor)
	}
	// Initialize os.File's poll descriptor before modeling an inherited blocking
	// socket. This preserves caller deadline support in both fixture modes.
	if err := unix.SetNonblock(descriptors[0], true); err != nil {
		unix.Close(descriptors[0])
		unix.Close(descriptors[1])
		t.Fatal(err)
	}
	input := os.NewFile(uintptr(descriptors[0]), "synthetic-batch-socket-input")
	producer := os.NewFile(uintptr(descriptors[1]), "synthetic-batch-socket-producer")
	t.Cleanup(func() {
		producer.Close()
		input.Close()
	})
	if blocking {
		connection, err := input.SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		var modeErr error
		if err := connection.Control(func(fd uintptr) {
			modeErr = unix.SetNonblock(int(fd), false)
		}); err != nil || modeErr != nil {
			t.Fatalf("prepare blocking socket: %v / %v", err, modeErr)
		}
	}
	if nonblocking := batchInputPipeFlags(t, input)&unix.O_NONBLOCK != 0; nonblocking == blocking {
		t.Fatal("socket fixture did not establish its requested descriptor mode")
	}
	return input, producer
}

func awaitBatchSocketRead(t *testing.T, producer *os.File, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		// Closing the owned peer releases even the prior synchronous read path.
		producer.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("socket reader remained active after fixture cleanup")
		}
		t.Fatal("socket read watchdog expired before cancellation or caller deadline")
		return nil
	}
}

func TestBatchRequestInputSocketCancellationPreservesBorrowedDescriptor(t *testing.T) {
	for _, blocking := range []bool{false, true} {
		name := "nonblocking"
		if blocking {
			name = "blocking"
		}
		t.Run(name, func(t *testing.T) {
			input, producer := batchInputSocketPair(t, blocking)
			flags := batchInputPipeFlags(t, input)
			for attempt := range 3 {
				ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
				want := error(context.DeadlineExceeded)
				var timer *time.Timer
				if attempt == 1 {
					cancel()
					ctx, cancel = context.WithCancel(t.Context())
					timer = time.AfterFunc(40*time.Millisecond, cancel)
					want = context.Canceled
				}
				var data []byte
				done := make(chan error, 1)
				go func() {
					var err error
					data, err = io.ReadAll(&batchRequestInput{ctx: ctx, file: input})
					done <- err
				}()
				err := awaitBatchSocketRead(t, producer, done)
				cancel()
				if timer != nil {
					timer.Stop()
				}
				if !errors.Is(err, want) || len(data) != 0 || batchInputPipeFlags(t, input) != flags {
					t.Fatalf("attempt %d changed cancellation, bytes, or flags: %q / %v", attempt, data, err)
				}
			}
			// Cancellation must not expire or close the caller's borrowed handle.
			if _, err := io.WriteString(producer, "still open"); err != nil {
				t.Fatal(err)
			}
			data := make([]byte, len("still open"))
			if _, err := io.ReadFull(input, data); err != nil || string(data) != "still open" {
				t.Fatalf("borrowed socket is unusable after cancellation: %q / %v", data, err)
			}
		})
	}
}

func TestBatchRequestInputSocketBytesAndEOF(t *testing.T) {
	for _, blocking := range []bool{false, true} {
		name := "nonblocking"
		if blocking {
			name = "blocking"
		}
		for _, value := range []string{"", "\ufeffa\r\n👩\u200d💻 e\u0301\x00\x1b\xff\t\n"} {
			t.Run(name+"/"+hex.EncodeToString([]byte(value)), func(t *testing.T) {
				input, producer := batchInputSocketPair(t, blocking)
				flags := batchInputPipeFlags(t, input)
				if _, err := io.WriteString(producer, value); err != nil {
					t.Fatal(err)
				}
				if err := producer.Close(); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				reader := &batchRequestInput{ctx: ctx, file: input}
				data, err := io.ReadAll(reader)
				if err != nil || string(data) != value || batchInputPipeFlags(t, input) != flags {
					t.Fatalf("socket bytes or flags changed: %q / %v", data, err)
				}
				if n, err := reader.Read(make([]byte, 1)); n != 0 || err != io.EOF {
					t.Fatalf("Batch EOF changed: n=%d error=%v", n, err)
				}
				if _, err := input.Read(make([]byte, 1)); err != io.EOF {
					t.Fatalf("borrowed socket was closed or lost EOF: %v", err)
				}
			})
		}
	}
}

func TestBatchRequestInputSocketCallerErrors(t *testing.T) {
	for _, blocking := range []bool{false, true} {
		name := "nonblocking"
		if blocking {
			name = "blocking"
		}
		for _, action := range []string{"closed", "expired_deadline", "future_deadline", "cancel_preserves_deadline"} {
			t.Run(name+"/"+action, func(t *testing.T) {
				input, producer := batchInputSocketPair(t, blocking)
				flags := batchInputPipeFlags(t, input)
				ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
				defer cancel()
				want := error(os.ErrDeadlineExceeded)
				if action == "closed" {
					input.Close()
					want = os.ErrClosed
				} else {
					deadline := time.Now().Add(120 * time.Millisecond)
					if action == "expired_deadline" {
						deadline = time.Now().Add(-time.Second)
					} else if action == "cancel_preserves_deadline" {
						deadline = time.Now().Add(500 * time.Millisecond)
					}
					if err := input.SetReadDeadline(deadline); err != nil {
						t.Fatal(err)
					}
					if action == "cancel_preserves_deadline" {
						cancel()
						ctx, cancel = context.WithTimeout(t.Context(), 40*time.Millisecond)
						defer cancel()
						want = context.DeadlineExceeded
					}
				}
				done := make(chan error, 1)
				go func() {
					_, err := (&batchRequestInput{ctx: ctx, file: input}).Read(make([]byte, 1))
					done <- err
				}()
				if err := awaitBatchSocketRead(t, producer, done); !errors.Is(err, want) {
					t.Fatalf("caller error identity changed: got %v; want %v", err, want)
				}
				if action == "closed" {
					return
				}
				if batchInputPipeFlags(t, input) != flags {
					t.Fatal("caller deadline changed descriptor flags")
				}
				// A fresh Batch context must still honor the original handle's deadline,
				// including when a shorter Batch context deadline already expired.
				retryCtx, retryCancel := context.WithTimeout(t.Context(), time.Second)
				defer retryCancel()
				if action == "cancel_preserves_deadline" {
					if _, err := io.WriteString(producer, "p"); err != nil {
						t.Fatal(err)
					}
					var data [1]byte
					if n, err := (&batchRequestInput{ctx: retryCtx, file: input}).Read(data[:]); err != nil || n != 1 || data[0] != 'p' {
						t.Fatalf("cancellation prematurely expired the caller deadline: %q / %v", data, err)
					}
				}
				go func() {
					_, err := (&batchRequestInput{ctx: retryCtx, file: input}).Read(make([]byte, 1))
					done <- err
				}()
				if err := awaitBatchSocketRead(t, producer, done); !errors.Is(err, os.ErrDeadlineExceeded) {
					t.Fatalf("caller deadline was cleared or replaced: %v", err)
				}
				if _, err := input.Read(make([]byte, 1)); !errors.Is(err, os.ErrDeadlineExceeded) {
					t.Fatalf("expired caller deadline changed on the borrowed handle: %v", err)
				}
				if err := input.SetReadDeadline(time.Time{}); err != nil {
					t.Fatal(err)
				}
				if _, err := io.WriteString(producer, "x"); err != nil {
					t.Fatal(err)
				}
				var data [1]byte
				if _, err := io.ReadFull(input, data[:]); err != nil || data[0] != 'x' {
					t.Fatalf("caller could not reuse its deadline-controlled socket: %q / %v", data, err)
				}
			})
		}
	}
}

func TestTokenizerSocketCancellationAndReuse(t *testing.T) {
	for _, blocking := range []bool{false, true} {
		name := "nonblocking"
		if blocking {
			name = "blocking"
		}
		for _, source := range []string{"stdin", "file-dash"} {
			t.Run(name+"/"+source, func(t *testing.T) {
				input, producer := batchInputSocketPair(t, blocking)
				flags := batchInputPipeFlags(t, input)
				args := []string{"openai", "--format", "json", "tokenizer", "inspect"}
				if source == "file-dash" {
					args = append(args, "--file", "-")
				}
				var out bytes.Buffer
				ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
				done := make(chan error, 1)
				go func() { done <- tokenizerTestRoot(input, &out).Run(ctx, args) }()
				err := awaitBatchSocketRead(t, producer, done)
				cancel()
				if !errors.Is(err, context.DeadlineExceeded) || out.Len() != 0 || batchInputPipeFlags(t, input) != flags {
					t.Fatalf("Tokenizer socket cancellation changed: %v / %q", err, out.String())
				}
				const value = "\ufeffhello\r\n👩\u200d💻 e\u0301\x00\x1b\t\n"
				if _, err := io.WriteString(producer, value); err != nil {
					t.Fatal(err)
				}
				producer.Close()
				if err := tokenizerTestRoot(input, &out).Run(t.Context(), args); err != nil {
					t.Fatal(err)
				}
				var result struct {
					InputBytes int `json:"input_bytes"`
					Tokens     []struct {
						StartByte int    `json:"start_byte"`
						EndByte   int    `json:"end_byte"`
						BytesHex  string `json:"bytes_hex"`
					} `json:"tokens"`
				}
				if err := json.Unmarshal(out.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				var reconstructed []byte
				for _, token := range result.Tokens {
					fragment, err := hex.DecodeString(token.BytesHex)
					if err != nil || token.StartByte != len(reconstructed) || token.EndByte != len(reconstructed)+len(fragment) {
						t.Fatalf("Tokenizer socket changed token bytes: %+v / %v", token, err)
					}
					reconstructed = append(reconstructed, fragment...)
				}
				if string(reconstructed) != value || result.InputBytes != len(value) || batchInputPipeFlags(t, input) != flags {
					t.Fatal("Tokenizer socket data or descriptor flags changed")
				}
				if _, err := input.Read(make([]byte, 1)); err != io.EOF {
					t.Fatalf("Tokenizer closed its borrowed input or lost EOF: %v", err)
				}
			})
		}
	}
}

func batchInputPipeFlags(t *testing.T, file *os.File) int {
	t.Helper()
	connection, err := file.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	flags := 0
	var flagErr error
	if err := connection.Control(func(fd uintptr) {
		flags, flagErr = unix.FcntlInt(fd, unix.F_GETFL, 0)
	}); err != nil || flagErr != nil {
		t.Fatalf("read descriptor flags: %v / %v", err, flagErr)
	}
	return flags
}
