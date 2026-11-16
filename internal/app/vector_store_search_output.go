package app

import (
	"context"

	"github.com/openai/openai-cli/internal/apiquery"
	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-cli/internal/commands/vectorstores"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/pagination"
	"github.com/urfave/cli/v3"
)

const vectorStoreSearchOperation = "(resource) vector_stores > (method) search"

func configureVectorStoreSearch(root *cli.Command) {
	group := root.Command("vector-stores")
	if group == nil {
		return
	}
	if search := group.Command("search"); search != nil {
		original := search.Action
		search.Action = func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Root().String("format") == "raw" {
				return original(ctx, cmd)
			}
			return searchVectorStore(ctx, cmd)
		}
	}
}

func searchVectorStore(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args().Slice()
	if !cmd.IsSet("vector-store-id") && len(args) > 0 {
		if err := cmd.Set("vector-store-id", args[0]); err != nil {
			return err
		}
		args = args[1:]
	}
	if len(args) > 0 {
		return &localcommands.LocalError{Message: parserErrorGuidance(cmd, "Unexpected extra arguments.")}
	}
	options, err := FlagOptions(cmd, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, ApplicationJSON, false)
	if err != nil {
		return err
	}
	client := openai.NewClient(GetDefaultRequestOptions(cmd)...)
	page, err := client.VectorStores.Search(ctx, cmd.String("vector-store-id"), openai.VectorStoreSearchParams{}, options...)
	if err != nil {
		return err
	}
	// The SDK pager treats missing or malformed data as an empty result.
	// Validate the first page before handing it to the same pager and renderer.
	if page == nil || !page.JSON.Data.Valid() {
		return &localcommands.LocalError{Message: "The API returned an invalid vector store search response. Expected a data array."}
	}
	limit := int64(-1)
	if cmd.IsSet("max-items") {
		limit = cmd.Int64("max-items")
	}
	opts := ShowJSONOpts{
		Context:        ctx,
		Operation:      vectorStoreSearchOperation,
		OutputKind:     OutputPageItem,
		ExplicitFormat: cmd.Root().IsSet("format"),
		Format:         cmd.Root().String("format"),
		RawOutput:      cmd.Root().Bool("raw-output"),
		Title:          "vector-stores search",
		Transform:      cmd.Root().String("transform"),
	}
	if opts.Transform == "" && !opts.RawOutput && resolvedOutputFormat(opts) == "text" {
		for i, result := range page.Data {
			if int64(i) == limit {
				break
			}
			if err := vectorstores.ValidateSearchResult(result); err != nil {
				return err
			}
		}
	}
	return ShowJSONIterator(pagination.NewPageAutoPager(page, nil), limit, opts)
}

// Empty search results do not establish the indexing state. Keep this wording
// specific to successful, untransformed search output; callers check errors first.
func vectorStoreSearchEmptyText(opts ShowJSONOpts) (string, bool) {
	if opts.Operation != vectorStoreSearchOperation ||
		opts.OutputKind != OutputPageItem || opts.Transform != "" || opts.RawOutput {
		return "", false
	}
	return "No matches returned.\nIndexing state was not checked.", true
}
