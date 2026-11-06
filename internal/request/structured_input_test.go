package request

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func parseStructuredTestJSON(text string) (any, error) {
	var value any
	err := json.Unmarshal([]byte(text), &value)
	return value, err
}

func TestStructuredInputSourcesResolveOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.json")
	payload := `{"literal":"@-","escaped":"\\@-","nested":["@private/missing.json",null,false,2]}`
	require.NoError(t, os.WriteFile(path, []byte(payload), 0600))
	want, err := parseStructuredTestJSON(payload)
	require.NoError(t, err)

	for styleName, style := range map[string]FileEmbedStyle{"text": EmbedText, "multipart": EmbedIOReader} {
		for _, source := range []struct{ name, raw string }{
			{"file", "@" + path}, {"file URI", "@file://" + path}, {"stdin", "@-"},
			{"stdin URI", "@file://-"}, {"stdin alias", "@/dev/stdin"},
		} {
			t.Run(styleName+"/"+source.name, func(t *testing.T) {
				calls := 0
				input := requestflag.InputValue{Raw: source.raw, Parse: func(text string) (any, error) {
					calls++
					require.Equal(t, payload, text)
					return parseStructuredTestJSON(text)
				}}
				stdin := &OnceStdinReader{Reader: strings.NewReader(payload)}
				got, err := EmbedFiles(input, style, stdin)
				require.NoError(t, err)
				require.Equal(t, want, got)
				require.Equal(t, 1, calls)
			})
		}
	}
}

func TestStructuredInputSourceGrammarPrecedesParsing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.txt")
	require.NoError(t, os.WriteFile(path, []byte("synthetic text"), 0600))
	for _, test := range []struct{ name, raw, want string }{
		{"base64 file", "@data://" + path, base64.StdEncoding.EncodeToString([]byte("synthetic text"))},
		{"base64 stdin", "@data://-", base64.StdEncoding.EncodeToString([]byte("synthetic text"))},
		{"escaped at", `\@private/missing.txt`, "@private/missing.txt"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := requestflag.InputValue{Raw: test.raw, Parse: func(text string) (any, error) {
				require.Equal(t, test.want, text)
				return text, nil
			}}
			got, err := EmbedFiles(input, EmbedIOReader, &OnceStdinReader{Reader: strings.NewReader("synthetic text")})
			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
}

func TestStructuredInputProtectsParsedValuesAndResolvesNestedFlags(t *testing.T) {
	inner := requestflag.InputValue{Raw: "@-", Parse: parseStructuredTestJSON}
	value := map[string]any{
		"literal": "@private/missing.json", "escaped": `\@-`, "stdin": "@-",
		"nested": []any{inner, requestflag.InputValue{Value: nil}},
	}
	input := requestflag.InputValue{Value: value}
	count, err := StdinConsumers(requestflag.RequestContents{Body: input}, false)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	got, err := EmbedFiles(input, EmbedText, &OnceStdinReader{Reader: strings.NewReader(`"@-"`)})
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"literal": "@private/missing.json", "escaped": `\@-`, "stdin": "@-",
		"nested": []any{"@-", nil},
	}, got)
	_, stillWrapped := value["nested"].([]any)[0].(requestflag.InputValue)
	require.True(t, stillWrapped, "request resolution must not mutate flag values")
}

func TestStructuredInputPreservesExplicitNestedSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "child.txt")
	require.NoError(t, os.WriteFile(path, []byte("synthetic child text"), 0600))
	for styleName, style := range map[string]FileEmbedStyle{"text": EmbedText, "multipart": EmbedIOReader} {
		for _, source := range []struct {
			name, value string
			consumers   int
		}{
			{"file", "@" + path, 0}, {"stdin", "@-", 1},
		} {
			t.Run(styleName+"/"+source.name, func(t *testing.T) {
				input := requestflag.InputValue{Value: map[string]any{
					"literal": "@private/missing.json", "stdin_data": "@-",
					"content": requestflag.InputValue{Value: source.value, Option: "--entry.content", ResolveSources: true},
				}}
				count, err := StdinConsumers(requestflag.RequestContents{Body: input}, false)
				require.NoError(t, err)
				require.Equal(t, source.consumers, count)
				got, err := EmbedFiles(input, style, &OnceStdinReader{Reader: strings.NewReader("synthetic child text")})
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, CloseFileUploads(got)) })
				body := got.(map[string]any)
				require.Equal(t, "@private/missing.json", body["literal"])
				require.Equal(t, "@-", body["stdin_data"])
				if style == EmbedText {
					require.Equal(t, "synthetic child text", body["content"])
				} else {
					reader, ok := body["content"].(io.Reader)
					require.True(t, ok)
					content, err := io.ReadAll(reader)
					require.NoError(t, err)
					require.Equal(t, "synthetic child text", string(content))
				}
			})
		}
	}
}

func TestStructuredInputNestedSourcesPreserveLegacyAndUntrustedValues(t *testing.T) {
	input := requestflag.InputValue{Value: requestflag.InputValue{ResolveSources: true, Value: map[string]any{
		"source": "@-", "escaped": `\@-`, "untrusted": UntrustedStdinValue("@private/missing.json"),
		"untrusted_stdin": UntrustedStdinValue("@-"), "structured": requestflag.InputValue{Value: "@-"},
	}}}
	count, err := StdinConsumers(requestflag.RequestContents{Body: input}, false)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	got, err := EmbedFiles(input, EmbedText, &OnceStdinReader{Reader: strings.NewReader("synthetic text")})
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"source": "synthetic text", "escaped": "@-", "untrusted": "@private/missing.json",
		"untrusted_stdin": "@-", "structured": "@-",
	}, got)
}

func TestStructuredInputExplicitNestedSourceAliases(t *testing.T) {
	t.Setenv(UntrustedStdinEnv, "true")
	input := &requestflag.Flag[any]{Name: "entry", BodyPath: "entry", Const: true, Default: requestflag.InputValue{
		Value: map[string]any{
			"literal": "@private/missing.json",
			"alias":   requestflag.InputValue{Value: "@-", Option: "--entry.content", ResolveSources: true},
		},
	}}
	inner := &requestflag.InnerFlag[any]{Name: "entry.content", OuterFlag: input, InnerField: "content", DataAliases: []string{"alias"}}
	var observed []byte
	_, err := FlagOptions(&cli.Command{Name: "create", Flags: []cli.Flag{input, inner}},
		apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatRepeat, ApplicationJSON, false,
		strings.NewReader("synthetic child text"), PreparationHooks{
			Encoding: EncodingHooks{ObserveJSON: []func([]byte){func(body []byte) { observed = body }}},
		})
	require.NoError(t, err)
	require.JSONEq(t, `{"entry":{"literal":"@private/missing.json","content":"synthetic child text"}}`, string(observed))
}

func TestStructuredInputCompositionPreservesSourceOrder(t *testing.T) {
	var order []string
	previous := requestflag.InputValue{Raw: `\@first`, Parse: func(text string) (any, error) {
		order = append(order, "previous")
		return []any{text}, nil
	}}
	input := requestflag.InputValue{
		Previous: &previous, Raw: `\@second`,
		Parse: func(text string) (any, error) {
			order = append(order, "current")
			return text, nil
		},
		Combine: func(previous, current any) (any, error) {
			order = append(order, "combine")
			return append(previous.([]any), current), nil
		},
	}
	got, err := EmbedFiles(input, EmbedText, nil)
	require.NoError(t, err)
	require.Equal(t, []any{"@first", "@second"}, got)
	require.Equal(t, []string{"previous", "current", "combine"}, order)
}

