package app

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"unicode"

	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-go/v3"
	"github.com/tidwall/gjson"
)

var permissionScopeName = regexp.MustCompile(`^[a-z][a-z0-9_]*(?:\.[a-z][a-z0-9_]*)*$`)

// Interpret only the API's explicit missing-scope diagnostic. Never display its
// prose or use it to change request, retry, authentication, or exit behavior.
func readableAPIPermissionMessage(failure error, apierr *openai.Error) string {
	if apierr.StatusCode != http.StatusUnauthorized && apierr.StatusCode != http.StatusForbidden {
		return ""
	}
	// The SDK can populate Message while partially decoding malformed JSON.
	if raw := apierr.RawJSON(); raw != "" && !gjson.Valid(raw) {
		return ""
	}
	const prefix = "You have insufficient permissions for this operation. Missing scopes: "
	scopes, ok := strings.CutPrefix(apierr.Message, prefix)
	if !ok || strings.ContainsFunc(apierr.Message, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
		return ""
	}
	scopes, _, _ = strings.Cut(scopes, ". ")
	scopes = strings.TrimSuffix(scopes, ".")
	missingFilesWrite := false
	for _, scope := range strings.Split(scopes, ", ") {
		if !permissionScopeName.MatchString(scope) {
			return ""
		}
		missingFilesWrite = missingFilesWrite || scope == "api.files.write"
	}
	if !missingFilesWrite {
		return ""
	}
	message := "The API denied this request."
	var contextual *commandError
	if errors.As(failure, &contextual) && contextual.command != nil && localcommands.ResourceName(contextual.command) == "files" {
		switch contextual.command.Name {
		case "upload", "create":
			message = "File upload was denied."
		}
	}
	return message + "\nThe API reports missing permission: api.files.write." +
		"\nCheck the key used by this command and its project." +
		"\nFor OpenAI, edit that key at https://platform.openai.com/api-keys." +
		"\nEnable Files Write; Batch Write also grants this permission." +
		"\nIf access remains denied, ask a project owner to check your role." +
		"\nCorrect access before retrying the failed operation."
}
