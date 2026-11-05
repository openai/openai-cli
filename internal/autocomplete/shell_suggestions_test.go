package autocomplete

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShellSuggestionsRequireSafeBoundName(t *testing.T) {
	for _, name := range []string{"", "1name", "openai.exe", "my-cli", "../openai", "x'", "$(id)", "雪", "x\n", strings.Repeat("x", 65)} {
		for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish, CompletionStylePowershell} {
			script, err := RenderShellSuggestions(shell, name)
			require.Error(t, err, "%s: %q", shell, name)
			require.Empty(t, script)
		}
	}
	_, err := RenderShellSuggestions("unknown", "openai")
	require.Error(t, err)
}

func TestShellSuggestionMatcherUsesBoundName(t *testing.T) {
	for _, test := range []struct {
		name          string
		near, distant []string
	}{
		{"openai", []string{"opneai", "opnai", "openaii", "opxnai", "open_ai"}, []string{"", "git", "opnneaii", "../openai", "openai\n", "opénai", "openai;"}},
		{"client", []string{"clietn", "clien", "cclient", "cl_ent"}, []string{"openai", "clxxnt", "CLIENT", "client/path"}},
		{"_tool2", []string{"_tolo2", "_too2", "_tool22", "_tool_"}, []string{"tool", "_tXXl2", "_tool2\x1b"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, expression := shellSuggestionMatchers(test.name)
			matcher := regexp.MustCompile(expression)
			for _, word := range test.near {
				require.True(t, matcher.MatchString(word), "%q", word)
			}
			for _, word := range test.distant {
				require.False(t, matcher.MatchString(word), "%q", word)
			}
		})
	}
}

func TestShellSuggestionScriptsResolveOnlyRendererTokens(t *testing.T) {
	for _, shell := range []CompletionStyle{CompletionStyleBash, CompletionStyleZsh, CompletionStyleFish, CompletionStylePowershell} {
		script, err := RenderShellSuggestions(shell, "synthetic_cli")
		require.NoError(t, err)
		require.Contains(t, script, "openai_suggestions_disable")
		require.Contains(t, script, "synthetic_cli")
		for _, token := range []string{"__APPNAME__", "__SUGGESTION_PATTERN__", "__SUGGESTION_REGEX__"} {
			require.NotContains(t, script, token)
		}
	}
}

func TestShellSuggestionMatcherCoversEverySingleEdit(t *testing.T) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"
	for _, name := range []string{"a", "openai", "future_cli2", strings.Repeat("x", 64)} {
		t.Run(name, func(t *testing.T) {
			_, expression := shellSuggestionMatchers(name)
			matcher := regexp.MustCompile(expression)
			check := func(word string) {
				t.Helper()
				if word != "" && !matcher.MatchString(word) {
					t.Fatalf("missing one-edit spelling %q for %q", word, name)
				}
			}
			for index := 0; index <= len(name); index++ {
				for _, letter := range alphabet {
					check(name[:index] + string(letter) + name[index:])
					if index < len(name) {
						check(name[:index] + string(letter) + name[index+1:])
					}
				}
				if index < len(name) {
					check(name[:index] + name[index+1:])
				}
				if index+1 < len(name) {
					check(name[:index] + name[index+1:index+2] + name[index:index+1] + name[index+2:])
				}
			}
		})
	}
}
