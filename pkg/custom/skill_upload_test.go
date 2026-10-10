package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

type skillCloseFailure struct{ count int }

func (s *skillCloseFailure) Read([]byte) (int, error) { return 0, io.EOF }
func (s *skillCloseFailure) Close() error             { s.count++; return errors.New("synthetic close failure") }

type skillBlockingCloser struct{}

func (*skillBlockingCloser) Close() error {
	if _, err := io.WriteString(os.Stdout, "ready\n"); err != nil {
		return err
	}
	_, err := io.Copy(io.Discard, os.Stdin)
	return err
}

func TestSkillUploadBlockedCleanupFirstSignal(t *testing.T) {
	if os.Getenv("OPENAI_CLI_SKILL_BLOCKED_CLOSE_HELPER") == "1" {
		command := &cli.Command{Name: "create", Action: func(_ context.Context, cmd *cli.Command) error {
			state := skillUploadState(cmd)
			state.owned = append(state.owned, &skillBlockingCloser{})
			return nil
		}}
		root := &cli.Command{Name: "openai", Commands: []*cli.Command{{Name: "skills", Commands: []*cli.Command{command}}}}
		configureSkillUploads(root)
		require.NoError(t, command.Action(t.Context(), command))
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("os.Process.Signal does not send SIGINT or SIGTERM on Windows")
	}
	for _, tc := range []struct {
		signal os.Signal
		status int
	}{{os.Interrupt, 130}, {syscall.SIGTERM, 143}} {
		t.Run(tc.signal.String(), func(t *testing.T) {
			binary, err := os.Executable()
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, binary, "-test.run=^TestSkillUploadBlockedCleanupFirstSignal$")
			child.Env = append(os.Environ(), "OPENAI_CLI_SKILL_BLOCKED_CLOSE_HELPER=1")
			stdin, err := child.StdinPipe()
			require.NoError(t, err)
			defer stdin.Close()
			stdout, err := child.StdoutPipe()
			require.NoError(t, err)
			var stderr bytes.Buffer
			child.Stderr = &stderr
			require.NoError(t, child.Start())
			done := make(chan error, 1)
			go func() { done <- child.Wait() }()
			waited := false
			defer func() {
				cancel()
				if !waited {
					<-done
				}
			}()
			var ready [6]byte
			_, err = io.ReadFull(stdout, ready[:])
			require.NoError(t, err)
			require.Equal(t, "ready\n", string(ready[:]))
			require.NoError(t, child.Process.Signal(tc.signal))
			select {
			case err := <-done:
				waited = true
				var exit *exec.ExitError
				require.ErrorAs(t, err, &exit)
				status, ok := exit.Sys().(syscall.WaitStatus)
				require.True(t, ok)
				require.True(t, status.Signaled(), "stderr=%s", stderr.String())
				require.Equal(t, tc.status, 128+int(status.Signal()))
			case <-time.After(2 * time.Second):
				t.Fatal("first signal did not stop blocked upload cleanup")
			}
		})
	}
}

func TestSkillUploadActionPreservesCleanupFailures(t *testing.T) {
	source := &skillCloseFailure{}
	reader := &skillUploadReader{ReadCloser: source}
	operationErr := errors.New("synthetic operation failure")
	command := &cli.Command{Name: "create", Action: func(ctx context.Context, cmd *cli.Command) error {
		state := skillUploadState(cmd)
		require.NotNil(t, state)
		state.owned = append(state.owned, reader)
		state.body = reader
		return operationErr
	}}
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{{Name: "skills", Commands: []*cli.Command{command}}}}
	configureSkillUploads(root)
	err := command.Action(t.Context(), command)
	require.ErrorIs(t, err, operationErr)
	require.NotErrorIs(t, err, context.Canceled, "intentional signal cleanup must preserve the original failure")
	require.Contains(t, err.Error(), "synthetic close failure")
	require.Equal(t, 1, source.count)
	require.Nil(t, skillUploadState(command))
}

