package audiomedia

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testWave(encoding, channels, bits uint16, rate uint32, data []byte) []byte {
	result := make([]byte, 44+len(data)+len(data)%2)
	copy(result, "RIFF")
	binary.LittleEndian.PutUint32(result[4:], uint32(len(result)-8))
	copy(result[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(result[16:], 16)
	binary.LittleEndian.PutUint16(result[20:], encoding)
	binary.LittleEndian.PutUint16(result[22:], channels)
	binary.LittleEndian.PutUint32(result[24:], rate)
	align := channels * (bits / 8)
	binary.LittleEndian.PutUint32(result[28:], rate*uint32(align))
	binary.LittleEndian.PutUint16(result[32:], align)
	binary.LittleEndian.PutUint16(result[34:], bits)
	copy(result[36:], "data")
	binary.LittleEndian.PutUint32(result[40:], uint32(len(data)))
	copy(result[44:], data)
	return result
}

func testFile(t *testing.T, data []byte) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "private audio snapshot")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	return file
}

func TestAnalyzeNativeWaveAndPCM(t *testing.T) {
	float32Data, float64Data := make([]byte, 8), make([]byte, 16)
	binary.LittleEndian.PutUint32(float32Data, math.Float32bits(-0.75))
	binary.LittleEndian.PutUint64(float64Data, math.Float64bits(0.25))
	for _, test := range []struct {
		name           string
		encoding, bits uint16
		data           []byte
		peak           float64
	}{
		{"pcm8", 1, 8, []byte{0, 128}, 1},
		{"pcm16", 1, 16, []byte{0, 128, 0, 0}, 1},
		{"pcm24", 1, 24, []byte{0, 0, 128, 0, 0, 0}, 1},
		{"pcm32", 1, 32, []byte{0, 0, 0, 128, 0, 0, 0, 0}, 1},
		{"float32", 3, 32, float32Data, 0.75},
		{"float64", 3, 64, float64Data, 0.25},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := testWave(test.encoding, 1, test.bits, 48000, test.data)
			file := testFile(t, body)
			if _, err := file.Seek(7, 0); err != nil {
				t.Fatal(err)
			}
			result, err := Analyze(t.Context(), file, "wav")
			if err != nil {
				t.Fatal(err)
			}
			if result.Duration != 2.0/48000 || result.Bytes != int64(len(body)) || result.Format != "wav" {
				t.Fatalf("analysis: %+v", result)
			}
			if len(result.Peaks) != 2 || result.Peaks[0] != test.peak || result.Peaks[1] != 0 {
				t.Fatalf("peaks: %v", result.Peaks)
			}
			if position, err := file.Seek(0, 1); err != nil || position != 7 {
				t.Fatalf("caller offset changed: %d %v", position, err)
			}
		})
	}
	result, err := Analyze(t.Context(), testFile(t, []byte{0, 128, 0, 0}), "pcm")
	if err != nil || result.Duration != 2.0/24000 || len(result.Peaks) != 2 || result.Peaks[0] != 1 {
		t.Fatalf("PCM: %+v %v", result, err)
	}
}

func TestAnalyzeStereoPreservesOppositePhaseAndSilence(t *testing.T) {
	data := []byte{0, 128, 255, 127, 0, 0, 0, 0}
	result, err := Analyze(t.Context(), testFile(t, testWave(1, 2, 16, 24000, data)), "wav")
	if err != nil || len(result.Peaks) != 2 || result.Peaks[0] != 1 || result.Peaks[1] != 0 {
		t.Fatalf("stereo: %+v %v", result, err)
	}
	if result.Duration != 2.0/24000 {
		t.Fatalf("channel count changed duration: %v", result.Duration)
	}
}

func TestAnalyzeMalformedAndEmptyNativeAudio(t *testing.T) {
	valid := testWave(1, 1, 16, 24000, []byte{0, 0})
	badAlign := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint16(badAlign[32:], 7)
	badFloat := testWave(3, 1, 32, 24000, []byte{0, 0, 192, 127})
	for _, data := range [][]byte{nil, []byte("RIFF"), valid[:len(valid)-1], badAlign, badFloat} {
		result, err := Analyze(t.Context(), testFile(t, data), "wav")
		if !errors.Is(err, ErrInvalidAudio) || result.Duration != 0 || len(result.Peaks) != 0 {
			t.Fatalf("malformed accepted: %+v %v", result, err)
		}
	}
	if _, err := Analyze(t.Context(), testFile(t, []byte{0}), "pcm"); !errors.Is(err, ErrInvalidAudio) {
		t.Fatalf("odd PCM: %v", err)
	}
	for _, format := range []string{"pcm", "wav"} {
		data := []byte{}
		if format == "wav" {
			data = testWave(1, 1, 16, 24000, nil)
		}
		result, err := Analyze(t.Context(), testFile(t, data), format)
		if err != nil || result.Duration != 0 || len(result.Peaks) != 0 {
			t.Fatalf("empty %s: %+v %v", format, result, err)
		}
	}
}

