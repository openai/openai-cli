package custom

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// Match TestSaveResponseCancellation's deterministic second-file boundary,
// then exercise the public response and error presenters around that saver.
func TestSavedImageCancellationPreservesRecovery(t *testing.T) {
	var fixture bytes.Buffer
	require.NoError(t, png.Encode(&fixture, image.NewRGBA(image.Rect(0, 0, 1, 1))))
	encoded := base64.StdEncoding.EncodeToString(fixture.Bytes())
	response := gjson.Parse(fmt.Sprintf(`{"data":[{"b64_json":%q},{"b64_json":%q}]}`, encoded, encoded))
	for _, format := range []string{"text", "json"} {
		for _, failReceipt := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/receipt_failure=%t", format, failReceipt), func(t *testing.T) {
				directory := t.TempDir()
				first := filepath.Join(directory, "robot.png")
				second := filepath.Join(directory, "robot-2.png")
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				canceledDuringSecond := false
				partialBytes := int64(0)
				checking := imageSaveCancellationContext{Context: ctx, check: func() {
					if canceledDuringSecond {
						return
					}
					info, err := os.Stat(second)
					if errors.Is(err, os.ErrNotExist) {
						return
					}
					require.NoError(t, err)
					if info.Size() == 0 {
						return
					}
					require.Less(t, info.Size(), int64(fixture.Len()), "cancel before the second image completes")
					completed, err := os.ReadFile(first)
					require.NoError(t, err)
					require.Equal(t, fixture.Bytes(), completed)
					partialBytes = info.Size()
					canceledDuringSecond = true
					cancel()
				}}
				var receipt bytes.Buffer
				receiptCalls := 0
				sinkErr := errors.New("synthetic-private-sink \x1b]52;c;synthetic-secret\a")
				writer := loadingTestWriter(func(data []byte) (int, error) {
					receiptCalls++
					require.ErrorIs(t, ctx.Err(), context.Canceled, "completed paths must print after cancellation")
					_, err := os.Stat(second)
					require.ErrorIs(t, err, os.ErrNotExist, "incomplete output must be removed before its receipt")
					if failReceipt {
						return failingImageWriter{sinkErr}.Write(data)
					}
					return receipt.Write(data)
				})
				plan := &imageOutputPlan{directory: directory, name: "robot", inline: "off"}
				presentation := context.WithValue(checking, imagePresentationKey{}, imagePresentation{plan, writer})
				failure := ShowJSON(response, ShowJSONOpts{
					Context: presentation, Operation: imageGenerationOperation, OutputKind: OutputResponse,
					Format: "auto", Stdout: writer, Stderr: io.Discard,
				})
				require.True(t, canceledDuringSecond, "the second-file cancellation boundary must execute")
				require.ErrorIs(t, failure, context.Canceled)
				require.Equal(t, 1, receiptCalls)
				completed, err := os.ReadFile(first)
				require.NoError(t, err)
				require.Equal(t, fixture.Bytes(), completed)
				entries, err := os.ReadDir(directory)
				require.NoError(t, err)
				require.Len(t, entries, 1)
				require.Equal(t, "robot.png", entries[0].Name())
				want := "Saved 1 image(s), but could not save the entire response. The listed files are kept; do not generate those images again."
				if failReceipt {
					require.ErrorIs(t, failure, sinkErr)
					require.Empty(t, receipt.String())
					want = "Saved 1 image(s), but could not save the entire response or print all saved paths. Check the output folder before generating again."
				} else {
					require.Equal(t, fmt.Sprintf("Saved image: %q\n", first), receipt.String())
				}
				require.Equal(t, want, failure.Error(), "saving already authors the required recovery guidance")
				root := readableErrorTestCommand(t, "--format-error", format)
				for _, interrupted := range []bool{false, true} {
					wrapped := failure
					wantStatus := 1 // main's fallback; no native process execution is claimed here.
					if interrupted {
						wrapped = &imageLoadingInterrupt{failure}
						wantStatus = 130
					}
					wrapped = withCommandError(root, wrapped)
					require.ErrorIs(t, wrapped, context.Canceled)
					if failReceipt {
						require.ErrorIs(t, wrapped, sinkErr)
					}
					status := 1
					var exit cli.ExitCoder
					if errors.As(wrapped, &exit) {
						status = exit.ExitCode()
					}
					require.Equal(t, wantStatus, status)
					var diagnostic bytes.Buffer
					require.NoError(t, ShowCommandError(root, wrapped, &diagnostic))
					assertReadableErrorContainsNoPrivateDetails(t, diagnostic.String())
					require.NotContains(t, diagnostic.String(), encoded)
					message := diagnostic.String()
					expected := want
					if format == "json" {
						var document map[string]any
						require.NoError(t, json.Unmarshal(diagnostic.Bytes(), &document), "the complete stderr stream must be one JSON document")
						require.Len(t, document, 1, "do not invent an HTTP status")
						var ok bool
						message, ok = document["message"].(string)
						require.True(t, ok)
					} else {
						expected += "\n"
					}
					t.Logf("interrupted=%t status_contract=%d kept_bytes=%d canceled_second_bytes=%d receipt_bytes=%d diagnostic=%q", interrupted, status, len(completed), partialBytes, receipt.Len(), message)
					if message != expected {
						t.Errorf("error presentation discarded saved-image recovery: got %q; want %q", message, expected)
					}
				}
			})
		}
	}
}

