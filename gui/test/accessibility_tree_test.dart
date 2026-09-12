// Replays every semantics update the framework sends through the commit the
// Windows engine performs (shell/platform/common/accessibility_bridge.cc
// feeding ui::AXTree), so a sequence that makes Windows log "Failed to
// update ui::AXTree" fails here instead of scrolling past in a run's log.
import 'dart:convert';
import 'dart:typed_data';
import 'dart:ui' as ui;

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:wharf_gui/daemon.dart';
import 'package:wharf_gui/models/state.dart';
import 'package:wharf_gui/pages/projects_page.dart';
import 'package:wharf_gui/theme.dart';

void main() {
  _AxBinding();
  setUp(_engine.reset);

  test('the engine model rejects what Windows rejects', () {
    _Node n(List<int> children) => _Node(children, '');
    _engine.commit({0: n([1]), 1: n([2]), 2: n([3]), 3: n([])});
    expect(_engine.errors, isEmpty);

    // 1 moves under a new node 4. 2 is unchanged and not resent, but 3 is:
    // taking 1 off its old parent took 2 and 3 with it, and 3 has no parent.
    _engine.commit({0: n([4]), 4: n([1]), 1: n([2]), 3: n([])});
    expect(_engine.errors.single, startsWith('3 will not be in the tree and is not the new root'));
  });

  testWidgets('hovering each control in the main window', (tester) async {
    final semantics = tester.ensureSemantics();
    await _app(tester, _FakeDaemon());
    final mouse = await _mouse(tester);

    for (final tip in [
      'Open www folder',
      'Add folder…',
      'Settings',
      'Open http://my-kirby-site.localhost',
      'Restart my-kirby-site',
      'Settings for my-kirby-site',
      'Stop my-kirby-site',
      'Start legacy-app',
      'Stop everything and quit Wharf',
    ]) {
      await mouse.moveTo(tester.getCenter(find.byTooltip(tip).first));
      await _wait(tester);
      await mouse.moveTo(_nowhere);
      await _wait(tester);
    }
    for (final dot in find.byType(StatusDot).evaluate().toList()) {
      await mouse.moveTo(tester.getCenter(find.byWidget(dot.widget)));
      await _wait(tester);
    }
    await mouse.moveTo(tester.getCenter(find.byTooltip('Settings').first));
    await _wait(tester);
    expect(find.text('Settings'), findsOneWidget, reason: 'the tooltip is showing');

    expect(_engine.errors, isEmpty);
    semantics.dispose();
  }, variant: _windows);

  // Opening the tray menu, closing to the tray or switching to another
  // window each leave the window inactive, and focus is put away until it
  // comes back.
  for (final (name, open) in <(String, Future<void> Function(WidgetTester))>[
    ('the main window', (_) async {}),
    ('a tooltip', (tester) async {
      final mouse = await _mouse(tester);
      await mouse.moveTo(tester.getCenter(find.byTooltip('Stop my-kirby-site').first));
    }),
    ('the project sheet', (tester) => tester.tap(find.text('my-kirby-site'))),
    ('the new project dialog', (tester) => tester.tap(find.text('New project'))),
    ('settings', (tester) => tester.tap(find.byTooltip('Settings'))),
  ]) {
    testWidgets('the window deactivated and back, over $name', (tester) async {
      final semantics = tester.ensureSemantics();
      await _app(tester, _FakeDaemon());
      await open(tester);
      await _wait(tester);

      for (final states in [
        [AppLifecycleState.inactive, AppLifecycleState.resumed],
        [AppLifecycleState.inactive, AppLifecycleState.hidden],
        [AppLifecycleState.inactive, AppLifecycleState.resumed],
      ]) {
        for (final state in states) {
          tester.binding.handleAppLifecycleStateChanged(state);
          await _wait(tester);
        }
      }
      if (tester.binding.lifecycleState == AppLifecycleState.hidden) {
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
        tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
        await _wait(tester);
      }

      expect(_engine.errors, isEmpty);
      semantics.dispose();
    }, variant: _windows);
  }

  // A minimised window can hand the view no room at all, which culls every
  // node, and restoring it brings them back.
  for (final (name, open) in <(String, Future<void> Function(WidgetTester))>[
    ('the main window', (_) async {}),
    ('the project sheet', (tester) => tester.tap(find.text('my-kirby-site'))),
    ('settings', (tester) => tester.tap(find.byTooltip('Settings'))),
  ]) {
    testWidgets('the window minimised and restored, over $name', (tester) async {
      final semantics = tester.ensureSemantics();
      await _app(tester, _FakeDaemon());
      await open(tester);
      await _wait(tester);

      // Content that does not fit a tiny window says so; that is not what
      // this is about.
      final report = FlutterError.onError;
      FlutterError.onError = (details) {
        if (!details.toString().contains('overflowed')) report?.call(details);
      };
      for (final size in const [
        Size.zero,
        Size(560, 720),
        Size(1, 1),
        Size(420, 480),
        Size(560, 720),
      ]) {
        tester.view.physicalSize = size;
        await _wait(tester);
      }
      FlutterError.onError = report;

      expect(_engine.errors, isEmpty);
      semantics.dispose();
    }, variant: _windows);
  }

  testWidgets('a long list scrolled under the mouse', (tester) async {
    final semantics = tester.ensureSemantics();
    await _app(tester, _FakeDaemon(extra: 30));
    final mouse = await _mouse(tester);
    final list = tester.getCenter(find.byType(ListView));
    await mouse.moveTo(list);
    await _wait(tester);

    final wheel = TestPointer(9, PointerDeviceKind.mouse, 9);
    await tester.sendEventToBinding(wheel.addPointer(location: list));
    for (final dy in const <double>[40, 40, 120, 400, -200, 900, -1500, 2000]) {
      await tester.sendEventToBinding(wheel.scroll(Offset(0, dy)));
      await _wait(tester);
    }
    await tester.sendEventToBinding(wheel.removePointer());

    expect(_engine.errors, isEmpty);
    semantics.dispose();
  }, variant: _windows);

  testWidgets('settings rows that change under the mouse', (tester) async {
    final semantics = tester.ensureSemantics();
    final daemon = _FakeDaemon()
      ..change((json) => _server(json, 'apache')['install'] = {'installable': true, 'hint': 'Downloads Apache'});
    await _app(tester, daemon);
    final mouse = await _mouse(tester);
    await tester.tap(find.byTooltip('Settings'));
    await _wait(tester);

    await tester.tap(find.byKey(const ValueKey('nav:webserver')));
    await _wait(tester);
    await mouse.moveTo(tester.getCenter(find.text('Install apache')));
    await _wait(tester);
    daemon.change((json) => _server(json, 'apache')['installing'] = true);
    await _wait(tester);
    daemon.change(
      (json) => _server(json, 'apache')
        ..['installing'] = false
        ..['installed'] = true
        ..['version'] = '2.4.62',
    );
    await _wait(tester);
    daemon.change((json) => _services(json, 'webserver')['switching'] = true);
    await _wait(tester);
    daemon.change((json) => _services(json, 'webserver')..['switching'] = false..['active'] = 'apache');
    await _wait(tester);

    await tester.tap(find.byKey(const ValueKey('nav:php')));
    await _wait(tester);
    await mouse.moveTo(tester.getCenter(find.widgetWithText(TextButton, 'Download')));
    await _wait(tester);
    daemon.change((json) => _services(json, 'php')['downloading'] = ['8.5']);
    await _wait(tester);
    daemon.change((json) {
      final php = _services(json, 'php');
      php['downloading'] = <String>[];
      php['downloadable'] = <Object>[];
      (php['installs'] as List).add({
        'version': '8.5', 'full_version': '8.5.1', 'dir': '/opt/php85', //
        'source': 'vendored', 'status': 'active',
      });
    });
    await _wait(tester);

    await tester.tap(find.byKey(const ValueKey('nav:ssl')));
    await _wait(tester);
    daemon.change((json) => json['ssl'] = {'installed': true, 'trusted': false});
    await _wait(tester);
    await mouse.moveTo(tester.getCenter(find.text('Trust certificates')));
    await _wait(tester);
    daemon.change((json) => json['ssl'] = {'installed': true, 'trusted': true, 'ca_root': '/ca'});
    await _wait(tester);

    expect(_engine.errors, isEmpty);
    semantics.dispose();
  }, variant: _windows);

  testWidgets('casting off: everything stops behind the closing dialog', (tester) async {
    final semantics = tester.ensureSemantics();
    final daemon = _FakeDaemon();
    await _app(tester, daemon, onCastOff: daemon.stopAll);

    await tester.tap(find.text('Cast off'));
    await _wait(tester);
    await tester.tap(find.text('Stay moored'));
    await _wait(tester);
    await tester.tap(find.text('Cast off'));
    await _wait(tester);
    await tester.tap(find.widgetWithText(FilledButton, 'Cast off'));
    await _wait(tester);

    expect(_engine.errors, isEmpty);
    semantics.dispose();
  }, variant: _windows);

  testWidgets('stop all from the title bar', (tester) async {
    final semantics = tester.ensureSemantics();
    await _app(tester, _FakeDaemon());

    await tester.tap(find.text('Stop all'));
    await _wait(tester);

    expect(_engine.errors, isEmpty);
    semantics.dispose();
  }, variant: _windows);

  testWidgets('removing a project from its sheet', (tester) async {
    final semantics = tester.ensureSemantics();
    await _app(tester, _FakeDaemon());

    await tester.tap(find.text('legacy-app'));
    await _wait(tester);
    await tester.tap(find.text('Remove project'));
    await _wait(tester);
    await tester.tap(find.text('Cancel'));
    await _wait(tester);
    await tester.tap(find.text('Remove project'));
    await _wait(tester);
    await tester.tap(find.widgetWithText(FilledButton, 'Remove'));
    await _wait(tester);

    expect(find.text('legacy-app'), findsNothing);
    expect(_engine.errors, isEmpty);
    semantics.dispose();
  }, variant: _windows);

  testWidgets('starting up: connecting, then the first snapshot', (tester) async {
    final semantics = tester.ensureSemantics();
    final daemon = _FakeDaemon()
      ..state = WharfState.empty
      ..connection = Connection.disconnected
      ..notice = 'wharfd not found';
    await _app(tester, daemon);

    daemon
      ..connection = Connection.connecting
      ..change((_) {});
    await _wait(tester);
    daemon
      ..connection = Connection.connected
      ..notice = null
      ..change((_) {});
    await _wait(tester);

    expect(_engine.errors, isEmpty);
    semantics.dispose();
  }, variant: _windows);

  testWidgets('tabbing through the main window and the sheet', (tester) async {
    final semantics = tester.ensureSemantics();
    await _app(tester, _FakeDaemon());

    for (var i = 0; i < 16; i++) {
      await tester.sendKeyEvent(LogicalKeyboardKey.tab);
      await _wait(tester);
    }
    await tester.tap(find.text('legacy-app'));
    await _wait(tester);
    for (var i = 0; i < 16; i++) {
      await tester.sendKeyEvent(LogicalKeyboardKey.tab);
      await _wait(tester);
    }

    expect(_engine.errors, isEmpty);
    semantics.dispose();
  }, variant: _windows);

  testWidgets('appearance switched, and settings narrowed', (tester) async {
    final semantics = tester.ensureSemantics();
    final daemon = _FakeDaemon();
    await _app(tester, daemon);

    await tester.tap(find.byTooltip('Settings'));
    await _wait(tester);
    for (final mode in ['dark', 'light', 'system']) {
      daemon.change((json) => json['appearance'] = mode);
      await _wait(tester);
    }
    for (final width in [420.0, 800.0, 420.0]) {
      tester.view.physicalSize = Size(width, 720);
      await _wait(tester);
    }
    final mouse = await _mouse(tester);
    await mouse.moveTo(tester.getCenter(find.byKey(const ValueKey('nav:php'))));
    await _wait(tester);

    expect(_engine.errors, isEmpty);
    semantics.dispose();
  }, variant: _windows);

  testWidgets('a row action clicked while its tooltip shows', (tester) async {
    final semantics = tester.ensureSemantics();
    final daemon = _FakeDaemon();
    await _app(tester, daemon);
    final mouse = await _mouse(tester);

    final start = tester.getCenter(find.byTooltip('Start legacy-app').first);
    await mouse.moveTo(start);
    await _wait(tester);
    await mouse.down(start);
    await mouse.up();
    await _wait(tester);
    daemon.change((json) => _project(json, 'legacy-app')['state'] = 'running');
    await _wait(tester);

    expect(_engine.errors, isEmpty);
    semantics.dispose();
  }, variant: _windows);

  testWidgets('the project sheet opens, changes and closes', (tester) async {
    final semantics = tester.ensureSemantics();
    final daemon = _FakeDaemon();
    await _app(tester, daemon);
    final mouse = await _mouse(tester);

    await tester.tap(find.text('my-kirby-site'));
    await _wait(tester);
    await tester.tap(find.byType(Switch));
    await _wait(tester);
    await tester.tap(find.text('PHP 8.4'));
    await _wait(tester);
    await tester.tap(find.text('PHP 8.1').last);
    await _wait(tester);
    await mouse.moveTo(tester.getCenter(find.byTooltip('Close')));
    await _wait(tester);
    await tester.tap(find.byTooltip('Close'));
    await _wait(tester);

    await tester.tap(find.text('legacy-app'));
    await _wait(tester);
    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await _wait(tester);

    expect(_engine.errors, isEmpty);
    semantics.dispose();
  }, variant: _windows);

  testWidgets('every settings page, and back', (tester) async {
    final semantics = tester.ensureSemantics();
    await _app(tester, _FakeDaemon());
    final mouse = await _mouse(tester);

    await tester.tap(find.byTooltip('Settings'));
    await _wait(tester);
    for (final page in ['webserver', 'php', 'ssl', 'general']) {
      await tester.tap(find.byKey(ValueKey('nav:$page')));
      await _wait(tester);
    }
    await tester.tap(find.byKey(const ValueKey('nav:webserver')));
    await _wait(tester);
    await mouse.moveTo(tester.getCenter(find.byType(RadioListTile<String>).first));
    await _wait(tester);
    await tester.pageBack();
    await _wait(tester);

    expect(_engine.errors, isEmpty);
    semantics.dispose();
  }, variant: _windows);

  testWidgets('a new project dialog, filled in and cancelled', (tester) async {
    final semantics = tester.ensureSemantics();
    await _app(tester, _FakeDaemon());

    await tester.tap(find.text('New project'));
    await _wait(tester);
    await tester.enterText(find.byType(TextField), 'My Site');
    await _wait(tester);
    await tester.tap(find.text('Kirby'));
    await _wait(tester);
    await tester.tap(find.text('Kirby').last);
    await _wait(tester);
    await tester.tap(find.text('Cancel'));
    await _wait(tester);

    expect(_engine.errors, isEmpty);
    semantics.dispose();
  }, variant: _windows);

  testWidgets('busy, notice and states come and go', (tester) async {
    final semantics = tester.ensureSemantics();
    final daemon = _FakeDaemon();
    await _app(tester, daemon);

    daemon.change((json) => json['busy'] = true);
    await _wait(tester);
    daemon.change((json) => _project(json, 'my-kirby-site')['state'] = 'stopping');
    await _wait(tester);
    daemon.change((json) {
      json['busy'] = false;
      _project(json, 'my-kirby-site')['state'] = 'stopped';
    });
    await _wait(tester);
    daemon
      ..notice = 'Something went wrong'
      ..change((_) {});
    await _wait(tester);
    await tester.tap(find.byTooltip('Dismiss'));
    await _wait(tester);

    expect(_engine.errors, isEmpty);
    semantics.dispose();
  }, variant: _windows);
}

