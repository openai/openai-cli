package custom

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"unicode"

	"github.com/openai/openai-cli/internal/audiohistory"
	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func TestFileReceiptRequestContext(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		want  []string
		omit  bool
	}{
		{"environment credentials", nil, nil, false},
		{"explicit profile", []string{"--profile=selected"}, []string{"--profile=selected"}, false},
		{"explicit empty profile", []string{"--profile="}, []string{"--profile="}, false},
		{"profile and request selection", []string{"--profile=selected", "--project=project-one"}, []string{"--profile=selected", "--project=project-one"}, false},
		{"request selection", []string{"--project=project-one", "--organization=org-one", "--base-url=https://example.invalid/proxy/v1"}, []string{"--project=project-one", "--organization=org-one", "--base-url=https://example.invalid/proxy/v1"}, false},
		{"explicit empty project", []string{"--project="}, []string{"--project="}, false},
		{"same inherited key", []string{"--api-key=sk-fake-inherited"}, nil, false},
		{"different key", []string{"--api-key=sk-fake-override"}, nil, true},
		{"empty key override", []string{"--api-key="}, nil, true},
		{"admin key override", []string{"--admin-api-key=sk-fake-admin"}, nil, true},
		{"webhook secret override", []string{"--webhook-secret=fake-secret"}, nil, true},
		{"header override", []string{"-H", "Authorization: Bearer fake-secret"}, nil, true},
		{"certificate override", []string{"--mtls-client-cert-file=/synthetic/private/cert.pem"}, nil, true},
		{"private key override", []string{"--mtls-client-key-file=/synthetic/private/key.pem"}, nil, true},
		{"URL user info", []string{"--base-url=https://fake:secret@example.invalid"}, nil, true},
		{"URL query", []string{"--base-url=https://example.invalid/v1?key=fake-secret"}, nil, true},
		{"URL fragment", []string{"--base-url=https://example.invalid/v1#fake-secret"}, nil, true},
		{"URL empty query", []string{"--base-url=https://example.invalid/v1?"}, nil, true},
		{"invalid project", []string{"--project=proj\x00name"}, nil, true},
		{"invalid profile NUL", []string{"--profile=bad\x00name"}, nil, true},
		{"invalid profile UTF-8", []string{"--profile=bad\xffname"}, nil, true},
		{"profile with secret override", []string{"--profile=selected", "--api-key=sk-fake-override"}, nil, true},
		{"profile with private URL", []string{"--profile=selected", "--base-url=https://example.invalid/v1?key=fake-secret"}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range []string{"OPENAI_ADMIN_KEY", "OPENAI_WEBHOOK_SECRET", "OPENAI_ORG_ID", "OPENAI_PROJECT_ID", mtlsClientCertFileEnv, mtlsClientKeyFileEnv} {
				t.Setenv(name, "")
				require.NoError(t, os.Unsetenv(name))
			}
			t.Setenv("OPENAI_API_KEY", "sk-fake-inherited")
			t.Setenv("OPENAI_PROFILE", "inherited-profile")
			root := fileWorkflowCommandTree(func(ctx context.Context, command *cli.Command) error {
				invocation := ctx.Value(fileInvocationKey{}).(fileInvocation)
				require.Equal(t, tc.want, invocation.requestArgs)
				require.Equal(t, tc.omit, invocation.omitHint)
				var receipt bytes.Buffer
				require.NoError(t, writeFileReceipt(&receipt, gjson.Parse(fileReceiptFixture), "bash", invocation))
				require.Equal(t, !tc.omit, bytes.Contains(receipt.Bytes(), []byte("Inspect it:")))
				for _, secret := range []string{"sk-fake", "fake-secret", "/synthetic/private/"} {
					require.NotContains(t, receipt.String(), secret)
				}
				model := newAudioWorkbench(ctx, command, nil, audiohistory.Store{})
				model.input.SetValue("SYNTHETIC_PRIVATE_EDITOR_TEXT")
				preview := model.commandPreview()
				if tc.omit {
					require.Equal(t, "Command preview unavailable with private request overrides.", preview)
				} else {
					require.Contains(t, preview, "OPENAI_UNTRUSTED_STDIN=true")
				}
				for _, secret := range []string{"sk-fake", "fake-secret", "/synthetic/private/", "inherited-profile"} {
					require.NotContains(t, preview, secret)
				}
				return nil
			})
			root.Writer, root.ErrWriter = io.Discard, io.Discard
			root.Flags = []cli.Flag{&cli.StringFlag{Name: "base-url"}, NewRequestHeaderFlag()}
			for _, pair := range [][2]string{{"api-key", "OPENAI_API_KEY"}, {"admin-api-key", "OPENAI_ADMIN_KEY"}, {"webhook-secret", "OPENAI_WEBHOOK_SECRET"}, {"organization", "OPENAI_ORG_ID"}, {"project", "OPENAI_PROJECT_ID"}} {
				root.Flags = append(root.Flags, &requestflag.Flag[string]{Name: pair[0], Sources: cli.EnvVars(pair[1])})
			}
			root.Flags = append(root.Flags, mtlsClientFlags()...)
			configureProfileContext(root)
			configureRootRequestFlags(root)
			localcommands.ConfigureTasks(root)
			configureFileCommands(root)
			args := append([]string{"openai", "files", "upload", "source.txt", "--purpose=user_data"}, tc.flags...)
			require.NoError(t, root.Run(t.Context(), args))
		})
	}
}

