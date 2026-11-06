package app

import (
	"fmt"

	"github.com/openai/openai-cli/internal/debugmiddleware"
	"github.com/urfave/cli/v3"
)

const (
	debugBodyFlag        = "debug-body"
	debugSSEFlag         = "debug-sse"
	debugEventTimingFlag = "debug-event-timing"
	debugMaxBytesFlag    = "debug-max-bytes"
	debugMaxEventsFlag   = "debug-max-events"
	debugDefaultBytes    = debugmiddleware.DefaultCaptureBytes
	debugDefaultEvents   = debugmiddleware.DefaultCaptureEvents
)

// Only locally declared flag names and limits enter this diagnostic.
// Keep its type so the error presenter can reject untyped private lookalikes.
type debugCaptureOptionError struct {
	message string
}

func (e *debugCaptureOptionError) Error() string { return e.message }

func debugCaptureFlags() []cli.Flag {
	return []cli.Flag{
		&cli.BoolFlag{
			Name: debugBodyFlag,
			Usage: "Show redacted JSON request and response structure on stderr. Requires --debug when enabled. " +
				"Non-JSON bodies are omitted.",
		},
		&cli.BoolFlag{
			Name: debugSSEFlag,
			Usage: "Inspect escaped, redacted SSE frames on stderr. Requires --debug when enabled. " +
				"Does not change streamed stdout.",
		},
		&cli.BoolFlag{
			Name: debugEventTimingFlag,
			Usage: "Time complete observed SSE data frames on stderr. Requires --debug when enabled. " +
				"This does not measure decoded event or output delivery.",
		},
		&cli.IntFlag{
			Name: debugMaxBytesFlag, Value: debugDefaultBytes,
			Usage: "Bound payload diagnostics per HTTP attempt, in bytes. Requires --debug. " +
				"Range: 1 to 1048576. Does not limit API payloads.",
		},
		&cli.IntFlag{
			Name: debugMaxEventsFlag, Value: debugDefaultEvents,
			Usage: "Bound SSE frame previews and data-frame timings per HTTP attempt. Requires --debug. " +
				"Range: 1 to 4096. Does not limit streamed events.",
		},
	}
}

// Read only root-owned flags. An endpoint field with the same name is unrelated.
// Validate before request preparation so invalid settings cannot read uploads or send requests.
func debugCaptureOptions(command *cli.Command) (debugmiddleware.CaptureOptions, error) {
	root := command.Root()
	opts := debugmiddleware.CaptureOptions{
		Bodies: root.Bool(debugBodyFlag), SSEFrames: root.Bool(debugSSEFlag),
		EventTiming: root.Bool(debugEventTimingFlag),
		MaxBytes:    debugDefaultBytes, MaxEvents: debugDefaultEvents,
	}
	for _, name := range []string{debugBodyFlag, debugSSEFlag, debugEventTimingFlag} {
		if root.Bool(name) && !root.Bool("debug") {
			return opts, &debugCaptureOptionError{message: fmt.Sprintf("--%s requires --debug", name)}
		}
	}
	for _, name := range []string{debugMaxBytesFlag, debugMaxEventsFlag} {
		if root.IsSet(name) && !root.Bool("debug") {
			return opts, &debugCaptureOptionError{message: fmt.Sprintf("--%s requires --debug", name)}
		}
	}
	if root.IsSet(debugMaxBytesFlag) {
		opts.MaxBytes = root.Int(debugMaxBytesFlag)
	}
	if root.IsSet(debugMaxEventsFlag) {
		opts.MaxEvents = root.Int(debugMaxEventsFlag)
	}
	if opts.MaxBytes < 1 || opts.MaxBytes > debugmiddleware.MaximumCaptureBytes {
		return opts, &debugCaptureOptionError{message: fmt.Sprintf("--%s must be between 1 and %d", debugMaxBytesFlag, debugmiddleware.MaximumCaptureBytes)}
	}
	if opts.MaxEvents < 1 || opts.MaxEvents > debugmiddleware.MaximumCaptureEvents {
		return opts, &debugCaptureOptionError{message: fmt.Sprintf("--%s must be between 1 and %d", debugMaxEventsFlag, debugmiddleware.MaximumCaptureEvents)}
	}
	return opts, nil
}