type imageSaveCancellationContext struct {
	context.Context
	check func()
}

func (c imageSaveCancellationContext) Err() error {
	c.check()
	return c.Context.Err()
}

func TestSavedImageCancellationWithoutCompletedFiles(t *testing.T) {
	var fixture bytes.Buffer
	require.NoError(t, png.Encode(&fixture, image.NewRGBA(image.Rect(0, 0, 1, 1))))
	encoded := base64.StdEncoding.EncodeToString(fixture.Bytes())
	response := gjson.Parse(fmt.Sprintf(`{"data":[{"b64_json":%q}]}`, encoded))
	for _, boundary := range []string{"before_first_save", "during_first_write"} {
		for _, format := range []string{"text", "json"} {
			t.Run(boundary+"/"+format, func(t *testing.T) {
				directory := t.TempDir()
				path := filepath.Join(directory, "synthetic-private-image.png")
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				boundaryReached := false
				partialBytes := int64(0)
				checking := imageSaveCancellationContext{Context: ctx, check: func() {
					if boundary != "during_first_write" || boundaryReached {
						return
					}
					info, err := os.Stat(path)
					if errors.Is(err, os.ErrNotExist) {
						return
					}
					require.NoError(t, err)
					if info.Size() == 0 {
						return
					}
					require.Less(t, info.Size(), int64(fixture.Len()), "cancel before the first image completes")
					partialBytes = info.Size()
					boundaryReached = true
					cancel()
				}}
				plan := &imageOutputPlan{directory: directory, name: "synthetic-private-image", inline: "off"}
				plan.loadingStage = func(stage imageLoadingStage) {
					if boundary == "before_first_save" && stage == imageLoadingSaving {
						boundaryReached = true
						cancel()
					}
				}
				var receipt bytes.Buffer
				receiptCalls := 0
				writer := loadingTestWriter(func(data []byte) (int, error) {
					receiptCalls++
					return receipt.Write(data)
				})
				presentation := context.WithValue(checking, imagePresentationKey{}, imagePresentation{plan, writer})
				failure := ShowJSON(response, ShowJSONOpts{
					Context: presentation, Operation: imageGenerationOperation, OutputKind: OutputResponse,
					Format: "auto", Stdout: writer, Stderr: io.Discard,
				})
				require.True(t, boundaryReached, "the public presenter must reach the selected save boundary")
				require.ErrorIs(t, failure, context.Canceled)
				if boundary == "during_first_write" {
					require.Positive(t, partialBytes, "exercise cleanup of an actual incomplete file")
				} else {
					require.Zero(t, partialBytes)
				}
				require.Zero(t, receiptCalls)
				require.Empty(t, receipt.String())
				_, err := os.Stat(path)
				require.ErrorIs(t, err, os.ErrNotExist)
				entries, err := os.ReadDir(directory)
				require.NoError(t, err)
				require.Empty(t, entries, "no completed file or incomplete output may remain")
				root := readableErrorTestCommand(t, "--format-error", format)
				for _, interrupted := range []bool{false, true} {
					wrapped := failure
					wantStatus := 1 // main's fallback; this check does not execute a native process.
					if interrupted {
						wrapped = &imageLoadingInterrupt{failure}
						wantStatus = 130
					}
					wrapped = withCommandError(root, wrapped)
					require.ErrorIs(t, wrapped, context.Canceled)
					status := 1
					var exit cli.ExitCoder
					if errors.As(wrapped, &exit) {
						status = exit.ExitCode()
					}
					require.Equal(t, wantStatus, status)
					var diagnostic bytes.Buffer
					require.NoError(t, ShowCommandError(root, wrapped, &diagnostic))
					assertReadableErrorContainsNoPrivateDetails(t, diagnostic.String())
					require.NotContains(t, diagnostic.String(), directory)
					require.NotContains(t, diagnostic.String(), encoded)
					message := diagnostic.String()
					want := "Request canceled."
					if format == "json" {
						var document map[string]any
						require.NoError(t, json.Unmarshal(diagnostic.Bytes(), &document))
						require.Len(t, document, 1, "do not invent an HTTP status")
						var ok bool
						message, ok = document["message"].(string)
						require.True(t, ok)
					} else {
						want += "\n"
					}
					t.Logf("interrupted=%t status_contract=%d canceled_first_bytes=%d diagnostic=%q", interrupted, status, partialBytes, message)
					if message != want {
						t.Errorf("zero-save cancellation presented save-failure guidance: got %q; want %q", message, want)
					}
				}
			})
		}
	}
}

