package custom

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImageLoadingSaveStagesFollowRetainedFiles(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nsynthetic image"))
	valid := fmt.Sprintf(`{"b64_json":%q}`, encoded)
	for _, test := range []struct {
		name     string
		response string
		cancel   bool
		count    int
		wantErr  bool
	}{
		{"success", `{"data":[` + valid + `]}`, false, 1, false},
		{"multiple", `{"data":[` + valid + `,` + valid + `]}`, false, 2, false},
		{"partial failure", `{"data":[` + valid + `,{"b64_json":"invalid"}]}`, false, 1, true},
		{"invalid image", `{"data":[{"b64_json":"invalid"}]}`, false, 0, true},
		{"empty response", `{"data":[]}`, false, 0, true},
		{"canceled", `{"data":[` + valid + `]}`, true, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.cancel {
				cancel()
			}
			folder := t.TempDir()
			var stages []imageLoadingStage
			stopped := false
			plan := &imageOutputPlan{directory: folder, name: "synthetic", inline: "none"}
			plan.loadingStage = func(stage imageLoadingStage) {
				require.False(t, stopped)
				stages = append(stages, stage)
				if stage == imageLoadingSaved {
					files, err := filepath.Glob(filepath.Join(folder, "*.png"))
					require.NoError(t, err)
					require.Len(t, files, test.count)
					for _, path := range files {
						contents, err := os.ReadFile(path)
						require.NoError(t, err)
						require.NotEmpty(t, contents)
					}
				}
			}
			plan.stopLoading = func() { stopped = true }
			var output bytes.Buffer
			err := plan.save(ctx, []byte(test.response), &output)
			if test.wantErr {
				require.Error(t, err)
				require.Equal(t, []imageLoadingStage{imageLoadingSaving}, stages)
			} else {
				require.NoError(t, err)
				require.Equal(t, []imageLoadingStage{imageLoadingSaving, imageLoadingSaved}, stages)
			}
			require.True(t, stopped)
			files, err := filepath.Glob(filepath.Join(folder, "*.png"))
			require.NoError(t, err)
			require.Len(t, files, test.count)
		})
	}
}

func TestImageLoadingStageWithoutFeedbackIsSafe(t *testing.T) {
	var absent *imageOutputPlan
	absent.setLoadingStage(imageLoadingSaving)
	(&imageOutputPlan{}).setLoadingStage(imageLoadingSaved)
}
