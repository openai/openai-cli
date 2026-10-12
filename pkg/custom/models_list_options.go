package custom

import (
	"context"
	"regexp"
	"strings"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/urfave/cli/v3"
)

type modelsListOptionsKey struct{}

type modelsListOptions struct {
	selection transformers.ModelSelection
	applied   bool
}

// Messages contain only fixed guidance, never filter values or response data.
type modelsListError struct{ message string }

func (e *modelsListError) Error() string { return e.message }

func configureModelsList(root *cli.Command) {
	resource := root.Command("models")
	if resource == nil || resource.Command("list") == nil {
		return
	}
	command := resource.Command("list")
	for _, flag := range command.Flags {
		if limit, ok := flag.(*requestflag.Flag[int64]); ok && limit.Name == "max-items" {
			limit.DefaultText = "unlimited"
		}
	}
	command.Usage = "List accessible model IDs and owners."
	command.Description = `Shows IDs and owners. Narrow terminals keep both values in labeled output.
Large results use Space/b/q navigation; p prints all selected records.
Slow interactive requests show loading feedback.
Use --filter and --sort-by to select models before --max-items limits the result.
Without these flags, --max-items selects response-order records before display sorting.
These controls use the loaded response and make no additional API requests.
Matches are case-sensitive by default. Regex matches anywhere; use ^ and $ to anchor it.
Quote the whole filter expression for your shell. Example: --filter 'id = "model name"'.
Whitespace outside operand quotes now separates syntax; quotes delimit text instead of matching literal quote characters.
Use quoted operands for literal spaces or Boolean-looking text. Escape matching quotes and backslashes with a backslash.
Filter escapes are decoded before RE2. To pass two backslashes to RE2, write four in the filter.
Only id=VALUE and id~REGEX are supported. Boolean expressions and operand lists are not supported.
Use --format json for complete records. Filtering and sorting do not support --format raw.`
	command.Flags = append(command.Flags,
		&cli.StringFlag{Name: "filter", Usage: "Select IDs with id=VALUE (exact) or id~REGEX (case-sensitive RE2). Quote the expression."},
		&cli.StringFlag{Name: "sort-by", Value: "id", Usage: "Sort selected IDs: id (ascending) or ~id (descending), before --max-items."},
	)
	next := command.Action
	command.Action = func(ctx context.Context, command *cli.Command) error {
		if !command.IsSet("filter") && !command.IsSet("sort-by") {
			return runWithModelsListLoading(ctx, command, next)
		}
		options, err := parseModelsListOptions(command.String("filter"), command.IsSet("filter"), command.String("sort-by"))
		if err != nil {
			return err
		}
		if strings.EqualFold(command.Root().String("format"), "raw") {
			return &modelsListError{"--filter and --sort-by do not support --format raw. Use --format json for selected complete records."}
		}
		return runWithModelsListLoading(context.WithValue(ctx, modelsListOptionsKey{}, options), command, next)
	}
}

func parseModelsListOptions(filter string, filterSet bool, order string) (modelsListOptions, error) {
	options := modelsListOptions{}
	switch order {
	case "id":
	case "~id":
		options.selection.Descending = true
	default:
		return options, &modelsListError{"--sort-by accepts id or ~id. Quote ~id for descending order."}
	}
	if !filterSet {
		return options, nil
	}
	operator, value, err := parseModelsListFilter(filter)
	if err != nil {
		return options, err
	}
	switch operator {
	case '=':
		options.selection.ExactID = &value
	case '~':
		pattern, err := regexp.Compile(value)
		if err != nil {
			return options, &modelsListError{"--filter has an invalid regular expression. Use id~REGEX with RE2 syntax."}
		}
		options.selection.Pattern = pattern
	}
	return options, nil
}
