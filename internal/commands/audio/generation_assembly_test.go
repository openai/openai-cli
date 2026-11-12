package audio_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-cli/generated/commands"
	"github.com/openai/openai-cli/internal/apiquery"
	appconfig "github.com/openai/openai-cli/internal/app"
	"github.com/openai/openai-cli/internal/commands/audio"
	"github.com/openai/openai-cli/internal/modeldefaults"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func buildAudioRequestOptions(command *cli.Command) ([]option.RequestOption, error) {
	return appconfig.FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, appconfig.ApplicationJSON, true)
}

func TestAudioGenerationPreservesLiteralEditorValuesAndRestoresFlags(t *testing.T) {
	audio.CheckAudioGenerationPreservesLiteralEditorValuesAndRestoresFlags(t, buildAudioRequestOptions)
}

func TestAudioGenerationSendsSelectedRequestSettings(t *testing.T) {
	audio.CheckAudioGenerationSendsSelectedRequestSettings(t, buildAudioRequestOptions)
}

func TestAudioActionsGenerationStoresOnlySentInstructions(t *testing.T) {
	audio.CheckAudioActionsGenerationStoresOnlySentInstructions(t, buildAudioRequestOptions)
}

func TestFreshSpeakComposerRequest(t *testing.T) {
	requests := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requests <- r:
		default:
			t.Error("unexpected extra speech request")
		}
		http.Error(w, "synthetic rejection after request capture", http.StatusForbidden)
	}))
	defer server.Close()

	// Use a fresh command tree and the generated endpoint handler.
	generated := commands.Command.Command("audio:speech").Command("create")
	command := &cli.Command{
		Name: "create", Flags: generated.Flags,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return audio.GenerateFreshSpeechForTest(t, ctx, cmd, generated.Action)
		},
	}
	root := &cli.Command{
		Name:           "openai",
		Writer:         io.Discard,
		Flags:          []cli.Flag{&cli.StringFlag{Name: "base-url"}, &cli.StringFlag{Name: "api-key"}},
		Commands:       []*cli.Command{{Name: "audio:speech", Commands: []*cli.Command{command}}},
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
	}
	audio.ConfigureSpeech(root, func(context.Context, *cli.Command) audio.SpeechPresentation {
		return audio.SpeechPresentation{}
	})
	t.Setenv("OPENAI_BASE_URL", server.URL+"/v1")
	t.Setenv("OPENAI_API_KEY", "synthetic-speech-key")
	err := root.Run(t.Context(), []string{"openai", "--base-url", server.URL + "/v1", "--api-key", "synthetic-speech-key", "audio:speech", "create"})
	require.Error(t, err)
	select {
	case r := <-requests:
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/v1/realtime", r.URL.Path)
		require.Equal(t, modeldefaults.Speech, r.URL.Query().Get("model"))
	default:
		t.Fatalf("the composer did not reach the local endpoint: %v", err)
	}
}
