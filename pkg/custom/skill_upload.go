package custom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/openai/openai-cli/internal/skillarchive"
	"github.com/urfave/cli/v3"
)

const skillUploadMetadata = "openai-skill-upload"

// A separate preparation belongs to each invocation, including cloned routes.
type skillUploadPreparation struct {
	ctx          context.Context
	parent       context.Context
	stopSignals  func() error
	owned        []io.Closer
	body         io.Closer
	finishOnce   sync.Once
	cleanupErr   error
	cancellation error
}

type skillUploadContextKey struct{}

// Finish owned upload work before result output can block. Cleanup failures stay
// available to the action defer, so an already-created skill ID remains visible.
func (s *skillUploadPreparation) finish() {
	s.finishOnce.Do(func() {
		// A filesystem close can block. Restore first-signal process termination
		// before cleanup, after retaining any signal already delivered.
		s.cancellation = s.stopSignals()
		if s.body != nil {
			s.cleanupErr = errors.Join(s.cleanupErr, s.body.Close())
		}
		for _, owned := range s.owned {
			s.cleanupErr = errors.Join(s.cleanupErr, owned.Close())
		}
	})
}

func (s *skillUploadPreparation) result(err error) error {
	s.finish()
	if s.cancellation != nil && (err == nil || errors.Is(err, context.Canceled)) {
		err = errors.Join(err, s.cancellation)
	}
	return errors.Join(err, s.cleanupErr)
}

func finishSkillUploadBeforeOutput(opts *ShowJSONOpts) error {
	if opts.Operation != "(resource) skills > (method) create" &&
		opts.Operation != "(resource) skills.versions > (method) create" {
		return nil
	}
	state, _ := opts.Context.Value(skillUploadContextKey{}).(*skillUploadPreparation)
	if state == nil {
		return nil
	}
	state.finish()
	opts.Context = state.parent
	return state.cancellation
}

func configureSkillUploads(root *cli.Command) {
	for _, resource := range []string{"skills", "skills:versions"} {
		group := root.Command(resource)
		if group == nil || group.Command("create") == nil {
			continue
		}
		command := group.Command("create")
		command.Description = "Upload one directory or ZIP with --files PATH. Directories include all regular files, including hidden files. " +
			"Directory uploads preserve relative paths and executable permissions in one top-level folder. " +
			"Symlinks and special files inside directories are rejected. ZIP files are sent unchanged; the API validates skill contents."
		next := command.Action
		command.Action = func(ctx context.Context, command *cli.Command) (err error) {
			parent := ctx
			ctx, stopSignals := skillUploadSignalContext(ctx)
			state := &skillUploadPreparation{ctx: ctx, parent: parent, stopSignals: stopSignals}
			ctx = context.WithValue(ctx, skillUploadContextKey{}, state)
			if command.Metadata == nil {
				command.Metadata = make(map[string]any)
			}
			command.Metadata[skillUploadMetadata] = state
			defer func() {
				err = state.result(err)
				delete(command.Metadata, skillUploadMetadata)
			}()
			return next(ctx, command)
		}
	}
}

type skillUploadInterrupt struct{ code int }

func (e *skillUploadInterrupt) Error() string { return "skill upload canceled by a process signal" }
func (e *skillUploadInterrupt) Unwrap() error { return context.Canceled }
func (e *skillUploadInterrupt) ExitCode() int { return e.code }

// Restore normal signal handling before cleanup so a second signal can stop a
// blocked filesystem. Join the watcher on every exit, including setup failures.
func skillUploadSignalContext(parent context.Context) (context.Context, func() error) {
	ctx, cancel := context.WithCancelCause(parent)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		select {
		case received := <-signals:
			signal.Stop(signals)
			code := 130
			if received == syscall.SIGTERM {
				code = 143
			}
			cancel(&skillUploadInterrupt{code: code})
		case <-ctx.Done():
		case <-stop:
		}
	}()
	return ctx, func() error {
		return finishSkillUploadSignalWatcher(ctx, cancel, signals, stop, done)
	}
}

// signal.Stop prevents later deliveries. Join before draining so the watcher and
// shutdown cannot race to consume a previously delivered signal.
func finishSkillUploadSignalWatcher(ctx context.Context, cancel context.CancelCauseFunc, signals chan os.Signal, stop, done chan struct{}) error {
	signal.Stop(signals)
	close(stop)
	<-done
	select {
	case received := <-signals:
		code := 130
		if received == syscall.SIGTERM {
			code = 143
		}
		cancel(&skillUploadInterrupt{code: code})
	default:
	}
	cause := context.Cause(ctx)
	if cause != nil {
		cause = errors.Join(ctx.Err(), cause)
	}
	cancel(nil)
	return cause
}

func skillUploadState(cmd *cli.Command) *skillUploadPreparation {
	state, _ := cmd.Metadata[skillUploadMetadata].(*skillUploadPreparation)
	return state
}

