package app

import (
	"io"
	"os"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/commands/admin"
	"github.com/openai/openai-cli/internal/commands/audio"
	"github.com/openai/openai-cli/internal/commands/images"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-go/v3/option"
	"github.com/urfave/cli/v3"
)

// Temporary aliases for the generated runtime bridge. Migrate generated callers
// through their producer before removing these names.
type BodyContentType = requestconfig.BodyContentType

const (
	EmptyBody              = requestconfig.EmptyBody
	MultipartFormEncoded   = requestconfig.MultipartFormEncoded
	ApplicationJSON        = requestconfig.ApplicationJSON
	ApplicationOctetStream = requestconfig.ApplicationOctetStream
)

func FlagOptions(
	cmd *cli.Command,
	nestedFormat apiquery.NestedQueryFormat,
	arrayFormat apiquery.ArrayQueryFormat,
	bodyType BodyContentType,

	// This parameter is true if stdin is already in use to pass a binary parameter by using the special value
	// "-". In this case, we won't attempt to read it as a JSON/YAML blob for options setting.
	ignoreStdin bool,
	observeJSON ...func([]byte),
) (options []option.RequestOption, err error) {
	return flagOptionsWithStdin(cmd, nestedFormat, arrayFormat, bodyType, ignoreStdin, os.Stdin, observeJSON...)
}

// Keep request parsing and stdin ownership shared when a local workflow needs
// a cancellable reader. Generated callers retain their ordinary stdin reader.
func flagOptionsWithStdin(
	cmd *cli.Command,
	nestedFormat apiquery.NestedQueryFormat,
	arrayFormat apiquery.ArrayQueryFormat,
	bodyType BodyContentType,
	ignoreStdin bool,
	stdin io.Reader,
	observeJSON ...func([]byte),
) (options []option.RequestOption, err error) {
	if prepared, ok, err := images.ConsumeSavingRequest(cmd, nestedFormat, arrayFormat, bodyType, ignoreStdin); ok {
		return prepared, err
	}
	hooks := requestconfig.PreparationHooks{
		PreflightMultipart:  func(body map[string]any) error { return audio.PreflightVoiceBody(cmd, body) },
		ValidateCredentials: func() error { return admin.CheckCredentials(cmd) },
		Encoding: requestconfig.EncodingHooks{
			PrepareMultipart: func(body map[string]any) error { return images.PrepareMultipartBody(cmd, body) },
			ValidateJSON:     func(body []byte) error { return validateProjectAPIKeyUpdate(cmd, body) },
			ObserveJSON:      observeJSON,
			ObserveMultipart: func(body io.Closer) { images.ObserveMultipart(cmd, body) },
		},
	}
	if audio.IsVoiceAction(cmd) {
		hooks.EmbedMultipart = func(body map[string]any, stdin *requestconfig.OnceStdinReader) (map[string]any, error) {
			return audio.EmbedVoiceBody(cmd, body, stdin)
		}
	}
	return requestconfig.FlagOptions(cmd, nestedFormat, arrayFormat, bodyType, ignoreStdin, stdin, hooks)
}