// ------------------------------------------------------------------ the app

final _nowhere = const Offset(280, 520);

/// The platform whose engine logs the error: tooltips on hover, and focus
/// put away while the window is inactive.
final _windows = TargetPlatformVariant.only(TargetPlatform.windows);

Future<void> _app(
  WidgetTester tester,
  _FakeDaemon daemon, {
  Future<void> Function()? onCastOff,
}) async {
  // The focus manager was made before the platform was overridden.
  FocusManager.instance.listenToApplicationLifecycleChangesIfSupported();
  tester.view.physicalSize = const Size(560, 720);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  // As main.dart builds it: the whole app follows the daemon.
  await tester.pumpWidget(
    ListenableBuilder(
      listenable: daemon,
      builder: (context, _) => MaterialApp(
        themeMode: themeModeFor(daemon.state.appearance),
        theme: wharfTheme(Brightness.light),
        darkTheme: wharfTheme(Brightness.dark),
        home: ProjectsPage(daemon: daemon, onCastOff: onCastOff ?? () async {}),
      ),
    ),
  );
  await _wait(tester);
  expect(_engine.size, greaterThan(10), reason: 'the engine model receives the updates');
}

Future<TestGesture> _mouse(WidgetTester tester) async {
  final mouse = await tester.createGesture(kind: PointerDeviceKind.mouse);
  await mouse.addPointer(location: _nowhere);
  addTearDown(mouse.removePointer);
  return mouse;
}

