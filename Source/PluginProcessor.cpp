// SPDX-License-Identifier: AGPL-3.0-only

#include "PluginProcessor.h"
#include "PluginEditor.h"
#include "RomBridge.h"

using namespace juce;

namespace
{
var object()
{
    return var(new DynamicObject());
}

void put(var& o, const Identifier& key, const var& value)
{
    o.getDynamicObject()->setProperty(key, value);
}

float value(AudioProcessorValueTreeState& p, const String& key)
{
    return p.getRawParameterValue(key)->load();
}
}

AudioProcessorValueTreeState::ParameterLayout CPS3Processor::layout()
{
    AudioProcessorValueTreeState::ParameterLayout result;
    for (int ch = 0; ch < 16; ++ch)
    {
        auto prefix = "Channel " + String(ch + 1) + " ";
        result.add(std::make_unique<AudioParameterInt>(ParameterID{id("bank", ch), 1},
                                                       prefix + "Bank", 0, 15, 0));
        result.add(std::make_unique<AudioParameterInt>(ParameterID{id("program", ch), 1},
                                                       prefix + "Program", 0, 127, 0));
        result.add(std::make_unique<AudioParameterInt>(ParameterID{id("volume", ch), 1},
                                                       prefix + "Volume", 0, 127, 80));
        result.add(std::make_unique<AudioParameterInt>(ParameterID{id("pan", ch), 1},
                                                       prefix + "Pan", 0, 127, 64));
    }
    result.add(std::make_unique<AudioParameterBool>(ParameterID{"follow", 1},
                                                    "Follow MIDI bank/program", true));
    result.add(std::make_unique<AudioParameterBool>(
        ParameterID{"rackExport", 1}, "Use rack instruments for MIDI file export", true));
    result.add(std::make_unique<AudioParameterInt>(ParameterID{"code", 1}, "Target sound code", 1,
                                                   49, 18));
    result.add(std::make_unique<AudioParameterBool>(ParameterID{"loop", 1}, "Loop song", true));
    result.add(std::make_unique<AudioParameterInt>(ParameterID{"loopStart", 1}, "Loop start bar", 1,
                                                   9999, 1));
    result.add(std::make_unique<AudioParameterInt>(
        ParameterID{"loopEnd", 1}, "Loop end bar (0 = MIDI markers/auto)", 0, 9999, 0));
    result.add(std::make_unique<AudioParameterInt>(ParameterID{"beats", 1}, "Quarter notes per bar",
                                                   1, 32, 4));
    result.add(std::make_unique<AudioParameterFloat>(
        ParameterID{"tempo", 1}, "Export tempo", NormalisableRange<float>(20, 400, 0.1f), 120.0f));
    result.add(std::make_unique<AudioParameterBool>(ParameterID{"tempoOverride", 1},
                                                    "Override MIDI tempo", false));
    return result;
}

CPS3Processor::CPS3Processor()
    : AudioProcessor(BusesProperties().withOutput("Stereo", AudioChannelSet::stereo(), true)),
      parameters(*this, nullptr, "CPS3State", layout()),
      queue(std::make_unique<CaptureEvent[]>(queueSize))
{
    const char* names[] = {"bank", "program", "volume", "pan"};
    for (int ch = 0; ch < 16; ++ch)
    {
        for (int p = 0; p < 4; ++p)
        {
            rack[ch][p] = parameters.getRawParameterValue(id(names[p], ch));
        }
        last[ch].fill(-1);
    }
    startTimerHz(20);
}

CPS3Processor::~CPS3Processor()
{
    stopTimer();
    if (job.joinable())
    {
        job.join();
    }
}

bool CPS3Processor::isBusesLayoutSupported(const BusesLayout& b) const
{
    return b.getMainOutputChannelSet() == AudioChannelSet::stereo() &&
           b.getMainInputChannelSet().isDisabled();
}

