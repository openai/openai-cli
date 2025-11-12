// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"context"
	"fmt"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

var fineTuningJobsCreate = requestflag.WithInnerFlags(cli.Command{
	Name:    "create",
	Usage:   "Creates a fine-tuning job which begins the process of creating a new model from\na given dataset.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:     "model",
			Usage:    "The name of the model to fine-tune. You can select one of the\n[supported models](https://platform.openai.com/docs/guides/fine-tuning#which-models-can-be-fine-tuned).\n",
			Required: true,
			BodyPath: "model",
		},
		&requestflag.Flag[string]{
			Name:     "training-file",
			Usage:    "The ID of an uploaded file that contains training data.\n\nSee [upload file](https://platform.openai.com/docs/api-reference/files/create) for how to upload a file.\n\nYour dataset must be formatted as a JSONL file. Additionally, you must upload your file with the purpose `fine-tune`.\n\nThe contents of the file should differ depending on if the model uses the [chat](https://platform.openai.com/docs/api-reference/fine-tuning/chat-input), [completions](https://platform.openai.com/docs/api-reference/fine-tuning/completions-input) format, or if the fine-tuning method uses the [preference](https://platform.openai.com/docs/api-reference/fine-tuning/preference-input) format.\n\nSee the [fine-tuning guide](https://platform.openai.com/docs/guides/model-optimization) for more details.\n",
			Required: true,
			BodyPath: "training_file",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "hyperparameters",
			Usage:    "The hyperparameters used for the fine-tuning job.\nThis value is now deprecated in favor of `method`, and should be passed in under the `method` parameter.\n",
			BodyPath: "hyperparameters",
		},
		&requestflag.Flag[any]{
			Name:     "integration",
			Usage:    "A list of integrations to enable for your fine-tuning job.",
			BodyPath: "integrations",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "metadata",
			Usage:    "Set of 16 key-value pairs that can be attached to an object. This can be\nuseful for storing additional information about the object in a structured\nformat, and querying for objects via API or the dashboard.\n\nKeys are strings with a maximum length of 64 characters. Values are strings\nwith a maximum length of 512 characters.\n",
			BodyPath: "metadata",
		},
		&requestflag.Flag[map[string]any]{
			Name:     "method",
			Usage:    "The method used for fine-tuning.",
			BodyPath: "method",
		},
		&requestflag.Flag[*int64]{
			Name:     "seed",
			Usage:    "The seed controls the reproducibility of the job. Passing in the same seed and job parameters should produce the same results, but may differ in rare cases.\nIf a seed is not specified, one will be generated for you.\n",
			BodyPath: "seed",
		},
		&requestflag.Flag[*string]{
			Name:     "suffix",
			Usage:    "A string of up to 64 characters that will be added to your fine-tuned model name.\n\nFor example, a `suffix` of \"custom-model-name\" would produce a model name like `ft:gpt-4o-mini:openai:custom-model-name:7p4lURel`.\n",
			Default:  nil,
			BodyPath: "suffix",
		},
		&requestflag.Flag[*string]{
			Name:     "validation-file",
			Usage:    "The ID of an uploaded file that contains validation data.\n\nIf you provide this file, the data is used to generate validation\nmetrics periodically during fine-tuning. These metrics can be viewed in\nthe fine-tuning results file.\nThe same data should not be present in both train and validation files.\n\nYour dataset must be formatted as a JSONL file. You must upload your file with the purpose `fine-tune`.\n\nSee the [fine-tuning guide](https://platform.openai.com/docs/guides/model-optimization) for more details.\n",
			BodyPath: "validation_file",
		},
	},
	Action:          handleFineTuningJobsCreate,
	HideHelpCommand: true,
}, map[string][]requestflag.HasOuterFlag{
	"hyperparameters": {
		&requestflag.InnerFlag[any]{
			Name:       "hyperparameters.batch-size",
			Usage:      "Number of examples in each batch. A larger batch size means that model parameters\nare updated less frequently, but with lower variance.\n",
			InnerField: "batch_size",
		},
		&requestflag.InnerFlag[any]{
			Name:       "hyperparameters.learning-rate-multiplier",
			Usage:      "Scaling factor for the learning rate. A smaller learning rate may be useful to avoid\noverfitting.\n",
			InnerField: "learning_rate_multiplier",
		},
		&requestflag.InnerFlag[any]{
			Name:       "hyperparameters.n-epochs",
			Usage:      "The number of epochs to train the model for. An epoch refers to one full cycle\nthrough the training dataset.\n",
			InnerField: "n_epochs",
		},
	},
	"integration": {
		&requestflag.InnerFlag[string]{
			Name:                  "integration.type",
			Usage:                 "The type of integration to enable. Currently, only \"wandb\" (Weights and Biases) is supported.\n",
			InnerField:            "type",
			OuterIsArrayOfObjects: true,
		},
		&requestflag.InnerFlag[map[string]any]{
			Name:                  "integration.wandb",
			Usage:                 "The settings for your integration with Weights and Biases. This payload specifies the project that\nmetrics will be sent to. Optionally, you can set an explicit display name for your run, add tags\nto your run, and set a default entity (team, username, etc) to be associated with your run.\n",
			InnerField:            "wandb",
			OuterIsArrayOfObjects: true,
		},
	},
	"method": {
		&requestflag.InnerFlag[string]{
			Name:       "method.type",
			Usage:      "The type of method. Is either `supervised`, `dpo`, or `reinforcement`.",
			InnerField: "type",
		},
		&requestflag.InnerFlag[map[string]any]{
			Name:       "method.dpo",
			Usage:      "Configuration for the DPO fine-tuning method.",
			InnerField: "dpo",
		},
		&requestflag.InnerFlag[map[string]any]{
			Name:       "method.reinforcement",
			Usage:      "Configuration for the reinforcement fine-tuning method.",
			InnerField: "reinforcement",
		},
		&requestflag.InnerFlag[map[string]any]{
			Name:       "method.supervised",
			Usage:      "Configuration for the supervised fine-tuning method.",
			InnerField: "supervised",
		},
	},
})

