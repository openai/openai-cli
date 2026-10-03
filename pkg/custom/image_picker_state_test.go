package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func pickerStateSettings(t *testing.T) imagePickerSettings {
	t.Helper()
	m, err := newImagePicker(imagePickerOptions{})
	require.NoError(t, err)
	m.settings.outputDir = filepath.Join(t.TempDir(), "pictures with spaces")
	return m.settings
}

func pickerStatePath(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("APPDATA", home)
	path, err := imagePickerStatePath()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(path, home+string(filepath.Separator)))
	require.Equal(t, "image-picker.json", filepath.Base(path))
	return path
}

func TestImagePickerStateRoundTripAndPrivateReplacement(t *testing.T) {
	path := pickerStatePath(t)
	_, found, err := loadImagePickerState(t.Context(), path)
	require.NoError(t, err)
	require.False(t, found)
	_, err = os.Stat(filepath.Dir(path))
	require.ErrorIs(t, err, os.ErrNotExist, "opening the picker must not create state")
	want := pickerStateSettings(t)
	for _, folder := range []string{want.outputDir, "", filepath.Join(t.TempDir(), "再利用 folder")} {
		want.outputDir = folder
		selection := want
		selection.prompt = "  Synthetic moon over 日本語\nsecond line 🌕\t'quoted'  "
		require.NoError(t, saveImagePickerState(t.Context(), path, selection))
		got, found, err := loadImagePickerState(t.Context(), path)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, want, got)
		entries, err := os.ReadDir(filepath.Dir(path))
		require.NoError(t, err)
		require.Len(t, entries, 1, "completed writes must not retain temporary state")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0600), info.Mode().Perm())
		info, err = os.Stat(filepath.Dir(path))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0700), info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(data, &fields))
	require.ElementsMatch(t, []string{"version", "model", "size", "quality", "background", "format", "count", "output_dir"}, pickerStateKeys(fields))
}

func TestImagePickerStateIgnoresLegacyPrompt(t *testing.T) {
	path := pickerStatePath(t)
	want := pickerStateSettings(t)
	want.quality = "high"
	require.NoError(t, saveImagePickerState(t.Context(), path, want))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(data, &fields))
	fields["prompt"] = "Previous synthetic description 雪"
	legacy, err := json.Marshal(fields)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, legacy, 0600))
	got, found, err := loadImagePickerState(t.Context(), path)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, want, got)
	unchanged, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, legacy, unchanged, "loading must not rewrite saved settings")
	require.NoError(t, saveImagePickerState(t.Context(), path, got))
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(data), "prompt")
}

func pickerStateKeys(fields map[string]any) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	return keys
}

func TestImagePickerStateMalformedAndUnknownRemainUntouched(t *testing.T) {
	path := pickerStatePath(t)
	settings := pickerStateSettings(t)
	require.NoError(t, saveImagePickerState(t.Context(), path, settings))
	valid, err := os.ReadFile(path)
	require.NoError(t, err)
	for name, data := range map[string][]byte{
		"empty":                   {},
		"malformed":               []byte("synthetic-private-prompt"),
		"missing fields":          []byte(`{"version":1}`),
		"unknown version":         bytes.Replace(valid, []byte(`"version":1`), []byte(`"version":2`), 1),
		"duplicate":               bytes.Replace(valid, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1),
		"unknown field":           bytes.Replace(valid, []byte(`"version":1`), []byte(`"version":1,"future":true`), 1),
		"trailing object":         append(append([]byte{}, valid...), []byte(`{}`)...),
		"wrong type":              bytes.Replace(valid, []byte(`"count":"1"`), []byte(`"count":1`), 1),
		"null":                    bytes.Replace(valid, []byte(`"count":"1"`), []byte(`"count":null`), 1),
		"legacy null prompt":      bytes.Replace(valid, []byte(`"version":1`), []byte(`"version":1,"prompt":null`), 1),
		"legacy duplicate prompt": bytes.Replace(valid, []byte(`"version":1`), []byte(`"version":1,"prompt":"one","prompt":"two"`), 1),
		"unknown choice":          bytes.Replace(valid, []byte(`"count":"1"`), []byte(`"count":"11"`), 1),
		"invalid UTF8":            bytes.Replace(valid, []byte(`"count":"1"`), []byte{'"', 'c', 'o', 'u', 'n', 't', '"', ':', '"', 0xff, '"'}, 1),
		"oversize":                bytes.Repeat([]byte(" "), imagePickerStateLimit+1),
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, os.WriteFile(path, data, 0600))
			_, _, err := loadImagePickerState(t.Context(), path)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "synthetic-private-prompt")
			require.Error(t, saveImagePickerState(t.Context(), path, settings))
			got, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, data, got)
		})
	}
}

