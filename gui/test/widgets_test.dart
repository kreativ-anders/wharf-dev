import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:wharf_gui/daemon.dart';
import 'package:wharf_gui/folders.dart';
import 'package:wharf_gui/models/state.dart';
import 'package:wharf_gui/pages/projects_page.dart';
import 'package:wharf_gui/pages/settings_page.dart';
import 'package:wharf_gui/theme.dart';
import 'package:wharf_gui/tray.dart';

/// A daemon that never connects, holding a snapshot the test supplies. The
/// GUI renders whatever the daemon publishes and owns no state of its own, so
/// this is enough to drive every view.
Daemon fixture(String json) {
  final daemon = Daemon(root: '/tmp/wharf-test');
  daemon.state = WharfState.fromJson(jsonDecode(json) as Map<String, dynamic>);
  return daemon;
}

Widget wrap(Widget child) => MaterialApp(theme: wharfTheme(Brightness.light), home: child);

/// Settings is a lazy list; a tall surface builds all of it.
void tall(WidgetTester tester) {
  tester.view.physicalSize = const Size(800, 2400);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
}

/// Records what would have been opened in the file manager.
List<String> captureOpened() {
  final opened = <String>[];
  final previous = openFolder;
  openFolder = (path) async => opened.add(path);
  addTearDown(() => openFolder = previous);
  return opened;
}