var fineTuningJobsRetrieve = cli.Command{
	Name:    "retrieve",
	Usage:   "Get info about a fine-tuning job.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "fine-tuning-job-id",
			Required:  true,
			PathParam: "fine_tuning_job_id",
		},
	},
	Action:          handleFineTuningJobsRetrieve,
	HideHelpCommand: true,
}

var fineTuningJobsList = cli.Command{
	Name:    "list",
	Usage:   "List your organization's fine-tuning jobs",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "after",
			Usage:     "Identifier for the last job from the previous pagination request.",
			QueryPath: "after",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "Number of fine-tuning jobs to retrieve.",
			Default:   20,
			QueryPath: "limit",
		},
		&requestflag.Flag[map[string]any]{
			Name:      "metadata",
			Usage:     "Optional metadata filter. To filter, use the syntax `metadata[k]=v`. Alternatively, set `metadata=null` to indicate no metadata.\n",
			QueryPath: "metadata",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleFineTuningJobsList,
	HideHelpCommand: true,
}

var fineTuningJobsCancel = cli.Command{
	Name:    "cancel",
	Usage:   "Immediately cancel a fine-tune job.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "fine-tuning-job-id",
			Required:  true,
			PathParam: "fine_tuning_job_id",
		},
	},
	Action:          handleFineTuningJobsCancel,
	HideHelpCommand: true,
}

var fineTuningJobsListEvents = cli.Command{
	Name:    "list-events",
	Usage:   "Get status updates for a fine-tuning job.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "fine-tuning-job-id",
			Required:  true,
			PathParam: "fine_tuning_job_id",
		},
		&requestflag.Flag[string]{
			Name:      "after",
			Usage:     "Identifier for the last event from the previous pagination request.",
			QueryPath: "after",
		},
		&requestflag.Flag[int64]{
			Name:      "limit",
			Usage:     "Number of events to retrieve.",
			Default:   20,
			QueryPath: "limit",
		},
		&requestflag.Flag[int64]{
			Name:  "max-items",
			Usage: "The maximum number of items to return (use -1 for unlimited).",
		},
	},
	Action:          handleFineTuningJobsListEvents,
	HideHelpCommand: true,
}

