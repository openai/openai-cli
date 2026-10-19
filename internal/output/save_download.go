package output

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
)

// DownloadSaveError reports the destination's state without exposing local paths.
type DownloadSaveError struct {
	message string
	cause   error
}

// NewDownloadSaveError retains the underlying cause behind a value-safe message.
func NewDownloadSaveError(message string, cause error) *DownloadSaveError {
	return &DownloadSaveError{message: message, cause: cause}
}

func (e *DownloadSaveError) Error() string { return e.message }
func (e *DownloadSaveError) Unwrap() error { return e.cause }
func (e *DownloadSaveError) ExitCode() int {
	var interrupted *DownloadSignalError
	if errors.As(e.cause, &interrupted) {
		return interrupted.exitCode
	}
	if errors.Is(e.cause, context.Canceled) {
		return 130
	}
	return 1
}

// DownloadResponseBody lets cancellation and ordinary completion share ownership.
type DownloadResponseBody struct {
	io.ReadCloser
	once sync.Once
	err  error
}

func (b *DownloadResponseBody) Close() error {
	b.once.Do(func() { b.err = b.ReadCloser.Close() })
	return b.err
}

// DownloadContext retains the response request context, or uses a background context.
func DownloadContext(response *http.Response) context.Context {
	if response.Request != nil {
		return response.Request.Context()
	}
	return context.Background()
}

// CopyDownloadData retains source errors returned with bytes even if writing those
// bytes fails. io.Copy otherwise returns only the destination error in that case.
func CopyDownloadData(ctx context.Context, destination io.Writer, source io.Reader) error {
	buffer := make([]byte, 32*1024)
	for {
		if err := DownloadContextError(ctx); err != nil {
			return err
		}
		n, readErr := source.Read(buffer)
		if n > 0 {
			written, writeErr := destination.Write(buffer[:n])
			if written != n && writeErr == nil {
				writeErr = io.ErrShortWrite
			}
			if writeErr != nil {
				if readErr == io.EOF {
					readErr = nil
				}
				return errors.Join(readErr, writeErr, DownloadContextError(ctx))
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return DownloadContextError(ctx)
			}
			return errors.Join(readErr, DownloadContextError(ctx))
		}
	}
}

type downloadDestination struct {
	path     string
	resolved string
	original os.FileInfo
	file     *os.File
}

// Resolve the parent before cleaning its path: symlink/../file can differ from
// lexical path cleaning. The final entry must never select a symlink target.
func resolveDownloadDestination(path string) (string, error) {
	directory, name := filepath.Split(path)
	if directory == "" {
		directory = "."
	}
	parent, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", err
	}
	parent, err = filepath.Abs(parent)
	if err != nil {
		return "", err
	}
	resolved := filepath.Join(parent, name)
	info, err := os.Lstat(resolved)
	if errors.Is(err, os.ErrNotExist) {
		return resolved, nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", ErrDownloadSymlink
	}
	return resolved, nil
}

func prepareDownloadDestination(path string) (*downloadDestination, error) {
	resolved, err := resolveDownloadDestination(path)
	if err != nil {
		return nil, err
	}
	destination := &downloadDestination{path: path, resolved: resolved}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return destination, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrDownloadSymlink
	}
	destination.original = info
	if !info.Mode().IsRegular() {
		return destination, nil
	}
	// Nonblocking open prevents a concurrently substituted FIFO from hanging.
	// The opened descriptor must still match the inspected regular file.
	file, err := destination.open(true)
	if err != nil {
		return nil, err
	}
	destination.file = file
	return destination, nil
}

// ErrDownloadDestinationChanged signals that a destination or staging file changed ownership or state.
var ErrDownloadDestinationChanged = errors.New("download destination changed during transfer")

// ErrDownloadSymlink reports an unsupported final-entry symlink destination.
var ErrDownloadSymlink = errors.New("symlink download destinations are not supported")