void CPS3Processor::prepareToPlay(double sampleRate, int)
{
    rate.store(sampleRate);
    const SpinLock::ScopedLockType lock(engineLock);
    if (engine)
    {
        engine->prepare(sampleRate);
        engine->panic();
    }
    for (int ch = 0; ch < 16; ++ch)
    {
        last[ch].fill(-1);
        channelConfigured[ch].store(false);
    }
    wasPlaying = false;
}

void CPS3Processor::releaseResources()
{
    panicRequested.store(true);
}

void CPS3Processor::pushCapture(const MidiMessage& message, double beat)
{
    if (message.getRawDataSize() > 8)
    {
        return;
    }
    int start1, size1, start2, size2;
    fifo.prepareToWrite(1, start1, size1, start2, size2);
    if (size1 + size2 == 0)
    {
        recording.store(0);
        captureError.store(1);
        return;
    }
    auto& event = queue[size1 ? start1 : start2];
    event.beat = jmax(0.0, beat);
    event.size = message.getRawDataSize();
    std::memcpy(event.bytes.data(), message.getRawData(), event.size);
    fifo.finishedWrite(1);
}

void CPS3Processor::processBlock(AudioBuffer<float>& buffer, MidiBuffer& midi)
{
    ScopedNoDenormals noDenormals;
    buffer.clear();
    const bool follow = value(parameters, "follow") > 0.5f;
    bool playing = false;
    double bpm = 120, beat = 0;
    int numerator = 4, denominator = 4;
    bool hasBeat = false;
    if (auto* playhead = getPlayHead())
    {
        if (auto pos = playhead->getPosition())
        {
            playing = pos->getIsPlaying();
            if (auto v = pos->getBpm())
            {
                bpm = *v;
            }
            if (auto v = pos->getPpqPosition())
            {
                beat = *v;
                hasBeat = true;
            }
            if (auto sig = pos->getTimeSignature())
            {
                numerator = sig->numerator;
                denominator = sig->denominator;
            }
        }
    }
    if (!std::isfinite(bpm) || bpm <= 0)
    {
        bpm = 120;
    }
    bool captureThisBlock = false;
    const SpinLock::ScopedTryLockType captureLock(captureGate);
    if (captureLock.isLocked())
    {
        if (recording.load() != 0 && playing && !hasBeat)
        {
            recording.store(0);
            captureError.store(3);
        }
        if (recording.load() == 1 && playing && hasBeat)
        {
            originBeat = previousBeat = beat;
            previousTempo = 0;
            previousNumerator = previousDenominator = 0;
            recording.store(2);
            for (int ch = 0; ch < 16; ++ch)
            {
                const bool configured = channelConfigured[ch].load();
                pushCapture(MidiMessage::controllerEvent(
                                ch + 1, 0,
                                follow && configured ? activeBanks[ch].load()
                                                     : static_cast<int>(rack[ch][0]->load())),
                            0);
                pushCapture(
                    MidiMessage::programChange(ch + 1, follow && configured
                                                           ? activePrograms[ch].load()
                                                           : static_cast<int>(rack[ch][1]->load())),
                    0);
                pushCapture(
                    MidiMessage::controllerEvent(ch + 1, 7, static_cast<int>(rack[ch][2]->load())),
                    0);
                pushCapture(
                    MidiMessage::controllerEvent(ch + 1, 10, static_cast<int>(rack[ch][3]->load())),
                    0);
            }
        }
        if (recording.load() == 2)
        {
            if (!playing)
            {
                recording.store(0);
            }
            else if (beat + 0.000001 < previousBeat)
            {
                recording.store(0);
                captureError.store(2);
            }
            else
            {
                captureThisBlock = true;
                if (bpm != previousTempo)
                {
                    pushCapture(MidiMessage::tempoMetaEvent(static_cast<int>(60000000.0 / bpm)),
                                beat - originBeat);
                    previousTempo = bpm;
                }
                if (numerator != previousNumerator || denominator != previousDenominator)
                {
                    pushCapture(MidiMessage::timeSignatureMetaEvent(numerator, denominator),
                                beat - originBeat);
                    previousNumerator = numerator;
                    previousDenominator = denominator;
                }
                previousBeat = beat;
                captureEnd.store(beat - originBeat +
                                 buffer.getNumSamples() * bpm / (60.0 * rate.load()));
            }
        }
    }
    const SpinLock::ScopedTryLockType synthLock(engineLock);
    const bool render = synthLock.isLocked() && engine != nullptr;
    if (render)
    {
        if (panicRequested.exchange(false) || (wasPlaying && !playing))
        {
            engine->panic();
        }
        for (int ch = 0; ch < 16; ++ch)
        {
            const int bank = static_cast<int>(rack[ch][0]->load()),
                      prog = static_cast<int>(rack[ch][1]->load());
            if (bank != last[ch][0] || prog != last[ch][1])
            {
                engine->select(ch, bank, prog);
                activeBanks[ch].store(bank);
                activePrograms[ch].store(prog);
                if (captureThisBlock)
                {
                    pushCapture(MidiMessage::controllerEvent(ch + 1, 0, bank), beat - originBeat);
                    pushCapture(MidiMessage::programChange(ch + 1, prog), beat - originBeat);
                }
                last[ch][0] = bank;
                last[ch][1] = prog;
                channelConfigured[ch].store(true);
            }
            int volume = static_cast<int>(rack[ch][2]->load()),
                pan = static_cast<int>(rack[ch][3]->load());
            if (volume != last[ch][2])
            {
                engine->volume(ch, volume);
                last[ch][2] = volume;
                if (captureThisBlock)
                {
                    pushCapture(MidiMessage::controllerEvent(ch + 1, 7, volume), beat - originBeat);
                }
            }
            if (pan != last[ch][3])
            {
                engine->pan(ch, pan);
                last[ch][3] = pan;
                if (captureThisBlock)
                {
                    pushCapture(MidiMessage::controllerEvent(ch + 1, 10, pan), beat - originBeat);
                }
            }
        }
    }
    wasPlaying = playing;
    int cursor = 0;
    for (const auto metadata : midi)
    {
        auto msg = metadata.getMessage();
        int offset = jlimit(0, buffer.getNumSamples(), metadata.samplePosition);
        if (render && offset > cursor)
        {
            engine->render(buffer, cursor, offset - cursor);
        }
        cursor = offset;
        if (captureThisBlock && msg.isForChannel(msg.getChannel()) && msg.getChannel() > 0 &&
            msg.getChannel() <= 16)
        {
            const bool program =
                msg.isProgramChange() || (msg.isController() && (msg.getControllerNumber() == 0 ||
                                                                 msg.getControllerNumber() == 32));
            if (!program || follow)
            {
                pushCapture(msg, beat - originBeat + offset * bpm / (60.0 * rate.load()));
            }
        }
        if (msg.isNoteOn() && msg.getChannel() > 0)
        {
            channelActivity[msg.getChannel() - 1].store(8);
        }
        if (render)
        {
            engine->midi(msg, follow);
        }
        if (follow && msg.getChannel() > 0 && msg.getChannel() <= 16)
        {
            int ch = msg.getChannel() - 1;
            if (msg.isProgramChange())
            {
                activePrograms[ch].store(msg.getProgramChangeNumber());
            }
            if (msg.isController() && msg.getControllerNumber() == 0)
            {
                activeBanks[ch].store(msg.getControllerValue());
            }
        }
    }
    if (render && cursor < buffer.getNumSamples())
    {
        engine->render(buffer, cursor, buffer.getNumSamples() - cursor);
    }
    midi.clear();
}

