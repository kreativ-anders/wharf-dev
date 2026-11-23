import 'dart:io';

import 'package:file_selector/file_selector.dart';
import 'package:url_launcher/url_launcher.dart';
import 'package:url_launcher/url_launcher_string.dart';

import 'daemon.dart';
import 'models/state.dart';

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

/// Opens a project's address in [browser]: the app chosen under "Open
/// projects in" while it is there, otherwise — and if it will not start — the
/// system's default browser (features/settings.feature). A variable so widget
/// tests can see what would have opened without a browser appearing.
Future<void> Function(String url, Browser browser) openInBrowser = _openInBrowser;

/// Every "Open" of a project — the window's row, its settings, the tray —
/// goes through here, so all three use the same browser.
Future<void> openProject(Daemon daemon, String url) =>
    daemon.open((url) => openInBrowser(url, daemon.state.browser), url);

Future<void> _openInBrowser(String url, Browser browser) async {
  if (browser.found) {
    try {
      await _startApp(browser.path, url);
      return;
    } on ProcessException {
      // INFO: An app that is there but will not start still opens the project,
      // in the default browser, rather than not at all.
    }
  }
  await launchUrlString(url);
}

/// A macOS .app is a folder that `open -a` starts; an app anywhere else is a
/// file, run with the address. The shape of the path decides, not the OS.
Future<void> _startApp(String app, String url) async {
  if (app.toLowerCase().endsWith('.app')) {
    final result = await Process.run('open', ['-a', app, url]);
    if (result.exitCode != 0) {
      throw ProcessException('open', ['-a', app, url], '${result.stderr}', result.exitCode);
    }
    return;
  }
  await Process.start(app, [url], mode: ProcessStartMode.detached);
}

/// Asks for the app projects open in. It starts in /Applications where there
/// is one (macOS); elsewhere the picker's own default. A variable so widget
/// tests can answer it without a dialog appearing.
Future<String?> Function() pickApp = () async {
  final apps = Directory('/Applications');
  final picked = await openFile(initialDirectory: apps.existsSync() ? apps.path : null, confirmButtonText: 'Choose');
  return picked?.path;
};

/// Asks for a folder: a picker that starts in www/ but accepts any folder.
/// A variable so widget tests can answer it without a dialog appearing.
Future<String?> Function(String www) pickFolder = (www) =>
    getDirectoryPath(initialDirectory: www.isEmpty ? null : www, confirmButtonText: 'Choose');

/// Asks where to save a download, proposing [name]. A variable so widget
/// tests can answer it without a dialog appearing.
Future<String?> Function(String name) pickSaveLocation = (name) async =>
    (await getSaveLocation(suggestedName: name, confirmButtonText: 'Save'))?.path;

/// The tray's "Add project…": the chosen folder is added as it is, without a
/// window. One outside www/ stays where it is
/// (features/tray-actions.feature, "Adding a project via the tray").
Future<void> pickAndAddFolder(Daemon daemon) async {
  final picked = await pickFolder(daemon.state.www);
  if (picked == null) return;
  await daemon.addFolder(picked);
}
