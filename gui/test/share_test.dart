import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:wharf_gui/daemon.dart';
import 'package:wharf_gui/models/state.dart';
import 'package:wharf_gui/pages/projects_page.dart';
import 'package:wharf_gui/theme.dart';

/// The main window as the app shows it: redrawn with every snapshot.
Widget _window(Daemon daemon) => MaterialApp(
  theme: wharfTheme(Brightness.light),
  home: ListenableBuilder(listenable: daemon, builder: (_, _) => ProjectsPage(daemon: daemon)),
);

WharfState _state(String json) => WharfState.fromJson(jsonDecode(json) as Map<String, dynamic>);

/// A daemon that shares a project the way the real one answers: its next
/// snapshot has the project shared. It records every share it is asked for.
class _ShareDaemon extends Daemon {
  _ShareDaemon(String before, this.after) : super(root: '/tmp/wharf-test') {
    state = _state(before);
  }

  final String after;
  final calls = <String>[];

  @override
  Future<void> shareProject(String name, {bool on = true}) async {
    calls.add('$name ${on ? 'on' : 'off'}');
    state = _state(on ? after : _running);
    notifyListeners();
  }
}

Finder _qr(String url) => find.byKey(ValueKey('qr:$url'));

void main() {
  // features/sharing.feature — "Sharing a running project on the network"
  testWidgets('Share shares the project and shows its URL as a QR code and as text', (tester) async {
    final daemon = _ShareDaemon(_running, _sharedPlain);
    await tester.pumpWidget(_window(daemon));

    await tester.tap(find.byTooltip('Share my-kirby-site on your network'));
    await tester.pumpAndSettle();

    expect(daemon.calls, ['my-kirby-site on']);
    expect(_qr('http://192.168.1.23:8800'), findsOneWidget);
    expect(find.text('http://192.168.1.23:8800'), findsOneWidget);

    // INFO: Stopping sharing leaves the project running, and the row says it
    // is no longer shared.
    await tester.tap(find.text('Stop sharing'));
    await tester.pumpAndSettle();
    expect(daemon.calls, ['my-kirby-site on', 'my-kirby-site off']);
    expect(find.textContaining('Shared on your network'), findsNothing);
  });

  // features/sharing.feature — "The certificate step is shown only for a
  // project with SSL"
  testWidgets('the certificate step is shown only for a project with SSL', (tester) async {
    // INFO: When the user shares a project without SSL, Then the GUI shows its
    // URL alone.
    final plain = _ShareDaemon(_sharedPlain, _sharedPlain);
    await tester.pumpWidget(_window(plain));
    expect(find.text('Shared on your network: http://192.168.1.23:8800'), findsOneWidget);
    await tester.tap(find.byTooltip('Show my-kirby-site on a phone'));
    await tester.pumpAndSettle();
    expect(plain.calls, isEmpty, reason: 'a shared project is shown, not shared again');
    expect(find.byWidgetPredicate((w) => w.key is ValueKey<String> &&
        (w.key! as ValueKey<String>).value.startsWith('qr:')), findsOneWidget);
    expect(find.textContaining('network certificate'), findsNothing);
    await tester.tap(find.text('Done'));
    await tester.pumpAndSettle();

    // INFO: When the user shares a project with SSL, Then the GUI first offers
    // the network certificate as a QR code, with its fingerprint to compare on
    // the phone, And says how to install it on iOS and Android.
    tester.view.physicalSize = const Size(1000, 1200);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final ssl = _ShareDaemon(_sharedSsl, _sharedSsl);
    await tester.pumpWidget(_window(ssl));
    await tester.tap(find.byTooltip('Show my-kirby-site on a phone'));
    await tester.pumpAndSettle();
    expect(find.text('1 · Install the network certificate'), findsOneWidget);
    expect(find.text('2 · Open the project'), findsOneWidget);
    expect(_qr('http://192.168.1.23/wharf-network-ca.crt'), findsOneWidget);
    expect(_qr('https://192.168.1.23:8800'), findsOneWidget);
    expect(find.textContaining('AB CD EF'), findsOneWidget);
    expect(find.textContaining('iPhone, iPad'), findsOneWidget);
    expect(find.textContaining('Android'), findsOneWidget);
    // INFO: The QR code is the certificate's step first, the project's second.
    expect(
      tester.getTopLeft(_qr('http://192.168.1.23/wharf-network-ca.crt')).dx,
      lessThan(tester.getTopLeft(_qr('https://192.168.1.23:8800')).dx),
    );
  });

  test('the snapshot carries sharing', () {
    final state = _state(_sharedSsl);
    final p = state.projects.single;
    expect(p.shared, isTrue);
    expect(p.shareUrl, 'https://192.168.1.23:8800');
    expect(state.network.address, '192.168.1.23');
    expect(state.network.certificateUrl, 'http://192.168.1.23/wharf-network-ca.crt');
    expect(state.network.certificate?.expires, DateTime.utc(2027, 9, 26, 12));
    expect(_state(_running).network.certificate, isNull);
  });
}

const _running = '''
{
  "root": "/Users/x/Wharf",
  "services": {"webserver": {"active": "nginx", "available": ["nginx"], "state": "running"}},
  "projects": [
    {"name": "my-kirby-site", "state": "running", "url": "http://my-kirby-site.localhost",
     "webserver": "nginx", "php_version": "8.4", "port": 8080}
  ],
  "unregistered": []
}
''';

const _sharedPlain = '''
{
  "root": "/Users/x/Wharf",
  "services": {"webserver": {"active": "nginx", "available": ["nginx"], "state": "running"}},
  "network": {"address": "192.168.1.23"},
  "projects": [
    {"name": "my-kirby-site", "state": "running", "url": "http://my-kirby-site.localhost",
     "webserver": "nginx", "php_version": "8.4", "port": 8080,
     "shared": true, "share_url": "http://192.168.1.23:8800"}
  ],
  "unregistered": []
}
''';

const _sharedSsl = '''
{
  "root": "/Users/x/Wharf",
  "services": {"webserver": {"active": "nginx", "available": ["nginx"], "state": "running"}},
  "network": {
    "address": "192.168.1.23",
    "certificate_url": "http://192.168.1.23/wharf-network-ca.crt",
    "certificate": {
      "fingerprint": "AB CD EF 01 23 45 67 89 AB CD EF 01 23 45 67 89 AB CD EF 01 23 45 67 89 AB CD EF 01 23 45 67 89",
      "expires": "2027-09-26T12:00:00Z",
      "file": "/Users/x/Wharf/data/network-ca/public/wharf-network-ca.crt"
    }
  },
  "projects": [
    {"name": "my-kirby-site", "state": "running", "url": "https://my-kirby-site.localhost", "ssl": true,
     "webserver": "nginx", "php_version": "8.4", "port": 8080,
     "shared": true, "share_url": "https://192.168.1.23:8800"}
  ],
  "unregistered": []
}
''';
