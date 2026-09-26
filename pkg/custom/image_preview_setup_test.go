package custom

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func previewSetupCommand(out io.Writer, run func(context.Context, io.Writer) error) *cli.Command {
	return &cli.Command{Name: "openai", Writer: out, ErrWriter: io.Discard,
		Flags:    []cli.Flag{&cli.StringFlag{Name: "format", Value: "auto"}, &cli.StringFlag{Name: "transform"}, &cli.BoolFlag{Name: "raw-output"}},
		Commands: []*cli.Command{{Name: "images", Commands: []*cli.Command{{Name: "inline", Commands: []*cli.Command{{Name: "setup", Action: imagePreviewSetupAction(run)}}}}}},
	}
}

func TestPreviewSetupRegistrationComposesInlineCommands(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			images := &cli.Command{Name: "images", Commands: []*cli.Command{{Name: "generate"}}}
			if existing {
				images.Commands = append(images.Commands, &cli.Command{Name: "inline", Commands: []*cli.Command{{Name: "on"}, {Name: "off"}}})
			}
			root := &cli.Command{Name: "openai", Commands: []*cli.Command{images}}
			registerImagePreviewSetup(root)
			count := 0
			for _, command := range images.Commands {
				if command.Name == "inline" {
					count++
				}
			}
			require.Equal(t, 1, count)
			inline := images.Command("inline")
			for _, name := range []string{"setup", "repair", "status"} {
				require.NotNil(t, inline.Command(name))
			}
			require.Nil(t, inline.Command("reset"))
			require.Nil(t, inline.Command("test"))
			if existing {
				require.NotNil(t, inline.Command("on"))
				require.NotNil(t, inline.Command("off"))
				require.Len(t, inline.Commands, 5)
			} else {
				require.Len(t, inline.Commands, 3)
			}
			require.NotNil(t, images.Command("generate"))
		})
	}
	root := &cli.Command{Name: "openai"}
	require.NotPanics(t, func() { registerImagePreviewSetup(root) })
}

func TestPreviewSetupConfigureCommandRunsOnce(t *testing.T) {
	root := &cli.Command{Name: "openai", Commands: []*cli.Command{{Name: "images"}}}
	ConfigureCommand(root)
	ConfigureCommand(root)
	inline := root.Command("images").Command("inline")
	require.NotNil(t, inline)
	for _, name := range []string{"setup", "repair", "status"} {
		count := 0
		for _, command := range inline.Commands {
			if command.Name == name {
				count++
			}
		}
		require.Equal(t, 1, count, name)
	}
}

func TestPreviewSetupRejectsInvalidOutputBeforeNativeCalls(t *testing.T) {
	cases := [][]string{
		{"images", "inline", "setup", "untrusted\x1b]52;secret\a"},
		{"--format", "json", "images", "inline", "setup"},
		{"--format", "YAML", "images", "inline", "setup"},
		{"--format", "jsonl", "images", "inline", "setup"},
		{"--format", "raw", "images", "inline", "setup"},
		{"--format", "explore", "images", "inline", "setup"},
		{"--transform", ".secret", "images", "inline", "setup"},
		{"--raw-output", "images", "inline", "setup"},
	}
	for _, args := range cases {
		t.Run(fmt.Sprint(args), func(t *testing.T) {
			var out bytes.Buffer
			calls := 0
			command := previewSetupCommand(&out, func(context.Context, io.Writer) error { calls++; return nil })
			err := command.Run(t.Context(), append([]string{"openai"}, args...))
			require.Error(t, err)
			require.Zero(t, calls)
			require.Empty(t, out.String())
			require.NotContains(t, err.Error(), "secret")
			require.NotContains(t, err.Error(), "\x1b")
		})
	}
}

func TestPreviewSetupAllowsReadableFormatsAndPreservesContextWriter(t *testing.T) {
	type key struct{}
	for _, format := range []string{"auto", "text", "TEXT"} {
		t.Run(format, func(t *testing.T) {
			var out bytes.Buffer
			calls := 0
			command := previewSetupCommand(&out, func(ctx context.Context, writer io.Writer) error {
				calls++
				require.Equal(t, "marker", ctx.Value(key{}))
				require.Same(t, &out, writer)
				_, err := io.WriteString(writer, "local only\n")
				return err
			})
			ctx := context.WithValue(t.Context(), key{}, "marker")
			require.NoError(t, command.Run(ctx, []string{"openai", "--format", format, "images", "inline", "setup"}))
			require.Equal(t, 1, calls)
			require.Equal(t, "local only\n", out.String())
		})
	}
}

func TestPreviewSetupRedactsFilesystemCauses(t *testing.T) {
	pathErr := &os.PathError{Op: "open", Path: "/private/sensitive\x1b]52;payload\a/state.json", Err: os.ErrPermission}
	linkErr := &os.LinkError{Op: "link", Old: "/private/source", New: "/private/destination", Err: os.ErrNotExist}
	for _, cause := range []error{pathErr, fmt.Errorf("cache wrapper reveals /private/wrapper: %w", pathErr), errors.Join(errors.New("/private/joined"), linkErr, pathErr)} {
		command := previewSetupCommand(io.Discard, func(context.Context, io.Writer) error { return cause })
		err := command.Run(t.Context(), []string{"openai", "images", "inline", "setup"})
		require.Error(t, err)
		require.ErrorIs(t, err, cause)
		require.Contains(t, err.Error(), "Retain the cache and saved originals")
		require.NotContains(t, err.Error(), "/private")
		require.NotContains(t, err.Error(), "payload")
		require.NotContains(t, err.Error(), "\x1b")
	}
}

func TestPreviewSetupRetainsLogicalGuidanceAndCancellation(t *testing.T) {
	for _, cause := range []error{context.Canceled, errors.New("select your original font and size, then retry"), io.ErrShortWrite} {
		command := previewSetupCommand(io.Discard, func(context.Context, io.Writer) error { return cause })
		err := command.Run(t.Context(), []string{"openai", "images", "inline", "setup"})
		require.ErrorIs(t, err, cause)
		require.Contains(t, err.Error(), cause.Error())
	}
}

func TestPreviewSetupHelpIsLocalAndStatesLimits(t *testing.T) {
	for _, name := range []string{"setup", "repair", "status"} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			root := &cli.Command{Name: "openai", Writer: &out, ErrWriter: io.Discard, Commands: []*cli.Command{{Name: "images"}}}
			registerImagePreviewSetup(root)
			args, help, err := ConfigureHelp(root, []string{"openai", "images", "inline", name, "--help"})
			require.NoError(t, err)
			require.True(t, help)
			require.NoError(t, root.Run(t.Context(), args))
			for _, want := range []string{"Automation permission", "--inline on", "preferences are unchanged", "No cache reset or removal of committed previews"} {
				require.Contains(t, out.String(), want)
			}
		})
	}
}
