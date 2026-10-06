// SPDX-License-Identifier: AGPL-3.0-only

package snd

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Import: MIDI to a music sequence.
//
// A song is made of parts: MIDI tracks (of one multi-track file, or one file per part, as Ableton exports a
// clip). Each part plays one instrument (bank, program). The sound driver plays one note at a time per track,
// so a part's notes are spread over as many tracks as it has notes at once; a song has 16 tracks in all.
// The loop, as Capcom's songs have it: LOOP_START(0) where it starts, LOOP(0) 0 (for ever) where it ends.

// PartSpec describes one part (manifest "parts" entry).
type PartSpec struct {
	Midi      string `json:"midi"`              // MIDI file (relative to the manifest)
	Track     *int   `json:"track,omitempty"`   // only this track of the file (0 = first track); default: all
	Channel   *int   `json:"channel,omitempty"` // only this MIDI channel (1-16)
	Bank      *int   `json:"bank,omitempty"`
	Program   *int   `json:"program,omitempty"`
	Volume    *int   `json:"volume,omitempty"` // 0-127
	Pan       *int   `json:"pan,omitempty"`    // 0 left, 64 centre, 127 right
	Transpose int    `json:"transpose,omitempty"`
	Name      string `json:"name,omitempty"`
}

// SongSpec is a song manifest (JSON).
type SongSpec struct {
	Parts          []PartSpec `json:"parts"`
	Tempo          float64    `json:"tempo,omitempty"`        // BPM; default: the MIDI tempo (120 if none)
	LoopStartBar   float64    `json:"loopStartBar,omitempty"` // bar numbers from 1 (4/4 unless timeSig)
	LoopEndBar     float64    `json:"loopEndBar,omitempty"`
	NoLoop         bool       `json:"noLoop,omitempty"`
	BeatsPerBar    int        `json:"beatsPerBar,omitempty"` // default 4
	BendRange      float64    `json:"bendRange,omitempty"`   // semitones; default: RPN in the file, else 2
	DefaultBank    int        `json:"defaultBank,omitempty"`
	DefaultProgram int        `json:"defaultProgram,omitempty"`
}

// ReadSongSpec reads a manifest.
func ReadSongSpec(path string) (*SongSpec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s SongSpec
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	dir := filepath.Dir(path)
	for i := range s.Parts {
		if !filepath.IsAbs(s.Parts[i].Midi) {
			s.Parts[i].Midi = filepath.Join(dir, s.Parts[i].Midi)
		}
	}
	return &s, nil
}

type note struct {
	start, end, key, vel int
	order                int // file order among events of its tick
}

type ctl struct {
	t    int
	op   byte
	args []byte
	prio int // order at one tick: settings first
}

// part is one instrument's notes and controls, in sequence ticks.
type part struct {
	ch    int // MIDI channel
	name  string
	notes []note
	ctls  []ctl
}

var bankProgName = regexp.MustCompile(`(?i)\bb(\d+)\s*p(\d+)\b`)

