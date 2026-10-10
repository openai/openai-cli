package custom

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSchemaArtifactPreservesExactBytesAndPrivateMode(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "invoice.schema.json")
	artifact, cleanup := prepareTestSchemaArtifact(t, path)
	data := []byte(" {\n\"description\":\"" + strings.Repeat("invoice 雪", 10000) + "\"\n}\n")
	require.NoFileExists(t, path)
	stage, err := os.Stat(filepath.Join(directory, artifact.stage))
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		require.Equal(t, os.FileMode(0600), stage.Mode().Perm())
	}
	require.NoError(t, artifact.publish(t.Context(), data))
	require.NoError(t, cleanup())
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, data, got)
	info, err := os.Stat(path)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}
	requireSchemaDirectoryNames(t, directory, "invoice.schema.json")
}

func TestSchemaArtifactRejectsExistingDestinations(t *testing.T) {
	for _, kind := range []string{"file", "directory", "symlink", "dangling symlink"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "schema.json")
			target := filepath.Join(directory, "target.json")
			const original = "original synthetic artifact"
			switch kind {
			case "file":
				require.NoError(t, os.WriteFile(path, []byte(original), 0640))
			case "directory":
				require.NoError(t, os.Mkdir(path, 0700))
			case "symlink", "dangling symlink":
				if kind == "symlink" {
					require.NoError(t, os.WriteFile(target, []byte(original), 0600))
				}
				requireSchemaSymlink(t, target, path)
			}
			before, err := os.Lstat(path)
			require.NoError(t, err)
			artifact, err := prepareSchemaArtifact(path)
			require.Error(t, err)
			require.Nil(t, artifact)
			after, err := os.Lstat(path)
			require.NoError(t, err)
			require.True(t, os.SameFile(before, after))
			if kind == "file" || kind == "symlink" {
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, original, string(data))
			}
			if strings.Contains(kind, "symlink") {
				got, err := os.Readlink(path)
				require.NoError(t, err)
				require.Equal(t, target, got)
			}
			stages, err := filepath.Glob(filepath.Join(directory, ".openai-schema-*"))
			require.NoError(t, err)
			require.Empty(t, stages)
		})
	}
}

func TestSchemaArtifactRejectsInvalidDestinations(t *testing.T) {
	for _, path := range []string{"", "-", t.TempDir() + string(os.PathSeparator), ".", "..", filepath.Join(t.TempDir(), "missing", "schema.json")} {
		artifact, err := prepareSchemaArtifact(path)
		require.Error(t, err)
		require.Nil(t, artifact)
	}
}

func TestSchemaArtifactPreservesDestinationCreatedAfterPrepare(t *testing.T) {
	for _, kind := range []string{"file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "schema.json")
			artifact, cleanup := prepareTestSchemaArtifact(t, path)
			const original = "concurrently created synthetic file"
			target := path
			if kind == "symlink" {
				target = filepath.Join(directory, "target.json")
			}
			require.NoError(t, os.WriteFile(target, []byte(original), 0600))
			if kind == "symlink" {
				requireSchemaSymlink(t, target, path)
			}
			before, err := os.Lstat(path)
			require.NoError(t, err)
			require.Error(t, artifact.publish(t.Context(), []byte(`{"type":"object"}`)))
			require.NoError(t, cleanup())
			after, err := os.Lstat(path)
			require.NoError(t, err)
			require.True(t, os.SameFile(before, after))
			got, err := os.ReadFile(target)
			require.NoError(t, err)
			require.Equal(t, original, string(got))
		})
	}
}

