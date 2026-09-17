package imagefontmac

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func sourceFixture(name string) SourceFont {
	return SourceFont{PostScript: name, Style: "Regular", Tables: map[string][]byte{"head": {1, 2, 3}, "cmap": {4, 5, 6}}, Ascent: 12, Descent: 4, Leading: 0, Advance: 8, LineHeight: 16}
}

func sourceResponse(font SourceFont) []byte {
	data, err := json.Marshal(map[string]any{"ok": true, "font": font})
	if err != nil {
		panic(err)
	}
	return data
}

func TestSourceUsesLiteralArgumentsAndExactTables(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "fake-private-value")
	name := "Literal'Font$(name)`"
	font := sourceFixture(name)
	font.Tables["OAIp"] = []byte(`{"version":1,"source_postscript":"original"}`)
	font.Companions = []SourceFont{sourceFixture("LiteralBold")}
	font.Companions[0].Style = "Bold"
	got, err := source(t.Context(), name, 13, func() bool { return true }, func(_ context.Context, program string, args, env []string) ([]byte, error) {
		if program != interpreter || !reflect.DeepEqual(args, []string{"-l", "JavaScript", "-e", sourceBridge, name, "13"}) {
			t.Fatal("font name was not passed as a literal interpreter argument")
		}
		if strings.Contains(sourceBridge, name) {
			t.Fatal("font name interpolated into script")
		}
		for _, item := range env {
			if strings.HasPrefix(strings.ToUpper(item), "OPENAI_") || strings.Contains(item, "fake-private-value") {
				t.Fatal("API credentials exposed to font source bridge")
			}
		}
		return sourceResponse(font), nil
	})
	if err != nil || !reflect.DeepEqual(got, font) {
		t.Fatalf("font tables or metrics changed: error=%v", err)
	}
}

