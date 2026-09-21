package imagefontmac

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// These tests remove Application(...) from the real bridge, replacing it with
// plain objects. They never contact Terminal or mutate any desktop settings.
func TestPreserveProfileJavaScript(t *testing.T) {
	if !Supported() {
		t.Skip("built-in JavaScript interpreter unavailable")
	}
	tests := []struct {
		name, reason string
		options      map[string]any
		ok           bool
	}{
		{"already enabled remains untouched", "", map[string]any{"sameFont": true, "size": 13}, true},
		{"fresh snapshot repeat is idempotent", "", map[string]any{"repeat": true, "size": 18}, true},
		{"snapshot preserves custom theme", "", map[string]any{"profileName": "深蓝 🧑‍💻", "size": 24}, true},
		{"captured font mismatch stops writes", "changed", map[string]any{"expectedFont": "Courier"}, false},
		{"captured size mismatch stops writes", "changed", map[string]any{"expectedSize": 13}, false},
		{"captured profile ID mismatch stops writes", "changed", map[string]any{"expectedID": 999}, false},
		{"captured profile name mismatch stops writes", "changed", map[string]any{"expectedName": "Other"}, false},
		{"fractional point size stops writes", "size", map[string]any{"size": 13.5}, false},
		{"concurrent profile before write preserved", "changed", map[string]any{"concurrentBefore": true}, false},
		{"concurrent profile after write preserved", "changed", map[string]any{"concurrentAfterFont": true}, false},
		{"concurrent font preserved", "changed", map[string]any{"concurrentFont": true}, false},
		{"concurrent size preserved", "changed", map[string]any{"concurrentSize": true}, false},
		{"ignored font setter leaves original", "changed", map[string]any{"fontIgnored": true}, false},
		{"denied font setter leaves original", "permission", map[string]any{"fontDenied": true}, false},
		{"failed setter after write restores original", "permission", map[string]any{"fontDeniedAfterWrite": true}, false},
		{"failed restoration is explicit", "rollback", map[string]any{"fontDeniedAfterWrite": true, "rollbackDenied": true}, false},
		{"duplicate tty prevents changes", "tab", map[string]any{"duplicateTTY": true}, false},
		{"missing tty prevents changes", "tab", map[string]any{"missingTTY": true}, false},
		{"non-tab window before target is skipped", "", map[string]any{"unreadableWindow": "before", "windowError": -1728}, true},
		{"non-tab window after target is skipped", "", map[string]any{"unreadableWindow": "after", "windowError": -1728}, true},
		{"skipped non-tab window does not hide duplicate tty", "tab", map[string]any{"unreadableWindow": "before", "windowError": -1728, "duplicateTTY": true}, false},
		{"skipped non-tab window does not replace missing tty", "tab", map[string]any{"unreadableWindow": "after", "windowError": -1728, "missingTTY": true}, false},
		{"window permission denial before target stops writes", "permission", map[string]any{"unreadableWindow": "before", "windowError": -1743}, false},
		{"window permission denial after target stops writes", "permission", map[string]any{"unreadableWindow": "after", "windowError": -1743}, false},
		{"unknown window failure before target stops writes", "native", map[string]any{"unreadableWindow": "before", "windowError": -1708}, false},
		{"unknown window failure after target stops writes", "native", map[string]any{"unreadableWindow": "after", "windowError": -1708}, false},
	}
	for _, size := range []int{12, 13, 18, 24, 32} {
		tests = append(tests, struct {
			name, reason string
			options      map[string]any
			ok           bool
		}{fmt.Sprintf("preserves exact %d point size", size), "", map[string]any{"size": size}, true})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options, err := json.Marshal(test.options)
			if err != nil {
				t.Fatal(err)
			}
			const app = `Application("com.apple.Terminal")`
			if strings.Count(profileBridge, app) != 1 {
				t.Fatal("native bridge changed; review test before executing")
			}
			logic := strings.Replace(profileBridge, app, "mockTerminal", 1)
			logic = strings.Replace(logic, "function run(argv)", "function profileOperation(argv)", 1)
			if strings.Contains(logic, "Application(") {
				t.Fatal("test cannot contain native application access")
			}
			mock := strings.Replace(switchProfileMock, `if (options.concurrentFont && !concurrentDone)`, `if (options.concurrentSize && !concurrentDone) { concurrentDone = true; mutations.push("external size"); size = originalSize + 3; }
            if (options.concurrentFont && !concurrentDone)`, 1)
			logic = "var options = " + string(options) + " || {};\n" + mock + logic + `
function run(argv) {
    var expected = {fontName: options.expectedFont || originalFont, fontSize: options.expectedSize || originalSize,
        profileID: options.expectedID || 102, profileName: options.expectedName || originalName};
    argv.push(JSON.stringify(expected));
    var reply = JSON.parse(profileOperation(argv));
    var firstMutations = mutations.length;
    if (options.repeat && reply.ok) {
        var fresh = JSON.parse(profileOperation(["snapshot", argv[1], argv[2], ""]));
        if (!fresh.ok) { throw Error("snapshot failed"); }
        argv[4] = JSON.stringify({fontName: fresh.fontName, fontSize: fresh.fontSize, profileID: fresh.profileID, profileName: fresh.profileName});
        reply = JSON.parse(profileOperation(argv));
    }
    reply.repeatMutations = mutations.length - firstMutations;
    reply.selectedID = target.currentSettings().id();
    reply.selectedName = target.currentSettings().name();
    reply.selectedFont = target.currentSettings().fontName();
    reply.selectedSize = target.currentSettings().fontSize();
    reply.originalName = originalName;
    reply.originalFont = originalFont;
    reply.originalSize = originalSize;
    reply.targetTheme = JSON.stringify(target.theme());
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
			data, err := run(ctx, interpreter, []string{"-l", "JavaScript", "-e", logic, "preserve", "OpenAI Images 0123abcd", "/dev/ttys002", "OpenAIImages-0123abcd-next-Regular"}, []string{})
			if err != nil {
				t.Fatalf("mock JavaScript failed: %v", err)
			}
			var reply struct {
				OK                                                        bool
				Reason                                                    string
				SelectedID, RepeatMutations                               int
				SelectedSize, OriginalSize, SavedSize, OtherSize          float64
				SelectedName, SelectedFont, OriginalName, OriginalFont    string
				SavedFont, OtherFont                                      string
				TargetTheme, OriginalTheme, OtherTheme, InitialOtherTheme string
				Mutations                                                 []string
			}
			if err := json.Unmarshal(data, &reply); err != nil {
				t.Fatal(err)
			}
			if reply.OK != test.ok || reply.Reason != test.reason {
				t.Fatalf("reply=%+v want ok=%v reason=%s", reply, test.ok, test.reason)
			}
			if reply.SavedFont != reply.OriginalFont || reply.SavedSize != reply.OriginalSize || reply.OtherFont != reply.OriginalFont || reply.OtherSize != reply.OriginalSize || reply.OtherTheme != reply.InitialOtherTheme {
				t.Fatalf("saved profile or neighboring tab changed: %+v", reply)
			}
			if test.options["concurrentBefore"] == true || test.options["concurrentAfterFont"] == true {
				if reply.SelectedID != 200 || reply.SelectedName != "Novel" || reply.SelectedFont != "Courier" || reply.SelectedSize != 18 {
					t.Fatalf("overwrote external profile change: %+v", reply)
				}
			} else {
				if reply.SelectedID != 102 || reply.SelectedName != reply.OriginalName || reply.TargetTheme != reply.OriginalTheme {
					t.Fatalf("selected profile or theme changed: %+v", reply)
				}
				wantSize := reply.OriginalSize
				if test.options["concurrentSize"] == true {
					wantSize += 3
				}
				if reply.SelectedSize != wantSize {
					t.Fatalf("changed point size: %+v", reply)
				}
				if test.ok && reply.SelectedFont != "OpenAIImages-0123abcd-next-Regular" {
					t.Fatalf("image font was not applied: %+v", reply)
				}
				if test.options["concurrentFont"] == true {
					if reply.SelectedFont != "Courier" {
						t.Fatalf("overwrote externally selected font: %+v", reply)
					}
				} else if !test.ok && test.reason != "rollback" && test.options["concurrentSize"] != true && reply.SelectedFont != reply.OriginalFont {
					t.Fatalf("failed preserve did not restore original font: %+v", reply)
				}
			}
			if reply.RepeatMutations != 0 {
				t.Fatalf("repeating preserve changed settings: %+v", reply)
			}
			if test.options["unreadableWindow"] != nil && !test.ok && len(reply.Mutations) != 0 {
				t.Fatalf("window enumeration failure changed settings: %+v", reply)
			}
			if (test.options["sameFont"] == true || test.options["expectedFont"] != nil || test.options["expectedSize"] != nil || test.options["expectedID"] != nil || test.options["expectedName"] != nil || test.reason == "size") && len(reply.Mutations) != 0 {
				t.Fatalf("unnecessary or stale setting writes: %+v", reply)
			}
			for _, mutation := range reply.Mutations {
				if mutation != "target font" && mutation != "external selection" && mutation != "external font" && mutation != "external size" {
					t.Fatalf("preserve wrote a point size, focus, profile or other state: %+v", reply)
				}
			}
		})
	}
}

func TestSnapshotProfileJavaScriptIsReadOnly(t *testing.T) {
	if !Supported() {
		t.Skip("built-in JavaScript interpreter unavailable")
	}
	const app = `Application("com.apple.Terminal")`
	if strings.Count(profileBridge, app) != 1 {
		t.Fatal("native bridge changed; review test before executing")
	}
	logic := strings.Replace(profileBridge, app, "mockTerminal", 1)
	logic = strings.Replace(logic, "function run(argv)", "function profileOperation(argv)", 1)
	if strings.Contains(logic, "Application(") {
		t.Fatal("test cannot contain native application access")
	}
	logic = "var options = {size: 13};\n" + switchProfileMock + logic + `
function run(argv) { var result = JSON.parse(profileOperation(argv)); result.mutations = mutations; return JSON.stringify(result); }
`
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	data, err := run(ctx, interpreter, []string{"-l", "JavaScript", "-e", logic, "snapshot", "OpenAI Images 0123abcd", "/dev/ttys002", ""}, []string{})
	if err != nil {
		t.Fatal(err)
	}
	var reply struct {
		OK bool
		ProfileStatus
		Mutations []string
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		t.Fatal(err)
	}
	if !reply.OK || reply.FontName != "Menlo-Regular" || reply.FontSize != 13 || reply.ProfileID != 102 || reply.ProfileName != "Basic" || len(reply.Mutations) != 0 {
		t.Fatalf("snapshot changed or misread original font: %+v", reply)
	}
}
