package images

import (
	"context"
	"io"

	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-cli/internal/output/transform"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// AssemblyForTest exercises private feature state against real application policy.
type AssemblyForTest struct {
	Configure        func(*cli.Command)
	ShowError        func(*cli.Command, error, io.Writer) error
	ErrorMessage     func(*cli.Command, error) string
	WrapError        func(*cli.Command, error) error
	ShowJSON         func(gjson.Result, PresentationForTest) error
	ShowJSONIterator func(jsonview.Iterator[outputJSON], int64, PresentationForTest) error
	Saving           SavingOptions
	Models           ModelOptions
	Preview          PreviewOptions
}

type PresentationForTest struct {
	Context              context.Context
	Operation            string
	OutputKind           transform.OutputKind
	ExplicitFormat       bool
	Format               string
	RawOutput            bool
	Stderr               io.Writer
	Stdout               io.Writer
	Title                string
	Transform            string
	CallerHandlesSignals bool
}
