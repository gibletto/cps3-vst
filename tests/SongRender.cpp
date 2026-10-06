// SPDX-License-Identifier: AGPL-3.0-only

#include "PluginProcessor.h"
#include <juce_audio_formats/juce_audio_formats.h>
#include <iostream>
#include <stdexcept>

using namespace juce;

namespace
{
struct RenderPlayhead : AudioPlayHead
{
    double beat = 0, bpm = 120;

    Optional<PositionInfo> getPosition() const override
    {
        PositionInfo position;
        position.setIsPlaying(true);
        position.setPpqPosition(beat);
        position.setBpm(bpm);
        return position;
    }
};
}

// Offline diagnostic: render the same MIDI through the processor at the file's tempo or a DAW tempo.
int main(int argc, char** argv)
{
    ScopedJuceInitialiser_GUI gui;
    try
    {
        if (argc < 4)
        {
            throw std::runtime_error(
                "Usage: CPS3Render font.sf2 song.mid output.wav [BPM override]");
        }
        FileInputStream input(File(String::fromUTF8(argv[2])));
        MidiFile file;
        if (!file.readFrom(input) || file.getTimeFormat() <= 0)
        {
            throw std::runtime_error("Cannot read MIDI");
        }
        const double ppq = file.getTimeFormat();
        const double tempo = argc > 4 ? String::fromUTF8(argv[4]).getDoubleValue() : 0;
        if (argc > 4 && (tempo <= 0 || tempo > 1000))
        {
            throw std::runtime_error("Invalid tempo");
        }
        if (tempo == 0)
        {
            file.convertTimestampTicksToSeconds();
        }
        MidiMessageSequence events;
        for (int track = 0; track < file.getNumTracks(); ++track)
        {
            auto sequence = *file.getTrack(track);
            if (tempo > 0)
            {
                for (int i = 0; i < sequence.getNumEvents(); ++i)
                {
                    auto& message = sequence.getEventPointer(i)->message;
                    message.setTimeStamp(message.getTimeStamp() * 60 / (ppq * tempo));
                }
            }
            events.addSequence(sequence, 0);
        }
        events.sort();
        constexpr int sampleRate = 48000, blockSize = 512;
        CPS3Processor processor;
        processor.prepareToPlay(sampleRate, blockSize);
        processor.loadFile(File(String::fromUTF8(argv[1])));
        for (int i = 0; i < 3000 && processor.isBusy(); ++i)
        {
            Thread::sleep(10);
        }
        if (processor.isBusy() || processor.presets().empty())
        {
            throw std::runtime_error("Cannot load SoundFont");
        }
        RenderPlayhead playhead;
        playhead.bpm = tempo > 0 ? tempo : 120;
        processor.setPlayHead(&playhead);
        File output(String::fromUTF8(argv[3]));
        if (output.exists())
        {
            throw std::runtime_error("Output already exists");
        }
        std::unique_ptr<OutputStream> stream = output.createOutputStream();
        WavAudioFormat format;
        auto writer = format.createWriterFor(
            stream, AudioFormatWriterOptions()
                        .withSampleRate(sampleRate)
                        .withNumChannels(2)
                        .withBitsPerSample(32)
                        .withSampleFormat(AudioFormatWriterOptions::SampleFormat::floatingPoint));
        if (!writer)
        {
            throw std::runtime_error("Cannot create WAV");
        }
        Array<var> noteTimes;
        const int total = static_cast<int>(std::ceil((events.getEndTime() + 1) * sampleRate));
        int event = 0;
        for (int position = 0; position < total; position += blockSize)
        {
            const int count = jmin(blockSize, total - position);
            AudioBuffer<float> audio(2, count);
            MidiBuffer midi;
            while (event < events.getNumEvents())
            {
                const auto message = events.getEventPointer(event)->message;
                const int offset =
                    static_cast<int>(std::llround(message.getTimeStamp() * sampleRate)) - position;
                if (offset >= count)
                {
                    break;
                }
                if (!message.isMetaEvent())
                {
                    midi.addEvent(message, jmax(0, offset));
                }
                if (message.isNoteOn() || message.isNoteOff())
                {
                    auto* note = new DynamicObject();
                    note->setProperty("time", message.getTimeStamp());
                    note->setProperty("on", message.isNoteOn());
                    note->setProperty("channel", message.getChannel());
                    note->setProperty("key", message.getNoteNumber());
                    noteTimes.add(var(note));
                }
                ++event;
            }
            playhead.beat = position * playhead.bpm / (60.0 * sampleRate);
            processor.processBlock(audio, midi);
            if (!writer->writeFromAudioSampleBuffer(audio, 0, count))
            {
                throw std::runtime_error("Cannot write audio");
            }
        }
        output.withFileExtension("json").replaceWithText(JSON::toString(var(noteTimes)));
        std::cout << "Rendered " << total / double(sampleRate) << " seconds to "
                  << output.getFullPathName() << std::endl;
    }
    catch (const std::exception& e)
    {
        std::cerr << e.what() << std::endl;
        return 1;
    }
}
