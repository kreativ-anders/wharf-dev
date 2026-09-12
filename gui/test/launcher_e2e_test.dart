@Tags(['e2e'])
library;

import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:wharf_gui/daemon_launcher.dart';
import 'package:wharf_gui/ipc/client.dart';
import 'package:wharf_gui/ipc/endpoint.dart';

/// The launcher against the real wharfd binary: the app must be one thing to
/// start and leave nothing behind. Run with `make gui-e2e`.
void main() {
  final binary =
      Platform.environment['WHARFD_BIN'] ??
      '${Directory.current.parent.path}/build/${daemonBinaryName()}';

  late Directory root;
  late List<String> safeArgs;

  setUp(() async {
    if (!File(binary).existsSync()) fail('run `make build` first: no $binary');
    root = await Directory.systemTemp.createTemp('wharf-launch');
    final hosts = File('${root.path}/hosts')..writeAsStringSync('127.0.0.1\tlocalhost\n');
    // Never the real hosts file, never a password prompt.
    safeArgs = ['--hosts', hosts.path, '--elevator', 'direct'];
  });

  tearDown(() async {
    if (root.existsSync()) await root.delete(recursive: true);
  });

  DaemonLauncher launcherFor() =>
      DaemonLauncher(root: root.path, candidates: [binary], extraArgs: safeArgs);

  // features/single-application.feature — "Launching the application with
  // nothing running"
  test('the app starts its own daemon when none is running', () async {
    final launcher = launcherFor();
    addTearDown(launcher.stop);

    final client = await launcher.connect();
    addTearDown(client.close);

    expect(launcher.ownsDaemon, isTrue);
    expect(File(endpointPathFor(root.path)).existsSync(), isTrue);
    final state = await client.call('state.get');
    expect(state['root'], root.path, reason: 'the project list has data to show');
  });

  // features/single-application.feature — "Launching the application when a
  // daemon is already running"
  test('the app attaches to a running daemon instead of starting another', () async {
    final external = await _startExternal(binary, root.path, safeArgs);
    addTearDown(() async {
      external.kill();
      await external.exitCode;
    });

    final launcher = launcherFor();
    final client = await launcher.connect();
    addTearDown(client.close);

    expect(launcher.ownsDaemon, isFalse, reason: 'no second daemon may be started');
    expect((await client.call('ping'))['pong'], 'wharf');
  });

  // features/single-application.feature — "Quitting an application that
  // started its own daemon"
  test('quitting stops the daemon the app started', () async {
    final launcher = launcherFor();
    final client = await launcher.connect();
    final pid = launcher.ownedPid!;
    await client.close();

    await launcher.stop();

    expect(launcher.ownsDaemon, isFalse);
    expect(_alive(pid), isFalse, reason: 'the daemon must not outlive the app');
    // A clean shutdown removes the endpoint, so the next launch starts fresh.
    expect(File(endpointPathFor(root.path)).existsSync(), isFalse);
  });

  // features/single-application.feature — "Quitting an application that
  // attached to an existing daemon"
  test('quitting leaves a daemon the app did not start running', () async {
    final external = await _startExternal(binary, root.path, safeArgs);
    addTearDown(() async {
      external.kill();
      await external.exitCode;
    });

    final launcher = launcherFor();
    final client = await launcher.connect();
    await client.close();
    await launcher.stop();

    expect(_alive(external.pid), isTrue);
    final again = await IpcClient.connect(await Endpoint.read(endpointPathFor(root.path)));
    addTearDown(again.close);
    expect((await again.call('ping'))['pong'], 'wharf', reason: 'its projects stay up');
  });

  // features/single-application.feature — "Casting off from the main window"
  test('casting off stops even a daemon the app only attached to', () async {
    final external = await _startExternal(binary, root.path, safeArgs);
    addTearDown(() async {
      external.kill();
      await external.exitCode;
    });

    final launcher = launcherFor();
    final client = await launcher.connect();
    await client.close();
    await launcher.stop(includingAttached: true);

    await external.exitCode.timeout(const Duration(seconds: 20));
    expect(File(endpointPathFor(root.path)).existsSync(), isFalse);
  });
}

/// Starts a daemon the way a user might have — not through the launcher.
Future<Process> _startExternal(String binary, String root, List<String> args) async {
  final process = await Process.start(binary, ['--root', root, ...args]);
  process.stdout.drain<void>();
  process.stderr.drain<void>();
  final endpoint = File(endpointPathFor(root));
  final deadline = DateTime.now().add(const Duration(seconds: 10));
  while (!endpoint.existsSync()) {
    if (DateTime.now().isAfter(deadline)) fail('external daemon never came up');
    await Future<void>.delayed(const Duration(milliseconds: 25));
  }
  return process;
}

bool _alive(int pid) {
  if (Platform.isWindows) {
    final result = Process.runSync('tasklist', ['/FI', 'PID eq $pid', '/NH']);
    return result.stdout.toString().contains('$pid');
  }
  return Process.runSync('kill', ['-0', '$pid']).exitCode == 0;
}
