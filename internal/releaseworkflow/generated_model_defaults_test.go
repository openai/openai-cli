package releaseworkflow

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/openai/openai-cli/generated/commands"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// Generated API commands currently leave the model choice to the caller or server.
// Inspect the request with no caller input: a default displayed in help need not be sent.
func TestGeneratedModelDefaults(t *testing.T) {
	if len(commands.Command.Commands) == 0 {
		t.Fatal("missing generated commands")
	}
	checkGeneratedCommand(t, commands.Command, commands.Command.Name)
}

func checkGeneratedCommand(t *testing.T, command *cli.Command, name string) {
	t.Helper()
	contents := requestflag.ExtractRequestContents(command)
	fields := map[string]any{"body": contents.Body, "query": contents.Queries, "header": contents.Headers}
	// Generated handlers read path arguments directly. Required, unset paths cannot send a request.
	for _, flag := range command.Flags {
		operand, ok := flag.(requestflag.InRequest)
		if !ok || operand.GetPathParam() == "" {
			continue
		}
		if required, ok := flag.(cli.RequiredFlag); ok && required.IsRequired() && !flag.IsSet() {
			continue
		}
		fields["path."+operand.GetPathParam()] = flag.Get()
	}
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("%s: could not encode generated defaults: %v", name, err)
	}
	checkGeneratedModelFields(t, name, "", gjson.ParseBytes(data))
	for _, child := range command.Commands {
		checkGeneratedCommand(t, child, name+" "+child.Name)
	}
}

var modelField = regexp.MustCompile(`(?i)(^|[._-])model(s|[_-]ids?)?$`)

func checkGeneratedModelFields(t *testing.T, command, field string, value gjson.Result) {
	t.Helper()
	switch {
	case value.IsArray(), value.IsObject():
		value.ForEach(func(key, item gjson.Result) bool {
			child := field
			if key.Type == gjson.String {
				child += "." + key.Str
			}
			checkGeneratedModelFields(t, command, child, item)
			return true
		})
	case value.Type == gjson.String && value.Str != "" && value.Str != "auto" && modelField.MatchString(field):
		t.Errorf("%s supplies %s without caller input. Add this model choice to release verification.", command, field)
	}
}
