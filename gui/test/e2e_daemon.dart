import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:wharf_gui/daemon_launcher.dart';

/// The wharfd binary the e2e tests drive.
///
/// A bare `flutter test` does not rebuild it, and a build older than the
/// daemon's sources fails these tests in ways that look like bugs in the code
/// under test — a shutdown the old daemon does not know, an endpoint it does
/// not remove. So a stale build is refused with the fix instead of run.
/// `WHARFD_BIN` points at a particular build on purpose and is taken as given.
String e2eDaemonBinary() {
  final override = Platform.environment['WHARFD_BIN'];
  if (override != null && override.isNotEmpty) {
    if (!File(override).existsSync()) fail('WHARFD_BIN points at $override, which does not exist');
    return override;
  }

  final repo = Directory.current.parent.path;
  final binary = File('$repo/build/${daemonBinaryName()}');
  if (!binary.existsSync()) {
    fail('wharfd not found at ${binary.path} — run `make build` at the repo root first');
  }
  final built = binary.lastModifiedSync();
  final newer = Directory('$repo/daemon')
      .listSync(recursive: true)
      .whereType<File>()
      .where((f) => _isSource(f.path) && f.lastModifiedSync().isAfter(built))
      .firstOrNull;
  if (newer != null) {
    fail('${binary.path} is older than ${newer.path} — run `make build` at the repo root, '
        'or `make gui-e2e`, which builds first');
  }
  return binary.path;
}

bool _isSource(String path) =>
    (path.endsWith('.go') && !path.endsWith('_test.go')) ||
    path.endsWith('go.mod') ||
    path.endsWith('go.sum');