func TestSchemaArtifactConcurrentPreparedWritersHaveOneWinner(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "schema.json")
	first, firstCleanup := prepareTestSchemaArtifact(t, path)
	second, secondCleanup := prepareTestSchemaArtifact(t, path)
	require.NotEqual(t, first.stage, second.stage)
	start := make(chan struct{})
	type result struct {
		data []byte
		err  error
	}
	results := make(chan result, 2)
	for i, artifact := range []*schemaArtifact{first, second} {
		go func() {
			data := bytes.Repeat([]byte{byte('a' + i)}, 64*1024)
			<-start
			results <- result{data, artifact.publish(t.Context(), data)}
		}()
	}
	close(start)
	winners := 0
	var winningData []byte
	for range 2 {
		result := <-results
		if result.err == nil {
			winners++
			winningData = result.data
		} else {
			require.ErrorIs(t, result.err, os.ErrExist)
		}
	}
	require.Equal(t, 1, winners)
	require.NoError(t, firstCleanup())
	require.NoError(t, secondCleanup())
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, winningData, got)
	requireSchemaDirectoryNames(t, directory, "schema.json")
}

func TestSchemaArtifactRejectsReplacedParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit this open-directory rename scenario")
	}
	directory := t.TempDir()
	original := filepath.Join(directory, "original")
	moved := filepath.Join(directory, "moved")
	require.NoError(t, os.Mkdir(original, 0700))
	artifact, cleanup := prepareTestSchemaArtifact(t, filepath.Join(original, "schema.json"))
	require.NoError(t, os.Rename(original, moved))
	require.NoError(t, os.Mkdir(original, 0700))
	data := []byte(`{"type":"object"}`)
	err := artifact.publish(t.Context(), data)
	require.Error(t, err)
	require.Contains(t, err.Error(), "directory changed")
	require.NoError(t, cleanup())
	requireSchemaDirectoryNames(t, original)
	requireSchemaDirectoryNames(t, moved)
}

func TestSchemaArtifactCancellationDoesNotPublish(t *testing.T) {
	for _, phase := range []string{"before write", "during write", "before publish"} {
		t.Run(phase, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "schema.json")
			artifact, cleanup := prepareTestSchemaArtifact(t, path)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			data := bytes.Repeat([]byte("x"), 128*1024)
			controlled := schemaArtifactContext{Context: ctx, check: func() {
				info, err := os.Stat(filepath.Join(directory, artifact.stage))
				require.NoError(t, err)
				if phase == "before write" || phase == "during write" && info.Size() > 0 || phase == "before publish" && info.Size() == int64(len(data)) {
					cancel()
				}
			}}
			require.ErrorIs(t, artifact.publish(controlled, data), context.Canceled)
			require.NoError(t, cleanup())
			require.NoFileExists(t, path)
			requireSchemaDirectoryNames(t, directory)
		})
	}
}

func TestSchemaArtifactCleanupRemovesOnlyItsStage(t *testing.T) {
	directory := t.TempDir()
	artifact, cleanup := prepareTestSchemaArtifact(t, filepath.Join(directory, "schema.json"))
	other := filepath.Join(directory, ".openai-schema-unowned")
	require.NoError(t, os.WriteFile(other, []byte("other operation"), 0600))
	require.NoError(t, cleanup())
	require.NoFileExists(t, filepath.Join(directory, artifact.stage))
	requireSchemaDirectoryNames(t, directory, filepath.Base(other))
}

func TestSchemaArtifactRejectsReplacedStage(t *testing.T) {
	for _, kind := range []string{"file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("Windows does not permit replacing this open staging file")
			}
			directory := t.TempDir()
			path := filepath.Join(directory, "schema.json")
			artifact, cleanup := prepareTestSchemaArtifact(t, path)
			stage := filepath.Join(directory, artifact.stage)
			require.NoError(t, os.Rename(stage, filepath.Join(directory, "moved-stage")))
			const replacement = "unowned replacement"
			target := stage
			if kind == "symlink" {
				target = filepath.Join(directory, "unowned-target")
			}
			require.NoError(t, os.WriteFile(target, []byte(replacement), 0600))
			if kind == "symlink" {
				requireSchemaSymlink(t, target, stage)
			}
			before, err := os.Lstat(stage)
			require.NoError(t, err)
			require.Error(t, artifact.publish(t.Context(), []byte(`{"type":"object"}`)))
			_ = cleanup()
			require.NoFileExists(t, path)
			after, err := os.Lstat(stage)
			require.NoError(t, err)
			require.True(t, os.SameFile(before, after), "cleanup must preserve an unowned replacement")
			got, err := os.ReadFile(target)
			require.NoError(t, err)
			require.Equal(t, replacement, string(got))
		})
	}
}

