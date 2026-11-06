package audiohistory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testSettings() Settings {
	return Settings{Input: "Synthetic 世界", Instructions: "Synthetic instructions", Model: "synthetic-model",
		Voice: `{"id":"voice_synthetic"}`, Format: "wav", Speed: 1.25, Preview: true}
}

func testCapture(t *testing.T, store Store, data string) Entry {
	t.Helper()
	entry, err := store.Capture(t.Context(), io.NopCloser(strings.NewReader(data)), testSettings())
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func TestCaptureRoundTripAndPrivateSnapshot(t *testing.T) {
	store := Store{Directory: filepath.Join(t.TempDir(), "history")}
	data := "RIFF\x00synthetic audio\xff\r\n"
	entry := testCapture(t, store, data)
	digest := sha256.Sum256([]byte(data))
	if !validID(entry.ID) || entry.CreatedAt.IsZero() || entry.Settings != testSettings() ||
		entry.Bytes != int64(len(data)) || entry.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatal("capture lost metadata or original byte identity")
	}
	entries, err := store.List(t.Context())
	if err != nil || len(entries) != 1 || entries[0] != entry {
		t.Fatalf("list changed captured metadata: entries=%d err=%v", len(entries), err)
	}
	for _, relative := range []string{".", entry.ID, entry.ID + "/audio", entry.ID + "/manifest.json"} {
		info, err := os.Stat(filepath.Join(store.Directory, relative))
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			t.Fatal("history artifact is accessible outside its owner")
		}
	}
	snapshot, err := store.Open(t.Context(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	if snapshot.Entry != entry {
		t.Fatal("snapshot lost immutable metadata, including preview status")
	}
	if _, err := snapshot.File.Write([]byte("change")); err == nil {
		t.Fatal("snapshot descriptor is writable")
	}
	if runtime.GOOS != "windows" {
		if _, err := os.Stat(snapshot.File.Name()); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Unix snapshot still has a public pathname: %v", err)
		}
	}
	got, err := io.ReadAll(snapshot.File)
	if err != nil || string(got) != data {
		t.Fatal("snapshot changed original bytes")
	}
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatalf("snapshot close is not idempotent: %v", err)
	}
}

func TestCaptureEmptyAndLargeBodies(t *testing.T) {
	// The large case crosses the previous 32 MiB capture boundary. The source
	// generates bytes incrementally, without an allocated payload or fixture.
	for _, size := range []int64{0, (32 << 20) + 1} {
		store := Store{Directory: filepath.Join(t.TempDir(), "history")}
		entry, err := store.Capture(t.Context(), io.NopCloser(io.LimitReader(zeroReader{}, size)), testSettings())
		if err != nil || entry.Bytes != size {
			t.Fatalf("capture size %d: bytes=%d err=%v", size, entry.Bytes, err)
		}
		snapshot, err := store.Open(t.Context(), entry.ID)
		if err != nil {
			t.Fatal(err)
		}
		n, err := io.Copy(io.Discard, snapshot.File)
		closeErr := snapshot.Close()
		if err != nil || closeErr != nil || n != size {
			t.Fatalf("snapshot size %d: bytes=%d read=%v close=%v", size, n, err, closeErr)
		}
	}
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	clear(buffer)
	return len(buffer), nil
}

type failingBody struct {
	reader   io.Reader
	closeErr error
	closes   atomic.Int32
}

func (body *failingBody) Read(buffer []byte) (int, error) { return body.reader.Read(buffer) }
func (body *failingBody) Close() error                    { body.closes.Add(1); return body.closeErr }

type partialFailureReader struct{ failure error }

func (reader partialFailureReader) Read(buffer []byte) (int, error) {
	return copy(buffer, "partial audio"), reader.failure
}

func TestCaptureFailuresNeverActivate(t *testing.T) {
	failure := errors.New("synthetic source failure")
	for _, closeFailure := range []bool{false, true} {
		store := Store{Directory: filepath.Join(t.TempDir(), "history")}
		body := &failingBody{reader: partialFailureReader{failure}}
		if closeFailure {
			body.reader, body.closeErr = strings.NewReader("complete bytes"), failure
		}
		entry, err := store.Capture(t.Context(), body, testSettings())
		if !errors.Is(err, failure) || entry != (Entry{}) || body.closes.Load() != 1 {
			t.Fatalf("failed capture was activated or closed incorrectly: err=%v closes=%d", err, body.closes.Load())
		}
		entries, err := store.List(t.Context())
		children, readErr := os.ReadDir(store.Directory)
		if err != nil || readErr != nil || len(entries) != 0 || len(children) != 0 {
			t.Fatalf("failed capture left artifacts: entries=%d children=%d err=%v read=%v", len(entries), len(children), err, readErr)
		}
	}
}

