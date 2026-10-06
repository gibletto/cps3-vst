# CPS3 Instrument

Make music with Street Fighter III: Third Strike's sounds. Available as a Windows 64-bit VST3 and standalone app.

You need your own SoundFont (`.sf2`) or original `sfiii3nr1.zip` ROM. Sounds and ROMs are not included.

## Install

Copy the whole **CPS3 Instrument.vst3** folder to `C:\Program Files\Common Files\VST3`, then rescan plugins in your DAW.

For standalone use, open **CPS3 Instrument.exe**.

## Load sounds

1. Open the plugin on an instrument track.
2. Drop your `.sf2` or `sfiii3nr1.zip` onto it, or click **Load SF2 / ROM**.
3. Start with **Bank 0** and choose an instrument for each channel.

The plugin has 16 MIDI channels. Give each part its own channel. Turn off **Follow MIDI bank/program** if you want the instruments chosen in the plugin to stay fixed.

If notes sound cut short with an older SoundFont, load your ROM again to extract a new one with the correct release settings.

When playing extracted MIDI, use its original tempo. Vocal phrases and drum loops have fixed lengths, so a slower tempo can leave gaps between samples. In Ableton, accept the tempo import when prompted. The opening song (`0001_op_100_move.mid`) uses **119.02 BPM**.

### FL Studio

Set an **Input port** in the plugin's wrapper settings. Add a **MIDI Out** for each part, using that same port and a different MIDI channel. [MIDI Out guide](https://www.image-line.com/fl-studio-learning/fl-studio-online-manual/html/plugins/MIDI%20Out.htm).

### Ableton Live

On each MIDI track, set **MIDI To** to the track holding CPS3 Instrument. In the lower chooser, select the plugin and the desired MIDI channel. [Live routing guide](https://www.ableton.com/en/manual/routing-and-i-o/#using-multi-timbral-plug-in-instruments).

## Capture MIDI

Capture records MIDI notes and controls, not audio.

1. Turn DAW looping off and move to the start of your song.
2. Click **Arm capture**, then press Play in your DAW.
3. Play through the song once, then stop.
4. Click **Use capture**.

Use **Save MIDI** to keep a MIDI file you can edit later. Capture needs DAW playback; use a MIDI file in the standalone app.

## Use a MIDI file instead

Click **Choose MIDI** or drop a `.mid` file onto the plugin. No capture is needed. The file is used for ROM export; play it in your DAW to hear it.

Enable **Use rack instruments for MIDI file** to use the instruments chosen in the plugin. Disable it to keep the file's instrument changes.

## Set the loop

Enable **Loop**, then set **Start bar** and **End bar**. The end is where playback jumps back:

- **Start 1, End 9:** repeat eight bars.
- **Start 5, End 13:** play a four-bar intro, then repeat bars 5–12.

Bar 1 is the beginning of your capture or imported MIDI file. **End bar = 0** loops the whole song, unless the MIDI file has loop markers.

Set **Quarter notes / bar** to 4 for 4/4, or 3 for 3/4 and 6/8. Disable **Loop** for music that plays once.

**BPM** is used when the MIDI file has no tempo. Enable **Override tempo** to change the song's tempo.

## Export to Third Strike

1. Click **Choose donor** and select your original `sfiii3nr1.zip` (990512 version).
2. Choose the stage or scene in **Replace music**, then choose its **Round variant**.
3. Click **Analyze** to check the arrangement.
4. Click **Export ROM ZIP**. Save as **sfiii3nr1.zip in a different folder** from the original.

Open that ZIP directly in FBNeo. Keep the filename `sfiii3nr1.zip`; use folder names to distinguish songs, for example `My Song/sfiii3nr1.zip`. A checksum warning is expected when music has been changed.

Your original ROM is kept intact. Existing exports cannot be overwritten.

## Things to know

- The game has 16 music voices. Chords use several voices; **Analyze** tells you if the arrangement needs simplifying.
- Draw sustained notes at their full length. Sustain pedal and aftertouch are not exported to the ROM.
- The preview may sound different from the game. ROM export uses the game's existing sounds; custom SoundFont samples are not added.
- Some stages and endings share music. Replacing one can affect another.
- Keep your SoundFont, MIDI and ROM files in place so saved projects can find them.

## Source and licence

CPS3 Instrument and its ROM converter are licensed under [AGPL-3.0](LICENSE). Dependency credits are in [Third-party notices](THIRD_PARTY_NOTICES.md). Build instructions are in [Development](docs/DEVELOPMENT.md).
