package custom

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type failedDownloadBody struct {
	io.Reader
	closeErr error
	closed   int
}

func (b *failedDownloadBody) Close() error { b.closed++; return b.closeErr }

func requireNoDownloadStages(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".openai-download-") {
			t.Errorf("incomplete staging file remains: %s", entry.Name())
		}
	}
}

func TestManagedDownloadPreservesDestinationOnTransferFailure(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, kind := range []string{"read", "close", "read and close"} {
			t.Run(kind+map[bool]string{true: " existing", false: " new"}[existing], func(t *testing.T) {
				directory := t.TempDir()
				path := filepath.Join(directory, "download.bin")
				var original os.FileInfo
				if existing {
					if err := os.WriteFile(path, []byte("previous good contents"), 0o640); err != nil {
						t.Fatal(err)
					}
					original, _ = os.Stat(path)
				}
				readErr := errors.New("synthetic source failure")
				closeErr := errors.New("synthetic source close failure")
				body := &failedDownloadBody{Reader: bytes.NewReader([]byte{0, 255, 1, 2})}
				if kind != "close" {
					body.Reader = &errorAfterDownloadReader{Reader: body.Reader, err: readErr}
				}
				if kind != "read" {
					body.closeErr = closeErr
				}
				message, err := WriteBinaryResponse(&http.Response{Body: body}, io.Discard, path)
				if message != "" || err == nil {
					t.Fatalf("result = %q, %v; want failure without receipt", message, err)
				}
				if kind != "close" && !errors.Is(err, readErr) || kind != "read" && !errors.Is(err, closeErr) {
					t.Errorf("error lost an expected cause: %v", err)
				}
				if body.closed != 1 {
					t.Errorf("source closed %d times, want once", body.closed)
				}
				if existing {
					data, readErr := os.ReadFile(path)
					current, statErr := os.Stat(path)
					if readErr != nil || statErr != nil || string(data) != "previous good contents" || !os.SameFile(original, current) {
						t.Errorf("previous destination changed: %q, %v, %v", data, readErr, statErr)
					}
				} else if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("new destination exists after failure: %v", err)
				}
				requireNoDownloadStages(t, directory)
			})
		}
	}
}

func TestManagedDownloadPreservesRegularInode(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "download.bin")
	if err := os.WriteFile(path, []byte("long previous contents"), 0o640); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	payload := []byte{0, 255, 10}
	message, err := WriteBinaryResponse(&http.Response{Body: io.NopCloser(bytes.NewReader(payload))}, io.Discard, path)
	if err != nil || message == "" {
		t.Fatalf("save = %q, %v", message, err)
	}
	after, _ := os.Stat(path)
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, payload) || !os.SameFile(before, after) || before.Mode() != after.Mode() {
		t.Errorf("save changed identity, mode, or bytes: %q, %v", actual, err)
	}
	requireNoDownloadStages(t, directory)
}

