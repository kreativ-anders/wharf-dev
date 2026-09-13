import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:wharf_gui/daemon_launcher.dart';

void main() {
  late Directory tmp;

  setUp(() async => tmp = await Directory.systemTemp.createTemp('wharf-launcher'));
  tearDown(() async => tmp.delete(recursive: true));

  test('an explicit override is tried first, then next to the app', () {
    final sep = Platform.pathSeparator;
    final appDir = '${tmp.path}${sep}Contents${sep}MacOS';
    final path = daemonSearchPath(
      override: '/custom/wharfd',
      executable: '$appDir${sep}Wharf',
      workingDirectory: tmp.path,
    );
    expect(path.first, '/custom/wharfd');
    expect(path[1], '$appDir$sep${daemonBinaryName()}');
  });

  test('a repository checkout finds its own build folder', () async {
    // INFO: gui/ sits beside daemon/ in the repo; running from gui/ must find
    // build/wharfd at the repo root.
    final sep = Platform.pathSeparator;
    await Directory('${tmp.path}${sep}daemon').create();
    await File('${tmp.path}${sep}daemon${sep}go.mod').writeAsString('module x\n');
    await Directory('${tmp.path}${sep}gui').create();

    final path = daemonSearchPath(
      override: '',
      executable: '${tmp.path}${sep}elsewhere${sep}Wharf',
      workingDirectory: '${tmp.path}${sep}gui',
    );
    expect(path, contains('${tmp.path}${sep}build$sep${daemonBinaryName()}'));
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
