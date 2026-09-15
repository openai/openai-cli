package apiquery

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMarshalRejectsNonStringMapKeys(t *testing.T) {
	t.Parallel()

	for name, input := range map[string]map[int]string{
		"populated": {1: "one"},
		"empty":     {},
		"nil":       nil,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			values, err := Marshal(input)

			require.ErrorContains(t, err, "non-string key")
			require.Nil(t, values)
		})
	}
}

func TestMarshalKeepsStringMapKeys(t *testing.T) {
	t.Parallel()

	values, err := Marshal(map[string]string{"one": "1"})

	require.NoError(t, err)
	require.Equal(t, "1", values.Get("one"))
}
