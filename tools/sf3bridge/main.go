// SPDX-License-Identifier: AGPL-3.0-only

// JSON-only worker used by the plugin. ROM operations stay out of the audio process callback.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"sf3music/snd"
)

var version = "dev"

type request struct {
	Action        string       `json:"action"`
	Donor         string       `json:"donor"`
	Output        string       `json:"output"`
	Code          int          `json:"code"`
	FallbackTempo float64      `json:"fallbackTempo,omitempty"`
	Song          snd.SongSpec `json:"song"`
}
type result struct {
	OK        bool           `json:"ok"`
	Version   string         `json:"version,omitempty"`
	Error     string         `json:"error,omitempty"`
	SoundFont string         `json:"soundFont,omitempty"`
	Output    string         `json:"output,omitempty"`
	Tracks    int            `json:"tracks,omitempty"`
	Bytes     int            `json:"bytes,omitempty"`
	Tempo     float64        `json:"tempo,omitempty"`
	Warnings  []string       `json:"warnings,omitempty"`
	Songs     map[int]string `json:"songs,omitempty"`
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(version)
		return
	}
	reply := result{}
	func() {
		defer func() {
			if e := recover(); e != nil {
				reply = result{Error: fmt.Sprintf("Invalid or unsupported input: %v", e)}
			}
		}()
		if len(os.Args) != 2 && len(os.Args) != 3 {
			reply.Error = "Expected a JSON request file"
			return
		}
		b, err := os.ReadFile(os.Args[1])
		if err != nil {
			reply.Error = err.Error()
			return
		}
		var req request
		if err = json.Unmarshal(b, &req); err != nil {
			reply.Error = err.Error()
			return
		}
		reply, err = run(req)
		if err != nil {
			reply = result{Error: err.Error()}
		}
	}()
	reply.Version = version
	if len(os.Args) == 3 {
		if b, err := json.Marshal(reply); err == nil {
			_ = os.WriteFile(os.Args[2], b, 0600)
		}
	} else {
		_ = json.NewEncoder(os.Stdout).Encode(reply)
	}
	if !reply.OK {
		os.Exit(1)
	}
}

func finitePositive(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 }

func validateSpec(s *snd.SongSpec) error {
	if len(s.Parts) == 0 {
		return errors.New("Choose a MIDI file or capture a performance first")
	}
	if s.Tempo != 0 && (!finitePositive(s.Tempo) || s.Tempo > 1000) {
		return errors.New("Tempo must be between 0 and 1000 BPM")
	}
	if s.BeatsPerBar < 0 || s.BeatsPerBar > 32 {
		return errors.New("Beats per bar must be 1–32")
	}
	if s.BendRange < 0 || s.BendRange > 48 {
		return errors.New("Pitch bend range must be 0–48 semitones")
	}
	if s.LoopStartBar != 0 && (!finitePositive(s.LoopStartBar) || s.LoopStartBar < 1) {
		return errors.New("Loop start bar must be at least 1")
	}
	if s.LoopEndBar != 0 && (!finitePositive(s.LoopEndBar) || s.LoopEndBar <= math.Max(1, s.LoopStartBar)) {
		return errors.New("Loop end must be after the start (end is exclusive)")
	}
	if s.DefaultBank < 0 || s.DefaultBank > 15 || s.DefaultProgram < 0 || s.DefaultProgram > 127 {
		return errors.New("Invalid default bank/program")
	}
	for _, p := range s.Parts {
		if p.Midi == "" {
			return errors.New("A part has no MIDI file")
		}
		if p.Channel != nil && (*p.Channel < 1 || *p.Channel > 16) {
			return errors.New("MIDI channels must be 1–16")
		}
		if p.Bank != nil && (*p.Bank < 0 || *p.Bank > 15) {
			return errors.New("CPS3 banks must be 0–15")
		}
		if p.Program != nil && (*p.Program < 0 || *p.Program > 127) {
			return errors.New("Programs must be 0–127")
		}
		if p.Track != nil && *p.Track < 0 {
			return errors.New("Track index cannot be negative")
		}
	}
	return nil
}

// Reject unavailable stock programs rather than silently wrapping or substituting them.
func validateInstruments(sound *snd.Sound, data []byte) error {
	seq := &snd.Seq{Data: data}
	decoded := snd.Decode(seq)
	for _, part := range decoded.Parts {
		bank, prog := 0, 0
		for _, e := range part.Events {
			if e.Op == snd.OpBank && len(e.Args) > 0 {
				bank = int(e.Args[0])
			}
			if e.Op == snd.OpProg && len(e.Args) > 0 {
				prog = int(e.Args[0])
			}
			if e.Note && (sound.Banks[bank] == nil || sound.Banks[bank].Programs[prog] == nil) {
				return fmt.Errorf("Bank %d program %d is not present in the donor ROM", bank, prog)
			}
		}
	}
	return nil
}

