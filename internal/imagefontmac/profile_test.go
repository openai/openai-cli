package imagefontmac

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// These tests only inspect the proposed invocation and return fake replies.
// They never call Terminal, issue AppleEvents, or change a profile.
func TestProfileOperationsUseExactOwnedTarget(t *testing.T) {
	for _, action := range []string{"check", "inspect", "activate", "unused"} {
		t.Run(action, func(t *testing.T) {
			name, tty, font := "OpenAI Images 0123abcd", "/dev/ttys001", ""
			if action == "activate" {
				font = "OpenAIImages-0123abcd-01234567890123456789012345678901-Regular"
			}
			if action == "unused" {
				tty = ""
			}
			called := false
			err := invokeProfile(context.Background(), action, name, tty, font, func() bool { return true }, func(_ context.Context, program string, args, _ []string) ([]byte, error) {
				called = true
				if program != interpreter || !reflect.DeepEqual(args, []string{"-l", "JavaScript", "-e", profileBridge, action, name, tty, font}) {
					t.Fatal("incorrect native invocation")
				}
				return []byte(`{"ok":true,"fontName":"OpenAIImages-0123abcd-original-Regular","fontSize":16}`), nil
			})
			if err != nil || !called {
				t.Fatalf("called=%v error=%v", called, err)
			}
		})
	}
}

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
		{"activate", "Basic", "/dev/ttys001", "OpenAIImages-0123abcd-new-Regular"},
		{"activate", "OpenAI Images 0123abcd", "/dev/ttys001", "OpenAIImages-ffffffff-new-Regular"},
		{"activate", "OpenAI Images 0123abcd", "/dev/ttys001", "Menlo-Regular"},
		{"activate", "OpenAI Images 0123abcd", "/dev/ttys001", "OpenAIImages-0123abcd-\n"},
		{"switch", "OpenAI Images 0123abcd", "/dev/ttys001", "OpenAIImages-0123abcd-new-Regular"},
		{"check", "OpenAI Images 0123abcd", "/dev/pts/1", ""},
		{"check", "OpenAI Images 0123abcd", "/dev/ttys001\n", ""},
		{"other", "OpenAI Images 0123abcd", "/dev/ttys001", ""},
		{"unused", "Basic", "", ""},
	} {
		err := invokeProfile(context.Background(), tt.action, tt.profile, tt.tty, tt.font, func() bool { return true }, func(context.Context, string, []string, []string) ([]byte, error) {
			t.Fatal("called native bridge for unowned target")
			return nil, nil
		})
		if err == nil || strings.ContainsAny(err.Error(), "\n\x1b") {
			t.Fatalf("missing or unsafe error: %v", err)
		}
	}
}

func TestProfileErrorsAreActionableAndSafe(t *testing.T) {
	for _, reason := range []string{"permission", "tab", "profile", "font", "size", "changed", "selection", "in-use", "native", "missing", "ambiguous", "private\x1bdata"} {
		err := invokeProfile(context.Background(), "check", "OpenAI Images 0123abcd", "/dev/ttys001", "", func() bool { return true }, func(context.Context, string, []string, []string) ([]byte, error) {
			return []byte(`{"ok":false,"reason":"` + reason + `"}`), nil
		})
		if err == nil || strings.Contains(err.Error(), "private") || strings.ContainsRune(err.Error(), '\x1b') {
			t.Fatalf("missing or unsafe error: %v", err)
		}
		if errors.Is(err, ErrOtherProfile) != (reason == "profile") {
			t.Fatalf("ordinary profile distinction lost: reason=%s error=%v", reason, err)
		}
		if errors.Is(err, ErrProfileMissing) != (reason == "missing") {
			t.Fatalf("missing profile distinction lost: reason=%s error=%v", reason, err)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	err := invokeProfile(cancelled, "check", "OpenAI Images 0123abcd", "/dev/ttys001", "", func() bool { return true }, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
}

func TestUnusedProfileRefusesLiveTabs(t *testing.T) {
	name := "OpenAI Images 0123abcd"
	err := invokeProfile(context.Background(), "unused", name, "", "", func() bool { return true }, func(_ context.Context, program string, args, _ []string) ([]byte, error) {
		if program != interpreter || args[4] != "unused" || args[5] != name || args[6] != "" || args[7] != "" {
			t.Fatal("unused check did not address the exact gallery profile")
		}
		return []byte(`{"ok":false,"reason":"in-use"}`), nil
	})
	if err == nil || !strings.Contains(err.Error(), "close every Terminal tab") || !strings.Contains(err.Error(), "retry reset") {
		t.Fatalf("unsafe or unhelpful reset check: %v", err)
	}
}