/// Long enough for a tooltip to wait, fade in, and for a route to finish its
/// transition, a frame at a time so every intermediate update is sent.
Future<void> _wait(WidgetTester tester) async {
  for (var i = 0; i < 20; i++) {
    await tester.pump(const Duration(milliseconds: 100));
  }
}

Map<String, dynamic> _project(Map<String, dynamic> json, String name) =>
    (json['projects'] as List).cast<Map<String, dynamic>>().firstWhere((p) => p['name'] == name);

Map<String, dynamic> _services(Map<String, dynamic> json, String name) =>
    (json['services'] as Map<String, dynamic>)[name] as Map<String, dynamic>;

Map<String, dynamic> _server(Map<String, dynamic> json, String name) =>
    (_services(json, 'webserver')['servers'] as List)
        .cast<Map<String, dynamic>>()
        .firstWhere((s) => s['name'] == name);

class _FakeDaemon extends Daemon {
  _FakeDaemon({int extra = 0}) : super(root: '/tmp/wharf-test') {
    (_json['projects'] as List).addAll([
      for (var i = 0; i < extra; i++)
        {'name': 'site-$i', 'state': 'stopped', 'url': 'http://site-$i.localhost'},
    ]);
    state = WharfState.fromJson(_json);
    templates = [Template.fromJson({'id': 'kirby', 'name': 'Kirby', 'runtime': 'php'})];
  }

