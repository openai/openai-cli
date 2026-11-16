//go:build !windows

package batches

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	sockettest "github.com/openai/openai-cli/internal/testutil/socket"
)

func TestBatchRequestInputSocketCancellationPreservesBorrowedDescriptor(t *testing.T) {
	for _, blocking := range []bool{false, true} {
		name := "nonblocking"
		if blocking {
			name = "blocking"
		}
		t.Run(name, func(t *testing.T) {
			input, producer := sockettest.Pair(t, blocking)
			flags := sockettest.Flags(t, input)
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
				err := sockettest.AwaitRead(t, producer, done)
				cancel()
				if timer != nil {
					timer.Stop()
				}
				if !errors.Is(err, want) || len(data) != 0 || sockettest.Flags(t, input) != flags {
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
				input, producer := sockettest.Pair(t, blocking)
				flags := sockettest.Flags(t, input)
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
				if err != nil || string(data) != value || sockettest.Flags(t, input) != flags {
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
				input, producer := sockettest.Pair(t, blocking)
				flags := sockettest.Flags(t, input)
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
				if err := sockettest.AwaitRead(t, producer, done); !errors.Is(err, want) {
					t.Fatalf("caller error identity changed: got %v; want %v", err, want)
				}
				if action == "closed" {
					return
				}
				if sockettest.Flags(t, input) != flags {
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
				if err := sockettest.AwaitRead(t, producer, done); !errors.Is(err, os.ErrDeadlineExceeded) {
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