// Open without creating or truncating anything, and verify the inspected entry
// before writing. The platform opener atomically refuses final-entry symlinks.
func (d *downloadDestination) open(nonblock bool) (*os.File, error) {
	file, err := OpenDownloadDestination(d.path, nonblock)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || opened.Mode().Type() != d.original.Mode().Type() || !os.SameFile(d.original, opened) {
		return nil, errors.Join(err, ErrDownloadDestinationChanged, file.Close())
	}
	return file, nil
}

func (d *downloadDestination) verify() error {
	resolved, err := resolveDownloadDestination(d.path)
	if err != nil || resolved != d.resolved {
		return errors.Join(ErrDownloadDestinationChanged, err)
	}
	current, err := os.Lstat(d.path)
	if d.original == nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return errors.Join(ErrDownloadDestinationChanged, err)
	}
	if err != nil || !os.SameFile(current, d.original) ||
		current.Size() != d.original.Size() || !current.ModTime().Equal(d.original.ModTime()) {
		return errors.Join(ErrDownloadDestinationChanged, err)
	}
	return nil
}

type downloadStage struct {
	root *os.Root
	name string
	file *os.File
	info os.FileInfo
}

func newDownloadStage(directory string) (*downloadStage, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	name := ".openai-download-" + rand.Text() + ".tmp"
	file, err := CreatePrivateDownloadFile(root, name)
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	info, err := file.Stat()
	if err == nil {
		// Keep the private stage readable even when umask removes owner-read.
		// info retains the creation mode required for a newly published file.
		err = file.Chmod(0o600)
	}
	if err != nil {
		return nil, errors.Join(err, file.Close(), root.Remove(name), root.Close())
	}
	return &downloadStage{root: root, name: name, file: file, info: info}, nil
}

func (s *downloadStage) closeWriter() error {
	if s.file == nil {
		return nil
	}
	file := s.file
	s.file = nil
	return file.Close()
}

func (s *downloadStage) verify() error {
	current, err := s.root.Lstat(s.name)
	if err != nil || !os.SameFile(current, s.info) || !current.Mode().IsRegular() {
		return errors.Join(ErrDownloadDestinationChanged, err)
	}
	return nil
}

func (s *downloadStage) cleanup() error {
	closeErr := s.closeWriter()
	checkErr := s.verify()
	var removeErr error
	if checkErr == nil {
		removeErr = s.root.Remove(s.name)
	}
	return errors.Join(closeErr, checkErr, removeErr, s.root.Close())
}

