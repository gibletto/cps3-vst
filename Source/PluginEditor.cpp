// SPDX-License-Identifier: AGPL-3.0-only

#include "PluginEditor.h"
#include <cps3/Version.h>

using namespace juce;

namespace
{
const Colour bg(0xff111820), panel(0xff1b2531), ink(0xffe7eef4), muted(0xff91a4b5),
    accent(0xff64dec2);
const char* stages[] = {"Alex / Ken", "Necro / Twelve", "Hugo",   "Chun-Li",
                        "Ryu",        "Ibuki",          "Makoto", "Akuma / Shin Akuma",
                        "Elena",      "Oro / Sean",     "Dudley", "Yun / Yang",
                        "Remy",       "Urien",          "Gill",   "Q",
                        "Stage 21",   "Stage 22"};
const int stageCodes[] = {10, 12, 14, 16, 18, 20, 22, 24, 26, 28, 30, 32, 34, 36, 38, 40, 42, 43};
const char* scenes[] = {"Opening",
                        "Player select / continue",
                        "Next opponent / bonus",
                        "Win / lose",
                        "New challenger",
                        "Scene effect",
                        "Ranking",
                        "Continue",
                        "Game over",
                        "Endings A / Stage 22 parity 1",
                        "Endings B",
                        "Endings C",
                        "Game over 2",
                        "Capcom logo",
                        "Appear"};
const int sceneCodes[] = {1, 2, 3, 4, 5, 6, 7, 8, 9, 44, 45, 46, 47, 48, 49};

void numberSlider(Slider& s, double lo, double hi, double step = 1)
{
    s.setRange(lo, hi, step);
    s.setSliderStyle(Slider::IncDecButtons);
    s.setTextBoxStyle(Slider::TextBoxLeft, false, 45, 26);
}

void setParam(CPS3Processor& p, const String& name, float v)
{
    if (auto* parameter = p.parameters.getParameter(name))
    {
        parameter->beginChangeGesture();
        parameter->setValueNotifyingHost(parameter->convertTo0to1(v));
        parameter->endChangeGesture();
    }
}
}

CPS3Editor::Channel::Channel(CPS3Processor& p, int n) : processor(p), number(n)
{
    numberSlider(bank, 0, 15);
    numberSlider(volume, 0, 127);
    numberSlider(pan, 0, 127);
    bank.setTooltip("CPS3 bank 0-15. Banks 8-14 are empty in Third Strike.");
    volume.setTooltip("Channel volume / CC7");
    pan.setTooltip("Channel pan / CC10: 0 left, 64 center, 127 right");
    program.setTooltip(
        "Zero-based CPS3 program. Incoming program changes apply when Follow MIDI is enabled.");
    for (auto* c : std::initializer_list<Component*>{&bank, &volume, &pan, &program})
    {
        addAndMakeVisible(c);
    }
    bankAttachment = std::make_unique<AudioProcessorValueTreeState::SliderAttachment>(
        p.parameters, CPS3Processor::id("bank", n), bank);
    volumeAttachment = std::make_unique<AudioProcessorValueTreeState::SliderAttachment>(
        p.parameters, CPS3Processor::id("volume", n), volume);
    panAttachment = std::make_unique<AudioProcessorValueTreeState::SliderAttachment>(
        p.parameters, CPS3Processor::id("pan", n), pan);
    program.onChange = [this]
    {
        if (program.getSelectedId() > 0)
        {
            setParam(processor, CPS3Processor::id("program", number),
                     static_cast<float>(program.getSelectedId() - 1));
        }
    };
}

void CPS3Editor::Channel::paint(Graphics& g)
{
    g.setColour(processor.activity(number) > 0 ? accent : muted);
    g.fillEllipse(8, 20, 6, 6);
    g.setFont(15);
    g.drawText(String(number + 1), 17, 8, 24, 30, Justification::centred);
}

