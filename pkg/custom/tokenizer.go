package custom

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/openai/openai-cli/internal/tokenizer"
	"github.com/urfave/cli/v3"
)

func registerTokenizerCommands(root *cli.Command) {
	help := `{{$bin := or (index .Root.Metadata "help-invocation") "openai"}}EXAMPLE
  {{$bin}} tokenizer

Opens the live editor in a terminal. Use count or inspect for scripts.

` + cli.SubcommandHelpTemplate + localUtilityGlobalHelp
	root.Commands = append(root.Commands, &cli.Command{
		Name: "tokenizer", Usage: "Explore text, token IDs, and bytes locally",
		Description: "Tokenizes exact UTF-8 text without credentials, network access, or saved input. " +
			"Plain-text counts are not complete request counts, billed usage, prices, or context-limit guarantees. " +
			"Use responses input-tokens count for server request counting.",
		HideHelpCommand:    true,
		CustomHelpTemplate: help,
		Metadata:           map[string]any{localUtilityMetadata: true, "local-help-full": help, "help-command-section": "Local tools"},
		Action:             handleTokenizerEditor,
		Commands: []*cli.Command{
			tokenizerInputCommand("count", false), tokenizerInputCommand("inspect", true),
			{Name: "encodings", Usage: "List the embedded tokenizer encodings", HideHelpCommand: true, CustomHelpTemplate: cli.CommandHelpTemplate + localUtilityGlobalHelp, Action: handleTokenizerEncodings},
			{Name: "licenses", Usage: "Print the bundled tokenizer license notices", HideHelpCommand: true, CustomHelpTemplate: cli.CommandHelpTemplate + localUtilityGlobalHelp, Action: handleTokenizerLicenses},
			tokenizerPreviewCommand(),
			tokenizerTerminalOutputCommand(),
		},
	})
}

func tokenizerInputCommand(name string, inspect bool) *cli.Command {
	usage := "Count plain-text tokens locally"
	if inspect {
		usage = "Inspect token IDs and exact byte boundaries locally"
	}
	help := `{{$bin := or (index .Root.Metadata "help-invocation") "openai"}}EXAMPLE
  {{$bin}} tokenizer {{.Name}} --text "Hello, world!"

` + cli.CommandHelpTemplate + localUtilityGlobalHelp
	return &cli.Command{
		Name: name, Usage: usage, HideHelpCommand: true,
		CustomHelpTemplate: help,
		Metadata:           map[string]any{"local-help-full": help},
		Description: "Choose --text or --file, or pipe UTF-8 text through stdin. " +
			"Explicit input leaves piped stdin unread. --text and --file cannot be combined. " +
			"Input is limited to 1 MiB. Long unbroken text can take minutes; Ctrl+C stops the command. " +
			"Whitespace, BOM, final newlines, and Unicode normalization are preserved. " +
			"Special-token spellings are ordinary text. Invalid UTF-8 is rejected. " +
			"Supports --format auto, text, or json. Inspect always includes lossless hexadecimal token bytes. " +
			"Plain-text counts exclude request structure and multimodal inputs.",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "text", Usage: "Use literal UTF-8 `TEXT`, including an empty string; @ stays literal", OnlyOnce: true},
			&cli.StringFlag{Name: "file", Usage: "Read UTF-8 text from `PATH`; use - for stdin through EOF", TakesFile: true, OnlyOnce: true},
			&cli.StringFlag{Name: "encoding", Usage: "Use `ENCODING`: " + strings.Join(tokenizer.SupportedEncodings(), ", ") + "; no model mapping", Value: tokenizer.DefaultEncoding, OnlyOnce: true},
		},
		Action: func(ctx context.Context, command *cli.Command) error {
			return handleTokenizer(ctx, command, inspect)
		},
	}
}

func tokenizerOutputFormat(command *cli.Command) (string, error) {
	if command.Args().Present() {
		return "", &localUtilityError{message: "Tokenizer commands take no positional arguments. Use --text or --file for input."}
	}
	root := command.Root()
	if root.IsSet("transform") || root.IsSet("raw-output") {
		return "", &localUtilityError{message: "Tokenizer output does not support --transform or --raw-output. Use --format text or json."}
	}
	format := strings.ToLower(root.String("format"))
	switch format {
	case "auto", "text", "json":
		return format, nil
	default:
		return "", &localUtilityError{message: "Tokenizer output supports --format auto, text, or json."}
	}
}