// collectParts reads a spec's MIDI into parts, and the tempo map and markers.
func collectParts(spec *SongSpec) (parts []*part, tempos []ctl, markers map[string]int, songEnd int, warn []string, err error) {
	markers = map[string]int{}
	files := map[string]*MidiFile{}
	for pi, ps := range spec.Parts {
		m := files[ps.Midi]
		if m == nil {
			if m, err = ReadMidi(ps.Midi); err != nil {
				return
			}
			files[ps.Midi] = m
		}
		scale := func(t int) int { return int(math.Round(float64(t) * PPQ / float64(m.PPQ))) }
		// tempo, markers and time signatures from every track (the conductor), once per file
		if pi == 0 || spec.Parts[pi-1].Midi != ps.Midi {
			for _, tr := range m.Tracks {
				for _, e := range tr.Events {
					if e.Status != 0xFF || len(e.Data) < 1 {
						continue
					}
					switch e.Data[0] {
					case 0x51:
						if len(e.Data) >= 4 && pi == 0 {
							us := int(e.Data[1])<<16 | int(e.Data[2])<<8 | int(e.Data[3])
							tempos = append(tempos, ctl{t: scale(e.Tick), op: OpTempo, args: tempoArgs(us)})
						}
					case 6, 1:
						txt := strings.ToLower(strings.TrimSpace(string(e.Data[1:])))
						if txt == "loopstart" || txt == "loop start" || txt == "loop" {
							markers["loopStart"] = scale(e.Tick)
						}
						if txt == "loopend" || txt == "loop end" {
							markers["loopEnd"] = scale(e.Tick)
						}
						if txt == "end" {
							markers["end"] = scale(e.Tick)
						}
					}
				}
			}
		}
		for ti, tr := range m.Tracks {
			if ps.Track != nil && *ps.Track != ti {
				continue
			}
			// Filtered channel manifests must not copy a track's sf3 text commands
			// onto every other channel. Exported game tracks have one owning channel.
			textChannel := 0
			for _, e := range tr.Events {
				if e.Status >= 0x80 && e.Status < 0xF0 {
					textChannel = int(e.Status & 0x0F)
					break
				}
			}
			// split the track by channel: each channel with notes is a part
			byCh := map[int]*part{}
			trackName := ""
			for _, e := range tr.Events {
				if e.Status == 0xFF && len(e.Data) > 0 && e.Data[0] == 3 {
					trackName = string(e.Data[1:])
				}
			}
			seqNo := 0 // file order of the controls: kept among events of one tick
			next := func() int { seqNo++; return seqNo }
			bendRange := map[int]float64{}
			rpn := map[int][2]int{}
			on := map[[2]int][]note{} // (channel, key) -> notes started
			bank := map[int]int{}
			porta := map[int]int{}
			get := func(ch int) *part {
				if byCh[ch] == nil {
					byCh[ch] = &part{ch: ch, name: trackName}
				}
				return byCh[ch]
			}
			for _, e := range tr.Events {
				if e.Status == 0xFF && len(e.Data) > 1 && e.Data[0] == 1 {
					if ps.Channel != nil && *ps.Channel-1 != textChannel {
						continue
					}
					// "sf3 NAME args": a command the export wrote as text (STATUS, LFOs, tuning); into the
					// track's first channel part
					if c, ok := textCommand(string(e.Data[1:])); ok {
						c.t = scale(e.Tick)
						if c.op != OpEnd {
							c.prio = next()
						}
						ch := 0
						for k := range byCh {
							ch = k
						}
						if ps.Channel != nil {
							ch = *ps.Channel - 1
						}
						get(ch).ctls = append(get(ch).ctls, c)
					}
					continue
				}
				if e.Status >= 0xF0 {
					continue
				}
				ch := int(e.Status & 0x0F)
				if ps.Channel != nil && *ps.Channel-1 != ch {
					continue
				}
				t := scale(e.Tick)
				if t > songEnd {
					songEnd = t
				}
				switch e.Status & 0xF0 {
				case 0x90, 0x80:
					key := int(e.Data[0])
					k := [2]int{ch, key}
					if e.Status&0xF0 == 0x90 && e.Data[1] > 0 {
						on[k] = append(on[k], note{start: t, key: key, vel: int(e.Data[1]), order: next()})
					} else if len(on[k]) > 0 {
						n := on[k][0]
						on[k] = on[k][1:]
						n.end = t
						if n.end > n.start {
							get(ch).notes = append(get(ch).notes, n)
						}
					}
				case 0xB0:
					cc, v := int(e.Data[0]), int(e.Data[1])
					p := get(ch)
					switch cc {
					case 0:
						bank[ch] = v
						p.ctls = append(p.ctls, ctl{t, OpBank, []byte{byte(v & 0x0F)}, next()})
					case 7:
						p.ctls = append(p.ctls, ctl{t, OpVol, []byte{byte(v)}, next()})
					case 10:
						p.ctls = append(p.ctls, ctl{t, OpPan, []byte{byte(v)}, next()})
					case 11:
						p.ctls = append(p.ctls, ctl{t, OpExpr, []byte{byte(v)}, next()})
					case 5:
						porta[ch] = v
					case 65:
						pv := 0
						if v >= 64 {
							pv = porta[ch]
							if pv == 0 {
								pv = 1
							}
						}
						p.ctls = append(p.ctls, ctl{t, OpPorta, []byte{byte(pv)}, next()})
					case 101, 100:
						r := rpn[ch]
						r[cc-100] = v
						rpn[ch] = r
					case 6:
						if rpn[ch] == [2]int{0, 0} {
							bendRange[ch] = float64(v)
						}
					}
				case 0xC0:
					p := get(ch)
					p.ctls = append(p.ctls, ctl{t, OpProg, []byte{e.Data[0] & 0x7F}, next()})
				case 0xE0:
					v := int(e.Data[0]) | int(e.Data[1])<<7
					rng := spec.BendRange
					if rng == 0 {
						if r, ok := bendRange[ch]; ok {
							rng = r
						} else {
							rng = 2
						}
					}
					semis := float64(v-8192) / 8192 * rng
					b := int(math.Round(semis * 128 / BendRange))
					if b < -128 {
						b = -128
					}
					if b > 127 {
						b = 127
					}
					get(ch).ctls = append(get(ch).ctls, ctl{t, OpBend, []byte{byte(int8(b))}, next()})
				}
			}
			for k, ns := range on {
				if len(ns) > 0 {
					warn = append(warn, fmt.Sprintf("%s track %d: %d notes of key %d never end; dropped", filepath.Base(ps.Midi), ti, len(ns), k[1]))
				}
			}
			var chans []int
			for ch := range byCh {
				chans = append(chans, ch)
			}
			sort.Ints(chans)
			for _, ch := range chans {
				p := byCh[ch]
				if len(p.notes) == 0 && !hasCommands(p) {
					continue
				}
				if ps.Name != "" {
					p.name = ps.Name
				}
				// the part's instrument: the spec, else the file's program changes, else a "b3p12" in the track
				// name or the file name (Ableton names an exported clip's file after the clip)
				var head []ctl
				bnk, prg := -1, -1
				hasProg := false
				for _, c := range p.ctls {
					if c.op == OpProg {
						hasProg = true
					}
				}
				if !hasProg {
					for _, nm := range []string{p.name, strings.TrimSuffix(filepath.Base(ps.Midi), filepath.Ext(ps.Midi))} {
						if m := bankProgName.FindStringSubmatch(nm); m != nil && bnk < 0 {
							bnk, _ = strconv.Atoi(m[1])
							prg, _ = strconv.Atoi(m[2])
						}
					}
				}
				if ps.Bank != nil {
					bnk = *ps.Bank
				}
				if ps.Program != nil {
					prg = *ps.Program
				}
				if len(p.notes) == 0 {
					parts = append(parts, p) // commands only (STATUS): no instrument, no mix
					continue
				}
				if bnk >= 0 || prg >= 0 || !hasProg {
					if bnk < 0 {
						bnk = spec.DefaultBank
					}
					if prg < 0 {
						prg = spec.DefaultProgram
					}
					head = append(head, ctl{0, OpBank, []byte{byte(bnk & 0x0F)}, -2}, ctl{0, OpProg, []byte{byte(prg & 0x7F)}, -2})
					// the spec's instrument wins over the file's program changes
					var keep []ctl
					for _, c := range p.ctls {
						if c.op != OpProg && c.op != OpBank {
							keep = append(keep, c)
						}
					}
					p.ctls = keep
				}
				vol, pan := DefaultVolume, 64
				if ps.Volume != nil {
					vol = *ps.Volume
				}
				if ps.Pan != nil {
					pan = *ps.Pan
				}
				// defaults, for the controls the part does not set itself
				// the driver starts a song's voices at volume 0, expression 64, pan centre: a part that never sets
				// its volume gets DefaultVolume (a typical value of the game's songs); the spec's mix wins
				if ps.Volume != nil || !usesOp(p, OpVol) {
					head = append(head, ctl{0, OpVol, []byte{byte(vol)}, -1})
				}
				if ps.Pan != nil {
					head = append(head, ctl{0, OpPan, []byte{byte(pan)}, -1})
				}
				if ps.Volume != nil || ps.Pan != nil { // the spec's mix wins over the file's
					var keep []ctl
					for _, c := range p.ctls {
						if !(ps.Volume != nil && c.op == OpVol) && !(ps.Pan != nil && c.op == OpPan) {
							keep = append(keep, c)
						}
					}
					p.ctls = keep
				}
				p.ctls = append(head, p.ctls...)
				if ps.Transpose != 0 {
					for i := range p.notes {
						p.notes[i].key += ps.Transpose
					}
				}
				parts = append(parts, p)
			}
		}
	}
	return
}

