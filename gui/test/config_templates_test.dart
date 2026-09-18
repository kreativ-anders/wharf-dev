import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:wharf_gui/daemon.dart';
import 'package:wharf_gui/ipc/client.dart';
import 'package:wharf_gui/models/state.dart';
import 'package:wharf_gui/pages/config_editor.dart';
import 'package:wharf_gui/pages/projects_page.dart';
import 'package:wharf_gui/pages/settings_page.dart';
import 'package:wharf_gui/theme.dart';

/// One project, Wharf's Kirby and Laravel templates — Laravel changed — and
/// one of the user's own.
const _snapshot = '''
{
  "root": "/Users/x/Wharf",
  "services": {
    "webserver": {"active": "nginx", "available": ["apache", "nginx"], "state": "stopped"},
    "php": {"version": "8.4", "available": ["8.4"], "status": "active"}
  },
  "projects": [
    {"name": "my-app", "state": "stopped", "url": "http://my-app.localhost",
     "webserver": "nginx", "php_version": "8.4", "port": 8080, "template": "laravel"}
  ],
  "unregistered": [],
  "config_templates": [
    {"id": "kirby", "name": "Kirby", "builtin": true, "changed": false, "projects": []},
    {"id": "laravel", "name": "Laravel", "builtin": true, "changed": true, "projects": ["my-app"]},
    {"id": "my-api", "name": "my-api", "builtin": false, "changed": false, "projects": []}
  ]
}
''';

/// A fixture daemon that serves template rules and records what it is asked.
class _TemplateDaemon extends Daemon {
  _TemplateDaemon() : super(root: '/tmp/wharf-test') {
    state = WharfState.fromJson(jsonDecode(_snapshot) as Map<String, dynamic>);
  }

  final calls = <String>[];
  final rules = {
    'nginx': 'root "{{root}}/public";\nlocation / {\n  try_files \$uri /index.php;\n}',
    'apache': 'DocumentRoot "{{root}}/public"',
  };
  DaemonError? refuseCreate;

  @override
  Future<String> readConfigTemplate(String id, String webserver) async => rules[webserver]!;

  @override
  Future<void> saveConfigTemplate(String id, String webserver, String content) async =>
      calls.add('save $id $webserver: $content');

  @override
  Future<String> createConfigTemplate(String name) async {
    if (refuseCreate != null) throw refuseCreate!;
    calls.add('create $name');
    return 'my-new';
  }

  @override
  Future<void> deleteConfigTemplate(String id) async => calls.add('delete $id');

  /// my-app's custom config: none yet, so the Laravel rules to start from.
  var custom = const CustomConfigRules(
    webserver: 'nginx',
    content: '# my-app\'s own nginx config\nserver {\n  {{listen}}\n}',
    exists: false,
  );
  DaemonError? refuseCustom;

  @override
  Future<CustomConfigRules> readCustomConfig(String project) async => custom;

  @override
  Future<void> saveCustomConfig(String project, String webserver, String content) async {
    if (refuseCustom != null) throw refuseCustom!;
    calls.add('save custom $project $webserver: $content');
  }

  @override
  Future<void> deleteCustomConfig(String project, String webserver) async =>
      calls.add('delete custom $project $webserver');

  @override
  Future<void> updateSettings(
    String name, {
    String? webserverOverride,
    String? phpOverride,
    bool? ssl,
    String? template,
  }) async => calls.add('settings $name template=$template');
}

Widget _wrap(Widget child) => MaterialApp(theme: wharfTheme(Brightness.light), home: child);

Future<void> _webserverPage(WidgetTester tester, Daemon daemon) async {
  tester.view.physicalSize = const Size(1000, 2400);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  await tester.pumpWidget(_wrap(SettingsPage(daemon: daemon, initial: SettingsSection.webserver)));
  await tester.pump();
}

Future<void> _confirm(WidgetTester tester, String action) async {
  await tester.pumpAndSettle();
  await tester.tap(find.widgetWithText(FilledButton, action));
  await tester.pumpAndSettle();
}

Future<void> _projectSettings(WidgetTester tester, Daemon daemon) async {
  tester.view.physicalSize = const Size(1000, 1400);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  await tester.pumpWidget(_wrap(ProjectsPage(daemon: daemon)));
  await tester.tap(find.byTooltip('Settings for my-app'));
  await tester.pumpAndSettle();
}