func TestFileAndAudioCopiedCommandsPreserveProfileSelector(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			binary, directory := audioPreviewExecutable(t, "openai preview's binary")
			for _, tc := range []struct {
				name, environment, rootFlag, childFlag string
				want                                   []string
			}{
				{"explicit", "inherited-profile", "--profile=team's \"$(printf altered)\" 世界\u202e", "", []string{"--profile=team's \"$(printf altered)\" 世界\u202e"}},
				{"explicit empty", "inherited-profile", "--profile=", "", []string{"--profile="}},
				{"inherited", "inherited-profile", "", "", nil},
				{"inherited empty", "", "", "", nil},
				{"child ownership", "inherited-profile", "", "--profile=/synthetic/private/child", nil},
				{"root and child ownership", "inherited-profile", "--profile=selected", "--profile=/synthetic/private/child", []string{"--profile=selected"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Setenv("OPENAI_PROFILE", tc.environment)
					var invocation fileInvocation
					var model *audioWorkbench
					root := fileWorkflowCommandTree(func(ctx context.Context, command *cli.Command) error {
						invocation = ctx.Value(fileInvocationKey{}).(fileInvocation)
						model = newAudioWorkbench(ctx, command, nil, audiohistory.Store{})
						return nil
					})
					root.Metadata = map[string]any{"help-invocation": audioShellQuote(binary)}
					configureProfileContext(root)
					if tc.childFlag != "" {
						create := root.Command("files").Command("create")
						create.Flags = append(create.Flags, &cli.StringFlag{Name: "profile"})
					}
					localcommands.ConfigureTasks(root)
					configureFileCommands(root)
					args := []string{"openai"}
					if tc.rootFlag != "" {
						args = append(args, tc.rootFlag)
					}
					args = append(args, "files", "upload", "source.txt", "--purpose=user_data")
					if tc.childFlag != "" {
						args = append(args, tc.childFlag)
					}
					require.NoError(t, root.Run(t.Context(), args))
					require.NotNil(t, model, "upload action must run")
					require.Equal(t, tc.want, invocation.requestArgs)
					require.False(t, invocation.omitHint)

					// Use the recorder executable while retaining parsed request arguments.
					invocation.display, invocation.executable = binary, binary
					invocation.goRun = false
					var receipt bytes.Buffer
					require.NoError(t, writeFileReceipt(&receipt, gjson.Parse(fileReceiptFixture), shell, invocation))
					_, copiedFile, found := strings.Cut(receipt.String(), "\nInspect it: ")
					require.True(t, found, receipt.String())
					fileCapture := executeAudioPreview(t, shell, strings.TrimSpace(copiedFile), directory)
					require.Equal(t, append(append([]string{}, tc.want...), "files", "get", "file-example"), fileCapture.args)
					require.Empty(t, fileCapture.stdin)
					require.Equal(t, "unset", fileCapture.protected)

					model.settings.Format = "wav"
					model.input.SetValue("@synthetic 'literal' text")
					preview := model.commandPreview()
					for _, copied := range []string{copiedFile, preview} {
						require.NotContains(t, copied, "/synthetic/private/")
						require.NotContains(t, copied, "inherited-profile")
						for _, character := range strings.TrimSpace(copied) {
							require.False(t, unicode.IsControl(character) || unicode.Is(unicode.Cf, character), "literal control in copied command")
						}
					}
					audioCapture := executeAudioPreview(t, shell, preview, directory)
					require.Equal(t, append(append([]string{}, tc.want...), "audio", "speak", "--output", "speech.wav"), audioCapture.args)
					require.Equal(t, "true", audioCapture.protected)
					assertAudioPreviewJSON(t, model, audioCapture.stdin)
				})
			}
		})
	}
}
