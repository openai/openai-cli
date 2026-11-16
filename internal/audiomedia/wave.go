package audiomedia

import (
	"context"
	"encoding/binary"
	"io"
	"math"
	"os"
)

type pcmFormat struct {
	order    binary.ByteOrder
	encoding uint16
	channels uint16
	rate     uint32
	bits     uint16
	align    uint16
}

func rawPCMFormat() pcmFormat {
	return pcmFormat{binary.LittleEndian, 1, 1, 24000, 16, 2}
}

type waveData struct {
	pcm       pcmFormat
	fmt       []byte
	bigEndian bool
	offset    int64
	length    int64
}

func waveFormat(raw []byte, order binary.ByteOrder, declared uint64) (pcmFormat, error) {
	if len(raw) < 16 {
		return pcmFormat{}, ErrInvalidAudio
	}
	if declared != 16 && (declared < 18 || len(raw) < 18 || uint64(order.Uint16(raw[16:18])) > declared-18) {
		return pcmFormat{}, ErrInvalidAudio
	}
	f := pcmFormat{order: order, encoding: order.Uint16(raw), channels: order.Uint16(raw[2:]),
		rate: order.Uint32(raw[4:]), align: order.Uint16(raw[12:]), bits: order.Uint16(raw[14:])}
	if f.encoding == 0xfffe {
		if len(raw) < 40 || order.Uint16(raw[16:]) < 22 {
			return f, ErrInvalidAudio
		}
		// The standard PCM/float subtype GUID contains the encoding in its first DWORD.
		guidTail := []byte{0, 0, 16, 0, 128, 0, 0, 170, 0, 56, 155, 113}
		for i, value := range guidTail {
			if raw[28+i] != value {
				return f, errNativeUnsupported
			}
		}
		code := order.Uint32(raw[24:])
		if code != 1 && code != 3 {
			return f, errNativeUnsupported
		}
		if valid := order.Uint16(raw[18:]); valid != 0 && valid != f.bits {
			return f, errNativeUnsupported
		}
		f.encoding = uint16(code)
	}
	if f.channels == 0 || f.rate == 0 || f.align == 0 {
		return f, ErrInvalidAudio
	}
	if f.encoding == 1 {
		if f.bits != 8 && f.bits != 16 && f.bits != 24 && f.bits != 32 {
			return f, errNativeUnsupported
		}
	} else if f.encoding == 3 {
		if f.bits != 32 && f.bits != 64 {
			return f, errNativeUnsupported
		}
	} else {
		return f, errNativeUnsupported
	}
	if uint64(f.channels)*uint64(f.bits/8) != uint64(f.align) {
		return f, ErrInvalidAudio
	}
	return f, nil
}

func readWave(ctx context.Context, file *os.File, size int64) (waveData, error) {
	var wave waveData
	header := make([]byte, 12)
	if size < 12 {
		return wave, ErrInvalidAudio
	}
	if _, err := file.ReadAt(header, 0); err != nil {
		return wave, ErrInvalidAudio
	}
	if string(header[:4]) == "RF64" {
		return wave, errNativeUnsupported
	}
	var order binary.ByteOrder = binary.LittleEndian
	switch string(header[:4]) {
	case "RIFF":
	case "RIFX":
		order, wave.bigEndian = binary.BigEndian, true
	default:
		return wave, ErrInvalidAudio
	}
	if string(header[8:]) != "WAVE" {
		return wave, ErrInvalidAudio
	}
	if order.Uint32(header[4:8]) == math.MaxUint32 {
		return wave, errNativeUnsupported
	}
	limit := int64(order.Uint32(header[4:])) + 8
	if limit < 12 || limit > size {
		return wave, ErrInvalidAudio
	}
	seenFormat, seenData := false, false
	for position := int64(12); position < limit; {
		if err := ctx.Err(); err != nil {
			return wave, err
		}
		if limit-position < 8 {
			return wave, ErrInvalidAudio
		}
		if _, err := file.ReadAt(header[:8], position); err != nil {
			return wave, ErrInvalidAudio
		}
		length := int64(order.Uint32(header[4:8]))
		position += 8
		if length > limit-position {
			return wave, ErrInvalidAudio
		}
		switch string(header[:4]) {
		case "fmt ":
			if seenFormat {
				return wave, ErrInvalidAudio
			}
			seenFormat = true
			// The native formats need at most the 40-byte extensible header.
			wave.fmt = make([]byte, min(length, 40))
			if _, err := file.ReadAt(wave.fmt, position); err != nil {
				return wave, ErrInvalidAudio
			}
			var err error
			wave.pcm, err = waveFormat(wave.fmt, order, uint64(length))
			if err != nil {
				return wave, err
			}
		case "data":
			if seenData {
				return wave, errNativeUnsupported
			}
			seenData = true
			wave.offset, wave.length = position, length
		}
		position += length + length%2
		if position > limit {
			return wave, ErrInvalidAudio
		}
	}
	if !seenFormat || !seenData || wave.length%int64(wave.pcm.align) != 0 {
		return wave, ErrInvalidAudio
	}
	return wave, nil
}