  final Map<String, dynamic> _json = jsonDecode(_snapshot) as Map<String, dynamic>;

  void change(void Function(Map<String, dynamic> json) edit) {
    edit(_json);
    state = WharfState.fromJson(jsonDecode(jsonEncode(_json)) as Map<String, dynamic>);
    notifyListeners();
  }

  @override
  Future<void> projectAction(ProjectAction action, String name) async => change(
    (json) => _project(json, name)['state'] = action == ProjectAction.stop ? 'stopping' : 'starting',
  );

  @override
  Future<void> updateSettings(
    String name, {
    String? webserverOverride,
    String? phpOverride,
    bool? ssl,
  }) async => change((json) {
    final project = _project(json, name);
    if (ssl != null) project['ssl'] = ssl;
    if (phpOverride != null && phpOverride.isNotEmpty) project['php_version'] = phpOverride;
  });

  @override
  Future<void> checkPhpReleases() async {}

  /// As the daemon answers: busy while everything stops, then idle.
  @override
  Future<void> stopAll() async {
    change((json) {
      json['busy'] = true;
      for (final p in (json['projects'] as List).cast<Map<String, dynamic>>()) {
        if (p['state'] == 'running') p['state'] = 'stopping';
      }
    });
    await Future<void>.delayed(const Duration(milliseconds: 150));
    change((json) {
      json['busy'] = false;
      for (final p in (json['projects'] as List).cast<Map<String, dynamic>>()) {
        p['state'] = 'stopped';
      }
    });
  }

