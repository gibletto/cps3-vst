// SPDX-License-Identifier: AGPL-3.0-only

#include "SoundFontEngine.h"
#define TSF_IMPLEMENTATION
#include "tsf.h"
#include <cmath>
#include <cstring>

SoundFontEngine::~SoundFontEngine()
{
    if (synth)
    {
        tsf_close(synth);
    }
}

bool SoundFontEngine::load(const juce::File& file, double rate, juce::String& error)
{
    if (file.getSize() < 12 || file.getSize() > 256 * 1024 * 1024)
    {
        error = "SoundFont must be a valid .sf2 file smaller than 256 MB";
        return false;
    }
    juce::MemoryBlock bytes;
    if (!file.loadFileAsData(bytes))
    {
        error = "Could not read SoundFont";
        return false;
    }
    auto* fresh = tsf_load_memory(bytes.getData(), static_cast<int>(bytes.getSize()));
    if (!fresh)
    {
        error = "Could not decode this SoundFont (.sf2)";
        return false;
    }
    if (!tsf_set_max_voices(fresh, 256))
    {
        tsf_close(fresh);
        error = "Could not allocate synth voices";
        return false;
    }
    if (synth)
    {
        tsf_close(synth);
    }
    synth = fresh;
    loadReleaseTimes(bytes);
    prepare(rate);
    presetList.clear();
    for (int i = 0; i < synth->presetNum; ++i)
    {
        presetList.push_back({synth->presets[i].bank, synth->presets[i].preset,
                              juce::String::fromUTF8(synth->presets[i].presetName, 20).trim()});
    }
    for (int ch = 0; ch < 16; ++ch)
    {
        tsf_channel_set_presetindex(synth, ch, 0); // allocate all channels off the audio thread
        tsf_channel_set_pitchrange(synth, ch, 2.0f);
        select(ch, 0, 0);
    }
    return true;
}

void SoundFontEngine::prepare(double rate)
{
    if (synth)
    {
        tsf_set_output(synth, TSF_STEREO_INTERLEAVED, static_cast<int>(rate), -6.0f);
    }
}

bool SoundFontEngine::select(int ch, int bank, int prog)
{
    banks[ch] = bank;
    programs[ch] = prog;
    valid[ch] = synth && tsf_channel_set_bank_preset(synth, ch, bank, prog) != 0;
    return valid[ch];
}

void SoundFontEngine::volume(int ch, int v)
{
    if (synth)
    {
        tsf_channel_midi_control(synth, ch, 7, v);
    }
}

void SoundFontEngine::pan(int ch, int v)
{
    if (synth)
    {
        tsf_channel_midi_control(synth, ch, 10, v);
    }
}

void SoundFontEngine::midi(const juce::MidiMessage& msg, bool follow)
{
    if (!synth || msg.getChannel() < 1 || msg.getChannel() > 16)
    {
        return;
    }
    int ch = msg.getChannel() - 1;
    if (msg.isNoteOn())
    {
        if (valid[ch])
        {
            tsf_channel_note_on(synth, ch, msg.getNoteNumber(), msg.getFloatVelocity());
        }
    }
    else if (msg.isNoteOff())
    {
        tsf_channel_note_off(synth, ch, msg.getNoteNumber());
        applyReleaseCurves();
    }
    else if (msg.isPitchWheel())
    {
        tsf_channel_set_pitchwheel(synth, ch, msg.getPitchWheelValue());
    }
    else if (msg.isProgramChange())
    {
        if (follow)
        {
            select(ch, banks[ch], msg.getProgramChangeNumber());
        }
    }
    else if (msg.isController())
    {
        int cc = msg.getControllerNumber(), v = msg.getControllerValue();
        if (cc == 0)
        {
            if (follow)
            {
                select(ch, v, programs[ch]);
            }
        }
        else if (cc != 32)
        {
            tsf_channel_midi_control(synth, ch, cc, v);
            if (cc == 64 || cc == 123 || cc == 121)
            {
                applyReleaseCurves();
            }
        }
    }
}

