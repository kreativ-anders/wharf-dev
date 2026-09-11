import 'dart:io';

import 'package:flutter/material.dart';
import 'package:window_manager/window_manager.dart';

import 'daemon.dart';
import 'folders.dart';
import 'pages/projects_page.dart';
import 'theme.dart';
import 'tray.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  await windowManager.ensureInitialized();

  // One window, no multi-window layouts (dev/design-principles.md §3). It is
  // narrow on purpose: the content is a list of names.
  await windowManager.waitUntilReadyToShow(
    const WindowOptions(
      size: Size(560, 720),
      minimumSize: Size(420, 480),
      title: 'Wharf',
      titleBarStyle: TitleBarStyle.normal,
      backgroundColor: Colors.transparent,
    ),
    () async {
      await windowManager.show();
      await windowManager.focus();
    },
  );

  runApp(const WharfApp());
}

class WharfApp extends StatefulWidget {
  const WharfApp({super.key});

  @override
  State<WharfApp> createState() => _WharfAppState();
}

/// Extra wharfd arguments from the environment. Only `make gui` sets this, to
/// point the daemon at a throwaway hosts file; a user never needs it.
List<String> _daemonArgsFromEnv() {
  final raw = Platform.environment['WHARFD_ARGS'] ?? '';
  return raw.split(RegExp(r'\s+')).where((a) => a.isNotEmpty).toList();
}

class _WharfAppState extends State<WharfApp> with WindowListener {
  final _daemon = Daemon(daemonArgs: _daemonArgsFromEnv());
  final _navigator = GlobalKey<NavigatorState>();
  TrayController? _tray;

  @override
  void initState() {
    super.initState();
    windowManager.addListener(this);
    // Closing the window leaves the tray running: this is a tray-resident app,
    // and quitting is an explicit choice in the tray menu.
    windowManager.setPreventClose(true);
    _daemon.start();
    _startTray();
  }

  Future<void> _startTray() async {
    final tray = TrayController(
      _daemon,
      onOpenWindow: _showWindow,
      onAddProject: _addProject,
      onQuit: _quit,
    );
    await tray.start();
    if (mounted) setState(() => _tray = tray);
  }

  /// Quit from the tray. The daemon this app started goes with it, taking
  /// its webservers along; a daemon that was already running is left alone.
  /// A force quit skips this — the daemon's parent watchdog covers that.
  Future<void> _quit() async {
    await _tray?.dispose();
    await _daemon.shutdown();
    await windowManager.destroy();
  }

  Future<void> _showWindow() async {
    await windowManager.show();
    await windowManager.focus();
  }

  /// "Add project…" from the tray: a folder picker, and the chosen folder is
  /// added without opening the main window (features/tray-actions.feature).
  Future<void> _addProject() => pickAndAddFolder(_daemon);

  @override
  void onWindowClose() async {
    if (await windowManager.isPreventClose()) {
      await windowManager.hide();
    }
  }

  @override
  void dispose() {
    windowManager.removeListener(this);
    _tray?.dispose();
    _daemon.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Wharf',
      navigatorKey: _navigator,
      debugShowCheckedModeBanner: false,
      theme: wharfTheme(Brightness.light),
      darkTheme: wharfTheme(Brightness.dark),
      home: ListenableBuilder(
        listenable: _daemon,
        builder: (context, _) => ProjectsPage(daemon: _daemon),
      ),
    );
  }
}