  @override
  Future<void> removeProject(String name) async =>
      change((json) => (json['projects'] as List).removeWhere((p) => (p as Map)['name'] == name));
}

const _snapshot = '''
{
  "root": "/Users/x/Wharf",
  "version": "v0.1.0",
  "www": "/Users/x/Wharf/www",
  "config": "/Users/x/Wharf/config",
  "ssl": {"installed": true, "trusted": true, "ca_root": "/Users/x/ca"},
  "services": {
    "webserver": {"active": "nginx", "available": ["apache", "nginx"], "state": "running",
      "servers": [
        {"name": "apache", "installed": false, "binary": "/Users/x/Wharf/bin/apache/httpd", "projects": []},
        {"name": "nginx", "installed": true, "binary": "/Users/x/Wharf/bin/nginx/nginx",
         "projects": ["my-kirby-site"], "version": "1.27.3"}
      ]},
    "php": {
      "version": "8.4", "available": ["8.1", "8.4"], "recommended": "8.5", "status": "active",
      "dir": "/Users/x/Wharf/bin/php", "settings": "/Users/x/Wharf/config/php.ini",
      "downloadable": [{"version": "8.5", "status": "active", "full_version": "8.5.1"}],
      "installs": [
        {"version": "8.4", "full_version": "8.4.3", "dir": "/opt/php84", "source": "system", "status": "active"},
        {"version": "8.1", "full_version": "8.1.29", "dir": "/opt/php81", "source": "system", "status": "eol"}
      ]
    }
  },
  "projects": [
    {"name": "my-kirby-site", "state": "running", "url": "http://my-kirby-site.localhost",
     "webserver": "nginx", "php_version": "8.4", "webserver_version": "1.27.3", "php_full_version": "8.4.3",
     "dir": "/Users/x/Code/my-kirby-site", "linked": true, "log_dir": "/Users/x/Wharf/data/log/my-kirby-site",
     "custom_configs": [
       {"webserver": "nginx", "path": "/Users/x/Wharf/config/vhosts/my-kirby-site.nginx.conf",
        "exists": true, "active": true}
     ]},
    {"name": "legacy-app", "state": "stopped", "url": "http://legacy-app.localhost",
     "webserver": "nginx", "php_version": "8.1", "dir": "/Users/x/Wharf/www/legacy-app"}
  ],
  "unregistered": []
}
''';