void CPS3Processor::setStatus(const String& text)
{
    const ScopedLock lock(stateMutex);
    statusText = text;
}

String CPS3Processor::status() const
{
    const ScopedLock lock(stateMutex);
    return statusText;
}

File CPS3Processor::fontFile() const
{
    const ScopedLock lock(stateMutex);
    return sf2Path;
}

File CPS3Processor::donorFile() const
{
    const ScopedLock lock(stateMutex);
    return donorPath;
}

File CPS3Processor::midiFile() const
{
    const ScopedLock lock(stateMutex);
    return midiPath;
}

std::vector<Preset> CPS3Processor::presets() const
{
    const ScopedLock lock(stateMutex);
    return presetList;
}

void CPS3Processor::launch(std::function<void()> work)
{
    bool expected = false;
    if (!busy.compare_exchange_strong(expected, true))
    {
        setStatus("Please wait for the current operation to finish");
        return;
    }
    if (job.joinable())
    {
        job.join();
    }
    job = std::thread(
        [this, work = std::move(work)]
        {
            try
            {
                work();
            }
            catch (const std::exception& e)
            {
                setStatus(String("Operation failed: ") + e.what());
            }
            busy.store(false);
        });
}

void CPS3Processor::loadFontWorker(const File& file)
{
    auto fresh = std::make_unique<SoundFontEngine>();
    String error;
    if (!fresh->load(file, rate.load(), error))
    {
        setStatus(error);
        return;
    }
    auto list = fresh->presets();
    {
        const SpinLock::ScopedLockType lock(engineLock);
        fresh->prepare(rate.load());
        engine.swap(fresh);
        for (int ch = 0; ch < 16; ++ch)
        {
            last[ch].fill(-1);
            channelConfigured[ch].store(false);
        }
    } // destroy the old synth on the worker thread
    {
        const ScopedLock lock(stateMutex);
        sf2Path = file;
        presetList = std::move(list);
        statusText =
            "Loaded " + file.getFileName() + " - " + String(presetList.size()) + " presets";
    }
}

