package audio

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/openai/openai-cli/internal/imageoutput"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/stretchr/testify/require"
)

func audioSaveTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func audioSaveTestSnapshot(t *testing.T, prompt, format, data string) *audiohistory.Snapshot {
	t.Helper()
	path := filepath.Join(t.TempDir(), "original")
	require.NoError(t, os.WriteFile(path, []byte(data), 0600))
	file, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = file.Close() })
	return &audiohistory.Snapshot{File: file, Entry: audiohistory.Entry{
		ID: "0123456789abcdef0123456789abcdef", Bytes: int64(len(data)),
		Settings: audiohistory.Settings{Input: prompt, Format: format},
	}}
}

func TestAudioDefaultSavePathFromRetainedText(t *testing.T) {
	home := audioSaveTestHome(t)
	for _, test := range []struct{ name, prompt, want string }{
		{"text", "Hello from the other side.", "hello-from-the-other-side"},
		{"article", "The quiet morning", "quiet-morning"},
		{"unicode", "ÉLÉPHANT 日本語 CAFE\u0301", "éléphant-日本語-cafe\u0301"},
		{"paths", "../../hello\\world: nice?", "hello-world-nice"},
		{"controls", "hello\x00\n\tworld\u202efile", "hello-world-file"},
		{"invalid bytes", "hello\xffworld", "hello-world"},
		{"empty", "🤖...", "speech-0123456789abcdef0123456789abcdef"},
		{"reserved", "CON", "image-con"},
		{"reserved digit", "LPT³", "image-lpt³"},
		{"word bound", "one two three four five six seven eight nine", "one-two-three-four-five-six-seven-eight"},
		{"byte bound", strings.Repeat("界", 100), strings.Repeat("界", 26)},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry := audiohistory.Entry{ID: "0123456789abcdef0123456789abcdef", Settings: audiohistory.Settings{Input: test.prompt, Format: "wav"}}
			path, err := audioDefaultSavePath(entry)
			require.NoError(t, err)
			require.Equal(t, filepath.Join(home, "Downloads", "gpt-audio", test.want+".wav"), path)
			require.True(t, utf8.ValidString(path))
			require.NoError(t, imageoutput.ValidateName(strings.TrimSuffix(filepath.Base(path), ".wav")))
		})
	}
	_, err := os.Stat(filepath.Join(home, "Downloads"))
	require.ErrorIs(t, err, os.ErrNotExist, "Opening Save must not create a folder")
}

func TestAudioDefaultSavePathFormatsAndUnavailableHome(t *testing.T) {
	audioSaveTestHome(t)
	for _, format := range audioFormats {
		path, err := audioDefaultSavePath(audiohistory.Entry{Settings: audiohistory.Settings{Input: "A retained result", Format: format}})
		require.NoError(t, err)
		require.Equal(t, "."+format, filepath.Ext(path))
	}
	for _, format := range []string{"", "../wav", "wav/../../outside", "wav\x00"} {
		path, err := audioDefaultSavePath(audiohistory.Entry{Settings: audiohistory.Settings{Input: "Retained result", Format: format}})
		require.Error(t, err)
		require.Empty(t, path)
	}
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	path, err := audioDefaultSavePath(audiohistory.Entry{Settings: audiohistory.Settings{Input: "Retained result", Format: "wav"}})
	require.Error(t, err)
	require.Equal(t, "retained-result.wav", path, "A safe basename permits custom-path recovery")
}

func TestWorkbenchAudioDefaultSaveCreatesFolderAndNumbersCollisions(t *testing.T) {
	home := audioSaveTestHome(t)
	snapshot := audioSaveTestSnapshot(t, "A retained recording", "wav", "complete original\x00bytes")
	suggestion, err := audioDefaultSavePath(snapshot.Entry)
	require.NoError(t, err)
	_, err = snapshot.File.Seek(9, io.SeekStart)
	require.NoError(t, err)
	first, err := saveWorkbenchAudio(t.Context(), snapshot, suggestion, true)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, "Downloads", "gpt-audio", "retained-recording.wav"), first)
	require.NoError(t, os.Mkdir(filepath.Join(filepath.Dir(first), "retained-recording-2.wav"), 0700))
	third, err := saveWorkbenchAudio(t.Context(), snapshot, suggestion, true)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(filepath.Dir(first), "retained-recording-3.wav"), third)
	for _, path := range []string{first, third} {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, "complete original\x00bytes", string(data))
	}
}