// tempoArgs turns MIDI microseconds per quarter note into TEMPO's two bytes.
func tempoArgs(us int) []byte {
	t := int(math.Round(PPQ * 256 / (float64(us) / 1e6 * FrameRate)))
	if t < 1 {
		t = 1
	}
	if t > 0xFFFF {
		t = 0xFFFF
	}
	return []byte{byte(t >> 8), byte(t)}
}

// DefaultVolume is the volume of a part that sets none.
const DefaultVolume = 80

// usesOp tells whether a part has a command.
func usesOp(p *part, op byte) bool {
	for _, c := range p.ctls {
		if c.op == op {
			return true
		}
	}
	return false
}

// hasCommands tells whether a part without notes still does something (the game's STATUS counters).
func hasCommands(p *part) bool {
	for _, c := range p.ctls {
		if c.op == OpStatus {
			return true
		}
	}
	return false
}

// voices spreads a part's notes over tracks, one note at a time on each (a part without notes: one track).
func voices(p *part) [][]note {
	if len(p.notes) == 0 {
		return [][]note{nil}
	}
	ns := append([]note(nil), p.notes...)
	sort.SliceStable(ns, func(i, j int) bool {
		if ns[i].start != ns[j].start {
			return ns[i].start < ns[j].start
		}
		return ns[i].key > ns[j].key
	})
	var lanes [][]note
	for _, n := range ns {
		placed := false
		for i := range lanes {
			if lanes[i][len(lanes[i])-1].end <= n.start {
				lanes[i] = append(lanes[i], n)
				placed = true
				break
			}
		}
		if !placed {
			lanes = append(lanes, []note{n})
		}
	}
	return lanes
}

