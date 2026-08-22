package apiquery

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMarshalRejectsNonStringMapKeys(t *testing.T) {
	t.Parallel()

	values, err := Marshal(map[int]string{1: "one"})

	require.ErrorContains(t, err, "non-string key")
	require.Nil(t, values)
}

func TestMarshalKeepsStringMapKeys(t *testing.T) {
	t.Parallel()

	values, err := Marshal(map[string]string{"one": "1"})

	require.NoError(t, err)
	require.Equal(t, "1", values.Get("one"))
}
