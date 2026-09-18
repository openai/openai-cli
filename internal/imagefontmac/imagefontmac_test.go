package imagefontmac

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBridgePassesArgumentsWithoutEvaluation(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "fake-private-value")
	t.Setenv("openai_test_secret", "fake-private-value")
	path := filepath.Join(t.TempDir(), "literal ' $() `font`.ttf")
	if err := os.WriteFile(path, []byte("test font"), 0600); err != nil {
		t.Fatal(err)
	}
	called := 0
	result, err := invoke(context.Background(), "register", path, func() bool { return true }, func(_ context.Context, program string, args, env []string) ([]byte, error) {
		called++
		if program != interpreter || !reflect.DeepEqual(args, []string{"-l", "JavaScript", "-e", bridge, "register", path}) {
			t.Fatalf("unexpected interpreter invocation: %q %q", program, args)
		}
		if strings.Contains(bridge, path) {
			t.Fatal("caller input interpolated into source")
		}
		for _, entry := range env {
			if strings.HasPrefix(strings.ToUpper(entry), "OPENAI_") || strings.Contains(entry, "fake-private-value") {
				t.Fatal("API environment exposed to native bridge")
			}
		}
		return json.Marshal(nativeResult{OK: true})
	})
	if err != nil || called != 1 || !result.OK {
		t.Fatalf("result=%+v calls=%d error=%v", result, called, err)
	}
}

func TestBridgeFailuresAreSafe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "font.ttf")
	if err := os.WriteFile(path, []byte("test font"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		output string
		err    error
		code   int
	}{
		{"native error", `{"ok":false,"code":105}`, nil, 105},
		{"invalid output", "private\x1b[2J", nil, 0},
		{"interpreter failure", "private", errors.New("private\n\x1b[2J"), 0},
		{"missing success", `{}`, nil, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := invoke(context.Background(), "register", path, func() bool { return true }, func(context.Context, string, []string, []string) ([]byte, error) {
				return []byte(tt.output), tt.err
			})
			if err == nil || strings.ContainsAny(err.Error(), "\x1b\n") || strings.Contains(err.Error(), "private") {
				t.Fatalf("unsafe or missing error: %v", err)
			}
			if tt.code != 0 {
				var native *NativeError
				if !errors.As(err, &native) || native.Code != tt.code {
					t.Fatalf("native error code lost: %v", err)
				}
			}
		})
	}
}

func TestBridgePreflight(t *testing.T) {
	path := filepath.Join(t.TempDir(), "font.ttf")
	if err := os.WriteFile(path, []byte("test font"), 0600); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tt := range []struct {
		name, action, path string
		ctx                context.Context
		supported          bool
		want               error
	}{
		{"unsupported", "register", path, context.Background(), false, ErrUnsupported},
		{"cancelled", "register", path, cancelled, true, context.Canceled},
		{"directory", "register", filepath.Dir(path), context.Background(), true, nil},
		{"missing", "register", path + "\n\x1bmissing", context.Background(), true, os.ErrNotExist},
		{"invalid action", "bad", path, context.Background(), true, nil},
		{"removed profile action", "profile", path, context.Background(), true, nil},
		{"nul path", "register", "bad\x00path", context.Background(), true, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := invoke(tt.ctx, tt.action, tt.path, func() bool { return tt.supported }, func(context.Context, string, []string, []string) ([]byte, error) {
				t.Fatal("native bridge invoked despite preflight failure")
				return nil, nil
			})
			if err == nil || tt.want != nil && !errors.Is(err, tt.want) || strings.ContainsAny(err.Error(), "\n\x1b") {
				t.Fatalf("error=%v want=%v", err, tt.want)
			}
		})
	}
}

func TestUnregisterMissingFileStillInvokesNativeAPI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "removed-font.ttf")
	called := false
	_, err := invoke(context.Background(), "unregister", path, func() bool { return true }, func(_ context.Context, _ string, args, _ []string) ([]byte, error) {
		called = true
		if args[4] != "unregister" || args[5] != path {
			t.Fatal("cleanup did not use original font URL")
		}
		return []byte(`{"ok":true}`), nil
	})
	if err != nil || !called {
		t.Fatalf("called=%v error=%v", called, err)
	}
}

func TestCancellationWinsNativeResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	_, err := invoke(ctx, "unregister", "/font.ttf", func() bool { return true }, func(context.Context, string, []string, []string) ([]byte, error) {
		cancel()
		return []byte(`{"ok":true}`), nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestEnvironmentPreservesUnrelatedSettings(t *testing.T) {
	source := []string{"PATH=/bin", "HOME=/home/test", "OPENAI_API_KEY=fake", "openai_admin_key=fake", "TERM=xterm", "OPENAI_IMAGE_SESSION=fake"}
	if got := environment(source); !reflect.DeepEqual(got, []string{"PATH=/bin", "HOME=/home/test", "TERM=xterm"}) {
		t.Fatalf("environment=%q", got)
	}
}

func TestBridgeDoesNotContainTerminalAutomation(t *testing.T) {
	for _, forbidden := range []string{"Application(", "Application.currentApplication", "CommandString", "RunCommandAsShell", "kCTFontManagerScopePersistent"} {
		if bytes.Contains([]byte(bridge), []byte(forbidden)) {
			t.Fatalf("unexpected native bridge capability %s", forbidden)
		}
	}
	if !strings.Contains(bridge, "Number($.kCTFontManagerScopeSession)") {
		t.Fatal("font registration scopes must use SDK names")
	}
}
