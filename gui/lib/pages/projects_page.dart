import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:url_launcher/url_launcher_string.dart';

import '../daemon.dart';
import '../models/state.dart';
import '../theme.dart';
import 'add_project.dart';
import 'project_sheet.dart';
import 'settings_page.dart';

/// The one primary view: a list of projects, each showing name, status and
/// URL. Nothing else is visible by default (dev/design-principles.md §2).
class ProjectsPage extends StatelessWidget {
  const ProjectsPage({super.key, required this.daemon, this.onCastOff});

  final Daemon daemon;

  /// Stops everything Wharf started and quits it; without one, the window
  /// offers no way out but the tray.
  final Future<void> Function()? onCastOff;

  /// The platform's command key: ⌘ on macOS, Ctrl elsewhere.
  static SingleActivator _shortcut(LogicalKeyboardKey key) =>
      defaultTargetPlatform == TargetPlatform.macOS
      ? SingleActivator(key, meta: true)
      : SingleActivator(key, control: true);

  void _openSettings(BuildContext context) =>
      Navigator.of(context)
          .push(MaterialPageRoute<void>(builder: (_) => SettingsPage(daemon: daemon)));

  @override
  Widget build(BuildContext context) {
    // INFO: Desktop conventions: ⌘, opens settings; ⌘O and ⌘N both add a
    // project, the one way in — so the keyboard reaches everything the
    // pointer does.
    return CallbackShortcuts(
      bindings: {
        _shortcut(LogicalKeyboardKey.comma): () => _openSettings(context),
        _shortcut(LogicalKeyboardKey.keyO): () => addProject(context, daemon),
        _shortcut(LogicalKeyboardKey.keyN): () => addProject(context, daemon),
      },
      child: Focus(autofocus: true, child: _scaffold(context)),
    );
  }

  Widget _scaffold(BuildContext context) {
    final state = daemon.state;
    final stop = WharfColors.of(context).stop;
    return Scaffold(
      appBar: AppBar(
        // INFO: The mark on its dark tile — the image the Windows and Linux tray
        // shows — beside the name. It says nothing the name does not, so a
        // screen reader skips it.
        title: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Image.asset(
              'assets/tray/icon_64.png',
              width: 22,
              height: 22,
              filterQuality: FilterQuality.medium,
              excludeFromSemantics: true,
            ),
            const SizedBox(width: 10),
            const Text('Wharf'),
          ],
        ),
        actions: [
          // INFO: A button, not a word in the bar: it stops everything
          // (features/tray-actions.feature, "Stopping all from the main
          // window").
          if (state.anyRunning)
            Padding(
              padding: const EdgeInsets.only(right: 4),
              child: OutlinedButton.icon(
                onPressed: daemon.stopAll,
                style: OutlinedButton.styleFrom(
                  foregroundColor: stop,
                  side: BorderSide(color: stop),
                  padding: const EdgeInsets.symmetric(horizontal: 12),
                  visualDensity: VisualDensity.compact,
                ),
                icon: const Icon(Icons.stop, size: 16),
                label: const Text('Stop all'),
              ),
            ),
          IconButton(
            tooltip: 'Settings',
            icon: const Icon(Icons.settings_outlined, size: 20),
            onPressed: () => _openSettings(context),
          ),
          const SizedBox(width: 8),
        ],
        bottom: WorkingBar(working: daemon.working),
      ),
      body: Column(
        children: [
          if (daemon.notice != null) _Notice(daemon: daemon),
          if (daemon.connection == Connection.disconnected) const _Disconnected(),
          Expanded(child: _body(context, state)),
        ],
      ),
      // INFO: Leaving stands opposite arriving: Cast off bottom left, Add
      // project bottom right (features/single-application.feature, "Casting off from
      // the main window"). The row between them lets taps through to the list.
      floatingActionButtonLocation: FloatingActionButtonLocation.centerFloat,
      floatingActionButton: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16),
        child: Row(
          children: [
            if (onCastOff != null)
              FloatingActionButton.extended(
                // WARNING: Two buttons on one page need distinct hero tags.
                heroTag: null,
                tooltip: 'Stop everything and quit Wharf',
                backgroundColor: WharfColors.of(context).castOff,
                foregroundColor: Theme.of(context).scaffoldBackgroundColor,
                onPressed: () => _castOff(context),
                icon: const Icon(Icons.sailing, size: 20),
                label: const Text('Cast off'),
              ),
            const Spacer(),
            FloatingActionButton.extended(
              onPressed: () => addProject(context, daemon),
              icon: const Icon(Icons.add, size: 20),
              label: const Text('Add project…'),
            ),
          ],
        ),
      ),
    );
  }

  /// Asks first: the window goes too, so a stray click should not end the
  /// session.
  Future<void> _castOff(BuildContext context) async {
    final castOff = WharfColors.of(context).castOff;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        icon: Icon(Icons.sailing, color: castOff),
        title: const Text('Cast off?'),
        content: const Text('Every project and service Wharf started stops, then Wharf quits.'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Stay moored'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(
              backgroundColor: castOff,
              foregroundColor: Theme.of(context).scaffoldBackgroundColor,
            ),
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Cast off'),
          ),
        ],
      ),
    );
    if (confirmed == true) await onCastOff?.call();
  }

  Widget _body(BuildContext context, WharfState state) {
    if (state.projects.isEmpty && state.unregistered.isEmpty) {
      return _Empty(daemon: daemon);
    }
    return ListView(
      padding: const EdgeInsets.only(top: 8, bottom: 96),
      children: [
        for (final project in state.projects) _ProjectRow(daemon: daemon, project: project),
        if (state.unregistered.isNotEmpty) _Unregistered(daemon: daemon, names: state.unregistered),
      ],
    );
  }
}

