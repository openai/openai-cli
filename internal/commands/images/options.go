// Package images owns handwritten image workflows, saving, previews, and picker state.
package images

import (
	"context"
	"io"

	"github.com/openai/openai-cli/internal/apiquery"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// RequestOptions prepares a generated request using assembly-owned hooks.
type RequestOptions func(*cli.Command, apiquery.NestedQueryFormat, apiquery.ArrayQueryFormat, requestconfig.BodyContentType, bool, ...func([]byte)) ([]option.RequestOption, error)

// ModelOptions connects image model discovery to application request and output policy.
type ModelOptions struct {
	RequestOptions        RequestOptions
	DefaultRequestOptions func(*cli.Command) []option.RequestOption
	HumanOutput           func(*cli.Command) bool
	DiagnosticsAllowed    func(context.Context) bool
	ShowData              func(context.Context, *cli.Command, gjson.Result, io.Writer) error
}

// SavingOptions supplies policy while the feature owns preparation and image lifetime.
type SavingOptions struct {
	RequestOptions     RequestOptions
	DiagnosticsAllowed func(context.Context) bool
	ErrorFormat        func(*cli.Command) string
}

// PreviewOptions connects local previews to receipt and error presentation.
type PreviewOptions struct {
	Receipt   func(context.Context, string) error
	ShowError func(*cli.Command, error, io.Writer) error
}
