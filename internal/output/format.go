package output

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/colorprofile"
	"github.com/itchyny/json2yaml"
	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-cli/internal/readable"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"github.com/tidwall/gjson"
	"github.com/tidwall/pretty"
)

var Formats = []string{"auto", "text", "explore", "json", "jsonl", "pretty", "raw", "yaml"}

// FormatOptions describes one JSON value after feature-specific transformation.
// Interactive exploration, paging, and output lifecycle remain with the caller.
type FormatOptions struct {
	Format    string
	RawOutput bool
	Title     string
	Transform string
}

// Automatic output is readable on every destination. Extraction keeps its
// existing JSON/raw-string contract, including when stdout is redirected.
func ResolveFormat(opts FormatOptions) string {
	format := strings.ToLower(opts.Format)
	if format == "" || format == "auto" {
		if opts.Transform != "" || opts.RawOutput {
			return "json"
		}
		return "text"
	}
	return format
}

// ApplyJSONPath preserves the original value when the requested path is absent.
func ApplyJSONPath(value gjson.Result, path string) gjson.Result {
	if path != "" {
		if extracted := value.Get(path); extracted.Exists() {
			return extracted
		}
	}
	return value
}

// UseColors applies JSON color policy to the final destination classification.
func UseColors(terminal bool) bool {
	if !terminal || os.Getenv("NO_COLOR") != "" {
		return false
	}
	force, ok := os.LookupEnv("FORCE_COLOR")
	if ok {
		if force == "1" {
			return true
		}
		if force == "0" {
			return false
		}
	}
	return terminal
}

// FormatJSON formats one result. The caller classifies the final destination,
// including feature wrappers and terminals behind pagers; intermediate buffers
// must not change terminal escaping or color policy.
func FormatJSON(res gjson.Result, opts FormatOptions, destination io.Writer, terminal bool) ([]byte, error) {
	opts.Format = ResolveFormat(opts)
	res = ApplyJSONPath(res, opts.Transform)
	// Modeled after `jq -r` (`--raw-output`): if the result is a string, print it without JSON quotes so that
	// it's easier to pipe into other programs.
	if opts.RawOutput && res.Type == gjson.String {
		value := res.Str
		if terminal {
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
		w := &colorprofile.Writer{Forward: &out, Profile: cliterminal.ColorProfile(destination, os.Environ(), terminal)}
		if _, err := w.WriteString(jsonview.RenderJSON(opts.Title, res) + "\n"); err != nil {
			return nil, err
		}
		return out.Bytes(), nil
	case "json":
		prettyJSON := pretty.Pretty([]byte(res.Raw))
		if UseColors(terminal) {
			return pretty.Color(prettyJSON, pretty.TerminalStyle), nil
		} else {
			return prettyJSON, nil
		}
	case "jsonl":
		// @ugly is gjson syntax for "no whitespace", so it fits on one line
		oneLineJSON := res.Get("@ugly").Raw
		if UseColors(terminal) {
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
		return nil, fmt.Errorf("Invalid format: %s, valid formats are: %s", opts.Format, strings.Join(Formats, ", "))
	}
}
