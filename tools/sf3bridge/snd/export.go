// SPDX-License-Identifier: AGPL-3.0-only

package snd

import (
	"fmt"
	"math"
)

// MIDI export: each track of a sequence is played the way the driver plays it (voice_process_primary), with its
// loops and repeats unrolled, until it ends or comes back to where it has been (the song's loop), which the
// file marks with the markers "loopStart" and "loopEnd". One sequence tick is one MIDI tick at PPQ ticks per
// quarter note; the tempo turns the driver's ticks per frame into MIDI tempo.

const (
	PPQ       = 48      // MIDI ticks per quarter note; one tick = one sequence tick
	BendRange = 12      // semitones: BEND is -128..127 for -12..+12 (the driver: bend * 0xC00 >> 7)
	maxTicks  = 1 << 20 // stop a track that neither ends nor loops
	maxSteps  = 400000
)

// TempoToUS converts a TEMPO value (ticks per frame x 256) to MIDI microseconds per quarter note.
func TempoToUS(t int) int {
	if t <= 0 {
		return 500000
	}
	return int(math.Round(PPQ * 256 / (float64(t) * FrameRate) * 1e6))
}

// TempoToBPM is the tempo in beats per minute, a quarter note being PPQ ticks.
func TempoToBPM(t int) float64 { return 60e6 / float64(TempoToUS(t)) }

type delayPos struct {
	delay int
	next  int // offset of the event after the delay
}

// program indexes a decoded sequence for playing: the delay positions and the events by offset.
type program struct {
	d       *Decoded
	delayAt map[int]delayPos
	evAt    map[int]*Event
	origins [16]int
}

func newProgram(d *Decoded) *program {
	p := &program{d: d, delayAt: map[int]delayPos{}, evAt: map[int]*Event{}}
	for pi := range d.Parts {
		pt := &d.Parts[pi]
		if pt.Raw != nil {
			continue
		}
		first := pt.Off + pt.LeadN
		p.delayAt[pt.Off] = delayPos{pt.Lead, first}
		for i := range pt.Events {
			e := &pt.Events[i]
			p.evAt[e.Off] = e
			if e.Note || HasDelay(e.Op) {
				h := e.Off + headLen(e)
				p.delayAt[h] = delayPos{e.Delay, h + e.DelayBytes}
			}
		}
	}
	p.origins = d.Seq.TrackOffsets()
	return p
}

// headLen is the size of an event without its delay.
func headLen(e *Event) int {
	if e.Note {
		return 2 + len(encLen(e.Len, e.LenBytes))
	}
	return 1 + len(e.Args)
}

func s16(a, b byte) int { return int(int16(uint16(a)<<8 | uint16(b))) }

// TrackState is what a track sets as it plays (for the listing and the MIDI).
type playResult struct {
	endTick   int
	loopStart int // -1: no loop
	loopEnd   int
}

