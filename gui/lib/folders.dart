import 'dart:io';

import 'package:file_selector/file_selector.dart';
import 'package:url_launcher/url_launcher.dart';

import 'daemon.dart';

/// Opens a folder in the system file manager, or a file in its editor.
typedef PathOpener = Future<void> Function(String path);

/// How folders are opened. A variable so widget tests can see what would
/// have been opened without a file manager appearing
/// (features/project-folders.feature, "Opening a project's folder").
PathOpener openFolder = _openNearestFolder;

/// How php.ini is opened for editing.
PathOpener editFile = _openInEditor;

/// Opens [path], or the nearest folder above it that exists — a webserver
/// that is not installed yet has no folder of its own to show.
Future<void> _openNearestFolder(String path) async {
  var dir = Directory(path);
  while (!await dir.exists() && dir.parent.path != dir.path) {
    dir = dir.parent;
  }
  await launchUrl(Uri.directory(dir.path));
}

/// A .conf or .ini file has no default application on most systems, so each
/// one is sent to the plain-text editor explicitly. A failure is thrown with
/// the path, so the window can say where the file is instead of doing
/// nothing.
Future<void> _openInEditor(String path) async {
  if (Platform.isWindows) {
    await Process.start('notepad.exe', [path], mode: ProcessStartMode.detached);
    return;
  }
  // INFO: macOS: the user's default text editor, else TextEdit, which every Mac
  // has. Linux: whatever the desktop opens the file with.
  final attempts = Platform.isMacOS
      ? [
          ['open', '-t', path],
          ['open', '-a', 'TextEdit', path],
        ]
      : [
          ['xdg-open', path],
        ];
  for (final command in attempts) {
    final result = await Process.run(command.first, command.sublist(1));
    if (result.exitCode == 0) return;
  }
  throw EditorUnavailable(path);
}

/// No editor would open a file. The message names the file, so it can be
/// opened by hand.
class EditorUnavailable implements Exception {
  EditorUnavailable(this.path);
  final String path;

  @override
  String toString() => 'No text editor would open $path — open it by hand.';
}

/// Asks for a folder: a picker that starts in www/ but accepts any folder.
/// A variable so widget tests can answer it without a dialog appearing.
Future<String?> Function(String www) pickFolder = (www) =>
    getDirectoryPath(initialDirectory: www.isEmpty ? null : www, confirmButtonText: 'Choose');

/// The tray's "Add project…": the chosen folder is added as it is, without a
/// window. One outside www/ stays where it is
/// (features/tray-actions.feature, "Adding a project via the tray").
Future<void> pickAndAddFolder(Daemon daemon) async {
  final picked = await pickFolder(daemon.state.www);
  if (picked == null) return;
  await daemon.addFolder(picked);
}