void main() {
  testWidgets('the project list shows name, status and URL, and nothing else', (tester) async {
    final daemon = fixture(_twoProjects);
    await tester.pumpWidget(wrap(ProjectsPage(daemon: daemon)));

    expect(find.text('my-kirby-site'), findsOneWidget);
    expect(find.text('http://my-kirby-site.wharf'), findsOneWidget);
    expect(find.byType(StatusDot), findsNWidgets(2));

    // A running project offers Stop, Restart and an Open action; a stopped
    // one offers Start. Each offers its settings. Nothing else is on the row.
    expect(find.byIcon(Icons.stop), findsOneWidget);
    expect(find.byIcon(Icons.restart_alt), findsOneWidget);
    expect(find.byIcon(Icons.play_arrow), findsOneWidget);
    expect(find.byIcon(Icons.open_in_new), findsOneWidget);
    expect(find.byTooltip('Settings for my-kirby-site'), findsOneWidget);
    expect(find.byTooltip('Settings for legacy-app'), findsOneWidget);
  });

  // features/tray-actions.feature — "Every project offers the actions that fit
  // its state"
  testWidgets('each project offers the actions that fit its state, in the row and the tray', (
    tester,
  ) async {
    Project withState(String state) => Project.fromJson({'name': 'site', 'state': state});
    final expected = {
      'running': [ProjectAction.stop, ProjectAction.restart],
      'stopped': [ProjectAction.start],
      'failed': [ProjectAction.start, ProjectAction.restart],
      'starting': <ProjectAction>[],
    };

    for (final MapEntry(key: state, value: actions) in expected.entries) {
      final project = withState(state);
      expect(project.actions, actions, reason: state);

      // The tray lists every action, enabling exactly the ones the row shows.
      final enabled = projectMenuItems(project)
          .where((i) => i.key != null && !i.key!.startsWith('open:') && !i.disabled)
          .map((i) => i.label)
          .toList();
      expect(enabled, actions.map((a) => a.label).toList(), reason: 'tray, $state');
    }

    // The row shows them as labelled buttons: a failed project can be
    // started again or restarted, and says why it failed.
    final daemon = fixture(_twoProjects.replaceFirst('"state": "stopped"', '"state": "failed"'));
    await tester.pumpWidget(wrap(ProjectsPage(daemon: daemon)));
    expect(find.byTooltip('Stop my-kirby-site'), findsOneWidget);
    expect(find.byTooltip('Restart my-kirby-site'), findsOneWidget);
    expect(find.byTooltip('Start my-kirby-site'), findsNothing);
    expect(find.byTooltip('Start legacy-app'), findsOneWidget);
    expect(find.byTooltip('Restart legacy-app'), findsOneWidget);
    expect(find.byTooltip('Stop legacy-app'), findsNothing);
  });

  // features/app-configuration.feature — "Every project row leads to its settings"
  testWidgets('every project row has a visible way into its settings', (tester) async {
    final daemon = fixture(_twoProjects);
    await tester.pumpWidget(wrap(ProjectsPage(daemon: daemon)));

    await tester.tap(find.byTooltip('Settings for my-kirby-site'));
    await tester.pumpAndSettle();

    for (final label in [
      'PHP version',
      'Webserver',
      'SSL',
      'Custom webserver config',
      'Open folder',
    ]) {
      expect(find.text(label), findsOneWidget, reason: label);
    }
    // One custom config per webserver, the one in use marked as such.
    expect(find.text('nginx · in use'), findsOneWidget);
    expect(find.text('apache'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Edit'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Create'), findsOneWidget);
  });

  // features/project-folders.feature — "Opening a project's folder"
  testWidgets('a project\'s folder opens from its settings', (tester) async {
    final opened = captureOpened();
    final daemon = fixture(_twoProjects);
    await tester.pumpWidget(wrap(ProjectsPage(daemon: daemon)));

    await tester.tap(find.byTooltip('Settings for my-kirby-site'));
    await tester.pumpAndSettle();
    expect(find.text('/Users/x/Code/my-kirby-site'), findsOneWidget);
    await tester.tap(find.text('Open folder'));

    expect(opened, ['/Users/x/Code/my-kirby-site']);
  });

  // features/project-folders.feature — "Opening the www folder"
  testWidgets('the www folder opens from the project list', (tester) async {
    final opened = captureOpened();
    final daemon = fixture(_twoProjects);
    await tester.pumpWidget(wrap(ProjectsPage(daemon: daemon)));

    await tester.tap(find.byTooltip('Open www folder'));

    expect(opened, ['/Users/x/Wharf/www']);
  });

  // features/local-ssl.feature — "Trust is declined"
  testWidgets('an SSL project whose certificates are not trusted says browsers will warn', (
    tester,
  ) async {
    final daemon = fixture(_twoProjects.replaceFirst('"trusted": true', '"trusted": false'));
    await tester.pumpWidget(wrap(ProjectsPage(daemon: daemon)));

    await tester.tap(find.byTooltip('Settings for legacy-app'));
    await tester.pumpAndSettle();
    expect(find.textContaining('Browsers will warn'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Trust…'), findsOneWidget);

    tall(tester);
    await tester.pumpWidget(wrap(SettingsPage(daemon: daemon)));
    await tester.pump();
    expect(find.widgetWithText(FilledButton, 'Trust certificates'), findsOneWidget);
  });

  // features/pretty-urls.feature — "Elevation is declined"
  testWidgets('a project with no hosts entry shows its raw-port URL', (tester) async {
    final daemon = fixture(_twoProjects);
    await tester.pumpWidget(wrap(ProjectsPage(daemon: daemon)));

    // The declined-elevation project shows the fallback, not a pretty URL.
    expect(find.text('http://127.0.0.1:8081'), findsOneWidget);
    expect(find.byIcon(Icons.lock_open), findsOneWidget);
  });

  // features/tray-actions.feature — "Adding a project via the tray"
  testWidgets('folders in www/ that are not projects yet can be added', (tester) async {
    final daemon = fixture(_twoProjects);
    await tester.pumpWidget(wrap(ProjectsPage(daemon: daemon)));

    expect(find.text('dropped-in'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Add'), findsOneWidget);
  });

  testWidgets('an empty state explains that a folder is a project', (tester) async {
    final daemon = fixture(
      '{"root":"/Users/x/Wharf","www":"/Users/x/Wharf/www","projects":[],"unregistered":[]}',
    );
    await tester.pumpWidget(wrap(ProjectsPage(daemon: daemon)));

    expect(find.text('No projects yet'), findsOneWidget);
    expect(find.textContaining('/Users/x/Wharf/www/'), findsOneWidget);
    // No drag-and-drop wording: it is a folder picker, and the folder can be
    // anywhere.
    expect(find.textContaining('Drop'), findsNothing);
    expect(find.widgetWithText(OutlinedButton, 'Add folder…'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Open www folder'), findsOneWidget);
  });

  // features/service-management.feature — "Port conflict on switch"
  testWidgets('a webserver switch is shown as switching, not as stopped', (tester) async {
    final daemon = fixture(_switching);
    tall(tester);
    await tester.pumpWidget(wrap(SettingsPage(daemon: daemon)));
    await tester.pump();

    expect(find.text('switching webserver…'), findsOneWidget);
  });

  // features/settings.feature — "Settings screen hides roadmap services in v1"
  testWidgets('settings show only Webserver, PHP runtime and SSL', (tester) async {
    final daemon = fixture(_twoProjects);
    tall(tester);
    await tester.pumpWidget(wrap(SettingsPage(daemon: daemon)));
    await tester.pump();

    expect(find.text('Webserver'), findsOneWidget);
    expect(find.text('PHP runtime'), findsOneWidget);
    expect(find.text('SSL'), findsOneWidget);
    for (final roadmap in ['MySQL', 'PostgreSQL', 'Mailpit', 'Database', 'Mail']) {
      expect(find.text(roadmap), findsNothing, reason: '$roadmap is a roadmap capability');
    }
  });

  // features/php-runtime.feature — "First start prefers a supported version
  // over a newer unsupported one"
  testWidgets('the PHP picker shows each version with its support status', (tester) async {
    final daemon = fixture(_twoProjects);
    tall(tester);
    await tester.pumpWidget(wrap(SettingsPage(daemon: daemon)));
    await tester.pump();

    expect(find.text('PHP 8.4.3'), findsOneWidget);
    expect(
      find.descendant(
        of: find.widgetWithText(RadioListTile<String>, 'PHP 8.4.3'),
        matching: find.text('active support'),
      ),
      findsOneWidget,
    );
    expect(find.text('PHP 8.1.29'), findsOneWidget);
    expect(find.text('end of life'), findsOneWidget);
  });

  // features/php-runtime.feature — "Opening where a PHP version lives"
  testWidgets('each PHP version\'s folder opens from the picker', (tester) async {
    final opened = captureOpened();
    final daemon = fixture(_twoProjects);
    tall(tester);
    await tester.pumpWidget(wrap(SettingsPage(daemon: daemon)));
    await tester.pump();

    await tester.tap(find.byTooltip('Open folder').first);

    expect(opened, ['/opt/php84']);
  });

  // features/php-runtime.feature — "Only supported versions are offered for
  // download"
  // features/php-runtime.feature — "Downloading a PHP version that is not
  // installed"
  testWidgets('supported versions that are not installed can be downloaded', (tester) async {
    final daemon = fixture(_twoProjects);
    tall(tester);
    await tester.pumpWidget(wrap(SettingsPage(daemon: daemon)));
    await tester.pump();

    expect(find.text('PHP 8.5'), findsOneWidget);
    expect(find.text('PHP 8.3'), findsOneWidget);
    expect(find.text('Downloading…'), findsOneWidget, reason: '8.3 is downloading');
    expect(
      find.widgetWithText(TextButton, 'Download'),
      findsOneWidget,
      reason: 'only 8.5 offers it',
    );
  });

  // features/settings.feature — "Webserver status while nothing is running"
  testWidgets('an idle webserver says when it starts, not that it is stopped', (tester) async {
    final daemon = fixture(_twoProjects.replaceFirst('"state": "running"', '"state": "stopped"'));
    tall(tester);
    await tester.pumpWidget(wrap(SettingsPage(daemon: daemon)));
    await tester.pump();

    expect(find.text('Starts with the first project — serves my-kirby-site'), findsOneWidget);
    expect(find.text('stopped'), findsNothing);
  });

  // features/settings.feature — "A webserver that is not installed says where
  // it belongs"
  testWidgets('a missing webserver says where its binary goes', (tester) async {
    final opened = captureOpened();
    final daemon = fixture(_twoProjects);
    tall(tester);
    await tester.pumpWidget(wrap(SettingsPage(daemon: daemon)));
    await tester.pump();

    expect(
      find.text('Not installed — expected at /Users/x/Wharf/bin/apache/httpd'),
      findsOneWidget,
    );
    await tester.tap(find.byTooltip('Open the folder it belongs in'));
    expect(opened, ['/Users/x/Wharf/bin/apache']);
  });

  testWidgets('a selected version that is not installed is called out', (tester) async {
    final daemon = fixture(_selectedNotInstalled);
    tall(tester);
    await tester.pumpWidget(wrap(SettingsPage(daemon: daemon)));
    await tester.pump();

    // The list renders, but nothing in it is selected — so say why.
    expect(find.textContaining('PHP 8.5 is selected but is not installed'), findsOneWidget);
    expect(find.text('PHP 8.4.3'), findsOneWidget);
  });

  // features/php-runtime.feature — "First start with no PHP installed"
  testWidgets('with no PHP installed the picker says where to put one', (tester) async {
    final daemon = fixture(_noPhp);
    tall(tester);
    await tester.pumpWidget(wrap(SettingsPage(daemon: daemon)));
    await tester.pump();

    expect(find.text('No PHP installation found on this machine.'), findsOneWidget);
    expect(find.textContaining('/Users/x/Wharf/bin/php'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Download'), findsOneWidget);
    // One per section: PHP and webservers are scanned separately.
    expect(find.widgetWithText(TextButton, 'Re-scan'), findsWidgets);
  });

  testWidgets('status is not told by colour alone', (tester) async {
    final semantics = tester.ensureSemantics();
    final daemon = fixture(_twoProjects);
    await tester.pumpWidget(wrap(ProjectsPage(daemon: daemon)));

    // Each row reads as one item that says its status in words.
    expect(find.bySemanticsLabel(RegExp(r'Running[\s\S]*my-kirby-site')), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp(r'Stopped[\s\S]*legacy-app')), findsOneWidget);
    semantics.dispose();
  });

  // features/settings.feature — "Choosing light or dark appearance"
  testWidgets('the appearance setting picks the theme', (tester) async {
    final daemon = fixture(_twoProjects.replaceFirst('"root":', '"appearance": "dark", "root":'));
    tall(tester);
    await tester.pumpWidget(
      MaterialApp(
        theme: wharfTheme(Brightness.light),
        darkTheme: wharfTheme(Brightness.dark),
        themeMode: themeModeFor(daemon.state.appearance),
        home: SettingsPage(daemon: daemon),
      ),
    );
    await tester.pump();

    expect(find.text('Appearance'), findsOneWidget);
    final picker = tester.widget<SegmentedButton<String>>(find.byType(SegmentedButton<String>));
    expect(picker.selected, {'dark'});
    expect(Theme.of(tester.element(find.text('Appearance'))).brightness, Brightness.dark);
    expect(themeModeFor('system'), ThemeMode.system);
    expect(themeModeFor('light'), ThemeMode.light);
  });

  // features/webserver-install.feature — "Installing nginx"
  testWidgets('a missing webserver Wharf can install offers to, then shows progress', (
    tester,
  ) async {
    final semantics = tester.ensureSemantics();
    final installable = _withNginx(
      '"installed": false, "binary": "/Users/x/Wharf/bin/nginx/nginx", "projects": [], '
      '"install": {"installable": true, "via": "download", "hint": "Downloads the latest nginx."}',
    );
    tall(tester);
    await tester.pumpWidget(wrap(SettingsPage(daemon: fixture(installable))));
    await tester.pump();
    expect(find.widgetWithText(FilledButton, 'Install nginx'), findsOneWidget);

    final installing = installable.replaceFirst(
      '"projects": [], "install"',
      '"projects": [], "installing": true, "install"',
    );
    await tester.pumpWidget(wrap(SettingsPage(daemon: fixture(installing))));
    await tester.pump();
    expect(find.text('Installing…'), findsOneWidget);
    expect(find.bySemanticsLabel(RegExp('Installing nginx')), findsOneWidget);
    expect(find.widgetWithText(FilledButton, 'Install nginx'), findsNothing);
    semantics.dispose();
  });

  // features/webserver-install.feature — "A webserver Wharf cannot install says
  // how to get it"
  testWidgets('a webserver Wharf cannot install says how to get it', (tester) async {
    final daemon = fixture(
      _twoProjects.replaceFirst(
        '"binary": "/Users/x/Wharf/bin/apache/httpd", "projects": []',
        '"binary": "/Users/x/Wharf/bin/apache/httpd", "projects": [], '
            '"install": {"installable": false, "hint": "Install Apache with your package manager."}',
      ),
    );
    tall(tester);
    await tester.pumpWidget(wrap(SettingsPage(daemon: daemon)));
    await tester.pump();

    expect(find.textContaining('Install Apache with your package manager.'), findsOneWidget);
    expect(find.widgetWithText(FilledButton, 'Install apache'), findsNothing);
  });
}

/// The two-project snapshot with nginx described by [fields].
String _withNginx(String fields) => _twoProjects.replaceFirst(
  '"installed": true, "binary": "/Users/x/Wharf/bin/nginx/nginx",\n         "projects": ["my-kirby-site"]',
  fields,
);

const _twoProjects = '''
{
  "root": "/Users/x/Wharf",
  "www": "/Users/x/Wharf/www",
  "config": "/Users/x/Wharf/config",
  "ssl": {"installed": true, "trusted": true, "ca_root": "/Users/x/ca"},
  "services": {
    "webserver": {"active": "nginx", "available": ["apache", "nginx"], "state": "running",
      "servers": [
        {"name": "apache", "installed": false, "binary": "/Users/x/Wharf/bin/apache/httpd", "projects": []},
        {"name": "nginx", "installed": true, "binary": "/Users/x/Wharf/bin/nginx/nginx",
         "projects": ["my-kirby-site"]}
      ]},
    "php": {
      "version": "8.4", "available": ["8.1", "8.4"], "recommended": "8.5", "status": "active",
      "dir": "/Users/x/Wharf/bin/php",
      "downloadable": [{"version": "8.5", "status": "active"}, {"version": "8.3", "status": "security"}],
      "downloading": ["8.3"],
      "installs": [
        {"version": "8.4", "full_version": "8.4.3", "dir": "/opt/php84", "fastcgi": "/opt/php84/php-fpm",
         "source": "system", "status": "active"},
        {"version": "8.1", "full_version": "8.1.29", "dir": "/opt/php81", "fastcgi": "/opt/php81/php-fpm",
         "source": "system", "status": "eol"}
      ]
    }
  },
  "projects": [
    {"name": "my-kirby-site", "state": "running", "url": "http://my-kirby-site.wharf",
     "pretty_url": "http://my-kirby-site.wharf", "fallback_url": "http://127.0.0.1:8080",
     "hosts_entry": true, "webserver": "nginx", "php_version": "8.4", "port": 8080,
     "dir": "/Users/x/Code/my-kirby-site", "linked": true,
     "custom_configs": [
       {"webserver": "apache", "path": "/Users/x/Wharf/config/vhosts/my-kirby-site.apache.conf",
        "exists": false, "active": false},
       {"webserver": "nginx", "path": "/Users/x/Wharf/config/vhosts/my-kirby-site.nginx.conf",
        "exists": true, "active": true}
     ]},
    {"name": "legacy-app", "state": "stopped", "url": "http://127.0.0.1:8081",
     "fallback_url": "http://127.0.0.1:8081", "hosts_entry": false, "ssl": true,
     "webserver": "nginx", "php_version": "8.4", "port": 8081,
     "dir": "/Users/x/Wharf/www/legacy-app"}
  ],
  "unregistered": ["dropped-in"]
}
''';

const _switching = '''
{
  "root": "/Users/x/Wharf",
  "services": {
    "webserver": {"active": "apache", "available": ["apache", "nginx"],
                  "state": "starting", "switching": true},
    "php": {"version": "8.4", "available": ["8.4"], "recommended": "8.5", "status": "active",
            "installs": [{"version": "8.4", "full_version": "8.4.3", "dir": "/opt/php84",
                          "fastcgi": "/opt/php84/php-fpm", "source": "system", "status": "active"}]}
  },
  "projects": [], "unregistered": []
}
''';

const _selectedNotInstalled = '''
{
  "root": "/Users/x/Wharf",
  "services": {
    "webserver": {"active": "nginx", "available": ["nginx"], "state": "stopped"},
    "php": {"version": "8.5", "available": ["8.5"], "recommended": "8.5", "status": "active",
            "installs": [{"version": "8.4", "full_version": "8.4.3", "dir": "/opt/php84",
                          "fastcgi": "/opt/php84/php-fpm", "source": "vendored", "status": "active"}]}
  },
  "projects": [], "unregistered": []
}
''';

const _noPhp = '''
{
  "root": "/Users/x/Wharf",
  "services": {
    "webserver": {"active": "nginx", "available": ["nginx"], "state": "stopped"},
    "php": {"version": "8.5", "available": ["8.5"], "recommended": "8.5",
            "status": "active", "installs": [], "dir": "/Users/x/Wharf/bin/php",
            "downloadable": [{"version": "8.5", "status": "active"}]}
  },
  "projects": [], "unregistered": []
}
''';