func TestImagePickerStateRejectsSpecialFilesAndSymlinks(t *testing.T) {
	settings := pickerStateSettings(t)
	for _, kind := range []string{"directory", "symlink", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "image-picker.json")
			switch kind {
			case "directory":
				require.NoError(t, os.Mkdir(path, 0700))
			case "symlink":
				if runtime.GOOS == "windows" {
					t.Skip("native Windows symlinks need additional privileges")
				}
				target := filepath.Join(t.TempDir(), "target")
				require.NoError(t, os.WriteFile(target, []byte("unchanged"), 0600))
				require.NoError(t, os.Symlink(target, path))
			case "fifo":
				if runtime.GOOS == "windows" {
					t.Skip("Unix FIFO semantics")
				}
				mkfifo, err := exec.LookPath("mkfifo")
				require.NoError(t, err)
				require.NoError(t, exec.Command(mkfifo, path).Run())
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			finished := make(chan error, 1)
			go func() {
				_, _, readErr := loadImagePickerState(ctx, path)
				writeErr := saveImagePickerState(ctx, path, settings)
				if readErr == nil || writeErr == nil {
					finished <- errors.New("special file accepted")
				} else {
					finished <- nil
				}
			}()
			select {
			case err := <-finished:
				require.NoError(t, err)
			case <-ctx.Done():
				t.Fatal("special file blocked state access")
			}
		})
	}
	if runtime.GOOS != "windows" {
		parent := filepath.Join(t.TempDir(), "linked-config")
		require.NoError(t, os.Symlink(t.TempDir(), parent))
		path := filepath.Join(parent, "image-picker.json")
		_, _, err := loadImagePickerState(t.Context(), path)
		require.Error(t, err)
		require.Error(t, saveImagePickerState(t.Context(), path, settings))
	}
}

func TestImagePickerStateRejectsExposedPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACLs differ from Unix mode bits")
	}
	path := pickerStatePath(t)
	settings := pickerStateSettings(t)
	require.NoError(t, saveImagePickerState(t.Context(), path, settings))
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	for _, mode := range []os.FileMode{0644, 0640, 0604, 0620, 0610} {
		require.NoError(t, os.Chmod(path, mode))
		_, _, err := loadImagePickerState(t.Context(), path)
		require.Error(t, err)
		require.Error(t, saveImagePickerState(t.Context(), path, settings))
	}
	require.NoError(t, os.Chmod(path, 0600))
	require.NoError(t, os.Chmod(filepath.Dir(path), 0770))
	_, _, err = loadImagePickerState(t.Context(), path)
	require.Error(t, err)
	require.Error(t, saveImagePickerState(t.Context(), path, settings))
	require.NoError(t, os.Chmod(filepath.Dir(path), 0700))
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestImagePickerStateWriteFailureKeepsPrevious(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACLs differ from Unix mode bits")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the write permission used by this test")
	}
	path := pickerStatePath(t)
	settings := pickerStateSettings(t)
	require.NoError(t, saveImagePickerState(t.Context(), path, settings))
	settings.quality = "high"
	require.NoError(t, os.Chmod(filepath.Dir(path), 0500))
	defer os.Chmod(filepath.Dir(path), 0700)
	require.Error(t, saveImagePickerState(t.Context(), path, settings))
	got, found, err := loadImagePickerState(t.Context(), path)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "auto", got.quality)
}

type pickerCancelAfterTemporary struct {
	context.Context
	directory string
	cancel    context.CancelFunc
	written   bool
}

func (c pickerCancelAfterTemporary) Err() error {
	entries, _ := os.ReadDir(c.directory)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".image-picker-") {
			info, err := entry.Info()
			if err == nil && (!c.written || info.Size() > 0) {
				c.cancel()
			}
		}
	}
	return c.Context.Err()
}

