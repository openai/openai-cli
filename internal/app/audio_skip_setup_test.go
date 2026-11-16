package app

import (
	"context"
	"sync/atomic"
	"testing"

	audiocommands "github.com/openai/openai-cli/internal/commands/audio"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func audioFirstRunTestRoot() *cli.Command {
	root := &cli.Command{Name: "openai", Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "project", Aliases: []string{"P"}},
		&requestflag.Flag[string]{Name: "organization"},
		&cli.StringFlag{Name: "format"}, &cli.BoolFlag{Name: "verbose"},
		&cli.BoolFlag{Name: "raw-output", Aliases: []string{"r"}},
		&cli.StringSliceFlag{Name: "header", Aliases: []string{"H"}},
	}, Commands: []*cli.Command{
		{Name: "audio", Aliases: []string{"sound"}, Commands: []*cli.Command{
			{Name: "speak", Flags: []cli.Flag{&cli.StringFlag{Name: "input"}}},
			{Name: "transcribe", Flags: []cli.Flag{&cli.StringFlag{Name: "file"}}},
			{Name: "translate", Flags: []cli.Flag{&cli.StringFlag{Name: "file"}}},
			{Name: "speech", Commands: []*cli.Command{{Name: "create"}}},
		}},
		{Name: "models", Commands: []*cli.Command{{Name: "list"}}},
		{Name: "help", HideHelpCommand: true, Metadata: map[string]any{"help-topic-command": true},
			Flags: []cli.Flag{&cli.BoolFlag{Name: "all", Hidden: true}}},
	}}
	requestconfig.ConfigureRootFlags(root)
	audiocommands.RegisterLocal(root, audiocommands.LocalOptions{})
	root.Command("audio").Command("history").Aliases = []string{"past"}
	return root
}

func TestAudioFirstRunScannerKeepsFlagsAndValuesDistinct(t *testing.T) {
	root := audioFirstRunTestRoot()
	for _, args := range [][]string{
		{"openai", "audio", "history"}, {"openai", "audio", "help", "inspect"},
		{"openai", "audio", "licenses"},
		{"openai", "help", "--all", "audio", "licenses"},
		{"openai", "audio", "help", "--all", "licenses"},
		{"openai", "help", "audio", "play"}, {"openai", "sound", "past"},
		{"openai", "--verbose", "audio", "export"}, {"openai", "audio", "-r", "history"},
		{"openai", "--project=audio", "audio", "history"},
		{"openai", "-format", "json", "audio", "--organization", "play", "inspect"},
		{"openai", "-H", "X-Test: audio", "audio", "history"},
		{"openai", "--verbose=false", "audio", "history"},
		{"openai", "audio", "--help", "inspect"},
		{"openai", "audio", "history", "--unknown-leaf-option"},
	} {
		require.True(t, audiocommands.SkipFirstRunSetup(root, args), "%q", args)
	}
	for _, args := range [][]string{
		{"openai", "audio", "speak", "--input", "history"},
		{"openai", "--project", "audio", "models", "list"},
		{"openai", "--header", "audio", "models", "list"},
		{"openai", "--unknown", "audio", "history"},
		{"openai", "--unknown=ignored", "audio", "history"},
		{"openai", "audio", "--unknown", "history"},
		{"openai", "audio", "speech", "create"}, {"openai", "--project"},
		{"openai", "--verbose", "false", "audio", "history"},
		{"openai", "--verbose=audio", "audio", "history"},
		{"openai", "--project", "audio", "history"},
		{"openai", "audio", "--project", "history"},
		{"openai", "unknown", "audio", "history"},
		{"openai", "help", "help", "audio", "history"},
		{"openai", "--all", "audio", "history"},
	} {
		require.False(t, audiocommands.SkipFirstRunSetup(root, args), "%q", args)
	}
	require.False(t, audiocommands.SkipFirstRunSetup(nil, []string{"openai", "audio", "history"}))
	require.False(t, audiocommands.SkipFirstRunSetup(root, nil))
}

func TestAudioFirstRunScannerResetsDoubleDashAtChildDispatch(t *testing.T) {
	root := audioFirstRunTestRoot()
	for _, args := range [][]string{
		{"openai", "--", "audio", "history"},
		{"openai", "--", "audio", "--project", "ignored", "history"},
		{"openai", "audio", "--", "history"},
		{"openai", "--", "audio", "--", "history"},
		{"openai", "--project", "--", "audio", "history"},
		{"openai", "help", "--", "audio", "history"},
	} {
		require.True(t, audiocommands.SkipFirstRunSetup(root, args), "%q", args)
	}
	for _, args := range [][]string{
		{"openai", "--", "--project", "audio", "history"},
		{"openai", "audio", "--", "--project", "ignored", "history"},
		{"openai", "--", "audio", "--project", "history"},
		{"openai", "help", "--", "audio", "--project", "ignored", "history"},
	} {
		require.False(t, audiocommands.SkipFirstRunSetup(root, args), "%q", args)
	}
}