void SoundFontEngine::loadReleaseTimes(const juce::MemoryBlock& bytes)
{
    releaseTimes.clear();
    const auto* data = static_cast<const uint8_t*>(bytes.getData());
    const size_t size = bytes.getSize();
    // Only fonts explicitly carrying our extension receive CPS3 release behavior.
    for (size_t pos = 12; pos + 8 <= size;)
    {
        const auto length = static_cast<size_t>(juce::ByteOrder::littleEndianInt(data + pos + 4));
        if (length > size - pos - 8)
        {
            return;
        }
        if (std::memcmp(data + pos, "cps3", 4) == 0)
        {
            const auto* chunk = data + pos + 8;
            if (length < 8 || juce::ByteOrder::littleEndianInt(chunk) != 1)
            {
                return;
            }
            const auto count = static_cast<size_t>(juce::ByteOrder::littleEndianInt(chunk + 4));
            if (count > 16384 || count != (length - 8) / 12 || (length - 8) % 12 != 0)
            {
                return;
            }
            releaseTimes.resize(static_cast<size_t>(synth->presetNum));
            for (int p = 0; p < synth->presetNum; ++p)
            {
                releaseTimes[static_cast<size_t>(p)].resize(
                    static_cast<size_t>(synth->presets[p].regionNum), 0.0f);
            }
            for (size_t i = 0; i < count; ++i)
            {
                const auto* record = chunk + 8 + 12 * i;
                const int bank = juce::ByteOrder::littleEndianShort(record),
                          program = juce::ByteOrder::littleEndianShort(record + 2);
                const int low = record[4], high = record[5];
                const uint32_t bits = juce::ByteOrder::littleEndianInt(record + 8);
                float seconds;
                std::memcpy(&seconds, &bits, sizeof(seconds));
                if (low > high || high > 127 || !std::isfinite(seconds) || seconds <= 0 ||
                    seconds > 600)
                {
                    continue;
                }
                const int p = tsf_get_presetindex(synth, bank, program);
                if (p < 0)
                {
                    continue;
                }
                for (int r = 0; r < synth->presets[p].regionNum; ++r)
                {
                    const auto& region = synth->presets[p].regions[r];
                    if (region.lokey == low && region.hikey == high)
                    {
                        releaseTimes[static_cast<size_t>(p)][static_cast<size_t>(r)] = seconds;
                    }
                }
            }
            return;
        }
        pos += 8 + length + (length & 1);
    }
}

void SoundFontEngine::applyReleaseCurves()
{
    if (releaseTimes.empty())
    {
        return;
    }
    for (int i = 0; i < synth->voiceNum; ++i)
    {
        auto& voice = synth->voices[i];
        auto& envelope = voice.ampenv;
        if (voice.playingPreset < 0 || envelope.segment != TSF_SEGMENT_RELEASE ||
            !envelope.segmentIsExponential || envelope.parameters.release == 0)
        {
            continue;
        }
        const auto region = voice.region - synth->presets[voice.playingPreset].regions;
        const float seconds =
            releaseTimes[static_cast<size_t>(voice.playingPreset)][static_cast<size_t>(region)];
        if (seconds <= 0)
        {
            continue;
        }
        envelope.samplesUntilNextSegment =
            juce::jmax(1, static_cast<int>(std::ceil(seconds * synth->outSampleRate)));
        envelope.slope = -envelope.level / envelope.samplesUntilNextSegment;
        envelope.segmentIsExponential = TSF_FALSE;
        // The original driver also disables sample looping when a note ends.
        voice.loopEnd = voice.loopStart;
    }
}

void SoundFontEngine::render(juce::AudioBuffer<float>& out, int offset, int count)
{
    if (!synth || out.getNumChannels() < 2)
    {
        return;
    }
    while (count > 0)
    {
        const int n = juce::jmin(count, 512);
        tsf_render_float(synth, scratch.data(), n, 0);
        for (int i = 0; i < n; ++i)
        {
            out.setSample(0, offset + i, scratch[2 * i]);
            out.setSample(1, offset + i, scratch[2 * i + 1]);
        }
        count -= n;
        offset += n;
    }
}

void SoundFontEngine::panic()
{
    if (synth)
    {
        for (int ch = 0; ch < 16; ++ch)
        {
            tsf_channel_sounds_off_all(synth, ch);
        }
    }
}