func (s *downloadStage) openCompleted() (*os.File, error) {
	file, err := s.root.OpenFile(s.name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !os.SameFile(info, s.info) || !info.Mode().IsRegular() {
		return nil, errors.Join(ErrDownloadDestinationChanged, err, file.Close())
	}
	return file, nil
}

// WriteManagedBinaryResponse stages the network transfer before changing ordinary
// destinations. Existing files keep their inode, links, permissions, ACLs and xattrs.
// A later local completion failure can leave those existing files partially written.
func WriteManagedBinaryResponse(response *http.Response, outfile string) (string, error) {
	return WriteManagedDownloadResponse(response, outfile, CopyDownloadData)
}

// WriteManagedDownloadResponse stages a transfer using endpoint-specific validation, including speech SSE.
func WriteManagedDownloadResponse(
	response *http.Response,
	outfile string,
	copyResponse func(context.Context, io.Writer, io.Reader) error,
) (message string, err error) {
	destination, err := prepareDownloadDestination(outfile)
	if err != nil {
		return "", &DownloadSaveError{"Download could not start. The destination was not changed.", err}
	}
	if destination.original != nil && !destination.original.Mode().IsRegular() {
		// Keep special-file streaming and signal behavior. In particular, opening
		// a FIFO can block before a descriptor exists for cancellation to close.
		file, openErr := destination.open(false)
		if openErr != nil {
			return "", openErr
		}
		copyErr := copyResponse(DownloadContext(response), file, response.Body)
		if closeErr := file.Close(); copyErr != nil || closeErr != nil {
			return "", &DownloadSaveError{"Download incomplete. The destination may contain partial output.", errors.Join(copyErr, closeErr)}
		}
		return fmt.Sprintf("Wrote output to: %s", outfile), nil
	}

	outcome := "Download incomplete. The download did not create or replace a destination file."
	if destination.original != nil {
		outcome = "Download incomplete. The existing destination was not changed."
	}
	defer func() {
		if destination.file != nil {
			err = errors.Join(err, destination.file.Close())
		}
		if err != nil {
			message = ""
			err = &DownloadSaveError{outcome, err}
		}
	}()

	ctx, stopSignals := DownloadSignalContext(DownloadContext(response))
	defer stopSignals()
	closed := make(chan struct{})
	stopClosing := context.AfterFunc(ctx, func() {
		defer close(closed)
		_ = response.Body.Close()
	})
	defer func() {
		if !stopClosing() {
			<-closed
		}
	}()

	stage, err := newDownloadStage(filepath.Dir(destination.resolved))
	if err != nil && destination.original != nil {
		stage, err = newDownloadStage(os.TempDir())
	}
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, stage.cleanup()) }()
	copyErr := copyResponse(ctx, stage.file, response.Body)
	if err = errors.Join(copyErr, response.Body.Close(), stage.closeWriter(), DownloadContextError(ctx)); err != nil {
		return "", err
	}
	if err = destination.verify(); err != nil {
		return "", err
	}
	if err = stage.verify(); err != nil {
		return "", err
	}
	if err = DownloadContextError(ctx); err != nil {
		return "", err
	}

	if destination.original == nil {
		parent, parentErr := os.Stat(filepath.Dir(destination.resolved))
		pinned, pinnedErr := stage.root.Stat(".")
		if parentErr != nil || pinnedErr != nil || !os.SameFile(parent, pinned) {
			return "", errors.Join(ErrDownloadDestinationChanged, parentErr, pinnedErr)
		}
		saved, incomplete, publishErr := publishNewDownload(ctx, stage, filepath.Base(destination.resolved), stage.root.Link)
		if publishErr != nil {
			outcome = "Download incomplete. No incomplete file from this download remains at the destination."
			if saved != nil {
				outcome = "Download finished, but final save verification or temporary-file cleanup failed. The saved file may remain."
			}
			if incomplete {
				outcome = "Download incomplete. A partial destination may remain; cleanup could not finish safely."
				if errors.Is(publishErr, ErrDownloadDestinationChanged) {
					outcome = "Download incomplete. The destination changed during completion; the replacement was preserved."
				}
			}
			return "", publishErr
		}
		outcome = "Download finished, but final save verification or temporary-file cleanup failed. The saved file may remain."
		current, statErr := os.Lstat(outfile)
		if statErr != nil || !os.SameFile(current, saved) {
			return "", errors.Join(ErrDownloadDestinationChanged, statErr)
		}
	} else {
		completed, openErr := stage.openCompleted()
		if openErr != nil {
			return "", openErr
		}
		outcome = "Download incomplete. The destination may contain partial output."
		if err = completeExistingDownload(ctx, destination.file, completed); err != nil {
			destination.file = nil
			return "", err
		}
		destination.file = nil
		current, statErr := os.Lstat(outfile)
		if statErr != nil || !os.SameFile(current, destination.original) {
			return "", errors.Join(ErrDownloadDestinationChanged, statErr)
		}
		outcome = "Download finished, but temporary-file cleanup failed. The saved destination contains the completed download."
	}
	return fmt.Sprintf("Wrote output to: %s", outfile), nil
}

// completeExistingDownload owns both descriptors after staging has succeeded.
func completeExistingDownload(ctx context.Context, destination interface {
	io.WriteCloser
	Truncate(int64) error
}, source io.ReadCloser) error {
	truncateErr := DownloadContextError(ctx)
	if truncateErr == nil {
		truncateErr = destination.Truncate(0)
	}
	var copyErr error
	if truncateErr == nil {
		copyErr = CopyDownloadData(ctx, destination, source)
	}
	return errors.Join(truncateErr, copyErr, source.Close(), destination.Close())
}

