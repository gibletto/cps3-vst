// SPDX-License-Identifier: AGPL-3.0-only

package snd

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"testing"
)

func TestStockReleaseRates(t *testing.T) {
	// The opening vocals use curve 48: eleven video frames, rather than a universal 80 ms fade.
	if got := forcedReleaseSeconds(48); math.Abs(got-11/FrameRate) > 1e-9 {
		t.Fatalf("vocal release = %f", got)
	}
	if forcedReleaseSeconds(63) != 1/FrameRate {
		t.Fatal("fast release must last one driver frame")
	}
	if !(Sample{Start: 10, Loop: 10, End: 20}).Looped() {
		t.Fatal("loop at sample start was discarded")
	}
	if (Sample{Start: 10, Loop: 20, End: 20}).Looped() {
		t.Fatal("one-shot sample marked looping")
	}
	path := os.Getenv("SF3_TEST_ROM")
	if path == "" {
		return
	}
	r, err := LoadROM(path)
	if err != nil {
		t.Fatal(err)
	}
	var table bytes.Buffer
	for _, rate := range decayRates {
		binary.Write(&table, binary.BigEndian, rate)
	}
	if !bytes.Contains(r.Prog, table.Bytes()) {
		t.Fatal("release rate table differs from donor ROM")
	}
	s, err := Load(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, bank := range s.Banks {
		for _, program := range bank.Programs {
			if program == nil {
				continue
			}
			for _, patch := range program.Patches {
				if int(uint8(patch.ForcedRelease)) >= len(decayRates) {
					t.Fatal("unsupported stock release curve")
				}
			}
		}
	}
}
