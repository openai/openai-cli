package imagefontmac

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// These tests only inspect the proposed invocation and return fake replies.
// They never call Terminal, issue AppleEvents, or change a profile.
func TestInspectProfileReturnsOnlyCheckedFontSettings(t *testing.T) {
	for _, tt := range []struct {
		name, reply string
		wantSize    float64
		wantError   bool
	}{
		{"normal", `{"ok":true,"fontName":"OpenAIImages-0123abcd-original-Regular","fontSize":16}`, 16, false},
		{"large", `{"ok":true,"fontName":"OpenAIImages-0123abcd-original-Regular","fontSize":32}`, 32, false},
		{"unsupported size", `{"ok":false,"reason":"size","fontName":"OpenAIImages-0123abcd-original-Regular","fontSize":17.5}`, 17.5, true},
		{"other profile", `{"ok":false,"reason":"profile","fontName":"private","fontSize":17}`, 0, true},
		{"missing settings", `{"ok":true}`, 0, true},
		{"wrong gallery", `{"ok":true,"fontName":"OpenAIImages-ffffffff-original-Regular","fontSize":16}`, 0, true},
		{"invalid name", `{"ok":true,"fontName":"OpenAIImages-0123abcd-\u001bprivate","fontSize":16}`, 0, true},
		{"invalid size", `{"ok":true,"fontName":"OpenAIImages-0123abcd-original-Regular","fontSize":0}`, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			status, err := inspectProfile(context.Background(), "inspect", "OpenAI Images 0123abcd", "/dev/ttys001", "", func() bool { return true }, func(context.Context, string, []string, []string) ([]byte, error) {
				return []byte(tt.reply), nil
			})
			if (err != nil) != tt.wantError || status.FontSize != tt.wantSize {
				t.Fatalf("status=%+v error=%v", status, err)
			}
			if tt.wantSize != 0 && status.FontName != "OpenAIImages-0123abcd-original-Regular" {
				t.Fatalf("missing checked font name: %+v", status)
			}
			if tt.wantSize == 0 && status.FontName != "" {
				t.Fatalf("unchecked font name exposed: %+v", status)
			}
			if err != nil && (strings.Contains(err.Error(), "private") || strings.ContainsRune(err.Error(), '\x1b')) {
				t.Fatalf("untrusted native data in error: %v", err)
			}
		})
	}
}

func TestProfileOperationsRejectUnownedTargets(t *testing.T) {
	for _, tt := range []struct{ action, profile, tty, font string }{
		{"preserve", "Basic", "/dev/ttys001", "OpenAIImages-0123abcd-new-Regular"},
		{"preserve", "OpenAI Images 0123abcd", "/dev/ttys001", "OpenAIImages-ffffffff-new-Regular"},
		{"preserve", "OpenAI Images 0123abcd", "/dev/ttys001", "Menlo-Regular"},
		{"preserve", "OpenAI Images 0123abcd", "/dev/ttys001", "OpenAIImages-0123abcd-\n"},
		{"switch", "OpenAI Images 0123abcd", "/dev/ttys001", "OpenAIImages-0123abcd-new-Regular"},
		{"inspect", "OpenAI Images 0123abcd", "/dev/pts/1", ""},
		{"inspect", "OpenAI Images 0123abcd", "/dev/ttys001\n", ""},
		{"other", "OpenAI Images 0123abcd", "/dev/ttys001", ""},
		{"unused", "Basic", "", ""},
	} {
		_, err := inspectProfile(context.Background(), tt.action, tt.profile, tt.tty, tt.font, func() bool { return true }, func(context.Context, string, []string, []string) ([]byte, error) {
			t.Fatal("called native bridge for unowned target")
			return nil, nil
		})
		if err == nil || strings.ContainsAny(err.Error(), "\n\x1b") {
			t.Fatalf("missing or unsafe error: %v", err)
		}
	}
}

func TestProfileErrorsAreActionableAndSafe(t *testing.T) {
	for _, reason := range []string{"permission", "tab", "profile", "font", "size", "changed", "native", "private\x1bdata"} {
		_, err := inspectProfile(context.Background(), "inspect", "OpenAI Images 0123abcd", "/dev/ttys001", "", func() bool { return true }, func(context.Context, string, []string, []string) ([]byte, error) {
			return []byte(`{"ok":false,"reason":"` + reason + `"}`), nil
		})
		if err == nil || strings.Contains(err.Error(), "private") || strings.ContainsRune(err.Error(), '\x1b') {
			t.Fatalf("missing or unsafe error: %v", err)
		}
		if errors.Is(err, ErrOtherProfile) != (reason == "profile") {
			t.Fatalf("ordinary profile distinction lost: reason=%s error=%v", reason, err)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := inspectProfile(cancelled, "inspect", "OpenAI Images 0123abcd", "/dev/ttys001", "", func() bool { return true }, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
}
