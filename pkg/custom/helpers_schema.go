package custom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func registerSchemaHelperCommands(root *cli.Command) {
	help := `{{$bin := or (index .Root.Metadata "help-invocation") "openai"}}EXAMPLE
  {{$bin}} helpers schema --model gpt-4.1-mini-2025-04-14 --description "An invoice with line items" --output invoice.schema.json

One paid Responses request, without retries. Results vary between runs.
Compiles JSON Schema locally; does not verify Structured Outputs compatibility.

` + cli.CommandHelpTemplate
	root.Commands = append(root.Commands, &cli.Command{
		Name: "helpers", Usage: "Generate and validate development artifacts", HideHelpCommand: true,
		Commands: []*cli.Command{{
			Name: "schema", Usage: "Generate a JSON Schema and save it after local compilation", HideHelpCommand: true,
			CustomHelpTemplate: help,
			Description: "Uses Responses JSON mode with store=false. Only completed, non-refused responses can produce a file. " +
				"Compilation checks JSON Schema Draft 2020-12 with local references only. " +
				"Structured Outputs uses a narrower subset; local compilation does not prove API acceptance. " +
				"Output formats and extraction apply to the save receipt, not the schema file.",
			Flags: []cli.Flag{
				&cli.StringFlag{Name: "description", Usage: "Describe the schema with literal `TEXT`; @ stays literal. Stdin bodies are unsupported", Required: true, OnlyOnce: true},
				&cli.StringFlag{Name: "model", Usage: "Use a Responses JSON-mode `MODEL`; required, with no default. API token charges apply", Required: true, OnlyOnce: true},
				&cli.StringFlag{Name: "output", Usage: "Save to a new `PATH`; existing files are never replaced. Parent directory must exist; - is unsupported", Required: true, OnlyOnce: true, TakesFile: true},
				&cli.Int64Flag{Name: "max-output-tokens", Usage: "Request at most `TOKENS`, including reasoning tokens. Incomplete output is not saved", Value: 8192, OnlyOnce: true},
			},
			Action: handleSchemaHelper,
		}},
	})
}

// Only fixed local prose enters this error. Preserve causes for API identity and cancellation.
type schemaHelperError struct {
	message string
	cause   error
	saved   bool
}

func (e *schemaHelperError) Error() string { return e.message }
func (e *schemaHelperError) Unwrap() error { return e.cause }
func (e *schemaHelperError) ExitCode() int {
	var signal *downloadSignalError
	if errors.As(e.cause, &signal) {
		return signal.exitCode
	}
	if errors.Is(e.cause, context.Canceled) {
		return 130
	}
	return 1
}

const schemaHelperInstructions = `Generate one JSON Schema Draft 2020-12 object for the user's description.
Return only the schema as JSON, without markdown fences or a name/strict/schema envelope.
Use descriptive property names and descriptions. Use local fragment references only, if needed.
Use a root object, require every property, and set additionalProperties to false on each object.
Represent optional values with a null union. Avoid unsupported Structured Outputs features such as allOf, not, if, then, and else.
Do not claim that the schema has been validated or accepted by an API.`

