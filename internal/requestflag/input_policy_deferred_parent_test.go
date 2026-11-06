package requestflag_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestInputPolicyDeferredParentRejectsNestedBeforeSourceReads(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "synthetic-missing.json")
	for _, parent := range []string{"@" + missing, "@-"} {
		t.Run(map[bool]string{true: "stdin", false: "file"}[parent == "@-"], func(t *testing.T) {
			outer := &requestflag.Flag[any]{Name: "entry", BodyPath: "entry", InputPolicy: requestflag.InputPolicyTextStructured}
			inner := &requestflag.InnerFlag[string]{Name: "entry.content", InnerField: "content", OuterFlag: outer, InputPolicy: requestflag.InputPolicyText}
			stdin := strings.NewReader(`{"type":"synthetic"}`)
			initialLength := stdin.Len()
			actionCalled := false
			command := &cli.Command{
				Name: "test", Flags: []cli.Flag{outer, inner},
				Reader: stdin, Writer: io.Discard, ErrWriter: io.Discard,
				Action: func(context.Context, *cli.Command) error { actionCalled = true; return nil },
			}
			requestflag.AddStructuredFlags(command)
			err := command.Run(context.Background(), []string{"test", "--entry-structured", parent, "--entry.content", "synthetic"})
			require.ErrorContains(t, err, "cannot combine nested flags with a structured parent source")
			require.False(t, errors.Is(err, os.ErrNotExist))
			require.NotContains(t, err.Error(), missing)
			require.Equal(t, initialLength, stdin.Len())
			require.False(t, actionCalled)
			err = inner.Set("entry.content", "@"+missing)
			var structuredError *requestflag.StructuredInputError
			require.ErrorAs(t, err, &structuredError)
			require.NotContains(t, err.Error(), missing)
		})
	}
}
