package imagefontmac

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestPreserveProfileArgumentsCaptureExactOriginal(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "fake-private-value")
	expected := ProfileStatus{FontName: "Original'Font$(name)`", FontSize: 13, ProfileID: 102, ProfileName: "Basic"}
	_, err := inspectProfile(t.Context(), "preserve", "OpenAI Images 0123abcd", "/dev/ttys002", "OpenAIImages-0123abcd-next-Regular", func() bool { return true }, func(_ context.Context, program string, args, env []string) ([]byte, error) {
		if program != interpreter || len(args) != 9 || !reflect.DeepEqual(args[:8], []string{"-l", "JavaScript", "-e", profileBridge, "preserve", "OpenAI Images 0123abcd", "/dev/ttys002", "OpenAIImages-0123abcd-next-Regular"}) {
			t.Fatalf("unexpected preserve command: %q", args)
		}
		var captured ProfileStatus
		if json.Unmarshal([]byte(args[8]), &captured) != nil || captured != expected || strings.Contains(profileBridge, expected.FontName) {
			t.Fatal("captured font was not isolated as JSON data")
		}
		for _, item := range env {
			if strings.HasPrefix(strings.ToUpper(item), "OPENAI_") || strings.Contains(item, "fake-private-value") {
				t.Fatal("API credential leaked to native profile bridge")
			}
		}
		return []byte(`{"ok":true}`), nil
	}, expected)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPreserveProfileRejectsInvalidCapturedSettings(t *testing.T) {
	for _, expected := range [][]ProfileStatus{nil, {{FontName: "Menlo-Regular", FontSize: 16}, {FontName: "Courier", FontSize: 16}}, {{FontSize: 16}}, {{FontName: "bad\nfont", FontSize: 16}}, {{FontName: "Menlo-Regular", FontSize: 0}}, {{FontName: "Menlo-Regular", FontSize: math.NaN()}}, {{FontName: "Menlo-Regular", FontSize: math.Inf(1)}}} {
		_, err := inspectProfile(t.Context(), "preserve", "OpenAI Images 0123abcd", "/dev/ttys002", "OpenAIImages-0123abcd-next-Regular", func() bool { return true }, func(context.Context, string, []string, []string) ([]byte, error) {
			t.Fatal("invalid captured settings reached native bridge")
			return nil, nil
		}, expected...)
		if err == nil {
			t.Fatal("invalid captured settings accepted")
		}
	}
}

func TestSnapshotReturnsOriginalFontWithoutGalleryRestriction(t *testing.T) {
	status, err := inspectProfile(t.Context(), "snapshot", "OpenAI Images 0123abcd", "/dev/ttys002", "", func() bool { return true }, func(_ context.Context, _ string, args, _ []string) ([]byte, error) {
		if args[4] != "snapshot" || args[6] != "/dev/ttys002" || args[7] != "" {
			t.Fatal("snapshot did not address exact tab")
		}
		return []byte(`{"ok":true,"fontName":"Menlo-Regular","fontSize":13,"profileID":102,"profileName":"Basic"}`), nil
	})
	if err != nil || status.FontName != "Menlo-Regular" || status.FontSize != 13 || status.ProfileID != 102 || status.ProfileName != "Basic" {
		t.Fatalf("snapshot lost original font: status=%+v error=%v", status, err)
	}
}