void CPS3Editor::Channel::resized()
{
    auto r = getLocalBounds().reduced(0, 8);
    r.removeFromLeft(44);
    bank.setBounds(r.removeFromLeft(78).reduced(3, 0));
    program.setBounds(r.removeFromLeft(238).reduced(3, 0));
    volume.setBounds(r.removeFromLeft(82).reduced(3, 0));
    pan.setBounds(r.reduced(3, 0));
}

void CPS3Editor::Channel::update(const std::vector<Preset>& presets)
{
    int b = static_cast<int>(bank.getValue());
    int p = static_cast<int>(
        processor.parameters.getRawParameterValue(CPS3Processor::id("program", number))->load());
    juce::int64 signature = 0;
    for (const auto& preset : presets)
    {
        if (preset.bank == b)
        {
            signature ^= preset.name.hashCode64() + preset.program * 131;
        }
    }
    if (b != currentBank || signature != presetSignature || p != currentProgram)
    {
        program.clear(dontSendNotification);
        for (const auto& preset : presets)
        {
            if (preset.bank == b && preset.program >= 0 && preset.program <= 127)
            {
                program.addItem(String(preset.program) + " - " + preset.name, preset.program + 1);
            }
        }
        bool present = false;
        for (const auto& preset : presets)
        {
            if (preset.bank == b && preset.program == p)
            {
                present = true;
            }
        }
        if (!present)
        {
            program.addItem(
                String(p) + (presets.empty() ? " - load a SoundFont" : " - unavailable"), p + 1);
        }
        program.setSelectedId(p + 1, dontSendNotification);
        currentBank = b;
        currentProgram = p;
        presetSignature = signature;
    }
    program.setTooltip("Rack: b" + String(b) + "p" + String(p) + ". Active MIDI instrument: b" +
                       String(processor.liveBank(number)) + "p" +
                       String(processor.liveProgram(number)));
    repaint();
}