void CPS3Processor::setDonor(const File& file)
{
    const ScopedLock lock(stateMutex);
    donorPath = file;
    statusText = "Donor selected: " + file.getFileName() + ". Analyze checks it before export.";
}

void CPS3Processor::loadFile(const File& file)
{
    if (file.hasFileExtension("sf2"))
    {
        setStatus("Loading SoundFont...");
        launch(
            [this, file]
            {
                loadFontWorker(file);
            });
    }
    else if (file.hasFileExtension("zip"))
    {
        setStatus("Extracting instruments from ROM...");
        launch(
            [this, file]
            {
                auto out = File::getSpecialLocation(File::userApplicationDataDirectory)
                               .getChildFile("CPS3Instrument")
                               .getChildFile("soundfonts")
                               .getChildFile(Uuid().toString());
                auto req = object();
                put(req, "action", "extract");
                put(req, "donor", file.getFullPathName());
                put(req, "output", out.getFullPathName());
                String error;
                auto reply = RomBridge::run(req, error);
                if (error.isNotEmpty())
                {
                    setStatus(error);
                    return;
                }
                {
                    const ScopedLock lock(stateMutex);
                    donorPath = file;
                }
                loadFontWorker(File(reply["soundFont"].toString()));
            });
    }
    else if (file.hasFileExtension("mid;midi"))
    {
        FileInputStream stream(file);
        MidiFile data;
        if (!stream.openedOk() || !data.readFrom(stream) || data.getTimeFormat() <= 0)
        {
            setStatus("Choose a valid MIDI file with musical ticks (SMPTE timing is unsupported)");
            return;
        }
        const ScopedLock lock(stateMutex);
        midiPath = file;
        statusText = "MIDI source: " + file.getFileName();
    }
    else
    {
        setStatus("Supported files: .sf2, .mid/.midi, and sfiii3nr1.zip");
    }
}

void CPS3Processor::startCapture()
{
    const SpinLock::ScopedLockType lock(captureGate);
    recording.store(0);
    fifo.reset();
    capture.clear();
    capturedCount.store(0);
    captureEnd.store(0);
    captureError.store(0);
    reportedCaptureError = 0;
    recording.store(1);
    setStatus("Capture armed. Start DAW playback; route each part to its own MIDI channel.");
}

void CPS3Processor::stopCapture()
{
    {
        const SpinLock::ScopedLockType lock(captureGate);
        recording.store(0);
    }
    drainCapture();
    setStatus("Capture stopped - " + String(capturedCount.load()) +
              " events. Use Capture as source or save MIDI.");
}

