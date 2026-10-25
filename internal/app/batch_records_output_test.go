package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestBatchRecordStreamingJSONFormatsAcrossChunks(t *testing.T) {
	document := `{"batch_id":"batch_test","data":[{"empty":{},"array":[],"str":"a\n\u202e\\\"😀","number":9007199254740993,"bools":[true,false,null]}]}`
	for _, indent := range []bool{false, true} {
		for _, color := range []bool{false, true} {
			var out bytes.Buffer
			stream := batchJSONOutput{out: bufio.NewWriter(&out), indent: indent, color: color}
			for i := 0; i < len(document); i++ {
				n, err := stream.Write([]byte{document[i]})
				require.NoError(t, err)
				require.Equal(t, 1, n)
			}
			require.NoError(t, stream.out.Flush())
			var compact bytes.Buffer
			require.NoError(t, json.Compact(&compact, []byte(ansi.Strip(out.String()))))
			require.Equal(t, document, compact.String())
		}
	}
}