type blockedBody struct {
	started chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (body *blockedBody) Read([]byte) (int, error) {
	close(body.started)
	<-body.closed
	return 0, errors.New("synthetic body closed")
}
func (body *blockedBody) Close() error { body.once.Do(func() { close(body.closed) }); return nil }

func TestCaptureCancellationClosesBodyAndSkipsPendingEntry(t *testing.T) {
	store := Store{Directory: filepath.Join(t.TempDir(), "history")}
	body := &blockedBody{started: make(chan struct{}), closed: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := store.Capture(ctx, body, testSettings()); done <- err }()
	select {
	case <-body.started:
	case <-time.After(3 * time.Second):
		t.Fatal("capture did not start")
	}
	entries, err := store.List(t.Context())
	if err != nil || len(entries) != 0 {
		t.Fatalf("list advertised an unfinished capture: %d %v", len(entries), err)
	}
	if err := store.Clear(t.Context()); err != nil {
		t.Fatalf("clear disturbed an unfinished capture: %v", err)
	}
	children, err := os.ReadDir(store.Directory)
	if err != nil || len(children) != 1 {
		t.Fatal("clear removed the active capture directory")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation identity lost: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("capture did not close and reap the blocked body")
	}
	children, err = os.ReadDir(store.Directory)
	if err != nil || len(children) != 0 {
		t.Fatal("canceled capture left owned artifacts")
	}
}

func TestConcurrentCapturesListStableEntries(t *testing.T) {
	store := Store{Directory: filepath.Join(t.TempDir(), "history")}
	const count = 12
	var workers sync.WaitGroup
	failures := make(chan error, count)
	for range count {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := store.Capture(t.Context(), io.NopCloser(strings.NewReader("synthetic concurrent audio")), testSettings())
			failures <- err
		}()
	}
	for range count {
		if _, err := store.List(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	entries, err := store.List(t.Context())
	if err != nil || len(entries) != count {
		t.Fatalf("concurrent captures lost entries: %d %v", len(entries), err)
	}
	seen := make(map[string]bool)
	for i, entry := range entries {
		if seen[entry.ID] || i > 0 && entries[i-1].CreatedAt.Before(entry.CreatedAt) {
			t.Fatal("history order or identity changed")
		}
		seen[entry.ID] = true
	}
	// Equal timestamps use IDs as a deterministic tie-breaker.
	for _, entry := range entries {
		entry.CreatedAt = time.Unix(1, 0).UTC()
		writeTestManifest(t, store, entry)
	}
	entries, err = store.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(entries); i++ {
		if entries[i-1].ID >= entries[i].ID {
			t.Fatal("equal-time ordering is unstable")
		}
	}
}

func writeTestManifest(t *testing.T, store Store, entry Entry) {
	t.Helper()
	data, err := json.Marshal(manifest{Version: 1, Entry: entry})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Directory, entry.ID, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCorruptMetadataAndUnrelatedFilesRemainUntouched(t *testing.T) {
	for _, change := range []func([]byte) []byte{
		func([]byte) []byte { return []byte(`{"version":99}`) },
		func(data []byte) []byte { return append(data, []byte(` {}`)...) },
		func(data []byte) []byte { return bytes.Replace(data, []byte(`"wav"`), []byte(`"unknown"`), 1) },
		func(data []byte) []byte {
			return bytes.Replace(data, []byte(`"version":1`), []byte(`"version":1,"unknown":true`), 1)
		},
		func(data []byte) []byte {
			return bytes.Replace(data, []byte(`"preview":true`), []byte(`"preview":false,"preview":true`), 1)
		},
		func(data []byte) []byte {
			return bytes.Replace(data, []byte("Synthetic 世界"), []byte{'S', 0xff}, 1)
		},
	} {
		store := Store{Directory: filepath.Join(t.TempDir(), "history")}
		bad := testCapture(t, store, "bad entry audio")
		good := testCapture(t, store, "good entry audio")
		path := filepath.Join(store.Directory, bad.ID, "manifest.json")
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		damaged := change(original)
		if err := os.WriteFile(path, damaged, 0600); err != nil {
			t.Fatal(err)
		}
		unrelated := filepath.Join(store.Directory, "unrelated.txt")
		if err := os.WriteFile(unrelated, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
		entries, err := store.List(t.Context())
		if !errors.Is(err, ErrInvalid) || len(entries) != 1 || entries[0].ID != good.ID {
			t.Fatal("list lost valid entries or accepted corrupt metadata")
		}
		if _, err := store.Open(t.Context(), bad.ID); !errors.Is(err, ErrInvalid) {
			t.Fatalf("open accepted corrupt metadata: %v", err)
		}
		if err := store.Clear(t.Context()); !errors.Is(err, ErrInvalid) {
			t.Fatalf("clear silently accepted corrupt metadata: %v", err)
		}
		current, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(current, damaged) {
			t.Fatal("clear overwrote corrupt metadata")
		}
		if current, err := os.ReadFile(unrelated); err != nil || string(current) != "keep" {
			t.Fatal("clear removed an unrelated file")
		}
	}
}

func TestSnapshotsSurviveHistoryReplacementAndClear(t *testing.T) {
	store := Store{Directory: filepath.Join(t.TempDir(), "history")}
	entry := testCapture(t, store, "original audio")
	snapshot, err := store.Open(t.Context(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	path := filepath.Join(store.Directory, entry.ID, "audio")
	if err := os.WriteFile(path, []byte("modified audio"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(t.Context(), entry.ID); !errors.Is(err, ErrChanged) {
		t.Fatalf("open accepted changed bytes: %v", err)
	}
	if err := store.Clear(t.Context()); !errors.Is(err, ErrChanged) {
		t.Fatalf("clear deleted changed bytes: %v", err)
	}
	if err := os.WriteFile(path, []byte("original audio"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(t.Context()); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(snapshot.File)
	if err != nil || string(data) != "original audio" {
		t.Fatal("history changes modified an open snapshot")
	}
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := store.List(t.Context())
	if err != nil || len(entries) != 0 {
		t.Fatal("clear left completed entries")
	}
}

func TestOpenRejectsUnsafeIDsAndSymlinks(t *testing.T) {
	store := Store{Directory: filepath.Join(t.TempDir(), "history")}
	entry := testCapture(t, store, "original audio")
	for _, id := range []string{"", ".", "../outside", entry.ID + "/audio", strings.Repeat("A", 32), strings.Repeat("z", 32)} {
		if _, err := store.Open(t.Context(), id); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unsafe ID accepted: %v", err)
		}
	}
	if runtime.GOOS == "windows" {
		return
	}
	for _, name := range []string{"audio", "manifest.json"} {
		path := filepath.Join(store.Directory, entry.ID, name)
		moved := path + ".original"
		if err := os.Rename(path, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(moved, path); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Open(t.Context(), entry.ID); !errors.Is(err, ErrInvalid) {
			t.Fatalf("symlink accepted: %v", err)
		}
		if err := store.Clear(t.Context()); err == nil {
			t.Fatal("clear accepted a symlink")
		}
		if _, err := os.Stat(moved); err != nil {
			t.Fatal("clear deleted the symlink target")
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(moved, path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCanceledOperationsAndMissingStore(t *testing.T) {
	store := Store{Directory: filepath.Join(t.TempDir(), "missing")}
	entries, err := store.List(t.Context())
	if err != nil || len(entries) != 0 {
		t.Fatal("missing history is not an empty history")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.List(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("list ignored cancellation")
	}
	if err := store.Clear(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("clear ignored cancellation")
	}
	body := &failingBody{reader: strings.NewReader("unused")}
	if _, err := store.Capture(ctx, body, testSettings()); !errors.Is(err, context.Canceled) || body.closes.Load() != 1 {
		t.Fatal("canceled capture retained its source")
	}
	if _, err := os.Stat(store.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled capture created history")
	}
}

func TestClearPreservesCaptureActivationWindow(t *testing.T) {
	store := Store{Directory: filepath.Join(t.TempDir(), "history")}
	entry := testCapture(t, store, "synthetic active capture")
	manifestPath := filepath.Join(store.Directory, entry.ID, "manifest.json")
	pendingPath := filepath.Join(store.Directory, entry.ID, ".manifest")
	// Capture activates through this hard link, then removes its temporary
	// name. Keep that exact filesystem state until Clear has returned.
	if err := os.Link(manifestPath, pendingPath); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(t.Context()); err != nil {
		t.Fatalf("clear misclassified capture activation: %v", err)
	}
	entries, err := store.List(t.Context())
	if err != nil || len(entries) != 1 {
		t.Fatal("clear removed an active capture")
	}
	if err := os.Remove(pendingPath); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(t.Context()); err != nil {
		t.Fatal(err)
	}
	entries, err = store.List(t.Context())
	if err != nil || len(entries) != 0 {
		t.Fatal("clear did not remove the completed capture")
	}
}

func TestMissingCommittedAudioReturnsDamageAlongsideValidEntries(t *testing.T) {
	store := Store{Directory: filepath.Join(t.TempDir(), "history")}
	bad := testCapture(t, store, "missing audio")
	good := testCapture(t, store, "valid audio")
	if err := os.Remove(filepath.Join(store.Directory, bad.ID, "audio")); err != nil {
		t.Fatal(err)
	}
	entries, err := store.List(t.Context())
	if !errors.Is(err, ErrChanged) || len(entries) != 1 || entries[0].ID != good.ID {
		t.Fatalf("list concealed a missing committed artifact: entries=%d err=%v", len(entries), err)
	}
	manifestPath := filepath.Join(store.Directory, bad.ID, "manifest.json")
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	// Clear can detect the incomplete directory layout before verifying audio.
	// Its contract requires a reported failure and preserved damaged metadata.
	if err := store.Clear(t.Context()); err == nil {
		t.Fatal("clear concealed a missing committed artifact")
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("clear deleted or changed the damaged entry's metadata")
	}
	if _, err := os.Stat(filepath.Join(store.Directory, good.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("clear did not remove the valid neighboring entry")
	}
}