func TestManagedDownloadRejectsConcurrentDestinationChanges(t *testing.T) {
	for _, action := range []string{"new destination", "replacement", "same inode"} {
		t.Run(action, func(t *testing.T) {
			if action == "replacement" && runtime.GOOS == "windows" {
				t.Skip("Windows prevents renaming the open destination handle")
			}
			directory := t.TempDir()
			path := filepath.Join(directory, "download.bin")
			if action != "new destination" {
				if err := os.WriteFile(path, []byte("previous"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			body := &inspectingDownloadReader{
				Reader: strings.NewReader("new download"),
				inspect: func() error {
					if action == "replacement" {
						if err := os.Rename(path, path+".original"); err != nil {
							return err
						}
					}
					return os.WriteFile(path, []byte("unrelated concurrent contents"), 0o600)
				},
			}
			message, err := WriteBinaryResponse(&http.Response{Body: io.NopCloser(body)}, io.Discard, path)
			if err == nil || message != "" || !errors.Is(err, errDownloadDestinationChanged) {
				t.Fatalf("save = %q, %v; want concurrent-change error", message, err)
			}
			actual, readErr := os.ReadFile(path)
			if readErr != nil || string(actual) != "unrelated concurrent contents" {
				t.Errorf("concurrent contents changed: %q, %v", actual, readErr)
			}
			requireNoDownloadStages(t, directory)
		})
	}
}

func TestManagedDownloadDanglingSymlinkFailure(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.bin")
	middle := filepath.Join(directory, "middle.bin")
	path := filepath.Join(directory, "output.bin")
	if err := os.Symlink("target.bin", middle); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if err := os.Symlink("middle.bin", path); err != nil {
		t.Fatal(err)
	}
	body := io.NopCloser(&errorAfterDownloadReader{Reader: strings.NewReader("partial"), err: io.ErrUnexpectedEOF})
	if _, err := WriteBinaryResponse(&http.Response{Body: body}, io.Discard, path); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("save error = %v", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dangling target exists after failure: %v", err)
	}
	for _, link := range []string{middle, path} {
		if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("symlink was replaced: %s, %v", link, err)
		}
	}
	requireNoDownloadStages(t, directory)
}

func TestManagedDownloadResolvesParentBeforeDotDot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows path normalization differs; native Windows symlink checks cover that platform")
	}
	directory := t.TempDir()
	actualParent := filepath.Join(directory, "actual")
	if err := os.MkdirAll(filepath.Join(actualParent, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("actual", "nested"), filepath.Join(directory, "link")); err != nil {
		t.Fatal(err)
	}
	path := directory + "/link/../output.bin"
	if _, err := WriteBinaryResponse(&http.Response{Body: io.NopCloser(strings.NewReader("download"))}, io.Discard, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(actualParent, "output.bin"))
	if err != nil || string(data) != "download" {
		t.Errorf("kernel-equivalent target = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "output.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("lexically cleaned target exists: %v", err)
	}
}

func TestManagedDownloadRetainsReplacedStage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows prevents renaming the open staging handle")
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "output.bin")
	var replacement string
	body := &inspectingDownloadReader{
		Reader: strings.NewReader("download"),
		inspect: func() error {
			entries, err := os.ReadDir(directory)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".openai-download-") {
					replacement = filepath.Join(directory, entry.Name())
					if err := os.Rename(replacement, replacement+".moved"); err != nil {
						return err
					}
					return os.WriteFile(replacement, []byte("unrelated stage replacement"), 0o600)
				}
			}
			return errors.New("staging file missing")
		},
	}
	if _, err := WriteBinaryResponse(&http.Response{Body: io.NopCloser(body)}, io.Discard, path); !errors.Is(err, errDownloadDestinationChanged) {
		t.Fatalf("save error = %v", err)
	}
	data, err := os.ReadFile(replacement)
	if err != nil || string(data) != "unrelated stage replacement" {
		t.Errorf("unrelated replacement was removed: %q, %v", data, err)
	}
}

type blockingDownloadBody struct {
	started chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (b *blockingDownloadBody) Read([]byte) (int, error) {
	close(b.started)
	<-b.closed
	return 0, os.ErrClosed
}
func (b *blockingDownloadBody) Close() error { b.once.Do(func() { close(b.closed) }); return nil }

func TestManagedDownloadCancellationClosesSource(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "output.bin")
	if err := os.WriteFile(path, []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1/synthetic", nil)
	body := &blockingDownloadBody{started: make(chan struct{}), closed: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, err := WriteBinaryResponse(&http.Response{Body: body, Request: request}, io.Discard, path)
		done <- err
	}()
	<-body.started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("canceled save error = %v", err)
		}
		var failure *downloadSaveError
		if !errors.As(err, &failure) || failure.ExitCode() != 130 {
			t.Errorf("canceled save did not preserve exit 130: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not unblock source")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "previous" {
		t.Errorf("cancellation changed previous file: %q, %v", data, err)
	}
	requireNoDownloadStages(t, directory)
}

type simultaneousDownloadReader struct{ err error }

func (r simultaneousDownloadReader) Read(p []byte) (int, error) {
	return copy(p, "partial"), r.err
}

func TestDownloadPreservesSimultaneousReadWriteCloseFailures(t *testing.T) {
	readErr := errors.New("synthetic read failure")
	writeErr := errors.New("synthetic write failure")
	closeErr := errors.New("synthetic close failure")
	file := &closeTrackingDownloadFile{closeErr: closeErr, writeErr: writeErr, writeLimit: 3}
	err := copyDownloadFile(file, simultaneousDownloadReader{readErr})
	for _, expected := range []error{readErr, writeErr, closeErr} {
		if !errors.Is(err, expected) {
			t.Errorf("error %v lost %v", err, expected)
		}
	}
}

type failedDownloadCompletion struct {
	closeTrackingDownloadFile
	truncateErr   error
	truncateCalls int
	shortWrite    bool
}

func (f *failedDownloadCompletion) Truncate(int64) error {
	f.truncateCalls++
	return f.truncateErr
}
func (f *failedDownloadCompletion) Write(data []byte) (int, error) {
	if f.shortWrite {
		return len(data) - 1, nil
	}
	return f.closeTrackingDownloadFile.Write(data)
}

func TestManagedDownloadCompletionFailures(t *testing.T) {
	for _, kind := range []string{"truncate", "write", "short write", "close", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			failure := errors.New("synthetic completion failure")
			file := &failedDownloadCompletion{}
			ctx := context.Background()
			switch kind {
			case "truncate":
				file.truncateErr = failure
			case "write":
				file.writeErr, file.writeLimit = failure, 3
			case "short write":
				file.shortWrite, failure = true, io.ErrShortWrite
			case "close":
				file.closeErr = failure
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				failure = context.Canceled
			}
			sourceClose := errors.New("synthetic staging close failure")
			source := &failedDownloadBody{Reader: strings.NewReader("complete download"), closeErr: sourceClose}
			err := completeExistingDownload(ctx, file, source)
			if !errors.Is(err, failure) || !errors.Is(err, sourceClose) {
				t.Errorf("completion lost failure causes: %v", err)
			}
			if source.closed != 1 || file.closeCalls != 1 {
				t.Errorf("source closes=%d, destination closes=%d; want one each", source.closed, file.closeCalls)
			}
			if kind == "canceled" && file.truncateCalls != 0 {
				t.Error("canceled completion truncated the existing file")
			}
		})
	}
}