func handleTokenizer(ctx context.Context, command *cli.Command, inspect bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	format, err := tokenizerOutputFormat(command)
	if err != nil {
		return err
	}
	encoding := command.String("encoding")
	if !tokenizer.IsSupportedEncoding(encoding) {
		return &localUtilityError{message: "Choose a tokenizer encoding: " + strings.Join(tokenizer.SupportedEncodings(), ", ") + ". Model names are not encodings."}
	}
	text, err := readTokenizerInput(ctx, command)
	if err != nil {
		return err
	}
	// The library runs synchronously. The CLI retains normal operating-system
	// SIGINT termination, including during library computation and blocked input.
	result, err := tokenizer.Encode(text, encoding, inspect)
	if err != nil {
		return &localUtilityError{message: "Could not tokenize input. Use valid UTF-8 text of at most 1 MiB.", cause: err}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return tokenizerOutputFailure(writeTokenizerResult(ctx, command.Root().Writer, result, inspect, format == "json"))
}

func tokenizerOutputFailure(err error) error {
	if err == nil {
		return nil
	}
	return &localUtilityError{
		message: "Could not write tokenizer output. Output may be incomplete. Check the output file or pipe before rerunning.",
		cause:   err,
	}
}

func readTokenizerInput(ctx context.Context, command *cli.Command) (string, error) {
	if command.IsSet("text") && command.IsSet("file") {
		return "", &localUtilityError{message: "Choose one tokenizer input: --text or --file. Piped stdin is used only without either option."}
	}
	if command.IsSet("text") {
		return validateTokenizerInput(command.String("text"))
	}
	input := command.Root().Reader
	if input == nil {
		input = os.Stdin
	}
	if command.IsSet("file") {
		path := command.String("file")
		if path == "" {
			return "", &localUtilityError{message: "--file requires a path. Use --file - to read stdin."}
		}
		if path != "-" {
			file, err := os.Open(path)
			if err != nil {
				return "", &localUtilityError{message: "Could not open tokenizer input. Check the --file path and its permissions.", cause: err}
			}
			defer file.Close()
			input = file
		}
	} else if file, ok := input.(*os.File); ok && isTerminal(file) {
		return "", &localUtilityError{message: "Provide --text or --file, or pipe UTF-8 text into the tokenizer. Use --file - for interactive stdin."}
	}
	data, err := io.ReadAll(io.LimitReader(&tokenizerInputReader{ctx: ctx, input: input}, tokenizer.MaxInputBytes+1))
	if err != nil {
		return "", &localUtilityError{message: "Could not read tokenizer input. Check --file or standard input.", cause: err}
	}
	return validateTokenizerInput(string(data))
}

type tokenizerInputReader struct {
	ctx   context.Context
	input io.Reader
}

func (r *tokenizerInputReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.input.Read(data)
}

func validateTokenizerInput(text string) (string, error) {
	if len(text) > tokenizer.MaxInputBytes {
		return "", &localUtilityError{message: "Tokenizer input exceeds 1 MiB. Use a smaller complete input; splitting text can change token boundaries."}
	}
	if !utf8.ValidString(text) {
		return "", &localUtilityError{message: "Tokenizer input must be valid UTF-8. Convert the source encoding before tokenizing."}
	}
	return text, nil
}

type tokenizerToken struct {
	ID        uint    `json:"id"`
	StartByte int     `json:"start_byte"`
	EndByte   int     `json:"end_byte"`
	BytesHex  string  `json:"bytes_hex"`
	Text      *string `json:"text,omitempty"`
}

func writeTokenizerResult(ctx context.Context, writer io.Writer, result tokenizer.Result, inspect, asJSON bool) error {
	out := bufio.NewWriterSize(outputWriter{ctx: ctx, out: writer}, 32*1024)
	if asJSON {
		if _, err := fmt.Fprintf(out, `{"encoding":%q,"input_bytes":%d,"token_count":%d`, result.Encoding, result.InputBytes, result.TokenCount); err != nil {
			return err
		}
		if inspect {
			if _, err := io.WriteString(out, `,"tokens":[`); err != nil {
				return err
			}
		}
	} else if _, err := fmt.Fprintf(out, "Encoding: %s\nInput bytes: %d\nTokens: %d\n", result.Encoding, result.InputBytes, result.TokenCount); err != nil {
		return err
	}
	offset := 0
	for i, fragment := range result.Fragments {
		if err := ctx.Err(); err != nil {
			return err
		}
		token := tokenizerToken{ID: result.IDs[i], StartByte: offset, EndByte: offset + len(fragment), BytesHex: hex.EncodeToString([]byte(fragment))}
		if utf8.ValidString(fragment) {
			token.Text = &fragment
		}
		if asJSON {
			if i > 0 {
				if err := out.WriteByte(','); err != nil {
					return err
				}
			}
			data, err := json.Marshal(token)
			if err != nil {
				return err
			}
			if _, err := out.Write(data); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintf(out, "\n%d  ID %d  [%d, %d)\n", i, token.ID, token.StartByte, token.EndByte); err != nil {
				return err
			}
			if token.Text != nil {
				if _, err := fmt.Fprintf(out, "   %s\n", strconv.QuoteToGraphic(*token.Text)); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintf(out, "   hex: %s\n", token.BytesHex); err != nil {
				return err
			}
		}
		offset = token.EndByte
	}
	if asJSON {
		if inspect {
			if err := out.WriteByte(']'); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(out, "}\n"); err != nil {
			return err
		}
	}
	return out.Flush()
}

func handleTokenizerEncodings(ctx context.Context, command *cli.Command) error {
	format, err := tokenizerOutputFormat(command)
	if err != nil {
		return err
	}
	encodings := tokenizer.SupportedEncodings()
	var lines []string
	for _, name := range encodings {
		label := name
		if name == tokenizer.DefaultEncoding {
			label += " (default)"
		} else if name == "r50k_base" || name == "p50k_base" {
			label += " (legacy)"
		}
		lines = append(lines, label)
	}
	text := strings.Join(lines, "\n") + "\n"
	if format == "json" {
		data, err := json.Marshal(struct {
			DefaultEncoding string   `json:"default_encoding"`
			Encodings       []string `json:"encodings"`
		}{tokenizer.DefaultEncoding, encodings})
		if err != nil {
			return err
		}
		text = string(data) + "\n"
	}
	_, err = io.WriteString(outputWriter{ctx: ctx, out: command.Root().Writer}, text)
	return tokenizerOutputFailure(err)
}

func handleTokenizerLicenses(ctx context.Context, command *cli.Command) error {
	format, err := tokenizerOutputFormat(command)
	if err != nil {
		return err
	}
	text := tokenizer.Licenses
	if format == "json" {
		data, err := json.Marshal(struct {
			Licenses string `json:"licenses"`
		}{text})
		if err != nil {
			return err
		}
		text = string(data) + "\n"
	}
	_, err = io.WriteString(outputWriter{ctx: ctx, out: command.Root().Writer}, text)
	return tokenizerOutputFailure(err)
}
