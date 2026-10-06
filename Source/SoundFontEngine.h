// SPDX-License-Identifier: AGPL-3.0-only

#pragma once
#include <juce_audio_processors/juce_audio_processors.h>
#include <array>
#include <memory>
#include <vector>

struct tsf;

struct Preset
{
    int bank;
    int program;
    juce::String name;
};

class SoundFontEngine
{
public:
    SoundFontEngine() = default;
    ~SoundFontEngine();
    bool load(const juce::File&, double sampleRate, juce::String& error);
    void prepare(double sampleRate);
    bool select(int channel, int bank, int program);
    void volume(int channel, int value);
    void pan(int channel, int value);
    void midi(const juce::MidiMessage&, bool followPrograms);
    void render(juce::AudioBuffer<float>&, int offset, int count);
    void panic();

    const std::vector<Preset>& presets() const
    {
        return presetList;
    }

private:
    void loadReleaseTimes(const juce::MemoryBlock&);
    void applyReleaseCurves();
    tsf* synth = nullptr;
    std::vector<Preset> presetList;
    std::vector<std::vector<float>> releaseTimes;
    std::array<bool, 16> valid{};
    std::array<int, 16> banks{};
    std::array<int, 16> programs{};
    std::array<float, 1024> scratch{};
};
