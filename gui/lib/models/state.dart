/// The snapshot the daemon publishes. The tray menu and the main window render
/// the same object, so they cannot disagree about what is running
/// (features/tray-actions.feature, "Opening the main window").
class WharfState {
  const WharfState({
    required this.root,
    required this.www,
    required this.config,
    required this.domain,
    required this.services,
    required this.projects,
    required this.unregistered,
    required this.ssl,
    required this.busy,
    this.appearance = 'system',
    this.version = '',
  });

  final String root;

  /// The running Wharf's version, as wharfd was built; empty while no daemon
  /// has answered (features/settings.feature).
  final String version;

  /// 'system', 'light' or 'dark' — a setting in wharf.json like any other
  /// (features/settings.feature).
  final String appearance;

  /// Where projects go by default, and the config folder — both of which the
  /// GUI offers to open (features/project-folders.feature).
  final String www;
  final String config;
  final String domain;
  final Services services;
  final List<Project> projects;

  /// Folders sitting in www/ that are not projects yet — "drop a folder in
  /// www/" made visible.
  final List<String> unregistered;

  /// Whether mkcert is installed and its authority trusted
  /// (features/local-ssl.feature).
  final SslStatus ssl;
  final bool busy;

  static const empty = WharfState(
    root: '',
    www: '',
    config: '',
    domain: 'localhost',
    services: Services.empty,
    projects: [],
    unregistered: [],
    ssl: SslStatus.empty,
    busy: false,
  );

  bool get anyRunning => projects.any((p) => p.isRunning) || services.webserver.isRunning;

  factory WharfState.fromJson(Map<String, dynamic> json) => WharfState(
    root: json['root'] as String? ?? '',
    version: json['version'] as String? ?? '',
    appearance: json['appearance'] as String? ?? 'system',
    www: json['www'] as String? ?? '',
    config: json['config'] as String? ?? '',
    domain: json['domain'] as String? ?? 'localhost',
    services: Services.fromJson(json['services'] as Map<String, dynamic>? ?? const {}),
    projects: (json['projects'] as List<dynamic>? ?? const [])
        .map((e) => Project.fromJson(e as Map<String, dynamic>))
        .toList(),
    unregistered: (json['unregistered'] as List<dynamic>? ?? const [])
        .map((e) => e as String)
        .toList(),
    ssl: SslStatus.fromJson(json['ssl'] as Map<String, dynamic>? ?? const {}),
    busy: json['busy'] as bool? ?? false,
  );
}

/// SSL as a whole: neither part is needed to turn SSL on, because the switch
/// sets up whatever is missing.
class SslStatus {
  const SslStatus({required this.installed, required this.trusted, required this.caRoot});

  final bool installed;

  /// Browsers accept Wharf's certificates without a warning.
  final bool trusted;
  final String caRoot;

  static const empty = SslStatus(installed: false, trusted: false, caRoot: '');

  factory SslStatus.fromJson(Map<String, dynamic> json) => SslStatus(
    installed: json['installed'] as bool? ?? false,
    trusted: json['trusted'] as bool? ?? false,
    caRoot: json['ca_root'] as String? ?? '',
  );
}

/// Only Webserver and PHP exist in v1: database and mail are roadmap and are
/// not surfaced at all (features/settings.feature).
class Services {
  const Services({required this.webserver, required this.php});

  final Webserver webserver;
  final Php php;

  static const empty = Services(webserver: Webserver.empty, php: Php.empty);

  factory Services.fromJson(Map<String, dynamic> json) => Services(
    webserver: Webserver.fromJson(json['webserver'] as Map<String, dynamic>? ?? const {}),
    php: Php.fromJson(json['php'] as Map<String, dynamic>? ?? const {}),
  );
}

class Webserver {
  const Webserver({
    required this.active,
    required this.available,
    required this.state,
    required this.switching,
    required this.error,
    this.servers = const [],
  });

  final String active;
  final List<String> available;
  final String state;

  /// True while a swap is in flight, which the GUI shows as "switching…"
  /// (features/service-management.feature, "Port conflict on switch").
  final bool switching;
  final String error;

  /// Each available webserver: installed or not, and what it serves
  /// (features/settings.feature).
  final List<Server> servers;

  static const empty = Webserver(
    active: '',
    available: [],
    state: 'stopped',
    switching: false,
    error: '',
  );

  bool get isRunning => state == 'running';

  factory Webserver.fromJson(Map<String, dynamic> json) => Webserver(
    active: json['active'] as String? ?? '',
    available: (json['available'] as List<dynamic>? ?? const []).map((e) => e as String).toList(),
    state: json['state'] as String? ?? 'stopped',
    switching: json['switching'] as bool? ?? false,
    error: json['error'] as String? ?? '',
    servers: (json['servers'] as List<dynamic>? ?? const [])
        .map((e) => Server.fromJson(e as Map<String, dynamic>))
        .toList(),
  );
}

