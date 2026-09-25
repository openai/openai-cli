package custom

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/openai/openai-go/v3/option"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
	"github.com/itchyny/json2yaml"
	"github.com/muesli/reflow/wrap"
	"github.com/tidwall/gjson"
	"github.com/tidwall/pretty"
	"github.com/urfave/cli/v3"
)

var OutputFormats = []string{"auto", "text", "explore", "json", "jsonl", "pretty", "raw", "yaml"}

// ValidateBaseURL checks that a base URL is correctly prefixed with a protocol scheme and produces a better
// error message than the person would see otherwise if it doesn't.
func ValidateBaseURL(value, source string) error {
	if value != "" && !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
		return fmt.Errorf("%s %q is missing a scheme (expected http:// or https://)", source, value)
	}
	return nil
}

func GetDefaultRequestOptions(cmd *cli.Command) []option.RequestOption {
	opts := []option.RequestOption{
		option.WithHeader("User-Agent", fmt.Sprintf("OpenAI/CLI %s", Version)),
		option.WithHeader("X-Stainless-Lang", "cli"),
		option.WithHeader("X-Stainless-Package-Version", Version),
		option.WithHeader("X-Stainless-Runtime", "cli"),
		option.WithHeader("X-Stainless-CLI-Command", cmd.FullName()),
		option.WithMiddleware(captureAudioText),
	}
	if cmd.IsSet("api-key") {
		opts = append(opts, option.WithAPIKey(cmd.String("api-key")))
	}
	if cmd.IsSet("admin-api-key") {
		opts = append(opts, option.WithAdminAPIKey(cmd.String("admin-api-key")))
	}
	if cmd.IsSet("organization") {
		opts = append(opts, option.WithOrganization(cmd.String("organization")))
	}
	if cmd.IsSet("project") {
		opts = append(opts, option.WithProject(cmd.String("project")))
	}
	if cmd.IsSet("webhook-secret") {
		opts = append(opts, option.WithWebhookSecret(cmd.String("webhook-secret")))
	}

	// Override base URL if the --base-url flag is provided
	if baseURL := cmd.String("base-url"); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	if httpClient, ok := cmd.Root().Metadata[mtlsHTTPClientMetadata].(*http.Client); ok {
		opts = append(opts, option.WithHTTPClient(httpClient))
	}

	return opts
}

// isInputPiped tries to check for input being piped into the CLI which tells us that we should try to read
// from stdin. This can be a bit tricky in some cases like when an stdin is connected to a pipe but nothing is
// being piped in (this may happen in some environments like Cursor's integration terminal or CI), which is
// why this function is a little more elaborate than it'd be otherwise.
func isInputPiped() bool {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return false
	}

	mode := stat.Mode()

	// Regular file (redirect like < file.txt) — only if non-empty.
	//
	// Notably, on Unix the case like `< /dev/null` is handled below because `/dev/null` is not a regular
	// file. On Windows, NUL appears as a regular file with size 0, so it's also handled correctly.
	if mode.IsRegular() && stat.Size() > 0 {
		return true
	}

	// For pipes/sockets (e.g. `echo foo | openai`), use an OS-specific check to determine whether
	// data is actually available. Some environments like Cursor's integrated terminal connect stdin as a
	// pipe even when nothing is being piped.
	if mode&(os.ModeNamedPipe|os.ModeSocket) != 0 {
		// Defined in either cmdutil_unix.go or cmdutil_windows.go.
		return isPipedDataAvailableOSSpecific()
	}

	return false
}

func isTerminal(w io.Writer) bool {
	switch v := w.(type) {
	case *os.File:
		return term.IsTerminal(v.Fd())
	default:
		return false
	}
}

func streamOutput(label string, generateOutput func(w *os.File) error) error {
	// For non-tty output (probably a pipe), write directly to stdout
	if !isTerminal(os.Stdout) {
		return streamToStdout(generateOutput)
	}

	// When streaming output on Unix-like systems, there's a special trick involving creating two socket pairs
	// that we prefer because it supports small buffer sizes which results in less pagination per buffer. The
	// constructs needed to run it don't exist on Windows builds, so we have this function broken up into
	// OS-specific files with conditional build comments. Under Windows (and in case our fancy constructs fail
	// on Unix), we fall back to using pipes (`streamToPagerWithPipe`), which are OS agnostic.
	//
	// Defined in either cmdutil_unix.go or cmdutil_windows.go.
	return streamOutputOSSpecific(label, generateOutput)
}

