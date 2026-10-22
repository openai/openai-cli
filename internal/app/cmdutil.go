package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/commands/audio"
	filecommands "github.com/openai/openai-cli/internal/commands/files"
	"github.com/openai/openai-cli/internal/commands/images"
	"github.com/openai/openai-cli/internal/commands/webhooks"
	"github.com/openai/openai-cli/internal/jsonview"
	clioutput "github.com/openai/openai-cli/internal/output"
	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/openai/openai-cli/internal/readable"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// Temporary forwarding value for the generated runtime bridge.
var OutputFormats = clioutput.Formats

// ValidateBaseURL forwards scheme validation for the generated runtime bridge.
func ValidateBaseURL(value, source string) error {
	return requestconfig.ValidateBaseURL(value, source)
}

// GetDefaultRequestOptions keeps feature middleware composition with its current owner.
func GetDefaultRequestOptions(cmd *cli.Command) []option.RequestOption {
	opts := requestconfig.DefaultOptions(cmd, Version, audio.CaptureText, captureSaveReceiptPolicy(cmd), webhooks.CaptureTestRequest)
	return append(opts, agentsRequestOptions(cmd)...)
}

func isTerminal(w io.Writer) bool { return terminal.IsWriterTerminal(w) }

// WriteBinaryResponse writes a binary response to stdout or a file.
//
// Takes in a stdout reference so we can test this function without overriding os.Stdout in tests.
func WriteBinaryResponse(response *http.Response, stdout io.Writer, outfile string) (message string, err error) {
	body := &clioutput.DownloadResponseBody{ReadCloser: response.Body}
	copyResponse := *response
	copyResponse.Body = body
	response = &copyResponse
	defer func() {
		if closeErr := body.Close(); closeErr != nil && !errors.Is(err, closeErr) {
			if err == nil {
				outcome := "Download incomplete. Output may contain partial bytes."
				if message != "" {
					outcome = "Download incomplete. The destination may contain partial output."
					if outfile == "" {
						outcome = "Download incomplete. An automatically selected file may remain with partial output."
					}
				}
				err = clioutput.NewDownloadSaveError(outcome, closeErr)
			} else {
				err = errors.Join(err, closeErr)
			}
		}
		if err != nil {
			message = ""
		}
		message, err = reportResponseSaveReceipt(response, message, err)
	}()
	if handled, message, err := audio.WriteSpeech(response, stdout, outfile); handled {
		return message, err
	}

	switch outfile {
	case "-", "/dev/stdout":
		return "", clioutput.CopyBinaryOutput(clioutput.DownloadContext(response), stdout, response.Body)
	case "":
		if !isTerminal(os.Stdout) {
			return "", clioutput.CopyBinaryOutput(clioutput.DownloadContext(response), stdout, response.Body)
		}
		return writeAutomaticBinaryResponse(response, stdout)
	default:
		return clioutput.WriteManagedBinaryResponse(response, outfile)
	}
}

// writeAutomaticBinaryResponse classifies a bounded prefix before streaming the
// complete response directly to its selected destination.
func writeAutomaticBinaryResponse(response *http.Response, stdout io.Writer) (string, error) {
	buffered := bufio.NewReader(response.Body)
	sample, err := buffered.Peek(512 + utf8.UTFMax - 1)
	if err != nil && err != io.EOF {
		return "", clioutput.NewDownloadSaveError("Download incomplete. No automatic destination file was created.", err)
	}
	if err != io.EOF && !utf8.Valid(sample) {
		for trim := 1; trim < utf8.UTFMax && trim <= len(sample); trim++ {
			boundary := len(sample) - trim
			if utf8.Valid(sample[:boundary]) && !utf8.FullRune(sample[boundary:]) {
				sample = sample[:boundary]
				break
			}
		}
	}
	if requestconfig.IsUTF8TextFile(sample) {
		if err := jsonview.WriteTerminalText(stdout, buffered); err != nil {
			return "", clioutput.NewDownloadSaveError("Download incomplete. Output may contain partial bytes.", err)
		}
		return "", nil
	}

	file, err := createDownloadFile(response, sample)
	if err != nil {
		return "", clioutput.NewDownloadSaveError("Download incomplete. An automatic destination file could not be created.", err)
	}
	filename := file.Name()
	owned, err := file.Stat()
	if err != nil {
		return "", clioutput.NewDownloadSaveError(
			"Download incomplete. An automatically selected file may remain with partial output.",
			errors.Join(err, file.Close()))
	}
	copyErr := copyDownloadFile(file, buffered)
	current, verifyErr := os.Lstat(filename)
	if verifyErr != nil {
		return "", clioutput.NewDownloadSaveError(
			"Download incomplete. The automatic destination could not be verified; partial output may remain.",
			errors.Join(copyErr, verifyErr))
	}
	if !os.SameFile(current, owned) {
		return "", clioutput.NewDownloadSaveError(
			"Download incomplete. The automatic destination changed; downloaded bytes may remain elsewhere.",
			errors.Join(copyErr, fmt.Errorf("download destination changed during streaming: %w", os.ErrInvalid)))
	}
	if copyErr != nil {
		return "", clioutput.NewDownloadSaveError("Download incomplete. The automatically selected file may contain partial output.", copyErr)
	}
	return fmt.Sprintf("Wrote output to: %s", jsonview.SanitizeTerminalString(filename)), nil
}