CPS3Editor::CPS3Editor(CPS3Processor& p) : AudioProcessorEditor(p), processor(p)
{
    theme.setColour(ResizableWindow::backgroundColourId, bg);
    theme.setColour(TextButton::buttonColourId, Colour(0xff2b3b4b));
    theme.setColour(TextButton::textColourOffId, ink);
    theme.setColour(Slider::textBoxTextColourId, ink);
    theme.setColour(Slider::textBoxBackgroundColourId, bg);
    theme.setColour(ComboBox::backgroundColourId, bg);
    theme.setColour(ComboBox::textColourId, ink);
    theme.setColour(Label::textColourId, muted);
    theme.setColour(ToggleButton::textColourId, ink);
    theme.setColour(ToggleButton::tickColourId, accent);
    setLookAndFeel(&theme);
    for (int ch = 0; ch < 16; ++ch)
    {
        channels[ch] = std::make_unique<Channel>(p, ch);
        addAndMakeVisible(channels[ch].get());
    }
    for (auto* c : std::initializer_list<Component*>{
             &load,         &donor,         &midi,       &arm,       &stop,        &captureSource,
             &saveMidi,     &analyze,       &exportRom,  &panic,     &follow,      &rackExport,
             &loop,         &tempoOverride, &target,     &round,     &loopStart,   &loopEnd,
             &tempo,        &beats,         &status,     &source,    &targetLabel, &loopStartLabel,
             &loopEndLabel, &tempoLabel,    &beatsLabel, &roundLabel})
    {
        addAndMakeVisible(c);
    }
    status.setFont(Font(FontOptions(14)));
    status.setJustificationType(Justification::topLeft);
    status.setColour(Label::textColourId, ink);
    source.setJustificationType(Justification::topLeft);
    targetLabel.setText("Replace music", dontSendNotification);
    roundLabel.setText("Round variant", dontSendNotification);
    loopStartLabel.setText("Start bar", dontSendNotification);
    loopEndLabel.setText("End bar (0 = auto)", dontSendNotification);
    tempoLabel.setText("BPM", dontSendNotification);
    beatsLabel.setText("Quarter notes / bar", dontSendNotification);
    numberSlider(loopStart, 1, 9999);
    numberSlider(loopEnd, 0, 9999);
    numberSlider(tempo, 20, 400, 0.1);
    numberSlider(beats, 1, 32);
    loopEnd.setTooltip(
        "End bar is exclusive. 1 to 9 loops eight bars; 0 uses MIDI markers or rounds to a bar.");
    beats.setTooltip("Quarter notes in a bar: use 3 for 3/4, 3 for 6/8, 4 for 4/4. Applies to "
                     "manual loop boundaries.");
    for (int i = 0; i < 18; ++i)
    {
        target.addItem(String(stages[i]) + " stage", 100 + i);
    }
    target.addSeparator();
    for (int i = 0; i < 15; ++i)
    {
        target.addItem(String(sceneCodes[i]) + " - " + scenes[i], sceneCodes[i]);
    }
    round.addItem("Parity 0 - first theme", 1);
    round.addItem("Parity 1 - alternate", 2);
    round.setTooltip("Alternates between the game's two round themes. Shared music slots also "
                     "affect other stages or scenes.");
    target.onChange = [this]
    {
        updateTarget();
    };
    round.onChange = [this]
    {
        updateTarget();
    };
    auto buttonAttachment = [this](const char* id, ToggleButton& b)
    {
        buttons.push_back(std::make_unique<AudioProcessorValueTreeState::ButtonAttachment>(
            processor.parameters, id, b));
    };
    buttonAttachment("follow", follow);
    buttonAttachment("rackExport", rackExport);
    buttonAttachment("loop", loop);
    buttonAttachment("tempoOverride", tempoOverride);
    auto sliderAttachment = [this](const char* id, Slider& s)
    {
        sliders.push_back(std::make_unique<AudioProcessorValueTreeState::SliderAttachment>(
            processor.parameters, id, s));
    };
    sliderAttachment("loopStart", loopStart);
    sliderAttachment("loopEnd", loopEnd);
    sliderAttachment("tempo", tempo);
    sliderAttachment("beats", beats);
    load.onClick = [this]
    {
        choose("Load SoundFont or extract your ROM", "*.sf2;*.zip", false,
               [this](File f)
               {
                   processor.loadFile(f);
               });
    };
    donor.onClick = [this]
    {
        choose("Choose stock sfiii3nr1.zip donor", "*.zip", false,
               [this](File f)
               {
                   processor.setDonor(f);
               });
    };
    midi.onClick = [this]
    {
        choose("Choose exported multi-track MIDI", "*.mid;*.midi", false,
               [this](File f)
               {
                   processor.loadFile(f);
               });
    };
    arm.onClick = [this]
    {
        processor.startCapture();
    };
    stop.onClick = [this]
    {
        processor.stopCapture();
    };
    saveMidi.onClick = [this]
    {
        choose("Save captured MIDI", "*.mid", true,
               [this](File f)
               {
                   String error;
                   if (processor.saveCapture(f.withFileExtension("mid"), error))
                   {
                       processor.setStatus("Saved captured MIDI");
                   }
                   else
                   {
                       processor.setStatus(error);
                   }
               });
    };
    captureSource.onClick = [this]
    {
        auto dir = File::getSpecialLocation(File::userApplicationDataDirectory)
                       .getChildFile("CPS3Instrument")
                       .getChildFile("captures");
        if (!dir.createDirectory())
        {
            processor.setStatus("Cannot create capture directory");
            return;
        }
        auto file = dir.getChildFile(Uuid().toString() + ".mid");
        String error;
        if (processor.saveCapture(file, error))
        {
            processor.loadFile(file);
            setParameter("rackExport", 0); // retain captured program changes and mix
            processor.setStatus(
                "Capture is the export source. Its recorded instrument changes are preserved.");
        }
        else
        {
            processor.setStatus(error);
        }
    };
    analyze.onClick = [this]
    {
        processor.analyze();
    };
    exportRom.onClick = [this]
    {
        choose(
            "Export sfiii3nr1.zip into a different folder from the donor", "*.zip", true,
            [this](File f)
            {
                processor.exportROM(f.withFileExtension("zip"));
            },
            File::getSpecialLocation(File::userDocumentsDirectory).getChildFile("sfiii3nr1.zip"));
    };
    panic.onClick = [this]
    {
        processor.panic();
    };
    exportRom.setColour(TextButton::buttonColourId, Colour(0xff236758));
    setSize(1120, 850);
    timerCallback();
    startTimerHz(15);
}

