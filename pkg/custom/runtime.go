// Package custom temporarily adapts Castiron-generated callers to application
// policy. Remove this package when the authoritative producer updates its imports.
package custom

import (
	"io"
	"net/http"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/app"
	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

type BodyContentType = app.BodyContentType
type ShowJSONOpts = app.ShowJSONOpts

const (
	EmptyBody              = app.EmptyBody
	MultipartFormEncoded   = app.MultipartFormEncoded
	ApplicationJSON        = app.ApplicationJSON
	ApplicationOctetStream = app.ApplicationOctetStream
	Version                = app.Version
	OutputResponse         = app.OutputResponse
	OutputPageItem         = app.OutputPageItem
	OutputStreamEvent      = app.OutputStreamEvent
)

var OutputFormats = app.OutputFormats

func GetDefaultRequestOptions(cmd *cli.Command) []option.RequestOption {
	return app.GetDefaultRequestOptions(cmd)
}

func FlagOptions(cmd *cli.Command, nested apiquery.NestedQueryFormat, array apiquery.ArrayQueryFormat, body BodyContentType, ignoreStdin bool) ([]option.RequestOption, error) {
	return app.FlagOptions(cmd, nested, array, body, ignoreStdin)
}

func WriteBinaryResponse(response *http.Response, stdout io.Writer, outfile string) (string, error) {
	return app.WriteBinaryResponse(response, stdout, outfile)
}

func ValidateBaseURL(value, source string) error {
	return app.ValidateBaseURL(value, source)
}

func ShowJSON(res gjson.Result, opts ShowJSONOpts) error {
	return app.ShowJSON(res, opts)
}

func ShowJSONIterator[T any](iter jsonview.Iterator[T], limit int64, opts ShowJSONOpts) error {
	return app.ShowJSONIterator(iter, limit, opts)
}

func NewRequestHeaderFlag() cli.Flag {
	return app.NewRequestHeaderFlag()
}