func TestSavedImageFailureWithoutCompletedFilesKeepsRecovery(t *testing.T) {
	response := gjson.Parse(`{"data":[{"b64_json":"synthetic-private-invalid-base64"}]}`)
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			plan := &imageOutputPlan{directory: t.TempDir(), name: "robot", inline: "off"}
			var receipt bytes.Buffer
			presentation := context.WithValue(t.Context(), imagePresentationKey{}, imagePresentation{plan, &receipt})
			failure := ShowJSON(response, ShowJSONOpts{
				Context: presentation, Operation: imageGenerationOperation, OutputKind: OutputResponse,
				Format: "auto", Stdout: &receipt, Stderr: io.Discard,
			})
			var malformed base64.CorruptInputError
			require.ErrorAs(t, failure, &malformed)
			require.False(t, errors.Is(failure, context.Canceled))
			require.Empty(t, receipt.String())
			entries, err := os.ReadDir(plan.directory)
			require.NoError(t, err)
			require.Empty(t, entries)
			root := readableErrorTestCommand(t, "--format-error", format)
			var diagnostic bytes.Buffer
			require.NoError(t, ShowCommandError(root, withCommandError(root, failure), &diagnostic))
			assertReadableErrorContainsNoPrivateDetails(t, diagnostic.String())
			require.NotContains(t, diagnostic.String(), plan.directory)
			want := "The API responded, but no images could be saved. Check the image data and output folder before trying again."
			if format == "json" {
				var document map[string]any
				require.NoError(t, json.Unmarshal(diagnostic.Bytes(), &document))
				require.Equal(t, map[string]any{"message": want}, document)
			} else {
				require.Equal(t, want+"\n", diagnostic.String())
			}
		})
	}
}

