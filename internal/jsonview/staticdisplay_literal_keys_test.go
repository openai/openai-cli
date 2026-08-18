package jsonview

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestStaticDisplayUsesLiteralObjectKeys(t *testing.T) {
	t.Parallel()

	result := gjson.Parse(`{"a.b":"literal-value","a":{"b":"nested-value"}}`)
	out := formatJSON(result, 80)

	require.Contains(t, out, "literal-value")
	require.Contains(t, out, "nested-value")
}
