import 'dart:async';

import 'package:flutter/foundation.dart';

import 'daemon_launcher.dart';
import 'ipc/client.dart';
import 'ipc/endpoint.dart';
import 'models/state.dart';

/// Method names, mirroring daemon/internal/ipc/protocol.go.
class Method {
  static const state = 'state.get';
  static const templates = 'templates.list';
  static const setWebserver = 'services.setWebserver';
  static const setPhpVersion = 'services.setPHPVersion';
  static const addPhpVersion = 'services.addPHPVersion';
  static const detectPhp = 'services.detectPHP';
  static const installPhp = 'services.installPHP';
  static const setupSsl = 'services.setupSSL';
  static const phpSettings = 'services.phpSettings';
  static const phpReleases = 'services.phpReleases';
  static const installWebserver = 'services.installWebserver';
  static const detectWebservers = 'services.detectWebservers';
  static const setAppearance = 'settings.setAppearance';
  static const reset = 'settings.reset';
  static const stopAll = 'services.stopAll';
  static const projectAdd = 'projects.add';
  static const projectRemove = 'projects.remove';
  static const projectStart = 'projects.start';
  static const projectStop = 'projects.stop';
  static const projectRestart = 'projects.restart';
  static const projectSettings = 'projects.settings';
  static const projectScaffold = 'projects.scaffold';
  static const projectCustomConfig = 'projects.customConfig';
}

/// Connection status, for the one line of chrome the window spends on it.
enum Connection { connecting, connected, disconnected }

/// Daemon holds the app's whole state: one snapshot, pushed by the daemon.
///
/// Every action returns a fresh snapshot and the daemon also broadcasts one on
/// any change, so there is no local state to keep in step and no polling.
class Daemon extends ChangeNotifier {
  Daemon({String? root, this.daemonArgs = const []}) : root = root ?? defaultRoot();

  final String root;

  /// Extra wharfd arguments; only `make gui` sets any.
  final List<String> daemonArgs;

  /// Finds or starts the background daemon, so the user launches one thing
  /// (features/single-application.feature).
  late final DaemonLauncher launcher = DaemonLauncher(root: root, extraArgs: daemonArgs);

  var _shuttingDown = false;

  IpcClient? _client;
  Timer? _retry;

  WharfState state = WharfState.empty;
  List<Template> templates = const [];
  Connection connection = Connection.connecting;

  /// The last thing that went wrong, shown as a dismissible line rather than a
  /// modal: a declined elevation prompt is not worth a dialog.
  String? notice;

  bool _disposed = false;

  Future<void> start() async {
    await _connect();
  }

  Future<void> _connect() async {
    _retry?.cancel();
    _set(() => connection = Connection.connecting);
    try {
      final client = await launcher.connect();
      _client = client;

      client.events.listen((frame) {
        if (frame['event'] == 'state') {
          final data = frame['data'] as Map<String, dynamic>?;
          if (data != null) _set(() => state = WharfState.fromJson(data));
        }
      });
      unawaited(client.done.then((_) => _onDisconnected()));

      await refresh();
      final list = await client.callList(Method.templates);
      templates = list.map((e) => Template.fromJson(e as Map<String, dynamic>)).toList();

      _set(() {
        connection = Connection.connected;
        notice = null;
      });
    } catch (e) {
      if (_shuttingDown) return;
      // A missing binary explains itself; keep retrying so that building it
      // is all it takes to recover.
      _set(() {
        connection = Connection.disconnected;
        notice = '$e';
      });
      _scheduleRetry();
    }
  }

  void _onDisconnected() {
    _client = null;
    if (_disposed || _shuttingDown) return;
    _set(() => connection = Connection.disconnected);
    _scheduleRetry();
  }

  /// The daemon may be restarting; reconnecting quietly beats an error dialog.
  void _scheduleRetry() {
    _retry?.cancel();
    _retry = Timer(const Duration(seconds: 2), () {
      if (!_disposed) _connect();
    });
  }

  Future<void> refresh() async {
    final result = await _require().call(Method.state);
    _set(() => state = WharfState.fromJson(result));
  }

  // ---------------------------------------------------------------- actions

  Future<void> startProject(String name) => _act(Method.projectStart, {'name': name});
  Future<void> stopProject(String name) => _act(Method.projectStop, {'name': name});
  Future<void> restartProject(String name) => _act(Method.projectRestart, {'name': name});

  /// Does [action] to a project — what its row and its tray submenu call.
  Future<void> projectAction(ProjectAction action, String name) => switch (action) {
    ProjectAction.start => startProject(name),
    ProjectAction.stop => stopProject(name),
    ProjectAction.restart => restartProject(name),
  };
  Future<void> removeProject(String name) => _act(Method.projectRemove, {'name': name});
  Future<void> stopAll() => _act(Method.stopAll);

  /// Back to a first start: every folder in www/ and every setting deleted
  /// (features/settings.feature, "Resetting Wharf").
  Future<void> reset() => _act(Method.reset);
  Future<void> setWebserver(String name) => _act(Method.setWebserver, {'name': name});
  Future<void> setPhpVersion(String version) => _act(Method.setPhpVersion, {'version': version});

  /// Downloads a PHP version into bin/php/. The snapshot shows it as
  /// downloading meanwhile (features/php-runtime.feature).
  Future<void> installPhp(String version) => _act(Method.installPhp, {'version': version});

  /// Installs nginx or Apache; Settings shows it as installing meanwhile
  /// (features/webserver-install.feature).
  Future<void> installWebserver(String name) => _act(Method.installWebserver, {'name': name});
  Future<void> rescanWebservers() => _act(Method.detectWebservers);

