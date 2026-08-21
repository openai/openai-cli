package cmd

import (
	"bufio"
	"bytes"
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
	"github.com/openai/openai-go/v3/option"

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

func getDefaultRequestOptions(cmd *cli.Command) []option.RequestOption {
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

	if err := generateOutput(w); err != nil && !strings.Contains(err.Error(), "broken pipe") {
		return err
	}

	w.Close()
	return cmd.Wait()
}

func streamToStdout(generateOutput func(w *os.File) error) error {
	signal.Ignore(syscall.SIGPIPE)
	err := generateOutput(os.Stdout)
	if err != nil && strings.Contains(err.Error(), "broken pipe") {
		return nil
	}
	return err
}

// writeBinaryResponse writes a binary response to stdout or a file.
//
// Takes in a stdout reference so we can test this function without overriding os.Stdout in tests.
func writeBinaryResponse(response *http.Response, stdout io.Writer, outfile string) (string, error) {
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
		if err := writeExplicitBinaryResponse(response.Body, outfile); err != nil {
			return "", err
		}
		return fmt.Sprintf("Wrote output to: %s", outfile), nil
	}
}

func writeExplicitBinaryResponse(body io.Reader, outfile string) error {
	info, err := os.Stat(outfile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if info != nil && !info.Mode().IsRegular() {
		file, err := os.OpenFile(outfile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			return err
		}
		return copyDownloadFile(file, body)
	}
	staged, err := os.CreateTemp(filepath.Dir(outfile), ".openai-cli-download-*")
	if err != nil {
		staged, err = os.CreateTemp("", ".openai-cli-download-*")
		if err != nil {
			return err
		}
	}
	stagedName := staged.Name()
	defer os.Remove(stagedName)

	if err := copyDownloadFile(staged, body); err != nil {
		return err
	}
	complete, err := os.Open(stagedName)
	if err != nil {
		return err
	}
	defer complete.Close()

	file, err := os.OpenFile(outfile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	return copyDownloadFile(file, complete)
}

// writeAutomaticBinaryResponse preserves full-response UTF-8 detection without
// keeping an arbitrarily large response in memory.
func writeAutomaticBinaryResponse(response *http.Response, stdout io.Writer) (string, error) {
	// Keep enough bytes to identify MIME content and finish a UTF-8 rune that
	// straddles DetectContentType's 512-byte boundary.
	sample := make([]byte, 512+utf8.UTFMax-1)
	n, readErr := io.ReadFull(response.Body, sample)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return "", readErr
	}
	sample = sample[:n]
	complete := errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF)
	sniff := sample
	if !complete && !utf8.Valid(sniff) {
		for trim := 1; trim < utf8.UTFMax && trim <= len(sniff); trim++ {
			boundary := len(sniff) - trim
			if utf8.Valid(sniff[:boundary]) && !utf8.FullRune(sniff[boundary:]) {
				sniff = sniff[:boundary]
				break
			}
		}
	}

	body := io.MultiReader(bytes.NewReader(sample), response.Body)
	if !isUTF8TextFile(sniff) {
		return writeAutomaticDownload(response, sample, body)
	}
	if complete {
		_, err := io.Copy(stdout, bytes.NewReader(sample))
		return "", err
	}

	staged, err := os.CreateTemp(".", ".openai-cli-download-*")
	if err != nil {
		staged, err = os.CreateTemp("", ".openai-cli-download-*")
		if err != nil {
			return "", err
		}
	}
	stagedName := staged.Name()
	defer os.Remove(stagedName)

	if err := copyDownloadFile(staged, body); err != nil {
		return "", err
	}
	sample, text, err := inspectAutomaticBinaryResponse(stagedName, stdout)
	if err != nil {
		return "", err
	}
	if text {
		return "", nil
	}

	completeBody, err := os.Open(stagedName)
	if err != nil {
		return "", err
	}
	defer completeBody.Close()
	return writeAutomaticDownload(response, sample, completeBody)
}

func writeAutomaticDownload(response *http.Response, sample []byte, body io.Reader) (string, error) {
	file, err := createDownloadFile(response, sample)
	if err != nil {
		return "", err
	}
	filename := file.Name()
	if err := copyDownloadFile(file, body); err != nil {
		os.Remove(filename)
		return "", err
	}
	return fmt.Sprintf("Wrote output to: %s", filename), nil
}

func inspectAutomaticBinaryResponse(filename string, stdout io.Writer) ([]byte, bool, error) {
	spool, err := os.Open(filename)
	if err != nil {
		return nil, false, err
	}
	defer spool.Close()

	// DetectContentType examines at most 512 bytes. Keep enough additional
	// bytes to complete a UTF-8 rune that straddles that boundary.
	sample := make([]byte, 512+utf8.UTFMax-1)
	n, err := io.ReadFull(spool, sample)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, false, err
	}
	sample = sample[:n]
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return nil, false, err
	}

	validUTF8 := true
	reader := bufio.NewReader(spool)
	for {
		r, size, err := reader.ReadRune()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, false, err
		}
		if r == utf8.RuneError && size == 1 {
			validUTF8 = false
			break
		}
	}

	if validUTF8 {
		for !utf8.Valid(sample) {
			sample = sample[:len(sample)-1]
		}
	} else if utf8.Valid(sample) {
		// Preserve .bin fallback when invalid UTF-8 appears after the MIME sample.
		sample = append(sample, 0xff)
	}

	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return nil, false, err
	}
	if validUTF8 && isUTF8TextFile(sample) {
		_, err := io.Copy(stdout, spool)
		return sample, true, err
	}
	return sample, false, nil
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

