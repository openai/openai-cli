package custom

import (
	"bytes"
	"context"
	"io"
	"mime"
	"net/http"

	"github.com/urfave/cli/v3"
)

type audioTextKey struct{}
type audioTextBody struct {
	bytes.Buffer
	present bool
}

// The generated audio handlers parse their captured response as JSON even when
// the caller requests text or subtitles. Retain that response while the SDK
// reads it, so presentation has the original text without another request or
// changing the generated handler, HTTP response, or request body.
func configureReadableAudio(root *cli.Command) {
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

func captureAudioText(request *http.Request, next func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	response, err := next(request)
	capture, _ := request.Context().Value(audioTextKey{}).(*audioTextBody)
	if err != nil || response == nil || capture == nil || response.Body == nil || response.StatusCode >= 300 {
		return response, err
	}
	kind, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	switch kind {
	case "text/plain", "text/srt", "text/vtt", "application/x-subrip":
		capture.Reset()
		capture.present = true
		response.Body = &capturedAudioBody{Reader: io.TeeReader(response.Body, capture), Closer: response.Body}
	}
	return response, err
}

type capturedAudioBody struct {
	io.Reader
	io.Closer
}

func audioTextResult(opts ShowJSONOpts) (string, bool) {
	if opts.Context == nil {
		return "", false
	}
	body, _ := opts.Context.Value(audioTextKey{}).(*audioTextBody)
	if body == nil || !body.present {
		return "", false
	}
	return body.String(), true
}