func TestSchemaArtifactDoesNotPublishStaleSuffix(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "schema.json")
	artifact, cleanup := prepareTestSchemaArtifact(t, path)
	stage := filepath.Join(directory, artifact.stage)
	require.NoError(t, os.WriteFile(stage, bytes.Repeat([]byte("unvalidated"), 100), 0600))
	data := []byte(`{"type":"object"}`)
	err := artifact.publish(t.Context(), data)
	require.NoError(t, cleanup())
	if err != nil {
		require.NoFileExists(t, path)
		return
	}
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, data, got, "a successful save must contain only the validated bytes")
}

func TestSchemaArtifactRejectsContentChangedBeforePublication(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "schema.json")
	artifact, cleanup := prepareTestSchemaArtifact(t, path)
	stage := filepath.Join(directory, artifact.stage)
	data := []byte(`{"type":"object"}`)
	changed := false
	ctx := schemaArtifactContext{Context: t.Context(), check: func() {
		info, err := os.Stat(stage)
		require.NoError(t, err)
		if !changed && info.Size() == int64(len(data)) {
			changed = true
			require.NoError(t, os.WriteFile(stage, bytes.Repeat([]byte("x"), len(data)), 0600))
		}
	}}
	err := artifact.publish(ctx, data)
	require.True(t, changed, "the fixture must alter the completely written stage")
	require.Error(t, err)
	require.NoError(t, cleanup())
	requireSchemaDirectoryNames(t, directory)
}

func TestSchemaArtifactReadOnlyParentRejectsStaging(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix permissions under an unprivileged account")
	}
	directory := t.TempDir()
	require.NoError(t, os.Chmod(directory, 0500))
	t.Cleanup(func() { require.NoError(t, os.Chmod(directory, 0700)) })
	artifact, err := prepareSchemaArtifact(filepath.Join(directory, "schema.json"))
	require.ErrorIs(t, err, os.ErrPermission)
	require.Nil(t, artifact)
	requireSchemaDirectoryNames(t, directory)
}

func TestSchemaArtifactWriteFailureDoesNotPublish(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "schema.json")
	artifact, cleanup := prepareTestSchemaArtifact(t, path)
	require.NoError(t, artifact.file.Close())
	err := artifact.publish(t.Context(), []byte(`{"type":"object"}`))
	require.ErrorIs(t, err, os.ErrClosed)
	cleanupErr := cleanup()
	require.True(t, cleanupErr == nil || errors.Is(cleanupErr, os.ErrClosed))
	require.NoFileExists(t, path)
	requireSchemaDirectoryNames(t, directory)
}

func prepareTestSchemaArtifact(t *testing.T, path string) (*schemaArtifact, func() error) {
	t.Helper()
	artifact, err := prepareSchemaArtifact(path)
	require.NoError(t, err)
	cleaned := false
	cleanup := func() error {
		if cleaned {
			return nil
		}
		cleaned = true
		return artifact.cleanup()
	}
	t.Cleanup(func() {
		if !cleaned {
			require.NoError(t, cleanup())
		}
	})
	return artifact, cleanup
}

func requireSchemaDirectoryNames(t *testing.T, directory string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	require.ElementsMatch(t, want, names)
}

func requireSchemaSymlink(t *testing.T, target, path string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		if runtime.GOOS == "windows" && errors.Is(err, os.ErrPermission) {
			t.Skip("Windows account lacks symlink permission")
		}
		require.NoError(t, err)
	}
}

type schemaArtifactContext struct {
	context.Context
	check func()
}

func (ctx schemaArtifactContext) Err() error {
	ctx.check()
	return ctx.Context.Err()
}
