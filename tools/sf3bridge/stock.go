// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"crypto/sha256"
	"errors"
	"fmt"

	"sf3music/snd"
)

// Verified against the 990512 stock SIMMs (not the rebuilt sf3.map). Sound
// changes live beyond this range, so an earlier music export can still be used.
func validateStockLayout(r *snd.ROM) error {
	const expected = "2a15de640f14181e93231d9fb9fcabf2cc030d58709f1ddf174ba85318b5a735"
	if len(r.Prog) < snd.BankTableAddr-snd.ProgBase || fmt.Sprintf("%x", sha256.Sum256(r.At(snd.ProgBase, snd.BankTableAddr-snd.ProgBase))) != expected {
		return errors.New("Unsupported donor layout: use the stock Third Strike 990512 sfiii3nr1.zip, not a recompiled decompilation or another revision")
	}
	return nil
}
