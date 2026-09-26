package imagefont

import (
	"bytes"
	"encoding/binary"
	"math"
	"strconv"
	"testing"
)

func TestPreservedMonacoKeepsTerminalLayoutAtOriginalSizes(t *testing.T) {
	for _, tt := range []struct{ size, height, baseline int }{{12, 16, 4}, {13, 17, 4}, {14, 19, 4}, {15, 20, 5}, {18, 25, 6}, {24, 32, 8}} {
		t.Run(strconv.Itoa(tt.size), func(t *testing.T) {
			head := make([]byte, 54)
			binary.BigEndian.PutUint16(head[18:20], 2048)
			glyphs := []byte{7, 8, 9}
			metrics := []byte{1, 2, 3, 4}
			tables := map[string][]byte{"head": head, "hhea": make([]byte, 36), "OS/2": make([]byte, 78), "glyf": glyphs, "hmtx": metrics}
			err := compensatePreservedMonaco(tables, PreserveOptions{SourcePostScript: "Monaco", PointSize: tt.size, TextHeight: tt.height, Baseline: tt.baseline})
			if err != nil {
				t.Fatal(err)
			}
			asc := float64(int16(binary.BigEndian.Uint16(tables["hhea"][4:]))) * float64(tt.size) / 2048
			desc := float64(int16(binary.BigEndian.Uint16(tables["hhea"][6:]))) * float64(tt.size) / 2048
			if int(math.Ceil(asc)+math.Ceil(-desc)) != tt.height || int(-math.Floor(desc+0.5)) != tt.baseline {
				t.Fatalf("layout changed: ascent %g descent %g", asc, desc)
			}
			if !bytes.Equal(tables["glyf"], glyphs) || !bytes.Equal(tables["hmtx"], metrics) {
				t.Fatal("text glyphs or advances changed")
			}
			if !bytes.Equal(tables["OS/2"][68:74], tables["hhea"][4:10]) {
				t.Fatal("vertical metrics disagree across tables")
			}
		})
	}
}

func TestPreservedMonacoInvalidMetricsDoNotMutateTables(t *testing.T) {
	for _, source := range []PreserveOptions{
		{SourcePostScript: "Monaco", PointSize: 13, TextHeight: 0, Baseline: 4},
		{SourcePostScript: "Monaco", PointSize: 13, TextHeight: 17, Baseline: 17},
		{SourcePostScript: "Monaco", PointSize: 0, TextHeight: 17, Baseline: 4},
	} {
		head := make([]byte, 54)
		binary.BigEndian.PutUint16(head[18:20], 2048)
		tables := map[string][]byte{"head": head, "hhea": bytes.Repeat([]byte{7}, 36), "OS/2": bytes.Repeat([]byte{8}, 78)}
		if err := compensatePreservedMonaco(tables, source); err == nil {
			t.Fatal("invalid metrics accepted")
		}
		if !bytes.Equal(tables["hhea"], bytes.Repeat([]byte{7}, 36)) || !bytes.Equal(tables["OS/2"], bytes.Repeat([]byte{8}, 78)) {
			t.Fatal("failed validation changed font tables")
		}
	}
	if err := compensatePreservedMonaco(nil, PreserveOptions{SourcePostScript: "Menlo-Regular"}); err != nil {
		t.Fatal("unrelated fonts require no compensation")
	}
}
