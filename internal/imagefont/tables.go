package imagefont

import (
	_ "embed"
	"encoding/binary"
	"sort"
	"unicode/utf16"
)

//go:embed GO-FONT-LICENSE.txt
var fontLicense string

func head(outlines [][]byte) []byte {
	xMin, yMin, xMax, yMax := int16(0), int16(-250), int16(500), int16(750)
	for _, outline := range outlines {
		if len(outline) < 10 {
			continue
		}
		xMin = min(xMin, int16(binary.BigEndian.Uint16(outline[2:4])))
		yMin = min(yMin, int16(binary.BigEndian.Uint16(outline[4:6])))
		xMax = max(xMax, int16(binary.BigEndian.Uint16(outline[6:8])))
		yMax = max(yMax, int16(binary.BigEndian.Uint16(outline[8:10])))
	}
	var b buffer
	b.u32(0x00010000)
	b.u32(0x00010000)
	b.u32(0)
	b.u32(0x5f0f3cf5)
	b.u16(3)
	b.u16(1000)
	b.zeros(16)
	b.i16(xMin)
	b.i16(yMin)
	b.i16(xMax)
	b.i16(yMax)
	b.u16(0)
	b.u16(8)
	b.i16(2)
	b.i16(1)
	b.i16(0)
	return b.Bytes()
}

func hhea(glyphCount int, outlines [][]byte) []byte {
	minLeft, minRight, maxExtent := int16(0), int16(0), int16(500)
	for _, outline := range outlines {
		if len(outline) >= 10 {
			xMin := int16(binary.BigEndian.Uint16(outline[2:4]))
			xMax := int16(binary.BigEndian.Uint16(outline[6:8]))
			minLeft = min(minLeft, xMin)
			minRight = min(minRight, 500-xMax)
			maxExtent = max(maxExtent, xMax)
		}
	}
	var b buffer
	b.u32(0x00010000)
	b.i16(750)
	b.i16(-250)
	b.i16(0)
	b.u16(500)
	b.i16(minLeft)
	b.i16(minRight)
	b.i16(maxExtent)
	b.i16(1)
	b.i16(0)
	b.i16(0)
	b.zeros(8)
	b.i16(0)
	b.u16(uint16(glyphCount))
	return b.Bytes()
}

func maxp(glyphCount int, outlines [][]byte) []byte {
	var maxPoints, maxContours uint16
	for _, outline := range outlines {
		if len(outline) < 10 {
			continue
		}
		contours := binary.BigEndian.Uint16(outline[:2])
		if contours == 0 || contours > 32767 || len(outline) < 10+int(contours)*2 {
			continue
		}
		maxContours = max(maxContours, contours)
		maxPoints = max(maxPoints, binary.BigEndian.Uint16(outline[8+int(contours)*2:])+1)
	}
	var b buffer
	b.u32(0x00010000)
	b.u16(uint16(glyphCount))
	b.u16(maxPoints)
	b.u16(maxContours)
	b.zeros(4)
	b.u16(1)
	b.zeros(16)
	return b.Bytes()
}

func os2(frames []preparedFrame) []byte {
	last := rune(126)
	for _, frame := range frames {
		last = max(last, frame.CodepointStart+rune(frame.Columns*frame.Rows)-1)
	}
	var b buffer
	b.u16(4)
	b.i16(500)
	b.u16(400)
	b.u16(5)
	b.u16(0)
	for _, v := range []int16{650, 600, 0, 75, 650, 600, 0, 350, 50, 250, 0} {
		b.i16(v)
	}
	b.Write([]byte{2, 0, 5, 9, 0, 0, 0, 0, 0, 0})
	b.u32(1)       // Unicode range bit 0: Basic Latin.
	b.u32(1 << 28) // Unicode range bit 60: private use area.
	b.u32(0)
	b.u32(0)
	b.WriteString("NONE")
	b.u16(0x00c0) // Regular, use typo metrics.
	b.u16(32)
	b.u16(uint16(last))
	b.i16(750)
	b.i16(-250)
	b.i16(0)
	b.u16(750)
	b.u16(250)
	b.zeros(8)
	b.i16(500)
	b.i16(700)
	b.u16(0)
	b.u16(32)
	b.u16(1)
	return b.Bytes()
}

func hmtx(glyphCount int, outlines [][]byte) []byte {
	var b buffer
	for i := 0; i < glyphCount; i++ {
		b.u16(500)
		if i < len(outlines) && len(outlines[i]) >= 10 {
			b.u16(binary.BigEndian.Uint16(outlines[i][2:4]))
		} else {
			b.i16(0)
		}
	}
	return b.Bytes()
}

func cmap(frames []preparedFrame) []byte {
	type segment struct{ start, end, delta uint16 }
	segments := []segment{{32, 126, uint16(1 - 32 + 65536)}}
	for _, frame := range frames {
		segments = append(segments, segment{
			start: uint16(frame.CodepointStart),
			end:   uint16(frame.CodepointStart + rune(frame.Columns*frame.Rows) - 1),
			delta: uint16(frame.firstGlyph - int(frame.CodepointStart)),
		})
	}
	segments = append(segments, segment{0xffff, 0xffff, 1})
	sort.Slice(segments, func(i, j int) bool { return segments[i].start < segments[j].start })
	n := len(segments)
	power, entry := 1, 0
	for power*2 <= n {
		power *= 2
		entry++
	}
	var table buffer
	table.u16(4)
	table.u16(uint16(16 + 8*n))
	table.u16(0)
	table.u16(uint16(2 * n))
	table.u16(uint16(2 * power))
	table.u16(uint16(entry))
	table.u16(uint16(2*n - 2*power))
	for _, segment := range segments {
		table.u16(segment.end)
	}
	table.u16(0)
	for _, segment := range segments {
		table.u16(segment.start)
	}
	for _, segment := range segments {
		table.u16(segment.delta)
	}
	table.zeros(2 * n)
	var b buffer
	b.u16(0)
	b.u16(2)
	b.u16(0)
	b.u16(3)
	b.u32(20)
	b.u16(3)
	b.u16(1)
	b.u32(20)
	b.Write(table.Bytes())
	return b.Bytes()
}

func names(options Options) []byte {
	values := []struct {
		id    uint16
		value string
	}{{0, "ASCII outlines derived from Go Mono. Copyright (c) 2016 Bigelow & Holmes Inc. All rights reserved."},
		{1, options.Family}, {2, "Regular"}, {3, options.PostScript + " 1.0"},
		{4, options.Family + " Regular"}, {5, "Version 1.0"}, {6, options.PostScript},
		{13, fontLicense}}
	var b, data buffer
	b.u16(0)
	b.u16(uint16(len(values)))
	b.u16(uint16(6 + 12*len(values)))
	for _, value := range values {
		var encoded buffer
		for _, c := range utf16.Encode([]rune(value.value)) {
			encoded.u16(c)
		}
		b.u16(3)
		b.u16(1)
		b.u16(0x409)
		b.u16(value.id)
		b.u16(uint16(encoded.Len()))
		b.u16(uint16(data.Len()))
		data.Write(encoded.Bytes())
	}
	b.Write(data.Bytes())
	return b.Bytes()
}

func post() []byte {
	var b buffer
	b.u32(0x00030000)
	b.u32(0)
	b.i16(-75)
	b.i16(50)
	b.u32(1)
	b.zeros(16)
	return b.Bytes()
}
