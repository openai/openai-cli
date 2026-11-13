package request

import (
	"encoding/base64"
	"encoding/json"
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

func structuredTestCommand(t *testing.T, validate func(any) error, args ...string) (*cli.Command, any) {
	t.Helper()
	parent := &requestflag.Flag[any]{Name: "input", BodyPath: "input", InputPolicy: requestflag.InputPolicyTextStructured, Default: "default", Validator: validate}
	content := &requestflag.InnerFlag[any]{Name: "input.content", OuterFlag: parent, InnerField: "content", DataAliases: []string{"alias"}, InputPolicy: requestflag.InputPolicyTextStructured}
	extra := &requestflag.InnerFlag[any]{Name: "input.extra", OuterFlag: parent, InnerField: "extra", InputPolicy: requestflag.InputPolicyTextStructured}
	cmd := &cli.Command{Name: "test", Flags: []cli.Flag{parent, content, extra}, Writer: io.Discard, ErrWriter: io.Discard}
	requestflag.AddStructuredFlags(cmd)
	require.NoError(t, cmd.Run(t.Context(), append([]string{"test"}, args...)))
	return cmd, parent.Get()
}

func TestStructuredInputSourcesResolveOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.json")
	payload := `{"literal":"@-","escaped":"\\@-","nested":["@private/missing.json",null,false,2]}`
	require.NoError(t, os.WriteFile(path, []byte(payload), 0600))

	for styleName, style := range map[string]FileEmbedStyle{"text": EmbedText, "multipart": EmbedIOReader} {
		for _, source := range []struct{ name, raw string }{
			{"file", "@" + path}, {"file URI", "@file://" + path}, {"stdin", "@-"},
			{"stdin URI", "@file://-"}, {"stdin alias", "@/dev/stdin"},
		} {
			t.Run(styleName+"/"+source.name, func(t *testing.T) {
				calls := 0
				_, input := structuredTestCommand(t, func(any) error {
					calls++
					return nil
				}, "--input-structured", source.raw)
				calls = 0 // Ignore validation of the flag default.
				stdin := &OnceStdinReader{Reader: strings.NewReader(payload)}
				got, err := EmbedFiles(input, style, stdin)
				require.NoError(t, err)
				wire, err := json.Marshal(got)
				require.NoError(t, err)
				require.JSONEq(t, payload, string(wire))
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
	} {
		t.Run(test.name, func(t *testing.T) {
			_, input := structuredTestCommand(t, nil, "--input-structured", test.raw)
			got, err := EmbedFiles(input, EmbedIOReader, &OnceStdinReader{Reader: strings.NewReader("synthetic text")})
			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
	t.Run("escaped at", func(t *testing.T) {
		const raw = `\@private/missing.txt`
		_, literal := structuredTestCommand(t, nil, "--input", raw)
		got, err := EmbedFiles(literal, EmbedText, nil)
		require.NoError(t, err)
		require.Equal(t, "@private/missing.txt", got)

		// The structured parser receives the unescaped text. YAML rejects a bare @.
		_, structured := structuredTestCommand(t, nil, "--input-structured", raw)
		_, err = EmbedFiles(structured, EmbedIOReader, nil)
		var failure *requestflag.StructuredInputError
		require.ErrorAs(t, err, &failure)
	})
}

func TestStructuredInputProtectsParsedValuesAndResolvesNestedFlags(t *testing.T) {
	_, input := structuredTestCommand(t, nil,
		"--input-structured", `[{"literal":"@private/missing.json","escaped":"\\@-","stdin":"@-","nested":[null]}]`,
		"--input.content-structured", "@-")
	count, err := StdinConsumers(requestflag.RequestContents{Body: input}, false)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	// Both evaluations read the supplied stream. The first must not mutate the flag.
	for _, text := range []string{"@-", "@file://-"} {
		data, err := json.Marshal(text)
		require.NoError(t, err)
		got, err := EmbedFiles(input, EmbedText, &OnceStdinReader{Reader: strings.NewReader(string(data))})
		require.NoError(t, err)
		require.Equal(t, []any{map[string]any{
			"literal": "@private/missing.json", "escaped": `\@-`, "stdin": "@-",
			"nested": []any{nil}, "content": text,
		}}, got)
	}
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
				_, input := structuredTestCommand(t, nil,
					"--input-structured", `{"literal":"@private/missing.json","stdin_data":"@-"}`,
					"--input.content", source.value)
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

func TestProtectStdinValueCopiesStructuredNestedSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested.txt")
	require.NoError(t, os.WriteFile(path, []byte("file contents"), 0600))
	_, original := structuredTestCommand(t, nil, "--input-structured", "{}",
		"--input.content", "@-", "--input.extra", "@"+path)

	protected := ProtectStdinValue(original)
	count, err := StdinConsumers(requestflag.RequestContents{Body: protected}, false)
	require.NoError(t, err)
	require.Zero(t, count)
	got, err := EmbedFiles(protected, EmbedText, &OnceStdinReader{FailureReason: "protected input must not read stdin"})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"content": "@-", "extra": "@" + path}, got)

	count, err = StdinConsumers(requestflag.RequestContents{Body: original}, false)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	got, err = EmbedFiles(original, EmbedText, &OnceStdinReader{Reader: strings.NewReader("stdin contents")})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"content": "stdin contents", "extra": "file contents"}, got)
}

func TestStructuredInputNestedSourcesPreserveLegacyAndUntrustedValues(t *testing.T) {
	t.Setenv(UntrustedStdinEnv, "true")
	command, _ := structuredTestCommand(t, nil, "--input-structured", `{"structured":"@-"}`, "--input.content", `\@-`)
	security, err := NewStdinSecurity()
	require.NoError(t, err)
	contents := requestflag.ExtractRequestContents(command)
	piped := `{"input":{"extra":{"untrusted":"@private/missing.json","untrusted_stdin":"@-"}}}`
	consumed, err := ReadPipedInput(command, strings.NewReader(piped), &contents, security, true)
	require.NoError(t, err)
	require.True(t, consumed)
	count, err := StdinConsumers(contents, false)
	require.NoError(t, err)
	require.Zero(t, count)
	got, err := EmbedFiles(contents.Body, EmbedText, &OnceStdinReader{FailureReason: "already consumed"})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"input": map[string]any{
		"content": "@-", "structured": "@-",
		"extra": map[string]any{"untrusted": "@private/missing.json", "untrusted_stdin": "@-"},
	}}, got)
}

