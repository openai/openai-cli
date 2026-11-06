package request

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestReadPipedInputPreservesRoutingAndProvenance(t *testing.T) {
	t.Parallel()

	for _, untrusted := range []bool{false, true} {
		for _, acceptBody := range []bool{false, true} {
			for _, input := range []string{
				`{"input":"@piped-override","cursor":"@piped-query","beta_alias":"@piped-header","id_alias":"synthetic-id","upload_alias":"synthetic-private.txt","metadata":{"value":"@piped-nested"}}`,
				"input: '@piped-override'\ncursor: '@piped-query'\nbeta_alias: '@piped-header'\nid_alias: synthetic-id\nupload_alias: synthetic-private.txt\nmetadata:\n  value: '@piped-nested'\n",
			} {
				t.Run(fmt.Sprintf("untrusted=%t/body=%t/JSON=%t", untrusted, acceptBody, strings.HasPrefix(input, "{")), func(t *testing.T) {
					explicit := &requestflag.Flag[string]{Name: "input", BodyPath: "input"}
					query := &requestflag.Flag[string]{Name: "after", QueryPath: "after", DataAliases: []string{"cursor"}}
					header := &requestflag.Flag[string]{Name: "beta", HeaderPath: "X-Test", DataAliases: []string{"beta_alias"}}
					path := &requestflag.Flag[string]{Name: "id", PathParam: "resource_id", DataAliases: []string{"id_alias"}}
					file := &requestflag.Flag[string]{Name: "upload", BodyPath: "file", DataAliases: []string{"upload_alias"}, FileInput: true}
					for _, flag := range []*requestflag.Flag[string]{explicit, query, header, path, file} {
						require.NoError(t, flag.PreParse())
					}
					require.NoError(t, explicit.Set("input", "@explicit-reference"))
					command := &cli.Command{Flags: []cli.Flag{explicit, query, header, path, file}}
					contents := requestflag.ExtractRequestContents(command)
					security := &StdinSecurity{enabled: untrusted}

					consumed, err := ReadPipedInput(command, strings.NewReader(input), &contents, security, acceptBody)

					require.NoError(t, err)
					require.True(t, consumed)
					literal := func(value string) any {
						if untrusted {
							return UntrustedStdinValue(value)
						}
						return value
					}
					require.Equal(t, literal("@piped-query"), contents.Queries["after"])
					require.Equal(t, literal("@piped-header"), contents.Headers["X-Test"])
					require.Equal(t, "synthetic-id", path.Get())
					body := contents.Body.(map[string]any)
					require.Equal(t, "@explicit-reference", body["input"])
					if !acceptBody {
						require.Len(t, body, 1)
						return
					}
					require.Len(t, body, 3)
					require.Equal(t, literal("@piped-nested"), body["metadata"].(map[string]any)["value"])
					require.Equal(t, literal("synthetic-private.txt"), body["file"])
					err = WrapFileInputValues(command, &contents, security)
					if untrusted {
						require.ErrorContains(t, err, "provide --upload explicitly")
					} else {
						require.NoError(t, err)
						require.Equal(t, FilePathValue("synthetic-private.txt"), body["file"])
					}
				})
			}
		}
	}
}

func TestReadPipedInputPreservesBodyShapesAndErrors(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, input, explicit string
		acceptBody            bool
		want                  any
		wantConsumed          bool
		wantError             string
	}{
		{"empty", "", "", true, map[string]any{}, false, ""},
		{"empty with explicit body", "", "synthetic-explicit", true, map[string]any{"input": "synthetic-explicit"}, false, ""},
		{"array", `["synthetic"]`, "", true, []any{"synthetic"}, true, ""},
		{"scalar", "true", "", true, true, true, ""},
		{"null", "null", "", true, nil, true, ""},
		{"bodyless array", `["synthetic"]`, "", false, map[string]any{}, true, ""},
		{"scalar conflict", `["synthetic"]`, "synthetic-explicit", true, nil, true, "Cannot merge flags with a body that is not a map:"},
		{"malformed", "{", "", true, nil, true, "Failed to parse piped data as YAML/JSON:"},
	} {
		t.Run(test.name, func(t *testing.T) {
			flag := &requestflag.Flag[string]{Name: "input", BodyPath: "input"}
			require.NoError(t, flag.PreParse())
			if test.explicit != "" {
				require.NoError(t, flag.Set("input", test.explicit))
			}
			command := &cli.Command{Flags: []cli.Flag{flag}}
			contents := requestflag.ExtractRequestContents(command)

			consumed, err := ReadPipedInput(command, strings.NewReader(test.input), &contents, &StdinSecurity{}, test.acceptBody)

			require.Equal(t, test.wantConsumed, consumed)
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, contents.Body)
		})
	}
}

type pipedInputBorrowedReader struct {
	io.Reader
	closed bool
}

func (r *pipedInputBorrowedReader) Close() error {
	r.closed = true
	return nil
}

func TestReadPipedInputPreservesBorrowedReaderAndReadCause(t *testing.T) {
	t.Parallel()

	cause := errors.Join(context.Canceled, io.ErrUnexpectedEOF)
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("read_failure=%t", fail), func(t *testing.T) {
			var source io.Reader = strings.NewReader(`{"input":"synthetic"}`)
			if fail {
				source = iotest.ErrReader(cause)
			}
			reader := &pipedInputBorrowedReader{Reader: source}
			command := &cli.Command{}
			contents := requestflag.RequestContents{Body: map[string]any{"existing": "synthetic"}}

			consumed, err := ReadPipedInput(command, reader, &contents, &StdinSecurity{}, true)

			require.False(t, reader.closed)
			if fail {
				require.False(t, consumed)
				require.ErrorIs(t, err, context.Canceled)
				require.ErrorIs(t, err, io.ErrUnexpectedEOF)
				require.Equal(t, map[string]any{"existing": "synthetic"}, contents.Body)
			} else {
				require.NoError(t, err)
				require.True(t, consumed)
				require.Equal(t, map[string]any{"input": "synthetic"}, contents.Body)
			}
		})
	}
}
