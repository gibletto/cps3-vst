// SPDX-License-Identifier: AGPL-3.0-only

// Package snd reads and writes the sound data of Street Fighter III 3rd Strike (CPS3): the sample ROM, the
// instrument banks, the sample table and the sound sequences, from the arcade ROM set (sfiii3nr1.zip).
package snd

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
)

const (
	chipSize = 0x200000     // one flash chip
	simmSize = 4 * chipSize // a program SIMM: four chips, one byte lane each
	ProgBase = 0x06000000   // the first program SIMM's address
	key1     = 0xA55432B4   // 3rd Strike's CPS3 program keys (MAME cps3_state::cps3_mask)
	key2     = 0x0C129981
)

// ROM is the ROM set, with the first program SIMM decrypted (06000000-067FFFFF, where the sound tables are)
// and the sample ROM as the sound chip sees it.
type ROM struct {
	Path   string
	Files  map[string][]byte // every file of the zip, by name
	Order  []string          // the zip's file order
	Prog   []byte            // 06000000-06FFFFFF: the two program SIMMs, decrypted
	Sample []byte            // the sample ROM (SIMM 3)
}

func rol16(v uint32, n uint) uint32 {
	v &= 0xFFFF
	return ((v << n) | (v >> (16 - n))) & 0xFFFF
}

func rotxor(val, x uint32) uint32 {
	res := (val + rol16(val, 2)) & 0xFFFF
	return (rol16(res, 4) ^ (res & (val ^ x))) & 0xFFFF
}

// mask is the CPS3 program encryption for the long at addr (MAME's cps3_mask).
func mask(addr uint32) uint32 {
	addr ^= key1
	v := (addr & 0xFFFF) ^ 0xFFFF
	v = rotxor(v, key2&0xFFFF)
	v ^= ((addr >> 16) & 0xFFFF) ^ 0xFFFF
	v = rotxor(v, key2>>16)
	v ^= (addr & 0xFFFF) ^ (key2 & 0xFFFF)
	return (v | (v << 16)) & 0xFFFFFFFF
}

// LoadROM reads the ROM set.
func LoadROM(path string) (*ROM, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	r := &ROM{Path: path, Files: map[string][]byte{}}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		r.Files[f.Name] = b
		r.Order = append(r.Order, f.Name)
	}
	r.Prog = make([]byte, 2*simmSize)
	for n := 1; n <= 2; n++ {
		chips := make([][]byte, 4)
		for i := range chips {
			name := fmt.Sprintf("sfiii3-simm%d.%d", n, i)
			if chips[i] = r.Files[name]; len(chips[i]) != chipSize {
				return nil, fmt.Errorf("%s: %s missing or not 2 MB (is this sfiii3nr1.zip?)", path, name)
			}
		}
		base := (n - 1) * simmSize
		for i := 0; i < chipSize; i++ {
			w := uint32(chips[0][i])<<24 | uint32(chips[1][i])<<16 | uint32(chips[2][i])<<8 | uint32(chips[3][i])
			w ^= mask(uint32(ProgBase + base + i*4))
			o := base + i*4
			r.Prog[o], r.Prog[o+1], r.Prog[o+2], r.Prog[o+3] = byte(w>>24), byte(w>>16), byte(w>>8), byte(w)
		}
	}
	// The sample ROM: SIMM 3's eight chips in pairs, 16-bit words from each, bytes chip 1, chip 0, alternating
	// (MAME's cps3 copy_from_nvram and cps3_sound_device read m_user5 this way).
	for p := 0; p < 4; p++ {
		a, b := r.Files[fmt.Sprintf("sfiii3-simm3.%d", 2*p)], r.Files[fmt.Sprintf("sfiii3-simm3.%d", 2*p+1)]
		if len(a) != chipSize || len(b) != chipSize {
			return nil, fmt.Errorf("%s: sfiii3-simm3.%d/%d missing", path, 2*p, 2*p+1)
		}
		pair := make([]byte, 2*chipSize)
		for i := 0; i < chipSize; i++ {
			pair[2*i], pair[2*i+1] = b[i], a[i]
		}
		r.Sample = append(r.Sample, pair...)
	}
	return r, nil
}

// At returns n bytes of the program at an arcade address.
func (r *ROM) At(addr uint32, n int) []byte {
	o := int(addr - ProgBase)
	return r.Prog[o : o+n]
}

func (r *ROM) U16(addr uint32) uint16 { b := r.At(addr, 2); return uint16(b[0])<<8 | uint16(b[1]) }
func (r *ROM) U32(addr uint32) uint32 {
	b := r.At(addr, 4)
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

// Put writes bytes into the program at an arcade address.
func (r *ROM) Put(addr uint32, b []byte) { copy(r.Prog[int(addr-ProgBase):], b) }

func (r *ROM) PutU32(addr uint32, v uint32) {
	r.Put(addr, []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// Save writes the ROM set to path, with the program SIMMs encrypted again from Prog.
func (r *ROM) Save(path string) error {
	chips := map[string][]byte{}
	for n := 1; n <= 2; n++ {
		base := (n - 1) * simmSize
		cs := make([][]byte, 4)
		for c := range cs {
			cs[c] = make([]byte, chipSize)
		}
		for i := 0; i < chipSize; i++ {
			o := base + i*4
			w := uint32(r.Prog[o])<<24 | uint32(r.Prog[o+1])<<16 | uint32(r.Prog[o+2])<<8 | uint32(r.Prog[o+3])
			w ^= mask(uint32(ProgBase + o))
			for c := 0; c < 4; c++ {
				cs[c][i] = byte(w >> (24 - 8*uint(c)))
			}
		}
		for c := 0; c < 4; c++ {
			chips[fmt.Sprintf("sfiii3-simm%d.%d", n, c)] = cs[c]
		}
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range r.Order {
		data := r.Files[name]
		if c, ok := chips[name]; ok {
			data = c
		}
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