  /// Light, dark or the system's — stored by the daemon like every other
  /// setting, so the window keeps no state of its own.
  Future<void> setAppearance(String mode) => _act(Method.setAppearance, {'mode': mode});

  /// Installs mkcert and asks once to trust its certificate authority. A
  /// declined prompt is a notice, not an error (features/local-ssl.feature).
  Future<void> setupSsl() async {
    await _guard(() async {
      try {
        await _require().call(Method.setupSsl);
      } on DaemonError catch (e) {
        if (!e.isElevationDenied) rethrow;
        notice = 'Not trusted — browsers will warn about Wharf\'s certificates until you allow it.';
      }
      await refresh();
    });
  }

  /// Opens a project's custom config for one webserver, creating it first if
  /// needed (features/app-configuration.feature).
  Future<void> editCustomConfig(
    String project,
    String webserver,
    Future<void> Function(String) open,
  ) async {
    await _guard(() async {
      final result = await _require().call(Method.projectCustomConfig, {
        'name': project,
        'webserver': webserver,
      });
      await refresh();
      final path = result['path'] as String?;
      if (path != null) await open(path);
    });
  }

  /// Opens config/php.ini, creating it first if needed
  /// (features/php-settings.feature).
  Future<void> editPhpSettings(Future<void> Function(String) open) async {
    await _guard(() async {
      final result = await _require().call(Method.phpSettings);
      await refresh();
      final path = result['path'] as String?;
      if (path != null) await open(path);
    });
  }

  /// Looks up the release each PHP download would fetch, so the offers can
  /// name it. Quiet: offline, the offers keep their minor version, which is
  /// no reason for a notice (features/php-runtime.feature).
  Future<void> checkPhpReleases() async {
    try {
      final result = await _require().call(Method.phpReleases);
      _set(() => state = WharfState.fromJson(result));
    } catch (_) {}
  }

  Future<void> rescanPhp() async {
    await _require().callList(Method.detectPhp);
    await refresh();
  }

  /// Registers an existing folder. A declined elevation prompt is reported as
  Future<void> addProject(String name) => _add({'name': name});

  /// Registers a folder from anywhere; one outside www/ stays where it is
  /// (features/project-folders.feature).
  Future<void> addFolder(String path) => _add({'path': path});

  Future<void> _add(Map<String, dynamic> params) async {
    await _guard(() async {
      await _require().call(Method.projectAdd, params);
      await refresh();
    });
  }

  /// Creates a project from a template, pinned to [webserver] unless that is
  /// empty, and returns it — or null if the daemon refused
  /// (features/quick-app-php.feature).
  Future<Project?> scaffold(String templateId, String name, {String webserver = ''}) async {
    Project? created;
    await _guard(() async {
      final result = await _require().call(Method.projectScaffold, {
        'template': templateId,
        'name': name,
        if (webserver.isNotEmpty) 'webserver': webserver,
      });
      created = Project.fromJson(result);
      await refresh();
    });
    return created;
  }

  /// Applies per-project overrides. A null field leaves a setting alone; an
  /// empty string clears an override and reverts to the global default
  /// (features/app-configuration.feature).
  Future<void> updateSettings(
    String name, {
    String? webserverOverride,
    String? phpOverride,
    bool? ssl,
  }) async {
    await _guard(() async {
      await _require().call(Method.projectSettings, {
        'name': name,
        // A key that is absent leaves that setting alone; an empty string
        // clears an override and reverts to the global default.
        'settings': {
          'webserver_override': ?webserverOverride,
          'php_version': ?phpOverride,
          'ssl': ?ssl,
        },
      });
      await refresh();
      if (ssl == true && !state.ssl.trusted) {
        notice =
            'SSL is on, but browsers will warn until Wharf\'s certificates are trusted — '
            'see Settings → SSL.';
      }
    });
  }

  void dismissNotice() => _set(() => notice = null);

  Future<void> _act(String method, [Map<String, dynamic>? params]) async {
    await _guard(() async {
      final result = await _require().call(method, params);
      if (result.containsKey('projects')) {
        state = WharfState.fromJson(result);
      } else {
        await refresh();
      }
    });
  }

  /// Runs an action, turning a daemon error into a notice. Nothing the user
  /// can do from this window deserves an exception reaching the framework.
  Future<void> _guard(Future<void> Function() action) async {
    try {
      await action();
      _set(() {});
    } on DaemonError catch (e) {
      // An older daemon still running after an update — or, in development,
      // after a hot reload, which rebuilds the app but not wharfd — cannot
      // do what this window asks. Say what fixes it, not "unknown method".
      _set(
        () => notice = e.isUnknownMethod
            ? 'Wharf\'s background service is older than this window. '
                  'Quit Wharf from the tray and open it again.'
            : e.message,
      );
    } catch (e) {
      _set(() => notice = '$e');
    }
  }

  IpcClient _require() {
    final client = _client;
    if (client == null) throw DaemonNotRunning(endpointPathFor(root));
    return client;
  }

  void _set(void Function() change) {
    if (_disposed) return;
    change();
    notifyListeners();
  }

  /// Quits cleanly: stops the daemon if this app started it, and leaves one
  /// that was already running alone.
  Future<void> shutdown() async {
    _shuttingDown = true;
    _retry?.cancel();
    await _client?.close();
    _client = null;
    await launcher.stop();
  }

  @override
  void dispose() {
    _disposed = true;
    _retry?.cancel();
    _client?.close();
    super.dispose();
  }
}