void main() {
  // features/app-configuration.feature — "A project's custom config starts from
  // its config template"
  testWidgets('"Customize…" opens the template\'s rules with a warning about the placeholders', (
    tester,
  ) async {
    final daemon = _TemplateDaemon();
    await _projectSettings(tester, daemon);

    await tester.tap(find.widgetWithText(TextButton, 'Customize…'));
    await tester.pumpAndSettle();

    expect(find.text('Custom nginx config for my-app'), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp('nginx rules of my-app')), findsOneWidget);
    expect(find.textContaining('Keep every {{…}}'), findsOneWidget);
    expect(find.byIcon(Icons.warning_amber_rounded), findsOneWidget);
    // INFO: Nothing to go back to yet: the template still applies.
    expect(find.text('Use config template'), findsNothing);

    await tester.enterText(find.byType(TextField), 'server {\n  {{listen}}\n  gzip on;\n}');
    await tester.tap(find.widgetWithText(FilledButton, 'Save'));
    await tester.pumpAndSettle();
    expect(daemon.calls, ['save custom my-app nginx: server {\n  {{listen}}\n  gzip on;\n}']);
    expect(find.text('Custom nginx config for my-app'), findsNothing);
  });

  // features/app-configuration.feature — "A custom config belongs to the
  // webserver serving the project"
  testWidgets(
    'project settings offer the custom config of the webserver serving it, and no other',
    (tester) async {
      final apache = _TemplateDaemon();
      apache.state = WharfState.fromJson(
        jsonDecode(
          _snapshot
              .replaceFirst(
                '"webserver": "nginx", "php_version"',
                '"webserver": "apache", "php_version"',
              )
              .replaceFirst(
                '"template": "laravel"}',
                '"template": "laravel", "custom_config": {"webserver": "apache", "path": "/x", "exists": true}}',
              ),
        ) as Map<String, dynamic>,
      );
      await _projectSettings(tester, apache);

      expect(find.text('Custom Apache config'), findsOneWidget);
      expect(find.text('Custom nginx config'), findsNothing);
      expect(find.widgetWithText(TextButton, 'Edit…'), findsOneWidget);
      expect(
        find.text('Used instead of the config template while Apache serves it.'),
        findsOneWidget,
      );
    },
  );

  // features/app-configuration.feature — "A custom config keeps Wharf's
  // placeholders"
  testWidgets('a custom config the daemon refuses keeps the editor open and says why', (
    tester,
  ) async {
    final daemon = _TemplateDaemon()
      ..refuseCustom = DaemonError(
        'bad_request',
        'the nginx rules leave out {{listen}} — put it back; Wharf fills in where the project is reachable',
      );
    await _projectSettings(tester, daemon);
    await tester.tap(find.widgetWithText(TextButton, 'Customize…'));
    await tester.pumpAndSettle();

    await tester.enterText(find.byType(TextField), 'server {\n  listen 8080;\n}');
    await tester.tap(find.widgetWithText(FilledButton, 'Save'));
    await tester.pumpAndSettle();

    expect(find.text('Custom nginx config for my-app'), findsOneWidget);
    expect(find.textContaining('leave out {{listen}}'), findsOneWidget);
  });

  // features/app-configuration.feature — "Removing a custom config returns to
  // the config template"
  testWidgets('"Use config template" deletes the custom config once the user confirms', (
    tester,
  ) async {
    final daemon = _TemplateDaemon()
      ..custom = const CustomConfigRules(webserver: 'nginx', content: 'server {}', exists: true);
    await _projectSettings(tester, daemon);
    await tester.tap(find.widgetWithText(TextButton, 'Customize…'));
    await tester.pumpAndSettle();

    await tester.tap(find.text('Use config template'));
    await tester.pumpAndSettle();
    expect(find.textContaining('served with the Laravel config template again'), findsOneWidget);
    await _confirm(tester, 'Delete');

    expect(daemon.calls, ['delete custom my-app nginx']);
  });

  // features/config-templates.feature — "Built-in config templates for common
  // CMS and frameworks"
  testWidgets('the Webserver page lists the config templates, Wharf\'s first', (tester) async {
    await _webserverPage(tester, _TemplateDaemon());

    expect(find.text('Config templates'), findsOneWidget);
    expect(find.text('Kirby'), findsOneWidget);
    expect(find.text('Built in'), findsOneWidget);
    // INFO: "Changed" and who uses it are said in words, not by colour.
    expect(find.text('Built in · changed · used by my-app'), findsOneWidget);
    expect(find.text('Yours'), findsOneWidget);
    expect(find.byTooltip('Edit Kirby'), findsOneWidget);
    expect(find.byTooltip('Restore Wharf\'s rules for Kirby'), findsNothing);
    expect(find.byTooltip('Delete Kirby'), findsNothing);
  });

  // features/config-templates.feature — "Editing a config template in Wharf"
  testWidgets('a config template opens in an editor with line numbers, one webserver at a time', (
    tester,
  ) async {
    final daemon = _TemplateDaemon();
    await _webserverPage(tester, daemon);

    await tester.tap(find.byTooltip('Edit Laravel'));
    await tester.pumpAndSettle();

    expect(find.text('Edit Laravel'), findsOneWidget);
    expect(find.text('1\n2\n3\n4'), findsOneWidget, reason: 'nginx rules have four numbered lines');
    expect(find.bySemanticsLabel(RegExp('nginx rules of Laravel')), findsOneWidget);

    await tester.tap(find.text('Apache'));
    await tester.pumpAndSettle();
    expect(find.text('1'), findsOneWidget, reason: 'Apache rules have one numbered line');
    await tester.enterText(find.byType(TextField), 'DocumentRoot "{{root}}/web"\n# mine');
    await tester.pump();
    expect(find.text('1\n2'), findsOneWidget, reason: 'the numbers follow the typing');

    await tester.tap(find.widgetWithText(FilledButton, 'Save'));
    await tester.pumpAndSettle();

    // INFO: Only the webserver whose rules changed is saved, and the window closes.
    expect(daemon.calls, ['save laravel apache: DocumentRoot "{{root}}/web"\n# mine']);
    expect(find.text('Edit Laravel'), findsNothing);
  });

  // features/config-templates.feature — "Changing a built-in config template, and
  // restoring it"
  testWidgets('a changed built-in config template is restored once the user confirms', (
    tester,
  ) async {
    final daemon = _TemplateDaemon();
    await _webserverPage(tester, daemon);

    await tester.tap(find.byTooltip('Restore Wharf\'s rules for Laravel'));
    await _confirm(tester, 'Restore');

    expect(daemon.calls, ['delete laravel']);
  });

  // features/config-templates.feature — "Deleting a config template"
  testWidgets('the user\'s own config template is deleted once the user confirms', (tester) async {
    final daemon = _TemplateDaemon();
    await _webserverPage(tester, daemon);

    await tester.tap(find.byTooltip('Delete my-api'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(TextButton, 'Cancel'));
    await tester.pumpAndSettle();
    expect(daemon.calls, isEmpty);

    await tester.tap(find.byTooltip('Delete my-api'));
    await _confirm(tester, 'Delete');
    expect(daemon.calls, ['delete my-api']);
  });

  // features/config-templates.feature — "Creating a config template"
  testWidgets('a new config template asks for a name, then opens the editor', (tester) async {
    final daemon = _TemplateDaemon();
    await _webserverPage(tester, daemon);

    // INFO: And a name another config template already has is refused
    daemon.refuseCreate = DaemonError('conflict', 'a config template named "kirby" already exists');
    await tester.tap(find.text('New template…'));
    await tester.pumpAndSettle();
    await tester.enterText(find.widgetWithText(TextField, 'Name'), 'Kirby');
    await tester.tap(find.widgetWithText(FilledButton, 'Create'));
    await tester.pumpAndSettle();
    expect(find.text('a config template named "kirby" already exists'), findsOneWidget);

    daemon.refuseCreate = null;
    await tester.enterText(find.widgetWithText(TextField, 'Name'), 'My New');
    await tester.tap(find.widgetWithText(FilledButton, 'Create'));
    await tester.pumpAndSettle();

    expect(daemon.calls, ['create My New']);
    expect(find.text('Edit my-new'), findsOneWidget);
  });

  // features/config-templates.feature — "Picking a config template for a project"
  testWidgets('a project picks its config template in its settings, or none', (tester) async {
    final daemon = _TemplateDaemon();
    await tester.pumpWidget(_wrap(ProjectsPage(daemon: daemon)));
    await tester.tap(find.byTooltip('Settings for my-app'));
    await tester.pumpAndSettle();

    expect(find.text('Config template'), findsOneWidget);
    await tester.tap(find.text('Laravel'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Kirby').last);
    await tester.pumpAndSettle();
    expect(daemon.calls, ['settings my-app template=kirby']);

    await tester.tap(find.text('Laravel'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('None').last);
    await tester.pumpAndSettle();
    expect(daemon.calls.last, 'settings my-app template=');
  });

  testWidgets('the editor keeps unsaved changes until the user discards them', (tester) async {
    final daemon = _TemplateDaemon();
    tester.view.physicalSize = const Size(1000, 1400);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    await tester.pumpWidget(
      _wrap(
        Builder(
          builder: (context) => TextButton(
            onPressed: () => showConfigTemplateEditor(
              context,
              daemon,
              const ConfigTemplate(id: 'kirby', name: 'Kirby', builtin: true),
            ),
            child: const Text('open'),
          ),
        ),
      ),
    );
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();

    await tester.enterText(find.byType(TextField), 'root "{{root}}";');
    await tester.tap(find.widgetWithText(TextButton, 'Cancel'));
    await tester.pumpAndSettle();
    expect(find.text('Discard changes?'), findsOneWidget);
    await tester.tap(find.text('Keep editing'));
    await tester.pumpAndSettle();
    expect(find.text('Edit Kirby'), findsOneWidget);

    await tester.tap(find.widgetWithText(TextButton, 'Cancel'));
    await _confirm(tester, 'Discard');
    expect(find.text('Edit Kirby'), findsNothing);
    expect(daemon.calls, isEmpty);
  });
}
