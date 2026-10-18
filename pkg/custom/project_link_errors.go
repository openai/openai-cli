package custom

import (
	"context"
	"errors"
	"os"

	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/urfave/cli/v3"
)

func projectLinkStoreFailure(root *cli.Command, action string, cause error) error {
	operation := "Could not inspect folder links. "
	switch action {
	case "link":
		operation = "Could not save the folder link. "
	case "unlink":
		operation = "Could not remove the folder link. "
	case "resolve":
		operation = "Could not resolve folder project defaults. "
	}
	message := operation + projectLinkRecovery(cause) + "This command did not change saved links."
	if action == "resolve" {
		message += " Inspect with " + errorHelpInvocation(root) + " link."
	}
	return projectLinkFailure(message, cause)
}

func projectLinkRecovery(cause error) string {
	recovery := "Check project-links.json and its lock in your user configuration folder. "
	switch {
	case errors.Is(cause, errProjectLinkAliasConflict):
		recovery = "Folder links assign different projects to the same folder. Stop CLI writers, then repair the matching paths in project-links.json. "
	case errors.Is(cause, errProjectLinkAliasAccess):
		recovery = "Could not verify equivalent folder paths. Check access to the linked folders, then retry. "
	case errors.Is(cause, errProjectLinksTooLarge):
		recovery = "Folder links must fit within 1 MiB. Remove unused entries or excess whitespace from project-links.json before retrying. "
	case errors.Is(cause, errProjectLinksInvalid):
		recovery = "Folder links contain invalid JSON or entries. Repair project-links.json in your user configuration folder. "
	case errors.Is(cause, errProjectLinkDirectoryUnsafe):
		recovery = "Use a private configuration directory owned by your user, without an application-directory symlink. On Unix, check ownership and mode 0700. "
	case errors.Is(cause, errProjectLinkFileUnsafe):
		recovery = "Use a private regular project-links.json owned by your user, without a symlink. On Unix, check ownership and mode 0600. "
	case errors.Is(cause, errProjectLinkLockUnsafe):
		recovery = "The .project-links.json.lock file must be private, regular, and owned by your user. On Unix, check ownership and mode 0600. "
	case errors.Is(cause, os.ErrPermission):
		recovery = "Your user cannot access the folder-link configuration. Check permissions on its directory, project-links.json, and .project-links.json.lock. "
	case errors.Is(cause, errProjectLinksChanged), errors.Is(cause, errProjectLinkLockChanged), errors.Is(cause, errProjectLinkDirectoryChanged):
		recovery = "Folder-link state changed during this command. Stop other registry writers before retrying. "
	case errors.Is(cause, errProjectLinkInputInvalid):
		recovery = "Use an existing folder and a project ID starting with proj_. "
	}
	return recovery
}

func projectLinkCommittedSelectionFailure(action string, cause error) error {
	outcome := "The folder link was saved. Its effective project could not be resolved. "
	if action == "unlink" {
		outcome = "This folder has no saved link of its own. Its inherited project could not be resolved. "
	}
	if errors.Is(cause, context.Canceled) {
		return &localCancellationGuidanceError{LocalError: &localcommands.LocalError{
			Message: outcome + "The command was canceled. Inspect saved links before retrying.",
			Cause:   cause,
		}}
	}
	var utility *localcommands.LocalError
	recovery := projectLinkRecovery(cause)
	if errors.As(cause, &utility) {
		// Profile selection can fail after the independent folder transaction.
		// Keep its vetted recovery instead of blaming the folder registry.
		recovery = utility.Error()
	}
	return &localCancellationGuidanceError{LocalError: &localcommands.LocalError{Message: outcome + recovery, Cause: cause}}
}

func projectLinkOutputFailure(root *cli.Command, action string, cause error) error {
	message := "Could not write folder-link inspection. This command did not change saved links."
	switch action {
	case "link":
		message = "The folder link was saved, but its confirmation could not be written."
	case "unlink":
		message = "This folder has no saved link of its own. Its confirmation could not be written."
	}
	failure := &localcommands.LocalError{
		Message: message + " Inspect with " + errorHelpInvocation(root) + " link.",
		Cause:   cause,
	}
	if action == "link" || action == "unlink" {
		return &localCancellationGuidanceError{LocalError: failure}
	}
	return failure
}