func TestAnalyzeLongAudioUsesBoundedRealPeaks(t *testing.T) {
	const frames = 250001
	data := make([]byte, frames*2)
	data[len(data)-1] = 128
	result, err := Analyze(t.Context(), testFile(t, data), "pcm")
	if err != nil {
		t.Fatal(err)
	}
	if result.Duration != float64(frames)/24000 || len(result.Peaks) > 512 {
		t.Fatalf("long analysis: %+v", result)
	}
	for _, peak := range result.Peaks[:len(result.Peaks)-1] {
		if peak != 0 {
			t.Fatalf("invented earlier signal: %v", result.Peaks)
		}
	}
	if result.Peaks[len(result.Peaks)-1] != 1 {
		t.Fatal("final partial bin lost its signal")
	}
}

type cancelDuringScan struct {
	context.Context
	checks int
}

func (c *cancelDuringScan) Err() error {
	c.checks++
	if c.checks >= 8 {
		return context.Canceled
	}
	return nil
}

func TestAnalyzeCancellationWhileScanning(t *testing.T) {
	file := testFile(t, make([]byte, 1<<20))
	ctx := &cancelDuringScan{Context: context.Background()}
	result, err := Analyze(ctx, file, "pcm")
	if !errors.Is(err, context.Canceled) || len(result.Peaks) != 0 {
		t.Fatalf("cancellation: %+v %v", result, err)
	}
	if _, err := file.ReadAt(make([]byte, 1), 0); err != nil {
		t.Fatalf("caller file closed: %v", err)
	}
}

func TestWaveformShowsMeasuredShapeWithoutANSI(t *testing.T) {
	got := Waveform([]float64{0, 1, 0}, 3, 5)
	want := " ⣿ \n ⣿ \n─⣿─\n ⣿ \n ⣿ "
	if got != want {
		t.Fatalf("shape: %q", got)
	}
	if Waveform(nil, 20, 5) != "" || Waveform([]float64{1}, 0, 5) != "" {
		t.Fatal("missing audio invented waveform")
	}
	if got := Waveform([]float64{0, 0, 0}, 3, 1); got != "───" {
		t.Fatalf("silence: %q", got)
	}
	large := Waveform([]float64{1, math.NaN(), math.Inf(1)}, math.MaxInt, math.MaxInt)
	if strings.Contains(large, "\x1b") || len(strings.Split(large, "\n")) != 31 {
		t.Fatal("unbounded or colored waveform")
	}
}

func TestDecodedWaveBoundariesAndChunkSplits(t *testing.T) {
	valid := testWave(1, 1, 16, 24000, []byte{0, 128, 0, 0})
	for _, streaming := range []bool{false, true} {
		body := append([]byte(nil), valid...)
		if streaming {
			binary.LittleEndian.PutUint32(body[4:], math.MaxUint32)
			binary.LittleEndian.PutUint32(body[40:], math.MaxUint32)
		}
		decoder := &decodedWave{ctx: t.Context()}
		for _, value := range body {
			if _, err := decoder.Write([]byte{value}); err != nil {
				t.Fatal(err)
			}
		}
		if err := decoder.finish(); err != nil {
			t.Fatal(err)
		}
		if decoder.analyzer.frames != 2 || decoder.analyzer.peaks.finish()[0] != 1 {
			t.Fatal("split decoder lost samples")
		}
	}
	badBounds := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(badBounds[4:], 4)
	for _, body := range [][]byte{badBounds, valid[:len(valid)-1], append(valid, 1)} {
		decoder := &decodedWave{ctx: t.Context()}
		_, writeErr := decoder.Write(body)
		if writeErr == nil && decoder.finish() == nil {
			t.Fatal("malformed decoded WAV accepted")
		}
	}
	for _, declared := range []uint64{17, 18} {
		raw := append(append([]byte(nil), valid[20:36]...), 22, 0)
		if _, err := waveFormat(raw, binary.LittleEndian, declared); !errors.Is(err, ErrInvalidAudio) {
			t.Fatalf("bad fmt size %d: %v", declared, err)
		}
	}
}

func TestOffsetFramesUsesWideArithmetic(t *testing.T) {
	if got := offsetFrames(1250*time.Millisecond, 24000, 48000); got != 30000 {
		t.Fatal(got)
	}
	if got := offsetFrames(time.Duration(math.MaxInt64), math.MaxUint32, math.MaxInt64); got != math.MaxInt64 {
		t.Fatal(got)
	}
	if got := offsetFrames(time.Second, 24000, 1); got != 1 {
		t.Fatal(got)
	}
}

func TestNativeWaveUsesDeclaredLengthAndPreservesCaller(t *testing.T) {
	body := append(testWave(1, 1, 16, 24000, []byte{0, 128}), bytes.Repeat([]byte{255}, 20)...)
	file := testFile(t, body)
	result, err := Analyze(t.Context(), file, "wav")
	if err != nil || result.Duration != 1.0/24000 {
		t.Fatalf("trailing metadata became audio: %+v %v", result, err)
	}
	if _, err := Analyze(t.Context(), file, "https"); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatal(err)
	}
}