// pagerCommand splits PAGER into an executable and its arguments. $PAGER is written
// as a command line by the tools this CLI is used beside (git, gh, man), so
// "less -R" has to reach less rather than be resolved as a single executable name.
// When the first word does not resolve, the whole value is kept, so a configuration
// that works today keeps working, including an executable path that contains spaces.
func pagerCommand() []string {
	pager := strings.TrimSpace(os.Getenv("PAGER"))
	if pager == "" {
		return []string{"less"}
	}
	// Prefer a complete executable path before treating spaces as argument
	// separators; a path containing spaces may itself be executable.
	if _, err := exec.LookPath(pager); err == nil {
		return []string{pager}
	}
	if command := strings.Fields(pager); len(command) > 1 {
		if _, err := exec.LookPath(command[0]); err == nil {
			return command
		}
	}
	return []string{pager}
}

// pagerError distinguishes pager setup and process failures from the request,
// formatter, or output callback. Keep the cause available to errors.Is/As.
type pagerError struct{ error }

func (e *pagerError) Unwrap() error { return e.error }

func wrapPagerError(err error) error {
	if err == nil {
		return nil
	}
	return &pagerError{err}
}

func streamToPagerWithPipe(label string, generateOutput func(w *os.File) error) error {
	r, w, err := os.Pipe()
	if err != nil {
		return wrapPagerError(err)
	}
	defer r.Close()
	defer w.Close()

	command := pagerCommand()
	if _, err := exec.LookPath(command[0]); err != nil {
		return wrapPagerError(err)
	}

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin = r
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		"LESS=-X -r -P "+label,
		"MORE=-r -P "+label,
	)

	if err := cmd.Start(); err != nil {
		return wrapPagerError(err)
	}

	if err := r.Close(); err != nil {
		return wrapPagerError(err)
	}

	// If we would be streaming to a terminal and aren't forcing color one way
	// or the other, we should configure things to use color so the pager gets
	// colorized input.
	if isTerminal(os.Stdout) && os.Getenv("FORCE_COLOR") == "" {
		os.Setenv("FORCE_COLOR", "1")
	}

	outputErr := generateOutput(w)
	// Deliver EOF and reap the pager even when output generation failed.
	w.Close()
	waitErr := cmd.Wait()
	if outputErr != nil && !isOutputBrokenPipe(outputErr) {
		return outputErr
	}
	return wrapPagerError(waitErr)
}

func streamToStdout(generateOutput func(w *os.File) error) error {
	signal.Ignore(syscall.SIGPIPE)
	err := generateOutput(os.Stdout)
	if isOutputBrokenPipe(err) {
		return nil
	}
	return err
}

// outputWriteError identifies failures from the output sink, rather than from
// the iterator, transport, or formatter producing the output.
type outputWriteError struct{ error }

func (e *outputWriteError) Unwrap() error { return e.error }

func isOutputBrokenPipe(err error) bool {
	if outputErr, ok := err.(*outputWriteError); ok {
		return strings.Contains(outputErr.Error(), "broken pipe")
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		causes := joined.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !isOutputBrokenPipe(cause) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return isOutputBrokenPipe(wrapped.Unwrap())
	}
	return false
}

// WriteBinaryResponse writes a binary response to stdout or a file.
//
// Takes in a stdout reference so we can test this function without overriding os.Stdout in tests.
func WriteBinaryResponse(response *http.Response, stdout io.Writer, outfile string) (string, error) {
	defer response.Body.Close()
	if handled, message, err := writeReadableSpeech(response, stdout, outfile); handled {
		return message, err
	}

	switch outfile {
	case "-", "/dev/stdout":
		_, err := io.Copy(stdout, response.Body)
		return "", err
	case "":
		if !isTerminal(os.Stdout) {
			_, err := io.Copy(stdout, response.Body)
			return "", err
		}
		return writeAutomaticBinaryResponse(response, stdout)
	default:
		file, err := os.OpenFile(outfile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			return "", err
		}
		if err := copyDownloadFile(file, response.Body); err != nil {
			return "", err
		}
		return fmt.Sprintf("Wrote output to: %s", outfile), nil
	}
}

