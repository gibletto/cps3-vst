cmake_minimum_required(VERSION 3.22)

file(STRINGS "${CMAKE_CURRENT_SOURCE_DIR}/VERSION" CPS3_VERSION LIMIT_COUNT 1)
if(NOT CPS3_VERSION MATCHES "^(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)$")
    message(FATAL_ERROR "VERSION must contain a version such as 0.1.1")
endif()

project(CPS3Instrument VERSION ${CPS3_VERSION} LANGUAGES C CXX)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set_property(DIRECTORY APPEND PROPERTY CMAKE_CONFIGURE_DEPENDS VERSION)
configure_file(Source/Version.h.in generated/cps3/Version.h @ONLY)

include(FetchContent)
set(CPS3_JUCE_COMMIT 91ad83ae34a81e0833b1a2b0866f54846370ae53)
if(EXISTS "${CMAKE_CURRENT_SOURCE_DIR}/external/JUCE/CMakeLists.txt")
    add_subdirectory(external/JUCE)
else()
    if(POLICY CMP0135)
        cmake_policy(SET CMP0135 NEW)
    endif()
    FetchContent_Declare(JUCE
        URL "https://github.com/juce-framework/JUCE/archive/${CPS3_JUCE_COMMIT}.tar.gz"
        URL_HASH SHA256=04f8d5055382582c757be9da069ea98338005f98248facd9c2804435ac853e70
    )
    FetchContent_MakeAvailable(JUCE)
endif()

get_target_property(CPS3_JUCE_SOURCE juce::juce_core INTERFACE_JUCE_MODULE_PATH)
get_filename_component(CPS3_JUCE_SOURCE "${CPS3_JUCE_SOURCE}/.." ABSOLUTE)
file(WRITE "${CMAKE_BINARY_DIR}/juce-source.txt" "${CPS3_JUCE_SOURCE}\n")

find_program(GO_EXECUTABLE go REQUIRED)
set(BRIDGE "${CMAKE_BINARY_DIR}/sf3bridge${CMAKE_EXECUTABLE_SUFFIX}")
file(GLOB_RECURSE BRIDGE_SOURCES CONFIGURE_DEPENDS tools/sf3bridge/*.go)
add_custom_command(
    OUTPUT "${BRIDGE}"
    COMMAND "${GO_EXECUTABLE}" build -trimpath "-ldflags=-s -w -X main.version=${PROJECT_VERSION}" -o "${BRIDGE}" .
    WORKING_DIRECTORY "${CMAKE_CURRENT_SOURCE_DIR}/tools/sf3bridge"
    DEPENDS ${BRIDGE_SOURCES} tools/sf3bridge/go.mod VERSION
    VERBATIM
)
add_custom_target(sf3bridge ALL DEPENDS "${BRIDGE}")
juce_add_binary_data(BridgeData SOURCES "${BRIDGE}")
add_dependencies(BridgeData sf3bridge)

juce_add_plugin(CPS3Instrument
    VERSION ${PROJECT_VERSION}
    COMPANY_NAME "CPS3 Community"
    BUNDLE_ID "community.cps3.instrument"
    PLUGIN_MANUFACTURER_CODE CpSc
    PLUGIN_CODE Cs3i
    PRODUCT_NAME "CPS3 Instrument"
    IS_SYNTH TRUE
    NEEDS_MIDI_INPUT TRUE
    NEEDS_MIDI_OUTPUT FALSE
    IS_MIDI_EFFECT FALSE
    EDITOR_WANTS_KEYBOARD_FOCUS FALSE
    COPY_PLUGIN_AFTER_BUILD FALSE
    FORMATS VST3 Standalone
)
if(WIN32)
    get_target_property(CPS3_GENERATED_SOURCE CPS3Instrument JUCE_GENERATED_SOURCES_DIRECTORY)
    get_target_property(CPS3_INFO_FILE CPS3Instrument JUCE_INFO_FILE)
    # JUCE's Windows resource generator omits the version metadata dependency.
    add_custom_command(
        OUTPUT "${CPS3_GENERATED_SOURCE}/CPS3Instrument_resources.rc"
        APPEND
        DEPENDS "${CPS3_INFO_FILE}" "${CMAKE_CURRENT_SOURCE_DIR}/VERSION"
    )
endif()
target_sources(CPS3Instrument PRIVATE
    Source/PluginProcessor.cpp
    Source/PluginProcessor.h
    Source/PluginEditor.cpp
    Source/PluginEditor.h
    Source/SoundFontEngine.cpp
    Source/SoundFontEngine.h
    Source/RomBridge.cpp
    Source/RomBridge.h
)
target_include_directories(CPS3Instrument
    PRIVATE external/TinySoundFont
    PUBLIC "${CMAKE_BINARY_DIR}/generated"
)
target_compile_options(CPS3Instrument PRIVATE $<$<CXX_COMPILER_ID:MSVC>:/utf-8>)
target_compile_definitions(CPS3Instrument PUBLIC
    JUCE_WEB_BROWSER=0
    JUCE_USE_CURL=0
    JUCE_VST3_CAN_REPLACE_VST2=0
    JUCE_PLUGINHOST_VST3=1
)
target_link_libraries(CPS3Instrument
    PRIVATE BridgeData juce::juce_audio_utils
    PUBLIC juce::juce_recommended_config_flags
)

option(CPS3_BUILD_TESTS "Build processor integration tests and renderer" ON)
if(CPS3_BUILD_TESTS)
    enable_testing()

    set(TEST_FONT "${CMAKE_BINARY_DIR}/test-font.sf2")
    add_custom_command(
        OUTPUT "${TEST_FONT}"
        COMMAND "${GO_EXECUTABLE}" run "${CMAKE_CURRENT_SOURCE_DIR}/tests/fixtures/soundfont.go" "${TEST_FONT}"
        WORKING_DIRECTORY "${CMAKE_CURRENT_SOURCE_DIR}/tools/sf3bridge"
        DEPENDS tests/fixtures/soundfont.go ${BRIDGE_SOURCES}
        VERBATIM
    )
    add_custom_target(CPS3TestFont DEPENDS "${TEST_FONT}")

    juce_add_console_app(CPS3Tests PRODUCT_NAME "CPS3 Tests")
    target_sources(CPS3Tests PRIVATE tests/ProcessorTests.cpp)
    target_include_directories(CPS3Tests PRIVATE Source)
    target_compile_definitions(CPS3Tests PRIVATE JUCE_MODAL_LOOPS_PERMITTED=1)
    target_link_libraries(CPS3Tests PRIVATE CPS3Instrument juce::juce_audio_utils)
    add_dependencies(CPS3Tests CPS3TestFont CPS3Instrument_VST3)

    juce_add_console_app(CPS3Render PRODUCT_NAME "CPS3 Render")
    target_sources(CPS3Render PRIVATE tests/SongRender.cpp)
    target_include_directories(CPS3Render PRIVATE Source)
    target_link_libraries(CPS3Render PRIVATE CPS3Instrument juce::juce_audio_utils)

    add_test(NAME processor COMMAND CPS3Tests "${TEST_FONT}")
    set_tests_properties(processor PROPERTIES
        ENVIRONMENT "CPS3_TEST_VST3=${CMAKE_BINARY_DIR}/CPS3Instrument_artefacts/$<CONFIG>/VST3/CPS3 Instrument.vst3"
    )
    add_test(NAME rom_bridge
        COMMAND "${GO_EXECUTABLE}" test ./...
        WORKING_DIRECTORY "${CMAKE_CURRENT_SOURCE_DIR}/tools/sf3bridge"
    )
endif()
