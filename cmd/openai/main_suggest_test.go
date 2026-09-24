package main

import (
	"strings"
	"testing"
)

// Unknown commands suggest the full path of a close match and stay silent
// when nothing is close.
func TestMainUnknownCommandSuggestions(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		stderr string
	}{
		{[]string{"fine-tuning:alpha:graders", "rn"}, "No help topic for 'rn'. Did you mean 'openai fine-tuning:alpha:graders run'?\n"},
		{[]string{"fine-tuning:alpha:graders", "rnu"}, "No help topic for 'rnu'. Did you mean 'openai fine-tuning:alpha:graders run'?\n"},
		{[]string{"responses", "creat"}, "No help topic for 'creat'. Did you mean 'openai responses create'?\n"},
		{[]string{"RESPONSES"}, "No help topic for 'RESPONSES'. Did you mean 'openai responses'?\n"},
		{[]string{"totallybogus"}, "No help topic for 'totallybogus'\n"},
		{[]string{"responses", "zzzzz"}, "No help topic for 'zzzzz'\n"},
	} {
		t.Run(strings.Join(tc.args, "/"), func(t *testing.T) {
			want := mainDispatchResult{3, "", tc.stderr}
			if got := runMainDispatch(t, "bash", append([]string{"openai"}, tc.args...)...); got != want {
				t.Fatalf("got %+v; want %+v", got, want)
			}
		})
	}
}