class _ProjectRow extends StatelessWidget {
  const _ProjectRow({required this.daemon, required this.project});

  final Daemon daemon;
  final Project project;

  @override
  Widget build(BuildContext context) {
    final muted = Theme.of(context).textTheme.bodySmall;
    return InkWell(
      onTap: () => showProjectSheet(context, daemon, project),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 14),
        child: Row(
          children: [
            // INFO: Name, status and URL read as one item: "my-kirby-site,
            // Running, http://…" rather than three stops for a screen reader.
            Expanded(
              child: MergeSemantics(
                child: Row(
                  children: [
                    StatusDot(project.state),
                    const SizedBox(width: 14),
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(project.name, style: Theme.of(context).textTheme.titleMedium),
                          const SizedBox(height: 3),
                          Row(
                            children: [
                              Flexible(
                                child: Text(
                                  project.error.isNotEmpty ? project.error : project.url,
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                  style: project.error.isNotEmpty
                                      ? muted?.copyWith(color: Theme.of(context).colorScheme.error)
                                      : muted,
                                ),
                              ),
                            ],
                          ),
                          // INFO: Only a running project has something serving it
                          // (features/app-configuration.feature).
                          if (project.isRunning) ...[
                            const SizedBox(height: 2),
                            Text(
                              project.servedBy,
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                              style: muted,
                            ),
                          ],
                        ],
                      ),
                    ),
                  ],
                ),
              ),
            ),
            _Actions(daemon: daemon, project: project),
          ],
        ),
      ),
    );
  }
}

/// The row's actions, always in one order — Open, Restart, Settings, then
/// Start or Stop — so they line up from row to row. An action the project
/// does not offer leaves its place empty (features/tray-actions.feature,
/// "Project actions keep their places in the list"). Which of them a project
/// offers is [Project.actions], which the tray reads too.
class _Actions extends StatelessWidget {
  const _Actions({required this.daemon, required this.project});

  final Daemon daemon;
  final Project project;

  /// One place in the row, the same size filled or empty.
  static Widget _place(Widget? child) => SizedBox.square(dimension: 40, child: child);