func TestSourcePreflightAndCancellation(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, test := range []struct {
		name      string
		size      int
		supported bool
		ctx       context.Context
		want      error
	}{
		{"Menlo-Regular", 16, false, t.Context(), ErrUnsupported},
		{"Menlo-Regular", 16, true, canceled, context.Canceled},
		{"", 16, true, t.Context(), nil},
		{"bad\nfont", 16, true, t.Context(), nil},
		{"Menlo-Regular", 0, true, t.Context(), nil},
		{"Menlo-Regular", 1025, true, t.Context(), nil},
	} {
		_, err := source(test.ctx, test.name, test.size, func() bool { return test.supported }, func(context.Context, string, []string, []string) ([]byte, error) {
			t.Fatal("invalid source request reached native bridge")
			return nil, nil
		})
		if err == nil || test.want != nil && !errors.Is(err, test.want) {
			t.Fatalf("error=%v wanted=%v", err, test.want)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, err := source(ctx, "Menlo-Regular", 16, func() bool { return true }, func(context.Context, string, []string, []string) ([]byte, error) {
		cancel()
		return sourceResponse(sourceFixture("Menlo-Regular")), nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestSourceRejectsMismatchesAndPrivateDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name   string
		output []byte
		err    error
	}{
		{"missing", []byte(`{"ok":false,"reason":"missing"}`), nil},
		{"native", []byte(`{"ok":false,"reason":"private\n\u001b"}`), nil},
		{"invalid output", []byte("private\n\x1b"), nil},
		{"interpreter", nil, errors.New("private\n\x1b")},
		{"fallback", sourceResponse(sourceFixture("Fallback")), nil},
		{"empty font", sourceResponse(SourceFont{}), nil},
		{"invalid metrics", sourceResponse(SourceFont{PostScript: "Menlo-Regular", Tables: map[string][]byte{"head": {1}}, Ascent: -1}), nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := source(t.Context(), "Menlo-Regular", 16, func() bool { return true }, func(context.Context, string, []string, []string) ([]byte, error) {
				return test.output, test.err
			})
			if err == nil || strings.ContainsAny(err.Error(), "\n\x1b") || strings.Contains(err.Error(), "private") {
				t.Fatalf("missing or unsafe error: %v", err)
			}
		})
	}
}

func TestSourceResolvesOriginalLineage(t *testing.T) {
	generatedName := "OpenAIImages-0123abcd-0123456789abcdef0123456789abcdef-Regular"
	generated := SourceFont{PostScript: generatedName, Tables: map[string][]byte{}}
	generated.Tables["OAIp"] = []byte(`{"version":1,"source_postscript":"Menlo-Regular"}`)
	original := sourceFixture("Menlo-Regular")
	original.Tables["glyf"] = []byte("exact original outlines")
	var names []string
	got, err := source(t.Context(), generatedName, 16, func() bool { return true }, func(_ context.Context, _ string, args, _ []string) ([]byte, error) {
		names = append(names, args[4])
		if args[4] == generatedName {
			return sourceResponse(generated), nil
		}
		return sourceResponse(original), nil
	})
	if err != nil || !reflect.DeepEqual(names, []string{generatedName, "Menlo-Regular"}) || !reflect.DeepEqual(got, original) {
		t.Fatalf("lineage did not resolve exact original face: calls=%q error=%v", names, err)
	}
	for _, metadata := range [][]byte{nil, []byte("invalid"), []byte(`{"version":2,"source_postscript":"Menlo-Regular"}`), []byte(`{"version":1,"source_postscript":"OpenAIImages-other"}`), []byte(strings.Repeat(" ", 4097))} {
		generated.Tables["OAIp"] = metadata
		calls := 0
		_, err := source(t.Context(), generatedName, 16, func() bool { return true }, func(context.Context, string, []string, []string) ([]byte, error) {
			calls++
			return sourceResponse(generated), nil
		})
		if !errors.Is(err, ErrLegacyFont) || calls != 1 {
			t.Fatalf("legacy or malformed lineage was followed: calls=%d error=%v", calls, err)
		}
	}
}

func TestSourceBridgeIsReadOnly(t *testing.T) {
	for _, forbidden := range []string{"Application(", "RegisterFonts", "UnregisterFonts", "NSUserDefaults", "writeTo", "currentSettings", "fontName ="} {
		if strings.Contains(sourceBridge, forbidden) {
			t.Fatalf("source bridge contains unexpected mutation or application access: %s", forbidden)
		}
	}
	for _, required := range []string{"CTFontCopyAvailableTables", "CTFontCopyTable", "CTFontCopyPostScriptName", "CTFontGetGlyphsForCharacters", "CTFontGetAdvancesForGlyphs", "character[0] = 87"} {
		if !strings.Contains(sourceBridge, required) {
			t.Fatalf("source bridge missing source data operation: %s", required)
		}
	}
}

func TestSourceAcceptsOnlyPositivelyMatchedAliases(t *testing.T) {
	const requested = "Menlo Bold"
	font := sourceFixture("Menlo-Bold")
	font.Style = "Bold"
	for _, match := range []string{"", "Menlo Regular", requested} {
		data, _ := json.Marshal(map[string]any{"ok": true, "matched_name": match, "font": font})
		got, err := source(t.Context(), requested, 13, func() bool { return true }, func(context.Context, string, []string, []string) ([]byte, error) { return data, nil })
		if match == requested {
			if err != nil || got.PostScript != "Menlo-Bold" || got.Style != "Bold" {
				t.Fatalf("valid styled alias rejected: %+v %v", got, err)
			}
		} else if err == nil {
			t.Fatalf("unverified alias accepted: %q", match)
		}
	}
}

func TestSourceGeneratedAliasResolvesCanonicalLineage(t *testing.T) {
	const requested = "OpenAI Local Gallery Face"
	const generated = "OpenAIImages-0123abcd-0123456789abcdef0123456789abcdef-Regular"
	font := SourceFont{PostScript: generated, Tables: map[string][]byte{"OAIp": []byte(`{"version":1,"source_postscript":"Menlo-Regular"}`)}}
	var calls []string
	got, err := source(t.Context(), requested, 13, func() bool { return true }, func(_ context.Context, _ string, args, _ []string) ([]byte, error) {
		calls = append(calls, args[4])
		if args[4] == requested {
			data, _ := json.Marshal(map[string]any{"ok": true, "matched_name": requested, "font": font})
			return data, nil
		}
		return sourceResponse(sourceFixture("Menlo-Regular")), nil
	})
	if err != nil || got.PostScript != "Menlo-Regular" || !reflect.DeepEqual(calls, []string{requested, "Menlo-Regular"}) {
		t.Fatalf("generated alias lineage failed: %q %+v %v", calls, got, err)
	}
}

func TestSourceMissingErrorIdentifiesEscapedRequestedName(t *testing.T) {
	const requested = `Missing "Quoted" Font`
	_, err := source(t.Context(), requested, 13, func() bool { return true }, func(context.Context, string, []string, []string) ([]byte, error) {
		return []byte(`{"ok":false,"reason":"missing"}`), nil
	})
	if err == nil || !strings.Contains(err.Error(), `Missing \"Quoted\" Font`) {
		t.Fatalf("requested name not safely identified: %v", err)
	}
}

func TestSourceLineageUsesFullFaceNameAndChecksCanonicalIdentity(t *testing.T) {
	const generated = "OpenAIImages-0123abcd-0123456789abcdef0123456789abcdef-Regular"
	const lookup = "SF Mono Regular Italic"
	const canonical = "SFMono-RegularItalic"
	original := sourceFixture(canonical)
	original.LookupName = lookup
	original.Style = "Italic"
	lineage, _ := json.Marshal(map[string]any{"version": 1, "source_postscript": canonical, "source_name": lookup})
	generatedFont := SourceFont{PostScript: generated, Tables: map[string][]byte{"OAIp": lineage}}
	var names []string
	runner := func(_ context.Context, _ string, args, _ []string) ([]byte, error) {
		names = append(names, args[4])
		if args[4] == generated {
			return sourceResponse(generatedFont), nil
		}
		data, _ := json.Marshal(map[string]any{"ok": true, "matched_name": lookup, "font": original})
		return data, nil
	}
	got, err := source(t.Context(), generated, 13, func() bool { return true }, runner)
	if err != nil || !reflect.DeepEqual(names, []string{generated, lookup}) || !reflect.DeepEqual(got, original) {
		t.Fatalf("full face lineage not retained: calls=%q got=%+v err=%v", names, got, err)
	}
	original.PostScript = "DifferentFont-Italic"
	if _, err := source(t.Context(), generated, 13, func() bool { return true }, runner); err == nil || !strings.Contains(err.Error(), "different face") {
		t.Fatalf("mismatching original identity accepted: %v", err)
	}
}

func TestSourceRejectsInvalidLookupNames(t *testing.T) {
	const generated = "OpenAIImages-0123abcd-0123456789abcdef0123456789abcdef-Regular"
	for _, name := range []string{"bad\nface", strings.Repeat("x", 256), "OpenAIImages-other"} {
		font := sourceFixture("Menlo-Regular")
		font.LookupName = name
		if _, err := source(t.Context(), font.PostScript, 13, func() bool { return true }, func(context.Context, string, []string, []string) ([]byte, error) { return sourceResponse(font), nil }); err == nil {
			t.Fatalf("invalid native lookup name accepted: %q", name)
		}
		lineage, _ := json.Marshal(map[string]any{"version": 1, "source_postscript": "Menlo-Regular", "source_name": name})
		owned := SourceFont{PostScript: generated, Tables: map[string][]byte{"OAIp": lineage}}
		calls := 0
		if _, err := source(t.Context(), generated, 13, func() bool { return true }, func(context.Context, string, []string, []string) ([]byte, error) {
			calls++
			return sourceResponse(owned), nil
		}); !errors.Is(err, ErrLegacyFont) || calls != 1 {
			t.Fatalf("invalid lineage lookup name followed: %q calls=%d err=%v", name, calls, err)
		}
	}
}