// playTrack plays one track into a MIDI track on channel ch; tempo changes go to the conductor.
// With stopAt > 0 the track plays to that tick, loops and all, without looking for its own loop.
func (p *program) playTrack(n int, mt, cond *MidiTrack, ch int, stopAt int) playResult {
	res := playResult{loopStart: -1}
	origin := p.origins[n]
	pos := origin
	t := 0
	latch := false
	var loopCursor [4]int
	var loopCount [4]int
	transpose := 0
	curKey, curEnd, curOn := 0, 0, false
	ended := false // the track stopped (END): its voice is free from here
	seen := map[string]int{}
	endNote := func(at int) {
		if curOn {
			if curEnd < at {
				at = curEnd
			}
			mt.NoteOff(at, ch, curKey)
			curOn = false
		}
	}
	// pitch bend range, so that BEND's +-12 semitones come out right
	mt.CC(0, ch, 101, 0)
	mt.CC(0, ch, 100, 0)
	mt.CC(0, ch, 6, BendRange)
	mt.CC(0, ch, 38, 0)
	limit := maxTicks
	if stopAt > 0 {
		limit = stopAt
	}
	jump := func(target int) bool { // false: the song's loop, stop
		if stopAt > 0 {
			pos = target
			return true
		}
		// the loop: a jump back to a position already played in the same state; it starts where that
		// position was first reached
		if at, ok := seen[fmt.Sprint(target, latch, loopCount)]; ok {
			res.loopStart, res.loopEnd = at, t
			return false
		}
		pos = target
		return true
	}
	for step := 0; step < maxSteps && t < limit; step++ {
		dp, ok := p.delayAt[pos]
		if !ok {
			break
		}
		if stopAt == 0 {
			if k := fmt.Sprint(pos, latch, loopCount); true {
				if _, ok := seen[k]; !ok {
					seen[k] = t
				}
			}
		}
		t += dp.delay
		if t >= limit {
			t = limit
			break
		}
		e := p.evAt[dp.next]
		if e == nil {
			break
		}
		after := e.Off + headLen(e) // the delay position after the event
		if e.Note {
			endNote(t)
			key := e.Key + transpose
			if key < 0 {
				key = 0
			}
			if key > 127 {
				key = 127
			}
			mt.NoteOn(t, ch, key, e.Vel*2+1)
			curKey, curOn = key, true
			curEnd = t + e.Len
			if e.Tie || e.Len == 0 {
				curEnd = math.MaxInt32
			}
			pos = after
			continue
		}
		a := e.Args
		switch op := e.Op; {
		case op == OpTempo:
			cond.Tempo(t, TempoToUS(int(a[0])<<8|int(a[1])))
			pos = after
		case op == OpBank:
			mt.CC(t, ch, 0, int(a[0]&0x0F))
			pos = after
		case op == OpProg:
			mt.Program(t, ch, int(a[0]&0x7F))
			pos = after
		case op == OpVol:
			mt.CC(t, ch, 7, int(a[0]&0x7F))
			pos = after
		case op == OpExpr:
			mt.CC(t, ch, 11, int(a[0]&0x7F))
			pos = after
		case op == OpPan:
			mt.CC(t, ch, 10, int(a[0]&0x7F))
			pos = after
		case op == OpBend:
			mt.Bend(t, ch, 8192+int(int8(a[0]))*64)
			pos = after
		case op == OpPorta:
			mt.CC(t, ch, 5, int(a[0]&0x7F))
			on := 0
			if a[0] != 0 {
				on = 127
			}
			mt.CC(t, ch, 65, on)
			pos = after
		case op == OpKey:
			transpose = int(int8(a[0]))
			pos = after
		case op == OpKeyAdd:
			transpose += int(int8(a[0]))
			pos = after
		case op == OpRepeat:
			if !latch {
				latch = true
				if !jump(origin) {
					goto done
				}
			} else {
				pos = after
			}
		case op == OpEndRepeat:
			if latch {
				ended = true
				goto done
			}
			pos = after
		case op == OpJump1:
			if !latch {
				latch = true
				if !jump(e.Off + 3 + s16(a[0], a[1])) {
					goto done
				}
			} else {
				pos = after
			}
		case op == OpJump2:
			if latch {
				if !jump(e.Off + 3 + int(int8(a[0]))*256 + int(int8(a[1]))) {
					goto done
				}
			} else {
				pos = after
			}
		case op == OpJump:
			if !jump(e.Off + 3 + s16(a[0], a[1])) {
				goto done
			}
		case op == OpJumpTrack:
			if !jump(p.origins[int(a[0])&15]) {
				goto done
			}
		case op >= 0xD0 && op <= 0xD3:
			loopCursor[op-0xD0] = after
			// kept as text: the game can jump back to a loop cursor from a later sequence on this voice (a LOOP
			// without its LOOP_START), so the import sets it at the same musical moment
			mt.Text(t, 1, fmt.Sprintf("sf3 LOOP_START(%d)", op-0xD0))
			pos = after
		case op >= 0xD4 && op <= 0xD7:
			i := op - 0xD4
			// a count of 0 loops forever: the song's loop, which jump() finds when it comes round again
			if loopCount[i] == 0 {
				loopCount[i] = int(a[0])
				if !jump(loopCursor[i]) {
					goto done
				}
			} else {
				loopCount[i]--
				if loopCount[i] == 0 {
					pos = after
				} else if !jump(loopCursor[i]) {
					goto done
				}
			}
		case op >= 0xD8 && op <= 0xDB:
			i := op - 0xD8
			if loopCount[i] == 1 {
				loopCount[i] = 0
				pos = e.Off + 3 + int(a[0])<<8 + int(a[1])
			} else {
				pos = after
			}
		case op == OpStatus:
			mt.Text(t, 1, fmt.Sprintf("sf3 STATUS %d %d", a[0], a[1]))
			pos = after
		case op == OpEnd:
			ended = true
			goto done
		default:
			if len(a) > 0 && op != OpNop {
				mt.Text(t, 1, fmt.Sprintf("sf3 %s %d", OpName(op), int8(a[0])))
			}
			pos = after
		}
	}
done:
	if stopAt > 0 && curOn && curEnd > stopAt {
		curEnd = stopAt
	}
	endNote(t)
	if ended {
		mt.Text(t, 1, "sf3 END") // the import ends the track here, freeing its voice as the game does
	}
	res.endTick = t
	return res
}

