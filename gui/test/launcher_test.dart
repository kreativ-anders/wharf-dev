import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:wharf_gui/daemon_launcher.dart';

void main() {
  late Directory tmp;

  setUp(() async => tmp = await Directory.systemTemp.createTemp('wharf-launcher'));
  tearDown(() async => tmp.delete(recursive: true));

  test('an explicit override is tried first, then next to the app', () {
    final path = daemonSearchPath(
      override: '/custom/wharfd',
      executable: '/Applications/Wharf.app/Contents/MacOS/Wharf',
      workingDirectory: tmp.path,
    );
    expect(path.first, '/custom/wharfd');
    expect(path[1], '/Applications/Wharf.app/Contents/MacOS/${daemonBinaryName()}');
  });

  test('a repository checkout finds its own build folder', () async {
    // gui/ sits beside daemon/ in the repo; running from gui/ must find
    // build/wharfd at the repo root.
    await Directory('${tmp.path}/daemon').create();
    await File('${tmp.path}/daemon/go.mod').writeAsString('module x\n');
    await Directory('${tmp.path}/gui').create();

    final path = daemonSearchPath(
      override: '',
      executable: '/elsewhere/Wharf',
      workingDirectory: '${tmp.path}/gui',
    );
    expect(path, contains('${tmp.path}/build/${daemonBinaryName()}'));
  });

  test('the first candidate that exists wins', () async {
    final real = File('${tmp.path}/second');
    await real.writeAsString('');
    expect(findDaemonBinary(['${tmp.path}/first', real.path, '${tmp.path}/third']), real.path);
  });

  // features/single-application.feature — "The daemon binary cannot be found"
  test('a missing binary says where it looked and how to build it', () async {
    final launcher = DaemonLauncher(
      root: tmp.path,
      candidates: ['${tmp.path}/nope/wharfd', '${tmp.path}/also-nope/wharfd'],
    );

    try {
      await launcher.connect();
      fail('connect should fail without a binary');
    } on MissingDaemonBinary catch (e) {
      final message = e.toString();
      expect(message, contains('${tmp.path}/nope/wharfd'));
      expect(message, contains('${tmp.path}/also-nope/wharfd'));
      expect(message, contains('make build'));
      expect(message, contains('keeps checking'), reason: 'building it must be enough to recover');
    }
    expect(launcher.ownsDaemon, isFalse);
  });
}
