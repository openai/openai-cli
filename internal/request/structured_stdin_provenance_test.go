package request

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestStructuredParentPipedInnerProvenance(t *testing.T) {
	privatePath := filepath.Join(t.TempDir(), "synthetic-private.txt")
	explicitPath := filepath.Join(t.TempDir(), "synthetic-explicit.txt")
	require.NoError(t, os.WriteFile(privatePath, []byte("synthetic private contents"), 0600))
	require.NoError(t, os.WriteFile(explicitPath, []byte("synthetic explicit contents"), 0600))
	for _, untrusted := range []bool{false, true} {
		for _, adapter := range []string{"text", "collection", "any", "nullable text"} {
			for _, source := range []string{"file", "stdin"} {
				t.Run(strconv.FormatBool(untrusted)+"/"+adapter+"/"+source, func(t *testing.T) {
					t.Setenv(UntrustedStdinEnv, strconv.FormatBool(untrusted))
					outer := &requestflag.Flag[any]{Name: "entry", BodyPath: "entry", InputPolicy: requestflag.InputPolicyTextStructured}
					explicit := &requestflag.InnerFlag[string]{Name: "entry.explicit", InnerField: "explicit", OuterFlag: outer, InputPolicy: requestflag.InputPolicyText}
					var inner cli.Flag
					switch adapter {
					case "text":
						inner = &requestflag.InnerFlag[string]{Name: "entry.value", InnerField: "value", DataAliases: []string{"alias"}, OuterFlag: outer, InputPolicy: requestflag.InputPolicyText}
					case "collection":
						inner = &requestflag.InnerFlag[[]string]{Name: "entry.value", InnerField: "value", DataAliases: []string{"alias"}, OuterFlag: outer, InputPolicy: requestflag.InputPolicyTyped}
					case "any":
						inner = &requestflag.InnerFlag[any]{Name: "entry.value", InnerField: "value", DataAliases: []string{"alias"}, OuterFlag: outer, InputPolicy: requestflag.InputPolicyTextStructured}
					case "nullable text":
						inner = &requestflag.InnerFlag[*string]{Name: "entry.value", InnerField: "value", DataAliases: []string{"alias"}, OuterFlag: outer, InputPolicy: requestflag.InputPolicyTextStructured}
					}
					command := &cli.Command{Flags: []cli.Flag{outer, explicit, inner}}
					require.NoError(t, outer.PreParse())
					requestflag.AddStructuredFlags(command)
					parent, err := json.Marshal(map[string]any{"literal": "@" + privatePath})
					require.NoError(t, err)
					for _, flag := range command.Flags {
						if flag.Names()[0] == "entry-structured" {
							require.NoError(t, flag.Set("entry-structured", string(parent)))
						}
					}
					require.NoError(t, explicit.Set("entry.explicit", "@"+explicitPath))
					text := "@" + privatePath
					if source == "stdin" {
						text = "@-"
					}
					var value any = text
					if adapter == "collection" {
						value = []any{text}
					}
					piped, err := json.Marshal(map[string]any{"entry": map[string]any{"alias": value, "explicit": "@piped/override.txt"}})
					require.NoError(t, err)
					security, err := NewStdinSecurity()
					require.NoError(t, err)
					contents := requestflag.ExtractRequestContents(command)
					consumed, err := ReadPipedInput(command, strings.NewReader(string(piped)), &contents, security, true)
					require.NoError(t, err)
					require.True(t, consumed)
					count, err := StdinConsumers(contents, false)
					require.NoError(t, err)
					if source == "stdin" && !untrusted {
						require.Equal(t, 1, count)
					} else {
						require.Zero(t, count)
					}
					got, err := EmbedRequestFiles(command, contents.Body, "body", EmbedText,
						&OnceStdinReader{FailureReason: "stdin was already consumed by piped YAML/JSON input"})
					if source == "stdin" && !untrusted {
						require.ErrorContains(t, err, "stdin was already consumed")
						return
					}
					require.NoError(t, err)
					body := got.(map[string]any)["entry"].(map[string]any)
					require.Equal(t, "@"+privatePath, body["literal"])
					require.Equal(t, "synthetic explicit contents", body["explicit"])
					var want any = text
					if !untrusted {
						want = "synthetic private contents"
					}
					if adapter == "collection" {
						want = []any{want}
					}
					require.Equal(t, want, body["value"])
				})
			}
		}
	}
}