func TestStructuredInputExplicitNestedSourceAliases(t *testing.T) {
	t.Setenv(UntrustedStdinEnv, "true")
	command, _ := structuredTestCommand(t, nil, "--input-structured", `{"alias":"@private/missing.json"}`, "--input.extra", "@-")
	var observed []byte
	_, err := FlagOptions(command,
		apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatRepeat, ApplicationJSON, false,
		strings.NewReader("synthetic child text"), PreparationHooks{
			Encoding: EncodingHooks{ObserveJSON: []func([]byte){func(body []byte) { observed = body }}},
		})
	require.NoError(t, err)
	require.JSONEq(t, `{"input":{"content":"@private/missing.json","extra":"synthetic child text"}}`, string(observed))
}

func TestStructuredInputCompositionPreservesSourceOrder(t *testing.T) {
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first.json"), filepath.Join(dir, "second.json")
	require.NoError(t, os.WriteFile(first, []byte(`["@first"]`), 0600))
	require.NoError(t, os.WriteFile(second, []byte(`"@second"`), 0600))
	var validated []any
	_, input := structuredTestCommand(t, func(value any) error {
		validated = append(validated, value)
		return nil
	}, "--input-structured", "@"+first, "--input-structured", "@"+second)
	validated = nil
	got, err := EmbedFiles(input, EmbedText, nil)
	require.NoError(t, err)
	require.Equal(t, []any{"@first", "@second"}, got)
	require.Equal(t, []any{[]any{"@first"}, []any{"@first", "@second"}}, validated)
}

func TestStructuredInputCountsOnlyExplicitStdinSources(t *testing.T) {
	for _, test := range []struct {
		name  string
		args  []string
		count int
	}{
		{"parsed string", []string{"--input-structured", `"@-"`}, 0},
		{"parsed array", []string{"--input-structured", `["@-","@file://-"]`}, 0},
		{"deferred", []string{"--input-structured", "@-"}, 1},
		{"escaped", []string{"--input-structured", `\@-`}, 0},
		{"previous", []string{"--input-structured", "@-", "--input-structured", `"@-"`}, 1},
		{"duplicate", []string{"--input-structured", "@-", "--input-structured", "@file://-"}, 2},
		{"nested", []string{"--input-structured", "[]", "--input.content-structured", "@-", "--input.content-structured", "@-"}, 2},
		{"ordinary nested", []string{"--input-structured", "[]", "--input.content", "@-", "--input.content", "@file://-"}, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, input := structuredTestCommand(t, nil, test.args...)
			count, err := StdinConsumers(requestflag.RequestContents{Body: input}, false)
			require.Equal(t, test.count, count)
			require.Equal(t, test.count > 1, err != nil)
		})
	}
}

func TestStructuredInputSourceFailurePreservesOption(t *testing.T) {
	validations := 0
	_, input := structuredTestCommand(t, func(any) error {
		validations++
		return nil
	}, "--input-structured", "@"+filepath.Join(t.TempDir(), "missing.json"))
	validations = 0
	_, err := EmbedFiles(input, EmbedText, nil)
	require.ErrorIs(t, err, os.ErrNotExist)
	var failure *InputFileError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "--input-structured", failure.Option)
	require.Zero(t, validations)
}

func TestStructuredInputPreparationReservesStdinAndResolvesAliases(t *testing.T) {
	t.Setenv(UntrustedStdinEnv, "true")
	command, _ := structuredTestCommand(t, nil, "--input-structured", "@-")
	var observed []byte
	_, err := FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatRepeat,
		ApplicationJSON, false, strings.NewReader(`{"alias":"@private/missing.json"}`), PreparationHooks{
			ValidateResolvedInputs: func(contents requestflag.RequestContents) error {
				require.Equal(t, map[string]any{"input": map[string]any{"content": "@private/missing.json"}}, contents.Body)
				return nil
			},
			Encoding: EncodingHooks{ObserveJSON: []func([]byte){func(body []byte) { observed = body }}},
		})
	require.NoError(t, err)
	require.JSONEq(t, `{"input":{"content":"@private/missing.json"}}`, string(observed))
}

func TestStructuredInputParseFailureStopsPreparation(t *testing.T) {
	t.Setenv(UntrustedStdinEnv, "false")
	command, _ := structuredTestCommand(t, nil, "--input-structured", "@-")
	_, err := FlagOptions(command,
		apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatRepeat, ApplicationJSON, false,
		strings.NewReader("[private malformed content"), PreparationHooks{
			ValidateResolvedInputs: func(requestflag.RequestContents) error {
				t.Fatal("resolved validation ran after a parse failure")
				return nil
			},
		})
	var failure *requestflag.StructuredInputError
	require.ErrorAs(t, err, &failure)
	require.ErrorContains(t, err, "--input-structured")
	require.NotContains(t, err.Error(), "private malformed content")
}