func handleSchemaHelper(ctx context.Context, command *cli.Command) (err error) {
	parentContext := ctx
	if command.Args().Present() || strings.TrimSpace(command.String("description")) == "" || strings.TrimSpace(command.String("model")) == "" {
		return &schemaHelperError{message: "Schema generation needs --description and --model, without positional arguments. Check helpers schema --help."}
	}
	if command.Int64("max-output-tokens") <= 0 {
		return &schemaHelperError{message: "Schema generation needs a positive --max-output-tokens value. No request was sent."}
	}
	if isInputPiped() {
		return &schemaHelperError{message: "Schema generation does not accept stdin bodies. Pass literal text with --description. No request was sent."}
	}
	options, err := FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, EmptyBody, true)
	if err != nil {
		return err
	}
	root := command.Root()
	// No file or signal handler exists yet. Kernel signal handling can stop a
	// blocked diagnostic write without leaving staging files or a request.
	if outputDiagnosticsAllowed(ctx) && errorOutputFormat(root) == "text" && root.String("transform-error") == "" {
		if err := readable.WriteText(os.Stderr, fmt.Sprintf("Model: %s. Generation uses one paid Responses request; no retries. Results vary.", command.String("model"))); err != nil {
			return &schemaHelperError{message: "Could not display generation details. No request was sent. Check stderr and try again.", cause: err}
		}
	}
	// Reuse the existing signal lifecycle, including SIGTERM and cleanup joining.
	ctx, stop := downloadSignalContext(ctx)
	saved := false
	defer func() {
		if stop != nil {
			stop()
		}
	}()
	defer func() {
		if err != nil && stop != nil && ctx.Err() != nil {
			err = &schemaHelperError{message: "Schema generation canceled. Check the destination before repeating a paid request.", cause: errors.Join(err, downloadContextError(ctx)), saved: saved}
		}
	}()
	artifact, err := prepareSchemaArtifact(command.String("output"))
	if err != nil {
		return err
	}
	cleaned := false
	defer func() {
		if !cleaned {
			if cleanupErr := artifact.cleanup(); cleanupErr != nil {
				err = &schemaHelperCleanupError{operation: err, cleanup: cleanupErr}
			}
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	client := openai.NewClient(GetDefaultRequestOptions(command)...)
	options = append(options, option.WithMaxRetries(0))
	response, err := client.Responses.New(ctx, responses.ResponseNewParams{
		Model:        shared.ResponsesModel(command.String("model")),
		Input:        responses.ResponseNewParamsInputUnion{OfString: openai.String(command.String("description"))},
		Instructions: openai.String(schemaHelperInstructions), Store: openai.Bool(false),
		MaxOutputTokens: openai.Int(command.Int64("max-output-tokens")),
		Text:            responses.ResponseTextConfigParam{Format: responses.ResponseFormatTextConfigUnionParam{OfJSONObject: &shared.ResponseFormatJSONObjectParam{}}},
	}, options...)
	if err != nil {
		return err
	}
	data, err := schemaHelperResponse(response)
	if err != nil {
		return err
	}
	if err := compileSchemaArtifact(ctx, data); err != nil {
		if errors.Is(err, errHelperSchemaInvalid) {
			return &schemaHelperError{message: "Generated schema failed local Draft 2020-12 compilation. No file was saved. Revise --description before another paid request.", cause: err}
		}
		return err
	}
	publishErr := artifact.publish(ctx, data)
	saved = artifact.published
	if publishErr != nil {
		return publishErr
	}
	cleanupErr := artifact.cleanup()
	cleaned = true
	if cleanupErr != nil {
		return cleanupErr
	}
	// All owned resources are closed before receipt output. Restore ordinary
	// signals so a blocked stdout cannot trap cancellation after a successful save.
	stop()
	stop = nil
	var interrupted *downloadSignalError
	if errors.As(context.Cause(ctx), &interrupted) || parentContext.Err() != nil {
		return &schemaHelperError{message: "Schema saved, but receipt output was canceled. Check --output before repeating generation.", cause: errors.Join(context.Cause(ctx), parentContext.Err()), saved: true}
	}
	receipt, _ := json.Marshal(struct {
		Saved                          string `json:"saved"`
		JSONSchemaCompilation          string `json:"json_schema_compilation"`
		StructuredOutputsCompatibility string `json:"structured_outputs_compatibility"`
		Model                          string `json:"model"`
		Requests                       int    `json:"requests"`
	}{command.String("output"), "passed (Draft 2020-12)", "not checked", command.String("model"), 1})
	if err := ShowJSON(gjson.ParseBytes(receipt), ShowJSONOpts{
		Context: parentContext, Format: root.String("format"), ExplicitFormat: root.IsSet("format"),
		Transform: root.String("transform"), RawOutput: root.Bool("raw-output"),
	}); err != nil {
		return &schemaHelperError{message: "Schema saved, but its receipt could not be displayed. Check --output; do not repeat generation to recover the receipt.", cause: err, saved: true}
	}
	return nil
}

func schemaHelperResponse(response *responses.Response) ([]byte, error) {
	if response == nil || response.Status != responses.ResponseStatusCompleted {
		return nil, &schemaHelperError{message: "Schema generation did not complete. No file was saved. Check model access and --max-output-tokens before another paid request."}
	}
	var text strings.Builder
	for _, item := range response.Output {
		switch item.Type {
		case "reasoning":
			continue
		case "message":
			if item.Status != "completed" || item.Role != "assistant" {
				return nil, &schemaHelperError{message: "Schema generation returned an incomplete message. No file was saved. Review --max-output-tokens before another paid request."}
			}
		default:
			return nil, &schemaHelperError{message: "Schema generation returned an unsupported output type. No file was saved. Use a model with Responses JSON-mode support."}
		}
		for _, part := range item.Content {
			switch part.Type {
			case "refusal":
				return nil, &schemaHelperError{message: "The model refused schema generation. No file was saved. Revise --description before another paid request."}
			case "output_text":
				text.WriteString(part.Text)
			default:
				return nil, &schemaHelperError{message: "Schema generation returned unsupported content. No file was saved. Use a model with Responses JSON-mode support."}
			}
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return nil, &schemaHelperError{message: "Schema generation returned no schema. No file was saved. Review --description before another paid request."}
	}
	return []byte(text.String()), nil
}