// Run after input merging and stdin security, before generic file embedding.
// Only trusted FileInput paths are expanded here. Other values retain their semantics.
func prepareSkillUploadInputs(cmd *cli.Command, body any, stdin *onceStdinReader) error {
	state := skillUploadState(cmd)
	if state == nil {
		return nil
	}
	fields, ok := body.(map[string]any)
	if !ok {
		return nil
	}
	value, supplied := fields["files"]
	if !supplied {
		return nil
	}
	values, array := value.([]any)
	if !array {
		values = []any{value}
	}
	commonDirectory := skillInputCommonDirectory(values)
	singular := !array
	for index, value := range values {
		pathValue, ok := value.(FilePathValue)
		if !ok || pathValue == "" {
			continue
		}
		path := string(pathValue)
		if isStdinPath(path) {
			if len(values) == 1 {
				reader, err := stdin.read()
				if err != nil {
					return err
				}
				values[index] = fileUpload{Reader: io.NopCloser(reader), filename: "skill.zip", contentType: "application/zip"}
				singular = true
			}
			continue
		}
		if err := state.ctx.Err(); err != nil {
			return err
		}
		info, err := os.Stat(path)
		if err != nil {
			return skillInputFailure(index, path, err)
		}
		if info.IsDir() {
			if len(values) != 1 {
				return &skillUploadError{message: "Skill upload: use one directory with --files. Upload separate skills in separate commands."}
			}
			archive, err := skillarchive.Prepare(state.ctx, path)
			if err != nil {
				return skillInputFailure(index, path, err)
			}
			state.owned = append(state.owned, archive)
			values[index] = fileUpload{Reader: archive, filename: archive.Filename(), contentType: "application/zip", size: archive.Size(), knownSize: true}
			singular = true
			continue
		}
		upload, err := openFileUpload(path)
		if err != nil {
			return skillInputFailure(index, path, err)
		}
		upload.Reader = &skillUploadReader{ReadCloser: readCloserForSkill(upload.Reader)}
		state.owned = append(state.owned, upload)
		if len(values) == 1 && strings.EqualFold(filepath.Ext(path), ".zip") {
			singular = true
		} else if !singular {
			// The API's individual-file form requires the path within one root.
			name := filepath.Clean(path)
			if !filepath.IsLocal(name) {
				absolute, err := filepath.Abs(name)
				if err != nil {
					return skillInputFailure(index, path, err)
				}
				relative, err := filepath.Rel(commonDirectory, absolute)
				if err != nil {
					return skillInputFailure(index, path, err)
				}
				rootName := filepath.Base(commonDirectory)
				if filepath.Dir(commonDirectory) == commonDirectory {
					rootName = "skill"
				}
				name = filepath.Join(rootName, relative)
			}
			upload.filename = filepath.ToSlash(name)
		}
		values[index] = upload
	}
	if singular && len(values) == 1 {
		fields["files"] = values[0]
	} else {
		fields["files"] = values
	}
	return nil
}

// Explicit paths outside the working directory share one relative root.
// Ordinary relative paths keep the directory layout the user supplied.
func skillInputCommonDirectory(values []any) string {
	common := ""
	for _, value := range values {
		path, ok := value.(FilePathValue)
		if !ok || path == "" || isStdinPath(string(path)) {
			continue
		}
		absolute, err := filepath.Abs(string(path))
		if err != nil {
			continue // The actual open reports the input error.
		}
		directory := filepath.Dir(absolute)
		if common == "" {
			common = directory
			continue
		}
		for common != filepath.Dir(common) {
			relative, err := filepath.Rel(common, directory)
			if err == nil && (relative == "." || filepath.IsLocal(relative)) {
				break
			}
			common = filepath.Dir(common)
		}
	}
	return common
}

type skillUploadError struct {
	message string
	cause   error
}

func (e *skillUploadError) Error() string { return e.message }
func (e *skillUploadError) Unwrap() error { return e.cause }

// The multipart encoder and action guard can both release the same input.
type skillUploadReader struct {
	io.ReadCloser
	once sync.Once
	err  error
}

func (r *skillUploadReader) Close() error {
	r.once.Do(func() { r.err = r.ReadCloser.Close() })
	return r.err
}

func readCloserForSkill(reader io.Reader) io.ReadCloser {
	if closer, ok := reader.(io.ReadCloser); ok {
		return closer
	}
	return io.NopCloser(reader)
}

func skillInputFailure(index int, path string, cause error) error {
	message := fmt.Sprintf("Skill upload: --files input %d (%q) could not be read. ", index+1, filepath.Base(filepath.Clean(path)))
	var archiveError *skillarchive.Error
	if errors.As(cause, &archiveError) {
		message += archiveError.Error() + ". Check this member before uploading the directory again."
		return &skillUploadError{message: message, cause: cause}
	}
	switch {
	case errors.Is(cause, os.ErrNotExist):
		message += "The input does not exist. Correct --files to an existing file or directory."
	case errors.Is(cause, os.ErrPermission):
		message += "Permission was denied. Check the input's read permissions."
	default:
		message += "Check the input and its readable regular files. Directory uploads cannot contain symlinks or special files."
	}
	return &skillUploadError{message: message, cause: cause}
}
