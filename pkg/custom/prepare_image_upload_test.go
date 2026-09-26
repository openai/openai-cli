package custom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-cli/internal/apiform"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestImageMultipartScalarInspectionPreservesText(t *testing.T) {
	for _, tc := range []struct {
		name, field, text, inspectedJSON string
	}{
		{"count", "n", "2", "2"},
		{"count whitespace", "n", " 2\r\n", "2"},
		{"partial count", "partial_images", "2", "2"},
		{"stream true", "stream", "true", "true"},
		{"stream false", "stream", "false", "false"},
		{"null", "n", "null", "null"},
		{"invalid number", "n", "2oops", `"2oops"`},
		{"quoted number", "n", `"2"`, `"\"2\""`},
		{"array", "n", "[2]", `"[2]"`},
		{"prompt", "prompt", "2", `"2"`},
		{"model", "model", "null", `"null"`},
	} {
		for _, fromFile := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/file=%v", tc.name, fromFile), func(t *testing.T) {
				var value any = tc.text
				if fromFile {
					value = strings.NewReader(tc.text)
				}
				inspected, replay, err := inspectImageMultipartSetting(t.Context(), tc.field, value)
				require.NoError(t, err)
				encoded, err := json.Marshal(inspected)
				require.NoError(t, err)
				require.JSONEq(t, tc.inspectedJSON, string(encoded))
				if fromFile {
					data, err := io.ReadAll(replay.(io.Reader))
					require.NoError(t, err)
					require.Equal(t, tc.text, string(data))
				} else {
					require.Equal(t, tc.text, replay)
				}
			})
		}
	}
}

func TestImageMultipartScalarInspectionPreservesFileMetadataAndOwnership(t *testing.T) {
	source := &recordingReadCloser{reader: strings.NewReader("A purple sky\r\n")}
	original := fileUpload{Reader: source, filename: "prompt.txt", contentType: "text/plain"}
	inspected, replay, err := inspectImageMultipartSetting(t.Context(), "prompt", original)
	require.NoError(t, err)
	require.Equal(t, "A purple sky\r\n", inspected)
	upload := replay.(fileUpload)
	require.Equal(t, original.filename, upload.filename)
	require.Equal(t, original.contentType, upload.contentType)
	require.EqualValues(t, len("A purple sky\r\n"), upload.size)
	require.True(t, upload.knownSize)
	contents, err := io.ReadAll(upload)
	require.NoError(t, err)
	require.Equal(t, "A purple sky\r\n", string(contents))
	require.NoError(t, closeFileUploads(replay))
	require.EqualValues(t, 1, source.closeCount.Load())
}

func TestImageMultipartScalarInspectionReadFailureIsPrivateAndClosesSource(t *testing.T) {
	readErr := errors.New("synthetic-private-file-contents")
	closeErr := errors.New("synthetic-private-close-error")
	source := &recordingReadCloser{reader: errorReader{err: readErr}, closeErr: closeErr}
	_, replay, err := inspectImageMultipartSetting(t.Context(), "response_format", fileUpload{Reader: source})
	require.ErrorContains(t, err, "--response-format")
	require.ErrorIs(t, err, readErr)
	require.ErrorIs(t, err, closeErr)
	require.NotContains(t, err.Error(), "synthetic-private")
	require.NoError(t, closeFileUploads(replay))
	require.EqualValues(t, 1, source.closeCount.Load())
}

func TestImageMultipartScalarInspectionCancellationReleasesOwnedRead(t *testing.T) {
	source := &closeUnblocksReader{readStarted: make(chan struct{}), closed: make(chan struct{}), err: errors.New("closed")}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := inspectImageMultipartSetting(ctx, "prompt", fileUpload{Reader: source})
		done <- err
	}()
	select {
	case <-source.readStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("scalar inspection did not start")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not release scalar source")
	}
}

func TestImageUploadPreparationClosesWhenActionAborts(t *testing.T) {
	for _, cancelAction := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelAction), func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "source.png")
			require.NoError(t, os.WriteFile(source, []byte("synthetic-source"), 0600))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("synthetic action failure")
			var upload fileUpload
			var prepared *multipartRequestBody
			command := &cli.Command{Name: "edit", Reader: strings.NewReader(""), Writer: io.Discard,
				Flags: []cli.Flag{&requestflag.Flag[string]{Name: "image", BodyPath: "image", FileInput: true}},
				Action: imageSavingWorkflow(func(ctx context.Context, command *cli.Command) error {
					state := command.Metadata[imageMultipartMetadata].(*imageMultipartPreparation)
					prepared = state.body.(*multipartRequestBody)
					upload = prepared.bodyMap["image"].(fileUpload)
					if cancelAction {
						cancel()
						return ctx.Err()
					}
					return failure
				}),
			}
			registerImageSavingFlags(command)
			err := command.Run(ctx, []string{"edit", "--image", source, "--output-dir", t.TempDir()})
			if cancelAction {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, failure)
			}
			require.NotNil(t, prepared)
			_, err = io.ReadAll(upload)
			require.ErrorIs(t, err, os.ErrClosed)
			select {
			case <-prepared.done:
			case <-time.After(5 * time.Second):
				t.Fatal("unconsumed multipart encoder did not stop")
			}
		})
	}
}

func TestImageUploadPreparedBodyClosesOnce(t *testing.T) {
	source := &recordingReadCloser{reader: strings.NewReader("synthetic image")}
	var body io.Closer
	_, err := multipartRequestOptions(map[string]any{"image": fileUpload{Reader: source}}, apiform.FormatBrackets, func(prepared io.Closer) { body = prepared })
	require.NoError(t, err)
	require.NotNil(t, body)
	require.NoError(t, body.Close())
	require.NoError(t, body.Close())
	require.EqualValues(t, 1, source.closeCount.Load())
}
