package custom

import (
	"context"
	"encoding/json"
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

func projectLinkTestPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "openai", "project-links.json")
}

func writeProjectLinkTestFile(t *testing.T, path, data string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte(data), 0600))
}

func TestProjectLinkStoreUpdatesAndRemovesExactEntries(t *testing.T) {
	path := projectLinkTestPath(t)
	directory := t.TempDir()
	child := filepath.Join(directory, "child")
	links, err := loadProjectLinks(t.Context(), path)
	require.NoError(t, err)
	require.Empty(t, links)
	_, err = os.Stat(filepath.Dir(path))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = updateProjectLink(t.Context(), path, directory, "proj_parent")
	require.NoError(t, err)
	_, err = updateProjectLink(t.Context(), path, child, "proj_child")
	require.NoError(t, err)
	links, err = updateProjectLink(t.Context(), path, directory, "proj_replacement")
	require.NoError(t, err)
	require.Equal(t, map[string]string{directory: "proj_replacement", child: "proj_child"}, links)
	links, err = updateProjectLink(t.Context(), path, child, "")
	require.NoError(t, err)
	require.Equal(t, map[string]string{directory: "proj_replacement"}, links)
	links, err = loadProjectLinks(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, map[string]string{directory: "proj_replacement"}, links)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0600), info.Mode().Perm())
		info, err = os.Stat(filepath.Dir(path))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0700), info.Mode().Perm())
	}
	files, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, files, 2, "only registry and persistent lock remain")
}

func TestProjectLinkStoreRejectsMalformedWithoutOverwriting(t *testing.T) {
	directory, err := json.Marshal(t.TempDir())
	require.NoError(t, err)
	key := string(directory)
	for name, data := range map[string]string{
		"empty": "", "null": "null", "array": "[]", "trailing": "{} {}",
		"duplicate":    "{" + key + ":\"proj_one\"," + key + ":\"proj_two\"}",
		"null-project": "{" + key + ":null}", "empty-project": "{" + key + ":\"\"}",
		"wrong-project": "{" + key + ":\"sk_fake\"}", "numeric-project": "{" + key + ":12}",
		"relative-folder": "{\"relative\":\"proj_one\"}",
		"unclean-folder":  "{\"/folder/../elsewhere\":\"proj_one\"}",
		"invalid-utf8":    "{\"/\xff\":\"proj_one\"}",
		"oversized":       strings.Repeat(" ", maxProjectLinkBytes) + "{}",
	} {
		t.Run(name, func(t *testing.T) {
			path := projectLinkTestPath(t)
			writeProjectLinkTestFile(t, path, data)
			_, err := loadProjectLinks(t.Context(), path)
			require.Error(t, err)
			_, err = updateProjectLink(t.Context(), path, t.TempDir(), "proj_new")
			require.Error(t, err)
			actual, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, data, string(actual))
		})
	}
}

func TestProjectLinkStoreLocalSizeBoundary(t *testing.T) {
	path := projectLinkTestPath(t)
	before := "{}" + strings.Repeat(" ", maxProjectLinkBytes-2)
	writeProjectLinkTestFile(t, path, before)
	links, err := loadProjectLinks(t.Context(), path)
	require.NoError(t, err)
	require.Empty(t, links)
	_, err = updateProjectLink(t.Context(), path, t.TempDir(), "proj_"+strings.Repeat("a", maxProjectLinkBytes))
	require.ErrorContains(t, err, "1 MiB")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, string(data))
}

func TestProjectLinkStoreRejectsInvalidUpdateBeforeCreation(t *testing.T) {
	for _, values := range [][2]string{{"relative", "proj_one"}, {t.TempDir(), "sk_fake"}, {"/bad\x00path", "proj_one"}} {
		path := projectLinkTestPath(t)
		_, err := updateProjectLink(t.Context(), path, values[0], values[1])
		require.Error(t, err)
		_, err = os.Stat(filepath.Dir(path))
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}

func TestProjectLinkStorePreservesMissingFoldersAndEscapesControls(t *testing.T) {
	path := projectLinkTestPath(t)
	directory := filepath.Join(t.TempDir(), "missing\n\x1b[31m")
	_, err := updateProjectLink(t.Context(), path, directory, "proj_saved")
	require.NoError(t, err)
	links, err := loadProjectLinks(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, "proj_saved", links[directory])
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(data), "\x1b")
}