func TestDownloadDoesNotSuppressErrorsJoinedWithEOF(t *testing.T) {
	failure := errors.New("synthetic source failure")
	var destination bytes.Buffer
	err := copyDownloadData(context.Background(), &destination, simultaneousDownloadReader{errors.Join(io.EOF, failure)})
	if !errors.Is(err, failure) {
		t.Errorf("source failure was lost: %v", err)
	}
}

func TestManagedDownloadExistingAccessPermissions(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("requires an unprivileged Unix permission model")
	}
	t.Run("readonly file", func(t *testing.T) {
		directory := t.TempDir()
		path := filepath.Join(directory, "readonly.bin")
		if err := os.WriteFile(path, []byte("previous"), 0o400); err != nil {
			t.Fatal(err)
		}
		body := &failedDownloadBody{Reader: strings.NewReader("download")}
		if _, err := WriteBinaryResponse(&http.Response{Body: body}, io.Discard, path); !errors.Is(err, os.ErrPermission) {
			t.Errorf("readonly save error = %v, want permission error", err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "previous" || body.closed != 1 {
			t.Errorf("readonly file or source ownership changed: %q, %v, closes=%d", data, err, body.closed)
		}
		requireNoDownloadStages(t, directory)
	})
	t.Run("readonly parent", func(t *testing.T) {
		directory := t.TempDir()
		fallback := t.TempDir()
		t.Setenv("TMPDIR", fallback)
		path := filepath.Join(directory, "writable.bin")
		if err := os.WriteFile(path, []byte("previous"), 0o600); err != nil {
			t.Fatal(err)
		}
		original, _ := os.Stat(path)
		if err := os.Chmod(directory, 0o500); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(directory, 0o700)
		if _, err := WriteBinaryResponse(&http.Response{Body: io.NopCloser(strings.NewReader("download"))}, io.Discard, path); err != nil {
			t.Fatalf("writable file inside readonly parent: %v", err)
		}
		current, _ := os.Stat(path)
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "download" || !os.SameFile(original, current) {
			t.Errorf("fallback changed identity or bytes: %q, %v", data, err)
		}
		requireNoDownloadStages(t, directory)
		requireNoDownloadStages(t, fallback)
	})
}

func TestManagedDownloadCopyCallbackRejectsSemanticFailure(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "download.bin")
	if err := os.WriteFile(path, []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("synthetic event failure")
	body := &downloadResponseBody{ReadCloser: io.NopCloser(strings.NewReader("valid wire framing with failure event"))}
	_, err := writeManagedDownloadResponse(&http.Response{Body: body}, path,
		func(ctx context.Context, destination io.Writer, source io.Reader) error {
			return errors.Join(copyDownloadData(ctx, destination, source), failure)
		})
	if !errors.Is(err, failure) {
		t.Errorf("semantic copy failure was lost: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "previous" {
		t.Errorf("semantic failure changed destination: %q, %v", data, err)
	}
	requireNoDownloadStages(t, directory)
}

func TestBinaryResponsePreservesSourceWriteAndBodyCloseFailures(t *testing.T) {
	readErr := errors.New("synthetic source failure")
	writeErr := errors.New("synthetic stdout failure")
	closeErr := errors.New("synthetic body close failure")
	body := &failedDownloadBody{Reader: simultaneousDownloadReader{readErr}, closeErr: closeErr}
	output := &closeTrackingDownloadFile{writeErr: writeErr, writeLimit: 3}
	message, err := WriteBinaryResponse(&http.Response{Body: body}, output, "-")
	for _, expected := range []error{readErr, writeErr, closeErr} {
		if !errors.Is(err, expected) {
			t.Errorf("error %v lost %v", err, expected)
		}
	}
	if message != "" || body.closed != 1 || output.closeCalls != 0 {
		t.Errorf("message=%q, source closes=%d, stdout closes=%d", message, body.closed, output.closeCalls)
	}
}

func TestManagedDownloadPublicationWithoutHardlinks(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(map[bool]string{false: "exclusive fallback", true: "concurrent destination"}[concurrent], func(t *testing.T) {
			directory := t.TempDir()
			stage, err := newDownloadStage(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := stage.cleanup(); err != nil {
					t.Error(err)
				}
			}()
			payload := []byte{0, 255, 128, 10}
			if _, err := stage.file.Write(payload); err != nil {
				t.Fatal(err)
			}
			if err := stage.closeWriter(); err != nil {
				t.Fatal(err)
			}
			link := func(string, string) error {
				if concurrent {
					if err := os.WriteFile(filepath.Join(directory, "output.bin"), []byte("unrelated"), 0o600); err != nil {
						return err
					}
				}
				return errors.New("synthetic filesystem without hardlink support")
			}
			saved, incomplete, err := publishNewDownload(context.Background(), stage, "output.bin", link)
			if concurrent {
				if err == nil || saved != nil || incomplete {
					t.Errorf("concurrent publication = %v, %v, %v", saved, incomplete, err)
				}
				payload = []byte("unrelated")
			} else if err != nil || saved == nil || incomplete {
				t.Errorf("fallback publication = %v, %v, %v", saved, incomplete, err)
			}
			actual, readErr := os.ReadFile(filepath.Join(directory, "output.bin"))
			if readErr != nil || !bytes.Equal(actual, payload) {
				t.Errorf("published bytes = %q, %v", actual, readErr)
			}
		})
	}
}

func TestManagedDownloadRestrictiveUmask(t *testing.T) {
	const childDestination = "OPENAI_TEST_MANAGED_DOWNLOAD_UMASK_DESTINATION"
	if path := os.Getenv(childDestination); path != "" {
		_, err := WriteBinaryResponse(&http.Response{Body: io.NopCloser(strings.NewReader("complete download"))}, io.Discard, path)
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not use Unix umask")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mask := range []os.FileMode{0o077, 0o400, 0o777} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("mask_%03o_existing_%t", mask, existing), func(t *testing.T) {
				directory := t.TempDir()
				path := filepath.Join(directory, "download.bin")
				expectedMode := os.FileMode(0o600) &^ mask
				if existing {
					if err := os.WriteFile(path, []byte("previous"), 0o640); err != nil {
						t.Fatal(err)
					}
					if err := os.Chmod(path, 0o640); err != nil {
						t.Fatal(err)
					}
					expectedMode = 0o640
				}
				command := exec.Command("sh", "-c", `umask "$1"; shift; exec "$@"`, "download-umask", fmt.Sprintf("%03o", mask), executable, "-test.run=^TestManagedDownloadRestrictiveUmask$")
				command.Env = append(os.Environ(), childDestination+"="+path)
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("native umask child: %v\n%s", err, output)
				}
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != expectedMode {
					t.Fatalf("destination mode = %v, %v; want %03o", info, err, expectedMode)
				}
				if err := os.Chmod(path, 0o600); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "complete download" {
					t.Errorf("saved bytes = %q, %v", data, err)
				}
				requireNoDownloadStages(t, directory)
			})
		}
	}
}

