import 'dart:async';
import 'dart:io';

import 'package:flutter/foundation.dart';

import 'daemon_launcher.dart';
import 'ipc/client.dart';
import 'ipc/endpoint.dart';
import 'models/state.dart';

/// Method names, mirroring daemon/internal/ipc/protocol.go.
class Method {
  static const state = 'state.get';
  static const setWebserver = 'services.setWebserver';
  static const setPhpVersion = 'services.setPHPVersion';
  static const addPhpVersion = 'services.addPHPVersion';
  static const detectPhp = 'services.detectPHP';
  static const installPhp = 'services.installPHP';
  static const removePhp = 'services.removePHP';
  static const unhidePhp = 'services.unhidePHP';
  static const setPhpTerminal = 'services.setPHPTerminal';
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
  static const projectInspect = 'projects.inspect';
  static const projectShare = 'projects.share';
  static const replaceNetworkCertificate = 'network.replaceCertificate';
  static const customConfigRead = 'projects.customConfig.read';
  static const customConfigSave = 'projects.customConfig.save';
  static const customConfigDelete = 'projects.customConfig.delete';
  static const configTemplateRead = 'configTemplates.read';
  static const configTemplateSave = 'configTemplates.save';
  static const configTemplateCreate = 'configTemplates.create';
  static const configTemplateDelete = 'configTemplates.delete';
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
  Connection connection = Connection.connecting;

  /// The last thing that went wrong, shown as a dismissible line rather than a
  /// modal: a declined elevation prompt is not worth a dialog.
  String? notice;

  /// How many of the window's and the tray's actions are still under way. The
  /// daemon answers most requests one at a time, so a click can wait behind
  /// another one; counting them lets the window show the click was taken
  /// (features/single-application.feature, "A click is acknowledged while
  /// Wharf works").
  int _pending = 0;

  /// True while an action is under way here, or the daemon is busy with one
  /// of its own.
  bool get working => _pending > 0 || state.busy;

  bool _disposed = false;

  Future<void> start() async {
    await _connect();
  }

