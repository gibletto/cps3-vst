// SPDX-License-Identifier: AGPL-3.0-only

package snd

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sort"
)

// A minimal Standard MIDI File reader and writer (format 0 and 1).

// MidiEvent is one event at an absolute tick.
type MidiEvent struct {
	Tick   int
	Status byte   // 0x80-0xEF channel message; 0xFF meta; 0xF0 sysex
	Data   []byte // channel message data bytes; meta: type then data; sysex: data
	order  int    // write order among events of the same tick
}

// MidiTrack is a track of the file.
type MidiTrack struct {
	Events []MidiEvent
}

// MidiFile is a parsed or built SMF.
type MidiFile struct {
	Format int
	PPQ    int
	Tracks []*MidiTrack
}

func (t *MidiTrack) add(tick int, status byte, data ...byte) {
	t.Events = append(t.Events, MidiEvent{Tick: tick, Status: status, Data: data, order: len(t.Events)})
}

// Meta adds a meta event.
func (t *MidiTrack) Meta(tick int, typ byte, data []byte) {
	t.add(tick, 0xFF, append([]byte{typ}, data...)...)
}

// Text adds a text meta event (type 1: text, 3: track name, 6: marker).
func (t *MidiTrack) Text(tick int, typ byte, s string) { t.Meta(tick, typ, []byte(s)) }

// Tempo adds a set-tempo meta event (microseconds per quarter note).
func (t *MidiTrack) Tempo(tick int, usPerQuarter int) {
	t.Meta(tick, 0x51, []byte{byte(usPerQuarter >> 16), byte(usPerQuarter >> 8), byte(usPerQuarter)})
}

func (t *MidiTrack) NoteOn(tick, ch, key, vel int) { t.add(tick, byte(0x90|ch), byte(key), byte(vel)) }
func (t *MidiTrack) NoteOff(tick, ch, key int)     { t.add(tick, byte(0x80|ch), byte(key), 0) }
func (t *MidiTrack) CC(tick, ch, cc, v int)        { t.add(tick, byte(0xB0|ch), byte(cc), byte(v)) }
func (t *MidiTrack) Program(tick, ch, p int)       { t.add(tick, byte(0xC0|ch), byte(p)) }
func (t *MidiTrack) Bend(tick, ch, v int) {
	if v < 0 {
		v = 0
	}
	if v > 16383 {
		v = 16383
	}
	t.add(tick, byte(0xE0|ch), byte(v&0x7F), byte(v>>7))
}

func vlq(v int) []byte {
	out := []byte{byte(v & 0x7F)}
	for v >>= 7; v > 0; v >>= 7 {
		out = append([]byte{byte(v&0x7F) | 0x80}, out...)
	}
	return out
}

// sortEvents orders a track by tick; at one tick note-offs come first, then the rest in the order added (the
// order the driver ran them).
func (t *MidiTrack) sortEvents() {
	rank := func(e MidiEvent) int {
		if e.Status&0xF0 == 0x80 || (e.Status&0xF0 == 0x90 && len(e.Data) > 1 && e.Data[1] == 0) {
			return 0
		}
		return 1
	}
	sort.SliceStable(t.Events, func(i, j int) bool {
		a, b := t.Events[i], t.Events[j]
		if a.Tick != b.Tick {
			return a.Tick < b.Tick
		}
		if rank(a) != rank(b) {
			return rank(a) < rank(b)
		}
		return a.order < b.order
	})
}

// Write writes the file.
func (m *MidiFile) Write(path string) error {
	var buf bytes.Buffer
	buf.WriteString("MThd")
	binary.Write(&buf, binary.BigEndian, uint32(6))
	binary.Write(&buf, binary.BigEndian, uint16(m.Format))
	binary.Write(&buf, binary.BigEndian, uint16(len(m.Tracks)))
	binary.Write(&buf, binary.BigEndian, uint16(m.PPQ))
	for _, t := range m.Tracks {
		t.sortEvents()
		var tb bytes.Buffer
		last := 0
		for _, e := range t.Events {
			tb.Write(vlq(e.Tick - last))
			last = e.Tick
			switch e.Status {
			case 0xFF:
				tb.WriteByte(0xFF)
				tb.WriteByte(e.Data[0])
				tb.Write(vlq(len(e.Data) - 1))
				tb.Write(e.Data[1:])
			case 0xF0:
				tb.WriteByte(0xF0)
				tb.Write(vlq(len(e.Data)))
				tb.Write(e.Data)
			default:
				tb.WriteByte(e.Status)
				tb.Write(e.Data)
			}
		}
		tb.Write([]byte{0, 0xFF, 0x2F, 0})
		buf.WriteString("MTrk")
		binary.Write(&buf, binary.BigEndian, uint32(tb.Len()))
		buf.Write(tb.Bytes())
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// ReadMidi parses a Standard MIDI File.
func ReadMidi(path string) (*MidiFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) < 14 || string(b[:4]) != "MThd" {
		return nil, errors.New(path + ": not a MIDI file")
	}
	hl := int(binary.BigEndian.Uint32(b[4:8]))
	m := &MidiFile{Format: int(binary.BigEndian.Uint16(b[8:10]))}
	ntr := int(binary.BigEndian.Uint16(b[10:12]))
	div := int(binary.BigEndian.Uint16(b[12:14]))
	if div&0x8000 != 0 {
		return nil, errors.New(path + ": SMPTE time division is not supported; save with ticks per quarter note")
	}
	m.PPQ = div
	p := 8 + hl
	for i := 0; i < ntr && p+8 <= len(b); i++ {
		if string(b[p:p+4]) != "MTrk" {
			return nil, fmt.Errorf("%s: track %d: bad chunk", path, i)
		}
		l := int(binary.BigEndian.Uint32(b[p+4 : p+8]))
		d := b[p+8 : min(p+8+l, len(b))]
		p += 8 + l
		t := &MidiTrack{}
		q, tick := 0, 0
		var running byte
		readV := func() int {
			v := 0
			for q < len(d) {
				c := d[q]
				q++
				v = v<<7 | int(c&0x7F)
				if c&0x80 == 0 {
					break
				}
			}
			return v
		}
		for q < len(d) {
			tick += readV()
			if q >= len(d) {
				break
			}
			st := d[q]
			if st&0x80 != 0 {
				q++
			} else {
				st = running
			}
			switch {
			case st == 0xFF:
				if q >= len(d) {
					break
				}
				typ := d[q]
				q++
				n := readV()
				data := d[q:min(q+n, len(d))]
				q += n
				if typ == 0x2F {
					q = len(d)
					break
				}
				t.Meta(tick, typ, append([]byte(nil), data...))
			case st == 0xF0 || st == 0xF7:
				n := readV()
				q += n
			case st >= 0x80:
				running = st
				n := 2
				if st&0xF0 == 0xC0 || st&0xF0 == 0xD0 {
					n = 1
				}
				if q+n > len(d) {
					q = len(d)
					break
				}
				t.add(tick, st, append([]byte(nil), d[q:q+n]...)...)
				q += n
			default:
				q = len(d)
			}
		}
		m.Tracks = append(m.Tracks, t)
	}
	return m, nil
}
