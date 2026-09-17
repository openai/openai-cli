package jsonview

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tidwall/gjson"
)

func TestFormatResultHandlesNonPositiveStringWidth(t *testing.T) {
	t.Parallel()

	result := gjson.Parse(`"abcdef"`)
	formatted := formatResult(result, 0, 0)

	assert.NotContains(t, formatted, "abcdef")
}

func TestTruncateStringToWidthOneUsesEllipsis(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "…", truncateStringToWidth("abcdef", 1))
}