// ExportMIDI turns a music sequence into a format 1 MIDI file: a conductor track (name, tempo, loop markers),
// then one track per sequence track, on the MIDI channel of the same number.
func ExportMIDI(q *Seq, name string) (*MidiFile, error) {
	if !q.IsMusic() {
		return nil, fmt.Errorf("sound code %d is a sound effect, not music", q.Code)
	}
	d := Decode(q)
	p := newProgram(d)
	m := &MidiFile{Format: 1, PPQ: PPQ}
	cond := &MidiTrack{}
	cond.Text(0, 3, name)
	m.Tracks = append(m.Tracks, cond)
	// pass 1: each track's own loop (period); the song loops from the latest loop start, for the least common
	// multiple of the periods (a 2-bar drum loop inside a 32-bar song)
	loopStart, period := -1, 0
	var ownStart [16]int
	for n := 0; n < 16; n++ {
		ownStart[n] = -1
		if p.origins[n] == 0 {
			continue
		}
		r := p.playTrack(n, &MidiTrack{}, &MidiTrack{}, n, 0)
		if r.loopStart < 0 || r.loopEnd <= r.loopStart {
			continue
		}
		ownStart[n] = r.loopStart
		if r.loopStart > loopStart {
			loopStart = r.loopStart
		}
		per := r.loopEnd - r.loopStart
		if period == 0 {
			period = per
		} else if l := lcm(period, per); l <= 64*period {
			period = l
		}
	}
	loopEnd := 0
	stop := 0
	if loopStart >= 0 {
		loopEnd = loopStart + period
		stop = loopEnd
	}
	// pass 2: every track to the song's loop end (or to its own end, for a song that does not loop)
	for n := 0; n < 16; n++ {
		if p.origins[n] == 0 {
			continue
		}
		mt := &MidiTrack{}
		mt.Text(0, 3, fmt.Sprintf("SF3 track %d %s", n, p.firstInstrument(n)))
		if ownStart[n] >= 0 {
			// the track's own loop start (a track may loop a few ticks from the song's loopStart marker; the
			// loop's length is the song's)
			mt.Text(ownStart[n], 1, "sf3 LOOP_START")
		}
		p.playTrack(n, mt, cond, n, stop)
		m.Tracks = append(m.Tracks, mt)
	}
	if loopStart >= 0 {
		cond.Text(loopStart, 6, "loopStart")
		cond.Text(loopEnd, 6, "loopEnd")
	} else {
		end := 0
		for _, t := range m.Tracks {
			for _, e := range t.Events {
				if e.Tick > end {
					end = e.Tick
				}
			}
		}
		cond.Text(end, 6, "end") // the song plays once: the import stops here instead of looping
	}
	return m, nil
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func lcm(a, b int) int { return a / gcd(a, b) * b }

// PlayUntil plays every track of a sequence to a tick, loops and all, into a MIDI file (for comparing two
// sequences over several passes of their loops).
func PlayUntil(q *Seq, stop int) *MidiFile {
	d := Decode(q)
	p := newProgram(d)
	m := &MidiFile{Format: 1, PPQ: PPQ}
	cond := &MidiTrack{}
	m.Tracks = append(m.Tracks, cond)
	for n := 0; n < 16; n++ {
		mt := &MidiTrack{}
		if p.origins[n] != 0 {
			p.playTrack(n, mt, cond, n, stop)
		}
		m.Tracks = append(m.Tracks, mt)
	}
	return m
}

// firstInstrument names the instrument a track starts with ("b0p57"), for the MIDI track name.
func (p *program) firstInstrument(n int) string {
	bank := 0
	for _, pt := range p.d.Parts {
		if pt.Raw != nil || pt.Off != p.origins[n] {
			continue
		}
		for _, e := range pt.Events {
			if e.Note {
				break
			}
			if e.Op == OpBank {
				bank = int(e.Args[0] & 0x0F)
			}
			if e.Op == OpProg {
				return fmt.Sprintf("b%dp%d", bank, e.Args[0]&0x7F)
			}
		}
	}
	return ""
}
