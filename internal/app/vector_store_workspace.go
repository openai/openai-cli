package app

import (
	"context"
	"errors"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/commands/vectorstores"
	"github.com/openai/openai-go/v3"
	"github.com/urfave/cli/v3"
)

func configureVectorStoreWorkspace(root *cli.Command) {
	vectorstores.Configure(root, vectorstores.Options{
		NewService: func(ctx context.Context, command *cli.Command) (vectorstores.Service, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			// Reserve terminal input for the workspace. Reuse ordinary request
			// configuration for credentials, headers, project, endpoint and mTLS.
			options, err := FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, EmptyBody, true)
			if err != nil {
				return nil, err
			}
			client := openai.NewClient(append(GetDefaultRequestOptions(command), options...)...)
			return vectorstores.NewService(client), nil
		},
		ErrorMessage: func(err error) string {
			var apierr *openai.Error
			if errors.As(err, &apierr) {
				return readableAPIErrorMessage(root, err, apierr)
			}
			return localErrorMessage(root, err)
		},
	})
}
