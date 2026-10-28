package batches

import (
	"context"

	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBatchPickerFilteringUsesOutputCancellation(t *testing.T) {
	m := newBatchInputPicker(t.Context(), []gjson.Result{gjson.Parse(`{"id":"file_one"}`)}, 80, 24)
	ctx, cancel := context.WithCancel(t.Context())
	m.setPickerContext(ctx)
	cancel()
	m.filter()
	require.ErrorIs(t, m.err, context.Canceled)
}