class Server {
  const Server({
    required this.name,
    required this.installed,
    required this.binary,
    required this.projects,
    this.version = '',
    this.source = '',
    this.installable = false,
    this.installHint = '',
    this.installing = false,
  });

  final String name;
  final bool installed;

  /// The copy in use, or where Wharf's own copy would go.
  final String binary;
  final List<String> projects;
  final String version;

  /// 'wharf', 'homebrew' or 'system'.
  final String source;

  /// Whether Wharf can install it here, and what it will do — or, when it
  /// cannot, what the user can do (features/webserver-install.feature).
  final bool installable;
  final String installHint;
  final bool installing;

  factory Server.fromJson(Map<String, dynamic> json) {
    final install = json['install'] as Map<String, dynamic>? ?? const {};
    return Server(
      name: json['name'] as String? ?? '',
      installed: json['installed'] as bool? ?? false,
      binary: json['binary'] as String? ?? '',
      projects: (json['projects'] as List<dynamic>? ?? const []).map((e) => e as String).toList(),
      version: json['version'] as String? ?? '',
      source: json['source'] as String? ?? '',
      installable: install['installable'] as bool? ?? false,
      installHint: install['hint'] as String? ?? '',
      installing: json['installing'] as bool? ?? false,
    );
  }
}

class Php {
  const Php({
    required this.version,
    required this.available,
    required this.installs,
    required this.recommended,
    required this.status,
    this.dir = '',
    this.downloadable = const [],
    this.downloading = const [],
    this.settings = '',
    this.settingsExist = false,
  });

  final String version;
  final List<String> available;

  /// Every PHP found on this machine, for the version picker
  /// (features/php-runtime.feature).
  final List<PhpInstall> installs;

  /// The newest version in active support — what a first run selects when
  /// nothing is installed.
  final String recommended;

  /// Support status of the selected version.
  final String status;

  /// bin/php, where downloads go.
  final String dir;

  /// Supported versions not installed yet, and those being downloaded now
  /// (features/php-runtime.feature).
  final List<PhpDownload> downloadable;
  final List<String> downloading;

  /// config/php.ini, the user's own PHP settings, and whether it exists yet
  /// (features/php-settings.feature).
  final String settings;
  final bool settingsExist;

  static const empty = Php(
    version: '',
    available: [],
    installs: [],
    recommended: '',
    status: 'unknown',
  );

  factory Php.fromJson(Map<String, dynamic> json) => Php(
    version: json['version'] as String? ?? '',
    available: (json['available'] as List<dynamic>? ?? const []).map((e) => e as String).toList(),
    installs: (json['installs'] as List<dynamic>? ?? const [])
        .map((e) => PhpInstall.fromJson(e as Map<String, dynamic>))
        .toList(),
    recommended: json['recommended'] as String? ?? '',
    status: json['status'] as String? ?? 'unknown',
    dir: json['dir'] as String? ?? '',
    downloadable: (json['downloadable'] as List<dynamic>? ?? const [])
        .map((e) => PhpDownload.fromJson(e as Map<String, dynamic>))
        .toList(),
    downloading: (json['downloading'] as List<dynamic>? ?? const [])
        .map((e) => e as String)
        .toList(),
    settings: json['settings'] as String? ?? '',
    settingsExist: json['settings_exist'] as bool? ?? false,
  );

  /// Whether the selected version is actually present on the machine.
  bool get selectedIsInstalled => installs.any((i) => i.version == version && i.servable);
}

/// A version the picker offers to download.
class PhpDownload {
  const PhpDownload({required this.version, required this.status, this.fullVersion = ''});
  final String version;
  final String status;

  /// The release a download would fetch, e.g. "8.5.1"; empty until the
  /// daemon has looked it up (features/php-runtime.feature).
  final String fullVersion;

  factory PhpDownload.fromJson(Map<String, dynamic> json) => PhpDownload(
    version: json['version'] as String? ?? '',
    status: json['status'] as String? ?? 'unknown',
    fullVersion: json['full_version'] as String? ?? '',
  );
}

/// One PHP installation found on this machine.
class PhpInstall {
  const PhpInstall({
    required this.version,
    required this.fullVersion,
    required this.dir,
    required this.fastcgi,
    required this.source,
    required this.status,
  });

  final String version;
  final String fullVersion;
  final String dir;
  final String fastcgi;

  /// 'vendored' for a build under bin/php/, 'system' for one the tool adopted.
  final String source;

  /// 'active', 'security', 'eol', 'unreleased' or 'unknown'.
  final String status;

  /// A CLI-only build cannot serve requests: nginx needs FastCGI.
  bool get servable => fastcgi.isNotEmpty;

  factory PhpInstall.fromJson(Map<String, dynamic> json) => PhpInstall(
    version: json['version'] as String? ?? '',
    fullVersion: json['full_version'] as String? ?? '',
    dir: json['dir'] as String? ?? '',
    fastcgi: json['fastcgi'] as String? ?? '',
    source: json['source'] as String? ?? '',
    status: json['status'] as String? ?? 'unknown',
  );
}

