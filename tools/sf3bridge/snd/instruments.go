// SPDX-License-Identifier: AGPL-3.0-only

package snd

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// Instruments: the samples as WAV files (16-bit, with a smpl loop chunk), every program as an SFZ instrument
// (sforzando and other SFZ players load it, in Ableton or any DAW) and all of them in one SoundFont (SF2):
// preset bank = the driver's bank, preset = its program, so a MIDI file's bank select (CC0) and program
// change pick the instrument the sequence picks.
//
// Pitch: at its base note a sample plays at RootRate; the patch's pitch bias is a fine tune in 1/256 semitone.
// SoundFont note-off releases use the patch's forced-release curve. Attack, natural decay, volume bias and LFOs
// are still approximated. The optional cps3 RIFF chunk lets the plugin use a linear release rather than
// SoundFont's exponential one; other SoundFont players can still load the file normally.

const releaseSec = 0.08

// snd_decay_rate_tbl from the original driver. Note-off subtracts a fixed fraction of the current
// level each video frame. All stock patches use indices 0..63.
var decayRates = [...]uint16{
	0, 1, 2, 2, 3, 3, 4, 4, 5, 6, 8, 10, 12, 14, 17, 19,
	24, 29, 33, 38, 48, 57, 67, 76, 95, 114, 133, 152, 190, 228, 266, 304,
	381, 457, 533, 608, 761, 913, 1066, 1217, 1522, 1826, 2131, 2435, 3044, 3654, 4262, 4871,
	6089, 7307, 8524, 9736, 12178, 14614, 17049, 19486, 24356, 29224, 34099, 38965, 48702, 58449, 61439, 65535,
}

func forcedReleaseSeconds(curve int8) float64 {
	i := int(uint8(curve))
	if i >= len(decayRates) {
		return releaseSec
	}
	// A reference full-scale envelope accounts for the driver's integer division and minimum rate.
	const level = 32768
	rate := max(1, level*(int(decayRates[i])+1)>>16)
	return float64((level+rate-1)/rate) / FrameRate
}

// SampleRate is the WAV/SF2 sample rate: the chip's rate at the root note.
func SampleRate() int { return int(math.Round(RootRate)) }

// Looped tells whether a sample has a loop (a loop point before its end).
func (sm Sample) Looped() bool { return sm.Loop >= sm.Start && sm.Loop < sm.End }

func pcm16(d []int8) []byte {
	b := make([]byte, 2*len(d))
	for i, v := range d {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(int16(v)<<8))
	}
	return b
}

// WriteWAV writes sample i as a 16-bit mono WAV file with its base note and loop in a smpl chunk.
func (s *Sound) WriteWAV(i int, path string) error {
	sm := s.Samples[i]
	d := s.SampleData(i)
	if d == nil {
		return fmt.Errorf("sample %d: empty", i)
	}
	rate := SampleRate()
	data := pcm16(d)
	var smpl bytes.Buffer
	w32 := func(v uint32) { binary.Write(&smpl, binary.LittleEndian, v) }
	w32(0)
	w32(0)
	w32(uint32(1e9 / float64(rate)))
	w32(uint32(sm.BasePitch))
	w32(0)
	w32(0)
	w32(0)
	if sm.Looped() {
		w32(1)
		w32(0)
		w32(0)
		w32(0)
		w32(sm.Loop - sm.Start)
		w32(sm.End - sm.Start - 1)
		w32(0)
		w32(0)
	} else {
		w32(0)
		w32(0)
	}
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(4+8+16+8+len(data)+8+smpl.Len()))
	b.WriteString("WAVEfmt ")
	binary.Write(&b, binary.LittleEndian, []any{uint32(16), uint16(1), uint16(1), uint32(rate), uint32(rate * 2),
		uint16(2), uint16(16)})
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(len(data)))
	b.Write(data)
	b.WriteString("smpl")
	binary.Write(&b, binary.LittleEndian, uint32(smpl.Len()))
	b.Write(smpl.Bytes())
	return os.WriteFile(path, b.Bytes(), 0o644)
}

// Zone is one key split of a program, as the instruments use it.
type Zone struct {
	LoKey, HiKey int
	Sample       int
	Root         int     // the sample's base note
	Cents        float64 // fine tune
	Pan          int     // -64..63 (0 centre); only when the patch sets one
	HasPan       bool
	Loop         bool
	Release      float64 // linear note-off release in seconds
}