func TestStructuredInputCountsOnlyExplicitStdinSources(t *testing.T) {
	deferred := requestflag.InputValue{Raw: "@-", Parse: parseStructuredTestJSON}
	for _, test := range []struct {
		name  string
		input requestflag.InputValue
		count int
	}{
		{"parsed string", requestflag.InputValue{Value: "@-"}, 0},
		{"parsed array", requestflag.InputValue{Value: []any{"@-", "@file://-"}}, 0},
		{"deferred", deferred, 1},
		{"escaped", requestflag.InputValue{Raw: `\@-`, Parse: parseStructuredTestJSON}, 0},
		{"previous", requestflag.InputValue{Previous: &deferred, Value: "@-"}, 1},
		{"duplicate", requestflag.InputValue{Previous: &deferred, Raw: "@file://-", Parse: parseStructuredTestJSON}, 2},
		{"nested", requestflag.InputValue{Value: []any{deferred, deferred}}, 2},
		{"ordinary nested", requestflag.InputValue{Value: []any{
			requestflag.InputValue{Value: "@-", ResolveSources: true},
			requestflag.InputValue{Value: "@file://-", ResolveSources: true},
		}}, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			count, err := StdinConsumers(requestflag.RequestContents{Body: test.input}, false)
			require.Equal(t, test.count, count)
			require.Equal(t, test.count > 1, err != nil)
		})
	}
}

func TestStructuredInputSourceFailurePreservesOption(t *testing.T) {
	input := requestflag.InputValue{
		Option: "--input-structured", Raw: "@" + filepath.Join(t.TempDir(), "missing.json"),
		Parse: func(string) (any, error) { t.Fatal("parse ran after a source failure"); return nil, nil },
	}
	_, err := EmbedFiles(input, EmbedText, nil)
	require.ErrorIs(t, err, os.ErrNotExist)
	var failure *InputFileError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "--input-structured", failure.Option)
}

func TestStructuredInputPreparationReservesStdinAndResolvesAliases(t *testing.T) {
	t.Setenv(UntrustedStdinEnv, "true")
	input := &requestflag.Flag[any]{Name: "input", BodyPath: "input", Const: true, Default: requestflag.InputValue{
		Raw: "@-", Parse: parseStructuredTestJSON,
	}}
	inner := &requestflag.InnerFlag[any]{Name: "input.value", OuterFlag: input, InnerField: "value", DataAliases: []string{"alias"}}
	command := &cli.Command{Name: "create", Flags: []cli.Flag{input, inner}}
	var observed []byte
	_, err := FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatRepeat,
		ApplicationJSON, false, strings.NewReader(`{"alias":"@private/missing.json"}`), PreparationHooks{
			ValidateResolvedInputs: func(contents requestflag.RequestContents) error {
				require.Equal(t, map[string]any{"input": map[string]any{"value": "@private/missing.json"}}, contents.Body)
				return nil
			},
			Encoding: EncodingHooks{ObserveJSON: []func([]byte){func(body []byte) { observed = body }}},
		})
	require.NoError(t, err)
	require.JSONEq(t, `{"input":{"value":"@private/missing.json"}}`, string(observed))
}

func TestStructuredInputParseFailureStopsPreparation(t *testing.T) {
	t.Setenv(UntrustedStdinEnv, "false")
	failure := errors.New("invalid structured input for --input-structured")
	input := &requestflag.Flag[any]{Name: "input", BodyPath: "input", Const: true, Default: requestflag.InputValue{
		Raw: "@-", Parse: func(string) (any, error) { return nil, failure },
	}}
	_, err := FlagOptions(&cli.Command{Name: "create", Flags: []cli.Flag{input}},
		apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatRepeat, ApplicationJSON, false,
		strings.NewReader("private malformed content"), PreparationHooks{
			ValidateResolvedInputs: func(requestflag.RequestContents) error {
				t.Fatal("resolved validation ran after a parse failure")
				return nil
			},
		})
	require.ErrorIs(t, err, failure)
	require.NotContains(t, err.Error(), "private malformed content")
}
