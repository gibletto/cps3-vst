// SPDX-License-Identifier: AGPL-3.0-only

#pragma once
#include "PluginProcessor.h"

class CPS3Editor final : public juce::AudioProcessorEditor,
                         public juce::FileDragAndDropTarget,
                         private juce::Timer
{
public:
    explicit CPS3Editor(CPS3Processor&);
    ~CPS3Editor() override;
    void paint(juce::Graphics&) override;
    void resized() override;
    bool isInterestedInFileDrag(const juce::StringArray&) override;
    void filesDropped(const juce::StringArray&, int, int) override;

private:
    struct Channel : juce::Component
    {
        Channel(CPS3Processor&, int);
        void paint(juce::Graphics&) override;
        void resized() override;
        void update(const std::vector<Preset>&);
        CPS3Processor& processor;
        int number;
        int currentBank = -1;
        int currentProgram = -1;
        juce::int64 presetSignature = 0;
        juce::Slider bank;
        juce::Slider volume;
        juce::Slider pan;
        juce::ComboBox program;
        std::unique_ptr<juce::AudioProcessorValueTreeState::SliderAttachment> bankAttachment;
        std::unique_ptr<juce::AudioProcessorValueTreeState::SliderAttachment> volumeAttachment;
        std::unique_ptr<juce::AudioProcessorValueTreeState::SliderAttachment> panAttachment;
    };

    void timerCallback() override;
    void choose(const juce::String& title, const juce::String& filter, bool save,
                std::function<void(juce::File)>, const juce::File& initial = {});
    void setParameter(const juce::String&, float);
    void updateTarget();
    CPS3Processor& processor;
    juce::LookAndFeel_V4 theme;
    std::array<std::unique_ptr<Channel>, 16> channels;
    juce::TextButton load{"Load SF2 / ROM"};
    juce::TextButton donor{"Choose donor"};
    juce::TextButton midi{"Choose MIDI"};
    juce::TextButton arm{"Arm capture"};
    juce::TextButton stop{"Stop"};
    juce::TextButton captureSource{"Use capture"};
    juce::TextButton saveMidi{"Save MIDI"};
    juce::TextButton analyze{"Analyze"};
    juce::TextButton exportRom{"Export ROM ZIP"};
    juce::TextButton panic{"All notes off"};
    juce::ToggleButton follow{"Follow MIDI bank/program"};
    juce::ToggleButton rackExport{"Use rack instruments for MIDI file"};
    juce::ToggleButton loop{"Loop"};
    juce::ToggleButton tempoOverride{"Override tempo"};
    juce::ComboBox target;
    juce::ComboBox round;
    juce::Slider loopStart;
    juce::Slider loopEnd;
    juce::Slider tempo;
    juce::Slider beats;
    juce::Label status;
    juce::Label source;
    juce::Label targetLabel;
    juce::Label loopStartLabel;
    juce::Label loopEndLabel;
    juce::Label tempoLabel;
    juce::Label beatsLabel;
    juce::Label roundLabel;
    std::vector<std::unique_ptr<juce::AudioProcessorValueTreeState::ButtonAttachment>> buttons;
    std::vector<std::unique_ptr<juce::AudioProcessorValueTreeState::SliderAttachment>> sliders;
    std::unique_ptr<juce::FileChooser> chooser;
    bool syncingTarget = false;
    JUCE_DECLARE_NON_COPYABLE_WITH_LEAK_DETECTOR(CPS3Editor)
};