func TestWorkbenchAudioDefaultSaveLongNameAndConcurrentSaves(t *testing.T) {
	audioSaveTestHome(t)
	snapshot := audioSaveTestSnapshot(t, strings.Repeat("x", 100), "flac", "all original bytes")
	suggestion, err := audioDefaultSavePath(snapshot.Entry)
	require.NoError(t, err)
	const count = 4
	paths, failures := make([]string, count), make([]error, count)
	var workers sync.WaitGroup
	for i := range count {
		workers.Go(func() { paths[i], failures[i] = saveWorkbenchAudio(t.Context(), snapshot, suggestion, true) })
	}
	workers.Wait()
	seen := map[string]bool{}
	for i, path := range paths {
		require.NoError(t, failures[i])
		require.False(t, seen[path], "Concurrent saves must allocate different files")
		seen[path] = true
		require.LessOrEqual(t, len(filepath.Base(path)), 80+2+len(".flac"))
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, "all original bytes", string(data))
	}
}

func TestWorkbenchAudioCustomSavePreservesPathAndExplicitOverwrite(t *testing.T) {
	home := audioSaveTestHome(t)
	snapshot := audioSaveTestSnapshot(t, "Different prompt", "wav", "retained bytes")
	_, err := snapshot.File.Seek(9, io.SeekStart)
	require.NoError(t, err)
	t.Chdir(t.TempDir())
	path, err := saveWorkbenchAudio(t.Context(), snapshot, "My exact filename.custom", false)
	require.NoError(t, err)
	require.Equal(t, "My exact filename.custom", path)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "retained bytes", string(data), "Custom Save must export from the original start")
	require.NoError(t, os.WriteFile(path, []byte("replace explicit destination"), 0600))
	_, err = saveWorkbenchAudio(t.Context(), snapshot, path, false)
	require.NoError(t, err)
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "retained bytes", string(data), "Explicit Save must preserve its established overwrite behavior")
	_, err = saveWorkbenchAudio(t.Context(), snapshot, filepath.Join("missing", "exact.wav"), false)
	require.Error(t, err)
	_, err = os.Stat("missing")
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(home, "Downloads"))
	require.ErrorIs(t, err, os.ErrNotExist, "Custom Save must not create the default folder")
}

func TestWorkbenchAudioCustomSaveFailurePreservesExistingDestination(t *testing.T) {
	snapshot := audioSaveTestSnapshot(t, "Retained recording", "wav", "new retained audio")
	path := filepath.Join(t.TempDir(), "existing.wav")
	require.NoError(t, os.WriteFile(path, []byte("existing bytes"), 0600))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := saveWorkbenchAudio(ctx, snapshot, path, false)
	require.ErrorIs(t, err, context.Canceled)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "existing bytes", string(data))
	require.NoError(t, snapshot.File.Close())
	_, err = saveWorkbenchAudio(t.Context(), snapshot, path, false)
	require.Error(t, err)
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "existing bytes", string(data), "Staging failure must preserve the explicit destination")
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 1, "Failed explicit Save must remove its owned staging file")
}

func TestWorkbenchAudioSaveRejectsSymlinksAndDefaultSkipsThem(t *testing.T) {
	audioSaveTestHome(t)
	snapshot := audioSaveTestSnapshot(t, "Retained recording", "wav", "audio bytes")
	suggestion, err := audioDefaultSavePath(snapshot.Entry)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(suggestion), 0700))
	target := filepath.Join(t.TempDir(), "keep")
	require.NoError(t, os.WriteFile(target, []byte("keep target"), 0600))
	if err := os.Symlink(target, suggestion); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err = saveWorkbenchAudio(t.Context(), snapshot, suggestion, false)
	require.Error(t, err)
	path, err := saveWorkbenchAudio(t.Context(), snapshot, suggestion, true)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(filepath.Dir(suggestion), "retained-recording-2.wav"), path)
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "keep target", string(data))
}