type item struct {
	t     int
	order int
	ev    Event
}

// encodeTrack turns a track's controls and notes into bytes: a leading delay of 0, then events, then END
// (at endAt, when endAt >= 0: the last event's delay reaches it, as the game's songs end).
func encodeTrack(ctls []ctl, notes []note, loopStart, loopEnd, endAt int) []byte {
	var its []item
	for _, c := range ctls {
		if loopEnd >= 0 && c.t >= loopEnd {
			continue
		}
		its = append(its, item{c.t, c.prio, Event{Op: c.op, Args: c.args, DelayBytes: -1}})
	}
	for _, n := range notes {
		if loopEnd >= 0 && n.start >= loopEnd {
			continue
		}
		end := n.end
		if loopEnd >= 0 && end > loopEnd {
			end = loopEnd
		}
		key := n.key
		if key < 0 {
			key = 0
		}
		if key > 127 {
			key = 127
		}
		vel := int(math.Round(float64(n.vel) * 63 / 127)) // the export writes vel*2+1: this undoes it exactly
		its = append(its, item{n.start, n.order, Event{Note: true, Key: key, Vel: vel, Len: end - n.start, DelayBytes: -1}})
	}
	if loopStart >= 0 {
		// the song's loop: when a LOOP_START(n) of the track's own is at the loop start, that is the loop (as
		// Capcom's songs do it: LOOP_START(n) ... LOOP(n) 0); otherwise a loop index the track leaves free.
		// Loop indexes are voice state a later sequence can use, so the choice follows the original.
		li, have := byte(0), false
		used := map[byte]bool{}
		for _, c := range ctls {
			if c.op >= OpLoopStart && c.op <= OpLoopStart+3 {
				used[c.op-OpLoopStart] = true
				if c.t == loopStart && !have {
					li, have = c.op-OpLoopStart, true
				}
			}
		}
		if !have {
			for li < 3 && used[li] {
				li++
			}
			its = append(its, item{loopStart, -100, Event{Op: OpLoopStart + li, DelayBytes: -1}})
		}
		its = append(its, item{loopEnd, math.MaxInt32, Event{Op: OpLoop + li, Args: []byte{0}, DelayBytes: -1}})
	}
	sort.SliceStable(its, func(i, j int) bool {
		if its[i].t != its[j].t {
			return its[i].t < its[j].t
		}
		return its[i].order < its[j].order
	})
	out := []byte{} // the leading delay: none (the first event is at tick 0)
	if len(its) > 0 && its[0].t > 0 {
		out = encDelay(its[0].t, -1)
	}
	for i := range its {
		e := its[i].ev
		if i+1 < len(its) {
			e.Delay = its[i+1].t - its[i].t
		} else if endAt > its[i].t && HasDelay(e.Op) {
			e.Delay = endAt - its[i].t
		}
		out = append(out, e.Encode()...)
	}
	if len(its) == 0 && endAt > 0 {
		out = encDelay(endAt, -1)
	}
	return append(out, OpEnd)
}

