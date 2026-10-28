package images_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	appconfig "github.com/openai/openai-cli/internal/app"
	"github.com/openai/openai-cli/internal/commands/images"
	"github.com/stretchr/testify/require"
)

func TestImagePickerDisplayFailurePresentation(t *testing.T) {
	for _, cause := range []error{context.DeadlineExceeded, io.ErrClosedPipe, context.Canceled} {
		for _, format := range []string{"text", "json"} {
			t.Run(cause.Error()+"/"+format, func(t *testing.T) {
				private := fmt.Errorf("synthetic-private-path\x1b[31m: %w", cause)
				failure := errors.Join(images.TestImagePickerDisplayFailure(private), io.ErrClosedPipe)
				require.ErrorIs(t, failure, cause)
				root := readableErrorTestCommand(t, "--format-error", format)
				var output strings.Builder
				require.NoError(t, appconfig.ShowCommandError(root, failure, &output))
				message := strings.TrimSpace(output.String())
				if format == "json" {
					var payload struct{ Message string }
					require.NoError(t, json.Unmarshal([]byte(message), &payload))
					message = payload.Message
				}
				want := "Could not display the image picker. Saved images remain available."
				if cause == context.Canceled {
					want = "Request canceled."
				}
				require.Equal(t, want, message)
			})
		}
	}
}
