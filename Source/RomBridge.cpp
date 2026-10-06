// SPDX-License-Identifier: AGPL-3.0-only

#include "RomBridge.h"
#include <cps3/Version.h>
#include <BinaryData.h>
#include <cstring>

juce::File RomBridge::executable()
{
    auto dir = juce::File::getSpecialLocation(juce::File::userApplicationDataDirectory)
                   .getChildFile("CPS3Instrument")
                   .getChildFile("worker-" + juce::String(cps3::version));
    if (!dir.createDirectory())
    {
        return {};
    }
#if JUCE_WINDOWS
    auto file = dir.getChildFile("sf3bridge.exe");
#else
    auto file = dir.getChildFile("sf3bridge");
#endif
    int size = 0;
    auto* data = BinaryData::getNamedResource(BinaryData::namedResourceList[0], size);
    juce::MemoryBlock existing;
    // Reuse the worker only when its bytes match this build.
    if (file.loadFileAsData(existing) && existing.getSize() == static_cast<size_t>(size) &&
        std::memcmp(existing.getData(), data, size) == 0)
    {
        return file;
    }
    juce::TemporaryFile temp(file);
    if (!temp.getFile().replaceWithData(data, size) || !temp.overwriteTargetFileWithTemporary())
    {
        return {};
    }
#if !JUCE_WINDOWS
    file.setExecutePermission(true);
#endif
    return file;
}

juce::var RomBridge::run(const juce::var& request, juce::String& error)
{
    const auto exe = executable();
    if (!exe.existsAsFile())
    {
        error = "Cannot install the bundled ROM worker in application data";
        return {};
    }
    juce::TemporaryFile input(".json");
    if (!input.getFile().replaceWithText(juce::JSON::toString(request)))
    {
        error = "Cannot write worker request";
        return {};
    }
    juce::TemporaryFile responseFile(".json");
    juce::ChildProcess worker;
    // StringArray bypasses shell parsing, including for spaces in file paths.
    if (!worker.start(juce::StringArray{exe.getFullPathName(), input.getFile().getFullPathName(),
                                        responseFile.getFile().getFullPathName()},
                      0))
    {
        error = "Cannot launch ROM worker";
        return {};
    }
    if (!worker.waitForProcessToFinish(120000))
    {
        worker.kill();
        error = "ROM worker timed out after two minutes";
        return {};
    }
    auto response = responseFile.getFile().loadFileAsString();
    auto reply = juce::JSON::parse(response);
    if (!static_cast<bool>(reply["ok"]))
    {
        error = reply["error"].toString();
        if (error.isEmpty())
        {
            error = "ROM worker failed: " + response.substring(0, 500);
        }
    }
    return reply;
}
