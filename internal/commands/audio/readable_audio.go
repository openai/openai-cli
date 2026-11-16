package audio

import (
	"bytes"
	"context"
	"io"
	"mime"
	"net/http"

	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/urfave/cli/v3"
)

type audioTextKey struct{}
type audioTextBody struct {
	bytes.Buffer
	present bool
}

// ConfigureText prepares capture because generated audio handlers parse their captured response as JSON even for text
// and subtitles. Retain those bytes while the SDK reads them, without changing
// generated code, the request, or the response consumed by the SDK.
func ConfigureText(root *cli.Command) {
	for _, name := range []string{"audio:transcriptions", "audio:translations"} {
		resource := root.Command(name)
		if resource == nil || resource.Command("create") == nil {
			continue
		}
		command := resource.Command("create")
		next := command.Action
		command.Action = func(ctx context.Context, command *cli.Command) error {
			return next(context.WithValue(ctx, audioTextKey{}, &audioTextBody{}), command)
		}
	}
}

// CaptureText retains successful transcript text as the SDK consumes its response.
func CaptureText(request *http.Request, next func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	capture, _ := request.Context().Value(audioTextKey{}).(*audioTextBody)
	if capture != nil {
		capture.Reset()
		capture.present = false
	}
	response, err := next(request)
	if err != nil || response == nil || capture == nil || response.Body == nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return response, err
	}
	kind, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	switch kind {
	case "text/plain", "text/srt", "text/vtt", "application/x-subrip":
		capture.present = true
		response.Body = &capturedAudioBody{Reader: io.TeeReader(response.Body, capture), Closer: response.Body}
	}
	return response, err
}

type capturedAudioBody struct {
	io.Reader
	io.Closer
}

// TextResult selects captured text only for successful transcription and translation results.
func TextResult(ctx context.Context, operation string, kind transformers.OutputKind) (string, bool) {
	if kind != transformers.OutputResponse ||
		(operation != "(resource) audio.transcriptions > (method) create" && operation != "(resource) audio.translations > (method) create") {
		return "", false
	}
	body, _ := ctx.Value(audioTextKey{}).(*audioTextBody)
	if body == nil || !body.present {
		return "", false
	}
	return body.String(), true
}