void CPS3Processor::drainCapture()
{
    int start1, size1, start2, size2;
    fifo.prepareToRead(fifo.getNumReady(), start1, size1, start2, size2);
    auto copy = [this](int start, int count)
    {
        for (int i = 0; i < count; ++i)
        {
            const auto& e = queue[start + i];
            capture.addEvent(MidiMessage(e.bytes.data(), e.size, e.beat * 960));
        }
    };
    copy(start1, size1);
    copy(start2, size2);
    fifo.finishedRead(size1 + size2);
    capturedCount.store(capture.getNumEvents());
}

bool CPS3Processor::saveCapture(const File& file, String& error)
{
    if (recording.load() != 0)
    {
        error = "Stop capture before saving or exporting";
        return false;
    }
    drainCapture();
    if (capture.getNumEvents() == 0)
    {
        error = "No captured MIDI yet";
        return false;
    }
    if (captureError.load() != 0)
    {
        error = "Capture is incomplete. Record again with DAW looping disabled.";
        return false;
    }
    MidiMessageSequence song(capture);
    std::array<std::array<int, 128>, 16> held{};
    bool hasNotes = false;
    for (int i = 0; i < song.getNumEvents(); ++i)
    {
        auto& m = song.getEventPointer(i)->message;
        if (m.isNoteOn())
        {
            ++held[m.getChannel() - 1][m.getNoteNumber()];
            hasNotes = true;
        }
        else if (m.isNoteOff())
        {
            auto& n = held[m.getChannel() - 1][m.getNoteNumber()];
            n = jmax(0, n - 1);
        }
    }
    if (!hasNotes)
    {
        error = "Capture contains no notes";
        return false;
    }
    const double end = jmax(captureEnd.load() * 960, song.getEndTime() + 1);
    for (int ch = 0; ch < 16; ++ch)
    {
        for (int key = 0; key < 128; ++key)
        {
            for (int n = 0; n < held[ch][key]; ++n)
            {
                auto off = MidiMessage::noteOff(ch + 1, key);
                off.setTimeStamp(end);
                song.addEvent(off);
            }
        }
    }
    MidiFile data;
    data.setTicksPerQuarterNote(960);
    data.addTrack(song);
    TemporaryFile temp(file);
    {
        FileOutputStream out(temp.getFile());
        if (!out.openedOk() || !data.writeTo(out))
        {
            error = "Cannot write captured MIDI";
            return false;
        }
    }
    if (!temp.overwriteTargetFileWithTemporary())
    {
        error = "Cannot save captured MIDI";
        return false;
    }
    return true;
}

var CPS3Processor::makeRequest(const String& action, const File& output)
{
    auto req = object(), song = object();
    Array<var> parts;
    File source = midiFile();
    if (!source.existsAsFile())
    {
        setStatus("Choose a MIDI file, or use Capture as source after recording");
        return {};
    }
    const bool useRack = value(parameters, "rackExport") > 0.5f;
    for (int ch = 0; ch < (useRack ? 16 : 1); ++ch)
    {
        auto part = object();
        put(part, "midi", source.getFullPathName());
        if (useRack)
        {
            put(part, "channel", ch + 1);
            put(part, "bank", static_cast<int>(rack[ch][0]->load()));
            put(part, "program", static_cast<int>(rack[ch][1]->load()));
        }
        parts.add(part);
    }
    put(song, "parts", parts);
    put(song, "noLoop", value(parameters, "loop") < 0.5f);
    // End=0 preserves markers and the importer's automatic loop length.
    if (value(parameters, "loopEnd") > 0)
    {
        put(song, "loopStartBar", static_cast<int>(value(parameters, "loopStart")));
        put(song, "loopEndBar", static_cast<int>(value(parameters, "loopEnd")));
    }
    put(song, "beatsPerBar", static_cast<int>(value(parameters, "beats")));
    if (value(parameters, "tempoOverride") > 0.5f)
    {
        put(song, "tempo", value(parameters, "tempo"));
    }
    put(req, "song", song);
    put(req, "action", action);
    put(req, "code", static_cast<int>(value(parameters, "code")));
    put(req, "fallbackTempo", value(parameters, "tempo"));
    put(req, "donor", donorFile().getFullPathName());
    put(req, "output", output.getFullPathName());
    return req;
}

