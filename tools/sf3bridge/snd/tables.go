// SPDX-License-Identifier: AGPL-3.0-only

package snd

import (
	"fmt"
	"sort"
)

// Arcade addresses of the sound tables (the program's SNDBANK and SND sections, fixed in the ROM).
const (
	BankTableAddr   = 0x06788000 // snd_bank_rom_tbl: 16 bank pointers
	SampleTableAddr = 0x0678C000 // snd_sample_rom_tbl: 768 samples
	SampleCount     = 768
	SeqTableAddr    = 0x0678F000 // snd_seq_rom_data: header, then one offset per sound code
	SndEnd          = 0x067D1F6B // the end of the SND section's data
	FreeEnd         = 0x067FFFE0 // the next section (CCDVOL); SndEnd..FreeEnd is zero fill
	SampleAddrBias  = 0x400000   // the chip's addresses (MAME subtracts it); the table's are already plain
)

// Hardware timing: the driver runs from the vertical blank; the chip's output rate.
const (
	FrameRate    = 42954545.0 / 5 / (546 * 264) // CPS3 video: 59.599 Hz
	ChipRate     = 42954545.0 / 3 / 384         // the sound chip's output rate: 37287 Hz
	RootStep     = 4065                         // snd_pitch_tbl[7 * 256 + 0x80]: the chip's step at a sample's root note
	RootRate     = ChipRate * RootStep / 4096   // a sample plays at this rate at its base note
	PitchPerSemi = 256                          // driver pitch units per semitone
)

// Sample is one entry of snd_sample_rom_tbl: 8-bit signed PCM in the sample ROM.
type Sample struct {
	Start, Loop, End uint32 // the chip plays Start..End and, if the patch loops, jumps back to Loop
	BasePitch        int32  // the note at which it plays at RootRate
}

// Patch is one key split of a program (SNDPATCH).
type Patch struct {
	NoteCeiling   int8  // the highest note of the split
	Pan           uint8 // 0xFF: none (the track's PAN applies); otherwise 0 left .. 0x7F right
	VolumeBias    int8
	Reserved      uint8
	SampleIndex   uint16 // bit 15: the sample loops (the driver keeps it sounding)
	PitchBias     int8   // in 1/256 semitone
	AttackCurve   int8
	DecayCurve    int8
	VelocityScale uint8
	ReleaseCurve  int8
	ForcedRelease int8
}

// Program is a list of key splits, lowest first.
type Program struct {
	Bank, Number int
	Patches      []Patch
}

// Bank holds up to 128 programs (nil: none).
type Bank struct {
	Number   int
	Addr     uint32
	Programs [128]*Program
}

// Seq is one sound code's sequence.
type Seq struct {
	Code int
	Addr uint32 // arcade address
	Data []byte // its bytes, to the next sequence
}

// IsMusic tells music (first byte 0) from a sound effect (first byte: the effect's priority).
func (s *Seq) IsMusic() bool { return len(s.Data) > 0 && s.Data[0] == 0 }

// TrackOffsets are the 16 track offsets from the sequence's start (0: no track).
func (s *Seq) TrackOffsets() [16]int {
	var t [16]int
	for i := 0; i < 16 && 1+2*i+1 < len(s.Data); i++ {
		t[i] = int(s.Data[1+2*i])<<8 | int(s.Data[2+2*i])
	}
	return t
}

// Sound is everything the sound driver reads.
type Sound struct {
	ROM     *ROM
	Samples []Sample
	Banks   [16]*Bank
	SeqHead uint32 // the table's first word: count << 16 | BGM volume << 8 | master volume
	Seqs    []*Seq // by sound code (index 0: code 0); nil: no sound
}

// Load reads the sound tables.
func Load(r *ROM) (*Sound, error) {
	s := &Sound{ROM: r}
	for i := 0; i < SampleCount; i++ {
		a := uint32(SampleTableAddr + i*16)
		s.Samples = append(s.Samples, Sample{r.U32(a), r.U32(a + 4), r.U32(a + 8), int32(r.U32(a + 12))})
	}
	for b := 0; b < 16; b++ {
		addr := r.U32(uint32(BankTableAddr + b*4))
		bank := &Bank{Number: b, Addr: addr}
		s.Banks[b] = bank
		if addr == 0 {
			continue
		}
		for p := 0; p < 128; p++ {
			off := r.U16(addr + uint32(p*2))
			if off == 0 {
				continue
			}
			prog := &Program{Bank: b, Number: p}
			for k := 0; k < 128; k++ {
				pb := r.At(addr+uint32(off)+uint32(k*12), 12)
				pt := Patch{int8(pb[0]), pb[1], int8(pb[2]), pb[3], uint16(pb[4])<<8 | uint16(pb[5]), int8(pb[6]),
					int8(pb[7]), int8(pb[8]), pb[9], int8(pb[10]), int8(pb[11])}
				if pt.NoteCeiling == -1 {
					break
				}
				prog.Patches = append(prog.Patches, pt)
			}
			if len(prog.Patches) > 0 {
				bank.Programs[p] = prog
			}
		}
	}
	s.SeqHead = r.U32(SeqTableAddr)
	count := int(s.SeqHead >> 16)
	offs := make([]uint32, count)
	var starts []uint32
	for c := 0; c < count; c++ {
		offs[c] = r.U32(uint32(SeqTableAddr + 4 + c*4))
		if offs[c] != 0 {
			starts = append(starts, SeqTableAddr+offs[c])
		}
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	end := func(a uint32) uint32 {
		i := sort.Search(len(starts), func(i int) bool { return starts[i] > a })
		limit := uint32(SndEnd)
		if a >= SndEnd { // imported: to the end of the data in its free region
			for _, rg := range freeRegions {
				if a >= rg[0] && a < rg[1] {
					limit, _ = r.regionFree(rg[0], rg[1])
				}
			}
		}
		if i < len(starts) && starts[i] < limit {
			return starts[i]
		}
		return limit
	}
	s.Seqs = make([]*Seq, count)
	for c := 0; c < count; c++ {
		if offs[c] == 0 {
			continue
		}
		a := SeqTableAddr + offs[c]
		if a >= FreeEnd && !(a >= freeRegions[1][0] && a < freeRegions[1][1]) {
			return nil, fmt.Errorf("sound code %d: address %08X outside the sound section", c, a)
		}
		e := end(a)
		s.Seqs[c] = &Seq{Code: c, Addr: a, Data: r.At(a, int(e-a))}
	}
	return s, nil
}

// SampleData returns a sample's PCM (signed 8-bit), from its start to its end.
func (s *Sound) SampleData(i int) []int8 {
	sm := s.Samples[i]
	if sm.End <= sm.Start || int(sm.End) > len(s.ROM.Sample) {
		return nil
	}
	out := make([]int8, sm.End-sm.Start)
	for k := range out {
		out[k] = int8(s.ROM.Sample[int(sm.Start)+k])
	}
	return out
}