func TestWorkbenchAudioSaveCancellationAndReadFailureCleanup(t *testing.T) {
	home := audioSaveTestHome(t)
	snapshot := audioSaveTestSnapshot(t, "Retained recording", "wav", "audio bytes")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := saveWorkbenchAudio(ctx, snapshot, "", true)
	require.ErrorIs(t, err, context.Canceled)
	_, err = os.Stat(filepath.Join(home, "Downloads"))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, snapshot.File.Close())
	path := filepath.Join(t.TempDir(), "failed.wav")
	_, err = saveWorkbenchAudio(t.Context(), snapshot, path, false)
	require.Error(t, err)
	require.NotContains(t, err.Error(), path)
	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist, "A failed copy must remove its owned partial file")
}

type audioSaveReplacingContext struct {
	context.Context
	replace func()
	once    sync.Once
}

func (c *audioSaveReplacingContext) Err() error {
	c.once.Do(c.replace)
	return context.Canceled
}

func TestWorkbenchAudioSaveCleanupPreservesReplacement(t *testing.T) {
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	require.NoError(t, err)
	defer root.Close()
	file, err := clioutput.CreatePrivateDownloadFile(root, "result.wav")
	require.NoError(t, err)
	snapshot := audioSaveTestSnapshot(t, "Retained recording", "wav", "audio bytes")
	ctx := &audioSaveReplacingContext{Context: t.Context(), replace: func() {
		require.NoError(t, root.Rename("result.wav", "original.wav"))
		require.NoError(t, os.WriteFile(filepath.Join(directory, "result.wav"), []byte("replacement"), 0600))
	}}
	err = writeWorkbenchAudio(ctx, root, "result.wav", filepath.Join(directory, "result.wav"), file, snapshot)
	require.True(t, errors.Is(err, context.Canceled))
	require.ErrorIs(t, err, clioutput.ErrDownloadDestinationChanged)
	data, err := os.ReadFile(filepath.Join(directory, "result.wav"))
	require.NoError(t, err)
	require.Equal(t, "replacement", string(data))
}

type audioSaveParentReplacementContext struct {
	context.Context
	path     string
	replace  func()
	replaced bool
}

func (c *audioSaveParentReplacementContext) Err() error {
	if !c.replaced {
		// The destination first exists at CopyDownloadData's initial check.
		if _, err := os.Lstat(c.path); err == nil {
			c.replaced = true
			c.replace()
		}
	}
	return c.Context.Err()
}

func TestWorkbenchAudioDefaultSaveRejectsReplacedParent(t *testing.T) {
	audioSaveTestHome(t)
	snapshot := audioSaveTestSnapshot(t, "Retained recording", "wav", "complete original audio")
	path, err := audioDefaultSavePath(snapshot.Entry)
	require.NoError(t, err)
	directory := filepath.Dir(path)
	moved := directory + "-moved"
	require.NoError(t, os.MkdirAll(directory, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "keep-existing.wav"), []byte("old sibling"), 0600))
	ctx := &audioSaveParentReplacementContext{Context: t.Context(), path: path, replace: func() {
		require.NoError(t, os.Rename(directory, moved))
		require.NoError(t, os.Mkdir(directory, 0700))
		require.NoError(t, os.WriteFile(path, []byte("replacement destination"), 0600))
	}}
	saved, err := saveWorkbenchAudio(ctx, snapshot, path, true)
	require.True(t, ctx.replaced, "The parent must change during the actual copy")
	require.ErrorIs(t, err, clioutput.ErrDownloadDestinationChanged)
	require.Empty(t, saved, "A replaced public path must not receive a success receipt")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "replacement destination", string(data))
	data, err = os.ReadFile(filepath.Join(moved, "keep-existing.wav"))
	require.NoError(t, err)
	require.Equal(t, "old sibling", string(data))
	_, err = os.Lstat(filepath.Join(moved, filepath.Base(path)))
	require.ErrorIs(t, err, os.ErrNotExist, "Cleanup must remove only the owned file through the pinned root")
}
