package custom

import (
	"bufio"
	"bytes"
	"context"
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

var OutputFormats = []string{"auto", "explore", "json", "jsonl", "pretty", "raw", "yaml"}

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

func streamToPagerWithPipe(label string, generateOutput func(w *os.File) error) error {
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	defer r.Close()
	defer w.Close()

	pagerProgram := os.Getenv("PAGER")
	if pagerProgram == "" {
		pagerProgram = "less"
	}

	if _, err := exec.LookPath(pagerProgram); err != nil {
		return err
	}

	cmd := exec.Command(pagerProgram)
	cmd.Stdin = r
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		"LESS=-X -r -P "+label,
		"MORE=-r -P "+label,
	)

	if err := cmd.Start(); err != nil {
		return err
	}

	if err := r.Close(); err != nil {
		return err
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
	return waitErr
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
	var outputErr *outputWriteError
	return errors.As(err, &outputErr) && strings.Contains(outputErr.Error(), "broken pipe")
}

// WriteBinaryResponse writes a binary response to stdout or a file.
//
// Takes in a stdout reference so we can test this function without overriding os.Stdout in tests.
func WriteBinaryResponse(response *http.Response, stdout io.Writer, outfile string) (string, error) {
	defer response.Body.Close()

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
	case "auto":
		autoOpts := opts
		autoOpts.Format = "json"
		autoOpts.Transform = ""
		return formatJSONForOutput(res, autoOpts, destination)
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
		_, err := opts.Stdout.Write([]byte(yaml.String()))
		if err != nil {
			return nil, &outputWriteError{err}
		}
		return nil, err
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
	Format         string          // output format (auto, explore, json, jsonl, pretty, raw, yaml)
	RawOutput      bool            // like jq -r: print strings without JSON quotes
	Stderr         io.Writer       // stderr for warnings; injectable for testing; defaults to os.Stderr
	Stdout         *os.File        // stdout (or pager); injectable for testing; defaults to os.Stdout
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
	res, err := transformOutput(opts.Context, res, selectOutputTransformer(opts, selectTransformer))
	if err != nil {
		return err
	}
	res = applyJSONPath(res, opts.Transform)
	opts.Transform = ""

	switch strings.ToLower(opts.Format) {
	case "auto":
		opts.Format = "json"
	case "explore":
		if isTerminal(opts.Stdout) {
			return jsonview.ExploreJSON(opts.Title, res)
		}
		if opts.ExplicitFormat {
			fmt.Fprint(opts.Stderr, warningExploreNotSupported)
		}
		opts.Format = "json"
	}
	formatted, err := formatJSON(res, opts)
	if err != nil {
		return err
	}
	_, err = opts.Stdout.Write(formatted)
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
		remaining: itemsToDisplay,
	}

	if strings.ToLower(opts.Format) == "explore" {
		if isTerminal(opts.Stdout) {
			err := jsonview.ExploreJSONStream(opts.Title, iter)
			return errors.Join(err, iter.Err())
		}
		if opts.ExplicitFormat {
			fmt.Fprint(opts.Stderr, warningExploreNotSupported)
		}
		opts.Format = "json"
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
			return err
		}
		output = append(output, formatted...)
		numberOfNewlines += countTerminalLines(formatted, terminalWidth)
		if numberOfNewlines >= terminalHeight-3 {
			usePager = true
			break
		}
	}

	if !usePager {
		if _, err := opts.Stdout.Write(output); err != nil {
			return err
		}
		return iter.Err()
	}

	return streamOutput(opts.Title, func(pager *os.File) error {
		if _, err := pager.Write(output); err != nil {
			return &outputWriteError{err}
		}
		pagerOpts := opts
		pagerOpts.Stdout = pager
		for iter.Next() {
			formatted, err := formatJSONForOutput(iter.Current().Result, pagerOpts, opts.Stdout)
			if err != nil {
				return err
			}
			if _, err := pager.Write(formatted); err != nil {
				return &outputWriteError{err}
			}
		}
		return iter.Err()
	})
}