CPS3Editor::~CPS3Editor()
{
    stopTimer();
    setLookAndFeel(nullptr);
}

void CPS3Editor::setParameter(const String& name, float value)
{
    setParam(processor, name, value);
}

void CPS3Editor::updateTarget()
{
    if (syncingTarget)
    {
        return;
    }
    int selected = target.getSelectedId();
    if (selected >= 100 && selected < 118)
    {
        setParameter("code", static_cast<float>(stageCodes[selected - 100] +
                                                jmax(0, round.getSelectedId() - 1)));
    }
    else if (selected > 0)
    {
        setParameter("code", static_cast<float>(selected));
    }
}

void CPS3Editor::choose(const String& title, const String& filter, bool save,
                        std::function<void(File)> done, const File& initial)
{
    chooser = std::make_unique<FileChooser>(title, initial, filter);
    Component::SafePointer<CPS3Editor> safe(this);
    int flags = save ? FileBrowserComponent::saveMode : FileBrowserComponent::openMode;
    flags |= FileBrowserComponent::canSelectFiles;
    chooser->launchAsync(flags,
                         [safe, done](const FileChooser& fc)
                         {
                             if (safe && fc.getResult() != File())
                             {
                                 done(fc.getResult());
                             }
                         });
}

bool CPS3Editor::isInterestedInFileDrag(const StringArray& files)
{
    for (auto& path : files)
    {
        if (File(path).hasFileExtension("sf2;zip;mid;midi"))
        {
            return true;
        }
    }
    return false;
}

void CPS3Editor::filesDropped(const StringArray& files, int, int)
{
    // Load one asset per drop: extraction/loading is serialized per plugin instance.
    if (!files.isEmpty())
    {
        processor.loadFile(File(files[0]));
    }
}

void CPS3Editor::timerCallback()
{
    auto list = processor.presets();
    for (auto& ch : channels)
    {
        ch->update(list);
    }
    status.setText(processor.status(), dontSendNotification);
    source.setText("SoundFont: " + processor.fontFile().getFileName() +
                       "    Donor: " + processor.donorFile().getFileName() +
                       "\nMIDI: " + processor.midiFile().getFileName() +
                       "    Capture: " + String(processor.eventCount()) + " events" +
                       (processor.captureState() == 2   ? " - recording"
                        : processor.captureState() == 1 ? " - armed"
                                                        : ""),
                   dontSendNotification);
    bool idle = !processor.isBusy();
    for (auto* b : {&load, &donor, &midi, &analyze, &exportRom})
    {
        b->setEnabled(idle);
    }
    arm.setEnabled(processor.captureState() == 0);
    stop.setEnabled(processor.captureState() != 0);
    captureSource.setEnabled(processor.captureState() == 0 && processor.eventCount() > 0);
    saveMidi.setEnabled(processor.captureState() == 0 && processor.eventCount() > 0);
    const int code = static_cast<int>(processor.parameters.getRawParameterValue("code")->load());
    int selected = target.getSelectedId();
    bool matches = selected >= 100 && selected < 118 &&
                   stageCodes[selected - 100] + jmax(0, round.getSelectedId() - 1) == code;
    if (!matches && selected != code)
    {
        syncingTarget = true;
        if (code >= 10 && code <= 43)
        {
            int i = (code - 10) / 2;
            target.setSelectedId(100 + i, dontSendNotification);
            round.setSelectedId(code - stageCodes[i] + 1, dontSendNotification);
        }
        else
        {
            target.setSelectedId(code, dontSendNotification);
        }
        syncingTarget = false;
    }
    round.setEnabled(target.getSelectedId() >= 100);
    loopStart.setEnabled(loop.getToggleState() && loopEnd.getValue() > 0);
    loopEnd.setEnabled(loop.getToggleState());
    tempo.setTooltip(
        "Used if the MIDI has no tempo. Enable Override tempo to replace an existing tempo map.");
}