var fineTuningJobsPause = cli.Command{
	Name:    "pause",
	Usage:   "Pause a fine-tune job.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "fine-tuning-job-id",
			Required:  true,
			PathParam: "fine_tuning_job_id",
		},
	},
	Action:          handleFineTuningJobsPause,
	HideHelpCommand: true,
}

var fineTuningJobsResume = cli.Command{
	Name:    "resume",
	Usage:   "Resume a fine-tune job.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "fine-tuning-job-id",
			Required:  true,
			PathParam: "fine_tuning_job_id",
		},
	},
	Action:          handleFineTuningJobsResume,
	HideHelpCommand: true,
}

func handleFineTuningJobsCreate(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()

	if len(unusedArgs) > 0 {
		return fmt.Errorf("Unexpected extra arguments: %v", unusedArgs)
	}

	options, err := flagOptions(
		cmd,
		apiquery.NestedQueryFormatBrackets,
		apiquery.ArrayQueryFormatBrackets,
		ApplicationJSON,
		false,
	)
	if err != nil {
		return err
	}

	params := openai.FineTuningJobNewParams{}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.FineTuning.Jobs.New(ctx, params, options...)
	if err != nil {
		return err
	}

	obj := gjson.ParseBytes(res)
	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	return ShowJSON(obj, ShowJSONOpts{
		ExplicitFormat: explicitFormat,
		Format:         format,
		RawOutput:      cmd.Root().Bool("raw-output"),
		Title:          "fine-tuning:jobs create",
		Transform:      transform,
	})
}

func handleFineTuningJobsRetrieve(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("fine-tuning-job-id") && len(unusedArgs) > 0 {
		cmd.Set("fine-tuning-job-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if len(unusedArgs) > 0 {
		return fmt.Errorf("Unexpected extra arguments: %v", unusedArgs)
	}

	options, err := flagOptions(
		cmd,
		apiquery.NestedQueryFormatBrackets,
		apiquery.ArrayQueryFormatBrackets,
		EmptyBody,
		false,
	)
	if err != nil {
		return err
	}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.FineTuning.Jobs.Get(ctx, cmd.Value("fine-tuning-job-id").(string), options...)
	if err != nil {
		return err
	}

	obj := gjson.ParseBytes(res)
	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	return ShowJSON(obj, ShowJSONOpts{
		ExplicitFormat: explicitFormat,
		Format:         format,
		RawOutput:      cmd.Root().Bool("raw-output"),
		Title:          "fine-tuning:jobs retrieve",
		Transform:      transform,
	})
}

func handleFineTuningJobsList(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()

	if len(unusedArgs) > 0 {
		return fmt.Errorf("Unexpected extra arguments: %v", unusedArgs)
	}

	options, err := flagOptions(
		cmd,
		apiquery.NestedQueryFormatBrackets,
		apiquery.ArrayQueryFormatBrackets,
		EmptyBody,
		false,
	)
	if err != nil {
		return err
	}

	params := openai.FineTuningJobListParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if format == "raw" {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.FineTuning.Jobs.List(ctx, params, options...)
		if err != nil {
			return err
		}
		obj := gjson.ParseBytes(res)
		return ShowJSON(obj, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "fine-tuning:jobs list",
			Transform:      transform,
		})
	} else {
		iter := client.FineTuning.Jobs.ListAutoPaging(ctx, params, options...)
		maxItems := int64(-1)
		if cmd.IsSet("max-items") {
			maxItems = cmd.Value("max-items").(int64)
		}
		return ShowJSONIterator(iter, maxItems, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "fine-tuning:jobs list",
			Transform:      transform,
		})
	}
}

