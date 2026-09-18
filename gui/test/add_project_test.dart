import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:wharf_gui/daemon.dart';
import 'package:wharf_gui/folders.dart';
import 'package:wharf_gui/ipc/client.dart';
import 'package:wharf_gui/models/state.dart';
import 'package:wharf_gui/pages/projects_page.dart';
import 'package:wharf_gui/theme.dart';

/// One project, nginx installed and apache installable, Wharf's Kirby and
/// Laravel config templates.
const _snapshot = '''
{
  "root": "/Users/x/Wharf",
  "www": "/Users/x/Wharf/www",
  "services": {
    "webserver": {"active": "nginx", "available": ["apache", "nginx"], "state": "stopped",
      "servers": [
        {"name": "apache", "installed": false, "binary": "/Users/x/Wharf/bin/apache/httpd",
         "projects": [], "install": {"installable": true, "hint": "Installs Apache."}},
        {"name": "nginx", "installed": true, "binary": "/opt/nginx", "projects": [],
         "version": "1.27.3"}
      ]},
    "php": {"version": "8.4", "available": ["8.4"], "recommended": "8.4", "status": "active",
            "installs": [{"version": "8.4", "full_version": "8.4.3", "dir": "/opt/php84",
                          "fastcgi": "/opt/php84/sbin/php-fpm", "source": "system", "status": "active"}]}
  },
  "projects": [
    {"name": "my-kirby-site", "state": "stopped", "url": "http://my-kirby-site.localhost",
     "webserver": "nginx", "php_version": "8.4", "port": 8080, "dir": "/Users/x/Code/my-kirby-site"}
  ],
  "unregistered": ["dropped-in"],
  "config_templates": [
    {"id": "kirby", "name": "Kirby", "builtin": true, "changed": false, "projects": []},
    {"id": "laravel", "name": "Laravel", "builtin": true, "changed": false, "projects": []}
  ]
}
''';

/// No project yet, no PHP and no webserver installed: nginx can be
/// installed, Apache cannot.
const _bare = '''
{
  "root": "/Users/x/Wharf",
  "www": "/Users/x/Wharf/www",
  "services": {
    "webserver": {"active": "nginx", "available": ["apache", "nginx"], "state": "stopped",
      "servers": [
        {"name": "apache", "installed": false, "binary": "/Users/x/Wharf/bin/apache/httpd",
         "projects": [], "install": {"installable": false,
                                     "hint": "Install Apache with your package manager."}},
        {"name": "nginx", "installed": false, "binary": "/Users/x/Wharf/bin/nginx/nginx",
         "projects": [], "install": {"installable": true, "hint": "Downloads nginx."}}
      ]},
    "php": {"version": "8.5", "available": ["8.5"], "installs": [], "recommended": "8.5", "status": "active",
            "downloadable": [{"version": "8.5", "status": "active"}]}
  },
  "projects": [], "unregistered": []
}
''';

/// A daemon that proposes what the test sets and records what it is asked.
class _AddDaemon extends Daemon {
  _AddDaemon(String json, {this.proposal = const FolderProposal(path: '', name: '')})
    : super(root: '/tmp/wharf-test') {
    state = WharfState.fromJson(jsonDecode(json) as Map<String, dynamic>);
  }

  FolderProposal proposal;
  final inspected = <String>[];
  final added = <Map<String, String>>[];
  final calls = <String>[];

  /// What "Add" answers with instead of adding; null adds.
  DaemonError? refuse;

  @override
  Future<FolderProposal?> inspectFolder(String path) async {
    inspected.add(path);
    return FolderProposal(
      path: path,
      name: proposal.name,
      fixed: proposal.fixed,
      template: proposal.template,
      project: proposal.project,
    );
  }

  @override
  Future<Project> addFolderAs(
    String path, {
    required String name,
    required String template,
    String webserver = '',
    bool start = true,
  }) async {
    final refused = refuse;
    if (refused != null) throw refused;
    added.add({'path': path, 'name': name, 'template': template, 'webserver': webserver});
    return Project.fromJson({'name': name});
  }

  @override
  Future<void> installWebserver(String name) async => calls.add('install $name');

  @override
  Future<void> installPhp(String version) async => calls.add('download PHP $version');
}

