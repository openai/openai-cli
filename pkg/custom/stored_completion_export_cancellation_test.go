package custom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const storedCompletionCancellationWait = 2 * time.Second

func waitStoredCompletionCancellation(t *testing.T, done <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(storedCompletionCancellationWait):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func waitStoredCompletionStageRemoval(t *testing.T, path string) {
	t.Helper()
	deadline := time.NewTimer(storedCompletionCancellationWait)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		_, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("original stage remained while the canceled writer was gated")
		}
	}
}

func TestStoredCompletionExportCancellationRemovesStageBeforeWriterReturns(t *testing.T) {
	// Repeated transactions use independent contexts and private stage files.
	for iteration := range 8 {
		t.Run(fmt.Sprint(iteration), func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "export.jsonl")
			ctx, cancel := context.WithCancel(t.Context())
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var open sync.Once
			unblock := func() { open.Do(func() { close(release) }) }
			var result error
			var lateWrite error
			go func() {
				defer close(done)
				result = saveStoredCompletionExport(ctx, path, func(out io.Writer) error {
					if _, err := io.WriteString(out, "partial record"); err != nil {
						return err
					}
					close(entered)
					<-release
					_, lateWrite = io.WriteString(out, "must not publish")
					return lateWrite
				})
			}()
			t.Cleanup(func() {
				cancel()
				unblock()
				<-done
			})
			waitStoredCompletionCancellation(t, entered, "writer entry")
			stages, err := filepath.Glob(filepath.Join(directory, ".openai-download-*.tmp"))
			if err != nil || len(stages) != 1 {
				t.Fatalf("expected one original stage: %v, %v", stages, err)
			}
			cancel()
			cancel()
			waitStoredCompletionStageRemoval(t, stages[0])
			select {
			case <-done:
				t.Fatal("writer returned before its release")
			default:
			}
			unblock()
			waitStoredCompletionCancellation(t, done, "writer completion")
			if !errors.Is(result, context.Canceled) || !errors.Is(lateWrite, os.ErrClosed) || !errors.Is(result, lateWrite) {
				t.Fatalf("cancellation or late file-write error lost: result=%v write=%v", result, lateWrite)
			}
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("canceled export published a destination: %v", err)
			}
			assertStoredExportStageCleanup(t, directory)
		})
	}
}

func TestStoredCompletionExportCancellationAlreadyCanceled(t *testing.T) {
	for range 16 {
		directory := t.TempDir()
		path := filepath.Join(directory, "export.jsonl")
		cause := errors.New("synthetic cancellation cause")
		ctx, cancel := context.WithCancelCause(t.Context())
		cancel(cause)
		err := saveStoredCompletionExport(ctx, path, func(out io.Writer) error {
			_, err := io.WriteString(out, "unpublished record\n")
			return err
		})
		if !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
			t.Fatalf("already-canceled context causes lost: %v", err)
		}
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("already-canceled export published a destination: %v", err)
		}
		assertStoredExportStageCleanup(t, directory)
	}
}

func TestStoredCompletionExportCancellationSuccessAndLateCancel(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "export.jsonl")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	const payload = "{\"id\":\"chatcmpl_demo\",\"unknown\":1.2300e+6}\n"
	if err := saveStoredCompletionExport(ctx, path, func(out io.Writer) error {
		_, err := io.WriteString(out, payload)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	cancel()
	cancel()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != payload {
		t.Fatalf("late cancellation changed completed bytes: %q, %v", data, err)
	}
	assertStoredExportStageCleanup(t, directory)
}

func TestStoredCompletionExportCancellationExistingDestination(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "export.jsonl")
	const payload = "existing private record\n"
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = saveStoredCompletionExport(t.Context(), path, func(io.Writer) error {
		called = true
		return nil
	})
	if !errors.Is(err, os.ErrExist) || called {
		t.Fatalf("existing destination not refused: writer=%v, error=%v", called, err)
	}
	data, err := os.ReadFile(path)
	after, statErr := os.Stat(path)
	if err != nil || statErr != nil || string(data) != payload || !os.SameFile(before, after) {
		t.Fatalf("existing destination changed: %q, %v, %v", data, err, statErr)
	}
	assertStoredExportStageCleanup(t, directory)
}

