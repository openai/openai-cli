package imagefontmac

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Exercise the real JavaScript selection logic with plain JavaScript objects.
// The Application expression is removed before execution, so these tests send
// no AppleEvents, inspect no Terminal windows, and change no native settings.
func TestProfileJavaScriptOwnershipLogic(t *testing.T) {
	if !Supported() {
		t.Skip("built-in JavaScript interpreter unavailable")
	}
	const name = "OpenAI Images 0123abcd"
	const font = "OpenAIImages-0123abcd-original-Regular"
	for _, tt := range []struct {
		label, action, profile, currentFont, tty, reason string
		size                                             float64
		stopped, ok                                      bool
	}{
		{"inspect normal", "inspect", name, font, "/dev/ttys001", "", 16, false, true},
		{"inspect large", "inspect", name, font, "/dev/ttys001", "", 32, false, true},
		{"inspect unsupported size", "inspect", name, font, "/dev/ttys001", "size", 17.5, false, false},
		{"ordinary tab", "inspect", "Basic", "Menlo-Regular", "/dev/ttys001", "profile", 16, false, false},
		{"renamed owned tab", "inspect", "OpenAI Images", font, "/dev/ttys001", "", 16, false, true},
		{"custom label owned font", "inspect", "my custom theme", font, "/dev/ttys001", "", 32, false, true},
		{"other gallery", "inspect", "OpenAI Images ffffffff", "OpenAIImages-ffffffff-original-Regular", "/dev/ttys001", "profile", 16, false, false},
		{"wrong owned font", "inspect", name, "Menlo-Regular", "/dev/ttys001", "profile", 16, false, false},
		{"stopped terminal", "inspect", name, font, "/dev/ttys001", "tab", 16, true, false},
		{"wrong tty", "inspect", name, font, "/dev/ttys999", "tab", 16, false, false},
	} {
		t.Run(tt.label, func(t *testing.T) {
			fixture, err := json.Marshal(map[string]any{"name": tt.profile, "font": tt.currentFont, "size": tt.size, "tty": tt.tty, "running": !tt.stopped})
			if err != nil {
				t.Fatal(err)
			}
			mock := `var fixture = ` + string(fixture) + `;
var mutations = [];
var settings = {
    name: function () { return fixture.name; },
    fontSize: function () { return fixture.size; },
    id: function () { return 42; }
};
Object.defineProperty(settings, "fontName", {
    get: function () { return function () { return fixture.font; }; },
    set: function (value) { mutations.push(value); fixture.font = value; }
});
var mockTerminal = {
    running: function () { return fixture.running; },
    windows: function () { return [{tabs: function () { return [{
        tty: function () { return fixture.tty; },
        currentSettings: function () { return settings; }
    }]; }}]; }
};
`
			const application = `Application("com.apple.Terminal")`
			if strings.Count(profileBridge, application) != 1 {
				t.Fatal("native bridge changed; update the mock before executing it")
			}
			logic := strings.Replace(profileBridge, application, "mockTerminal", 1)
			logic = strings.Replace(logic, "function run(argv)", "function profileOperation(argv)", 1)
			if strings.Contains(logic, "Application(") {
				t.Fatal("test must not retain any native application access")
			}
			logic = mock + logic + `
function run(argv) {
    var reply = JSON.parse(profileOperation(argv));
    reply.mutations = mutations;
    return JSON.stringify(reply);
}`
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			args := []string{"-l", "JavaScript", "-e", logic, tt.action, name, "/dev/ttys001", ""}
			data, err := run(ctx, interpreter, args, []string{})
			if err != nil {
				t.Fatalf("mock JavaScript failed: %v", err)
			}
			var result struct {
				ProfileStatus
				OK        bool     `json:"ok"`
				Reason    string   `json:"reason"`
				Mutations []string `json:"mutations"`
			}
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
			if result.OK != tt.ok || result.Reason != tt.reason {
				t.Fatalf("result=%+v want ok=%v reason=%s", result, tt.ok, tt.reason)
			}
			if tt.action == "inspect" && (tt.ok || tt.reason == "size") && (result.FontName != tt.currentFont || result.FontSize != float64(tt.size)) {
				t.Fatalf("missing checked font settings: %+v", result)
			}
			if len(result.Mutations) != 0 {
				t.Fatalf("unexpected profile change: %+v", result)
			}
		})
	}
}
