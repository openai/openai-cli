package custom

import (
	"errors"
	"os"

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
	recovery := "Check project-links.json and its lock in your user configuration folder. "
	switch {
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
	message := operation + recovery + "This command did not change saved links."
	if action == "resolve" {
		message += " Inspect with " + errorHelpInvocation(root) + " link."
	}
	return projectLinkFailure(message, cause)
}

func projectLinkOutputFailure(root *cli.Command, action string, cause error) error {
	message := "Could not write folder-link inspection. This command did not change saved links."
	switch action {
	case "link":
		message = "The folder link was saved, but its confirmation could not be written."
	case "unlink":
		message = "This folder has no saved link of its own. Its confirmation could not be written."
	}
	return projectLinkFailure(message+" Inspect with "+errorHelpInvocation(root)+" link.", cause)
}
