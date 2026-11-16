package audiomedia

import (
	"context"
	"math"
	"regexp"
	"strconv"
	"time"
)

// FFplay 4.4 and 8.0 write audio-clock status to stderr at loglevel error with
// -stats. Older versions append frame-correction counts. Startup clocks can be NaN
// before the audio stream exists. Only complete status records suppress errors.
// Keep this grammar narrow: device failures can otherwise exit successfully.
var playerStatus = regexp.MustCompile(`^[ \t]*([+-]?(?:[0-9]+\.[0-9]+|nan|inf))[ \t]+(M-A|M-V|A-V|[ \t]*):[ \t]*[+-]?(?:[0-9]+\.[0-9]+|nan|inf)[ \t]+fd=[ \t]*[0-9]+[ \t]+aq=[ \t]*[0-9]+KB[ \t]+vq=[ \t]*[0-9]+KB[ \t]+sq=[ \t]*[0-9]+B(?:[ \t]+f=[0-9]+/[0-9]+)?[ \t]*$`)

type playerOutput struct {
	ctx     context.Context
	offset  time.Duration
	report  func(time.Duration)
	present bool
	last    time.Duration
	started bool
	// This bounds helper diagnostic capture, not audio input or playback length.
	record   [512]byte
	length   int
	dropping bool
	afterCR  bool
}

func (p *playerOutput) Write(data []byte) (int, error) {
	if p.report == nil {
		p.present = p.present || len(data) > 0
		return len(data), nil
	}
	for _, value := range data {
		if value == '\r' || value == '\n' {
			if p.length > 0 && !p.dropping {
				p.consume()
			} else if p.length == 0 && !(value == '\n' && p.afterCR) {
				p.present = true
			}
			clear(p.record[:p.length])
			p.length, p.dropping = 0, false
			p.afterCR = value == '\r'
			continue
		}
		p.afterCR = false
		if p.dropping {
			continue
		}
		if p.length == len(p.record) {
			p.present, p.dropping = true, true
			continue
		}
		p.record[p.length] = value
		p.length++
	}
	return len(data), nil
}

func (p *playerOutput) consume() {
	fields := playerStatus.FindSubmatch(p.record[:p.length])
	if fields == nil {
		p.present = true
		return
	}
	if string(fields[2]) != "M-A" || p.ctx.Err() != nil {
		return
	}
	seconds, err := strconv.ParseFloat(string(fields[1]), 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 ||
		seconds >= float64(math.MaxInt64)/float64(time.Second) {
		return
	}
	elapsed := time.Duration(seconds * float64(time.Second))
	if elapsed > time.Duration(math.MaxInt64)-p.offset {
		return
	}
	position := p.offset + elapsed
	if p.started && position <= p.last {
		return
	}
	p.last, p.started = position, true
	p.report(position)
}

// Wait joins stderr copying before finish. An unterminated record is diagnostic
// output, not a complete player status. Never expose retained helper text.
func (p *playerOutput) finish() {
	p.present = p.present || p.length > 0
	clear(p.record[:p.length])
	p.length = 0
}
