package app

import (
	"io"
	"path/filepath"
	"strings"

	"github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-cli/internal/request"
	"github.com/urfave/cli/v3"
)

func isSkillCreate(cmd *cli.Command) bool {
	resource := commands.ResourceName(cmd)
	return cmd.Name == "create" && (resource == "skills" || resource == "skills:versions")
}

// The CLI collects --files into a slice. The API takes one ZIP as a single
// multipart field named "files", separate from the individual-file array.
func selectSkillZIP(cmd *cli.Command, body map[string]any) (request.BodyContentType, error) {
	value := body["files"]
	if values, ok := value.([]any); ok && len(values) == 1 {
		value = values[0]
	}
	path, ok := value.(request.FilePathValue)
	if !ok || (!request.IsStdinPath(string(path)) && !strings.EqualFold(filepath.Ext(string(path)), ".zip")) {
		return request.ApplicationJSON, nil
	}
	// The SDK rejects an empty version path before taking ownership of the upload.
	if cmd.IsSet("skill-id") && cmd.String("skill-id") == "" {
		return request.ApplicationJSON, &commands.LocalError{Message: "Supply a nonempty --skill-id for the new version."}
	}
	body["files"] = path
	return request.MultipartFormEncoded, nil
}

func embedSkillZIP(cmd *cli.Command, body map[string]any, stdin *request.OnceStdinReader) (map[string]any, error) {
	path := string(body["files"].(request.FilePathValue))
	delete(body, "files")
	// Other fields keep their JSON input semantics. Resolve text references
	// before opening the archive so a failed scalar read owns no upload.
	scalars, err := request.EmbedRequestFiles(cmd, body, "body", request.EmbedText, stdin)
	if err != nil {
		return nil, err
	}
	var file request.FileUpload
	if request.IsStdinPath(path) {
		reader, err := stdin.Take()
		if err != nil {
			return nil, err
		}
		file = request.FileUpload{Reader: io.NopCloser(reader), FileName: "skill.zip"}
	} else {
		file, err = (request.FileInputSource{Option: "--files"}).OpenUpload(path)
		if err != nil {
			return nil, err
		}
	}
	file.MediaType = "application/zip"
	form := scalars.(map[string]any)
	form["files"] = file
	return form, nil
}
