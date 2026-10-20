package app

import (
	"errors"
	"slices"
	"strings"

	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/urfave/cli/v3"
)

func inputFileErrorMessage(command *cli.Command, failure error) string {
	var messages []string
	var contextual *commandError
	if errors.As(failure, &contextual) && contextual.command != nil {
		command = contextual.command
	}
	add := func(message string) {
		if !slices.Contains(messages, message) {
			messages = append(messages, message)
		}
	}
	var visit func(error)
	visit = func(err error) {
		if err == nil {
			return
		}
		if input, ok := err.(*requestconfig.InputFileError); ok {
			add("Could not " + input.Operation + " the file for " + input.Option + ". Check the path and permissions.")
			return
		}
		var input *requestconfig.InputFileError
		if !errors.As(err, &input) {
			// A cleanup failure can accompany a primary failure from an unknown
			// field or stdin. Keep that branch's existing safe recovery guidance.
			add(localErrorMessage(command, err))
			return
		}
		switch wrapped := err.(type) {
		case interface{ Unwrap() []error }:
			for _, child := range wrapped.Unwrap() {
				visit(child)
			}
		case interface{ Unwrap() error }:
			visit(wrapped.Unwrap())
		}
	}
	visit(failure)
	return strings.Join(messages, "\n")
}