// publishNewDownload prefers atomic, exclusive publication. Filesystems without
// hardlinks use an exclusive destination and copy only the completed staging file.
// That fallback can expose partial bytes during the local completion copy.
func publishNewDownload(
	ctx context.Context,
	stage *downloadStage,
	name string,
	link func(string, string) error,
) (saved os.FileInfo, incomplete bool, err error) {
	if err := DownloadContextError(ctx); err != nil {
		return nil, false, err
	}
	source, err := stage.openCompleted()
	if err != nil {
		return nil, false, err
	}
	// Restore the umask-derived mode before publication. The pinned reader
	// remains readable for fallback copying even if that mode is 000.
	if err := source.Chmod(stage.info.Mode().Perm()); err != nil {
		return nil, false, errors.Join(err, source.Close())
	}
	if err := link(stage.name, name); err == nil {
		return stage.info, false, source.Close()
	}
	if err := DownloadContextError(ctx); err != nil {
		return nil, false, errors.Join(err, source.Close())
	}
	file, err := CreatePrivateDownloadFile(stage.root, name)
	if err != nil {
		return nil, false, errors.Join(err, source.Close())
	}
	owned, err := file.Stat()
	if err != nil {
		return nil, true, errors.Join(err, source.Close(), file.Close())
	}
	defer func() {
		if err != nil {
			cleanupErr := removeOwnedDownloadFile(stage.root, name, owned)
			incomplete = cleanupErr != nil
			err = errors.Join(err, cleanupErr)
		}
	}()
	if err = errors.Join(CopyDownloadData(ctx, file, source), source.Close(), file.Close()); err != nil {
		return nil, true, err
	}
	current, statErr := stage.root.Lstat(name)
	if statErr != nil || !os.SameFile(current, owned) {
		return nil, true, errors.Join(ErrDownloadDestinationChanged, statErr)
	}
	return owned, false, nil
}

func removeOwnedDownloadFile(root *os.Root, name string, owned os.FileInfo) error {
	current, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !os.SameFile(current, owned) {
		return errors.Join(ErrDownloadDestinationChanged, err)
	}
	return root.Remove(name)
}

// DownloadSignalError identifies cancellation caused by a process signal.
type DownloadSignalError struct{ exitCode int }

func (e *DownloadSignalError) Error() string { return "download canceled by a process signal" }
func (e *DownloadSignalError) Unwrap() error { return context.Canceled }

// SignalExitCode returns the status for the process signal that canceled the operation.
func (e *DownloadSignalError) SignalExitCode() int { return e.exitCode }

// DownloadContextError retains both context cancellation and its underlying cause.
func DownloadContextError(ctx context.Context) error {
	return errors.Join(ctx.Err(), context.Cause(ctx))
}

// DownloadSignalContext cancels on process signals. The caller unregisters and reaps
// this watcher before returning; ordinary cleanup never changes a successful result.
func DownloadSignalContext(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case received := <-signals:
			// Restore ordinary signal handling while cancellation cleans up.
			signal.Stop(signals)
			code := 130
			if received == syscall.SIGTERM {
				code = 143
			}
			cancel(&DownloadSignalError{exitCode: code})
		case <-ctx.Done():
		case <-stop:
		}
	}()
	return ctx, func() {
		signal.Stop(signals)
		close(stop)
		<-done
		cancel(nil)
	}
}

// CopyBinaryOutput uses process SIGPIPE protection only for actual stdout. Keep the
// copy errors unmarked so StreamToStdout cannot suppress an observed failure.
func CopyBinaryOutput(ctx context.Context, destination io.Writer, source io.Reader) error {
	var err error
	if file, ok := destination.(*os.File); ok && file == os.Stdout {
		err = StreamToStdout(func(stdout *os.File) error {
			return CopyDownloadData(ctx, stdout, source)
		})
	} else {
		err = CopyDownloadData(ctx, destination, source)
	}
	if err != nil {
		return &DownloadSaveError{"Download incomplete. Output may contain partial bytes.", err}
	}
	return nil
}
