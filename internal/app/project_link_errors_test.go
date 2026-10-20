package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestProjectLinkCancellationGuidanceRetainsErrorBoundary(t *testing.T) {
	committed := projectLinkCommittedSelectionFailure("link", context.Canceled)
	plain := &localcommands.LocalError{Message: "A different operation failed.", Cause: context.Canceled}
	for _, test := range []struct {
		name    string
		failure error
		want    string
	}{
		{"direct", committed, "The folder link was saved."},
		{"wrapped", fmt.Errorf("synthetic-private: %w", committed), "The folder link was saved."},
		{"plain_outer", &localcommands.LocalError{Message: "An outer operation failed.", Cause: committed}, "Request canceled."},
		{"plain_first_join", errors.Join(plain, committed), "Request canceled."},
		{"guidance_first_join", errors.Join(committed, plain), "The folder link was saved."},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.ErrorIs(t, test.failure, context.Canceled)
			var shared *localcommands.LocalError
			require.ErrorAs(t, test.failure, &shared)
			var diagnostic bytes.Buffer
			require.NoError(t, ShowCommandError(&cli.Command{Name: "openai"}, test.failure, &diagnostic))
			require.Contains(t, diagnostic.String(), test.want)
			require.NotContains(t, diagnostic.String(), "synthetic-private")
		})
	}
}

func TestProjectLinkStoreFailurePreservesCauses(t *testing.T) {
	root := &cli.Command{Metadata: map[string]any{"help-invocation": "'/synthetic/CLI with spaces/openai'"}}
	for _, test := range []struct {
		name, guidance string
		cause          error
	}{
		{"size", "1 MiB", errProjectLinksTooLarge},
		{"invalid", "invalid JSON or entries", errProjectLinksInvalid},
		{"alias conflict", "different projects", errProjectLinkAliasConflict},
		{"alias access", "Check access to the linked folders", errors.Join(errProjectLinkAliasAccess,
			&os.PathError{Op: "lstat", Path: "/synthetic-private/alias", Err: os.ErrPermission})},
		{"directory", "0700", errProjectLinkDirectoryUnsafe},
		{"file", "0600", errProjectLinkFileUnsafe},
		{"lock", ".project-links.json.lock", errProjectLinkLockUnsafe},
		{"permission", "cannot access", &os.PathError{Op: "open", Path: "/synthetic-private/folder", Err: os.ErrPermission}},
		{"snapshot", "Stop other registry writers", errProjectLinksChanged},
		{"lock changed", "Stop other registry writers", errProjectLinkLockChanged},
		{"directory changed", "Stop other registry writers", errProjectLinkDirectoryChanged},
	} {
		t.Run(test.name, func(t *testing.T) {
			cause := errors.Join(test.cause, errors.New("synthetic-private-cause"))
			failure := projectLinkStoreFailure(root, "resolve", cause)
			require.ErrorIs(t, failure, test.cause)
			require.Contains(t, failure.Error(), test.guidance)
			require.Contains(t, failure.Error(), "Inspect with '/synthetic/CLI with spaces/openai' link.")
			require.NotContains(t, failure.Error(), "synthetic-private")
		})
	}
}

func TestProjectLinkCommittedSelectionFailurePreservesOutcomeAndCause(t *testing.T) {
	private := &os.PathError{Op: "lstat", Path: "/synthetic-private/alias", Err: os.ErrPermission}
	for _, test := range []struct {
		action, outcome string
	}{
		{"link", "The folder link was saved."},
		{"unlink", "This folder has no saved link of its own."},
	} {
		t.Run(test.action, func(t *testing.T) {
			cause := errors.Join(errProjectLinkAliasAccess, private)
			failure := projectLinkCommittedSelectionFailure(test.action, cause)
			require.ErrorIs(t, failure, private)
			require.ErrorIs(t, failure, errProjectLinkAliasAccess)
			message := localErrorMessage(nil, failure)
			require.Contains(t, message, test.outcome)
			require.NotContains(t, message, "did not change")
			require.NotContains(t, message, "synthetic-private")
		})
	}
}

func TestProjectLinkOutputFailureReportsActualOutcome(t *testing.T) {
	for _, test := range []struct {
		name, outcome, wantProject string
		args                       []string
	}{
		{"inspect", "This command did not change saved links.", "proj_saved", []string{"link"}},
		{"save", "The folder link was saved", "proj_updated", []string{"link", "--project=proj_updated"}},
		{"remove", "no saved link of its own", "", []string{"unlink"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := projectLinkTestEnvironment(t)
			require.NoError(t, projectLinkTestCommand(io.Discard).Run(t.Context(), []string{"openai", "link", "--project=proj_saved"}))
			fault := cli.Exit("synthetic-private-sink-failure", 17)
			root := projectLinkTestCommand(projectLinkBrokenWriter{fault})
			root.Metadata = map[string]any{"help-invocation": "'/synthetic/CLI with spaces/openai'"}
			failure := root.Run(t.Context(), append([]string{"openai"}, test.args...))
			require.ErrorIs(t, failure, fault)
			var exit cli.ExitCoder
			require.ErrorAs(t, failure, &exit)
			require.Equal(t, 17, exit.ExitCode())
			require.Contains(t, failure.Error(), test.outcome)
			require.Contains(t, failure.Error(), "Inspect with '/synthetic/CLI with spaces/openai' link.")
			require.NotContains(t, failure.Error(), "synthetic-private")
			links, err := loadProjectLinks(t.Context(), path)
			require.NoError(t, err)
			directory, err := projectLinkDirectory()
			require.NoError(t, err)
			require.Equal(t, test.wantProject, links[directory])
		})
	}
}

func TestProjectLinkCommittedCancellationPreservesOutcome(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		for _, test := range []struct {
			name, outcome, wantProject string
			args                       []string
		}{
			{"inspect", "Request canceled.", "proj_saved", []string{"link"}},
			{"save", "The folder link was saved", "proj_updated", []string{"link", "--project=proj_updated"}},
			{"remove", "no saved link of its own", "", []string{"unlink"}},
		} {
			t.Run(format+"/"+test.name, func(t *testing.T) {
				path := projectLinkTestEnvironment(t)
				require.NoError(t, projectLinkTestCommand(io.Discard).Run(t.Context(), []string{"openai", "link", "--project=proj_saved"}))
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				root := projectLinkTestCommand(profileCancelOutputWriter{ctx, cancel})
				failure := root.Run(ctx, append([]string{"openai", "--format=" + format}, test.args...))
				require.ErrorIs(t, failure, context.Canceled)
				var diagnostic bytes.Buffer
				require.NoError(t, ShowCommandError(root, failure, &diagnostic))
				require.Contains(t, diagnostic.String(), test.outcome)
				if format == "json" {
					require.True(t, json.Valid(diagnostic.Bytes()), diagnostic.String())
				}
				links, err := loadProjectLinks(t.Context(), path)
				require.NoError(t, err)
				directory, err := projectLinkDirectory()
				require.NoError(t, err)
				require.Equal(t, test.wantProject, links[directory])
			})
		}
	}
	for _, action := range []string{"link", "unlink"} {
		cause := errors.Join(context.Canceled, &os.PathError{Op: "open", Path: "/synthetic-private/state", Err: os.ErrPermission})
		failure := projectLinkCommittedSelectionFailure(action, cause)
		require.ErrorIs(t, failure, context.Canceled)
		message := localErrorMessage(nil, failure)
		require.Contains(t, message, "The command was canceled.")
		require.NotContains(t, message, "synthetic-private")
		require.NotContains(t, message, "project-links.json")
	}
}