/// Something the user can do to a project from its row or the tray.
enum ProjectAction {
  start('Start'),
  stop('Stop'),
  restart('Restart');

  const ProjectAction(this.label);
  final String label;
}

/// One row in the project list: name, status, URL. Nothing else is shown by
/// default (dev/design-principles.md §2).
class Project {
  const Project({
    required this.name,
    required this.state,
    required this.url,
    required this.webserver,
    required this.webserverOverride,
    required this.phpVersion,
    required this.phpOverride,
    required this.ssl,
    required this.port,
    required this.error,
    this.dir = '',
    this.linked = false,
    this.logDir = '',
    this.customConfigs = const [],
    this.webserverVersion = '',
    this.phpFullVersion = '',
  });

  final String name;
  final String state;
  /// `<name>.localhost`, with no port whichever webserver serves the project
  /// (features/pretty-urls.feature).
  final String url;

  final String webserver;
  final String? webserverOverride;
  final String phpVersion;
  final String? phpOverride;

  /// What the serving binaries reported, e.g. "1.27.3" and "8.3.14"; empty
  /// when one did not say (features/app-configuration.feature, "A running
  /// project shows what serves it").
  final String webserverVersion;
  final String phpFullVersion;

  /// "nginx 1.27.3 · PHP 8.3.14", falling back to the configured version
  /// where a binary did not report its own.
  String get servedBy {
    final server = webserverVersion.isEmpty ? webserver : '$webserver $webserverVersion';
    final php = phpFullVersion.isEmpty ? phpVersion : phpFullVersion;
    return '$server · PHP $php';
  }
  final bool ssl;
  final int port;
  final String error;

  /// The project's folder; [linked] when it is not in www/.
  final String dir;
  final bool linked;

  /// The project's own log folder (features/project-logs.feature).
  final String logDir;

  /// One per webserver (features/app-configuration.feature).
  final List<CustomConfig> customConfigs;

  bool get isRunning => state == 'running';
  bool get isBusy => state == 'starting' || state == 'stopping';
  bool get hasFailed => state == 'failed';
  bool get hasOverrides => webserverOverride != null || phpOverride != null || ssl;

  /// What the project offers to do next. The row and the tray submenu both
  /// read this, so they offer the same actions (features/tray-actions.feature,
  /// "Every project offers the actions that fit its state"). A project on its
  /// way up or down offers nothing until it gets there.
  List<ProjectAction> get actions {
    if (isBusy) return const [];
    if (isRunning) return const [ProjectAction.stop, ProjectAction.restart];
    if (hasFailed) return const [ProjectAction.start, ProjectAction.restart];
    return const [ProjectAction.start];
  }

  factory Project.fromJson(Map<String, dynamic> json) => Project(
    name: json['name'] as String? ?? '',
    state: json['state'] as String? ?? 'stopped',
    url: json['url'] as String? ?? '',
    webserver: json['webserver'] as String? ?? '',
    webserverOverride: json['webserver_override'] as String?,
    phpVersion: json['php_version'] as String? ?? '',
    phpOverride: json['php_override'] as String?,
    webserverVersion: json['webserver_version'] as String? ?? '',
    phpFullVersion: json['php_full_version'] as String? ?? '',
    ssl: json['ssl'] as bool? ?? false,
    port: json['port'] as int? ?? 0,
    error: json['error'] as String? ?? '',
    dir: json['dir'] as String? ?? '',
    linked: json['linked'] as bool? ?? false,
    logDir: json['log_dir'] as String? ?? '',
    customConfigs: (json['custom_configs'] as List<dynamic>? ?? const [])
        .map((e) => CustomConfig.fromJson(e as Map<String, dynamic>))
        .toList(),
  );
}

/// A project's custom directives for one webserver.
class CustomConfig {
  const CustomConfig({
    required this.webserver,
    required this.path,
    required this.exists,
    required this.active,
  });

  final String webserver;
  final String path;
  final bool exists;

  /// This is the file of the webserver serving the project now.
  final bool active;

  factory CustomConfig.fromJson(Map<String, dynamic> json) => CustomConfig(
    webserver: json['webserver'] as String? ?? '',
    path: json['path'] as String? ?? '',
    exists: json['exists'] as bool? ?? false,
    active: json['active'] as bool? ?? false,
  );
}

/// A quick-app template (features/quick-app-php.feature).
class Template {
  const Template({required this.id, required this.name, required this.runtime});
  final String id;
  final String name;
  final String runtime;

  factory Template.fromJson(Map<String, dynamic> json) => Template(
    id: json['id'] as String? ?? '',
    name: json['name'] as String? ?? '',
    runtime: json['runtime'] as String? ?? '',
  );
}
