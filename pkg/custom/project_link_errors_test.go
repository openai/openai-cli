package custom

import (
	"errors"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestProjectLinkStoreFailurePreservesCauses(t *testing.T) {
	root := &cli.Command{Metadata: map[string]any{"help-invocation": "'/synthetic/CLI with spaces/openai'"}}
	for _, test := range []struct {
		name, guidance string
		cause          error
	}{
		{"size", "1 MiB", errProjectLinksTooLarge},
		{"invalid", "invalid JSON or entries", errProjectLinksInvalid},
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
