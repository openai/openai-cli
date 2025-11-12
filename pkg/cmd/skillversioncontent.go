// File generated from our OpenAPI spec by Stainless. See CONTRIBUTING.md for details.

package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/urfave/cli/v3"
)

var skillsVersionsContentRetrieve = cli.Command{
	Name:    "retrieve",
	Usage:   "Download a skill version zip bundle.",
	Suggest: true,
	Flags: []cli.Flag{
		&requestflag.Flag[string]{
			Name:      "skill-id",
			Required:  true,
			PathParam: "skill_id",
		},
		&requestflag.Flag[string]{
			Name:      "version",
			Usage:     "The skill version number.",
			Required:  true,
			PathParam: "version",
		},
		&requestflag.Flag[string]{
			Name:    "output",
			Aliases: []string{"o"},
			Usage:   "The file where the response contents will be stored. Use the value '-' to force output to stdout.",
		},
	},
	Action:          handleSkillsVersionsContentRetrieve,
	HideHelpCommand: true,
}

func handleSkillsVersionsContentRetrieve(ctx context.Context, cmd *cli.Command) error {
	client := openai.NewClient(getDefaultRequestOptions(cmd)...)
	unusedArgs := cmd.Args().Slice()
	if !cmd.IsSet("skill-id") && len(unusedArgs) > 0 {
		cmd.Set("skill-id", unusedArgs[0])
		unusedArgs = unusedArgs[1:]
	}
	if !cmd.IsSet("version") && len(unusedArgs) > 0 {
		cmd.Set("version", unusedArgs[0])
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

	response, err := client.Skills.Versions.Content.Get(
		ctx,
		cmd.Value("skill-id").(string),
		cmd.Value("version").(string),
		options...,
	)
	if err != nil {
		return err
	}
	message, err := writeBinaryResponse(response, os.Stdout, cmd.String("output"))
	if message != "" {
		fmt.Println(message)
	}
	return err
}
