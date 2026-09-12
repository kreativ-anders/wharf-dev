import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'endpoint.dart';

/// An error the daemon returned, carrying a code the GUI can branch on.
class DaemonError implements Exception {
  DaemonError(this.code, this.message);
  final String code;
  final String message;

  /// The elevation prompt was declined. Not a failure: the action finished
  /// without what the prompt would have added — SSL works with a browser
  /// warning (features/local-ssl.feature).
  bool get isElevationDenied => code == 'elevation_denied';

  /// A binary the daemon needs is not installed. The message names the path.
  bool get isMissingBinary => code == 'missing_binary';

  /// A template could not be downloaded.
  bool get isOffline => code == 'offline';

  /// The daemon does not know the method: it is older than this app.
  bool get isUnknownMethod => code == 'unknown_method';

  @override
  String toString() => message;
}

/// A connection to the daemon: newline-delimited JSON, one object per line.
///
/// Requests carry an id and are answered by exactly one response; frames
/// without an id are unsolicited events.
class IpcClient {
  IpcClient._(this._socket, this._events);

  final Socket _socket;
  final StreamController<Map<String, dynamic>> _events;
  final _pending = <String, Completer<Map<String, dynamic>>>{};
  var _nextId = 0;
  var _closed = false;

  /// Events pushed by the daemon, each a full state snapshot.
  Stream<Map<String, dynamic>> get events => _events.stream;

  /// Fires when the connection ends, so the app can show "reconnecting".
  final _done = Completer<void>();
  Future<void> get done => _done.future;

  static Future<IpcClient> connect(Endpoint endpoint) async {
    final socket = await endpoint.connect();
    final events = StreamController<Map<String, dynamic>>.broadcast();
    final client = IpcClient._(socket, events);
    client._listen();

    // Loopback TCP is not authorisation on its own; the token from the
    // endpoint file is.
    if (endpoint.token.isNotEmpty) {
      await client.call('auth', {'token': endpoint.token});
    }
    return client;
  }

  void _listen() {
    _socket
        .cast<List<int>>()
        .transform(utf8.decoder)
        .transform(const LineSplitter())
        .listen(_onLine, onError: (Object e) => _shutdown(e), onDone: () => _shutdown(null));
  }

  void _onLine(String line) {
    // Lines already buffered by the socket can arrive after close; delivering
    // them would add to a closed stream.
    if (_closed || line.trim().isEmpty) return;
    final Map<String, dynamic> frame;
    try {
      frame = jsonDecode(line) as Map<String, dynamic>;
    } on FormatException {
      return; // A malformed line is not worth tearing the connection down for.
    }

    final event = frame['event'] as String?;
    if (event != null && event.isNotEmpty) {
      if (!_events.isClosed) _events.add(frame);
      return;
    }
    final completer = _pending.remove(frame['id'] as String?);
    if (completer != null && !completer.isCompleted) {
      completer.complete(frame);
    }
  }

  void _shutdown(Object? error) {
    if (_closed) return;
    _closed = true;
    for (final completer in _pending.values) {
      if (!completer.isCompleted) {
        completer.completeError(error ?? const SocketException('daemon connection closed'));
      }
    }
    _pending.clear();
    _events.close();
    if (!_done.isCompleted) _done.complete();
  }

  /// Sends one request and returns its result.
  Future<Map<String, dynamic>> call(String method, [Map<String, dynamic>? params]) async {
    if (_closed) throw const SocketException('daemon connection closed');

    final id = (++_nextId).toString();
    final completer = Completer<Map<String, dynamic>>();
    _pending[id] = completer;

    _socket.write('${jsonEncode({'id': id, 'method': method, 'params': ?params})}\n');

    final frame = await completer.future;
    final error = frame['error'] as Map<String, dynamic>?;
    if (error != null) {
      throw DaemonError(
        error['code'] as String? ?? 'internal',
        error['message'] as String? ?? 'unknown error',
      );
    }
    return (frame['result'] as Map<String, dynamic>?) ?? const {};
  }

  /// Sends a request whose result is a list rather than an object.
  Future<List<dynamic>> callList(String method, [Map<String, dynamic>? params]) async {
    if (_closed) throw const SocketException('daemon connection closed');
    final id = (++_nextId).toString();
    final completer = Completer<Map<String, dynamic>>();
    _pending[id] = completer;
    _socket.write('${jsonEncode({'id': id, 'method': method, 'params': ?params})}\n');

    final frame = await completer.future;
    final error = frame['error'] as Map<String, dynamic>?;
    if (error != null) {
      throw DaemonError(
        error['code'] as String? ?? 'internal',
        error['message'] as String? ?? 'unknown error',
      );
    }
    return (frame['result'] as List<dynamic>?) ?? const [];
  }

  Future<void> close() async {
    _shutdown(null);
    await _socket.close();
  }
}
