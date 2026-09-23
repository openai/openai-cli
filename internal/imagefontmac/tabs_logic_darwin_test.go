package imagefontmac

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Replace the sole Application expression with plain objects before running
// the bridge. These tests cannot inspect or change actual Terminal windows.
func TestTerminalTabsJavaScript(t *testing.T) {
	if !Supported() {
		t.Skip("built-in JavaScript interpreter unavailable")
	}
	for _, test := range []struct {
		name        string
		windowError int
		tabError    bool
		running     bool
		ok          bool
		count       int
	}{
		{"all tabs", 0, false, true, true, 2},
		{"not running", 0, false, false, true, 0},
		{"Inspector has no tabs", -1728, false, true, true, 2},
		{"permission error", -1743, false, true, false, 0},
		{"unknown window error", -1708, false, true, false, 0},
		{"tab read error", 0, true, true, false, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			const application = `Application("com.apple.Terminal")`
			require.Equal(t, 1, strings.Count(tabsBridge, application))
			logic := strings.Replace(tabsBridge, application, "mockTerminal", 1)
			require.NotContains(t, logic, "Application(")
			mock := fmt.Sprintf(`
var mockTerminal = {
    running: function () { return %t; },
    windows: function () { return [
        {tabs: function () { return [{tty: function () { return "/dev/ttys001"; }, currentSettings: function () { return {fontName: function () { return "GoMono"; }}; }}]; }},
        {tabs: function () { if (%d) { var error = new Error("private window error"); error.number = %d; throw error; } return []; }},
        {tabs: function () { return [{tty: function () { if (%t) { throw new Error("private tab error"); } return "/dev/ttys002"; }, currentSettings: function () { return {fontName: function () { return "Menlo"; }}; }}]; }}
    ]; }
};
`, test.running, test.windowError, test.windowError, test.tabError)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			data, err := run(ctx, interpreter, []string{"-l", "JavaScript", "-e", mock + logic}, []string{})
			require.NoError(t, err)
			var result struct {
				OK   bool          `json:"ok"`
				Tabs []TerminalTab `json:"tabs"`
			}
			require.NoError(t, json.Unmarshal(data, &result))
			require.Equal(t, test.ok, result.OK)
			require.Len(t, result.Tabs, test.count)
			require.NotContains(t, string(data), "private")
		})
	}
}