  Future<void> _connect() async {
    _retry?.cancel();
    _set(() => connection = Connection.connecting);
    IpcClient? client;
    try {
      client = await launcher.connect();
      _client = client;

      client.events.listen((frame) {
        if (frame['event'] == 'state') {
          final data = frame['data'] as Map<String, dynamic>?;
          if (data != null) _set(() => state = WharfState.fromJson(data));
        }
      });
      unawaited(client.done.then((_) => _onDisconnected()));

      await refresh();

      _set(() {
        connection = Connection.connected;
        notice = null;
      });
    } catch (e) {
      // WARNING: A connection that failed half-way through setting up is
      // closed here, or the retry would open a second one beside it.
      if (client != null) {
        _client = null;
        unawaited(client.close());
      }
      if (_shuttingDown) return;
      // INFO: A missing binary explains itself; keep retrying so that building it
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

  /// Shares a running project on the local network, or stops sharing it
  /// (features/sharing.feature). Why it could not be shared — not running,
  /// no network — becomes the notice.
  Future<void> shareProject(String name, {bool on = true}) =>
      _act(Method.projectShare, {'name': name, 'on': on});

  /// Deletes the network certificate authority and serves every project
  /// shared over HTTPS with a new one; phones must install it again.
  Future<void> replaceNetworkCertificate() => _act(Method.replaceNetworkCertificate);
  Future<void> stopAll() => _act(Method.stopAll);

  /// Back to a first start: every setting deleted, every project forgotten,
  /// and the folders in www/ deleted only with [deleteProjects]
  /// (features/settings.feature, "Resetting Wharf").
  Future<void> reset({bool deleteProjects = false}) =>
      _act(Method.reset, {'delete_projects': deleteProjects});
  Future<void> setWebserver(String name) => _act(Method.setWebserver, {'name': name});
  Future<void> setPhpVersion(String version) => _act(Method.setPhpVersion, {'version': version});

  /// Downloads a PHP version into bin/php/. The snapshot shows it as
  /// downloading meanwhile (features/php-runtime.feature).
  Future<void> installPhp(String version) => _act(Method.installPhp, {'version': version});

  /// Deletes a PHP version Wharf downloaded, or hides one found on the
  /// machine — the daemon knows which it is (features/php-runtime.feature).
  Future<void> removePhp(String version) => _act(Method.removePhp, {'version': version});

  /// Shows a hidden PHP folder in the picker again.
  Future<void> unhidePhp(String dir) => _act(Method.unhidePhp, {'dir': dir});

  /// Puts bin/path on the user's PATH, or takes it off
  /// (features/php-terminal.feature).
  Future<void> setPhpTerminal(bool on) => _act(Method.setPhpTerminal, {'on': on});

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

  /// Opens config/php.ini, creating it first if needed
  /// (features/php-settings.feature).
  Future<void> editPhpSettings(Future<void> Function(String) open) async {
    final php = state.services.php;
    await _openOrCreate(
      php.settingsExist ? php.settings : null,
      open,
      () => _require().call(Method.phpSettings),
    );
  }

  /// A file the snapshot already lists as existing opens at once: the daemon
  /// handles most requests one at a time, so asking it first would queue the
  /// editor behind whatever it is busy with — a project start, a restart.
  /// Only a file that still has to be created goes through the daemon, and the
  /// editor opens before the snapshot is refreshed.
  Future<void> _openOrCreate(
    String? known,
    Future<void> Function(String) open,
    Future<Map<String, dynamic>> Function() create,
  ) async {
    await _guard(() async {
      if (known != null && known.isNotEmpty && await File(known).exists()) {
        await open(known);
        return;
      }
      final path = (await create())['path'] as String?;
      if (path != null) await open(path);
      await refresh();
    });
  }

  /// Looks up the release each PHP download would fetch, so the offers can
  /// name it. Quiet: offline, the offers keep their minor version, which is
  /// no reason for a notice (features/php-runtime.feature).
  Future<void> checkPhpReleases() async {
    _set(() => _pending++);
    try {
      final result = await _require().call(Method.phpReleases);
      _set(() => state = WharfState.fromJson(result));
    } catch (_) {
    } finally {
      _set(() => _pending--);
    }
  }

  Future<void> rescanPhp() async {
    await _guard(() async {
      await _require().callList(Method.detectPhp);
      await refresh();
    });
  }

  /// Opens a folder or file with [opener]. The file manager can take a moment
  /// to appear, so the window shows it is working meanwhile, and a failure
  /// becomes a notice like any other.
  Future<void> open(Future<void> Function(String) opener, String path) =>
      _guard(() => opener(path));

  /// Registers a folder in www/ by name, with the config template detected
  /// in it.
  Future<void> addProject(String name) => _add({'name': name});

  /// Registers a folder from anywhere, as it is — the tray's "Add project…",
  /// which opens no window. One outside www/ stays where it is
  /// (features/project-folders.feature).
  Future<void> addFolder(String path) => _add({'path': path});

  Future<void> _add(Map<String, dynamic> params) async {
    await _guard(() async {
      await _require().call(Method.projectAdd, params);
      await refresh();
    });
  }

  /// What "Add project…" proposes for the folder at [path], or null — with a
  /// notice saying why — when the daemon cannot look at it.
  Future<FolderProposal?> inspectFolder(String path) async {
    FolderProposal? proposal;
    await _guard(() async {
      proposal = FolderProposal.fromJson(
        await _require().call(Method.projectInspect, {'path': path}),
      );
    });
    return proposal;
  }

  /// Registers the folder at [path] as the "Add project…" sheet confirmed it:
  /// under [name], with [template] (empty for none), pinned to [webserver]
  /// unless that is empty, and started unless [start] is false. Throws, so
  /// the sheet can say what is wrong — a name that is taken — and stay open.
  Future<Project> addFolderAs(
    String path, {
    required String name,
    required String template,
    String webserver = '',
    bool start = true,
  }) async {
    _set(() => _pending++);
    try {
      final result = await _require().call(Method.projectAdd, {
        'path': path,
        'name': name,
        'template': template,
        if (webserver.isNotEmpty) 'webserver': webserver,
        'start': start,
      });
      await refresh();
      return Project.fromJson(result);
    } finally {
      _set(() => _pending--);
    }
  }

  /// Applies per-project overrides. A null field leaves a setting alone; an
  /// empty string clears an override and reverts to the global default
  /// (features/app-configuration.feature).
  Future<void> updateSettings(
    String name, {
    String? webserverOverride,
    String? phpOverride,
    bool? ssl,
    String? template,
  }) async {
    await _guard(() async {
      await _require().call(Method.projectSettings, {
        'name': name,
        // INFO: A key that is absent leaves that setting alone; an empty string
        // clears an override and reverts to the global default.
        'settings': {
          'webserver_override': ?webserverOverride,
          'php_version': ?phpOverride,
          'ssl': ?ssl,
          'template': ?template,
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

  /// A config template's rules for one webserver, for the editor. Throws, so
  /// the editor can say why it has nothing to show.
  Future<String> readConfigTemplate(String id, String webserver) async {
    final result = await _require().call(Method.configTemplateRead, {
      'id': id,
      'webserver': webserver,
    });
    return result['content'] as String? ?? '';
  }

  /// Saves a config template's rules for one webserver; the daemon restarts
  /// what serves the projects using it. Throws, so the editor stays open on a
  /// failed save (features/config-templates.feature).
  Future<void> saveConfigTemplate(String id, String webserver, String content) =>
      _attempt(Method.configTemplateSave, {'id': id, 'webserver': webserver, 'content': content});

  /// Creates a config template from a typed name and returns its id. Throws,
  /// so the name window can say a name is taken.
  Future<String> createConfigTemplate(String name) async {
    final result = await _require().call(Method.configTemplateCreate, {'name': name});
    await refresh();
    return result['id'] as String? ?? '';
  }

  /// A project's custom config for the webserver serving it, or its config
  /// template's rules to start from. Throws, so the editor can say why it has
  /// nothing to show (features/app-configuration.feature).
  Future<CustomConfigRules> readCustomConfig(String project) async {
    final result = await _require().call(Method.customConfigRead, {'name': project});
    return CustomConfigRules.fromJson(result);
  }

  /// Saves a project's custom config; the daemon refuses rules without
  /// Wharf's placeholders, or for a webserver no longer serving the project.
  /// Throws, so the editor stays open on a failed save.
  Future<void> saveCustomConfig(String project, String webserver, String content) => _attempt(
    Method.customConfigSave,
    {'name': project, 'webserver': webserver, 'content': content},
  );

  /// Deletes a project's custom config: its config template applies again.
  Future<void> deleteCustomConfig(String project, String webserver) =>
      _attempt(Method.customConfigDelete, {'name': project, 'webserver': webserver});

  /// Deletes the user's own config template, or restores a built-in one.
  Future<void> deleteConfigTemplate(String id) => _act(Method.configTemplateDelete, {'id': id});

  /// Like [_act], but a failure reaches the caller instead of the notice line.
  Future<void> _attempt(String method, Map<String, dynamic> params) async {
    _set(() => _pending++);
    try {
      final result = await _require().call(method, params);
      _set(() => state = WharfState.fromJson(result));
    } finally {
      _set(() => _pending--);
    }
  }

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
    _set(() => _pending++);
    try {
      await action();
    } on DaemonError catch (e) {
      // INFO: An older daemon still running after an update — or, in development,
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
    } finally {
      _set(() => _pending--);
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
  /// that was already running alone — unless [includingAttached].
  Future<void> shutdown({bool includingAttached = false}) async {
    _shuttingDown = true;
    _retry?.cancel();
    await _client?.close();
    _client = null;
    await launcher.stop(includingAttached: includingAttached);
  }

  /// Stops every service and project, then the daemon too — even one this
  /// app only attached to (features/single-application.feature, "Casting off
  /// from the main window").
  Future<void> castOff() async {
    await stopAll();
    await shutdown(includingAttached: true);
  }

  @override
  void dispose() {
    _disposed = true;
    _retry?.cancel();
    _client?.close();
    super.dispose();
  }
}
