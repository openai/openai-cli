package terminalimage

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/imagefont"
	"github.com/openai/openai-cli/internal/imagefontmac"
	"github.com/openai/openai-cli/internal/imagegallery"
	"github.com/stretchr/testify/require"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

func TestSharpProgressRetainsPixelsTextAndEarlierPreviews(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "gallery")
	bridge := newTestFontBridge()
	original := bridge.status
	services := bridge.services(t)
	var images []*image.NRGBA
	var texts []string
	retained := map[string][]byte{}
	for stage := range 4 {
		// Four columns and two rows at 7x14 points exactly fit this 56x56
		// source in the 2x strike, allowing byte-exact pixel comparison.
		img := image.NewNRGBA(image.Rect(0, 0, 56, 56))
		for y := range 56 {
			for x := range 56 {
				img.SetNRGBA(x, y, color.NRGBA{R: uint8(x*3 + stage), G: uint8(y * 3), B: uint8(stage*50 + 9), A: 255})
			}
		}
		before := bytes.Clone(img.Pix)
		var out bytes.Buffer
		reserve := maxFontPreviewColumns * imagefont.MaxFrameRows
		if stage == 3 {
			reserve = 0
		}
		require.NoError(t, displayImageFontReserved(t.Context(), &out, img, 4, directory, "/dev/ttys001", testFontViewport, services, reserve))
		require.Equal(t, before, img.Pix)
		images = append(images, img)
		texts = append(texts, out.String())
		for _, path := range bridge.registered {
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			if old, ok := retained[path]; ok {
				require.Equal(t, old, data)
			} else {
				retained[path] = data
			}
		}
		require.Equal(t, original.FontSize, bridge.status.FontSize)
		require.Equal(t, original.ProfileID, bridge.status.ProfileID)
		require.Equal(t, original.ProfileName, bridge.status.ProfileName)
	}
	data, err := os.ReadFile(bridge.registered[len(bridge.registered)-1])
	require.NoError(t, err)
	parsed, err := sfnt.Parse(data)
	require.NoError(t, err)
	originalFont, err := sfnt.Parse(gomono.TTF)
	require.NoError(t, err)
	for _, r := range "ABC abc 123" {
		oldGlyph, err := originalFont.GlyphIndex(nil, r)
		require.NoError(t, err)
		newGlyph, err := parsed.GlyphIndex(nil, r)
		require.NoError(t, err)
		require.Equal(t, oldGlyph, newGlyph)
		oldAdvance, err := originalFont.GlyphAdvance(nil, oldGlyph, fixed.I(12), font.HintingNone)
		require.NoError(t, err)
		newAdvance, err := parsed.GlyphAdvance(nil, newGlyph, fixed.I(12), font.HintingNone)
		require.NoError(t, err)
		require.Equal(t, oldAdvance, newAdvance)
		oldOutline, err := originalFont.LoadGlyph(nil, oldGlyph, fixed.I(12), nil)
		require.NoError(t, err)
		newOutline, err := parsed.LoadGlyph(nil, newGlyph, fixed.I(12), nil)
		require.NoError(t, err)
		require.Equal(t, oldOutline, newOutline)
	}
	bitmap := testFontTable(t, data, "sbix")
	strike := bitmap[binary.BigEndian.Uint32(bitmap[8:12]):]
	require.Equal(t, uint16(24), binary.BigEndian.Uint16(strike))
	for index, text := range texts {
		rows := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
		require.Len(t, rows, 2)
		for row, characters := range rows {
			for column, r := range []rune(characters) {
				glyph, err := parsed.GlyphIndex(nil, r)
				require.NoError(t, err)
				require.NotZero(t, glyph)
				start := binary.BigEndian.Uint32(strike[4+int(glyph)*4:])
				end := binary.BigEndian.Uint32(strike[8+int(glyph)*4:])
				payload := strike[start:end]
				if column != 0 {
					require.Empty(t, payload)
					continue
				}
				require.Equal(t, "png ", string(payload[4:8]))
				require.Equal(t, int16(-6), int16(binary.BigEndian.Uint16(payload[2:4])))
				strip, err := png.Decode(bytes.NewReader(payload[8:]))
				require.NoError(t, err)
				require.Equal(t, image.Rect(0, 0, 56, 28), strip.Bounds())
				for y := range 28 {
					for x := range 56 {
						require.Equal(t, images[index].NRGBAAt(x, row*28+y), color.NRGBAModel.Convert(strip.At(x, y)))
					}
				}
			}
		}
	}
	gallery, err := imagegallery.Open(t.Context(), directory)
	require.NoError(t, err)
	require.Equal(t, 4, gallery.State().ImageCount)
	require.NoError(t, gallery.Close())
}

