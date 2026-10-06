// SPDX-License-Identifier: AGPL-3.0-only

package snd

import "fmt"

// The sequence language (the sound driver's voice_process_primary in the decomp's sound_voice.c; the macros of
// include/sndseq.h name the commands).
//
// A track is a delay, then events, each but CE, CF and FF followed by the delay to the next event. A delay is
// 0 to 3 bytes below 0x80, 7 bits each, most significant first (no bytes: none). A note is 0x80|velocity (0-63),
// then the key (bit 7: tied into the next note), then its length as a MIDI variable-length number. Commands are
// 0xC0-0xFF with fixed argument counts. Times are ticks; TEMPO is ticks per frame x 256.

const (
	OpNop       = 0xC0
	OpTempo     = 0xC1
	OpBank      = 0xC2
	OpBend      = 0xC3
	OpProg      = 0xC4
	OpPLFO      = 0xC5
	OpVol       = 0xC6
	OpPan       = 0xC7
	OpExpr      = 0xC8
	OpPorta     = 0xC9
	OpRepeat    = 0xCA
	OpEndRepeat = 0xCB
	OpJump1     = 0xCC
	OpJump2     = 0xCD
	OpJump      = 0xCE
	OpJumpTrack = 0xCF
	OpLoopStart = 0xD0 // D0-D3
	OpLoop      = 0xD4 // D4-D7
	OpLoopExit  = 0xD8 // D8-DB
	OpKey       = 0xDC
	OpKeyAdd    = 0xDD
	OpTune      = 0xDE
	OpTuneAdd   = 0xDF
	OpLFOSync   = 0xE0
	OpLFORate   = 0xE1
	OpVLFO      = 0xE2
	OpPrio      = 0xE3
	OpFine      = 0xE7
	OpStatus    = 0xE8
	OpEnd       = 0xFF
)

// ArgCount is the number of argument bytes of a command.
func ArgCount(op byte) int {
	switch {
	case op == OpTempo, op == OpJump1, op == OpJump2, op == OpJump, op == 0xE4, op == 0xE5, op == OpStatus:
		return 2
	case op >= 0xC2 && op <= 0xC9, op == OpJumpTrack:
		return 1
	case op >= 0xD4 && op <= 0xD7:
		return 1
	case op >= 0xD8 && op <= 0xDB:
		return 2
	case op >= 0xDC && op <= 0xE3, op == 0xE6, op == OpFine:
		return 1
	}
	return 0
}

// HasDelay tells whether a delay follows the command (not after the unconditional jumps and the end).
func HasDelay(op byte) bool { return op != OpJump && op != OpJumpTrack && op != OpEnd }

var opNames = map[byte]string{
	OpNop: "NOP", OpTempo: "TEMPO", OpBank: "BANK", OpBend: "BEND", OpProg: "PROG", OpPLFO: "PLFO", OpVol: "VOL",
	OpPan: "PAN", OpExpr: "EXPR", OpPorta: "PORTA", OpRepeat: "REPEAT", OpEndRepeat: "END_REPEAT", OpJump1: "JUMP1",
	OpJump2: "JUMP2", OpJump: "JUMP", OpJumpTrack: "JUMP_TRACK", OpKey: "KEY", OpKeyAdd: "KEY_ADD", OpTune: "TUNE",
	OpTuneAdd: "TUNE_ADD", OpLFOSync: "LFO_SYNC", OpLFORate: "LFO_RATE", OpVLFO: "VLFO", OpPrio: "PRIO", OpFine: "FINE",
	OpStatus: "STATUS", OpEnd: "END",
}

// OpName is the command's macro name.
func OpName(op byte) string {
	switch {
	case op >= 0xD0 && op <= 0xD3:
		return fmt.Sprintf("LOOP_START(%d)", op-0xD0)
	case op >= 0xD4 && op <= 0xD7:
		return fmt.Sprintf("LOOP(%d)", op-0xD4)
	case op >= 0xD8 && op <= 0xDB:
		return fmt.Sprintf("LOOP_EXIT(%d)", op-0xD8)
	}
	if n, ok := opNames[op]; ok {
		return n
	}
	return fmt.Sprintf("CMD_%02X", op)
}

// Event is one decoded event, with its encoding kept so that an unedited track encodes to the same bytes.
type Event struct {
	Off        int  // offset in the sequence
	Note       bool // a note (else a command)
	Vel, Key   int  // note: velocity 0-63, key 0-127
	Tie        bool // note: tied into the next note
	Len        int  // note: length in ticks
	LenBytes   int  // note: bytes of the length (1-3; 0: choose)
	Op         byte // command
	Args       []byte
	Delay      int // ticks to the next event
	DelayBytes int // bytes of the delay (0-3; -1: choose)
}

// Size is the event's size in bytes as encoded.
func (e *Event) Size() int { return len(e.Encode()) }

// Encode returns the event's bytes, its delay included.
func (e *Event) Encode() []byte {
	var b []byte
	if e.Note {
		k := e.Key & 0x7F
		if e.Tie {
			k |= 0x80
		}
		b = append(b, byte(0x80|e.Vel&0x3F), byte(k))
		b = append(b, encLen(e.Len, e.LenBytes)...)
	} else {
		b = append(b, e.Op)
		b = append(b, e.Args...)
		if !HasDelay(e.Op) {
			return b
		}
	}
	return append(b, encDelay(e.Delay, e.DelayBytes)...)
}

