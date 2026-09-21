// File generated from our OpenAPI spec by Castiron. See CONTRIBUTING.md for details.

package cmd

import (
	"io"
	"net/http"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-cli/pkg/custom"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

type BodyContentType = custom.BodyContentType
type ShowJSONOpts = custom.ShowJSONOpts

const (
	EmptyBody              = custom.EmptyBody
	MultipartFormEncoded   = custom.MultipartFormEncoded
	ApplicationJSON        = custom.ApplicationJSON
	ApplicationOctetStream = custom.ApplicationOctetStream
	Version                = custom.Version

	outputResponse    = custom.OutputResponse
	outputPageItem    = custom.OutputPageItem
	outputStreamEvent = custom.OutputStreamEvent
)

var OutputFormats = custom.OutputFormats

func configureCustomCommand(root *cli.Command) {
	custom.ConfigureCommand(root)
}

func getDefaultRequestOptions(cmd *cli.Command) []option.RequestOption {
	return custom.GetDefaultRequestOptions(cmd)
}

func flagOptions(
	cmd *cli.Command,
	nestedFormat apiquery.NestedQueryFormat,
	arrayFormat apiquery.ArrayQueryFormat,
	bodyType BodyContentType,
	ignoreStdin bool,
) ([]option.RequestOption, error) {
	return custom.FlagOptions(cmd, nestedFormat, arrayFormat, bodyType, ignoreStdin)
}

func writeBinaryResponse(response *http.Response, stdout io.Writer, outfile string) (string, error) {
	return custom.WriteBinaryResponse(response, stdout, outfile)
}

func ValidateBaseURL(value, source string) error {
	return custom.ValidateBaseURL(value, source)
}

func ShowJSON(res gjson.Result, opts ShowJSONOpts) error {
	return custom.ShowJSON(res, opts)
}

func ShowJSONIterator[T any](iter jsonview.Iterator[T], itemsToDisplay int64, opts ShowJSONOpts) error {
	return custom.ShowJSONIterator(iter, itemsToDisplay, opts)
}

func NewRequestHeaderFlag() cli.Flag {
	return custom.NewRequestHeaderFlag()
}
