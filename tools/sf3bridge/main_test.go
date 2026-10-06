// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sf3music/snd"
)

func integer(v int) *int { return &v }
func songFile(t *testing.T, voices int, bank, program int) string {
	t.Helper()
	track := &snd.MidiTrack{}
	track.Tempo(0, 500000)
	track.CC(0, 0, 0, bank)
	track.Program(0, 0, program)
	for i := 0; i < voices; i++ {
		track.NoteOn(0, 0, 48+i, 100)
		track.NoteOff(480, 0, 48+i)
	}
	m := &snd.MidiFile{Format: 1, PPQ: 480, Tracks: []*snd.MidiTrack{track}}
	path := filepath.Join(t.TempDir(), "part.mid")
	if err := m.Write(path); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestSpecValidation(t *testing.T) {
	base := func() snd.SongSpec { return snd.SongSpec{Parts: []snd.PartSpec{{Midi: "part.mid"}}} }
	cases := []struct {
		name   string
		change func(*snd.SongSpec)
	}{
		{"bank", func(s *snd.SongSpec) { s.Parts[0].Bank = integer(16) }},
		{"program", func(s *snd.SongSpec) { s.Parts[0].Program = integer(128) }},
		{"channel zero", func(s *snd.SongSpec) { s.Parts[0].Channel = integer(0) }},
		{"channel 17", func(s *snd.SongSpec) { s.Parts[0].Channel = integer(17) }},
		{"loop reversed", func(s *snd.SongSpec) { s.LoopStartBar = 9; s.LoopEndBar = 1 }},
		{"loop equal", func(s *snd.SongSpec) { s.LoopStartBar = 2; s.LoopEndBar = 2 }},
		{"tempo negative", func(s *snd.SongSpec) { s.Tempo = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := base()
			tc.change(&s)
			if validateSpec(&s) == nil {
				t.Fatal("invalid spec accepted")
			}
		})
	}
	s := base()
	s.LoopStartBar = 1
	s.LoopEndBar = 9
	s.Parts[0].Channel = integer(16)
	if err := validateSpec(&s); err != nil {
		t.Fatal(err)
	}
}
func TestVoiceBudget(t *testing.T) {
	for _, n := range []int{16, 17} {
		s := snd.SongSpec{Parts: []snd.PartSpec{{Midi: songFile(t, n, 0, 10)}}}
		got, err := snd.ImportSong(&s)
		if n == 16 && (err != nil || got.Tracks != 16) {
			t.Fatalf("16 voices: %v %v", got, err)
		}
		if n == 17 && (err == nil || !strings.Contains(err.Error(), "needs 17 tracks")) {
			t.Fatalf("17 voices accepted: %v", err)
		}
	}
}

func TestChannelMappingKeepsTextOnItsOwner(t *testing.T) {
	a := &snd.MidiTrack{}
	a.Tempo(0, 500000)
	a.Program(0, 0, 10)
	a.Text(0, 1, "sf3 STATUS 0 3")
	a.NoteOn(0, 0, 60, 90)
	a.NoteOff(480, 0, 60)
	b := &snd.MidiTrack{}
	b.Program(0, 1, 6)
	b.NoteOn(0, 1, 48, 90)
	b.NoteOff(480, 1, 48)
	file := filepath.Join(t.TempDir(), "owner.mid")
	if err := (&snd.MidiFile{Format: 1, PPQ: 480, Tracks: []*snd.MidiTrack{a, b}}).Write(file); err != nil {
		t.Fatal(err)
	}
	s := snd.SongSpec{}
	for ch := 1; ch <= 16; ch++ {
		s.Parts = append(s.Parts, snd.PartSpec{Midi: file, Channel: integer(ch), Bank: integer(0), Program: integer(10)})
	}
	got, err := snd.ImportSong(&s)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, p := range snd.Decode(&snd.Seq{Data: got.Data}).Parts {
		for _, e := range p.Events {
			if !e.Note && e.Op == snd.OpStatus {
				count++
			}
		}
	}
	if got.Tracks != 2 || count != 1 {
		t.Fatalf("duplicated commands/tracks: %d tracks, %d STATUS commands", got.Tracks, count)
	}
}
func TestOutputProtectsDonorAndExistingFiles(t *testing.T) {
	dir := t.TempDir()
	donor := filepath.Join(dir, "donor.zip")
	_ = os.WriteFile(donor, []byte("donor"), 0644)
	for _, out := range []string{donor, filepath.Join(dir, "DONOR.zip"), filepath.Join(dir, "song.mid")} {
		if safeOutput(donor, out) == nil {
			t.Fatalf("unsafe output accepted: %s", out)
		}
	}
	output := filepath.Join(dir, "sfiii3nr1.zip")
	if err := safeOutput(donor, output); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "sfiii3nr1.dat"), []byte("unrelated"), 0644)
	if err := safeOutput(donor, output); err != nil {
		t.Fatal("unrelated .dat affected ZIP export:", err)
	}
	_ = os.WriteFile(output, []byte("existing"), 0644)
	if safeOutput(donor, output) == nil {
		t.Fatal("existing output accepted")
	}
	for _, name := range []string{"sfiii3nr1-jazzy.zip", "sfiii3.zip"} {
		if safeOutput(donor, filepath.Join(dir, name)) == nil {
			t.Fatalf("unrecognized archive name accepted: %s", name)
		}
	}
}

