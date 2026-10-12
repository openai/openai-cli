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

func tokenizerPipeFlags(t *testing.T, file *os.File) int {
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

func TestTokenizerPipeCancellationPreservesInput(t *testing.T) {
	for _, mode := range []string{"count", "inspect"} {
		for _, source := range []string{"stdin", "file-dash"} {
			for _, blocking := range []bool{false, true} {
				name := mode + "/" + source + "/nonblocking"
				if blocking {
					name = mode + "/" + source + "/blocking"
				}
				t.Run(name, func(t *testing.T) {
					input, producer, err := os.Pipe()
					if err != nil {
						t.Fatal(err)
					}
					defer input.Close()
					defer producer.Close()
					if blocking {
						connection, err := input.SyscallConn()
						if err != nil {
							t.Fatal(err)
						}
						var modeErr error
						if err := connection.Control(func(fd uintptr) {
							modeErr = unix.SetNonblock(int(fd), false)
						}); err != nil || modeErr != nil {
							t.Fatalf("prepare inherited blocking pipe: %v / %v", err, modeErr)
						}
					}
					flags := tokenizerPipeFlags(t, input)
					// Reuse the same borrowed pipe across several canceled commands.
					for attempt := range 3 {
						var out bytes.Buffer
						ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
						root := tokenizerTestRoot(input, &out)
						args := []string{"openai", "--format", "json", "tokenizer", mode}
						if source == "file-dash" {
							args = append(args, "--file", "-")
						}
						done := make(chan error, 1)
						go func() { done <- root.Run(ctx, args) }()
						select {
						case err := <-done:
							if !errors.Is(err, context.DeadlineExceeded) {
								t.Fatalf("attempt %d: cancellation = %v", attempt, err)
							}
						case <-time.After(time.Second):
							cancel()
							_ = producer.Close()
							<-done
							t.Fatal("cancellation waited for producer closure")
						}
						cancel()
						if out.Len() != 0 || tokenizerPipeFlags(t, input) != flags {
							t.Fatal("cancellation wrote output or changed input flags")
						}
					}
					// The caller can still write and read through its original handles.
					if _, err := producer.Write([]byte("still open")); err != nil {
						t.Fatal(err)
					}
					data := make([]byte, len("still open"))
					if _, err := io.ReadFull(input, data); err != nil || string(data) != "still open" {
						t.Fatalf("caller input unusable: %q / %v", data, err)
					}
				})
			}
		}
	}
}

func TestTokenizerPipePreservesDataAndEOF(t *testing.T) {
	for _, source := range []string{"stdin", "file-dash"} {
		for _, value := range []string{"", "\ufeffa\r\n👩\u200d💻 e\u0301\x00\x1b\t\n"} {
			t.Run(source+"/"+hex.EncodeToString([]byte(value)), func(t *testing.T) {
				input, producer, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				defer input.Close()
				if _, err := io.WriteString(producer, value); err != nil {
					t.Fatal(err)
				}
				if err := producer.Close(); err != nil {
					t.Fatal(err)
				}
				flags := tokenizerPipeFlags(t, input)
				var out bytes.Buffer
				args := []string{"openai", "--format", "json", "tokenizer", "inspect"}
				if source == "file-dash" {
					args = append(args, "--file", "-")
				}
				if err := tokenizerTestRoot(input, &out).Run(t.Context(), args); err != nil {
					t.Fatal(err)
				}
				var result struct {
					InputBytes int              `json:"input_bytes"`
					Tokens     []tokenizerToken `json:"tokens"`
				}
				if err := json.Unmarshal(out.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				var reconstructed []byte
				for _, token := range result.Tokens {
					fragment, err := hex.DecodeString(token.BytesHex)
					if err != nil || token.StartByte != len(reconstructed) || token.EndByte != len(reconstructed)+len(fragment) {
						t.Fatalf("changed token bytes: %+v / %v", token, err)
					}
					reconstructed = append(reconstructed, fragment...)
				}
				if string(reconstructed) != value || result.InputBytes != len(value) || tokenizerPipeFlags(t, input) != flags {
					t.Fatal("pipe data or descriptor flags changed")
				}
				if _, err := input.Read(make([]byte, 1)); err != io.EOF {
					t.Fatalf("caller EOF changed: %v", err)
				}
			})
		}
	}
}

func TestTokenizerPipeHonorsLaterCloseAndDeadline(t *testing.T) {
	for _, action := range []string{"close", "deadline", "expired deadline"} {
		t.Run(action, func(t *testing.T) {
			input, producer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			defer producer.Close()
			if action == "expired deadline" {
				if err := input.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var out bytes.Buffer
			done := make(chan error, 1)
			go func() {
				done <- tokenizerTestRoot(input, &out).Run(ctx, []string{"openai", "tokenizer", "count", "--file", "-"})
			}()
			if action != "expired deadline" {
				time.Sleep(30 * time.Millisecond)
				if action == "close" {
					err = input.Close()
				} else {
					err = input.SetReadDeadline(time.Now())
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				want := os.ErrDeadlineExceeded
				if action == "close" {
					want = os.ErrClosed
				}
				if !errors.Is(err, want) || out.Len() != 0 {
					t.Fatalf("caller action lost: %v / %q", err, out.String())
				}
			case <-time.After(time.Second):
				cancel()
				_ = producer.Close()
				<-done
				t.Fatal("caller close/deadline did not stop input")
			}
			if action != "close" {
				if _, err := input.Read(make([]byte, 1)); !errors.Is(err, os.ErrDeadlineExceeded) {
					t.Fatalf("caller deadline was reset: %v", err)
				}
			}
		})
	}
}