// --------------------------------------------------------------- the engine

final _engine = _Engine();

class _AxBinding extends AutomatedTestWidgetsFlutterBinding {
  @override
  ui.SemanticsUpdateBuilder createSemanticsUpdateBuilder() => _Recorder();
}

/// Passes every update on to the real builder, and hands the engine model
/// what it would receive.
class _Recorder implements ui.SemanticsUpdateBuilder {
  final _real = ui.SemanticsUpdateBuilder();
  final _nodes = <int, _Node>{};

  @override
  void updateCustomAction({required int id, String? label, String? hint, int overrideId = -1}) =>
      _real.updateCustomAction(id: id, label: label, hint: hint, overrideId: overrideId);

  @override
  ui.SemanticsUpdate build() {
    _engine.commit(_nodes);
    return _real.build();
  }

  // updateNode has some forty named parameters; only two matter here.
  @override
  dynamic noSuchMethod(Invocation invocation) {
    if (invocation.memberName != #updateNode) return super.noSuchMethod(invocation);
    final args = invocation.namedArguments;
    _nodes[args[#id] as int] = _Node(
      (args[#childrenInTraversalOrder] as Int32List).toList(),
      [args[#label], args[#tooltip]].whereType<String>().where((s) => s.isNotEmpty).join(' / '),
    );
    Function.apply(_real.updateNode, const [], args);
    return null;
  }
}

class _Node {
  _Node(this.children, this.label);
  final List<int> children;
  final String label;
}

/// AccessibilityBridge::CommitUpdates and the structural checks of
/// AXTree::Unserialize, without the attributes neither of them looks at.
class _Engine {
  final _tree = <int, List<int>>{};
  final _labels = <int, String>{};
  final errors = <String>[];

  int get size => _tree.length;

  void reset() {
    _tree.clear();
    _labels.clear();
    errors.clear();
  }

  void commit(Map<int, _Node> pending) {
    pending.forEach((id, node) => _labels[id] = node.label);

    // Update 1: every node listed under a new parent leaves its old one.
    final parents = {
      for (final entry in _tree.entries)
        for (final child in entry.value) child: entry.key,
    };
    final removals = <int, List<int>>{};
    pending.forEach((id, node) {
      for (final child in node.children) {
        final parent = parents[child];
        if (parent == null || parent == id) continue;
        removals.putIfAbsent(parent, () => [..._tree[parent]!]).remove(child);
      }
    });
    if (removals.isNotEmpty &&
        !_unserialize([for (final e in removals.entries) (e.key, e.value)], null)) {
      return;
    }

    // Update 2: each pending subtree parent first, the subtrees in reverse
    // order of discovery.
    final remaining = Map.of(pending);
    final lists = <List<int>>[];
    void subtree(int id, List<int> out) {
      out.add(id);
      for (final child in pending[id]!.children) {
        if (remaining.remove(child) != null) subtree(child, out);
      }
    }

    while (remaining.isNotEmpty) {
      final id = remaining.keys.first;
      remaining.remove(id);
      lists.add([]);
      subtree(id, lists.last);
    }
    final root = _tree.isEmpty && lists.isNotEmpty ? lists.last.first : null;
    _unserialize([
      for (final list in lists.reversed)
        for (final id in list) (id, pending[id]!.children),
    ], root);
  }

  bool _unserialize(List<(int, List<int>)> nodes, int? newRoot) {
    final exists = <int, bool>{};
    final known = <int, List<int>>{};
    final needsData = <int>{};
    bool present(int id) => exists[id] ?? _tree.containsKey(id);
    void destroy(int id) {
      if (!present(id)) return;
      exists[id] = false;
      needsData.remove(id);
      (known[id] ?? _tree[id] ?? const <int>[]).forEach(destroy);
    }

    bool fail(String message, int id) {
      errors.add('$message — ${_describe(id)}');
      return false;
    }

    for (final (id, children) in nodes) {
      if (!present(id)) {
        if (id != newRoot) return fail('$id will not be in the tree and is not the new root', id);
        exists[id] = true;
        needsData.add(id);
      }
      if (needsData.remove(id)) {
        for (final child in children) {
          if (present(child)) return fail('Node $child is already pending for creation', child);
          exists[child] = true;
          needsData.add(child);
        }
      } else {
        final old = (known[id] ?? _tree[id] ?? const <int>[]).toSet();
        final now = children.toSet();
        for (final child in now.difference(old)) {
          if (present(child)) return fail('Node $child would be reparented to $id', child);
          exists[child] = true;
          needsData.add(child);
        }
        old.difference(now).forEach(destroy);
      }
      known[id] = children;
    }
    if (needsData.isNotEmpty) {
      return fail('Nodes left pending by the update: ${needsData.join(' ')}', needsData.first);
    }

    exists.forEach((id, alive) {
      if (!alive) _tree.remove(id);
    });
    known.forEach((id, children) {
      if (exists[id] ?? true) _tree[id] = children;
    });
    return true;
  }

  String _describe(int id) {
    final parent = _tree.entries.where((e) => e.value.contains(id)).map((e) => e.key).firstOrNull;
    return 'node $id "${_labels[id] ?? ''}", in the tree under '
        '${parent == null ? 'nothing' : '$parent "${_labels[parent] ?? ''}"'}';
  }
}