  @override
  Widget build(BuildContext context) {
    final c = WharfColors.of(context);
    final offers = project.actions;

    Widget action(ProjectAction a, IconData icon, Color color) => IconButton(
      tooltip: '${a.label} ${project.name}',
      color: color,
      style: IconButton.styleFrom(backgroundColor: color.withValues(alpha: WharfColors.actionTint)),
      icon: Icon(icon, size: 20),
      onPressed: () => daemon.projectAction(a, project.name),
    );

    final Widget? toggle;
    if (project.isBusy) {
      toggle = Center(
        child: SizedBox(
          width: 16,
          height: 16,
          // INFO: Named, or a screen reader meets a bare progress indicator
          // where the Start or Stop button was.
          child: CircularProgressIndicator(
            strokeWidth: 2,
            semanticsLabel: '${statusLabel(project.state)} ${project.name}',
          ),
        ),
      );
    } else if (offers.contains(ProjectAction.stop)) {
      toggle = action(ProjectAction.stop, Icons.stop, c.stop);
    } else if (offers.contains(ProjectAction.start)) {
      toggle = action(ProjectAction.start, Icons.play_arrow, c.start);
    } else {
      toggle = null;
    }

    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        _place(
          project.isRunning
              ? IconButton(
                  tooltip: 'Open ${project.url}',
                  icon: const Icon(Icons.open_in_new, size: 18),
                  onPressed: () => launchUrlString(project.url),
                )
              : null,
        ),
        _place(
          offers.contains(ProjectAction.restart)
              ? action(ProjectAction.restart, Icons.restart_alt, c.restart)
              : null,
        ),
        // INFO: The row itself opens the settings too, but nothing about a row
        // says so; this does (features/app-configuration.feature).
        _place(
          IconButton(
            tooltip: 'Settings for ${project.name}',
            icon: const Icon(Icons.tune, size: 18),
            onPressed: () => showProjectSheet(context, daemon, project),
          ),
        ),
        const SizedBox(width: 4),
        _place(toggle),
      ],
    );
  }
}

/// Folders in www/ that are not projects yet. "Add" opens the same sheet as
/// a folder chosen in the picker (features/project-folders.feature, "Adding a
/// folder found in www/").
class _Unregistered extends StatelessWidget {
  const _Unregistered({required this.daemon, required this.names});

  final Daemon daemon;
  final List<String> names;

  @override
  Widget build(BuildContext context) {
    final muted = Theme.of(context).textTheme.bodySmall;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const SizedBox(height: 16),
        const Divider(),
        Padding(
          padding: const EdgeInsets.fromLTRB(24, 18, 24, 6),
          child: Text('Folders in www/ that are not projects yet', style: muted),
        ),
        for (final name in names)
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 4),
            child: Row(
              children: [
                Expanded(child: Text(name)),
                // INFO: "Add" alone does not tell a screen reader what it adds.
                Tooltip(
                  message: 'Add $name as a project',
                  child: TextButton(
                    onPressed: () => addProject(context, daemon, path: wwwFolder(daemon, name)),
                    child: const Text('Add'),
                  ),
                ),
              ],
            ),
          ),
      ],
    );
  }
}

/// Before the first project: what is still missing, each with the action
/// that fixes it, then "Add project…" (features/project-folders.feature,
/// "Before the first project, Wharf says what is missing").
class _Empty extends StatelessWidget {
  const _Empty({required this.daemon});
  final Daemon daemon;

