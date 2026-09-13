import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
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

/// A fixture daemon that answers "Edit php.ini" with the snapshot's path, as
/// the real one does once the file exists.
class _PhpSettingsDaemon extends Daemon {
  _PhpSettingsDaemon(String json) : super(root: '/tmp/wharf-test') {
    state = WharfState.fromJson(jsonDecode(json) as Map<String, dynamic>);
  }

  @override
  Future<void> editPhpSettings(Future<void> Function(String) open) =>
      open(state.services.php.settings);
}

/// A fixture daemon that records what casting off asks of it.
class _CastOffDaemon extends Daemon {
  _CastOffDaemon(String json) : super(root: '/tmp/wharf-test') {
    state = WharfState.fromJson(jsonDecode(json) as Map<String, dynamic>);
  }

  final calls = <String>[];

  @override
  Future<void> stopAll() async => calls.add('stop all');

  @override
  Future<void> shutdown({bool includingAttached = false}) async =>
      calls.add(includingAttached ? 'shutdown, attached too' : 'shutdown');
}

/// A fixture daemon that records which PHP versions it is asked to remove or
/// hide, and which hidden folders to show again.
class _PhpDaemon extends Daemon {
  _PhpDaemon(String json) : super(root: '/tmp/wharf-test') {
    state = WharfState.fromJson(jsonDecode(json) as Map<String, dynamic>);
  }

  final calls = <String>[];

  @override
  Future<void> removePhp(String version) async => calls.add('remove $version');

  @override
  Future<void> unhidePhp(String dir) async => calls.add('show $dir');
}

/// One PHP Wharf downloaded, one found on the machine, one folder hidden.
const _phpRemovable = '''
{
  "root": "/Users/x/Wharf",
  "services": {
    "webserver": {"active": "nginx", "available": ["nginx"], "state": "stopped"},
    "php": {"version": "8.4", "available": ["8.2", "8.4"], "recommended": "8.5", "status": "active",
            "dir": "/Users/x/Wharf/bin/php",
            "installs": [
              {"version": "8.4", "full_version": "8.4.3", "dir": "/opt/php84",
               "fastcgi": "/opt/php84/php-fpm", "source": "system", "status": "active"},
              {"version": "8.2", "full_version": "8.2.28", "dir": "/Users/x/Wharf/bin/php/8.2",
               "fastcgi": "/Users/x/Wharf/bin/php/8.2/php-fpm", "source": "vendored", "status": "security"}
            ],
            "hidden": ["/opt/php83"]}
  },
  "projects": [], "unregistered": []
}
''';

Widget wrap(Widget child) => MaterialApp(theme: wharfTheme(Brightness.light), home: child);

/// Settings pages are lazy lists; a tall surface builds all of one.
void tall(WidgetTester tester) {
  tester.view.physicalSize = const Size(800, 2400);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
}

