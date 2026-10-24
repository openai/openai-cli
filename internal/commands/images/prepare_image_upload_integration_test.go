package images_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/commands/images"
	"github.com/openai/openai-cli/internal/requestflag"
	appconfig "github.com/openai/openai-cli/internal/app"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestImageUploadPreparationClosesWhenActionAborts(t *testing.T) {
	for _, cancelAction := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelAction), func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "source.png")
			require.NoError(t, os.WriteFile(source, []byte("synthetic-source"), 0600))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("synthetic action failure")
			var prepared io.ReadCloser
			command := &cli.Command{Name: "edit", Reader: strings.NewReader(""), Writer: io.Discard,
				Flags: []cli.Flag{&requestflag.Flag[string]{Name: "image", BodyPath: "image", FileInput: true}},
				Action: images.TestImageSavingWorkflow(func(ctx context.Context, command *cli.Command) error {
					prepared = images.PreparedBodyForTest(command)
					if cancelAction {
						cancel()
						return ctx.Err()
					}
					return failure
				}, appconfig.ImageSavingOptions()),
			}
			images.TestRegisterImageSavingFlags(command)
			err := command.Run(ctx, []string{"edit", "--image", source, "--output-dir", t.TempDir()})
			if cancelAction {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, failure)
			}
			require.NotNil(t, prepared)
			_, err = io.ReadAll(prepared)
			require.ErrorIs(t, err, io.ErrClosedPipe)
		})
	}
}