// opLoopMark marks a track's own loop start in its controls (from the export's "sf3 LOOP_START"); it is not
// encoded as a command.
const opLoopMark = 0x100 - 1 - 0x40 // an unused command number (0xBF is a note byte, never a command)

// trackLoopStart is a track's own loop start, if the file marks one.
func trackLoopStart(cs []ctl) (int, bool) {
	for _, c := range cs {
		if c.op == opLoopMark {
			return c.t, true
		}
	}
	return 0, false
}

// trackEnd is the tick of a track's own END, if it has one.
func trackEnd(cs []ctl) (int, bool) {
	for _, c := range cs {
		if c.op == OpEnd {
			return c.t, true
		}
	}
	return 0, false
}

// encodeTrackEnd is encodeTrack for a song that plays once: the END comes at the song's end.
func encodeTrackEnd(ctls []ctl, notes []note, end int) []byte {
	return encodeTrack(ctls, notes, -1, -1, end)
}

// Imported is a converted song.
type Imported struct {
	Data      []byte
	Tracks    int
	LoopStart int
	LoopEnd   int
	Tempo     float64
	Warnings  []string
}

// ImportSong converts a song spec to a music sequence.
func ImportSong(spec *SongSpec) (*Imported, error) {
	parts, tempos, markers, songEnd, warn, err := collectParts(spec)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("no notes found")
	}
	if spec.Tempo > 0 {
		tempos = []ctl{{0, OpTempo, tempoArgs(int(math.Round(60e6 / spec.Tempo))), -3}}
	}
	if len(tempos) == 0 || tempos[0].t > 0 {
		tempos = append([]ctl{{0, OpTempo, tempoArgs(500000), -3}}, tempos...)
	}
	for i := range tempos {
		tempos[i].prio = -3
	}
	bar := PPQ * 4
	if spec.BeatsPerBar > 0 {
		bar = PPQ * spec.BeatsPerBar
	}
	loopStart, loopEnd := -1, -1
	_, hasLoop := markers["loopStart"]
	if v, ok := markers["end"]; ok && !hasLoop && spec.LoopStartBar == 0 && spec.LoopEndBar == 0 {
		spec.NoLoop = true // an exported song that plays once
		if v > songEnd {
			songEnd = v
		}
	}
	if !spec.NoLoop {
		loopStart, loopEnd = 0, (songEnd+bar-1)/bar*bar
		if v, ok := markers["loopStart"]; ok {
			loopStart = v
		}
		if v, ok := markers["loopEnd"]; ok {
			loopEnd = v
		}
		if spec.LoopStartBar > 0 {
			loopStart = int(math.Round((spec.LoopStartBar - 1) * float64(bar)))
		}
		if spec.LoopEndBar > 0 {
			loopEnd = int(math.Round((spec.LoopEndBar - 1) * float64(bar)))
		}
		if loopEnd <= loopStart {
			return nil, fmt.Errorf("the loop ends (tick %d) before it starts (tick %d)", loopEnd, loopStart)
		}
	}
	type track struct {
		ctls  []ctl
		notes []note
		slot  int
	}
	var tracks []track
	// a part on a channel of its own and one voice keeps its channel's track number (the game's sound
	// effects share voices with the music by number); otherwise tracks are numbered in order
	byChannel := true
	used := map[int]bool{}
	for _, p := range parts {
		if len(voices(p)) != 1 || used[p.ch] {
			byChannel = false
		}
		used[p.ch] = true
	}
	for _, p := range parts {
		for _, l := range voices(p) {
			slot := len(tracks)
			if byChannel {
				slot = p.ch
			}
			tracks = append(tracks, track{append([]ctl(nil), p.ctls...), l, slot})
		}
	}
	if len(tracks) > 16 {
		var desc []string
		for _, p := range parts {
			desc = append(desc, fmt.Sprintf("%q: %d", p.name, len(voices(p))))
		}
		return nil, fmt.Errorf("the song needs %d tracks (notes at once, per part: %s); the sound driver has 16",
			len(tracks), strings.Join(desc, ", "))
	}
	sort.SliceStable(tracks, func(i, j int) bool { return tracks[i].slot < tracks[j].slot })
	tracks[0].ctls = append(append([]ctl(nil), tempos...), tracks[0].ctls...)
	header := make([]byte, 33)
	var body []byte
	for _, tr := range tracks {
		i := tr.slot
		off := 33 + len(body)
		header[1+2*i], header[2+2*i] = byte(off>>8), byte(off)
		// a track with an END of its own (an exported song's track that stops early) ends there, unlooped
		if end, ok := trackEnd(tr.ctls); ok {
			var cs []ctl
			for _, c := range tr.ctls {
				if c.op != OpEnd && c.op != opLoopMark {
					cs = append(cs, c)
				}
			}
			body = append(body, encodeTrackEnd(cs, tr.notes, end)...)
			continue
		}
		if loopStart >= 0 {
			// the loop: the song's length, from the track's own start when the file marks one
			ls, le := loopStart, loopEnd
			if own, ok := trackLoopStart(tr.ctls); ok && own < loopEnd {
				ls, le = own, own+(loopEnd-loopStart)
			}
			var cs []ctl
			for _, c := range tr.ctls {
				if c.op != opLoopMark {
					cs = append(cs, c)
				}
			}
			body = append(body, encodeTrack(cs, tr.notes, ls, le, -1)...)
		} else {
			body = append(body, encodeTrackEnd(tr.ctls, tr.notes, songEnd)...)
		}
	}
	data := append(header, body...)
	if len(data) > 0xFFFF {
		return nil, fmt.Errorf("the song is %d bytes; a sequence's track offsets reach 65535", len(data))
	}
	bpm := 60e6 / float64(TempoToUS(int(tempos[0].args[0])<<8|int(tempos[0].args[1])))
	return &Imported{Data: data, Tracks: len(tracks), LoopStart: loopStart, LoopEnd: loopEnd, Tempo: bpm, Warnings: warn}, nil
}

