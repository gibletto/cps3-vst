// SPDX-License-Identifier: AGPL-3.0-only

#pragma once
#include <juce_audio_processors/juce_audio_processors.h>

class RomBridge
{
public:
    static juce::File executable();
    static juce::var run(const juce::var& request, juce::String& error);
};