// writeAutomaticBinaryResponse classifies a bounded prefix before streaming the
// complete response directly to its selected destination.
func writeAutomaticBinaryResponse(response *http.Response, stdout io.Writer) (string, error) {
	buffered := bufio.NewReader(response.Body)
	sample, err := buffered.Peek(512 + utf8.UTFMax - 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if !errors.Is(err, io.EOF) && !utf8.Valid(sample) {
		for trim := 1; trim < utf8.UTFMax && trim <= len(sample); trim++ {
			boundary := len(sample) - trim
			if utf8.Valid(sample[:boundary]) && !utf8.FullRune(sample[boundary:]) {
				sample = sample[:boundary]
				break
			}
		}
	}
	if isUTF8TextFile(sample) {
		return "", jsonview.WriteTerminalText(stdout, buffered)
	}

	file, err := createDownloadFile(response, sample)
	if err != nil {
		return "", err
	}
	filename := file.Name()
	owned, err := file.Stat()
	if err != nil {
		if closeErr := file.Close(); closeErr != nil {
			return "", errors.Join(err, closeErr)
		}
		return "", err
	}
	if err := copyDownloadFile(file, buffered); err != nil {
		return "", err
	}
	current, err := os.Lstat(filename)
	if err != nil {
		return "", err
	}
	if !os.SameFile(current, owned) {
		return "", fmt.Errorf("download destination changed during streaming: %w", os.ErrInvalid)
	}
	return fmt.Sprintf("Wrote output to: %s", filename), nil
}

func copyDownloadFile(file io.WriteCloser, source io.Reader) (err error) {
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	_, err = io.Copy(file, source)
	return err
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
			// Try to create the file with exclusive flag to avoid race conditions
			file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err == nil {
				return file, nil
			}
		}
	}

	// If file already exists, create a unique filename using CreateTemp
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = guessExtension(data)
	}
	base := strings.TrimSuffix(filename, ext)
	return os.CreateTemp(".", base+"-*"+ext)
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
	} else if isUTF8TextFile(data) {
		return ".txt"
	} else {
		return ".bin"
	}
}

func shouldUseColors(w io.Writer) bool {
	force, ok := os.LookupEnv("FORCE_COLOR")
	if ok {
		if force == "1" {
			return true
		}
		if force == "0" {
			return false
		}
	}
	return isTerminal(w)
}

// prettyColorProfile picks the color profile for --format pretty output written
// to w. Lipgloss v2 styles always render full color escapes, so the output must
// be downsampled for its destination. This matches the lipgloss v1 behavior the
// CLI had before: plain text when w is not a terminal or NO_COLOR is set, and
// color on a terminal. CLICOLOR_FORCE is honored, and FORCE_COLOR works the same
// way as it does for the json and jsonl formats.
func prettyColorProfile(w io.Writer, environ []string) colorprofile.Profile {
	lookup := func(name string) string {
		value := ""
		for _, kv := range environ {
			if v, ok := strings.CutPrefix(kv, name+"="); ok {
				value = v
			}
		}
		return value
	}
	// Lipgloss v1 dropped all styling, including bold, under NO_COLOR, and
	// NO_COLOR won over CLICOLOR_FORCE. colorprofile does neither, so check it
	// here.
	if lookup("FORCE_COLOR") == "0" || lookup("NO_COLOR") != "" {
		return colorprofile.NoTTY
	}
	profile := colorprofile.Detect(w, environ)
	if lookup("FORCE_COLOR") == "1" && profile < colorprofile.ANSI {
		profile = max(colorprofile.Env(environ), colorprofile.ANSI256)
	}
	return profile
}

func formatJSON(res gjson.Result, opts ShowJSONOpts) ([]byte, error) {
	return formatJSONForOutput(res, opts, opts.Stdout)
}

