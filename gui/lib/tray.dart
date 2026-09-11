import 'dart:io';

import 'package:flutter/material.dart';
import 'package:tray_manager/tray_manager.dart';
import 'package:url_launcher/url_launcher_string.dart';

import 'daemon.dart';
import 'models/state.dart';

/// The tray menu. Start, stop and open belong here, not buried in a window
/// that has to be opened for a two-second action
/// (dev/design-principles.md §2, features/tray-actions.feature).
///
/// It renders the same snapshot the window does, so the two cannot disagree.
class TrayController with TrayListener {
  TrayController(
    this.daemon, {
    required this.onOpenWindow,
    required this.onAddProject,
    required this.onQuit,
  });

  final Daemon daemon;
  final VoidCallback onOpenWindow;
  final Future<void> Function() onAddProject;

  /// Quitting stops the daemon this app started before the window goes.
  final Future<void> Function() onQuit;

  /// Ids the menu handlers dispatch on.
  static const _open = 'open';
  static const _stopAll = 'stop-all';
  static const _add = 'add';
  static const _quit = 'quit';

  Future<void> start() async {
    trayManager.addListener(this);
    await trayManager.setIcon(_iconPath(), isTemplate: Platform.isMacOS);
    daemon.addListener(rebuildMenu);
    await rebuildMenu();
  }

  Future<void> dispose() async {
    daemon.removeListener(rebuildMenu);
    trayManager.removeListener(this);
    await trayManager.destroy();
  }

  /// macOS wants a template image so the icon follows the menu bar's theme;
  /// Windows wants an .ico.
  String _iconPath() {
    if (Platform.isWindows) return 'assets/tray/icon.ico';
    return 'assets/tray/icon_32.png';
  }

  Future<void> rebuildMenu() async {
    final state = daemon.state;
    final items = <MenuItem>[MenuItem(key: _open, label: 'Open Wharf'), MenuItem.separator()];

    if (daemon.connection != Connection.connected) {
      items.add(MenuItem(label: 'Starting…', disabled: true));
    } else if (state.projects.isEmpty) {
      items.add(MenuItem(label: 'No projects yet', disabled: true));
    } else {
      for (final project in state.projects) {
        items.add(
          MenuItem.submenu(
            label: '${_marker(project)}  ${project.name}',
            submenu: Menu(
              items: [
                MenuItem(
                  key: 'start:${project.name}',
                  label: 'Start',
                  disabled: project.isRunning || project.isBusy,
                ),
                MenuItem(key: 'stop:${project.name}', label: 'Stop', disabled: !project.isRunning),
                MenuItem.separator(),
                MenuItem(
                  key: 'open:${project.name}',
                  label: project.url,
                  disabled: !project.isRunning,
                ),
              ],
            ),
          ),
        );
      }
    }

    items.addAll([
      MenuItem.separator(),
      MenuItem(key: _add, label: 'Add project…'),
      MenuItem(key: _stopAll, label: 'Stop all', disabled: !state.anyRunning),
      MenuItem.separator(),
      MenuItem(key: _quit, label: 'Quit'),
    ]);

    await trayManager.setContextMenu(Menu(items: items));
    // The icon's tooltip is the whole of the idle-versus-active signal.
    await trayManager.setToolTip(state.anyRunning ? 'Wharf — running' : 'Wharf — idle');
  }

  String _marker(Project p) {
    if (p.isBusy) return '◐';
    if (p.hasFailed) return '✕';
    return p.isRunning ? '●' : '○';
  }

  @override
  void onTrayIconMouseDown() => trayManager.popUpContextMenu();

  @override
  void onTrayIconRightMouseDown() => trayManager.popUpContextMenu();

  @override
  void onTrayMenuItemClick(MenuItem menuItem) {
    final key = menuItem.key;
    if (key == null) return;

    switch (key) {
      case _open:
        onOpenWindow();
        return;
      case _stopAll:
        daemon.stopAll();
        return;
      case _add:
        onAddProject();
        return;
      case _quit:
        onQuit();
        return;
    }

    final parts = key.split(':');
    if (parts.length != 2) return;
    final name = parts[1];
    switch (parts[0]) {
      case 'start':
        daemon.startProject(name);
      case 'stop':
        daemon.stopProject(name);
      case 'open':
        final project = daemon.state.projects.where((p) => p.name == name).firstOrNull;
        if (project != null) launchUrlString(project.url);
    }
  }
}
