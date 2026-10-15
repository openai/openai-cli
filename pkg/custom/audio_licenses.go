package custom

import (
	"context"
	_ "embed"

	"github.com/urfave/cli/v3"
)

//go:embed AUDIO_LICENSES.txt
var audioLicenses string

func handleAudioLicenses(ctx context.Context, command *cli.Command) error {
	if command.Args().Present() {
		return audioFailure("Audio licenses takes no positional arguments.", nil)
	}
	return showAudioData(ctx, command, map[string]string{"licenses": audioLicenses})
}