// formatJSONForOutput keeps the final destination available when a pager sits between
// formatted output and the terminal.
func formatJSONForOutput(res gjson.Result, opts ShowJSONOpts, destination io.Writer) ([]byte, error) {
	opts.Format = resolvedOutputFormat(opts)
	res = applyJSONPath(res, opts.Transform)
	// Modeled after `jq -r` (`--raw-output`): if the result is a string, print it without JSON quotes so that
	// it's easier to pipe into other programs.
	if opts.RawOutput && res.Type == gjson.String {
		value := res.Str
		if isTerminal(destination) {
			value = jsonview.SanitizeTerminalString(value)
		}
		return []byte(value + "\n"), nil
	}
	switch strings.ToLower(opts.Format) {
	case "text":
		var text bytes.Buffer
		err := readable.Write(&text, res)
		return text.Bytes(), err
	case "pretty":
		var out bytes.Buffer
		w := &colorprofile.Writer{Forward: &out, Profile: prettyColorProfile(destination, os.Environ())}
		if _, err := w.WriteString(jsonview.RenderJSON(opts.Title, res) + "\n"); err != nil {
			return nil, err
		}
		return out.Bytes(), nil
	case "json":
		prettyJSON := pretty.Pretty([]byte(res.Raw))
		if shouldUseColors(destination) {
			return pretty.Color(prettyJSON, pretty.TerminalStyle), nil
		} else {
			return prettyJSON, nil
		}
	case "jsonl":
		// @ugly is gjson syntax for "no whitespace", so it fits on one line
		oneLineJSON := res.Get("@ugly").Raw
		if shouldUseColors(destination) {
			bytes := append(pretty.Color([]byte(oneLineJSON), pretty.TerminalStyle), '\n')
			return bytes, nil
		} else {
			return []byte(oneLineJSON + "\n"), nil
		}
	case "raw":
		return []byte(res.Raw + "\n"), nil
	case "yaml":
		input := strings.NewReader(res.Raw)
		var yaml strings.Builder
		if err := json2yaml.Convert(&yaml, input); err != nil {
			return nil, err
		}
		return []byte(yaml.String()), nil
	default:
		return nil, fmt.Errorf("Invalid format: %s, valid formats are: %s", opts.Format, strings.Join(OutputFormats, ", "))
	}
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
	return showJSON(res, opts, transformers.Select)
}