func run(req request) (result, error) {
	if req.Action != "extract" && req.Action != "analyze" && req.Action != "export" {
		return result{}, errors.New("Unknown action")
	}
	rom, err := snd.LoadROM(req.Donor)
	if err != nil {
		return result{}, err
	}
	if err := validateStockLayout(rom); err != nil {
		return result{}, err
	}
	sound, err := snd.Load(rom)
	if err != nil {
		return result{}, err
	}
	if req.Action == "extract" {
		if req.Output == "" {
			return result{}, errors.New("Extraction needs an output directory")
		}
		path := filepath.Join(req.Output, "sf3.sf2")
		if err := sound.WriteSF2(path); err != nil {
			return result{}, err
		}
		return result{OK: true, SoundFont: path, Songs: snd.SongNames}, nil
	}
	if req.Code < 1 || req.Code > 49 || req.Code >= len(sound.Seqs) || sound.Seqs[req.Code] == nil || !sound.Seqs[req.Code].IsMusic() {
		return result{}, errors.New("Select an existing Third Strike music slot (sound code 1–49)")
	}
	if err := validateSpec(&req.Song); err != nil {
		return result{}, err
	}
	if req.Song.Tempo == 0 && req.FallbackTempo != 0 {
		if !finitePositive(req.FallbackTempo) || req.FallbackTempo > 1000 {
			return result{}, errors.New("Invalid fallback tempo")
		}
		m, err := snd.ReadMidi(req.Song.Parts[0].Midi)
		if err != nil {
			return result{}, err
		}
		hasTempo := false
		for _, tr := range m.Tracks {
			for _, e := range tr.Events {
				if e.Status == 0xff && len(e.Data) >= 4 && e.Data[0] == 0x51 {
					hasTempo = true
				}
			}
		}
		if !hasTempo {
			req.Song.Tempo = req.FallbackTempo
		}
	}
	imported, err := snd.ImportSong(&req.Song)
	if err != nil {
		return result{}, err
	}
	if err := validateInstruments(sound, imported.Data); err != nil {
		return result{}, err
	}
	// This also checks available space. analyze mutates only the in-memory donor.
	if _, err = sound.PutSequence(req.Code, imported.Data); err != nil {
		return result{}, err
	}
	reply := result{OK: true, Tracks: imported.Tracks, Bytes: len(imported.Data), Tempo: imported.Tempo, Warnings: imported.Warnings}
	reply.Warnings = append(reply.Warnings, unsupportedControls(req.Song.Parts)...)
	if req.Action == "analyze" {
		return reply, nil
	}
	if req.Output == "" {
		return result{}, errors.New("Choose a new output ZIP")
	}
	if err := safeOutput(req.Donor, req.Output); err != nil {
		return result{}, err
	}
	// A fresh file is required; keep both the donor and any previous exports intact.
	f, err := os.CreateTemp(filepath.Dir(req.Output), ".cps3-export-*.zip")
	if err != nil {
		return result{}, err
	}
	tmp := f.Name()
	_ = f.Close()
	defer os.Remove(tmp)
	if err := rom.Save(tmp); err != nil {
		return result{}, err
	}
	bytes, err := os.ReadFile(tmp)
	if err != nil {
		return result{}, err
	}
	// O_EXCL prevents an output appearing between validation and commit from being overwritten.
	if err := writeNewFile(req.Output, bytes); err != nil {
		return result{}, err
	}
	reply.Output = req.Output
	reply.Warnings = append(reply.Warnings, "Open this ZIP directly in FBNeo. Changed music may produce a checksum warning.")
	return reply, nil
}

func writeNewFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}

func safeOutput(donor, output string) error {
	in, err := filepath.Abs(donor)
	if err != nil {
		return err
	}
	out, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if strings.EqualFold(filepath.Clean(in), filepath.Clean(out)) {
		return errors.New("Export must use a new file; the donor cannot be overwritten")
	}
	if !strings.EqualFold(filepath.Ext(out), ".zip") {
		return errors.New("ROM output must be a .zip file")
	}
	if !strings.EqualFold(filepath.Base(out), "sfiii3nr1.zip") {
		return errors.New("FBNeo identifies this game by the archive name sfiii3nr1.zip. Save with that name in a different folder from the donor")
	}
	if _, err := os.Lstat(out); err == nil {
		return errors.New("Output already exists; choose a new file name")
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func unsupportedControls(parts []snd.PartSpec) []string {
	seen := map[string]bool{}
	var warnings []string
	for _, p := range parts {
		if seen[p.Midi] {
			continue
		}
		seen[p.Midi] = true
		m, err := snd.ReadMidi(p.Midi)
		if err != nil {
			continue
		}
		sustain, pressure := false, false
		for _, t := range m.Tracks {
			for _, e := range t.Events {
				if e.Status&0xF0 == 0xB0 && len(e.Data) > 1 && e.Data[0] == 64 && e.Data[1] >= 64 {
					sustain = true
				}
				if e.Status&0xF0 == 0xA0 || e.Status&0xF0 == 0xD0 {
					pressure = true
				}
			}
		}
		if sustain {
			warnings = append(warnings, "Sustain pedal is previewed but not converted by the ROM importer; extend note lengths in the DAW")
		}
		if pressure {
			warnings = append(warnings, "Aftertouch is not supported by the ROM importer")
		}
	}
	return warnings
}