func TestImagePreferenceCancellationKeepsPathPrivate(t *testing.T) {
	home := filepath.Join(t.TempDir(), "synthetic-private-home")
	for _, key := range []string{"HOME", "APPDATA", "XDG_CONFIG_HOME"} {
		t.Setenv(key, home)
	}
	path, err := imageInlinePreferencePath()
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			root := readableErrorTestCommand(t, "--format-error", format)
			failure := imageInlinePreferenceCommands()[0].Action(ctx, root)
			require.ErrorIs(t, failure, context.Canceled)
			require.Contains(t, failure.Error(), "Could not save the inline preference.")
			require.NotContains(t, failure.Error(), path)
			var output bytes.Buffer
			require.NoError(t, ShowCommandError(root, withCommandError(root, failure), &output))
			assertReadableErrorContainsNoPrivateDetails(t, output.String())
			require.NotContains(t, output.String(), path)
			if format == "json" {
				var document map[string]any
				require.NoError(t, json.Unmarshal(output.Bytes(), &document))
				require.Equal(t, map[string]any{"message": "Request canceled."}, document)
			} else {
				require.Equal(t, "Request canceled.\n", output.String())
			}
			require.ErrorIs(t, failure, context.Canceled)
			_, err := os.Stat(path)
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestSavedImageCancellationOutputModes(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	plan := &imageOutputPlan{directory: t.TempDir(), name: "robot", inline: "off"}
	failure := plan.save(ctx, []byte(`{"data":[]}`), io.Discard)
	require.ErrorIs(t, failure, context.Canceled)
	want := "Request canceled."
	for _, test := range []struct {
		name, format string
		args         []string
	}{
		{"auto", "text", []string{"--format-error", "auto"}},
		{"JSON lines", "json", []string{"--format-error", "jsonl"}},
		{"raw JSON", "json", []string{"--format-error", "raw"}},
		{"YAML", "yaml", []string{"--format-error", "yaml"}},
		{"extracted message", "string", []string{"--format-error", "json", "--transform-error", "message"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := readableErrorTestCommand(t, test.args...)
			var output bytes.Buffer
			require.NoError(t, ShowCommandError(root, failure, &output))
			switch test.format {
			case "text":
				require.Equal(t, want+"\n", output.String())
			case "string":
				var message string
				require.NoError(t, json.Unmarshal(output.Bytes(), &message))
				require.Equal(t, want, message)
			default:
				var document map[string]any
				if test.format == "yaml" {
					require.NoError(t, yaml.Unmarshal(output.Bytes(), &document))
				} else {
					require.NoError(t, json.Unmarshal(output.Bytes(), &document))
				}
				require.Equal(t, map[string]any{"message": want}, document)
			}
			sinkErr := errors.New("synthetic diagnostic sink failure")
			require.ErrorIs(t, ShowCommandError(root, failure, errorSink{sinkErr}), sinkErr)
			require.ErrorIs(t, failure, context.Canceled)
		})
	}
}

func TestSavedImageCancellationAfterReceiptKeepsSavedResult(t *testing.T) {
	var fixture bytes.Buffer
	require.NoError(t, png.Encode(&fixture, image.NewRGBA(image.Rect(0, 0, 1, 1))))
	encoded := base64.StdEncoding.EncodeToString(fixture.Bytes())
	response := gjson.Parse(fmt.Sprintf(`{"data":[{"b64_json":%q}]}`, encoded))
	for _, inline := range []string{"off", "auto"} {
		for _, test := range []struct {
			name, format string
			args         []string
		}{
			{"text", "text", []string{"--format-error", "text"}},
			{"JSON", "json", []string{"--format-error", "json"}},
			{"auto", "text", []string{"--format-error", "auto"}},
			{"JSON lines", "json", []string{"--format-error", "jsonl"}},
			{"raw JSON", "json", []string{"--format-error", "raw"}},
			{"YAML", "yaml", []string{"--format-error", "yaml"}},
			{"extracted message", "string", []string{"--format-error", "json", "--transform-error", "message"}},
		} {
			t.Run(inline+"/"+test.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				plan := &imageOutputPlan{directory: t.TempDir(), name: "synthetic-private-image", inline: inline}
				path := filepath.Join(plan.directory, "synthetic-private-image.png")
				var receipt bytes.Buffer
				receiptCalls := 0
				// This writer is nonterminal, so auto does not request a preview.
				writer := loadingTestWriter(func(data []byte) (int, error) {
					receiptCalls++
					require.NoError(t, ctx.Err(), "cancel only after saving and printing the completed path")
					completed, err := os.ReadFile(path)
					require.NoError(t, err)
					require.Equal(t, fixture.Bytes(), completed)
					n, err := receipt.Write(data)
					cancel()
					return n, err
				})
				presentation := context.WithValue(ctx, imagePresentationKey{}, imagePresentation{plan, writer})
				failure := ShowJSON(response, ShowJSONOpts{
					Context: presentation, Operation: imageGenerationOperation, OutputKind: OutputResponse,
					Format: "auto", Stdout: writer, Stderr: io.Discard,
				})
				require.ErrorIs(t, failure, context.Canceled)
				require.Equal(t, 1, receiptCalls)
				require.Equal(t, fmt.Sprintf("Saved image: %q\n", path), receipt.String())
				completed, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, fixture.Bytes(), completed)
				entries, err := os.ReadDir(plan.directory)
				require.NoError(t, err)
				require.Len(t, entries, 1)
				require.Equal(t, filepath.Base(path), entries[0].Name())
				root := readableErrorTestCommand(t, test.args...)
				for _, interrupted := range []bool{false, true} {
					wrapped := failure
					wantStatus := 1 // main's fallback; no native process execution is claimed here.
					if interrupted {
						wrapped = &imageLoadingInterrupt{failure}
						wantStatus = 130
					}
					wrapped = withCommandError(root, wrapped)
					require.ErrorIs(t, wrapped, context.Canceled)
					status := 1
					var exit cli.ExitCoder
					if errors.As(wrapped, &exit) {
						status = exit.ExitCode()
					}
					require.Equal(t, wantStatus, status)
					var diagnostic bytes.Buffer
					require.NoError(t, ShowCommandError(root, wrapped, &diagnostic))
					assertReadableErrorContainsNoPrivateDetails(t, diagnostic.String())
					require.NotContains(t, diagnostic.String(), plan.directory)
					require.NotContains(t, diagnostic.String(), encoded)
					message := diagnostic.String()
					want := "Images were saved, but the command was canceled. Use the saved files; no need to generate again."
					switch test.format {
					case "text":
						want += "\n"
					case "string":
						require.NoError(t, json.Unmarshal(diagnostic.Bytes(), &message))
					default:
						var document map[string]any
						if test.format == "yaml" {
							require.NoError(t, yaml.Unmarshal(diagnostic.Bytes(), &document))
						} else {
							require.NoError(t, json.Unmarshal(diagnostic.Bytes(), &document))
						}
						require.Len(t, document, 1, "do not invent an HTTP status")
						var ok bool
						message, ok = document["message"].(string)
						require.True(t, ok)
					}
					t.Logf("interrupted=%t status_contract=%d kept_bytes=%d receipt_calls=%d diagnostic=%q", interrupted, status, len(completed), receiptCalls, message)
					if message != want {
						t.Errorf("saved-result cancellation claimed a preview failure: got %q; want %q", message, want)
					}
				}
			})
		}
	}
}