func TestProjectLinkStoreRejectsNonregularFiles(t *testing.T) {
	for _, kind := range []string{"directory", "symlink", "config-symlink"} {
		t.Run(kind, func(t *testing.T) {
			path := projectLinkTestPath(t)
			if kind == "config-symlink" {
				target := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(target, filepath.Base(path)), []byte("{}"), 0600))
				if err := os.Symlink(target, filepath.Dir(path)); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			} else {
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
				if kind == "directory" {
					require.NoError(t, os.Mkdir(path, 0700))
				} else {
					target := filepath.Join(t.TempDir(), "target")
					require.NoError(t, os.WriteFile(target, []byte("{}"), 0600))
					if err := os.Symlink(target, path); err != nil {
						t.Skipf("symlinks unavailable: %v", err)
					}
				}
			}
			_, err := loadProjectLinks(t.Context(), path)
			require.Error(t, err)
			_, err = updateProjectLink(t.Context(), path, t.TempDir(), "proj_new")
			require.Error(t, err)
		})
	}
}

func TestProjectLinkStoreMissingRegistryPreservesSymlinkedConfiguration(t *testing.T) {
	path := projectLinkTestPath(t)
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Dir(path)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	links, err := loadProjectLinks(t.Context(), path)
	require.NoError(t, err)
	require.Empty(t, links)
	_, err = updateProjectLink(t.Context(), path, t.TempDir(), "proj_new")
	require.ErrorContains(t, err, "private configuration directory")
	_, err = os.Stat(filepath.Join(target, filepath.Base(path)))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestProjectLinkStoreConcurrentWritersPreserveEntries(t *testing.T) {
	path := projectLinkTestPath(t)
	const writers = 12
	start := make(chan struct{})
	results := make(chan error, writers)
	directory := t.TempDir()
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() {
			<-start
			_, err := updateProjectLink(t.Context(), path, filepath.Join(directory, fmt.Sprint(i)), fmt.Sprintf("proj_%d", i))
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	links, err := loadProjectLinks(t.Context(), path)
	require.NoError(t, err)
	require.Len(t, links, writers)
	for i := range writers {
		require.Equal(t, fmt.Sprintf("proj_%d", i), links[filepath.Join(directory, fmt.Sprint(i))])
	}
}

func TestProjectLinkStoreCancellationReleasesLock(t *testing.T) {
	path := projectLinkTestPath(t)
	root, name, err := openProjectLinkDirectory(path, true)
	require.NoError(t, err)
	defer root.Close()
	lock, err := lockProjectLinks(t.Context(), root, name)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err = updateProjectLink(ctx, path, t.TempDir(), "proj_cancelled")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, lock.Close())
	_, err = updateProjectLink(t.Context(), path, t.TempDir(), "proj_after")
	require.NoError(t, err)
}

func TestProjectLinkStoreSnapshotRejectsReplacement(t *testing.T) {
	path := projectLinkTestPath(t)
	writeProjectLinkTestFile(t, path, "{}")
	root, name, err := openProjectLinkDirectory(path, false)
	require.NoError(t, err)
	defer root.Close()
	info, err := root.Lstat(name)
	require.NoError(t, err)
	temporary := filepath.Join(filepath.Dir(path), "replacement")
	require.NoError(t, os.WriteFile(temporary, []byte("{}"), 0600))
	require.NoError(t, os.Rename(temporary, path))
	_, err = readProjectLinkSnapshot(t.Context(), root, name, info)
	require.ErrorIs(t, err, errProjectLinksChanged)
}

func TestProjectLinkStoreSnapshotRejectsInPlaceRewrite(t *testing.T) {
	path := projectLinkTestPath(t)
	writeProjectLinkTestFile(t, path, "{}")
	root, name, err := openProjectLinkDirectory(path, false)
	require.NoError(t, err)
	defer root.Close()
	info, err := root.Lstat(name)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("{} "), 0600))
	_, err = readProjectLinkSnapshot(t.Context(), root, name, info)
	require.ErrorIs(t, err, errProjectLinksChanged)
}