func testFontTable(t *testing.T, data []byte, tag string) []byte {
	t.Helper()
	for i := 0; i < int(binary.BigEndian.Uint16(data[4:6])); i++ {
		record := data[12+16*i : 28+16*i]
		if string(record[:4]) == tag {
			start, length := binary.BigEndian.Uint32(record[8:12]), binary.BigEndian.Uint32(record[12:16])
			return data[start : start+length]
		}
	}
	t.Fatalf("missing font table %s", tag)
	return nil
}

func TestSharpProgressReservesSourceCompanionAndBitmapCapacity(t *testing.T) {
	for _, reason := range []string{"source", "companion", "bitmap"} {
		t.Run(reason, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "gallery")
			bridge := newTestFontBridge()
			services := bridge.services(t)
			viewport := testFontViewport
			services.source = func(context.Context, string, int) (imagefontmac.SourceFont, error) {
				source := testFontSource()
				switch reason {
				case "source":
					source.Tables["maxp"] = bytes.Clone(source.Tables["maxp"])
					binary.BigEndian.PutUint16(source.Tables["maxp"][4:], 65535-1024)
				case "companion":
					companion := testFontSource()
					companion.PostScript = "GoMonoCompanion"
					companion.Tables["maxp"] = bytes.Clone(companion.Tables["maxp"])
					binary.BigEndian.PutUint16(companion.Tables["maxp"][4:], 65535-1024)
					source.Companions = []imagefontmac.SourceFont{companion}
				case "bitmap":
					source.Advance, source.Ascent, source.Descent, source.LineHeight = 64, 50, 14, 64
				}
				return source, nil
			}
			if reason == "bitmap" {
				viewport = func() fontViewport { return fontViewport{80, 100, 5120, 6400} }
			}
			var out bytes.Buffer
			err := displayImageFontReserved(t.Context(), &out, image.NewRGBA(image.Rect(0, 0, 16, 16)), 4, directory, "/dev/ttys001", viewport, services, maxFontPreviewColumns*imagefont.MaxFrameRows)
			var fontErr *FontError
			require.ErrorAs(t, err, &fontErr)
			require.Contains(t, err.Error(), "leave room for the final image")
			require.Empty(t, out.String())
			require.Empty(t, bridge.registered)
			for _, name := range []string{"fonts", "images"} {
				files, err := os.ReadDir(filepath.Join(directory, name))
				require.NoError(t, err)
				require.Empty(t, files)
			}
			_, err = os.Stat(filepath.Join(directory, ".pending.json"))
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestSharpProgressWriteAndRollbackFailuresStayFatal(t *testing.T) {
	for _, reason := range []string{"write", "rollback", "cancel"} {
		t.Run(reason, func(t *testing.T) {
			bridge := newTestFontBridge()
			services := bridge.services(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("synthetic " + reason + " failure")
			var out bytes.Buffer
			writer := interface{ Write([]byte) (int, error) }(&out)
			switch reason {
			case "write":
				writer = writerFunc(func([]byte) (int, error) { return 0, failure })
			case "rollback":
				services.inspect = func(context.Context, string, string) (imagefontmac.ProfileStatus, error) {
					return imagefontmac.ProfileStatus{}, errors.New("synthetic inspect failure")
				}
				services.restore = func(context.Context, string, string, string, imagefontmac.ProfileStatus) error { return failure }
			case "cancel":
				cancel()
			}
			err := displayImageFontReserved(ctx, writer, image.NewRGBA(image.Rect(0, 0, 16, 16)), 4, filepath.Join(t.TempDir(), "gallery"), "/dev/ttys001", testFontViewport, services, maxFontPreviewColumns*imagefont.MaxFrameRows)
			if reason == "cancel" {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, failure)
				var fontErr *FontError
				require.False(t, errors.As(err, &fontErr))
			}
		})
	}
}

func TestWriteProgressKeepsNativeProtocolsAndBlockFallback(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for _, protocol := range []string{"kitty", "iterm", "blocks"} {
		var before, after bytes.Buffer
		require.NoError(t, Write(t.Context(), &before, img, protocol, 4))
		require.NoError(t, WriteProgress(t.Context(), &after, img, protocol, 4))
		require.Equal(t, before.Bytes(), after.Bytes())
	}
}

func TestSharpProgressReservesCFFCapacityForFinal(t *testing.T) {
	for _, face := range []string{"primary", "companion"} {
		t.Run(face, func(t *testing.T) {
			const reserve = 32 * imagefont.MaxFrameRows
			cff := testProgressCFFSource(reserve + 8)
			originalCFF := bytes.Clone(cff.Tables["CFF "])
			source := cff
			if face == "companion" {
				source = testFontSource()
				source.Companions = []imagefontmac.SourceFont{cff}
			}
			bridge := newTestFontBridge()
			bridge.status.FontName = source.PostScript
			services := bridge.services(t)
			services.source = func(context.Context, string, int) (imagefontmac.SourceFont, error) { return source, nil }
			viewport := func() fontViewport { return fontViewport{80, 40, 560, 560} }
			directory := filepath.Join(t.TempDir(), "gallery")
			partial := image.NewNRGBA(image.Rect(0, 0, 16, 16))
			var out bytes.Buffer
			require.NoError(t, displayImageFontReserved(t.Context(), &out, partial, 4, directory, "/dev/ttys001", viewport, services, reserve))
			require.NotEmpty(t, out.String())
			retained := map[string][]byte{}
			for _, path := range bridge.registered {
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				retained[path] = data
			}
			stateBefore, err := os.ReadFile(filepath.Join(directory, "state.json"))
			require.NoError(t, err)
			registered := len(bridge.registered)
			partial.SetNRGBA(0, 0, color.NRGBA{R: 1, A: 255})
			out.Reset()
			err = displayImageFontReserved(t.Context(), &out, partial, 4, directory, "/dev/ttys001", viewport, services, reserve)
			var fontErr *FontError
			require.ErrorAs(t, err, &fontErr)
			require.Contains(t, err.Error(), "leave room for the final image")
			require.Empty(t, out.String())
			// Re-registering the currently selected immutable font is safe;
			// the rejected partial must not create or register a new one.
			for _, path := range bridge.registered[registered:] {
				_, existed := retained[path]
				require.True(t, existed)
			}
			stateAfter, err := os.ReadFile(filepath.Join(directory, "state.json"))
			require.NoError(t, err)
			require.Equal(t, stateBefore, stateAfter)
			_, err = os.Stat(filepath.Join(directory, ".pending.json"))
			require.ErrorIs(t, err, os.ErrNotExist)
			// The final adds all 1,024 reserved cells. Its cumulative CFF reaches
			// the SID boundary exactly, and must still preserve the earlier font.
			require.NoError(t, displayImageFont(t.Context(), &out, image.NewNRGBA(image.Rect(0, 0, 32, 64)), 32, directory, "/dev/ttys001", viewport, services))
			require.Len(t, strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n"), 32)
			gallery, err := imagegallery.Open(t.Context(), directory)
			require.NoError(t, err)
			require.Equal(t, reserve+8, gallery.State().UsedGlyphs)
			require.Equal(t, 2, gallery.State().ImageCount)
			require.NoError(t, gallery.Close())
			require.Equal(t, originalCFF, cff.Tables["CFF "])
			for path, before := range retained {
				after, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, before, after)
			}
		})
	}
}

// Keep Go Mono's metrics and cmap, but use synthetic empty Type 2 outlines.
// Many unused custom names exercise CFF's SID limit independently of maxp.
func testProgressCFFSource(remaining int) imagefontmac.SourceFont {
	index := func(objects [][]byte) []byte {
		data := binary.BigEndian.AppendUint16(nil, uint16(len(objects)))
		if len(objects) == 0 {
			return data
		}
		data = append(data, 4)
		offset := uint32(1)
		for _, object := range objects {
			data = binary.BigEndian.AppendUint32(data, offset)
			offset += uint32(len(object))
		}
		data = binary.BigEndian.AppendUint32(data, offset)
		for _, object := range objects {
			data = append(data, object...)
		}
		return data
	}
	source := testFontSource()
	source.PostScript = "SyntheticCFF"
	count := int(binary.BigEndian.Uint16(source.Tables["maxp"][4:]))
	names := make([][]byte, 65000-391-remaining)
	for i := range names {
		names[i] = []byte("g" + strconv.Itoa(i))
	}
	chars := make([][]byte, count)
	charset := []byte{0}
	for i := range chars {
		chars[i] = []byte{14} // Type 2 endchar.
		if i > 0 {
			charset = binary.BigEndian.AppendUint16(charset, uint16(391+i-1))
		}
	}
	nameIndex, stringIndex, charIndex := index([][]byte{[]byte(source.PostScript)}), index(names), index(chars)
	dict := func(base int) []byte {
		data := binary.BigEndian.AppendUint32([]byte{29}, uint32(base))
		data = append(data, 15, 29)
		data = binary.BigEndian.AppendUint32(data, uint32(base+len(charset)))
		return append(data, 17)
	}
	base := 4 + len(nameIndex) + len(index([][]byte{dict(0)})) + len(stringIndex) + 2
	var cff []byte
	for _, part := range [][]byte{{1, 0, 4, 4}, nameIndex, index([][]byte{dict(base)}), stringIndex, {0, 0}, charset, charIndex} {
		cff = append(cff, part...)
	}
	source.Tables["CFF "] = cff
	source.Tables["maxp"] = binary.BigEndian.AppendUint16([]byte{0, 0, 0x50, 0}, uint16(count))
	delete(source.Tables, "glyf")
	delete(source.Tables, "loca")
	return source
}

func TestSharpProgressReservesUnoccupiedCharactersForFinal(t *testing.T) {
	const occupied = rune(0xf0300)
	originalFont, err := sfnt.Parse(gomono.TTF)
	require.NoError(t, err)
	iconGlyph, err := originalFont.GlyphIndex(nil, 'A')
	require.NoError(t, err)
	for _, face := range []string{"primary", "companion"} {
		for _, record := range []string{"first", "secondary"} {
			t.Run(face+"/"+record, func(t *testing.T) {
				modified := testFontSource()
				modified.Tables["cmap"] = testProgressOccupiedCmap(modified.Tables["cmap"], occupied, iconGlyph, record == "first")
				originalCmap := bytes.Clone(modified.Tables["cmap"])
				source := modified
				if face == "companion" {
					modified.PostScript = "GoMonoCompanion"
					source = testFontSource()
					source.Companions = []imagefontmac.SourceFont{modified}
				}
				bridge := newTestFontBridge()
				services := bridge.services(t)
				services.source = func(context.Context, string, int) (imagefontmac.SourceFont, error) { return source, nil }
				directory := filepath.Join(t.TempDir(), "gallery")
				var out bytes.Buffer
				require.NoError(t, displayImageFont(t.Context(), &out, image.NewNRGBA(image.Rect(0, 0, 16, 16)), 4, directory, "/dev/ttys001", testFontViewport, services))
				earlierText := out.String()
				retained := map[string][]byte{}
				for _, path := range bridge.registered {
					data, err := os.ReadFile(path)
					require.NoError(t, err)
					retained[path] = data
				}
				stateBefore, err := os.ReadFile(filepath.Join(directory, "state.json"))
				require.NoError(t, err)
				registered := len(bridge.registered)
				partial := image.NewNRGBA(image.Rect(0, 0, 16, 16))
				partial.SetNRGBA(0, 0, color.NRGBA{R: 1, A: 255})
				out.Reset()
				// The partial alone fits below U+F0300, but consuming its 512
				// cells would make the next 512-cell final overlap that icon.
				err = displayImageFontReserved(t.Context(), &out, partial, 32, directory, "/dev/ttys001", testFontViewport, services, 32*imagefont.MaxFrameRows)
				var fontErr *FontError
				require.ErrorAs(t, err, &fontErr)
				require.Contains(t, err.Error(), "leave room for the final image")
				require.Empty(t, out.String())
				for _, path := range bridge.registered[registered:] {
					_, existed := retained[path]
					require.True(t, existed, "skipped partial must not register a new font")
				}
				stateAfter, err := os.ReadFile(filepath.Join(directory, "state.json"))
				require.NoError(t, err)
				require.Equal(t, stateBefore, stateAfter)
				_, err = os.Stat(filepath.Join(directory, ".pending.json"))
				require.ErrorIs(t, err, os.ErrNotExist)
				registered = len(bridge.registered)
				final := image.NewNRGBA(image.Rect(0, 0, 16, 16))
				final.SetNRGBA(0, 0, color.NRGBA{R: 2, A: 255})
				require.NoError(t, displayImageFont(t.Context(), &out, final, 32, directory, "/dev/ttys001", testFontViewport, services))
				require.Len(t, strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n"), 16)
				foundOriginalIcon := false
				for _, path := range bridge.registered[registered:] {
					if _, existed := retained[path]; existed {
						continue // Re-registering the previous font adds no new glyphs.
					}
					data, err := os.ReadFile(path)
					require.NoError(t, err)
					font, err := sfnt.Parse(data)
					require.NoError(t, err)
					got, err := font.GlyphIndex(nil, occupied)
					require.NoError(t, err)
					if got != 0 {
						require.Equal(t, iconGlyph, got)
						foundOriginalIcon = true
					}
					for _, r := range strings.ReplaceAll(earlierText+out.String(), "\n", "") {
						glyph, err := font.GlyphIndex(nil, r)
						require.NoError(t, err)
						require.NotZero(t, glyph)
					}
				}
				require.True(t, foundOriginalIcon, "original private character must survive final rendering")
				gallery, err := imagegallery.Open(t.Context(), directory)
				require.NoError(t, err)
				require.Equal(t, 8+512, gallery.State().UsedGlyphs)
				require.Equal(t, 2, gallery.State().ImageCount)
				require.NoError(t, gallery.Close())
				require.Equal(t, originalCmap, modified.Tables["cmap"])
				for path, before := range retained {
					after, err := os.ReadFile(path)
					require.NoError(t, err)
					require.Equal(t, before, after)
				}
			})
		}
	}
}

// Add a separate Unicode format-12 record without changing the original tables.
// Its position tests that source coverage includes secondary Unicode records.
func testProgressOccupiedCmap(original []byte, character rune, glyph sfnt.GlyphIndex, first bool) []byte {
	count := int(binary.BigEndian.Uint16(original[2:]))
	data := make([]byte, 4+8*(count+1))
	binary.BigEndian.PutUint16(data[2:], uint16(count+1))
	for i := range count {
		at := i
		if first {
			at++
		}
		copy(data[4+8*at:12+8*at], original[4+8*i:12+8*i])
		binary.BigEndian.PutUint32(data[8+8*at:], binary.BigEndian.Uint32(original[8+8*i:])+8)
	}
	data = append(data, original[4+8*count:]...)
	at := count
	if first {
		at = 0
	}
	binary.BigEndian.PutUint16(data[6+8*at:], 4) // Unicode, full repertoire.
	binary.BigEndian.PutUint32(data[8+8*at:], uint32(len(data)))
	data = append(data, 0, 12, 0, 0, 0, 0, 0, 28, 0, 0, 0, 0, 0, 0, 0, 1)
	data = binary.BigEndian.AppendUint32(data, uint32(character))
	data = binary.BigEndian.AppendUint32(data, uint32(character))
	return binary.BigEndian.AppendUint32(data, uint32(glyph))
}
