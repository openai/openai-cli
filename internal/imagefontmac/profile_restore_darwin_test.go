package imagefontmac

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The real bridge runs against plain objects, without contacting Terminal.
func TestRestoreProfileJavaScript(t *testing.T) {
	if !Supported() {
		t.Skip("built-in JavaScript interpreter unavailable")
	}
	for _, test := range []struct {
		name, reason string
		options      map[string]any
		writes       int
	}{
		{"restores original font", "", nil, 1},
		{"already restored is unchanged", "", map[string]any{"alreadyRestored": true}, 0},
		{"concurrent font is unchanged", "", map[string]any{"userFont": "Courier"}, 0},
		{"concurrent size is unchanged", "", map[string]any{"userSize": 18}, 0},
		{"concurrent profile is unchanged", "", map[string]any{"userProfile": true}, 0},
		{"profile changes before restoration", "", map[string]any{"concurrentBefore": true}, 0},
		{"ignored restoration is explicit", "rollback", map[string]any{"fontIgnored": true}, 1},
		{"denied restoration is explicit", "rollback", map[string]any{"rollbackDenied": true}, 1},
		{"missing tab is unchanged", "tab", map[string]any{"missingTTY": true}, 0},
		{"duplicate tab is unchanged", "tab", map[string]any{"duplicateTTY": true}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			options, err := json.Marshal(test.options)
			require.NoError(t, err)
			const app = `Application("com.apple.Terminal")`
			require.Equal(t, 1, strings.Count(profileBridge, app))
			logic := strings.Replace(profileBridge, app, "mockTerminal", 1)
			logic = strings.Replace(logic, "function run(argv)", "function profileOperation(argv)", 1)
			require.NotContains(t, logic, "Application(")
			logic = "var restoreOptions = " + string(options) + ` || {};
var options = {duplicateTTY: restoreOptions.duplicateTTY, missingTTY: restoreOptions.missingTTY};
` + switchProfileMock + logic + `
function run(argv) {
    var expected = {fontName: originalFont, fontSize: originalSize, profileID: 102, profileName: originalName};
    if (!restoreOptions.alreadyRestored) { target.currentSettings().fontName = argv[3]; }
    if (restoreOptions.userFont) { target.currentSettings().fontName = restoreOptions.userFont; }
    if (restoreOptions.userSize) { target.currentSettings().fontSize = restoreOptions.userSize; }
    if (restoreOptions.userProfile) { target.externalSelection(); }
    options = restoreOptions;
    mutations = [];
    var reply = JSON.parse(profileOperation(argv.concat([JSON.stringify(expected)])));
    reply.font = target.currentSettings().fontName();
    reply.size = target.currentSettings().fontSize();
    reply.id = target.currentSettings().id();
    reply.name = target.currentSettings().name();
    reply.theme = JSON.stringify(target.theme());
    reply.originalTheme = initialTheme;
    reply.savedFont = saved.fontName();
    reply.savedSize = saved.fontSize();
    reply.otherFont = other.currentSettings().fontName();
    reply.otherSize = other.currentSettings().fontSize();
    reply.otherTheme = JSON.stringify(other.theme());
    reply.initialOtherTheme = initialOtherTheme;
    reply.mutations = mutations;
    return JSON.stringify(reply);
}`
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			data, err := run(ctx, interpreter, []string{"-l", "JavaScript", "-e", logic, "restore", "OpenAI Images 0123abcd", "/dev/ttys002", "OpenAIImages-0123abcd-next-Regular"}, []string{})
			require.NoError(t, err)
			var reply struct {
				OK                                                             bool
				Reason, Font, Name, Theme, OriginalTheme, SavedFont, OtherFont string
				OtherTheme, InitialOtherTheme                                  string
				Size, SavedSize, OtherSize                                     float64
				ID                                                             int
				Mutations                                                      []string
			}
			require.NoError(t, json.Unmarshal(data, &reply))
			require.Equal(t, test.reason == "", reply.OK, "%+v", reply)
			require.Equal(t, test.reason, reply.Reason)
			require.Equal(t, "Menlo-Regular", reply.SavedFont)
			require.Equal(t, "Menlo-Regular", reply.OtherFont)
			require.Equal(t, 14.0, reply.SavedSize)
			require.Equal(t, 14.0, reply.OtherSize)
			require.Equal(t, reply.InitialOtherTheme, reply.OtherTheme)
			require.Equal(t, reply.OriginalTheme, reply.Theme)
			writes := 0
			for _, mutation := range reply.Mutations {
				require.Contains(t, []string{"target font", "external selection"}, mutation)
				if mutation == "target font" {
					writes++
				}
			}
			require.Equal(t, test.writes, writes)
			if test.options["userProfile"] == true || test.options["concurrentBefore"] == true {
				require.Equal(t, 200, reply.ID)
				require.Equal(t, "Novel", reply.Name)
				require.Equal(t, "Courier", reply.Font)
				require.Equal(t, 18.0, reply.Size)
			} else {
				require.Equal(t, 102, reply.ID)
				require.Equal(t, "Basic", reply.Name)
				wantSize, wantFont := 14.0, "Menlo-Regular"
				if test.options["userFont"] != nil {
					wantFont = "Courier"
				} else if test.options["userSize"] != nil || test.reason != "" {
					wantFont = "OpenAIImages-0123abcd-next-Regular"
				}
				if test.options["userSize"] != nil {
					wantSize = 18
				}
				require.Equal(t, wantFont, reply.Font)
				require.Equal(t, wantSize, reply.Size)
			}
		})
	}
}
