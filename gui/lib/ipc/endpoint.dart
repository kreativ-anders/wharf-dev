import 'dart:convert';
import 'dart:io';

/// Where the daemon is listening.
///
/// The daemon writes this to `<root>/data/wharf.endpoint` on start. Reading it
/// is how every client finds the daemon: no hard-coded socket path, no guessed
/// port. See dev/architecture.md §4a.
class Endpoint {
  const Endpoint({required this.transport, this.path = '', this.address = '', this.token = ''});

  /// 'unix' on macOS and Linux, 'tcp' on Windows — where dart:io has no unix
  /// domain sockets even though the OS does.
  final String transport;
  final String path;
  final String address;
  final String token;

  static const fileName = 'wharf.endpoint';

  factory Endpoint.fromJson(Map<String, dynamic> json) => Endpoint(
    transport: json['transport'] as String? ?? '',
    path: json['path'] as String? ?? '',
    address: json['address'] as String? ?? '',
    token: json['token'] as String? ?? '',
  );

  /// Reads the endpoint file, or throws if the daemon has not published one.
  static Future<Endpoint> read(String path) async {
    final file = File(path);
    if (!await file.exists()) {
      throw DaemonNotRunning(path);
    }
    final body = await file.readAsString();
    final ep = Endpoint.fromJson(jsonDecode(body) as Map<String, dynamic>);
    if (ep.transport.isEmpty) {
      throw FormatException('endpoint file names no transport', body);
    }
    return ep;
  }

  /// Opens a connection on whichever transport the daemon published.
  Future<Socket> connect() {
    if (transport == 'tcp') {
      final parts = address.split(':');
      return Socket.connect(parts[0], int.parse(parts.last));
    }
    return Socket.connect(InternetAddress(path, type: InternetAddressType.unix), 0);
  }
}

/// Thrown when no endpoint file exists — the daemon is not running.
class DaemonNotRunning implements Exception {
  DaemonNotRunning(this.endpointPath);
  final String endpointPath;

  @override
  String toString() => 'The Wharf daemon is not running.';
}

/// Resolves the root folder the same way the daemon does: WHARF_ROOT, else
/// ~/Wharf. Both sides must agree or the GUI looks in the wrong place.
String defaultRoot() {
  final env = Platform.environment['WHARF_ROOT'];
  if (env != null && env.isNotEmpty) return env;
  return usualRoot();
}

/// ~/Wharf: the root when WHARF_ROOT names none.
String usualRoot() {
  // WARNING: The same order as Go's os.UserHomeDir. On Windows a HOME set by
  // Git Bash or MSYS would otherwise point the window at another folder than
  // the daemon it attaches to.
  final home = Platform.isWindows
      ? Platform.environment['USERPROFILE']
      : Platform.environment['HOME'];
  return '${home ?? '.'}${Platform.pathSeparator}Wharf';
}

String endpointPathFor(String root) =>
    '$root${Platform.pathSeparator}data${Platform.pathSeparator}${Endpoint.fileName}';
