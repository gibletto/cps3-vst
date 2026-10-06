// SPDX-License-Identifier: AGPL-3.0-only

// Generate a synthetic SoundFont so CI can test audio without game samples or ROMs.
package main

import (
	"fmt"
	"math"
	"os"

	"sf3music/snd"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "Expected an output SoundFont path")
		os.Exit(1)
	}
	pcm := make([]byte, snd.SampleRate()*2)
	for i := range pcm {
		pcm[i] = byte(int8(80 * math.Sin(2*math.Pi*440*float64(i)/float64(snd.SampleRate()))))
	}
	sound := &snd.Sound{
		ROM:     &snd.ROM{Sample: pcm},
		Samples: []snd.Sample{{Start: 0, Loop: uint32(len(pcm)), End: uint32(len(pcm)), BasePitch: 60}},
	}
	for i := range sound.Banks {
		sound.Banks[i] = &snd.Bank{Number: i}
	}
	for _, number := range []int{0, 7, 10} {
		sound.Banks[0].Programs[number] = &snd.Program{
			Bank: 0, Number: number,
			Patches: []snd.Patch{{NoteCeiling: 127, Pan: 255, AttackCurve: 63, DecayCurve: 63, VelocityScale: 127, ForcedRelease: 48}},
		}
	}
	if err := sound.WriteSF2(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