func TestProjectLinkStoreCancelledContextDoesNotCreateState(t *testing.T) {
	path := projectLinkTestPath(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := loadProjectLinks(ctx, path)
	require.ErrorIs(t, err, context.Canceled)
	_, err = updateProjectLink(ctx, path, t.TempDir(), "proj_cancelled")
	require.ErrorIs(t, err, context.Canceled)
	_, err = os.Stat(filepath.Dir(path))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestProjectLinkStoreRejectsUnsafeLockFiles(t *testing.T) {
	for _, kind := range []string{"directory", "symlink", "dangling-symlink"} {
		t.Run(kind, func(t *testing.T) {
			path := projectLinkTestPath(t)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
			lockPath := filepath.Join(filepath.Dir(path), ".project-links.json.lock")
			target := filepath.Join(t.TempDir(), "target")
			if kind == "directory" {
				require.NoError(t, os.Mkdir(lockPath, 0700))
			} else {
				if kind == "symlink" {
					require.NoError(t, os.WriteFile(target, []byte("keep"), 0600))
				}
				if err := os.Symlink(target, lockPath); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			_, err := updateProjectLink(t.Context(), path, t.TempDir(), "proj_new")
			require.Error(t, err)
			_, err = os.Stat(path)
			require.ErrorIs(t, err, os.ErrNotExist)
			if kind == "symlink" {
				data, err := os.ReadFile(target)
				require.NoError(t, err)
				require.Equal(t, "keep", string(data))
			} else {
				_, err = os.Stat(target)
				require.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
}

type observedProjectLinkWait struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

func (ctx *observedProjectLinkWait) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.waiting) })
	return ctx.Context.Done()
}

func TestProjectLinkStoreWaitingWriterValidatesCurrentState(t *testing.T) {
	for _, replacement := range []string{"malformed-registry", "replaced-lock"} {
		t.Run(replacement, func(t *testing.T) {
			path := projectLinkTestPath(t)
			writeProjectLinkTestFile(t, path, "{}")
			root, name, err := openProjectLinkDirectory(path, false)
			require.NoError(t, err)
			defer root.Close()
			first, err := lockProjectLinks(t.Context(), root, name)
			require.NoError(t, err)
			defer first.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			observed := &observedProjectLinkWait{Context: ctx, waiting: make(chan struct{})}
			result := make(chan error, 1)
			directory := t.TempDir()
			go func() {
				_, err := updateProjectLink(observed, path, directory, "proj_new")
				result <- err
			}()
			select {
			case <-observed.waiting:
			case err := <-result:
				t.Fatalf("writer did not wait: %v", err)
			case <-ctx.Done():
				t.Fatal("writer did not reach lock wait")
			}
			wantData, wantError := "{}", "lock changed"
			if replacement == "malformed-registry" {
				wantData, wantError = "{\"future-schema\":1}", "invalid project links"
				temporary := filepath.Join(filepath.Dir(path), "replacement")
				require.NoError(t, os.WriteFile(temporary, []byte(wantData), 0600))
				require.NoError(t, os.Rename(temporary, path))
			} else {
				lockPath := filepath.Join(filepath.Dir(path), "."+name+".lock")
				require.NoError(t, os.Rename(lockPath, lockPath+".old"))
				require.NoError(t, os.WriteFile(lockPath, nil, 0600))
			}
			require.NoError(t, first.Close())
			select {
			case err := <-result:
				require.ErrorContains(t, err, wantError)
			case <-ctx.Done():
				t.Fatal("waiting writer did not finish")
			}
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, wantData, string(data))
		})
	}
}

func TestProjectLinkStoreProcessExitReleasesLock(t *testing.T) {
	const helperPath = "OPENAI_CLI_TEST_PROJECT_LINK_LOCK"
	if path := os.Getenv(helperPath); path != "" {
		root, name, err := openProjectLinkDirectory(path, true)
		require.NoError(t, err)
		_, err = lockProjectLinks(t.Context(), root, name)
		require.NoError(t, err)
		// Intentionally exit without Close to exercise kernel lock cleanup.
		os.Exit(0)
	}
	path := projectLinkTestPath(t)
	child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestProjectLinkStoreProcessExitReleasesLock$")
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(name), "OPENAI_") {
			child.Env = append(child.Env, entry)
		}
	}
	child.Env = append(child.Env, helperPath+"="+path)
	output, err := child.CombinedOutput()
	require.NoError(t, err, "%s", output)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, err = updateProjectLink(ctx, path, t.TempDir(), "proj_after_exit")
	require.NoError(t, err)
}
