package custom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/openai/openai-cli/internal/audiomedia"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

const localAudioMetadata = "openai-local-audio"

func registerAudioCommands(root *cli.Command) {
	group := root.Command("audio")
	if group == nil {
		return
	}
	for _, command := range []*cli.Command{
		{Name: "licenses", Usage: "Print the bundled audio editor dependency notice", Action: handleAudioLicenses},
		{Name: "history", Usage: "List privately retained speech results", Flags: []cli.Flag{&cli.BoolFlag{Name: "clear", Usage: "Delete owned audio history; preserve exported files"}}, Action: handleAudioHistory},
		{Name: "inspect", Usage: "Show a retained result's waveform, duration, and settings", ArgsUsage: "ID", Action: handleAudioInspect},
		{Name: "play", Usage: "Play a retained speech result locally", ArgsUsage: "ID", Action: handleAudioPlay},
		{Name: "export", Usage: "Save original retained audio bytes to a file", ArgsUsage: "ID", Flags: []cli.Flag{&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Usage: "Save original audio to PATH, or - for stdout", Required: true, TakesFile: true}}, Action: handleAudioExport},
	} {
		command.HideHelpCommand = true
		command.Metadata = map[string]any{localAudioMetadata: true}
		command.Description = "Uses local audio history without API requests. The speech form retains audio, text, instructions, and settings privately."
		if command.Name == "licenses" {
			command.Description = "Prints the embedded dependency notice without credentials, history access, or network requests."
		}
		command.Action = audioLocalAction(command.Action)
		group.Commands = append(group.Commands, command)
	}
}

// Own signals before history reads, snapshot copying, or playback starts.
// Join the operation error while retaining the user's real signal exit status.
func audioLocalAction(next cli.ActionFunc) cli.ActionFunc {
	return func(parent context.Context, command *cli.Command) error {
		ctx, stop := downloadSignalContext(parent)
		defer stop()
		err := next(ctx, command)
		var signalErr *downloadSignalError
		if errors.As(context.Cause(ctx), &signalErr) {
			return errors.Join(cli.Exit("", signalErr.exitCode), err)
		}
		return errors.Join(err, ctx.Err())
	}
}

// Before runs after descendant parsing. Match exact registered command nodes;
// flag values and remote audio commands must never select this local path.
func isLocalAudioCommand(command *cli.Command) bool {
	root := command.Root()
	group := root.Command(root.Args().First())
	if group == nil || group.Name != "audio" {
		return false
	}
	leaf := group.Command(group.Args().First())
	return leaf != nil && leaf.Metadata[localAudioMetadata] == true
}

func audioStore() (audiohistory.Store, error) {
	store, err := audiohistory.Default()
	return store, audioFailureIf("Could not open private audio history.", err)
}
func audioFailureIf(message string, err error) error {
	if err == nil {
		return nil
	}
	return audioFailure(message, err)
}

func handleAudioHistory(ctx context.Context, command *cli.Command) error {
	if command.Args().Present() {
		return audioFailure("Audio history takes no positional arguments.", nil)
	}
	store, err := audioStore()
	if err != nil {
		return err
	}
	if command.Bool("clear") {
		if err := store.Clear(ctx); err != nil {
			return audioFailure("Could not clear all owned audio history. Exported files are unchanged.", err)
		}
		return showAudioData(ctx, command, map[string]any{"cleared": true})
	}
	entries, listErr := store.List(ctx)
	visible := make([]audiohistory.Entry, 0, len(entries))
	for _, entry := range entries {
		if !entry.Settings.Preview {
			visible = append(visible, entry)
		}
	}
	return errors.Join(showAudioData(ctx, command, visible), audioFailureIf("Some audio history records could not be read.", listErr))
}

func openAudioResult(ctx context.Context, command *cli.Command) (*audiohistory.Snapshot, error) {
	if command.Args().Len() != 1 {
		return nil, audioFailure("Specify one audio history ID.", nil)
	}
	store, err := audioStore()
	if err != nil {
		return nil, err
	}
	snapshot, err := store.Open(ctx, command.Args().First())
	return snapshot, audioFailureIf("Could not open the retained audio. Run openai audio history for available IDs.", err)
}

func handleAudioInspect(ctx context.Context, command *cli.Command) (err error) {
	snapshot, err := openAudioResult(ctx, command)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, audioFailureIf("Could not clean up the private audio snapshot.", snapshot.Close()))
	}()
	analysis, err := audiomedia.Analyze(ctx, snapshot.File, snapshot.Entry.Settings.Format)
	if err != nil {
		return audioFailure("Could not measure audio. Compressed formats need FFmpeg on PATH.", err)
	}
	root := command.Root()
	format := strings.ToLower(root.String("format"))
	if !root.IsSet("transform") && !root.Bool("raw-output") && (format == "auto" || format == "text") {
		// Render once through the established output path. Numeric peaks remain
		// available in explicit data formats without flooding the readable view.
		return showAudioData(ctx, command, struct {
			Waveform string             `json:"waveform"`
			Duration string             `json:"duration"`
			Entry    audiohistory.Entry `json:"entry"`
		}{"\n" + audiomedia.Waveform(analysis.Peaks, 64, 5), fmt.Sprintf("%s (%g s)", audioDuration(analysis.Duration), analysis.Duration), snapshot.Entry})
	}
	return showAudioData(ctx, command, struct {
		Entry    audiohistory.Entry  `json:"entry"`
		Analysis audiomedia.Analysis `json:"analysis"`
	}{snapshot.Entry, analysis})
}

func handleAudioPlay(ctx context.Context, command *cli.Command) (err error) {
	snapshot, err := openAudioResult(ctx, command)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, audioFailureIf("Could not clean up the private audio snapshot.", snapshot.Close()))
	}()
	err = audiomedia.Play(ctx, snapshot.File, snapshot.Entry.Settings.Format, 0)
	return audioFailureIf("Could not play audio. Playback needs FFplay; compressed formats also need FFmpeg on PATH.", err)
}

func exportAudio(ctx context.Context, snapshot *audiohistory.Snapshot, destination string, stdout io.Writer) error {
	reader := io.NewSectionReader(snapshot.File, 0, snapshot.Entry.Bytes)
	if destination == "-" || destination == "/dev/stdout" {
		return copyBinaryOutput(ctx, stdout, reader)
	}
	response := &http.Response{Body: io.NopCloser(reader), Request: (&http.Request{}).WithContext(ctx)}
	_, err := writeManagedDownloadResponse(response, destination, copyDownloadData)
	return err
}

func handleAudioExport(ctx context.Context, command *cli.Command) (err error) {
	snapshot, err := openAudioResult(ctx, command)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, audioFailureIf("Could not clean up the private audio snapshot.", snapshot.Close()))
	}()
	destination := command.String("output")
	err = exportAudio(ctx, snapshot, destination, command.Root().Writer)
	message := ""
	if err == nil && destination != "-" && destination != "/dev/stdout" {
		message = "Wrote output to: " + destination
	}
	return ReportSaveReceipt(command, os.Stderr, message, err)
}

func showAudioData(ctx context.Context, command *cli.Command, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	root := command.Root()
	write := func() error {
		return ShowJSON(gjson.ParseBytes(data), ShowJSONOpts{Context: ctx, Format: strings.ToLower(root.String("format")), ExplicitFormat: root.IsSet("format"), RawOutput: root.Bool("raw-output"), Transform: root.String("transform"), Stdout: root.Writer, Stderr: root.ErrWriter, Title: "audio " + command.Name})
	}
	if root.Writer == os.Stdout {
		return streamToStdout(func(*os.File) error { return write() })
	}
	return write()
}