type pcmAnalyzer struct {
	ctx     context.Context
	format  pcmFormat
	frames  uint64
	peaks   peakBins
	pending []byte
}

func newPCMAnalyzer(ctx context.Context, format pcmFormat) *pcmAnalyzer {
	return &pcmAnalyzer{ctx: ctx, format: format}
}

func (p *pcmAnalyzer) Write(data []byte) (int, error) {
	length := len(data)
	align := int(p.format.align)
	if len(p.pending) != 0 {
		count := min(align-len(p.pending), len(data))
		p.pending = append(p.pending, data[:count]...)
		data = data[count:]
		if len(p.pending) == align {
			if err := p.frame(p.pending); err != nil {
				return 0, err
			}
			p.pending = p.pending[:0]
		}
	}
	for len(data) >= align {
		if err := p.frame(data[:align]); err != nil {
			return 0, err
		}
		data = data[align:]
	}
	p.pending = append(p.pending, data...)
	return length, nil
}

func (p *pcmAnalyzer) frame(data []byte) error {
	if p.frames%1024 == 0 {
		if err := p.ctx.Err(); err != nil {
			return err
		}
	}
	step := int(p.format.bits / 8)
	peak := 0.0
	for offset := 0; offset < len(data); offset += step {
		if offset%4096 == 0 {
			if err := p.ctx.Err(); err != nil {
				return err
			}
		}
		sample := data[offset : offset+step]
		value := 0.0
		if p.format.encoding == 3 {
			if step == 4 {
				value = float64(math.Float32frombits(p.format.order.Uint32(sample)))
			} else {
				value = math.Float64frombits(p.format.order.Uint64(sample))
			}
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return ErrInvalidAudio
			}
		} else {
			switch step {
			case 1:
				value = (float64(sample[0]) - 128) / 128
			case 2:
				value = float64(int16(p.format.order.Uint16(sample))) / 32768
			case 3:
				var integer int32
				if p.format.order == binary.LittleEndian {
					integer = int32(sample[0]) | int32(sample[1])<<8 | int32(sample[2])<<16
				} else {
					integer = int32(sample[2]) | int32(sample[1])<<8 | int32(sample[0])<<16
				}
				integer = integer << 8 >> 8
				value = float64(integer) / 8388608
			case 4:
				value = float64(int32(p.format.order.Uint32(sample))) / 2147483648
			}
		}
		peak = math.Max(peak, math.Min(1, math.Abs(value)))
	}
	p.frames++
	p.peaks.add(peak)
	return nil
}

func (p *pcmAnalyzer) finish() error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	if len(p.pending) != 0 {
		return ErrInvalidAudio
	}
	return nil
}