func TestAudioFirstRunScannerUsesFlagOwnershipAndCurrentDeclarations(t *testing.T) {
	root := audioFirstRunTestRoot()
	audio := root.Command("audio")
	root.Flags = append(root.Flags, &cli.StringFlag{Name: "root-only", Local: true},
		&cli.StringFlag{Name: "selector", Aliases: []string{"S"}})
	require.True(t, audiocommands.SkipFirstRunSetup(root, []string{"openai", "--root-only", "audio", "audio", "history"}))
	require.False(t, audiocommands.SkipFirstRunSetup(root, []string{"openai", "audio", "--root-only", "value", "history"}))
	require.False(t, audiocommands.SkipFirstRunSetup(root, []string{"openai", "help", "--root-only", "value", "audio", "history"}))
	root.Command("help").Flags = append(root.Command("help").Flags, &cli.StringFlag{Name: "topic-probe", Aliases: []string{"T"}})
	require.True(t, audiocommands.SkipFirstRunSetup(root, []string{"openai", "help", "-T", "audio", "audio", "history"}))
	require.False(t, audiocommands.SkipFirstRunSetup(root, []string{"openai", "help", "-T", "audio", "history"}))
	require.True(t, audiocommands.SkipFirstRunSetup(root, []string{"openai", "audio", "-S", "audio", "history"}))
	audio.Flags = append(audio.Flags, &cli.BoolFlag{Name: "selector", Local: true})
	require.True(t, audiocommands.SkipFirstRunSetup(root, []string{"openai", "audio", "--selector", "history"}))
	require.False(t, audiocommands.SkipFirstRunSetup(root, []string{"openai", "audio", "-S", "value", "history"}), "shadowed ancestor aliases must not remain active")
	args := []string{"openai", "--future", "audio", "audio", "history"}
	require.False(t, audiocommands.SkipFirstRunSetup(root, args))
	root.Flags = append(root.Flags, &cli.StringFlag{Name: "future"})
	require.True(t, audiocommands.SkipFirstRunSetup(root, args), "new flag declarations must apply without a cache refresh")
	root.Flags[len(root.Flags)-1] = &cli.BoolFlag{Name: "future"}
	require.False(t, audiocommands.SkipFirstRunSetup(root, args), "changed flag arity must apply immediately")
	root.MutuallyExclusiveFlags = []cli.MutuallyExclusiveFlags{{Flags: [][]cli.Flag{{&cli.StringFlag{Name: "grouped-option"}}}}}
	require.True(t, audiocommands.SkipFirstRunSetup(root, []string{"openai", "--grouped-option", "audio", "audio", "history"}))
	audio.Command("history").Metadata[audiocommands.LocalMetadata] = false
	require.False(t, audiocommands.SkipFirstRunSetup(root, []string{"openai", "audio", "history"}))
	audio.Command("history").Metadata[audiocommands.LocalMetadata] = true
	audio.Command("history").Aliases = append(audio.Command("history").Aliases, "archive")
	require.True(t, audiocommands.SkipFirstRunSetup(root, []string{"openai", "sound", "archive"}))
	root.Commands[0] = &cli.Command{Name: "audio", Commands: []*cli.Command{{Name: "history"}}}
	require.False(t, audiocommands.SkipFirstRunSetup(root, []string{"openai", "audio", "history"}), "replaced nodes must not reuse old metadata")
}

func TestAudioFirstRunScannerRespectsDeclaredFlagParsingPolicy(t *testing.T) {
	root := audioFirstRunTestRoot()
	root.SkipFlagParsing = true
	require.False(t, audiocommands.SkipFirstRunSetup(root, []string{"openai", "--project", "ignored", "audio", "history"}))
	require.True(t, audiocommands.SkipFirstRunSetup(root, []string{"openai", "audio", "--project", "ignored", "history"}))
	root.SkipFlagParsing = false
	stop := 0
	root.StopOnNthArg = &stop
	require.False(t, audiocommands.SkipFirstRunSetup(root, []string{"openai", "--project", "ignored", "audio", "history"}))
	require.True(t, audiocommands.SkipFirstRunSetup(root, []string{"openai", "audio", "--project", "ignored", "history"}))
}

type audioFirstRunFlagSpy struct {
	*cli.StringFlag
	names, values atomic.Int32
}

func (f *audioFirstRunFlagSpy) Names() []string        { f.names.Add(1); return f.StringFlag.Names() }
func (f *audioFirstRunFlagSpy) TakesValue() bool       { f.values.Add(1); return true }
func (*audioFirstRunFlagSpy) PreParse() error          { panic("startup scanner invoked PreParse") }
func (*audioFirstRunFlagSpy) PostParse() error         { panic("startup scanner invoked PostParse") }
func (*audioFirstRunFlagSpy) Set(string, string) error { panic("startup scanner invoked Set") }
func (*audioFirstRunFlagSpy) Get() any                 { panic("startup scanner inspected parsed values") }

func TestAudioFirstRunScannerNeverRunsParserCallbacks(t *testing.T) {
	root := audioFirstRunTestRoot()
	root.Action = func(context.Context, *cli.Command) error { t.Fatal("scanner invoked root action"); return nil }
	spy := &audioFirstRunFlagSpy{StringFlag: &cli.StringFlag{Name: "future-option", Aliases: []string{"F"}}}
	root.Flags = append(root.Flags, spy)
	args := []string{"openai", "-F", "audio", "audio", "history"}
	copyArgs := append([]string(nil), args...)
	require.True(t, audiocommands.SkipFirstRunSetup(root, args))
	require.Positive(t, spy.names.Load())
	require.Positive(t, spy.values.Load())
	require.Equal(t, copyArgs, args)
	require.False(t, spy.IsSet())
}