func copyDownloadFile(file io.WriteCloser, source io.Reader) (err error) {
	defer func() { err = errors.Join(err, file.Close()) }()
	return clioutput.CopyDownloadData(context.Background(), file, source)
}

// Return a writable file handle to a new file, which attempts to choose a good filename
// based on the Content-Disposition header or sniffing the MIME filetype of the response.
func createDownloadFile(response *http.Response, data []byte) (*os.File, error) {
	filename := "file"
	// If the header provided an output filename, use that
	disp := response.Header.Get("Content-Disposition")
	_, params, err := mime.ParseMediaType(disp)
	if err == nil {
		if dispFilename, ok := params["filename"]; ok {
			// Only use the last path component to prevent directory traversal
			filename = filepath.Base(dispFilename)
			// MIME decoding can introduce controls absent from the HTTP header.
			// Normalize the automatic name before both exclusive and fallback creation.
			filename = normalizeDownloadFilename(filename)
			if strings.HasPrefix(filename, ".") {
				// Server suggestions must not select hidden configuration files,
				// including through the collision fallback below.
				filename = "file"
			} else {
				// Try to create the file with exclusive flag to avoid race conditions
				file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err == nil {
					return file, nil
				}
			}
		}
	}

	// If file already exists, create a unique filename using CreateTemp
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = guessExtension(data)
	}
	base := strings.TrimSuffix(filename, ext)
	file, err := os.CreateTemp(".", base+"-*"+ext)
	if err == nil {
		return file, nil
	}
	// The suggested name may leave no room for CreateTemp's collision suffix.
	// A short content-derived name works without assuming a filesystem limit.
	file, fallbackErr := os.CreateTemp(".", "file-*"+guessExtension(data))
	if fallbackErr != nil {
		return nil, errors.Join(err, fallbackErr)
	}
	return file, nil
}

func normalizeDownloadFilename(filename string) string {
	var normalized strings.Builder
	normalized.Grow(len(filename))
	for offset := 0; offset < len(filename); {
		r, width := utf8.DecodeRuneInString(filename[offset:])
		if unicode.IsControl(r) {
			normalized.WriteByte('_')
		} else {
			// Preserve original bytes, including invalid UTF-8 in legacy names.
			normalized.WriteString(filename[offset : offset+width])
		}
		offset += width
	}
	return normalized.String()
}

func guessExtension(data []byte) string {
	ct := http.DetectContentType(data)

	// Prefer common extensions over obscure ones
	switch ct {
	case "application/gzip":
		return ".gz"
	case "application/pdf":
		return ".pdf"
	case "application/zip":
		return ".zip"
	case "audio/mpeg":
		return ".mp3"
	case "image/bmp":
		return ".bmp"
	case "image/gif":
		return ".gif"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	}

	exts, err := mime.ExtensionsByType(ct)
	if err == nil && len(exts) > 0 {
		return exts[0]
	} else if requestconfig.IsUTF8TextFile(data) {
		return ".txt"
	} else {
		return ".bin"
	}
}