// decodedWave reads FFmpeg's streaming WAV without retaining decoded audio.
// Its RIFF/data lengths may be unknown. Other chunks are skipped incrementally.
type decodedWave struct {
	ctx         context.Context
	buffer      []byte
	state       int
	skip        uint64
	fmtSize     uint64
	dataLeft    uint64
	unknownData bool
	bounded     bool
	remaining   uint64
	analyzer    *pcmAnalyzer
	err         error
}

func (d *decodedWave) Write(data []byte) (int, error) {
	length := len(data)
	if d.err != nil {
		return 0, d.err
	}
	err := d.consume(data)
	if err != nil {
		d.err = err
		return 0, err
	}
	return length, nil
}

func (d *decodedWave) consume(data []byte) error {
	for len(data) != 0 {
		if err := d.ctx.Err(); err != nil {
			return err
		}
		if d.skip != 0 {
			count := min(uint64(len(data)), d.skip)
			if err := d.account(count); err != nil {
				return err
			}
			d.skip -= count
			data = data[count:]
			continue
		}
		if d.state == 3 {
			count := uint64(len(data))
			if !d.unknownData {
				count = min(count, d.dataLeft)
			}
			if err := d.account(count); err != nil {
				return err
			}
			if _, err := d.analyzer.Write(data[:count]); err != nil {
				return err
			}
			data = data[count:]
			if !d.unknownData {
				d.dataLeft -= count
				if d.dataLeft == 0 {
					d.state = 4
				}
			}
			continue
		}
		if d.state == 4 {
			return ErrInvalidAudio
		}
		need := 8
		if d.state == 0 {
			need = 12
		}
		if d.state == 2 {
			need = int(min(d.fmtSize, 40))
		}
		count := min(need-len(d.buffer), len(data))
		if err := d.account(uint64(count)); err != nil {
			return err
		}
		d.buffer = append(d.buffer, data[:count]...)
		data = data[count:]
		if len(d.buffer) < need {
			continue
		}
		switch d.state {
		case 0:
			if string(d.buffer[:4]) != "RIFF" || string(d.buffer[8:12]) != "WAVE" {
				return ErrInvalidAudio
			}
			size := uint64(binary.LittleEndian.Uint32(d.buffer[4:8]))
			if size < 4 {
				return ErrInvalidAudio
			}
			d.bounded, d.remaining = size != math.MaxUint32, size-4
			d.state = 1
		case 1:
			size := uint64(binary.LittleEndian.Uint32(d.buffer[4:8]))
			switch string(d.buffer[:4]) {
			case "fmt ":
				if d.analyzer != nil || size < 16 {
					return ErrInvalidAudio
				}
				d.state, d.fmtSize = 2, size
			case "data":
				if d.analyzer == nil {
					return ErrInvalidAudio
				}
				d.state, d.dataLeft, d.unknownData = 3, size, size == math.MaxUint32
				if size == 0 {
					d.state = 4
				}
			default:
				d.skip = size + size%2
			}
		case 2:
			format, err := waveFormat(d.buffer, binary.LittleEndian, d.fmtSize)
			if err != nil {
				return ErrInvalidAudio
			}
			d.analyzer = newPCMAnalyzer(d.ctx, format)
			d.skip = d.fmtSize - uint64(need) + d.fmtSize%2
			d.state = 1
		}
		d.buffer = d.buffer[:0]
	}
	return nil
}

func (d *decodedWave) finish() error {
	if d.err != nil {
		return d.err
	}
	if d.bounded && d.remaining != 0 {
		return ErrInvalidAudio
	}
	if d.analyzer == nil || d.state != 3 && d.state != 4 || !d.unknownData && d.dataLeft != 0 {
		return ErrInvalidAudio
	}
	return d.analyzer.finish()
}

func (d *decodedWave) account(count uint64) error {
	if !d.bounded {
		return nil
	}
	if count > d.remaining {
		return ErrInvalidAudio
	}
	d.remaining -= count
	return nil
}

var _ io.Writer = (*decodedWave)(nil)
