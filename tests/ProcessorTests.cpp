// SPDX-License-Identifier: AGPL-3.0-only

#include "PluginProcessor.h"
#include "RomBridge.h"
#include "SoundFontEngine.h"
#include <cps3/Version.h>
#include <cstring>
#include <iostream>
#include <stdexcept>

using namespace juce;

namespace
{
void check(bool condition, const char* message)
{
    if (!condition)
    {
        throw std::runtime_error(message);
    }
}

void param(CPS3Processor& p, const String& id, float n)
{
    auto* value = p.parameters.getParameter(id);
    value->setValueNotifyingHost(value->convertTo0to1(n));
}

void wait(CPS3Processor& p)
{
    for (int n = 0; n < 3000 && p.isBusy(); ++n)
    {
        Thread::sleep(10);
    }
    check(!p.isBusy(), "worker timed out");
}

struct Playhead : AudioPlayHead
{
    bool playing = true;
    double beat = 0, bpm = 120;

    Optional<PositionInfo> getPosition() const override
    {
        PositionInfo result;
        result.setIsPlaying(playing);
        result.setPpqPosition(beat);
        result.setBpm(bpm);
        result.setTimeSignature(TimeSignature{4, 4});
        return result;
    }
};

void checkRelease(const File& font)
{
    SoundFontEngine held, released;
    String error;
    check(held.load(font, 48000, error) && released.load(font, 48000, error),
          "release test font load failed");
    held.select(0, 0, 7);
    released.select(0, 0, 7); // opening-song vocal slices
    const auto on = MidiMessage::noteOn(1, 62, static_cast<uint8>(100));
    held.midi(on, true);
    released.midi(on, true);
    AudioBuffer<float> a(2, 4800), b(2, 4800);
    held.render(a, 0, 4800);
    released.render(b, 0, 4800);
    released.midi(MidiMessage::noteOff(1, 62), true);
    held.render(a, 0, 1008);
    released.render(b, 0, 1008);
    const float ratio = b.getRMSLevel(0, 768, 240) / a.getRMSLevel(0, 768, 240);
    std::cout << "Vocal level after 20 ms gap: " << ratio << " of held note" << std::endl;
    check(ratio > 0.86f && ratio < 0.93f, "vocal release creates a dip across a 20 ms note gap");
    released.render(b, 0, 4800);
    released.render(b, 0, 4800);
    check(b.getMagnitude(0, 3600, 1200) == 0, "linear vocal release failed to finish");

    // Sustain defers the release; a second note-off must not restart a voice's fade.
    released.panic();
    held.panic();
    released.render(b, 0, 4800);
    held.render(a, 0, 4800);
    held.midi(on, true);
    released.midi(on, true);
    released.midi(MidiMessage::controllerEvent(1, 64, 127), true);
    held.render(a, 0, 4800);
    released.render(b, 0, 4800);
    released.midi(MidiMessage::noteOff(1, 62), true);
    held.render(a, 0, 1008);
    released.render(b, 0, 1008);
    check(std::abs(b.getRMSLevel(0, 0, 1008) / a.getRMSLevel(0, 0, 1008) - 1) < 0.001f,
          "sustain pedal released too early");
    released.midi(MidiMessage::controllerEvent(1, 64, 0), true);
    held.render(a, 0, 1008);
    released.render(b, 0, 1008);
    check(b.getRMSLevel(0, 768, 240) / a.getRMSLevel(0, 768, 240) > 0.86f,
          "pedal release uses wrong fade");
    released.midi(MidiMessage::noteOff(1, 62), true);
    released.render(b, 0, 4800);
    released.render(b, 0, 4800);
    check(b.getMagnitude(0, 3600, 1200) == 0, "repeat note-off restarted release");
    released.midi(on, true);
    released.render(b, 0, 1008);
    released.panic();
    released.render(b, 0, 1008);
    check(b.getMagnitude(0, 600, 408) == 0, "CPS3 envelope changed panic fade");

    // Removing the extension must retain ordinary SoundFont envelope semantics.
    MemoryBlock bytes;
    check(font.loadFileAsData(bytes), "cannot read extension test font");
    auto* data = static_cast<uint8_t*>(bytes.getData());
    for (size_t pos = 12; pos + 8 <= bytes.getSize();)
    {
        const size_t length = ByteOrder::littleEndianInt(data + pos + 4);
        check(length <= bytes.getSize() - pos - 8, "invalid test font chunk");
        if (std::memcmp(data + pos, "cps3", 4) == 0)
        {
            bytes.removeSection(pos, 8 + length + (length & 1));
            data = static_cast<uint8_t*>(bytes.getData());
            const uint32_t size =
                ByteOrder::swapIfBigEndian(static_cast<uint32_t>(bytes.getSize() - 8));
            std::memcpy(data + 4, &size, 4);
            break;
        }
        pos += 8 + length + (length & 1);
    }
    TemporaryFile generic(".sf2");
    check(generic.getFile().replaceWithData(bytes.getData(), bytes.getSize()),
          "cannot write generic test font");
    check(released.load(generic.getFile(), 48000, error), "generic SoundFont failed to load");
    released.select(0, 0, 7);
    released.midi(on, true);
    released.render(b, 0, 4800);
    released.midi(MidiMessage::noteOff(1, 82), true);
    released.render(b, 0, 4800);
    released.render(b, 0, 4800);
    check(b.getMagnitude(0, 2000, 2800) > 0.00001f, "ordinary SoundFont release was overridden");
}
}