/// Opens Settings on one page.
Future<void> showSettings(
  WidgetTester tester,
  Daemon daemon, [
  SettingsSection page = SettingsSection.general,
]) async {
  tall(tester);
  await tester.pumpWidget(wrap(SettingsPage(daemon: daemon, initial: page)));
  await tester.pump();
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
  // features/single-application.feature — "A click is acknowledged while Wharf works"
  testWidgets('the window shows it is working until an action is done', (tester) async {
    final semantics = tester.ensureSemantics();
    final opening = Completer<void>();
    final previous = openFolder;
    openFolder = (_) => opening.future;
    addTearDown(() => openFolder = previous);

    final daemon = fixture(_twoProjects);
    await tester.pumpWidget(
      wrap(ListenableBuilder(listenable: daemon, builder: (_, _) => ProjectsPage(daemon: daemon))),
    );
    expect(find.byType(LinearProgressIndicator), findsNothing);

    await tester.tap(find.byTooltip('Open www folder'));
    await tester.pump();
    expect(find.bySemanticsLabel('Working…'), findsOneWidget);

    opening.complete();
    await tester.pump();
    expect(find.byType(LinearProgressIndicator), findsNothing);
    semantics.dispose();
  });

  // features/app-configuration.feature — "A running project shows what serves it"
  testWidgets('a running project shows its webserver and PHP versions', (tester) async {
    final daemon = fixture(_twoProjects);
    await tester.pumpWidget(wrap(ProjectsPage(daemon: daemon)));

    expect(find.text('nginx 1.27.3 · PHP 8.4.3'), findsOneWidget);
    // INFO: legacy-app carries the same versions but is stopped: nothing serves it.
    expect(find.textContaining('· PHP'), findsOneWidget);
  });

  testWidgets('the project list shows name, status and URL, and nothing else', (tester) async {
    final daemon = fixture(_twoProjects);
    await tester.pumpWidget(wrap(ProjectsPage(daemon: daemon)));

    expect(find.text('my-kirby-site'), findsOneWidget);
    expect(find.text('http://my-kirby-site.localhost'), findsOneWidget);
    expect(find.byType(StatusDot), findsNWidgets(2));

    // INFO: A running project offers Stop, Restart and an Open action; a stopped
    // one offers Start. Each offers its settings. Nothing else is on the row.
    expect(find.byIcon(Icons.stop), findsNWidgets(2), reason: 'the row, and Stop all');
    expect(find.byIcon(Icons.restart_alt), findsOneWidget);
    expect(find.byIcon(Icons.play_arrow), findsOneWidget);
    expect(find.byIcon(Icons.open_in_new), findsOneWidget);
    expect(find.byTooltip('Settings for my-kirby-site'), findsOneWidget);
    expect(find.byTooltip('Settings for legacy-app'), findsOneWidget);
  });

  testWidgets('the title bar carries the mark beside the name', (tester) async {
    final semantics = tester.ensureSemantics();
    await tester.pumpWidget(wrap(ProjectsPage(daemon: fixture(_twoProjects))));

    final mark = find.descendant(of: find.byType(AppBar), matching: find.byType(Image));
    expect(mark, findsOneWidget);
    expect(tester.getCenter(mark).dx, lessThan(tester.getCenter(find.text('Wharf')).dx));
    // INFO: It repeats the name, so a screen reader hears "Wharf" once.
    expect(tester.widget<Image>(mark).excludeFromSemantics, isTrue);
    semantics.dispose();
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

      // INFO: The tray lists every action, enabling exactly the ones the row shows.
      final enabled = projectMenuItems(project)
          .where((i) => i.key != null && !i.key!.startsWith('open:') && !i.disabled)
          .map((i) => i.label)
          .toList();
      expect(enabled, actions.map((a) => a.label).toList(), reason: 'tray, $state');
    }

    // INFO: The row shows them as labelled buttons: a failed project can be
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

  // features/tray-actions.feature — "Project actions keep their places in the
  // list"
  testWidgets('row actions keep one order and their places, each in its colour', (tester) async {
    final daemon = fixture(_twoProjects);
    await tester.pumpWidget(wrap(ProjectsPage(daemon: daemon)));
    double x(String tooltip) => tester.getCenter(find.byTooltip(tooltip)).dx;

    // INFO: Open, Restart, Settings, then Stop, left to right.
    final running = [
      x('Open http://my-kirby-site.localhost'),
      x('Restart my-kirby-site'),
      x('Settings for my-kirby-site'),
      x('Stop my-kirby-site'),
    ];
    expect(running, orderedEquals(List.of(running)..sort()));
    // INFO: A stopped project leaves Open and Restart empty, so its Settings and
    // Start stand exactly under the running project's Settings and Stop.
    expect(x('Settings for legacy-app'), running[2]);
    expect(x('Start legacy-app'), running[3]);

    IconButton button(String tooltip) => tester.widget<IconButton>(
      find.ancestor(of: find.byTooltip(tooltip), matching: find.byType(IconButton)).first,
    );
    const c = WharfColors.light;
    expect(button('Start legacy-app').color, c.start);
    expect(button('Stop my-kirby-site').color, c.stop);
    expect(button('Restart my-kirby-site').color, c.restart);
    // INFO: Neutral actions stay ink.
    expect(button('Settings for my-kirby-site').color, isNull);
  });

  // features/tray-actions.feature — "Stopping all from the main window"
  testWidgets('stop all is a button while anything runs', (tester) async {
    await tester.pumpWidget(wrap(ProjectsPage(daemon: fixture(_twoProjects))));
    expect(find.widgetWithText(OutlinedButton, 'Stop all'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Stop all'), findsNothing);

    final idle = _twoProjects
        .replaceFirst('"state": "running"', '"state": "stopped"')
        .replaceFirst('"state": "running"', '"state": "stopped"');
    await tester.pumpWidget(wrap(ProjectsPage(daemon: fixture(idle))));
    expect(find.text('Stop all'), findsNothing);
  });

  // features/single-application.feature — "Casting off from the main window"
  testWidgets('cast off stands opposite New project, stops everything, then quits', (
    tester,
  ) async {
    final daemon = _CastOffDaemon(_twoProjects);
    var quit = 0;
    await tester.pumpWidget(
      wrap(
        ProjectsPage(
          daemon: daemon,
          onCastOff: () async {
            await daemon.castOff();
            quit++;
          },
        ),
      ),
    );

    final castOff = find.widgetWithText(FloatingActionButton, 'Cast off');
    final newProject = find.widgetWithText(FloatingActionButton, 'New project');
    final middle = tester.getSize(find.byType(Scaffold)).width / 2;
    expect(tester.getCenter(castOff).dx, lessThan(middle));
    expect(tester.getCenter(newProject).dx, greaterThan(middle));
    expect(tester.getCenter(castOff).dy, closeTo(tester.getCenter(newProject).dy, 0.01));
    expect(tester.widget<FloatingActionButton>(castOff).backgroundColor, WharfColors.light.castOff);

    // INFO: Staying moored leaves everything as it was.
    await tester.tap(castOff);
    await tester.pumpAndSettle();
    expect(find.text('Cast off?'), findsOneWidget);
    await tester.tap(find.text('Stay moored'));
    await tester.pumpAndSettle();
    expect(daemon.calls, isEmpty);
    expect(quit, 0);

    // INFO: Confirmed: everything stops before the daemon and the app go.
    await tester.tap(castOff);
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, 'Cast off'));
    await tester.pumpAndSettle();
    expect(daemon.calls, ['stop all', 'shutdown, attached too']);
    expect(quit, 1);
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
    // INFO: One custom config per webserver, the one in use marked as such.
    expect(find.text('nginx · in use'), findsOneWidget);
    expect(find.text('apache'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Edit'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Create'), findsOneWidget);
  });

  // features/project-logs.feature — "Opening a project's logs"
  testWidgets('a project\'s logs open from its settings', (tester) async {
    final opened = captureOpened();
    await tester.pumpWidget(wrap(ProjectsPage(daemon: fixture(_twoProjects))));

    await tester.tap(find.byTooltip('Settings for my-kirby-site'));
    await tester.pumpAndSettle();
    expect(find.text('Logs'), findsOneWidget);
    expect(find.text('/Users/x/Wharf/data/log/projects/my-kirby-site'), findsOneWidget);
    await tester.ensureVisible(find.text('Open logs'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Open logs'));

    expect(opened, ['/Users/x/Wharf/data/log/projects/my-kirby-site']);
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

    await showSettings(tester, daemon, SettingsSection.ssl);
    expect(find.widgetWithText(FilledButton, 'Trust certificates'), findsOneWidget);
  });

  // features/pretty-urls.feature — "Every project is reachable under its own
  // .localhost name"
  testWidgets('every project shows its .localhost URL, with no port', (tester) async {
    final daemon = fixture(_twoProjects);
    await tester.pumpWidget(wrap(ProjectsPage(daemon: daemon)));

    expect(find.text('http://my-kirby-site.localhost'), findsOneWidget);
    // INFO: legacy-app runs on Apache behind the nginx front door: still no port.
    expect(find.text('https://legacy-app.localhost'), findsOneWidget);
    expect(find.textContaining(':80'), findsNothing);
  });

  // features/quick-app-php.feature — "Choosing the webserver while creating a
  // project"
  testWidgets('a new project gets a name, a template and a webserver', (tester) async {
    final daemon = fixture(_twoProjects);
    daemon.templates = const [
      Template(id: 'kirby', name: 'Kirby', runtime: 'php'),
      Template(id: 'empty', name: 'Empty folder', runtime: 'php'),
    ];
    await tester.pumpWidget(wrap(ProjectsPage(daemon: daemon)));

    await tester.tap(find.text('New project'));
    await tester.pumpAndSettle();
    expect(find.widgetWithText(TextField, 'Name'), findsOneWidget);
    expect(find.text('Kirby'), findsOneWidget);
    // INFO: The active webserver is picked to begin with; there is no "Default".
    expect(find.text('nginx'), findsOneWidget);
    expect(find.textContaining('Default'), findsNothing);

    await tester.tap(find.text('nginx'));
    await tester.pumpAndSettle();
    expect(find.text('apache').last, findsOneWidget);
    await tester.tap(find.text('apache').last);
    await tester.pumpAndSettle();
    expect(find.text('apache'), findsOneWidget);

    await tester.enterText(find.widgetWithText(TextField, 'Name'), 'blog');
    await tester.pump();
    // INFO: The address does not depend on the webserver picked.
    expect(find.text('http://blog.localhost'), findsOneWidget);
    expect(find.widgetWithText(FilledButton, 'Create'), findsOneWidget);
  });

  // features/quick-app-php.feature — "A typed name becomes a project name"
  testWidgets('the name field becomes a project name once the user leaves it', (tester) async {
    final daemon = fixture(_twoProjects);
    daemon.templates = const [Template(id: 'empty', name: 'Empty folder', runtime: 'php')];
    await tester.pumpWidget(wrap(ProjectsPage(daemon: daemon)));
    await tester.tap(find.text('New project'));
    await tester.pumpAndSettle();

    final field = find.widgetWithText(TextField, 'Name');
    await tester.enterText(field, 'Müller & Söhne');
    await tester.pump();
    // INFO: While typing, the field keeps what was typed; the address already
    // shows the rewritten name.
    expect(find.text('Müller & Söhne'), findsOneWidget);
    expect(find.text('http://mueller-soehne.localhost'), findsOneWidget);

    await tester.sendKeyEvent(LogicalKeyboardKey.tab);
    await tester.pump();
    expect(find.text('Müller & Söhne'), findsNothing);
    expect(find.text('mueller-soehne'), findsOneWidget);

    // INFO: A name with no letter or digit in it is refused, asking for at least one
    await tester.enterText(field, '!!!');
    await tester.pump();
    expect(find.text('Use at least one letter or digit'), findsOneWidget);
    expect(tester.widget<FilledButton>(find.widgetWithText(FilledButton, 'Create')).onPressed, isNull);
  });

  // features/settings.feature — "Reset asks first"
  testWidgets('reset warns first, naming what is deleted and what is kept', (tester) async {
    final daemon = fixture(_twoProjects);
    await showSettings(tester, daemon);

    await tester.ensureVisible(find.text('Reset Wharf…'));
    await tester.tap(find.text('Reset Wharf…'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 500));

    expect(find.text('Reset Wharf?'), findsOneWidget);
    // INFO: legacy-app lives in www/, and so does the unregistered folder: both go.
    expect(find.text('•  legacy-app'), findsOneWidget);
    expect(find.text('•  dropped-in'), findsOneWidget);
    // INFO: my-kirby-site was added from elsewhere: it stays where it is.
    expect(find.textContaining('/Users/x/Code/my-kirby-site'), findsOneWidget);
    // INFO: The whole config folder goes.
    expect(find.textContaining('Everything in /Users/x/Wharf/config goes too'), findsOneWidget);

    // INFO: And nothing is deleted unless the user confirms
    await tester.tap(find.text('Cancel'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 500));
    expect(find.text('Reset Wharf?'), findsNothing);
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
    // INFO: No drag-and-drop wording: it is a folder picker, and the folder can be
    // anywhere.
    expect(find.textContaining('Drop'), findsNothing);
    expect(find.widgetWithText(OutlinedButton, 'Add folder…'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Open www folder'), findsOneWidget);
  });

  // features/service-management.feature — "Port conflict on switch"
  testWidgets('a webserver switch is shown as switching, not as stopped', (tester) async {
    await showSettings(tester, fixture(_switching), SettingsSection.webserver);

    expect(find.text('switching webserver…'), findsOneWidget);
  });

  // features/settings.feature — "Settings screen hides roadmap services in v1"
  testWidgets('settings list only General, Webserver, PHP and SSL', (tester) async {
    await showSettings(tester, fixture(_twoProjects));

    final pages = [
      for (final e in find
          .byWidgetPredicate(
            (w) => w.key is ValueKey<String> && (w.key! as ValueKey<String>).value.startsWith('nav:'),
          )
          .evaluate())
        (e.widget.key! as ValueKey<String>).value,
    ];
    expect(pages, ['nav:general', 'nav:webserver', 'nav:php', 'nav:ssl']);
    for (final label in ['General', 'Webserver', 'PHP', 'SSL']) {
      expect(find.text(label), findsWidgets, reason: label);
    }
    for (final roadmap in ['MySQL', 'PostgreSQL', 'Mailpit', 'Database', 'Mail']) {
      expect(find.text(roadmap), findsNothing, reason: '$roadmap is a roadmap capability');
    }
  });

  // features/settings.feature — "Settings pages are chosen from a side
  // navigation"
  testWidgets('settings pages are chosen from a navigation on the left', (tester) async {
    await showSettings(tester, fixture(_twoProjects));

    // INFO: General holds the appearance, the config folder, the version and the reset.
    expect(find.text('Appearance'), findsOneWidget);
    expect(find.text('Open config folder'), findsOneWidget);
    expect(find.text('Version'), findsOneWidget);
    expect(find.text('Reset Wharf…'), findsOneWidget);
    final php = find.byKey(const ValueKey('nav:php'));
    expect(tester.getCenter(php).dx, lessThan(tester.getCenter(find.text('Appearance')).dx));

    // INFO: Choosing a page shows that page alone.
    await tester.tap(php);
    await tester.pump();
    expect(find.text('PHP 8.4.3'), findsOneWidget);
    expect(find.text('Appearance'), findsNothing);
    expect(find.text('Reset Wharf…'), findsNothing);

    // INFO: The narrowest window Wharf allows keeps the icons, each still named,
    // and nothing on the page is cut off.
    tester.view.physicalSize = const Size(420, 1600);
    await tester.pump();
    expect(find.byTooltip('Webserver'), findsOneWidget);
  });

  // features/settings.feature — "General shows the version, and no update
  // check yet"
  testWidgets('General shows the version and an update check that is off', (tester) async {
    await showSettings(tester, fixture(_twoProjects));

    expect(find.text('Wharf v0.1.0'), findsOneWidget);
    final check = tester.widget<OutlinedButton>(
      find.widgetWithText(OutlinedButton, 'Check for updates'),
    );
    expect(check.onPressed, isNull, reason: 'the update check is not built yet');
    expect(find.textContaining('only look when you ask'), findsOneWidget);

    // INFO: A GUI no daemon has answered yet says so, rather than showing "Wharf ".
    await showSettings(tester, fixture('{}'));
    expect(find.text('Version unknown'), findsOneWidget);
  });

  // features/php-runtime.feature — "First start prefers a supported version
  // over a newer unsupported one"
  testWidgets('the PHP picker shows each version with its support status', (tester) async {
    await showSettings(tester, fixture(_twoProjects), SettingsSection.php);

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
    await showSettings(tester, fixture(_twoProjects), SettingsSection.php);

    await tester.tap(find.byTooltip('Open /opt/php84'));

    expect(opened, ['/opt/php84']);
  });

  // features/php-runtime.feature — "Removing a downloaded PHP version"
  testWidgets('a downloaded PHP version is removed once the user confirms', (tester) async {
    final daemon = _PhpDaemon(_phpRemovable);
    await showSettings(tester, daemon, SettingsSection.php);

    await tester.tap(find.byTooltip('Remove PHP 8.2.28'));
    await tester.pumpAndSettle();
    expect(find.text('Remove PHP 8.2.28?'), findsOneWidget);
    expect(
      find.descendant(
        of: find.byType(AlertDialog),
        matching: find.textContaining('/Users/x/Wharf/bin/php/8.2'),
      ),
      findsOneWidget,
      reason: 'the dialog names the folder it deletes',
    );

    // INFO: Cancelling removes nothing.
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();
    expect(daemon.calls, isEmpty);

    await tester.tap(find.byTooltip('Remove PHP 8.2.28'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(FilledButton, 'Remove'));
    await tester.pumpAndSettle();
    expect(daemon.calls, ['remove 8.2']);
  });

  // features/php-runtime.feature — "Hiding a PHP version found on the machine"
  testWidgets('a PHP version found on the machine is hidden, never deleted', (tester) async {
    final daemon = _PhpDaemon(_phpRemovable);
    await showSettings(tester, daemon, SettingsSection.php);

    expect(find.byTooltip('Remove PHP 8.4.3'), findsNothing);
    await tester.tap(find.byTooltip('Hide PHP 8.4.3 from Wharf'));
    await tester.pump();

    expect(find.byType(AlertDialog), findsNothing, reason: 'hiding is undone with one click');
    expect(daemon.calls, ['remove 8.4']);
  });

  // features/php-runtime.feature — "Showing a hidden PHP version again"
  testWidgets('a hidden PHP folder can be shown again', (tester) async {
    final daemon = _PhpDaemon(_phpRemovable);
    await showSettings(tester, daemon, SettingsSection.php);

    expect(find.text('Hidden'), findsOneWidget);
    expect(find.text('/opt/php83'), findsOneWidget);
    await tester.tap(find.byTooltip('Show /opt/php83 in the picker again'));
    await tester.pump();

    expect(daemon.calls, ['show /opt/php83']);
  });

  // features/php-runtime.feature — "Only supported versions are offered for
  // download"
  // features/php-runtime.feature — "Downloading a PHP version that is not
  // installed"
  // features/php-runtime.feature — "A download offer names the release it
  // downloads"
  testWidgets('supported versions that are not installed can be downloaded', (tester) async {
    await showSettings(tester, fixture(_twoProjects), SettingsSection.php);

    // INFO: Named by the release it fetches where that has been looked up…
    expect(find.text('PHP 8.5.1'), findsOneWidget);
    // INFO: …and by its minor version where it has not.
    expect(find.text('PHP 8.3'), findsOneWidget);
    expect(find.text('Downloading…'), findsOneWidget, reason: '8.3 is downloading');
    expect(
      find.widgetWithText(TextButton, 'Download'),
      findsOneWidget,
      reason: 'only 8.5 offers it',
    );
  });

  // features/php-settings.feature — "Editing PHP settings"
  testWidgets('php.ini opens in the editor from the PHP page', (tester) async {
    final edited = <String>[];
    final previous = editFile;
    editFile = (path) async => edited.add(path);
    addTearDown(() => editFile = previous);
    await showSettings(tester, _PhpSettingsDaemon(_twoProjects), SettingsSection.php);

    expect(find.text('PHP settings'), findsOneWidget);
    await tester.ensureVisible(find.text('Edit php.ini'));
    await tester.tap(find.text('Edit php.ini'));
    await tester.pump();

    expect(edited, ['/Users/x/Wharf/config/php.ini']);
  });

  // features/settings.feature — "Webserver status while nothing is running"
  testWidgets('an idle webserver says when it starts, and names its projects on hover', (
    tester,
  ) async {
    final daemon = fixture(_twoProjects.replaceFirst('"state": "running"', '"state": "stopped"'));
    await showSettings(tester, daemon, SettingsSection.webserver);

    expect(find.text('Starts with the first project'), findsOneWidget);
    expect(find.text('stopped'), findsNothing);
    // INFO: Name and version, not where the copy came from.
    expect(find.text('nginx 1.27.3'), findsOneWidget);
    expect(find.textContaining('Homebrew'), findsNothing);
    expect(find.textContaining('on this machine'), findsNothing);
    // INFO: The projects it serves are in its tooltip, not in the list.
    expect(find.textContaining('my-kirby-site'), findsNothing);
    expect(find.byTooltip('Serves my-kirby-site'), findsOneWidget);
    expect(find.byTooltip('Serves no project'), findsOneWidget);
  });

  // features/settings.feature — "A webserver that is not installed says where
  // it belongs"
  testWidgets('a missing webserver says where its binary goes', (tester) async {
    final opened = captureOpened();
    await showSettings(tester, fixture(_twoProjects), SettingsSection.webserver);

    expect(
      find.text('Not installed — expected at /Users/x/Wharf/bin/apache/httpd'),
      findsOneWidget,
    );
    await tester.tap(find.byTooltip('Open the folder apache belongs in'));
    expect(opened, ['/Users/x/Wharf/bin/apache']);
  });

  testWidgets('a selected version that is not installed is called out', (tester) async {
    await showSettings(tester, fixture(_selectedNotInstalled), SettingsSection.php);

    // INFO: The list renders, but nothing in it is selected — so say why.
    expect(find.textContaining('PHP 8.5 is selected but is not installed'), findsOneWidget);
    expect(find.text('PHP 8.4.3'), findsOneWidget);
  });

  // features/php-runtime.feature — "First start with no PHP installed"
  testWidgets('with no PHP installed the picker says where to put one', (tester) async {
    await showSettings(tester, fixture(_noPhp), SettingsSection.php);

    expect(find.text('No PHP installation found on this machine.'), findsOneWidget);
    expect(find.textContaining('/Users/x/Wharf/bin/php'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Download'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Re-scan'), findsOneWidget);
  });

  testWidgets('status is not told by colour alone', (tester) async {
    final semantics = tester.ensureSemantics();
    final daemon = fixture(_twoProjects);
    await tester.pumpWidget(wrap(ProjectsPage(daemon: daemon)));

    // INFO: Each row reads as one item that says its status in words.
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
    await showSettings(tester, fixture(installable), SettingsSection.webserver);
    expect(find.widgetWithText(FilledButton, 'Install nginx'), findsOneWidget);

    final installing = installable.replaceFirst(
      '"projects": [], "install"',
      '"projects": [], "installing": true, "install"',
    );
    await showSettings(tester, fixture(installing), SettingsSection.webserver);
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
    await showSettings(tester, daemon, SettingsSection.webserver);

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
  "version": "v0.1.0",
  "www": "/Users/x/Wharf/www",
  "config": "/Users/x/Wharf/config",
  "ssl": {"installed": true, "trusted": true, "ca_root": "/Users/x/ca"},
  "services": {
    "webserver": {"active": "nginx", "available": ["apache", "nginx"], "state": "running",
      "servers": [
        {"name": "apache", "installed": false, "binary": "/Users/x/Wharf/bin/apache/httpd", "projects": []},
        {"name": "nginx", "installed": true, "binary": "/Users/x/Wharf/bin/nginx/nginx",
         "projects": ["my-kirby-site"], "version": "1.27.3", "source": "homebrew"}
      ]},
    "php": {
      "version": "8.4", "available": ["8.1", "8.4"], "recommended": "8.5", "status": "active",
      "dir": "/Users/x/Wharf/bin/php",
      "settings": "/Users/x/Wharf/config/php.ini",
      "downloadable": [{"version": "8.5", "status": "active", "full_version": "8.5.1"},
                       {"version": "8.3", "status": "security"}],
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
    {"name": "my-kirby-site", "state": "running", "url": "http://my-kirby-site.localhost",
     "webserver": "nginx", "php_version": "8.4", "port": 8080,
     "webserver_version": "1.27.3", "php_full_version": "8.4.3",
     "dir": "/Users/x/Code/my-kirby-site", "linked": true,
     "log_dir": "/Users/x/Wharf/data/log/projects/my-kirby-site",
     "custom_configs": [
       {"webserver": "apache", "path": "/Users/x/Wharf/config/vhosts/my-kirby-site.apache.conf",
        "exists": false, "active": false},
       {"webserver": "nginx", "path": "/Users/x/Wharf/config/vhosts/my-kirby-site.nginx.conf",
        "exists": true, "active": true}
     ]},
    {"name": "legacy-app", "state": "stopped", "url": "https://legacy-app.localhost",
     "ssl": true,
     "webserver": "nginx", "php_version": "8.4", "port": 8081,
     "webserver_version": "1.27.3", "php_full_version": "8.4.3",
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