func formatJSON(res gjson.Result, opts ShowJSONOpts) ([]byte, error) {
	return formatJSONForOutput(res, opts, opts.Stdout)
}

// formatJSONForOutput keeps the final destination available when a pager sits between
// formatted output and the terminal.
func formatJSONForOutput(res gjson.Result, opts ShowJSONOpts, destination io.Writer) ([]byte, error) {
	if opts.Transform != "" {
		transformed := res.Get(opts.Transform)
		if transformed.Exists() {
			res = transformed
		}
	}
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
		return []byte(jsonview.RenderJSON(opts.Title, res) + "\n"), nil
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
		return nil, err
	default:
		return nil, fmt.Errorf("Invalid format: %s, valid formats are: %s", opts.Format, strings.Join(OutputFormats, ", "))
	}
}

const warningExploreNotSupported = "Warning: Output format 'explore' not supported for non-terminal output; falling back to 'json'\n"

// ShowJSONOpts configures how JSON output is displayed.
type ShowJSONOpts struct {
	ExplicitFormat bool      // true if the user explicitly passed --format
	Format         string    // output format (auto, explore, json, jsonl, pretty, raw, yaml)
	RawOutput      bool      // like jq -r: print strings without JSON quotes
	Stderr         io.Writer // stderr for warnings; injectable for testing; defaults to os.Stderr
	Stdout         *os.File  // stdout (or pager); injectable for testing; defaults to os.Stdout
	Title          string    // display title
	Transform      string    // GJSON path to extract before displaying
}

func (o *ShowJSONOpts) setDefaults() {
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
}

// ShowJSON displays a single JSON result to the user.
func ShowJSON(res gjson.Result, opts ShowJSONOpts) error {
	opts.setDefaults()

	switch strings.ToLower(opts.Format) {
	case "auto":
		autoOpts := opts
		autoOpts.Format = "json"
		return ShowJSON(res, autoOpts)
	case "explore":
		if !isTerminal(opts.Stdout) {
			if opts.ExplicitFormat {
				fmt.Fprint(opts.Stderr, warningExploreNotSupported)
			}
			jsonOpts := opts
			jsonOpts.Format = "json"
			return ShowJSON(res, jsonOpts)
		}
		if opts.Transform != "" {
			transformed := res.Get(opts.Transform)
			if transformed.Exists() {
				res = transformed
			}
		}
		return jsonview.ExploreJSON(opts.Title, res)
	default:
		bytes, err := formatJSON(res, opts)
		if err != nil {
			return err
		}

		_, err = opts.Stdout.Write(bytes)
		return err
	}
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
	opts.setDefaults()

	if opts.Format == "explore" {
		if isTerminal(opts.Stdout) {
			return jsonview.ExploreJSONStream(opts.Title, iter)
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

	// Decide whether or not to use a pager based on whether it's a short output or a long output
	usePager := false
	output := []byte{}
	numberOfNewlines := 0
	// -1 is used to signal no limit of items to display
	for itemsToDisplay != 0 && iter.Next() {
		item := iter.Current()
		var obj gjson.Result
		if hasRaw, ok := any(item).(hasRawJSON); ok {
			obj = gjson.Parse(hasRaw.RawJSON())
		} else {
			jsonData, err := json.Marshal(item)
			if err != nil {
				return err
			}
			obj = gjson.ParseBytes(jsonData)
		}
		json, err := formatJSON(obj, opts)
		if err != nil {
			return err
		}

		output = append(output, json...)
		itemsToDisplay -= 1
		numberOfNewlines += countTerminalLines(json, terminalWidth)

		// If the output won't fit in the terminal window, stream it to a pager
		if numberOfNewlines >= terminalHeight-3 {
			usePager = true
			break
		}
	}

	if !usePager {
		_, err := opts.Stdout.Write(output)
		if err != nil {
			return err
		}

		return iter.Err()
	}

	return streamOutput(opts.Title, func(pager *os.File) error {
		_, err := pager.Write(output)
		if err != nil {
			return err
		}

		pagerOpts := opts
		pagerOpts.Stdout = pager

		for iter.Next() {
			if itemsToDisplay == 0 {
				break
			}
			item := iter.Current()
			var obj gjson.Result
			if hasRaw, ok := any(item).(hasRawJSON); ok {
				obj = gjson.Parse(hasRaw.RawJSON())
			} else {
				jsonData, err := json.Marshal(item)
				if err != nil {
					return err
				}
				obj = gjson.ParseBytes(jsonData)
			}
			formatted, err := formatJSONForOutput(obj, pagerOpts, opts.Stdout)
			if err != nil {
				return err
			}
			if _, err := pager.Write(formatted); err != nil {
				return err
			}
			itemsToDisplay -= 1
		}
		return iter.Err()
	})
}