// Zones lists a program's key splits.
func (s *Sound) Zones(p *Program) []Zone {
	var zs []Zone
	lo := 0
	for _, pt := range p.Patches {
		hi := int(pt.NoteCeiling)
		if hi > 127 {
			hi = 127
		}
		idx := int(pt.SampleIndex & 0x7FFF)
		if hi >= lo && idx < len(s.Samples) {
			sm := s.Samples[idx]
			z := Zone{LoKey: lo, HiKey: hi, Sample: idx, Root: int(sm.BasePitch), Cents: float64(pt.PitchBias) * 100 / 256,
				Loop: sm.Looped(), Release: forcedReleaseSeconds(pt.ForcedRelease)}
			if pt.Pan <= 0x7F { // 0xFF: none (the track's PAN applies), as every patch of the game has it
				z.Pan, z.HasPan = int(pt.Pan)-64, true
			}
			zs = append(zs, z)
		}
		lo = hi + 1
	}
	return zs
}

// WriteSFZ writes a program as an SFZ file; sampleDir is the WAV directory relative to the SFZ file.
func (s *Sound) WriteSFZ(p *Program, path, sampleDir string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "// Street Fighter III 3rd Strike: bank %d program %d\n<control>\ndefault_path=%s/\n<global>\n"+
		"ampeg_release=%.2f\n", p.Bank, p.Number, sampleDir, releaseSec)
	for _, z := range s.Zones(p) {
		fmt.Fprintf(&b, "<region> sample=%s lokey=%d hikey=%d pitch_keycenter=%d", sampleName(z.Sample), z.LoKey, z.HiKey, z.Root)
		if z.Cents != 0 {
			fmt.Fprintf(&b, " tune=%d", int(math.Round(z.Cents)))
		}
		if z.HasPan {
			fmt.Fprintf(&b, " pan=%d", int(math.Round(float64(z.Pan)*100/64)))
		}
		if z.Loop {
			sm := s.Samples[z.Sample]
			fmt.Fprintf(&b, " loop_mode=loop_continuous loop_start=%d loop_end=%d", sm.Loop-sm.Start, sm.End-sm.Start-1)
		} else {
			b.WriteString(" loop_mode=no_loop")
		}
		b.WriteString("\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func sampleName(i int) string { return fmt.Sprintf("sample_%03d.wav", i) }

// ProgramName names a program's instrument file.
func ProgramName(p *Program) string { return fmt.Sprintf("bank%d_prog%03d", p.Bank, p.Number) }

// WriteSF2 writes every program into one SoundFont.
func (s *Sound) WriteSF2(path string) error {
	type shdr struct {
		name                     string
		start, end, lstart, lend uint32
		root                     int
	}
	var smpl bytes.Buffer
	var shdrs []shdr
	used := map[int]int{} // sample -> shdr index
	sampleOf := func(i int) int {
		if k, ok := used[i]; ok {
			return k
		}
		d := s.SampleData(i)
		sm := s.Samples[i]
		start := uint32(smpl.Len() / 2)
		smpl.Write(pcm16(d))
		smpl.Write(make([]byte, 46*2))
		h := shdr{name: fmt.Sprintf("sample %d", i), start: start, end: start + uint32(len(d)), root: int(sm.BasePitch)}
		if sm.Looped() {
			h.lstart, h.lend = start+(sm.Loop-sm.Start), start+(sm.End-sm.Start)
		} else {
			h.lstart, h.lend = start, start+uint32(len(d))
		}
		used[i] = len(shdrs)
		shdrs = append(shdrs, h)
		return used[i]
	}
	var phdr, pbag, pgen, inst, ibag, igen, releases bytes.Buffer
	le := func(b *bytes.Buffer, v ...any) {
		for _, x := range v {
			binary.Write(b, binary.LittleEndian, x)
		}
	}
	name20 := func(b *bytes.Buffer, n string) {
		x := make([]byte, 20)
		copy(x, n)
		b.Write(x)
	}
	gen := func(b *bytes.Buffer, op uint16, amt int16) { le(b, op, amt) }
	genRange := func(b *bytes.Buffer, op uint16, lo, hi int) { le(b, op, uint8(lo), uint8(hi)) }
	nIgen, nIbag, nPgen, nPbag, nInst := 0, 0, 0, 0, 0
	for _, bank := range s.Banks {
		for _, p := range bank.Programs {
			if p == nil {
				continue
			}
			zs := s.Zones(p)
			if len(zs) == 0 {
				continue
			}
			name20(&inst, ProgramName(p))
			le(&inst, uint16(nIbag))
			for _, z := range zs {
				// Versioned extension: bank, program, key range, reserved, linear release seconds.
				le(&releases, uint16(p.Bank), uint16(p.Number), uint8(z.LoKey), uint8(z.HiKey), uint16(0), float32(z.Release))
				le(&ibag, uint16(nIgen), uint16(0))
				nIbag++
				genRange(&igen, 43, z.LoKey, z.HiKey) // keyRange
				nIgen++
				// Match the initial fade rate in ordinary SF2 players (TSF's exponential drops ~80 dB).
				// The plugin reads the exact linear duration from the cps3 chunk below.
				gen(&igen, 38, int16(math.Round(1200*math.Log2(math.Min(100, 9.226*z.Release))))) // releaseVolEnv
				nIgen++
				if z.Cents != 0 {
					gen(&igen, 52, int16(math.Round(z.Cents))) // fineTune
					nIgen++
				}
				if z.HasPan {
					gen(&igen, 17, int16(math.Round(float64(z.Pan)*500/64))) // pan
					nIgen++
				}
				if z.Loop {
					gen(&igen, 54, 1) // sampleModes: loop
					nIgen++
				}
				gen(&igen, 58, int16(z.Root)) // overridingRootKey
				nIgen++
				gen(&igen, 53, int16(sampleOf(z.Sample))) // sampleID (last)
				nIgen++
			}
			// the preset: bank, program, one zone with the instrument
			name20(&phdr, ProgramName(p))
			le(&phdr, uint16(p.Number), uint16(p.Bank), uint16(nPbag), uint32(0), uint32(0), uint32(0))
			le(&pbag, uint16(nPgen), uint16(0))
			nPbag++
			gen(&pgen, 41, int16(nInst)) // instrument
			nPgen++
			nInst++
		}
	}
	// terminal records
	name20(&phdr, "EOP")
	le(&phdr, uint16(0), uint16(0), uint16(nPbag), uint32(0), uint32(0), uint32(0))
	le(&pbag, uint16(nPgen), uint16(0))
	le(&pgen, uint16(0), uint16(0))
	name20(&inst, "EOI")
	le(&inst, uint16(nIbag))
	le(&ibag, uint16(nIgen), uint16(0))
	le(&igen, uint16(0), uint16(0))
	var sh bytes.Buffer
	for _, h := range shdrs {
		name20(&sh, h.name)
		le(&sh, h.start, h.end, h.lstart, h.lend, uint32(SampleRate()), uint8(h.root), int8(0), uint16(0), uint16(1))
	}
	name20(&sh, "EOS")
	le(&sh, uint32(0), uint32(0), uint32(0), uint32(0), uint32(0), uint8(0), int8(0), uint16(0), uint16(0))
	pmod := make([]byte, 10)
	imod := make([]byte, 10)

	chunk := func(id string, data []byte) []byte {
		var b bytes.Buffer
		b.WriteString(id)
		le(&b, uint32(len(data)))
		b.Write(data)
		if len(data)%2 == 1 {
			b.WriteByte(0)
		}
		return b.Bytes()
	}
	list := func(typ string, parts ...[]byte) []byte {
		var body bytes.Buffer
		body.WriteString(typ)
		for _, p := range parts {
			body.Write(p)
		}
		return chunk("LIST", body.Bytes())
	}
	info := list("INFO", chunk("ifil", []byte{2, 0, 1, 0}), chunk("isng", []byte("EMU8000\x00")),
		chunk("INAM", []byte("Street Fighter III 3rd Strike\x00")),
		chunk("ICMT", []byte("Extracted from the arcade ROM by sf3snd\x00")))
	sdta := list("sdta", chunk("smpl", smpl.Bytes()))
	pdta := list("pdta", chunk("phdr", phdr.Bytes()), chunk("pbag", pbag.Bytes()), chunk("pmod", pmod),
		chunk("pgen", pgen.Bytes()), chunk("inst", inst.Bytes()), chunk("ibag", ibag.Bytes()), chunk("imod", imod),
		chunk("igen", igen.Bytes()), chunk("shdr", sh.Bytes()))
	var body bytes.Buffer
	body.WriteString("sfbk")
	body.Write(info)
	body.Write(sdta)
	body.Write(pdta)
	var cps3 bytes.Buffer
	le(&cps3, uint32(1), uint32(releases.Len()/12))
	cps3.Write(releases.Bytes())
	body.Write(chunk("cps3", cps3.Bytes()))
	out := chunk("RIFF", body.Bytes())
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}