func TestStoredCompletionExportCancellationPreservesReplacementAndCauses(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "export.jsonl")
	moved := filepath.Join(directory, "original-stage")
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	writeErr, cancelErr := errors.New("synthetic writer failure"), errors.New("synthetic cancellation cause")
	var stagePath string
	var replacement os.FileInfo
	err := saveStoredCompletionExport(ctx, path, func(out io.Writer) error {
		if _, err := io.WriteString(out, "original bytes\n"); err != nil {
			return err
		}
		stages, err := filepath.Glob(filepath.Join(directory, ".openai-download-*.tmp"))
		if err != nil || len(stages) != 1 {
			return fmt.Errorf("expected one original stage: %v, %v", stages, err)
		}
		stagePath = stages[0]
		if err := os.Rename(stagePath, moved); err != nil {
			return err
		}
		if err := os.WriteFile(stagePath, []byte("replacement bytes\n"), 0o600); err != nil {
			return err
		}
		replacement, err = os.Lstat(stagePath)
		if err != nil {
			return err
		}
		cancel(cancelErr)
		return writeErr
	})
	for _, cause := range []error{writeErr, cancelErr, context.Canceled, errDownloadDestinationChanged} {
		if !errors.Is(err, cause) {
			t.Fatalf("writer, cancellation, or cleanup cause lost (%v): %v", cause, err)
		}
	}
	current, statErr := os.Lstat(stagePath)
	data, readErr := os.ReadFile(stagePath)
	if statErr != nil || readErr != nil || !os.SameFile(replacement, current) || string(data) != "replacement bytes\n" {
		t.Fatalf("cleanup changed the replacement: %q, %v, %v", data, statErr, readErr)
	}
	data, readErr = os.ReadFile(moved)
	if readErr != nil || string(data) != "original bytes\n" {
		t.Fatalf("cleanup changed the moved original: %q, %v", data, readErr)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement was published: %v", err)
	}
}

func TestStoredCompletionExportCancellationPanicCleansStage(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprint(canceled), func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "export.jsonl")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := errors.New("synthetic writer panic")
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				_ = saveStoredCompletionExport(ctx, path, func(out io.Writer) error {
					if _, err := io.WriteString(out, "partial record"); err != nil {
						return err
					}
					if canceled {
						cancel()
					}
					panic(want)
				})
			}()
			if recovered != want {
				t.Fatalf("writer panic changed: %v", recovered)
			}
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("panicking export published a destination: %v", err)
			}
			assertStoredExportStageCleanup(t, directory)
		})
	}
}

func TestStoredCompletionExportCancellationJoinsCleanupBeforeReturnOrPanic(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(fmt.Sprint(panics), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			writerEntered, cleanupEntered := make(chan struct{}), make(chan struct{})
			writerReturned, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var open sync.Once
			unblock := func() { open.Do(func() { close(release) }) }
			var cleanupCalls atomic.Int32
			var cleanupFinished atomic.Bool
			writeErr, cleanupErr := errors.New("synthetic writer failure"), errors.New("synthetic cleanup failure")
			cleanup := sync.OnceValue(func() error {
				cleanupCalls.Add(1)
				close(cleanupEntered)
				<-release
				cleanupFinished.Store(true)
				return cleanupErr
			})
			var result error
			var recovered any
			var joined bool
			go func() {
				defer close(done)
				defer func() {
					recovered = recover()
					joined = cleanupFinished.Load()
				}()
				result = writeStoredCompletionStage(ctx, func() error {
					close(writerEntered)
					<-cleanupEntered
					close(writerReturned)
					if panics {
						panic(writeErr)
					}
					return writeErr
				}, cleanup)
			}()
			t.Cleanup(func() {
				cancel()
				unblock()
				<-done
			})
			waitStoredCompletionCancellation(t, writerEntered, "writer entry")
			cancel()
			waitStoredCompletionCancellation(t, writerReturned, "writer return")
			select {
			case <-done:
				t.Fatal("write phase exited while its cancellation cleanup was blocked")
			case <-time.After(20 * time.Millisecond):
			}
			unblock()
			waitStoredCompletionCancellation(t, done, "joined cleanup")
			if !joined || cleanupCalls.Load() != 1 {
				t.Fatalf("callback was not joined once: joined=%v, calls=%d", joined, cleanupCalls.Load())
			}
			if panics && recovered != writeErr || !panics && (recovered != nil || !errors.Is(result, writeErr)) {
				t.Fatalf("writer outcome changed: panic=%v, error=%v", recovered, result)
			}
			if !errors.Is(cleanup(), cleanupErr) || cleanupCalls.Load() != 1 {
				t.Fatal("final cleanup did not reuse its retained error")
			}
		})
	}
}

func TestStoredCompletionExportCancellationStopsCallbackOnOrdinaryReturn(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var calls atomic.Int32
	called := make(chan struct{}, 2)
	cleanupErr := errors.New("synthetic cleanup failure")
	cleanup := func() error {
		calls.Add(1)
		called <- struct{}{}
		return cleanupErr
	}
	if err := writeStoredCompletionStage(ctx, func() error { return nil }, cleanup); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("uncanceled writer triggered cancellation cleanup")
	}
	cancel()
	select {
	case <-called:
		t.Fatal("late cancellation ran a stopped callback")
	case <-time.After(20 * time.Millisecond):
	}
	if !errors.Is(cleanup(), cleanupErr) || calls.Load() != 1 {
		t.Fatal("owner cleanup did not retain its error")
	}
}
