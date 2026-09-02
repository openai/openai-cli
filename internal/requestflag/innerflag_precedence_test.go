package requestflag

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/urfave/cli/v3"
)

func TestInnerFlagCLIValueBeatsPipedData(t *testing.T) {
	t.Parallel()

	outer := &Flag[map[string]any]{
		Name:     "address",
		BodyPath: "address",
	}
	assert.NoError(t, outer.PreParse())

	cityInner := &InnerFlag[string]{
		Name:       "address.city",
		InnerField: "city",
		OuterFlag:  outer,
	}
	assert.NoError(t, cityInner.Set("address.city", "cli-value"))
	assert.True(t, cityInner.IsSet())

	data := map[string]any{
		"address": map[string]any{"city": "piped-value"},
	}
	cmd := &cli.Command{Flags: []cli.Flag{outer, cityInner}}
	assert.NoError(t, ApplyStdinDataToFlags(cmd, data))

	outerVal, ok := outer.Get().(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "cli-value", outerVal["city"])
}