  @override
  Widget build(BuildContext context) {
    final muted = Theme.of(context).textTheme.bodySmall;
    final services = daemon.state.services;
    return Center(
      child: SingleChildScrollView(
        padding: const EdgeInsets.all(48),
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 460),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Text('No projects yet', style: Theme.of(context).textTheme.titleMedium),
              const SizedBox(height: 20),
              _PhpReadiness(daemon: daemon, php: services.php),
              const SizedBox(height: 12),
              _WebserverReadiness(daemon: daemon, webserver: services.webserver),
              const SizedBox(height: 24),
              Text(
                'A folder is a project. Pick one from anywhere on this machine — it stays '
                'where it is.',
                textAlign: TextAlign.center,
                style: muted,
              ),
              const SizedBox(height: 16),
              FilledButton.icon(
                onPressed: () => addProject(context, daemon),
                icon: const Icon(Icons.add, size: 18),
                label: const Text('Add project…'),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// One thing a project needs, ready or not. Ready or missing is said in
/// words and by the icon's shape, never by colour alone.
class _Readiness extends StatelessWidget {
  const _Readiness({required this.ready, required this.label, this.detail = '', this.action});

  final bool ready;
  final String label;
  final String detail;
  final Widget? action;

  @override
  Widget build(BuildContext context) {
    final c = WharfColors.of(context);
    final muted = Theme.of(context).textTheme.bodySmall;
    return MergeSemantics(
      child: Row(
        children: [
          Icon(
            ready ? Icons.check_circle : Icons.radio_button_unchecked,
            size: 20,
            color: ready ? c.running : c.dimmed,
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('$label — ${ready ? 'ready' : 'missing'}'),
                if (detail.isNotEmpty) Text(detail, style: muted),
              ],
            ),
          ),
          if (action != null) ...[const SizedBox(width: 12), action!],
        ],
      ),
    );
  }
}

/// PHP: the default version once one is installed, else the recommended one
/// to download.
class _PhpReadiness extends StatelessWidget {
  const _PhpReadiness({required this.daemon, required this.php});

  final Daemon daemon;
  final Php php;

  @override
  Widget build(BuildContext context) {
    if (php.available.isNotEmpty) {
      return _Readiness(ready: true, label: 'PHP ${php.version}');
    }
    final offer = php.recommended.isNotEmpty
        ? php.recommended
        : (php.downloadable.isEmpty ? '' : php.downloadable.first.version);
    final Widget? action;
    if (offer.isEmpty) {
      action = null;
    } else if (php.downloading.contains(offer)) {
      action = Semantics(
        label: 'Downloading PHP $offer',
        child: const SizedBox.square(
          dimension: 18,
          child: CircularProgressIndicator(strokeWidth: 2),
        ),
      );
    } else {
      action = FilledButton(
        onPressed: () => daemon.installPhp(offer),
        child: Text('Download PHP $offer'),
      );
    }
    return _Readiness(
      ready: false,
      label: 'PHP',
      detail: offer.isEmpty
          ? 'No PHP found on this machine — see Settings → PHP.'
          : 'No PHP found on this machine.',
      action: action,
    );
  }
}

/// The webserver: the active one once any is installed, else each one Wharf
/// can install — or how to get the one it cannot.
class _WebserverReadiness extends StatelessWidget {
  const _WebserverReadiness({required this.daemon, required this.webserver});

  final Daemon daemon;
  final Webserver webserver;

  @override
  Widget build(BuildContext context) {
    final installed = webserver.servers.where((s) => s.installed).toList();
    if (installed.isNotEmpty) {
      final shown = installed.firstWhere(
        (s) => s.name == webserver.active,
        orElse: () => installed.first,
      );
      final version = shown.version.isEmpty ? '' : ' ${shown.version}';
      return _Readiness(ready: true, label: 'Webserver: ${shown.name}$version');
    }
    final hints = [
      for (final s in webserver.servers)
        if (!s.installable && s.installHint.isNotEmpty) s.installHint,
    ];
    return _Readiness(
      ready: false,
      label: 'Webserver',
      detail: ['No webserver found on this machine.', ...hints].join('\n'),
      action: Wrap(
        spacing: 8,
        runSpacing: 8,
        children: [
          for (final s in webserver.servers)
            if (s.installable || s.installing) InstallWebserverAction(daemon: daemon, server: s),
        ],
      ),
    );
  }
}

class _Notice extends StatelessWidget {
  const _Notice({required this.daemon});
  final Daemon daemon;

  @override
  Widget build(BuildContext context) {
    // INFO: A live region, so a screen reader announces what went wrong without
    // the user having to find it.
    return Semantics(
      liveRegion: true,
      child: Material(
        color: Theme.of(context).colorScheme.surfaceContainerHighest,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(24, 10, 12, 10),
          child: Row(
            children: [
              Expanded(child: Text(daemon.notice!, style: Theme.of(context).textTheme.bodyMedium)),
              IconButton(
                tooltip: 'Dismiss',
                icon: const Icon(Icons.close, size: 16),
                onPressed: daemon.dismissNotice,
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _Disconnected extends StatelessWidget {
  const _Disconnected();

  @override
  Widget build(BuildContext context) {
    // INFO: A live region, like a notice: a lost connection is news.
    return Semantics(
      liveRegion: true,
      child: Container(
        width: double.infinity,
        color: Theme.of(context).colorScheme.errorContainer,
        padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 10),
        child: Text(
          'Wharf is starting…',
          style: TextStyle(color: Theme.of(context).colorScheme.onErrorContainer, fontSize: 12),
        ),
      ),
    );
  }
}
