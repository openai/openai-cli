package custom

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

type skillCloseFailure struct{ count int }

func (s *skillCloseFailure) Read([]byte) (int, error) { return 0, io.EOF }
func (s *skillCloseFailure) Close() error             { s.count++; return errors.New("synthetic close failure") }

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
