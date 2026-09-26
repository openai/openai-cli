package imagefontmac

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"math"
	"os"
	"regexp"
	"strings"
)

//go:embed profile.js
var profileBridge string

var ownedProfile = regexp.MustCompile(`^OpenAI Images ([0-9a-f]{8})$`)
var terminalTTY = regexp.MustCompile(`^/dev/ttys[0-9]+$`)
var postScriptName = regexp.MustCompile(`^[A-Za-z0-9-]{1,63}$`)

// ErrOtherProfile means this tab needs the image font enabled. Its Inspector
// profile name is not significant once the owned font is selected.
var ErrOtherProfile = errors.New("sharp image settings are not enabled in this tab")

// ProfileStatus describes the settings that Terminal exposes for the exact
// local tab. It does not certify line spacing, which Terminal's scripting API
// does not expose, or the window width, which callers must check separately.
type ProfileStatus struct {
	FontName    string  `json:"fontName"`
	FontSize    float64 `json:"fontSize"`
	ProfileID   int     `json:"profileID"`
	ProfileName string  `json:"profileName"`
}

// Snapshot reads the exact caller's font before preparing an image-capable copy.
// Unlike InspectProfile, the font need not belong to this gallery.
func Snapshot(ctx context.Context, profileName, tty string) (ProfileStatus, error) {
	return inspectProfile(ctx, "snapshot", profileName, tty, "", Supported, run)
}

// Preserve activates an image-capable copy only if the font and size still
// match the captured settings. It never writes a point size or selects a profile.
func Preserve(ctx context.Context, profileName, tty, fontName string, expected ProfileStatus) error {
	_, err := inspectProfile(ctx, "preserve", profileName, tty, fontName, Supported, run, expected)
	return err
}

// Restore undoes activation only while this tab still has the generated font
// and captured settings. Concurrent changes belong to the user and are left alone.
func Restore(ctx context.Context, profileName, tty, fontName string, original ProfileStatus) error {
	_, err := inspectProfile(ctx, "restore", profileName, tty, fontName, Supported, run, original)
	return err
}

// InspectProfile reads this tab's image font and size without changing it.
// An unsupported font size returns the checked status together with an error.
// This may request macOS Automation permission when the user
// runs the command; it never selects a tab or changes a default.
func InspectProfile(ctx context.Context, profileName, tty string) (ProfileStatus, error) {
	return inspectProfile(ctx, "inspect", profileName, tty, "", Supported, run)
}

func inspectProfile(ctx context.Context, action, profileName, tty, fontName string, supported func() bool, execute runner, expected ...ProfileStatus) (ProfileStatus, error) {
	var status ProfileStatus
	if err := ctx.Err(); err != nil {
		return status, err
	}
	if !supported() {
		return status, ErrUnsupported
	}
	match := ownedProfile.FindStringSubmatch(profileName)
	if match == nil {
		return status, errors.New("invalid image-font session identifier")
	}
	if action != "inspect" && action != "snapshot" && action != "preserve" && action != "restore" {
		return status, errors.New("invalid image profile operation")
	}
	if !terminalTTY.MatchString(tty) {
		return status, errors.New("image-font previews require a local Apple Terminal tab")
	}
	if (action == "preserve" || action == "restore") && (!postScriptName.MatchString(fontName) || !strings.HasPrefix(fontName, "OpenAIImages-"+match[1]+"-")) {
		return status, errors.New("the image font does not belong to this gallery profile")
	}
	args := []string{"-l", "JavaScript", "-e", profileBridge, action, profileName, tty, fontName}
	if action == "preserve" || action == "restore" {
		if len(expected) != 1 || !validCapturedFont(expected[0]) || !validCapturedProfile(expected[0]) {
			return status, errors.New("invalid original Terminal font settings")
		}
		data, _ := json.Marshal(expected[0])
		args = append(args, string(data))
	}
	data, err := execute(ctx, interpreter, args, environment(os.Environ()))
	if ctx.Err() != nil {
		return status, ctx.Err()
	}
	if err != nil {
		return status, errors.New("could not check the image profile; allow this command to control Terminal in macOS Privacy & Security > Automation, then retry")
	}
	var result struct {
		ProfileStatus
		OK     bool   `json:"ok"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return status, errors.New("macOS image profile bridge returned an invalid result")
	}
	if (action == "inspect" || action == "snapshot") && (result.OK || result.Reason == "size") {
		owned := postScriptName.MatchString(result.FontName) && strings.HasPrefix(result.FontName, "OpenAIImages-"+match[1]+"-")
		if !validCapturedFont(result.ProfileStatus) || action == "snapshot" && !validCapturedProfile(result.ProfileStatus) || action == "inspect" && !owned {
			return status, errors.New("macOS image profile bridge returned invalid font settings")
		}
		status = result.ProfileStatus
	}
	if result.OK {
		return status, nil
	}
	switch result.Reason {
	case "permission":
		return status, errors.New("allow this command to control Terminal in macOS Privacy & Security > Automation, then retry")
	case "tab":
		return status, errors.New("could not identify this local Apple Terminal tab")
	case "rollback":
		return status, errors.New("could not restore this tab's previous font after image rendering failed; restore its font in Terminal's Inspector, then retry")
	case "profile":
		return status, ErrOtherProfile
	case "font":
		return status, errors.New("Terminal did not apply the image font to this tab")
	case "size":
		return status, errors.New("this Terminal font size is unsupported; choose a whole-number point size before enabling sharp previews")
	case "changed":
		return status, errors.New("this tab's settings changed while preparing the image font; retry the command")
	default:
		return status, errors.New("could not apply image settings to this tab")
	}
}

func validCapturedFont(status ProfileStatus) bool {
	return status.FontName != "" && len(status.FontName) <= 255 && !strings.ContainsAny(status.FontName, "\x00\r\n\x1b") && !math.IsNaN(status.FontSize) && !math.IsInf(status.FontSize, 0) && status.FontSize > 0 && status.FontSize <= 1024
}

func validCapturedProfile(status ProfileStatus) bool {
	return status.ProfileID > 0 && uint64(status.ProfileID) <= 1<<53-1 && status.ProfileName != "" && len(status.ProfileName) <= 1024
}