func TestROMIntegration(t *testing.T) {
	donor := os.Getenv("SF3_TEST_ROM")
	if donor == "" {
		t.Skip("Set SF3_TEST_ROM to your own sfiii3nr1.zip for real ROM integration checks")
	}
	before, err := os.ReadFile(donor)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(before)
	originalROM, err := snd.LoadROM(donor)
	if err != nil {
		t.Fatal(err)
	}
	original, err := snd.Load(originalROM)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateStockLayout(originalROM); err != nil {
		t.Fatal(err)
	}
	originalROM.Prog[0x400] ^= 1
	if validateStockLayout(originalROM) == nil {
		t.Fatal("changed stock code accepted")
	}
	originalROM.Prog[0x400] ^= 1
	input := songFile(t, 3, 0, 10)
	req := request{Action: "analyze", Donor: donor, Code: 18, Song: snd.SongSpec{Parts: []snd.PartSpec{{Midi: input}}}}
	report, err := run(req)
	if err != nil || !report.OK || report.Tracks != 3 {
		t.Fatalf("analyze: %+v %v", report, err)
	}
	req.Action = "export"
	req.Output = filepath.Join(t.TempDir(), "sfiii3nr1.zip")
	report, err = run(req)
	if err != nil || !report.OK {
		t.Fatalf("export: %+v %v", report, err)
	}
	datPath := filepath.Join(filepath.Dir(req.Output), "sfiii3nr1.dat")
	if _, err := os.Stat(datPath); !os.IsNotExist(err) {
		t.Fatal("export produced unexpected sidecar")
	}
	z, err := zip.OpenReader(req.Output)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	if len(z.File) != len(originalROM.Order) {
		t.Fatal("export changed ZIP member count")
	}
	patchedROM, err := snd.LoadROM(req.Output)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateStockLayout(patchedROM); err != nil {
		t.Fatalf("earlier music export rejected: %v", err)
	}
	patched, err := snd.Load(patchedROM)
	if err != nil {
		t.Fatal(err)
	}
	for code, seq := range original.Seqs {
		if seq == nil {
			continue
		}
		if code == 18 {
			if patched.Seqs[code].Addr == seq.Addr {
				t.Fatal("target slot unchanged")
			}
			continue
		}
		// Moving a directory entry can enlarge its predecessor's inferred slice;
		// the actual bytes at each original sequence address must remain intact.
		if patched.Seqs[code].Addr != seq.Addr || !bytes.Equal(patchedROM.At(seq.Addr, len(seq.Data)), seq.Data) {
			t.Fatalf("non-target sequence %d changed", code)
		}
	}
	newAddress := patched.Seqs[18].Addr
	pointerAddress := uint32(snd.SeqTableAddr + 4 + 18*4)
	for i, v := range originalROM.Prog {
		address := uint32(snd.ProgBase + i)
		allowed := (address >= newAddress && address < newAddress+uint32(report.Bytes)) || (address >= pointerAddress && address < pointerAddress+4)
		if !allowed && patchedROM.Prog[i] != v {
			t.Fatalf("unrelated program byte changed at %08x", address)
		}
	}
	for name, data := range originalROM.Files {
		if strings.HasPrefix(name, "sfiii3-simm1.") || strings.HasPrefix(name, "sfiii3-simm2.") {
			continue
		}
		if !bytes.Equal(data, patchedROM.Files[name]) {
			t.Fatalf("sample/graphics/BIOS file changed: %s", name)
		}
	}
	after, err := os.ReadFile(donor)
	if err != nil || sha256.Sum256(after) != hash {
		t.Fatal("donor changed")
	}
	if _, err := run(req); err == nil {
		t.Fatal("second export overwrote an existing output")
	}
	req.Action = "analyze"
	req.Song.Parts[0].Bank = integer(8)
	req.Song.Parts[0].Program = integer(10)
	if _, err := run(req); err == nil || !strings.Contains(err.Error(), "not present") {
		t.Fatalf("empty bank accepted: %v", err)
	}
	req.Song.Parts[0].Bank = integer(0)
	req.Code = 2007
	if _, err := run(req); err == nil {
		t.Fatal("out of range sound code accepted")
	}
	req.Action = "extract"
	req.Output = t.TempDir()
	report, err = run(req)
	if err != nil || !report.OK {
		t.Fatalf("extract: %+v %v", report, err)
	}
	if info, err := os.Stat(report.SoundFont); err != nil || info.Size() < 1000 {
		t.Fatal("no SoundFont generated")
	}
}
