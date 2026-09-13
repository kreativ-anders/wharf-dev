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

  /// macOS wants a template image so the icon follows the menu bar's theme —
  /// 64 px, because it is drawn at 18 pt and a Retina bar needs 36. Windows
  /// and Linux bars may be light or dark and tint nothing, so there the mark
  /// sits on its own dark tile; Windows wants that as an .ico.
  /// (gui/tool/draw_icons.py draws them all.)
  String _iconPath() {
    if (Platform.isMacOS) return 'assets/tray/template_64.png';
    if (Platform.isWindows) return 'assets/tray/icon.ico';
    return 'assets/tray/icon_32.png';
  }

  /// What the menu showed when it was last built. The daemon notifies on
  /// every snapshot and every pending click, and most change nothing here.
  String? _shown;

  Future<void> rebuildMenu() async {
    final state = daemon.state;
    final connected = daemon.connection == Connection.connected;
    // INFO: The native menu is rebuilt only when something it shows changed.
    final shown = [
      connected,
      state.anyRunning,
      for (final p in state.projects) '${p.name} ${_marker(p)} ${p.actions} ${p.url} ${p.isRunning}',
    ].join('\n');
    if (shown == _shown) return;
    _shown = shown;

    final items = <MenuItem>[MenuItem(key: _open, label: 'Open Wharf'), MenuItem.separator()];

    if (!connected) {
      items.add(MenuItem(label: 'Starting…', disabled: true));
    } else if (state.projects.isEmpty) {
      items.add(MenuItem(label: 'No projects yet', disabled: true));
    } else {
      for (final project in state.projects) {
        items.add(
          MenuItem.submenu(
            label: '${_marker(project)}  ${project.name}',
            submenu: Menu(items: projectMenuItems(project)),
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
    // INFO: The icon's tooltip is the whole of the idle-versus-active signal.
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
    if (parts[0] == 'open') {
      final project = daemon.state.projects.where((p) => p.name == name).firstOrNull;
      if (project != null) launchUrlString(project.url);
      return;
    }
    final action = ProjectAction.values.where((a) => a.name == parts[0]).firstOrNull;
    if (action != null) daemon.projectAction(action, name);
  }
}

/// A project's tray submenu. Every action is listed so the menu keeps its
/// shape; those that do not fit the project's state are disabled — the same
/// ones its row in the window leaves out (features/tray-actions.feature).
List<MenuItem> projectMenuItems(Project project) => [
  for (final action in ProjectAction.values)
    MenuItem(
      key: '${action.name}:${project.name}',
      label: action.label,
      disabled: !project.actions.contains(action),
    ),
  MenuItem.separator(),
  MenuItem(key: 'open:${project.name}', label: project.url, disabled: !project.isRunning),
];
