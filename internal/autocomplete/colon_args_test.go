package autocomplete

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/urfave/cli/v3"
)

func TestRebuildColonSeparatedArgs(t *testing.T) {
	t.Parallel()

	root := &cli.Command{
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "header"},
			&cli.BoolFlag{Name: "debug"},
			&cli.StringFlag{Name: "timeout"},
		},
		Commands: []*cli.Command{
			{Name: "config:get"},
			{Name: "config:set"},
			{Name: "completions", Commands: []*cli.Command{{Name: "create"}}},
			{Name: "models", Commands: []*cli.Command{{Name: "retrieve"}}},
			{Name: "chat:completions", Commands: []*cli.Command{{Name: "create"}}},
		},
	}

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
		"flag value ending in colon before command": {
			args: []string{"--header", "chat:", "completions", "create", "--mo"},
			want: []string{"--header", "chat:", "completions", "create", "--mo"},
		},
		"bash-split colon inside flag value": {
			args: []string{"--header", "X", ":", "completions", "models", "retrieve", "--mo"},
			want: []string{"--header", "X:completions", "models", "retrieve", "--mo"},
		},
		"bash-split trailing colon before command": {
			args: []string{"--header", "chat", ":", "completions", "create", "--mo"},
			want: []string{"--header", "chat:", "completions", "create", "--mo"},
		},
		"bash-split header value before bool flag and command": {
			args: []string{"--header", "X", ":", "completions", "--debug", "models", "retrieve", "--mo"},
			want: []string{"--header", "X:completions", "--debug", "models", "retrieve", "--mo"},
		},
		"bash-split header value before value flag and command": {
			args: []string{"--header", "X", ":", "completions", "--timeout", "30", "models", "retrieve", "--mo"},
			want: []string{"--header", "X:completions", "--timeout", "30", "models", "retrieve", "--mo"},
		},
		"bash-split trailing colon before flag and command": {
			args: []string{"--header", "chat", ":", "--debug", "completions", "create", "--mo"},
			want: []string{"--header", "chat:", "--debug", "completions", "create", "--mo"},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.want, rebuildColonSeparatedArgs(root, test.args))
		})
	}
}