func showJSON(res gjson.Result, opts ShowJSONOpts, selectTransformer transformerSelector) error {
	opts.setDefaults()
	if err := opts.Context.Err(); err != nil {
		return err
	}
	if text, ok := audioTextResult(opts); ok {
		if opts.Transform == "" && (strings.EqualFold(opts.Format, "raw") || opts.RawOutput) {
			if opts.RawOutput && isTerminal(opts.Stdout) {
				text = jsonview.SanitizeTerminalString(text)
			}
			_, err := (outputWriter{ctx: opts.Context, out: opts.Stdout}).WriteString(text)
			return err
		}
		encoded, err := json.Marshal(text)
		if err != nil {
			return err
		}
		res = gjson.ParseBytes(encoded)
	}
	res, err := transformOutput(opts.Context, res, selectOutputTransformer(opts, selectTransformer))
	if err != nil {
		return err
	}
	opts.Format = resolvedOutputFormat(opts)
	projectAudio := opts.Transform == "" && !opts.RawOutput
	res = applyJSONPath(res, opts.Transform)

	switch strings.ToLower(opts.Format) {
	case "text":
		if !opts.RawOutput || res.Type != gjson.String {
			if projectAudio {
				if event, ok := transformers.ProjectAudioResponse(res, transformers.Route{Operation: opts.Operation, OutputKind: opts.OutputKind}); ok {
					writer := readable.NewStreamWriter(outputWriter{ctx: opts.Context, out: opts.Stdout})
					if err := writer.Write(event); err != nil {
						return err
					}
					if !writer.HasOutput() {
						return readable.WriteText(outputWriter{ctx: opts.Context, out: opts.Stdout}, "")
					}
					return writer.Finish()
				}
			}
			out := outputWriter{ctx: opts.Context, out: opts.Stdout}
			omitted, err := writeReadableResource(out, res, opts)
			if err != nil {
				return err
			}
			if omitted {
				return readable.WriteText(out, resourceSummaryHint)
			}
			return nil
		}
	case "explore":
		if isTerminal(opts.Stdout) {
			return jsonview.ExploreJSONWithOutput(opts.Title, res, opts.Stdout)
		}
		if opts.ExplicitFormat {
			if _, err := (outputWriter{ctx: opts.Context, out: opts.Stderr}).WriteString(warningExploreNotSupported); err != nil {
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
	_, err = (outputWriter{ctx: opts.Context, out: opts.Stdout}).Write(formatted)
	return err
}

// Get the number of lines that would be output by writing the data to the terminal
func countTerminalLines(data []byte, terminalWidth int) int {
	return bytes.Count([]byte(wrap.String(string(data), terminalWidth)), []byte("\n"))
}

type hasRawJSON interface {
	RawJSON() string
}

// ShowJSONIterator displays an iterator of values to the user. Use itemsToDisplay = -1 for no limit.
func ShowJSONIterator[T any](iter jsonview.Iterator[T], itemsToDisplay int64, opts ShowJSONOpts) error {
	return showJSONIterator(iter, itemsToDisplay, opts, transformers.Select)
}

func showJSONIterator[T any](source jsonview.Iterator[T], itemsToDisplay int64, opts ShowJSONOpts, selectTransformer transformerSelector) error {
	opts.setDefaults()
	if itemsToDisplay == 0 {
		return source.Err()
	}
	iter := &outputIterator[T]{
		source:    source,
		context:   opts.Context,
		transform: selectOutputTransformer(opts, selectTransformer),
		route:     transformers.Route{Operation: opts.Operation, OutputKind: opts.OutputKind},
		remaining: itemsToDisplay,
	}
	opts.Format = resolvedOutputFormat(opts)
	if opts.Format == "text" {
		if stdout, ok := opts.Stdout.(*os.File); ok && stdout == os.Stdout {
			return streamToStdout(func(*os.File) error { return showReadableIterator(iter, opts) })
		}
		return showReadableIterator(iter, opts)
	}

	if strings.ToLower(opts.Format) == "explore" {
		if isTerminal(opts.Stdout) {
			out := terminalOutputWriter{outputWriter{ctx: opts.Context, out: opts.Stdout}, opts.Stdout.(*os.File)}
			err := jsonview.ExploreJSONStreamWithOutput(opts.Title, iter, out)
			if iterErr := iter.Err(); iterErr != nil && !errors.Is(err, iterErr) {
				return errors.Join(err, iterErr)
			}
			return err
		}
		if opts.ExplicitFormat {
			if _, err := (outputWriter{ctx: opts.Context, out: opts.Stderr}).WriteString(warningExploreNotSupported); err != nil {
				return err
			}
		}
		opts.Format = "json"
	}

	// The existing pager writes to process stdout. Other injected destinations
	// must stay on their own writer, including error output on stderr.
	stdout, processStdout := opts.Stdout.(*os.File)
	if !processStdout || stdout != os.Stdout {
		for iter.Next() {
			formatted, err := formatJSON(iter.Current().Result, opts)
			if err != nil {
				return errors.Join(err, iter.Err())
			}
			if _, err := (outputWriter{ctx: opts.Context, out: opts.Stdout}).Write(formatted); err != nil {
				return errors.Join(err, iter.Err())
			}
		}
		return iter.Err()
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
		numberOfNewlines += countTerminalLines(formatted, terminalWidth)
		if numberOfNewlines >= terminalHeight-3 {
			usePager = true
			break
		}
	}

	if !usePager {
		return streamToStdout(func(stdout *os.File) error {
			if _, err := (outputWriter{ctx: opts.Context, out: stdout}).Write(output); err != nil {
				return errors.Join(err, iter.Err())
			}
			return iter.Err()
		})
	}

	return streamOutput(opts.Title, func(pager *os.File) error {
		if _, err := (outputWriter{ctx: opts.Context, out: pager}).Write(output); err != nil {
			return errors.Join(err, iter.Err())
		}
		pagerOpts := opts
		pagerOpts.Stdout = pager
		for iter.Next() {
			formatted, err := formatJSONForOutput(iter.Current().Result, pagerOpts, opts.Stdout)
			if err != nil {
				return errors.Join(err, iter.Err())
			}
			if _, err := (outputWriter{ctx: opts.Context, out: pager}).Write(formatted); err != nil {
				return errors.Join(err, iter.Err())
			}
		}
		return iter.Err()
	})
}
