package request

import (
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

func TestFlagOptionsFeatureValidationPrecedesFileExpansion(t *testing.T) {
	t.Setenv(UntrustedStdinEnv, "false")
	for _, stage := range []string{"required", "preflight", "credentials", "files"} {
		t.Run(stage, func(t *testing.T) {
			file := &requestflag.Flag[string]{Name: "file", BodyPath: "file", FileInput: true, Required: true}
			require.NoError(t, file.PreParse())
			if stage != "required" {
				require.NoError(t, file.Set("file", filepath.Join(t.TempDir(), "synthetic-missing.bin")))
			}
			failure := errors.New("synthetic " + stage + " failure")
			var events []string
			_, err := FlagOptions(&cli.Command{Name: "upload", Flags: []cli.Flag{file}}, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatRepeat, MultipartFormEncoded, true, strings.NewReader(""), PreparationHooks{
				PreflightMultipart: func(body map[string]any) error {
					events = append(events, "preflight")
					_, marked := body["file"].(FilePathValue)
					require.True(t, marked)
					if stage == "preflight" {
						return failure
					}
					return nil
				},
				ValidateCredentials: func() error {
					events = append(events, "credentials")
					if stage == "credentials" {
						return failure
					}
					return nil
				},
			})
			switch stage {
			case "required":
				require.ErrorContains(t, err, `Required flag "file" not set`)
				require.Empty(t, events)
			case "preflight":
				require.ErrorIs(t, err, failure)
				require.Equal(t, []string{"preflight"}, events)
			case "credentials":
				require.ErrorIs(t, err, failure)
				require.Equal(t, []string{"preflight", "credentials"}, events)
			case "files":
				require.ErrorIs(t, err, os.ErrNotExist)
				require.Equal(t, []string{"preflight", "credentials"}, events)
			}
		})
	}
}

func TestFlagOptionsResolvedInputValidationClosesUploads(t *testing.T) {
	t.Setenv(UntrustedStdinEnv, "false")
	path := filepath.Join(t.TempDir(), "purpose.txt")
	require.NoError(t, os.WriteFile(path, []byte("batch"), 0600))
	query := &requestflag.Flag[string]{Name: "purpose", QueryPath: "purpose"}
	require.NoError(t, query.PreParse())
	require.NoError(t, query.Set("purpose", "@"+path))
	source := &recordingReadCloser{reader: strings.NewReader("synthetic upload")}
	failure := errors.New("synthetic resolved-input rejection")
	var phases []string
	_, err := FlagOptions(&cli.Command{Name: "create", Flags: []cli.Flag{query}},
		apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatRepeat, MultipartFormEncoded,
		true, strings.NewReader(""), PreparationHooks{
			EmbedMultipart: func(body map[string]any, stdin *OnceStdinReader) (map[string]any, error) {
				phases = append(phases, "expand")
				return map[string]any{"file": FileUpload{Reader: source, FileName: "synthetic.txt"}}, nil
			},
			ValidateResolvedInputs: func(contents requestflag.RequestContents) error {
				phases = append(phases, "validate")
				require.Equal(t, "batch", contents.Queries["purpose"])
				_, ok := contents.Body.(map[string]any)["file"].(FileUpload)
				require.True(t, ok)
				return failure
			},
			Encoding: EncodingHooks{PrepareMultipart: func(map[string]any) error {
				phases = append(phases, "encode")
				return nil
			}},
		})
	require.ErrorIs(t, err, failure)
	require.Equal(t, []string{"expand", "validate"}, phases)
	require.EqualValues(t, 1, source.closeCount.Load())
}

func TestFlagOptionsMultipartHookCleanupPreservesJoinedCauses(t *testing.T) {
	t.Setenv(UntrustedStdinEnv, "false")
	for _, failHeader := range []bool{true, false} {
		t.Run(map[bool]string{true: "header expansion", false: "encoding hook"}[failHeader], func(t *testing.T) {
			cleanupFailure := errors.New("synthetic cleanup failure")
			prepareFailure := errors.New("synthetic encoding preparation failure")
			source := &recordingReadCloser{reader: strings.NewReader("synthetic upload"), closeErr: cleanupFailure}
			header := &requestflag.Flag[string]{Name: "trace", HeaderPath: "X-Trace"}
			require.NoError(t, header.PreParse())
			if failHeader {
				require.NoError(t, header.Set("trace", "@"+filepath.Join(t.TempDir(), "synthetic-missing.txt")))
			}
			var events []string
			_, err := FlagOptions(&cli.Command{Name: "upload", Flags: []cli.Flag{header}}, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatRepeat, MultipartFormEncoded, true, strings.NewReader(""), PreparationHooks{
				PreflightMultipart:  func(map[string]any) error { events = append(events, "preflight"); return nil },
				ValidateCredentials: func() error { events = append(events, "credentials"); return nil },
				EmbedMultipart: func(body map[string]any, stdin *OnceStdinReader) (map[string]any, error) {
					events = append(events, "embed")
					body["file"] = FileUpload{Reader: source, FileName: "synthetic.txt"}
					_, err := stdin.Take()
					require.ErrorContains(t, err, "stdin is already being used for the request body")
					return body, nil
				},
				Encoding: EncodingHooks{PrepareMultipart: func(map[string]any) error {
					events = append(events, "encode")
					return prepareFailure
				}},
			})
			require.ErrorIs(t, err, cleanupFailure)
			require.EqualValues(t, 1, source.closeCount.Load())
			if failHeader {
				require.ErrorIs(t, err, os.ErrNotExist)
				require.Equal(t, []string{"preflight", "credentials", "embed"}, events)
			} else {
				require.ErrorIs(t, err, prepareFailure)
				require.Equal(t, []string{"preflight", "credentials", "embed", "encode"}, events)
			}
		})
	}
}

func TestFlagOptionsExplicitStdinRemainsBorrowedThroughMultipart(t *testing.T) {
	t.Setenv(UntrustedStdinEnv, "false")
	file := &requestflag.Flag[string]{Name: "file", BodyPath: "file", FileInput: true}
	require.NoError(t, file.PreParse())
	require.NoError(t, file.Set("file", "-"))
	source := &recordingReadCloser{reader: strings.NewReader("synthetic borrowed input")}
	var body io.ReadCloser
	_, err := FlagOptions(&cli.Command{Name: "upload", Flags: []cli.Flag{file}}, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatRepeat, MultipartFormEncoded, false, source, PreparationHooks{
		Encoding: EncodingHooks{ObserveMultipart: func(prepared io.Closer) { body = prepared.(io.ReadCloser) }},
	})
	require.NoError(t, err)
	require.NotNil(t, body)
	t.Cleanup(func() { require.NoError(t, body.Close()) })
	contents, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Contains(t, string(contents), "synthetic borrowed input")
	require.NoError(t, body.Close())
	require.Zero(t, source.closeCount.Load())
}
