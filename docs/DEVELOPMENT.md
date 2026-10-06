# Development

The root README covers using the instrument. This file covers its source, tests and releases.

## Build

Use Windows x64 with Visual Studio 2022 C++ tools, a Windows SDK, CMake 3.22+ and Go 1.25+.

```powershell
.\scripts\build.ps1
```

This builds the VST3, standalone app, ROM worker and test tools, runs the tests and Go vet, then creates a package. It uses two build jobs by default; use `-Jobs` to change that.

Outputs:

- `dist/CPS3Instrument-Windows-x64/`: unpacked app and VST3.
- `dist/CPS3Instrument-<version>-Windows-x64.zip`: download package.
- The matching `.zip.sha256`: archive checksum.

The package contains the app, complete VST3 bundle, README, version and licence notices. It excludes developer notes, the ROM audit, test tools, ROMs and SoundFonts.

CMake downloads JUCE from a pinned archive and verifies its SHA256. An existing `external/JUCE` checkout can be used for local work. TinySoundFont is vendored in `external/TinySoundFont`.

## Style

C++ uses four spaces, Allman braces and one statement per line. Use clang-format 19; CI uses 19.1.5. Go uses gofmt. Dependency source is excluded from formatting.

```powershell
.\scripts\format.ps1
.\scripts\format.ps1 -Check
```

The script finds clang-format on PATH or in Visual Studio's LLVM tools. Alternatively, install the same formatter as CI:

```powershell
python -m pip install clang-format==19.1.5
```

## Tests

```powershell
ctest --test-dir build -C Release --output-on-failure
```

The default tests generate a sine-wave SoundFont. No game files are required. They check MIDI routing, saved state, capture timing, note closure, sample offsets, releases, sustain, panic and VST3 loading, audio and editor creation. Go tests cover MIDI import, validation and export rules.

For the optional ROM tests, set `SF3_TEST_ROM` to your original 990512 `sfiii3nr1.zip` and run the build again. These check extraction, donor preservation, stock layout and replacement-song bytes.

The processor test executable also accepts a freshly extracted SoundFont, a ROM and a VST3 bundle, in that order. Use these to check the embedded worker and plugin's full ROM-export path.

`CPS3Render` renders a MIDI file through the processor:

```text
CPS3Render font.sf2 song.mid output.wav [BPM]
```

Without BPM it uses the MIDI tempo map; with BPM it simulates a fixed DAW tempo. It writes note times beside the WAV and refuses an existing WAV output. This reproduced the opening song's sample gaps at 116 BPM; the original 119.02 BPM resolved them in Ableton.

An example ROM export booted to the attract screen in an offscreen FBNeo core. Replacement-music playback still needs a manual in-game check.

## Versions and releases

`VERSION` is the source of the numeric `major.minor.patch` version. CMake uses it for the plugin metadata, UI and bundled worker; packaging uses it for the archive name. Keep the plugin identifiers and parameter IDs stable so DAW projects keep loading.

1. Update `VERSION` and `CHANGELOG.md`.
2. Run formatting checks and `scripts/build.ps1`.
3. Commit the changes and push a matching tag, such as `v0.1.1`.
4. Review and publish the draft GitHub release after the workflow succeeds.

Use patch versions for fixes, minor versions for new features and major versions for breaking changes. Every tag must match `VERSION`.

GitHub Actions builds on branch pushes, pull requests and manual runs. The Windows package and checksum are available as a workflow artifact for 30 days. Matching version tags also create a draft release with those files. Reruns can replace draft assets; published releases require a new version. The workflow does not use a game ROM or personal files.

Actions use pinned commits. Dependabot checks those pins monthly. The workflow uses read-only permissions for builds and grants release writes only to the tag job.

## Source and dependencies

- `Source`: processor, editor, SoundFont engine and ROM worker interface.
- `tools/sf3bridge`: the bundled Go worker and adapted sf3-music converter.
- `tests`: processor checks, audio renderer and synthetic SoundFont generator.
- `docs/rom-address-audit.txt`: verified stock ROM addresses.

JUCE 8.0.15 is pinned to `91ad83ae34a81e0833b1a2b0866f54846370ae53`. TinySoundFont is pinned to `853a0a171759f1ddba0de1442133a75912bbeffa`. Preserve the dependency licences when updating either.

ROM conversion runs outside the audio callback. The converter's channel filter keeps SF3 text commands on their owning MIDI channel.

Extracted SoundFonts carry a version 1 `cps3` RIFF chunk: little-endian uint32 version and count, followed by 12-byte records (uint16 bank, uint16 program, uint8 low key, uint8 high key, uint16 reserved, float32 release seconds). These describe each patch's linear note-off release, derived from the stock decay-rate table and frame rate. The plugin applies them after note-off or pedal release and disables sample looping. Fonts without the chunk keep ordinary SoundFont behavior. Standard SF2 generators approximate the initial fade for other players. Attack, natural decay, LFO and voice allocation still differ from the game.

The project and adapted converter use AGPL-3.0. Third-party terms remain in their original licence files; see `THIRD_PARTY_NOTICES.md`.