// Free regions for new sequences: after the sound section (zero fill up to the next section), and after the
// graphics in the second program SIMM (zero fill to the end of the ROM, unless a hack uses it). Songs imported
// before are found as the last non-zero byte of a region.
var freeRegions = [][2]uint32{{SndEnd, FreeEnd}, {0x06E60000, 0x06FFFFF0}}

// regionFree returns where the free space of a region starts, or ok=false when the region is not ours (not
// zero fill from some point to its end).
func (r *ROM) regionFree(lo, hi uint32) (start uint32, ok bool) {
	a := hi
	for a > lo && r.Prog[a-1-ProgBase] == 0 {
		a--
	}
	if lo == SndEnd {
		return a, true
	}
	// the second region is ours only if everything before its free space was put there by an import:
	// all of it zero at first. A region starting with non-zero data (EX's code) is left alone.
	if a > lo && r.Prog[lo-ProgBase] != 0 && !r.importedAt(lo) {
		return 0, false
	}
	return a, true
}

// importedAt tells whether a sound code's sequence starts at addr (a song imported there).
func (r *ROM) importedAt(addr uint32) bool {
	count := int(r.U32(SeqTableAddr) >> 16)
	for c := 0; c < count; c++ {
		if o := r.U32(uint32(SeqTableAddr + 4 + c*4)); o != 0 && SeqTableAddr+o == addr {
			return true
		}
	}
	return false
}

