package autocomplete

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRebuildColonSeparatedArgs(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		args []string
		want []string
	}{
		"standalone colon": {
			args: []string{"a", "b", ":", "c", "d"},
			want: []string{"a", "b:c", "d"},
		},
		"trailing colon": {
			args: []string{"config:", "get"},
			want: []string{"config:get"},
		},
		"repeated colons": {
			args: []string{"a", ":", ":", "b"},
			want: []string{"a::b"},
		},
		"ordinary arguments": {
			args: []string{"a", "b", "c"},
			want: []string{"a", "b", "c"},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.want, rebuildColonSeparatedArgs(test.args))
		})
	}
}