void CPS3Processor::romJob(const String& action, const File& output)
{
    if (recording.load() != 0)
    {
        setStatus("Stop capture before analyzing or exporting");
        return;
    }
    if (!donorFile().existsAsFile())
    {
        setStatus("Choose your sfiii3nr1.zip donor first");
        return;
    }
    auto request = makeRequest(action, output);
    if (request.isVoid())
    {
        return;
    }
    setStatus(action == "analyze" ? "Checking tracks, instruments and ROM space..."
                                  : "Writing a new ROM ZIP...");
    launch(
        [this, request, action]
        {
            String error;
            auto reply = RomBridge::run(request, error);
            if (error.isNotEmpty())
            {
                setStatus(error);
                return;
            }
            String message = String(action == "analyze" ? "Ready: " : "Exported: ") +
                             reply["tracks"].toString() + "/16 game tracks, " +
                             reply["bytes"].toString() + " bytes, " + reply["tempo"].toString() +
                             " BPM";
            if (auto* warnings = reply["warnings"].getArray())
            {
                for (auto& w : *warnings)
                {
                    message += "\n" + w.toString();
                }
            }
            if (action == "export")
            {
                message += "\n" + reply["output"].toString();
            }
            setStatus(message);
        });
}

void CPS3Processor::analyze()
{
    romJob("analyze", {});
}

void CPS3Processor::exportROM(const File& file)
{
    romJob("export", file);
}

void CPS3Processor::timerCallback()
{
    drainCapture();
    for (auto& meter : channelActivity)
    {
        int n = meter.load();
        if (n > 0)
        {
            meter.compare_exchange_strong(n, n - 1);
        }
    }
    const int error = captureError.load();
    if (error != reportedCaptureError)
    {
        if (error == 1)
        {
            setStatus(
                "Capture buffer overflowed. Record again; no partial capture can be exported.");
        }
        else if (error == 2)
        {
            setStatus("Capture stopped on a DAW loop/seek. Record again with looping disabled.");
        }
        else if (error == 3)
        {
            setStatus("Host provides no beat position. Use a MIDI file exported from the DAW.");
        }
        reportedCaptureError = error;
    }
    File pending;
    {
        const ScopedLock lock(stateMutex);
        if (!busy.load())
        {
            pending = restoreFont;
            restoreFont = {};
        }
    }
    if (pending != File())
    {
        if (pending.existsAsFile())
        {
            loadFile(pending);
        }
        else
        {
            setStatus("Saved SoundFont is missing: " + pending.getFullPathName() +
                      ". Load it again.");
        }
    }
}

void CPS3Processor::getStateInformation(MemoryBlock& dest)
{
    auto state = parameters.copyState();
    state.setProperty("sf2Path", fontFile().getFullPathName(), nullptr);
    state.setProperty("donorPath", donorFile().getFullPathName(), nullptr);
    state.setProperty("midiPath", midiFile().getFullPathName(), nullptr);
    if (auto xml = state.createXml())
    {
        copyXmlToBinary(*xml, dest);
    }
}

void CPS3Processor::setStateInformation(const void* data, int size)
{
    if (auto xml = getXmlFromBinary(data, size))
    {
        auto state = ValueTree::fromXml(*xml);
        if (!state.hasType("CPS3State"))
        {
            return;
        }
        parameters.replaceState(state);
        const ScopedLock lock(stateMutex);
        donorPath = File(state["donorPath"].toString());
        midiPath = File(state["midiPath"].toString());
        restoreFont = File(state["sf2Path"].toString());
    }
}

AudioProcessorEditor* CPS3Processor::createEditor()
{
    return new CPS3Editor(*this);
}

AudioProcessor* JUCE_CALLTYPE createPluginFilter()
{
    return new CPS3Processor();
}