// usedEnd is the end of the data after the sound section (songs imported there before).
func (r *ROM) usedEnd() uint32 {
	a, _ := r.regionFree(SndEnd, FreeEnd)
	return a
}

// PutSequence stores a sequence in the free space after the sound section and points a sound code at it.
// It returns the address it used.
func (s *Sound) PutSequence(code int, data []byte) (uint32, error) {
	count := int(s.SeqHead >> 16)
	if code < 0 || code >= count {
		return 0, fmt.Errorf("sound code %d: there are %d", code, count)
	}
	var addr uint32
	room := 0
	found := false
	for _, rg := range freeRegions {
		start, ok := s.ROM.regionFree(rg[0], rg[1])
		if !ok {
			continue
		}
		a := (start + 4) &^ 3
		if a < rg[0] {
			a = rg[0]
		}
		if a+uint32(len(data))+1 <= rg[1] {
			addr, found = a, true
			break
		}
		room += int(rg[1]) - int(a)
	}
	if !found {
		return 0, fmt.Errorf("no room: %d bytes free for new songs, the song is %d", room, len(data))
	}
	s.ROM.Put(addr, data)
	s.ROM.PutU32(uint32(SeqTableAddr+4+code*4), addr-SeqTableAddr)
	return addr, nil
}

// textCommand reads a command the export wrote as a text event: "sf3 STATUS i v" or "sf3 NAME v".
func textCommand(txt string) (ctl, bool) {
	f := strings.Fields(txt)
	if len(f) < 2 || f[0] != "sf3" || (len(f) < 3 && f[1] != "END" && !strings.HasPrefix(f[1], "LOOP_START")) {
		return ctl{}, false
	}
	num := func(s string) (byte, bool) {
		v, err := strconv.Atoi(s)
		return byte(v), err == nil
	}
	if f[1] == "END" {
		return ctl{op: OpEnd, prio: 40}, true
	}
	if len(f[1]) == 13 && strings.HasPrefix(f[1], "LOOP_START(") && f[1][12] == ')' && f[1][11] >= '0' && f[1][11] <= '3' {
		return ctl{op: OpLoopStart + f[1][11] - '0', prio: 1}, true
	}
	if f[1] == "LOOP_START" {
		return ctl{op: opLoopMark, prio: -100}, true // the track's own loop start (not a command)
	}
	if f[1] == "STATUS" && len(f) == 4 {
		i, ok1 := num(f[2])
		v, ok2 := num(f[3])
		return ctl{op: OpStatus, args: []byte{i, v}, prio: 2}, ok1 && ok2
	}
	for op := 0xC0; op <= 0xFE; op++ {
		if OpName(byte(op)) == f[1] && ArgCount(byte(op)) == 1 {
			v, ok := num(f[2])
			return ctl{op: byte(op), args: []byte{v}, prio: 1}, ok
		}
	}
	return ctl{}, false
}