func TestBinaryProcessStdoutPreservesObservedSourceFailure(t *testing.T) {
	const child = "OPENAI_TEST_BINARY_CLOSED_STDOUT"
	if os.Getenv(child) == "1" {
		sourceErr := errors.New("synthetic upstream failure returned with bytes")
		body := &failedDownloadBody{Reader: simultaneousDownloadReader{sourceErr}}
		_, err := WriteBinaryResponse(&http.Response{Body: body}, os.Stdout, "-")
		var failure *downloadSaveError
		if errors.Is(err, sourceErr) && errors.Is(err, syscall.EPIPE) &&
			errors.As(err, &failure) && body.closed == 1 {
			fmt.Fprintln(os.Stderr, "observed source and stdout errors; source closed")
			os.Exit(failure.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "source or stdout error was lost")
		os.Exit(97)
	}
	if runtime.GOOS == "windows" {
		t.Skip("requires native Unix SIGPIPE and EPIPE behavior")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	command := exec.Command(executable, "-test.run=^TestBinaryProcessStdoutPreservesObservedSourceFailure$")
	command.Env = append(os.Environ(), child+"=1")
	command.Stdout = writer
	var stderr bytes.Buffer
	command.Stderr = &stderr
	err = command.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("closed stdout process = %v; stderr=%q", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "observed source and stdout errors; source closed") {
		t.Errorf("child did not verify all failure causes: %q", stderr.String())
	}
}

func TestAutomaticDownloadFailureMessagesPreserveState(t *testing.T) {
	for _, kind := range []string{"before destination", "partial retained", "destination replaced"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "destination replaced" && runtime.GOOS == "windows" {
				t.Skip("Windows prevents renaming the open destination handle")
			}
			t.Chdir(t.TempDir())
			content := append([]byte{255}, bytes.Repeat([]byte("synthetic"), 1024)...)
			if kind == "before destination" {
				content = content[:7]
			}
			var source io.Reader = &errorAfterDownloadReader{Reader: bytes.NewReader(content), err: io.ErrUnexpectedEOF}
			if kind == "destination replaced" {
				source = &inspectingDownloadReader{Reader: source, inspect: func() error {
					if err := os.Rename("synthetic.bin", "original.bin"); err != nil {
						return err
					}
					return os.WriteFile("synthetic.bin", []byte("unrelated replacement"), 0o600)
				}}
			}
			response := &http.Response{Body: io.NopCloser(source), Header: http.Header{"Content-Disposition": {`attachment; filename="synthetic.bin"`}}}
			message, err := writeAutomaticBinaryResponse(response, io.Discard)
			var failure *downloadSaveError
			if message != "" || !errors.Is(err, io.ErrUnexpectedEOF) || !errors.As(err, &failure) {
				t.Fatalf("automatic result = %q, %v", message, err)
			}
			switch kind {
			case "before destination":
				if !strings.Contains(failure.message, "No automatic destination") {
					t.Errorf("wrong pre-destination message: %q", failure.message)
				}
				if _, err := os.Stat("synthetic.bin"); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("unexpected destination: %v", err)
				}
			case "partial retained":
				data, err := os.ReadFile("synthetic.bin")
				if err != nil || !bytes.Equal(data, content) || !strings.Contains(failure.message, "may contain partial output") {
					t.Errorf("partial state or message changed: %q, %v", failure.message, err)
				}
			case "destination replaced":
				data, err := os.ReadFile("synthetic.bin")
				if err != nil || string(data) != "unrelated replacement" || !strings.Contains(failure.message, "destination changed") {
					t.Errorf("replacement state or message changed: %q, %v", failure.message, err)
				}
			}
		})
	}
}

func TestAutomaticDownloadSourceCloseFailure(t *testing.T) {
	if !isTerminal(os.Stdout) {
		t.Skip("automatic destination source-close handling requires an actual terminal")
	}
	t.Chdir(t.TempDir())
	closeErr := errors.New("synthetic source close failure")
	content := append([]byte{255}, bytes.Repeat([]byte("synthetic"), 128)...)
	body := &failedDownloadBody{Reader: bytes.NewReader(content), closeErr: closeErr}
	response := &http.Response{Body: body, Header: http.Header{"Content-Disposition": {`attachment; filename="synthetic.bin"`}}}
	message, err := WriteBinaryResponse(response, io.Discard, "")
	var failure *downloadSaveError
	if message != "" || !errors.Is(err, closeErr) || !errors.As(err, &failure) || body.closed != 1 {
		t.Fatalf("late close failure = %q, %v, closes=%d", message, err, body.closed)
	}
	if !strings.Contains(failure.message, "automatically selected file may remain") {
		t.Errorf("late close message = %q", failure.message)
	}
	data, readErr := os.ReadFile("synthetic.bin")
	if readErr != nil || !bytes.Equal(data, content) {
		t.Errorf("late close removed or changed retained file: %v", readErr)
	}
}