func formatJSON(res gjson.Result, opts ShowJSONOpts) ([]byte, error) {
	return formatJSONForOutput(res, opts, opts.Stdout)
}

// Keep feature-specific destination classification outside shared formatting.
func formatJSONForOutput(res gjson.Result, opts ShowJSONOpts, destination io.Writer) ([]byte, error) {
	return clioutput.FormatJSON(res, clioutput.FormatOptions{
		Format: opts.Format, RawOutput: opts.RawOutput, Title: opts.Title, Transform: opts.Transform,
	}, destination, isTerminal(destination))
}

const warningExploreNotSupported = "Warning: Output format 'explore' not supported for non-terminal output; falling back to 'json'\n"

// ShowJSONOpts configures how JSON output is displayed.
type ShowJSONOpts struct {
	Context        context.Context // request cancellation; defaults to context.Background()
	Operation      string          // generated operation identifier; empty for error presentation
	OutputKind     OutputKind      // response, page item, or stream event; unspecified for errors
	ExplicitFormat bool            // true if the user explicitly passed --format
	Format         string          // output format (auto, text, explore, json, jsonl, pretty, raw, yaml)
	RawOutput      bool            // like jq -r: print strings without JSON quotes
	Stderr         io.Writer       // stderr for warnings; injectable for testing; defaults to os.Stderr
	Stdout         io.Writer       // output destination; defaults to os.Stdout
	Title          string          // display title
	Transform      string          // GJSON path to extract before displaying

	// The caller cancels Context on signals, so the explorer must not also handle them.
	CallerHandlesSignals bool
}

