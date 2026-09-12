import 'dart:async';
import 'dart:io';

import 'ipc/client.dart';
import 'ipc/endpoint.dart';

/// Makes the application one thing to launch. The daemon is an implementation
/// detail: the app finds it, starts it if nothing is running, and stops it on
/// quit if it was the one that started it (features/single-application.feature).
class DaemonLauncher {
  DaemonLauncher({
    required this.root,
    List<String>? candidates,
    this.extraArgs = const [],
    this.startTimeout = const Duration(seconds: 15),
  }) : candidates = candidates ?? daemonSearchPath();

  final String root;

  /// Where to look for the wharfd binary, in order.
  final List<String> candidates;

  /// Passed through to wharfd — used by `make gui` for a throwaway root.
  final List<String> extraArgs;
  final Duration startTimeout;

  Process? _owned;

  /// True if this app started the daemon, and so must stop it on quit. A
  /// daemon that was already running belongs to someone else and is left
  /// alone.
  bool get ownsDaemon => _owned != null;

  /// The process id of the daemon this app started, if any.
  int? get ownedPid => _owned?.pid;

  /// Connects to a running daemon, or starts one and connects to it.
  Future<IpcClient> connect() async {
    final endpointPath = endpointPathFor(root);

    final existing = await _tryConnect(endpointPath);
    if (existing != null) return existing;

    final binary = findDaemonBinary(candidates);
    if (binary == null) throw MissingDaemonBinary(candidates);

    final process = await Process.start(binary, [
      '--root', root,
      // If the app dies without stopping the daemon, the daemon stops itself.
      '--parent-pid', '$pid',
      ...extraArgs,
    ]);
    _owned = process;
    // Nothing reads the daemon's log here, but an undrained pipe fills and
    // blocks it.
    process.stdout.drain<void>();
    final stderr = StringBuffer();
    process.stderr.transform(const SystemEncoding().decoder).listen((chunk) {
      if (stderr.length < 4000) stderr.write(chunk);
    });

    var exited = false;
    unawaited(process.exitCode.then((_) => exited = true));

    // The endpoint file may be a stale one from a crashed daemon until the new
    // one rewrites it, so keep trying until a connection actually works.
    final deadline = DateTime.now().add(startTimeout);
    while (DateTime.now().isBefore(deadline)) {
      if (exited) {
        _owned = null;
        throw DaemonFailedToStart(binary, stderr.toString().trim());
      }
      final client = await _tryConnect(endpointPath);
      if (client != null) return client;
      await Future<void>.delayed(const Duration(milliseconds: 50));
    }
    process.kill();
    _owned = null;
    throw DaemonFailedToStart(binary, 'did not come up within ${startTimeout.inSeconds}s');
  }

  /// Stops the daemon if this app started it. Asked over IPC first so it can
  /// stop its webservers — on Windows a signal is TerminateProcess, which the
  /// daemon never sees. Killed if it does not go.
  ///
  /// [includingAttached] also asks a daemon this app only attached to — what
  /// "Cast off" means by everything. In development a hot restart makes every
  /// daemon look attached: the restarted app no longer knows it started it.
  Future<void> stop({bool includingAttached = false}) async {
    final process = _owned;
    if (process == null) {
      if (includingAttached) await _askToShutDown();
      return;
    }
    _owned = null;

    if (!await _askToShutDown()) process.kill(ProcessSignal.sigterm);
    try {
      await process.exitCode.timeout(const Duration(seconds: 20));
    } on TimeoutException {
      process.kill(ProcessSignal.sigkill);
      await process.exitCode;
    }
  }

  /// False if the daemon could not be reached to ask.
  Future<bool> _askToShutDown() async {
    final client = await _tryConnect(endpointPathFor(root));
    if (client == null) return false;
    try {
      await client.call('daemon.shutdown').timeout(const Duration(seconds: 5));
    } catch (_) {
      // The daemon drops its connections as it goes down; the reply can be lost.
    }
    await client.close().catchError((_) {});
    return true;
  }

  Future<IpcClient?> _tryConnect(String endpointPath) async {
    try {
      final endpoint = await Endpoint.read(endpointPath);
      return await IpcClient.connect(endpoint);
    } catch (_) {
      return null;
    }
  }
}

/// The binary's file name on this OS.
String daemonBinaryName() => Platform.isWindows ? 'wharfd.exe' : 'wharfd';

/// Where the app looks for wharfd, most specific first:
///
/// 1. `WHARFD_BIN`, for anyone who wants to point at a particular build;
/// 2. next to the app's own executable — where a packaged app carries it,
///    and where the macOS build phase embeds it;
/// 3. the repository's `build/` folder, found by walking up from the working
///    directory, so `flutter run` from a checkout works too.
List<String> daemonSearchPath({String? override, String? executable, String? workingDirectory}) {
  final name = daemonBinaryName();
  final sep = Platform.pathSeparator;
  final out = <String>[];

  override ??= Platform.environment['WHARFD_BIN'];
  if (override != null && override.isNotEmpty) out.add(override);

  executable ??= Platform.resolvedExecutable;
  out.add('${File(executable).parent.path}$sep$name');

  var dir = Directory(workingDirectory ?? Directory.current.path);
  for (var i = 0; i < 6; i++) {
    if (File('${dir.path}${sep}daemon${sep}go.mod').existsSync()) {
      out.add('${dir.path}${sep}build$sep$name');
      break;
    }
    final parent = dir.parent;
    if (parent.path == dir.path) break;
    dir = parent;
  }
  return out;
}

/// Returns the first candidate that exists, or null.
String? findDaemonBinary(List<String> candidates) {
  for (final path in candidates) {
    if (File(path).existsSync()) return path;
  }
  return null;
}

/// The daemon binary is not anywhere the app looked.
class MissingDaemonBinary implements Exception {
  MissingDaemonBinary(this.searched);
  final List<String> searched;

  @override
  String toString() =>
      'Wharf could not find its daemon (${daemonBinaryName()}). '
      'Looked in: ${searched.join(', ')}. '
      'Build it with `make build` in the repository — Wharf keeps checking and '
      'starts as soon as it is there.';
}

/// The daemon was found but did not come up.
class DaemonFailedToStart implements Exception {
  DaemonFailedToStart(this.binary, this.detail);
  final String binary;
  final String detail;

  @override
  String toString() =>
      'The Wharf daemon at $binary did not start${detail.isEmpty ? '' : ': $detail'}';
}