void CPS3Editor::paint(Graphics& g)
{
    g.fillAll(bg);
    g.setColour(accent);
    g.setFont(Font(FontOptions(28).withStyle("Bold")));
    g.drawText("CPS3 INSTRUMENT", 24, 18, 420, 40, Justification::centredLeft);
    g.setColour(muted);
    g.setFont(14);
    g.drawText("v" + String(cps3::version), 936, 29, 160, 22, Justification::centredRight);
    g.drawText("Third Strike - 16-channel SoundFont rack + ROM song export", 24, 58, 700, 22,
               Justification::centredLeft);
    for (int col = 0; col < 2; ++col)
    {
        int x = 20 + col * 550;
        g.setColour(panel);
        g.fillRoundedRectangle(static_cast<float>(x), 139, 540, 390, 8);
        g.setColour(muted);
        g.setFont(12);
        g.drawText("CH", x + 15, 147, 32, 20, Justification::centredLeft);
        g.drawText("BANK", x + 50, 147, 74, 20, Justification::centredLeft);
        g.drawText("PROGRAM", x + 129, 147, 248, 20, Justification::centredLeft);
        g.drawText("LEVEL", x + 367, 147, 80, 20, Justification::centredLeft);
        g.drawText("PAN", x + 449, 147, 80, 20, Justification::centredLeft);
    }
    g.setColour(muted);
    g.setFont(12);
    g.drawText("Preview allows chords. ROM export needs at most 16 monophonic tracks in total; "
               "Analyze checks this.",
               24, 533, 1080, 20, Justification::centredLeft);
    g.drawText("SoundFont preview uses extracted samples; CPS3 envelopes/LFOs are played by the "
               "game after export.",
               24, 814, 1080, 20, Justification::centredLeft);
}

void CPS3Editor::resized()
{
    load.setBounds(24, 94, 170, 32);
    donor.setBounds(204, 94, 140, 32);
    midi.setBounds(354, 94, 140, 32);
    follow.setBounds(514, 94, 280, 32);
    panic.setBounds(910, 94, 186, 32);
    for (int ch = 0; ch < 16; ++ch)
    {
        channels[ch]->setBounds(20 + (ch / 8) * 550, 172 + (ch % 8) * 44, 540, 44);
    }
    arm.setBounds(24, 562, 135, 32);
    stop.setBounds(169, 562, 80, 32);
    captureSource.setBounds(259, 562, 130, 32);
    saveMidi.setBounds(399, 562, 110, 32);
    rackExport.setBounds(539, 562, 365, 32);
    targetLabel.setBounds(24, 607, 130, 24);
    target.setBounds(154, 607, 310, 28);
    roundLabel.setBounds(484, 607, 110, 24);
    round.setBounds(599, 607, 240, 28);
    loop.setBounds(24, 644, 70, 28);
    loopStartLabel.setBounds(94, 644, 70, 28);
    loopStart.setBounds(167, 644, 91, 28);
    loopEndLabel.setBounds(272, 644, 130, 28);
    loopEnd.setBounds(405, 644, 95, 28);
    tempoOverride.setBounds(514, 644, 160, 28);
    tempoLabel.setBounds(674, 644, 40, 28);
    tempo.setBounds(717, 644, 95, 28);
    beatsLabel.setBounds(825, 644, 165, 28);
    beats.setBounds(993, 644, 100, 28);
    source.setBounds(24, 681, 780, 48);
    analyze.setBounds(826, 686, 100, 32);
    exportRom.setBounds(936, 686, 160, 32);
    status.setBounds(24, 738, 1072, 73);
}