func TestImagePickerStateCanceledWriteKeepsPreviousAndCleansTemporary(t *testing.T) {
	path := pickerStatePath(t)
	before := pickerStateSettings(t)
	require.NoError(t, saveImagePickerState(t.Context(), path, before))
	after := before
	after.quality = "high"
	for _, written := range []bool{false, true} {
		t.Run(fmt.Sprintf("temporary written=%t", written), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			ctx = pickerCancelAfterTemporary{ctx, filepath.Dir(path), cancel, written}
			require.ErrorIs(t, saveImagePickerState(ctx, path, after), context.Canceled)
			got, found, err := loadImagePickerState(t.Context(), path)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, before, got)
			entries, err := os.ReadDir(filepath.Dir(path))
			require.NoError(t, err)
			require.Len(t, entries, 1)
			_, _, err = loadImagePickerState(ctx, path)
			require.ErrorIs(t, err, context.Canceled)
		})
	}
}

func TestImagePickerStateConcurrentSavesKeepWholeRecords(t *testing.T) {
	path := pickerStatePath(t)
	base := pickerStateSettings(t)
	require.NoError(t, saveImagePickerState(t.Context(), path, base))
	var workers sync.WaitGroup
	errs := make(chan error, 25)
	start := make(chan struct{})
	done, readerDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(readerDone)
		<-start
		for {
			select {
			case <-done:
				return
			default:
			}
			got, found, err := loadImagePickerState(t.Context(), path)
			if err != nil || !found {
				errs <- fmt.Errorf("concurrent read failed: found %t, %w", found, err)
				return
			}
			if got.prompt != "" || (got != base && got.outputDir != filepath.Join(base.outputDir, got.count)) {
				errs <- errors.New("concurrent reader saw a mixed record")
				return
			}
		}
	}()
	for i := range 24 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			settings := base
			settings.prompt = fmt.Sprintf("synthetic prompt %d", i)
			settings.count = fmt.Sprint(i%10 + 1)
			settings.outputDir = filepath.Join(base.outputDir, settings.count)
			errs <- saveImagePickerState(t.Context(), path, settings)
		}()
	}
	close(start)
	workers.Wait()
	close(done)
	<-readerDone
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	got, found, err := loadImagePickerState(t.Context(), path)
	require.NoError(t, err)
	require.True(t, found)
	require.Empty(t, got.prompt)
	require.Equal(t, filepath.Join(base.outputDir, got.count), got.outputDir, "a saved record must not mix writers")
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestImagePickerStateLimitExcludesPrompt(t *testing.T) {
	path := pickerStatePath(t)
	before := pickerStateSettings(t)
	require.NoError(t, saveImagePickerState(t.Context(), path, before))
	after := before
	after.prompt = strings.Repeat("p", imagePickerStateLimit+1)
	m := imagePicker{settings: after}
	require.True(t, m.validPrompt(), "persistence size is not a generation limit")
	require.Contains(t, after.args(), after.prompt)
	after.quality = "high"
	require.NoError(t, saveImagePickerState(t.Context(), path, after))
	got, _, err := loadImagePickerState(t.Context(), path)
	require.NoError(t, err)
	want := after
	want.prompt = ""
	require.Equal(t, want, got)
	// The limit still applies to settings that are actually stored.
	after.outputDir = filepath.Join(t.TempDir(), strings.Repeat("p", imagePickerStateLimit+1))
	require.Error(t, saveImagePickerState(t.Context(), path, after))
	got, _, err = loadImagePickerState(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestImagePickerStateRejectsUnstableSaveFolders(t *testing.T) {
	path := pickerStatePath(t)
	settings := pickerStateSettings(t)
	require.NoError(t, saveImagePickerState(t.Context(), path, settings))
	valid, err := os.ReadFile(path)
	require.NoError(t, err)
	for _, folder := range []string{".", "../pictures", "pictures with spaces", filepath.Join(t.TempDir(), "invalid") + "\x00tail"} {
		settings.outputDir = folder
		require.Error(t, saveImagePickerState(t.Context(), path, settings))
		unchanged, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, valid, unchanged)
		var fields map[string]any
		require.NoError(t, json.Unmarshal(valid, &fields))
		fields["output_dir"] = folder
		invalid, err := json.Marshal(fields)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, invalid, 0600))
		_, _, err = loadImagePickerState(t.Context(), path)
		require.Error(t, err)
		require.NoError(t, os.WriteFile(path, valid, 0600))
	}
}
