// SPDX-License-Identifier: AGPL-3.0-only

#pragma once
#include "SoundFontEngine.h"
#include <atomic>
#include <thread>

class CPS3Processor final : public juce::AudioProcessor, private juce::Timer
{
public:
    CPS3Processor();
    ~CPS3Processor() override;
    void prepareToPlay(double, int) override;
    void releaseResources() override;
    void processBlock(juce::AudioBuffer<float>&, juce::MidiBuffer&) override;
    bool isBusesLayoutSupported(const BusesLayout&) const override;
    juce::AudioProcessorEditor* createEditor() override;

    bool hasEditor() const override
    {
        return true;
    }

    const juce::String getName() const override
    {
        return "CPS3 Instrument";
    }

    bool acceptsMidi() const override
    {
        return true;
    }

    bool producesMidi() const override
    {
        return false;
    }

    bool isMidiEffect() const override
    {
        return false;
    }

    double getTailLengthSeconds() const override
    {
        return 0.5;
    }

    int getNumPrograms() override
    {
        return 1;
    }

    int getCurrentProgram() override
    {
        return 0;
    }

    void setCurrentProgram(int) override
    {
    }

    const juce::String getProgramName(int) override
    {
        return "Default";
    }

    void changeProgramName(int, const juce::String&) override
    {
    }

    void getStateInformation(juce::MemoryBlock&) override;
    void setStateInformation(const void*, int) override;

    juce::AudioProcessorValueTreeState parameters;

    static juce::String id(const char* name, int ch)
    {
        return juce::String(name) + juce::String(ch + 1);
    }

    void loadFile(const juce::File&); // .sf2, supported donor .zip, or .mid
    void setDonor(const juce::File&);
    void startCapture();
    void stopCapture();
    bool saveCapture(const juce::File&, juce::String& error);
    void analyze();
    void exportROM(const juce::File&);

    void panic()
    {
        panicRequested.store(true);
    }

    juce::String status() const;
    juce::File fontFile() const;
    juce::File donorFile() const;
    juce::File midiFile() const;
    std::vector<Preset> presets() const;

    bool isBusy() const
    {
        return busy.load();
    }

    int captureState() const
    {
        return recording.load();
    } // 0 off, 1 armed, 2 recording

    int eventCount() const
    {
        return capturedCount.load();
    }

    int activity(int ch) const
    {
        return channelActivity[ch].load();
    }

    int liveBank(int ch) const
    {
        return activeBanks[ch].load();
    }

    int liveProgram(int ch) const
    {
        return activePrograms[ch].load();
    }

    void setStatus(const juce::String&);

private:
    static juce::AudioProcessorValueTreeState::ParameterLayout layout();
    void timerCallback() override;
    void launch(std::function<void()>);
    void loadFontWorker(const juce::File&);
    juce::var makeRequest(const juce::String&, const juce::File& output);
    void romJob(const juce::String&, const juce::File& output);
    void pushCapture(const juce::MidiMessage&, double beat);
    void drainCapture();
    mutable juce::CriticalSection stateMutex;
    juce::String statusText = "Drop your extracted .sf2 or sfiii3nr1.zip to begin";
    juce::File sf2Path;
    juce::File donorPath;
    juce::File midiPath;
    std::vector<Preset> presetList;
    juce::SpinLock engineLock;
    std::unique_ptr<SoundFontEngine> engine;
    std::thread job;
    std::atomic<bool> busy{false};
    std::atomic<bool> panicRequested{false};
    std::atomic<double> rate{44100.0};
    std::array<std::array<std::atomic<float>*, 4>, 16> rack{};
    std::array<std::array<int, 4>, 16> last{};
    std::array<std::atomic<int>, 16> channelActivity{};
    std::array<std::atomic<int>, 16> activeBanks{};
    std::array<std::atomic<int>, 16> activePrograms{};
    std::array<std::atomic<bool>, 16> channelConfigured{};

    struct CaptureEvent
    {
        double beat;
        std::array<juce::uint8, 8> bytes;
        int size;
    };

    static constexpr int queueSize = 65536;
    std::unique_ptr<CaptureEvent[]> queue;
    juce::AbstractFifo fifo{queueSize};
    std::atomic<int> recording{0};
    std::atomic<int> capturedCount{0};
    std::atomic<int> captureError{0};
    std::atomic<double> captureEnd{0};
    juce::SpinLock captureGate;
    double originBeat = 0;
    double previousBeat = 0;
    double previousTempo = 0;
    int previousNumerator = 0;
    int previousDenominator = 0;
    int reportedCaptureError = 0;
    bool wasPlaying = false;
    juce::MidiMessageSequence capture;
    juce::File restoreFont;
    JUCE_DECLARE_NON_COPYABLE_WITH_LEAK_DETECTOR(CPS3Processor)
};