func (o *ShowJSONOpts) setDefaults() {
	if o.Context == nil {
		o.Context = context.Background()
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
}

// ShowJSON displays a single JSON result to the user.
func ShowJSON(res gjson.Result, opts ShowJSONOpts) error {
	if err := showJSON(res, opts, transformers.Select); err != nil {
		return err
	}
	return showBatchNextCommand(res, opts)
}

func showJSON(res gjson.Result, opts ShowJSONOpts, selectTransformer transformerSelector) error {
	opts.setDefaults()
	stopModelsListLoading(opts)
	if err := opts.Context.Err(); err != nil {
		return err
	}
	if handled, err := webhooks.ShowResult(res, webhooks.ResultOptions{ReadableOptions: readableOutputOptions(opts), WriteHint: writeWebhookHint}); handled {
		return err
	}
	if handled, err := filecommands.ShowResult(res, filecommands.ResultOptions{ReadableOptions: fileResultOptions(opts), Quiet: quietOutput(opts.Context)}); handled {
		return err
	}
	if isSavedImageRoute(opts, OutputResponse) {
		if handled, err := images.ShowSavedResponse(opts.Context, []byte(res.Raw)); handled {
			return err
		}
	}
	if text, ok := audio.TextResult(opts.Context, opts.Operation, opts.OutputKind); ok {
		if opts.Transform == "" && (strings.EqualFold(opts.Format, "raw") || opts.RawOutput) {
			if opts.RawOutput && isTerminal(opts.Stdout) {
				text = jsonview.SanitizeTerminalString(text)
			}
			_, err := (clioutput.Writer{Context: opts.Context, Sink: opts.Stdout}).WriteString(text)
			return err
		}
		encoded, err := json.Marshal(text)
		if err != nil {
			return err
		}
		res = gjson.ParseBytes(encoded)
	}
	res, err := clioutput.Transform(opts.Context, res, selectOutputTransformer(opts, selectTransformer))
	if err != nil {
		return err
	}
	opts.Format = resolvedOutputFormat(opts)
	projectAudio := opts.Transform == "" && !opts.RawOutput
	res = clioutput.ApplyJSONPath(res, opts.Transform)

	switch strings.ToLower(opts.Format) {
	case "text":
		if !opts.RawOutput || res.Type != gjson.String {
			if projectAudio {
				if event, ok := transformers.ProjectAudioResponse(res, transformers.Route{Operation: opts.Operation, OutputKind: opts.OutputKind}); ok {
					writer := readable.NewStreamWriter(clioutput.Writer{Context: opts.Context, Sink: opts.Stdout})
					if err := writer.Write(event); err != nil {
						return err
					}
					if !writer.HasOutput() {
						return readable.WriteText(clioutput.Writer{Context: opts.Context, Sink: opts.Stdout}, "")
					}
					return writer.Finish()
				}
			}
			out := clioutput.Writer{Context: opts.Context, Sink: opts.Stdout}
			omitted, err := clioutput.WriteReadableResource(out, res, readableOutputOptions(opts))
			if err != nil {
				return err
			}
			if omitted {
				return writeOutputHint(opts, clioutput.ResourceSummaryHint)
			}
			return nil
		}
	case "explore":
		if isTerminal(opts.Stdout) {
			var options []tea.ProgramOption
			if opts.CallerHandlesSignals {
				options = append(options, tea.WithoutSignalHandler())
			}
			return jsonview.ExploreJSONWithContext(opts.Context, opts.Title, res, opts.Stdout, options...)
		}
		if opts.ExplicitFormat && outputDiagnosticsAllowed(opts.Context) {
			if _, err := (clioutput.Writer{Context: opts.Context, Sink: opts.Stderr}).WriteString(warningExploreNotSupported); err != nil {
				return err
			}
		}
		opts.Format = "json"
	}
	opts.Transform = ""
	formatted, err := formatJSON(res, opts)
	if err != nil {
		return err
	}
	_, err = (clioutput.Writer{Context: opts.Context, Sink: opts.Stdout}).Write(formatted)
	return err
}

type hasRawJSON interface {
	RawJSON() string
}

// ShowJSONIterator displays an iterator of values to the user. Use itemsToDisplay = -1 for no limit.
func ShowJSONIterator[T any](iter jsonview.Iterator[T], itemsToDisplay int64, opts ShowJSONOpts) error {
	route := transformers.Route{Operation: opts.Operation, OutputKind: opts.OutputKind}
	if transformers.IsAgentsStream(route) {
		state := transformers.AgentsStreamState{AllowNoInputSessionCreation: agentsSessionCreationWithoutInput(opts.Context)}
		return showJSONIterator(&agentsStream[T]{source: iter, route: route, state: state}, itemsToDisplay, opts, transformers.Select)
	}
	return showJSONIterator(iter, itemsToDisplay, opts, transformers.Select)
}

func showJSONIterator[T any](source jsonview.Iterator[T], itemsToDisplay int64, opts ShowJSONOpts, selectTransformer transformerSelector) (resultErr error) {
	opts.setDefaults()
	stopModelsListLoading(opts)
	if isSavedImageRoute(opts, OutputStreamEvent) {
		if handled, err := images.ShowSavedStream(opts.Context, source, quietOutput(opts.Context)); handled {
			return err
		}
	}
	// SDK streams own response bodies and timers. Close even when presentation
	// stops at a limit, cancellation, or an output failure. Saved-image streams
	// retain their cleanup owner above; list iterators remain caller-owned.
	if opts.OutputKind == OutputStreamEvent {
		if closer, ok := any(source).(io.Closer); ok {
			defer func() { resultErr = errors.Join(resultErr, closer.Close()) }()
		}
	}
	if handled, err := showFiniteJSONList(source, itemsToDisplay, opts, selectTransformer); handled {
		return err
	}
	if itemsToDisplay == 0 {
		return errors.Join(opts.Context.Err(), source.Err())
	}
	if handled, err := showModelsListSelection(source, itemsToDisplay, opts); handled {
		return err
	}
	iter := clioutput.NewIterator(source, clioutput.IteratorOptions{
		Context:   opts.Context,
		Transform: selectOutputTransformer(opts, selectTransformer),
		Route:     transformers.Route{Operation: opts.Operation, OutputKind: opts.OutputKind},
		Limit:     itemsToDisplay,
	})
	if handled, err := showModelsListViewer(source, iter, opts); handled {
		return err
	}
	opts.Format = resolvedOutputFormat(opts)
	if opts.Format == "text" {
		if stdout, ok := opts.Stdout.(*os.File); ok && stdout == os.Stdout {
			return clioutput.StreamToStdout(func(*os.File) error { return clioutput.WriteReadableIterator(iter, readableOutputOptions(opts)) })
		}
		return clioutput.WriteReadableIterator(iter, readableOutputOptions(opts))
	}

	if strings.ToLower(opts.Format) == "explore" {
		if isTerminal(opts.Stdout) {
			out := clioutput.TerminalWriter{Writer: clioutput.Writer{Context: opts.Context, Sink: opts.Stdout}, File: opts.Stdout.(*os.File)}
			var explorer jsonview.Iterator[outputJSON] = iter
			if transformers.IsAgentsStream(transformers.Route{Operation: opts.Operation, OutputKind: opts.OutputKind}) {
				explorer = &agentsExplorerStream{source: iter}
			}
			err := jsonview.ExploreJSONStreamWithOutput(opts.Title, explorer, out)
			if iterErr := iter.Err(); iterErr != nil && !errors.Is(err, iterErr) {
				return errors.Join(err, iterErr)
			}
			return err
		}
		if opts.ExplicitFormat && outputDiagnosticsAllowed(opts.Context) {
			if _, err := (clioutput.Writer{Context: opts.Context, Sink: opts.Stderr}).WriteString(warningExploreNotSupported); err != nil {
				return err
			}
		}
		opts.Format = "json"
	}

	// The existing pager writes to process stdout. Other injected destinations
	// must stay on their own writer, including error output on stderr.
	stdout, processStdout := opts.Stdout.(*os.File)
	writeUnpaged := func(destination io.Writer) error {
		for iter.Next() {
			formatted, err := formatJSON(iter.Current().Result, opts)
			if err != nil {
				return errors.Join(err, iter.Err())
			}
			if _, err := (clioutput.Writer{Context: opts.Context, Sink: destination}).Write(formatted); err != nil {
				return errors.Join(err, iter.Err())
			}
		}
		return iter.Err()
	}
	if !processStdout || stdout != os.Stdout {
		return writeUnpaged(opts.Stdout)
	}
	// Event streams cannot wait for a page of output. Preserve stdout's
	// signal/error handling while delivering each formatted event.
	if opts.OutputKind == OutputStreamEvent {
		return clioutput.StreamToStdout(func(out *os.File) error { return writeUnpaged(out) })
	}

	terminalWidth, terminalHeight, err := term.GetSize(os.Stdout.Fd())
	if err != nil {
		terminalWidth = 100
		terminalHeight = 40
	}

	// Decide whether or not to use a pager based on whether it's a short output or a long output.
	usePager := false
	output := []byte{}
	numberOfNewlines := 0
	for iter.Next() {
		formatted, err := formatJSON(iter.Current().Result, opts)
		if err != nil {
			return errors.Join(err, iter.Err())
		}
		output = append(output, formatted...)
		numberOfNewlines += clioutput.CountTerminalLines(formatted, terminalWidth)
		if numberOfNewlines >= terminalHeight-3 {
			usePager = true
			break
		}
	}

	if !usePager {
		return clioutput.StreamToStdout(func(stdout *os.File) error {
			if _, err := (clioutput.Writer{Context: opts.Context, Sink: stdout}).Write(output); err != nil {
				return errors.Join(err, iter.Err())
			}
			return iter.Err()
		})
	}

	return clioutput.Stream(opts.Title, func(pager *os.File) error {
		if _, err := (clioutput.Writer{Context: opts.Context, Sink: pager}).Write(output); err != nil {
			return errors.Join(err, iter.Err())
		}
		pagerOpts := opts
		pagerOpts.Stdout = pager
		for iter.Next() {
			formatted, err := formatJSONForOutput(iter.Current().Result, pagerOpts, opts.Stdout)
			if err != nil {
				return errors.Join(err, iter.Err())
			}
			if _, err := (clioutput.Writer{Context: opts.Context, Sink: pager}).Write(formatted); err != nil {
				return errors.Join(err, iter.Err())
			}
		}
		return iter.Err()
	})
}

func fileResultOptions(opts ShowJSONOpts) clioutput.ReadableOptions {
	result := readableOutputOptions(opts)
	result.Terminal = isTerminal(opts.Stdout) && isTerminal(opts.Stderr)
	return result
}