func TestSkillUploadCleanupFailurePreservesReturnedID(t *testing.T) {
	for _, operation := range []string{"(resource) skills > (method) create", "(resource) skills.versions > (method) create"} {
		t.Run(operation, func(t *testing.T) {
			var output bytes.Buffer
			var sources []*skillCloseFailure
			command := &cli.Command{Name: "create", Action: func(ctx context.Context, cmd *cli.Command) error {
				source := &skillCloseFailure{}
				sources = append(sources, source)
				skillUploadState(cmd).owned = append(skillUploadState(cmd).owned, source)
				err := ShowJSON(gjson.Parse(`{"id":"skill_returned"}`), ShowJSONOpts{
					Context: ctx, Operation: operation, Format: "json", Stdout: &output, Stderr: io.Discard,
				})
				require.NoError(t, err, "cleanup failure must not hide the successful response")
				require.Equal(t, 1, source.count, "cleanup must finish before output")
				return err
			}}
			root := &cli.Command{Name: "openai", Commands: []*cli.Command{{Name: "skills", Commands: []*cli.Command{command}}}}
			configureSkillUploads(root)
			for range 2 {
				output.Reset()
				err := command.Action(t.Context(), command)
				require.ErrorContains(t, err, "synthetic close failure")
				require.NotErrorIs(t, err, context.Canceled)
				require.JSONEq(t, `{"id":"skill_returned"}`, output.String())
				require.Nil(t, skillUploadState(command))
			}
			for _, source := range sources {
				require.Equal(t, 1, source.count)
			}
		})
	}
}

func TestSkillUploadSignalShutdownRetainsPendingDelivery(t *testing.T) {
	for _, pending := range []os.Signal{nil, os.Interrupt, syscall.SIGTERM} {
		ctx, cancel := context.WithCancelCause(t.Context())
		signals, stop, done := make(chan os.Signal, 1), make(chan struct{}), make(chan struct{})
		if pending != nil {
			signals <- pending
		}
		// Model the watcher selecting stop while a delivered signal stays queued.
		close(done)
		cause := finishSkillUploadSignalWatcher(ctx, cancel, signals, stop, done)
		if pending == nil {
			require.NoError(t, cause, "intentional cancellation is not a process interrupt")
		} else {
			var interrupted *skillUploadInterrupt
			require.ErrorAs(t, cause, &interrupted)
			want := 130
			if pending == syscall.SIGTERM {
				want = 143
			}
			require.Equal(t, want, interrupted.ExitCode())
		}
		require.Empty(t, signals)
	}
}

func TestSkillUploadOutputRestoresParentContext(t *testing.T) {
	parent, cancelParent := context.WithCancel(t.Context())
	ctx, stop := skillUploadSignalContext(parent)
	state := &skillUploadPreparation{ctx: ctx, parent: parent, stopSignals: stop}
	ctx = context.WithValue(ctx, skillUploadContextKey{}, state)
	defer state.finish()
	wrong := ShowJSONOpts{Context: ctx, Operation: "(resource) models > (method) list"}
	wrong.setDefaults()
	assertions := func(opts *ShowJSONOpts) { require.NoError(t, finishSkillUploadBeforeOutput(opts)) }
	assertions(&wrong)
	assertions(&ShowJSONOpts{Context: parent, Operation: "(resource) skills > (method) create"})
	correct := ShowJSONOpts{Context: ctx, Operation: "(resource) skills > (method) create"}
	assertions(&correct)
	assertions(&correct)
	assertions(&wrong)
	require.Same(t, parent, correct.Context)
	require.NoError(t, parent.Err())
	cancelParent()
	require.ErrorIs(t, correct.Context.Err(), context.Canceled)
}

func TestSkillUploadParentCancellationRetainsCause(t *testing.T) {
	parent, cancelParent := context.WithCancelCause(t.Context())
	defer cancelParent(nil)
	ctx, stop := skillUploadSignalContext(parent)
	state := &skillUploadPreparation{ctx: ctx, parent: parent, stopSignals: stop}
	ctx = context.WithValue(ctx, skillUploadContextKey{}, state)
	defer state.finish()
	cause := errors.New("synthetic parent cancellation")
	cancelParent(cause)
	opts := ShowJSONOpts{Context: ctx, Operation: "(resource) skills > (method) create"}
	err := finishSkillUploadBeforeOutput(&opts)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, cause)
	require.Same(t, parent, opts.Context)
}

func TestSkillUploadDiagnosticPreservesCauseAndHidesParent(t *testing.T) {
	for _, cause := range []error{os.ErrNotExist, os.ErrPermission, context.Canceled, context.DeadlineExceeded} {
		failure := skillInputFailure(1, filepath.Join("private-parent", "bad\x1b-name.zip"), cause)
		require.ErrorIs(t, failure, cause)
		message := localErrorMessage(&cli.Command{Name: "openai"}, failure)
		require.NotContains(t, message, "private-parent")
		require.NotContains(t, message, "\x1b")
		if errors.Is(cause, context.Canceled) {
			require.Equal(t, "Request canceled.", message)
		} else if !errors.Is(cause, context.DeadlineExceeded) {
			require.Contains(t, message, "--files input 2")
		}
	}
}