func handleFineTuningJobsCancel(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("fine-tuning-job-id") && len(unusedArgs) > 0 {
		cmd.Set("fine-tuning-job-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if len(unusedArgs) > 0 {
		return fmt.Errorf("Unexpected extra arguments: %v", unusedArgs)
	}

	options, err := flagOptions(
		cmd,
		apiquery.NestedQueryFormatBrackets,
		apiquery.ArrayQueryFormatBrackets,
		EmptyBody,
		false,
	)
	if err != nil {
		return err
	}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.FineTuning.Jobs.Cancel(ctx, cmd.Value("fine-tuning-job-id").(string), options...)
	if err != nil {
		return err
	}

	obj := gjson.ParseBytes(res)
	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	return ShowJSON(obj, ShowJSONOpts{
		ExplicitFormat: explicitFormat,
		Format:         format,
		RawOutput:      cmd.Root().Bool("raw-output"),
		Title:          "fine-tuning:jobs cancel",
		Transform:      transform,
	})
}

func handleFineTuningJobsListEvents(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("fine-tuning-job-id") && len(unusedArgs) > 0 {
		cmd.Set("fine-tuning-job-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if len(unusedArgs) > 0 {
		return fmt.Errorf("Unexpected extra arguments: %v", unusedArgs)
	}

	options, err := flagOptions(
		cmd,
		apiquery.NestedQueryFormatBrackets,
		apiquery.ArrayQueryFormatBrackets,
		EmptyBody,
		false,
	)
	if err != nil {
		return err
	}

	params := openai.FineTuningJobListEventsParams{}

	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	if format == "raw" {
		var res []byte
		options = append(options, option.WithResponseBodyInto(&res))
		_, err = client.FineTuning.Jobs.ListEvents(
			ctx,
			cmd.Value("fine-tuning-job-id").(string),
			params,
			options...,
		)
		if err != nil {
			return err
		}
		obj := gjson.ParseBytes(res)
		return ShowJSON(obj, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "fine-tuning:jobs list-events",
			Transform:      transform,
		})
	} else {
		iter := client.FineTuning.Jobs.ListEventsAutoPaging(
			ctx,
			cmd.Value("fine-tuning-job-id").(string),
			params,
			options...,
		)
		maxItems := int64(-1)
		if cmd.IsSet("max-items") {
			maxItems = cmd.Value("max-items").(int64)
		}
		return ShowJSONIterator(iter, maxItems, ShowJSONOpts{
			ExplicitFormat: explicitFormat,
			Format:         format,
			RawOutput:      cmd.Root().Bool("raw-output"),
			Title:          "fine-tuning:jobs list-events",
			Transform:      transform,
		})
	}
}

func handleFineTuningJobsPause(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("fine-tuning-job-id") && len(unusedArgs) > 0 {
		cmd.Set("fine-tuning-job-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if len(unusedArgs) > 0 {
		return fmt.Errorf("Unexpected extra arguments: %v", unusedArgs)
	}

	options, err := flagOptions(
		cmd,
		apiquery.NestedQueryFormatBrackets,
		apiquery.ArrayQueryFormatBrackets,
		EmptyBody,
		false,
	)
	if err != nil {
		return err
	}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.FineTuning.Jobs.Pause(ctx, cmd.Value("fine-tuning-job-id").(string), options...)
	if err != nil {
		return err
	}

	obj := gjson.ParseBytes(res)
	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	return ShowJSON(obj, ShowJSONOpts{
		ExplicitFormat: explicitFormat,
		Format:         format,
		RawOutput:      cmd.Root().Bool("raw-output"),
		Title:          "fine-tuning:jobs pause",
		Transform:      transform,
	})
}

func handleFineTuningJobsResume(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("fine-tuning-job-id") && len(unusedArgs) > 0 {
		cmd.Set("fine-tuning-job-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if len(unusedArgs) > 0 {
		return fmt.Errorf("Unexpected extra arguments: %v", unusedArgs)
	}

	options, err := flagOptions(
		cmd,
		apiquery.NestedQueryFormatBrackets,
		apiquery.ArrayQueryFormatBrackets,
		EmptyBody,
		false,
	)
	if err != nil {
		return err
	}

	var res []byte
	options = append(options, option.WithResponseBodyInto(&res))
	_, err = client.FineTuning.Jobs.Resume(ctx, cmd.Value("fine-tuning-job-id").(string), options...)
	if err != nil {
		return err
	}

	obj := gjson.ParseBytes(res)
	format := cmd.Root().String("format")
	explicitFormat := cmd.Root().IsSet("format")
	transform := cmd.Root().String("transform")
	return ShowJSON(obj, ShowJSONOpts{
		ExplicitFormat: explicitFormat,
		Format:         format,
		RawOutput:      cmd.Root().Bool("raw-output"),
		Title:          "fine-tuning:jobs resume",
		Transform:      transform,
	})
}
