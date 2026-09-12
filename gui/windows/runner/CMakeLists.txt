cmake_minimum_required(VERSION 3.14)
project(runner LANGUAGES CXX)

# Define the application target. To change its name, change BINARY_NAME in the
# top-level CMakeLists.txt, not the value here, or `flutter run` will no longer
# work.
#
# Any new source files that you add to the application should be added here.
add_executable(${BINARY_NAME} WIN32
  "flutter_window.cpp"
  "main.cpp"
  "utils.cpp"
  "win32_window.cpp"
  "${FLUTTER_MANAGED_DIR}/generated_plugin_registrant.cc"
  "Runner.rc"
  "runner.exe.manifest"
)

# Apply the standard set of build settings. This can be removed for applications
# that need different build settings.
apply_standard_settings(${BINARY_NAME})

# Add preprocessor definitions for the build version.
target_compile_definitions(${BINARY_NAME} PRIVATE "FLUTTER_VERSION=\"${FLUTTER_VERSION}\"")
target_compile_definitions(${BINARY_NAME} PRIVATE "FLUTTER_VERSION_MAJOR=${FLUTTER_VERSION_MAJOR}")
target_compile_definitions(${BINARY_NAME} PRIVATE "FLUTTER_VERSION_MINOR=${FLUTTER_VERSION_MINOR}")
target_compile_definitions(${BINARY_NAME} PRIVATE "FLUTTER_VERSION_PATCH=${FLUTTER_VERSION_PATCH}")
target_compile_definitions(${BINARY_NAME} PRIVATE "FLUTTER_VERSION_BUILD=${FLUTTER_VERSION_BUILD}")

# Disable Windows macros that collide with C++ standard library functions.
target_compile_definitions(${BINARY_NAME} PRIVATE "NOMINMAX")

# Add dependency libraries and include directories. Add any application-specific
# dependencies here.
target_link_libraries(${BINARY_NAME} PRIVATE flutter flutter_wrapper_app)
target_link_libraries(${BINARY_NAME} PRIVATE "dwmapi.lib")
target_include_directories(${BINARY_NAME} PRIVATE "${CMAKE_SOURCE_DIR}")

# Run the Flutter tool portions of the build. This must not be removed.
add_dependencies(${BINARY_NAME} flutter_assemble)

# === Wharf daemon ===
# Builds wharfd fresh and drops it next to the app executable on every build,
# so `flutter run -d windows` runs the daemon from the working tree instead of
# a stale build/wharfd.exe (the CMake equivalent of the "Embed wharfd" Xcode
# build phase in gui/macos; see gui/README.md "Embedding on each platform").
# A POST_BUILD step must live in the directory that defines the target, so
# this can't move to the top-level windows/CMakeLists.txt. It reruns every
# build regardless of whether wharf_gui's own sources changed; `go build`'s
# own cache keeps that cheap on unchanged daemon code.
# The Go installer puts go.exe in Program Files but only adds it to PATH for
# shells started afterwards, so an IDE or terminal opened earlier misses it.
# ProgramW6432, not just ProgramFiles: Visual Studio's CMake may run as a
# 32-bit process, where ProgramFiles means "Program Files (x86)".
find_program(GO_EXECUTABLE go
  PATHS "$ENV{GOROOT}/bin" "$ENV{ProgramW6432}/Go/bin" "$ENV{ProgramFiles}/Go/bin" "C:/Go/bin")
if(GO_EXECUTABLE)
  set(WHARF_REPO_ROOT "${CMAKE_CURRENT_SOURCE_DIR}/../../..")
  add_custom_command(TARGET ${BINARY_NAME} POST_BUILD
    COMMAND "${GO_EXECUTABLE}" build -o "$<TARGET_FILE_DIR:${BINARY_NAME}>/wharfd.exe" ./cmd/wharfd
    WORKING_DIRECTORY "${WHARF_REPO_ROOT}/daemon"
    COMMENT "Building wharfd.exe (Wharf daemon)"
    VERBATIM
  )
else()
  message(WARNING "Go not found on PATH, in GOROOT or in Program Files — wharfd.exe will not be rebuilt; "
    "Wharf falls back to build\\wharfd.exe or WHARFD_BIN. Install Go and "
    "reconfigure to enable it.")
endif()
