package autocomplete

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/urfave/cli/v3"
)

func TestRebuildColonSeparatedArgs(t *testing.T) {
	t.Parallel()

	root := &cli.Command{Commands: []*cli.Command{
		{Name: "config:get"},
		{Name: "config:set"},
	}}

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
		"colon-ending value before flag": {
			args: []string{"--instructions", "Prefix:", "--mo"},
			want: []string{"--instructions", "Prefix:", "--mo"},
		},
		"colon-ending ordinary value": {
			args: []string{"Prefix:", "value"},
			want: []string{"Prefix:", "value"},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.want, rebuildColonSeparatedArgs(root, test.args))
		})
	}
}