func encLen(d, n int) []byte {
	if n <= 0 {
		n = 1
		for d>>(7*n) != 0 {
			n++
		}
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = byte(d>>(7*(n-1-i))) & 0x7F
		if i < n-1 {
			out[i] |= 0x80
		}
	}
	return out
}

func encDelay(d, n int) []byte {
	if n < 0 {
		n = 0
		for d>>(7*n) != 0 {
			n++
		}
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = byte(d>>(7*(n-1-i))) & 0x7F
	}
	return out
}

// readDelay reads a delay (bytes below 0x80, up to 3) at p.
func readDelay(b []byte, p int) (val, n int) {
	for n < 3 && p+n < len(b) && b[p+n] < 0x80 {
		val = val<<7 | int(b[p+n])
		n++
	}
	return
}

// DecodeRegion decodes events from start until end, stopping early (ok=false) at bytes that do not decode.
// It returns the events and the offset where decoding stopped.
func DecodeRegion(b []byte, start, end int) (evs []Event, stop int, ok bool) {
	p := start
	for p < end {
		e := Event{Off: p}
		x := b[p]
		switch {
		case x < 0x80:
			return evs, p, false
		case x < 0xC0:
			if p+2 >= len(b) {
				return evs, p, false
			}
			e.Note = true
			e.Vel = int(x & 0x3F)
			e.Key = int(b[p+1] & 0x7F)
			e.Tie = b[p+1]&0x80 != 0
			q := p + 2
			n := 0
			for {
				if q >= len(b) || n == 4 {
					return evs, p, false
				}
				c := b[q]
				e.Len = e.Len<<7 | int(c&0x7F)
				q++
				n++
				if c&0x80 == 0 {
					break
				}
			}
			e.LenBytes = n
			d, dn := readDelay(b, q)
			e.Delay, e.DelayBytes = d, dn
			p = q + dn
		default:
			na := ArgCount(x)
			if p+1+na > len(b) {
				return evs, p, false
			}
			e.Op = x
			e.Args = append([]byte(nil), b[p+1:p+1+na]...)
			q := p + 1 + na
			if HasDelay(x) {
				d, dn := readDelay(b, q)
				e.Delay, e.DelayBytes = d, dn
				q += dn
			} else {
				e.DelayBytes = 0
			}
			p = q
		}
		if p > end {
			return evs, e.Off, false
		}
		evs = append(evs, e)
	}
	return evs, p, true
}

// Part is a piece of a sequence: its header, a decoded run of events, or raw bytes that do not decode as a
// track (so that every sequence encodes back to its exact bytes).
type Part struct {
	Off    int
	Header bool
	Lead   int // track start: the delay before the first event
	LeadN  int
	Events []Event
	Raw    []byte
}

// Decoded is a sequence split into its parts.
type Decoded struct {
	Seq   *Seq
	Parts []Part
}

// Decode splits a sequence into the header, its tracks (a leading delay, then events) and raw leftovers.
func Decode(s *Seq) *Decoded {
	d := &Decoded{Seq: s}
	b := s.Data
	if len(b) < 33 {
		d.Parts = append(d.Parts, Part{Off: 0, Raw: append([]byte(nil), b...)})
		return d
	}
	d.Parts = append(d.Parts, Part{Off: 0, Header: true, Raw: append([]byte(nil), b[:33]...)})
	// track starts, sorted; each track runs to the next start (or the end)
	starts := map[int]bool{}
	for _, o := range s.TrackOffsets() {
		if o >= 33 && o < len(b) {
			starts[o] = true
		}
	}
	var order []int
	for o := range starts {
		order = append(order, o)
	}
	sortInts(order)
	p := 33
	for i, o := range order {
		if o > p {
			d.Parts = append(d.Parts, Part{Off: p, Raw: append([]byte(nil), b[p:o]...)})
		}
		if o < p {
			continue // overlaps the previous part
		}
		end := len(b)
		if i+1 < len(order) {
			end = order[i+1]
		}
		lead, ln := readDelay(b, o)
		evs, stop, _ := DecodeRegion(b, o+ln, end)
		d.Parts = append(d.Parts, Part{Off: o, Lead: lead, LeadN: ln, Events: evs})
		if stop < end {
			d.Parts = append(d.Parts, Part{Off: stop, Raw: append([]byte(nil), b[stop:end]...)})
		}
		p = end
	}
	if p < len(b) {
		d.Parts = append(d.Parts, Part{Off: p, Raw: append([]byte(nil), b[p:]...)})
	}
	return d
}

// Encode returns the sequence's bytes.
func (d *Decoded) Encode() []byte {
	var out []byte
	for _, pt := range d.Parts {
		if pt.Raw != nil {
			out = append(out, pt.Raw...)
			continue
		}
		out = append(out, encDelay(pt.Lead, pt.LeadN)...)
		for i := range pt.Events {
			out = append(out, pt.Events[i].Encode()...)
		}
	}
	return out
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}
