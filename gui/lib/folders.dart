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

/// How custom config files are opened for editing.
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

/// A .conf file has no default application on most systems, so each one is
/// sent to the plain-text editor explicitly.
Future<void> _openInEditor(String path) async {
  if (Platform.isMacOS) {
    await Process.run('open', ['-t', path]);
  } else if (Platform.isWindows) {
    await Process.start('notepad.exe', [path], mode: ProcessStartMode.detached);
  } else {
    await Process.run('xdg-open', [path]);
  }
}

/// "Add folder…": a folder picker that starts in www/ but accepts any folder.
/// One outside www/ stays where it is (features/project-folders.feature).
Future<void> pickAndAddFolder(Daemon daemon) async {
  final www = daemon.state.www;
  final picked = await getDirectoryPath(
    initialDirectory: www.isEmpty ? null : www,
    confirmButtonText: 'Add',
  );
  if (picked == null) return;
  await daemon.addFolder(picked);
}