Widget _wrap(Daemon daemon) => MaterialApp(
  theme: wharfTheme(Brightness.light),
  home: ProjectsPage(daemon: daemon),
);

/// Answers the folder picker with [folder], counting how often it is asked.
List<String> _picks(String? folder) {
  final asked = <String>[];
  final previous = pickFolder;
  pickFolder = (www) async {
    asked.add(www);
    return folder;
  };
  addTearDown(() => pickFolder = previous);
  return asked;
}

/// Taps "Add project…" and waits for the sheet.
Future<void> _openSheet(WidgetTester tester) async {
  await tester.tap(find.widgetWithText(FloatingActionButton, 'Add project…'));
  await tester.pumpAndSettle();
}

Finder get _nameField => find.widgetWithText(TextField, 'Name');

String _fieldText(WidgetTester tester) => tester.widget<TextField>(_nameField).controller!.text;

void main() {
  // features/project-folders.feature — "Adding a project with the folder picker"
  testWidgets('a chosen folder is proposed as a project and added once confirmed', (tester) async {
    final asked = _picks('/Users/x/Code/Client Site');
    final daemon = _AddDaemon(
      _snapshot,
      proposal: const FolderProposal(path: '', name: 'client-site', template: 'kirby'),
    );
    await tester.pumpWidget(_wrap(daemon));

    // INFO: The one way in: no folder icons in the title bar.
    expect(find.byTooltip('Open www folder'), findsNothing);
    expect(find.byTooltip('Add folder…'), findsNothing);

    await _openSheet(tester);
    expect(asked, ['/Users/x/Wharf/www']);
    expect(daemon.inspected, ['/Users/x/Code/Client Site']);

    // INFO: Then a sheet proposes the name "client-site" and shows its address
    expect(find.text('Add project'), findsOneWidget);
    expect(_fieldText(tester), 'client-site');
    expect(find.text('http://client-site.localhost'), findsOneWidget);
    // INFO: And it proposes the detected config template and the active webserver
    expect(find.text('Kirby'), findsOneWidget);
    expect(find.text('Detected in the folder'), findsOneWidget);
    expect(find.text('nginx'), findsOneWidget);

    await tester.tap(find.widgetWithText(FilledButton, 'Add'));
    await tester.pumpAndSettle();
    expect(daemon.added, [
      {
        'path': '/Users/x/Code/Client Site',
        'name': 'client-site',
        'template': 'kirby',
        'webserver': '',
      },
    ]);
    expect(find.text('Add project'), findsNothing);
  });

  testWidgets('a cancelled folder picker opens no sheet', (tester) async {
    _picks(null);
    final daemon = _AddDaemon(_snapshot);
    await tester.pumpWidget(_wrap(daemon));
    await _openSheet(tester);
    expect(daemon.inspected, isEmpty);
    expect(find.text('Add project'), findsNothing);
  });

  // features/project-folders.feature — "The proposed name comes from the folder
  // name"
  testWidgets('the name field holds the name alone and is rewritten once left', (tester) async {
    _picks('/Users/x/Code/Müller & Söhne');
    final daemon = _AddDaemon(
      _snapshot,
      proposal: const FolderProposal(path: '', name: 'mueller-soehne'),
    );
    await tester.pumpWidget(_wrap(daemon));
    await _openSheet(tester);

    // INFO: The name alone in the field, ".localhost" only in the address.
    expect(_fieldText(tester), 'mueller-soehne');
    expect(find.text('http://mueller-soehne.localhost'), findsOneWidget);

    await tester.enterText(_nameField, 'Café_Relaunch 2026');
    await tester.pump();
    // INFO: While typing, the field keeps what was typed; the address already
    // shows the rewritten name.
    expect(_fieldText(tester), 'Café_Relaunch 2026');
    expect(find.text('http://cafe-relaunch-2026.localhost'), findsOneWidget);
    await tester.sendKeyEvent(LogicalKeyboardKey.tab);
    await tester.pump();
    expect(_fieldText(tester), 'cafe-relaunch-2026');

    // INFO: And a name with no letter or digit in it is refused, asking for at least one
    await tester.enterText(_nameField, '!!!');
    await tester.pump();
    expect(find.text('Use at least one letter or digit'), findsOneWidget);
    expect(tester.widget<FilledButton>(find.widgetWithText(FilledButton, 'Add')).onPressed, isNull);
  });

  // features/project-folders.feature — "The config template is detected from the
  // folder"
  testWidgets('the detected config template can be changed, or none picked', (tester) async {
    _picks('/Users/x/Code/shop');
    final daemon = _AddDaemon(
      _snapshot,
      proposal: const FolderProposal(path: '', name: 'shop', template: 'laravel'),
    );
    await tester.pumpWidget(_wrap(daemon));
    await _openSheet(tester);
    expect(find.text('Laravel'), findsOneWidget);
    expect(find.text('Detected in the folder'), findsOneWidget);

    await tester.tap(find.text('Laravel'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('None').last);
    await tester.pumpAndSettle();
    expect(find.text('Detected in the folder'), findsNothing);

    await tester.tap(find.widgetWithText(FilledButton, 'Add'));
    await tester.pumpAndSettle();
    expect(daemon.added.single['template'], '');
  });

  // features/project-folders.feature — "Choosing the webserver while adding a
  // project"
  // features/project-folders.feature — "Adding a project for a webserver that is
  // not installed"
  testWidgets('the other webserver pins the project, and offers to install itself', (tester) async {
    _picks('/Users/x/Code/legacy-app');
    final daemon = _AddDaemon(
      _snapshot,
      proposal: const FolderProposal(path: '', name: 'legacy-app'),
    );
    await tester.pumpWidget(_wrap(daemon));
    await _openSheet(tester);
    expect(find.textContaining('is not installed'), findsNothing);

    await tester.tap(find.text('nginx'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('apache').last);
    await tester.pumpAndSettle();

    // INFO: Then the sheet says apache is not installed and offers to install it
    expect(find.text('apache is not installed.'), findsOneWidget);
    await tester.tap(find.widgetWithText(FilledButton, 'Install apache'));
    await tester.pump();
    expect(daemon.calls, ['install apache']);

    await tester.tap(find.widgetWithText(FilledButton, 'Add'));
    await tester.pumpAndSettle();
    expect(daemon.added.single['webserver'], 'apache');
  });

  // features/project-folders.feature — "Choosing a folder inside www/ registers
  // it by name"
  testWidgets('a folder in www/ keeps its own name', (tester) async {
    _picks('/Users/x/Wharf/www/my-blog');
    final daemon = _AddDaemon(
      _snapshot,
      proposal: const FolderProposal(path: '', name: 'my-blog', fixed: true),
    );
    await tester.pumpWidget(_wrap(daemon));
    await _openSheet(tester);

    expect(tester.widget<TextField>(_nameField).readOnly, isTrue);
    expect(find.text('The name of its folder in www/'), findsOneWidget);
  });

  // features/project-folders.feature — "Adding a folder found in www/"
  testWidgets('adding a folder listed from www/ opens the same sheet', (tester) async {
    final asked = _picks('/somewhere/else');
    final daemon = _AddDaemon(
      _snapshot,
      proposal: const FolderProposal(path: '', name: 'dropped-in', fixed: true),
    );
    await tester.pumpWidget(_wrap(daemon));

    await tester.tap(find.widgetWithText(TextButton, 'Add'));
    await tester.pumpAndSettle();

    expect(asked, isEmpty, reason: 'the folder is known; no picker');
    expect(daemon.inspected, ['/Users/x/Wharf/www${Platform.pathSeparator}dropped-in']);
    expect(find.text('Add project'), findsOneWidget);
    expect(_fieldText(tester), 'dropped-in');
  });

  // features/project-folders.feature — "A folder whose name is already taken"
  testWidgets('a taken name is said in the sheet, which stays open', (tester) async {
    _picks('/Users/x/Code/two/client-site');
    final daemon = _AddDaemon(
      _snapshot,
      proposal: const FolderProposal(path: '', name: 'client-site'),
    )..refuse = DaemonError('conflict', 'the name "client-site" is taken — pick another name');
    await tester.pumpWidget(_wrap(daemon));
    await _openSheet(tester);

    await tester.tap(find.widgetWithText(FilledButton, 'Add'));
    await tester.pumpAndSettle();
    expect(find.text('Add project'), findsOneWidget);
    expect(find.text('the name "client-site" is taken — pick another name'), findsOneWidget);
    expect(daemon.added, isEmpty);

    // INFO: Another name clears the complaint, and is added.
    daemon.refuse = null;
    await tester.enterText(_nameField, 'client-site-2');
    await tester.pumpAndSettle();
    expect(find.textContaining('is taken'), findsNothing);
    await tester.tap(find.widgetWithText(FilledButton, 'Add'));
    await tester.pumpAndSettle();
    expect(daemon.added.single['name'], 'client-site-2');
  });

  // features/project-folders.feature — "Choosing a folder that is already a
  // project"
  testWidgets('a folder that is a project already opens its settings', (tester) async {
    _picks('/Users/x/Code/my-kirby-site');
    final daemon = _AddDaemon(
      _snapshot,
      proposal: const FolderProposal(path: '', name: 'my-kirby-site', project: 'my-kirby-site'),
    );
    await tester.pumpWidget(_wrap(daemon));
    await _openSheet(tester);

    expect(find.text('Add project'), findsNothing);
    expect(find.text('Remove project'), findsOneWidget);
    expect(daemon.added, isEmpty);
  });

  // features/project-folders.feature — "Before the first project, Wharf says what
  // is missing"
  testWidgets('before the first project, the window says what is missing', (tester) async {
    final daemon = _AddDaemon(_bare);
    await tester.pumpWidget(_wrap(daemon));

    expect(find.text('No projects yet'), findsOneWidget);
    // INFO: Then the window says PHP is missing and offers the recommended version
    expect(find.text('PHP — missing'), findsOneWidget);
    await tester.tap(find.widgetWithText(FilledButton, 'Download PHP 8.5'));
    await tester.pump();
    // INFO: And it offers to install each webserver Wharf can install
    expect(find.text('Webserver — missing'), findsOneWidget);
    await tester.tap(find.widgetWithText(FilledButton, 'Install nginx'));
    await tester.pump();
    expect(daemon.calls, ['download PHP 8.5', 'install nginx']);
    // INFO: And a webserver Wharf cannot install says how to get it instead
    expect(find.widgetWithText(FilledButton, 'Install apache'), findsNothing);
    expect(find.textContaining('Install Apache with your package manager.'), findsOneWidget);
    // INFO: And "Add project…" is offered once, by the window's own button
    expect(find.text('Add project…'), findsOneWidget);
    expect(find.widgetWithText(FloatingActionButton, 'Add project…'), findsOneWidget);

    // INFO: A download under way shows as one, named for a screen reader — as
    // part of its row, which reads as one item.
    final semantics = tester.ensureSemantics();
    final downloading = _AddDaemon(
      _bare.replaceFirst('"status": "active",\n', '"status": "active", "downloading": ["8.5"],\n'),
    );
    await tester.pumpWidget(_wrap(downloading));
    expect(find.bySemanticsLabel(RegExp('Downloading PHP 8.5')), findsOneWidget);
    expect(find.widgetWithText(FilledButton, 'Download PHP 8.5'), findsNothing);
    semantics.dispose();
  });

  // features/project-folders.feature — "Once PHP and a webserver are there, the
  // window says so"
  testWidgets('with PHP and a webserver there, adding a project is what is left', (tester) async {
    final daemon = _AddDaemon(
      _snapshot
          .replaceFirst(RegExp(r'"projects": \[\n.*?\n.*?\n  \],', dotAll: true), '"projects": [],')
          .replaceFirst('"unregistered": ["dropped-in"]', '"unregistered": []'),
    );
    await tester.pumpWidget(_wrap(daemon));

    expect(find.text('No projects yet'), findsOneWidget);
    expect(find.text('PHP 8.4 — ready'), findsOneWidget);
    expect(find.text('Webserver: nginx 1.27.3 — ready'), findsOneWidget);
    // INFO: And "Add project…" is the one thing left to do
    expect(find.byType(FilledButton), findsNothing);
    expect(find.text('Add project…'), findsOneWidget);
  });

  // features/project-folders.feature — "Before the first project, Wharf says what
  // is missing"
  testWidgets('ready and missing are told by shape and words, not colour', (tester) async {
    final daemon = _AddDaemon(_bare);
    await tester.pumpWidget(_wrap(daemon));
    expect(find.byIcon(Icons.radio_button_unchecked), findsNWidgets(2));
    expect(find.byIcon(Icons.check_circle), findsNothing);
  });
}