int main(int argc, char** argv)
{
    ScopedJuceInitialiser_GUI juce;
    try
    {
        CPS3Processor processor;
        processor.prepareToPlay(48000, 512);
        check(processor.acceptsMidi() && !processor.producesMidi(), "instrument MIDI contract");
        AudioBuffer<float> buffer(2, 512);
        MidiBuffer midi;
        midi.addEvent(MidiMessage::noteOn(16, 60, static_cast<uint8>(100)), 0);
        processor.processBlock(buffer, midi);
        check(buffer.getMagnitude(0, 512) == 0, "unloaded synth must be silent");
        check(midi.isEmpty(), "instrument consumes MIDI");
        param(processor, "bank16", 15);
        param(processor, "program16", 27);
        param(processor, "code", 19);
        MemoryBlock state;
        processor.getStateInformation(state);
        CPS3Processor restored;
        restored.setStateInformation(state.getData(), static_cast<int>(state.getSize()));
        check(restored.parameters.getRawParameterValue("bank16")->load() == 15, "bank state lost");
        check(restored.parameters.getRawParameterValue("program16")->load() == 27,
              "program state lost");
        check(restored.parameters.getRawParameterValue("code")->load() == 19,
              "round slot state lost");

        Playhead playhead;
        processor.setPlayHead(&playhead);
        processor.startCapture();
        midi.addEvent(MidiMessage::noteOn(1, 60, static_cast<uint8>(100)), 240);
        midi.addEvent(MidiMessage::noteOn(16, 72, static_cast<uint8>(80)), 480);
        processor.processBlock(buffer, midi);
        playhead.beat = 1;
        playhead.bpm = 90;
        midi.addEvent(MidiMessage::noteOff(1, 60), 120);
        processor.processBlock(buffer, midi);
        processor.stopCapture();
        TemporaryFile recorded(".mid");
        String error;
        check(processor.saveCapture(recorded.getFile(), error), "capture failed to save");
        FileInputStream stream(recorded.getFile());
        MidiFile song;
        check(song.readFrom(stream) && song.getTimeFormat() == 960, "capture MIDI format");
        bool channel16 = false, correctOffset = false, closedNote = false;
        int tempoChanges = 0;
        for (int i = 0; i < song.getTrack(0)->getNumEvents(); ++i)
        {
            auto m = song.getTrack(0)->getEventPointer(i)->message;
            if (m.isTempoMetaEvent())
            {
                ++tempoChanges;
            }
            if (m.isNoteOn() && m.getChannel() == 16)
            {
                channel16 = true;
            }
            if (m.isNoteOn() && m.getChannel() == 1 && std::abs(m.getTimeStamp() - 9.6) < 1.1)
            {
                correctOffset = true;
            }
            if (m.isNoteOff() && m.getChannel() == 16)
            {
                closedNote = true;
            }
        }
        check(channel16 && correctOffset && closedNote && tempoChanges == 2,
              "capture timing/channel/tempo/note closure");
        processor.startCapture();
        playhead.beat = 4;
        processor.processBlock(buffer, midi);
        playhead.beat = 0;
        processor.processBlock(buffer, midi);
        processor.stopCapture();
        check(!processor.saveCapture(recorded.getFile(), error),
              "looped/incomplete recording accepted");
        processor.setPlayHead(nullptr);

        if (argc > 1)
        {
            std::cout << "Checking SoundFont audio..." << std::endl;
            processor.loadFile(File(String::fromUTF8(argv[1])));
            wait(processor);
            check(processor.presets().size() >= 3, "SoundFont presets not loaded");
            checkRelease(File(String::fromUTF8(argv[1])));
            for (int ch = 0; ch < 16; ++ch)
            {
                param(processor, CPS3Processor::id("bank", ch), 0);
                param(processor, CPS3Processor::id("program", ch), 10);
            }
            processor.panic();
            midi.addEvent(MidiMessage::noteOn(1, 60, static_cast<uint8>(100)), 200);
            processor.processBlock(buffer, midi);
            check(buffer.getMagnitude(0, 0, 200) == 0, "MIDI rendered before sample offset");
            check(buffer.getMagnitude(0, 200, 312) > 0.00001f, "note did not render");
            processor.panic();
            processor.processBlock(buffer, midi);
            processor.processBlock(
                buffer, midi); // TinySoundFont uses a 10 ms anti-click fade for all-sound-off
            check(buffer.getMagnitude(0, 512) == 0, "panic did not silence synth");
            param(processor, "bank16", 8);
            midi.addEvent(MidiMessage::noteOn(16, 60, static_cast<uint8>(100)), 0);
            processor.processBlock(buffer, midi);
            check(buffer.getMagnitude(0, 512) == 0,
                  "empty bank silently fell back to a different preset");
            param(processor, "bank16", 0);
            midi.addEvent(MidiMessage::noteOn(16, 60, static_cast<uint8>(100)), 0);
            processor.processBlock(buffer, midi);
            check(buffer.getMagnitude(0, 512) > 0.00001f, "channel 16 did not play");
            std::cout << "Rendering editor..." << std::endl;
            auto editor = std::unique_ptr<AudioProcessorEditor>(processor.createEditor());
            auto image = editor->createComponentSnapshot(editor->getLocalBounds());
            auto target = File::getCurrentWorkingDirectory().getChildFile("editor-preview.png");
            FileOutputStream png(target);
            PNGImageFormat().writeImageToStream(image, png);
        }
        const File pluginFolder(argc > 3
                                    ? String::fromUTF8(argv[3])
                                    : SystemStats::getEnvironmentVariable("CPS3_TEST_VST3", {}));
        if (pluginFolder != File())
        {
            AudioPluginFormatManager formats;
            addDefaultFormatsToManager(formats);
            OwnedArray<PluginDescription> descriptions;
            for (auto* format : formats.getFormats())
            {
                if (format->getName() == "VST3")
                {
                    format->findAllTypesForFile(descriptions, pluginFolder.getFullPathName());
                }
            }
            check(descriptions.size() == 1 && descriptions[0]->isInstrument,
                  "built VST3 failed host scan");
            check(descriptions[0]->version == cps3::version, "VST3 metadata version differs");
            String error;
            auto hosted = formats.createPluginInstance(*descriptions[0], 48000, 512, error);
            check(hosted != nullptr, "VST3 failed host instantiation");
            MemoryBlock loadedState;
            processor.getStateInformation(loadedState);
            XmlElement vstState("VST3PluginState");
            vstState.createNewChildElement("IComponent")
                ->addTextElement(loadedState.toBase64Encoding());
            MemoryBlock vstBytes;
            AudioProcessor::copyXmlToBinary(vstState, vstBytes);
            hosted->setStateInformation(vstBytes.getData(), static_cast<int>(vstBytes.getSize()));
            hosted->prepareToPlay(48000, 512);
            MessageManager::getInstance()->runDispatchLoopUntil(750);
            Thread::sleep(250);
            midi.addEvent(MidiMessage::noteOn(16, 60, static_cast<uint8>(100)), 0);
            hosted->processBlock(buffer, midi);
            check(buffer.getMagnitude(0, 512) > 0.00001f,
                  "hosted VST3 did not restore SoundFont/play channel 16");
            auto editor = std::unique_ptr<AudioProcessorEditor>(hosted->createEditorIfNeeded());
            check(editor && editor->getWidth() == 1120, "hosted VST3 editor failed");
            editor.reset();
            hosted->releaseResources();
        }
        if (argc > 2)
        {
            std::cout << "Checking embedded worker..." << std::endl;
            auto* req = new DynamicObject();
            req->setProperty("action", "extract");
            req->setProperty("donor", String::fromUTF8(argv[2]));
            auto output = File::getSpecialLocation(File::tempDirectory)
                              .getChildFile("cps3-test-" + Uuid().toString());
            req->setProperty("output", output.getFullPathName());
            String error;
            auto result = RomBridge::run(var(req), error);
            check(error.isEmpty() && static_cast<bool>(result["ok"]),
                  "bundled worker IPC/extraction failed");
            check(result["version"].toString() == cps3::version,
                  "worker and plugin versions differ");
            check(File(result["soundFont"].toString()).existsAsFile(),
                  "worker did not produce SoundFont");
            checkRelease(File(result["soundFont"].toString()));
            output.deleteRecursively(); // explicitly created temporary test directory only
            processor.setDonor(File(String::fromUTF8(argv[2])));
            processor.loadFile(recorded.getFile());
            processor.analyze();
            wait(processor);
            check(processor.status().startsWith("Ready:"), "plugin MIDI analysis path failed");
            auto exportDir = File::getSpecialLocation(File::tempDirectory)
                                 .getChildFile("cps3-export-test-" + Uuid().toString());
            check(exportDir.createDirectory(), "cannot create export test folder");
            auto exportFile = exportDir.getChildFile("sfiii3nr1.zip");
            processor.exportROM(exportFile);
            wait(processor);
            check(processor.status().startsWith("Exported:") && exportFile.getSize() > 1000,
                  "plugin MIDI-to-ROM export path failed");
            check(!exportFile.withFileExtension("dat").exists(),
                  "export unexpectedly produced a sidecar");
            exportDir.deleteRecursively(); // explicitly created temporary test directory only
        }
        std::cout
            << "PASS: MIDI routing, state, capture timing, tempo, note closure, loop rejection";
        if (argc > 1)
        {
            std::cout << ", SoundFont audio, sample offsets, panic, empty bank, channel 16, editor "
                         "render";
        }
        if (argc > 2)
        {
            std::cout << ", embedded worker extraction/analysis/ROM export";
        }
        if (pluginFolder != File())
        {
            std::cout << ", VST3 host scan/instantiate/state/audio/editor";
        }
        std::cout << std::endl;
        return 0;
    }
    catch (const std::exception& error)
    {
        std::cerr << "FAIL: " << error.what() << std::endl;
        return 1;
    }
}
