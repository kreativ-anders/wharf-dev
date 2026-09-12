import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:url_launcher/url_launcher_string.dart';

import '../daemon.dart';
import '../folders.dart';
import '../models/state.dart';
import '../project_name.dart';
import '../theme.dart';
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
    // Desktop conventions: ⌘, opens settings, ⌘O adds a folder, ⌘N starts a
    // new project — so the keyboard reaches everything the pointer does.
    return CallbackShortcuts(
      bindings: {
        _shortcut(LogicalKeyboardKey.comma): () => _openSettings(context),
        _shortcut(LogicalKeyboardKey.keyO): () => pickAndAddFolder(daemon),
        _shortcut(LogicalKeyboardKey.keyN): () => _newProject(context),
      },
      child: Focus(autofocus: true, child: _scaffold(context)),
    );
  }

  Widget _scaffold(BuildContext context) {
    final state = daemon.state;
    final stop = WharfColors.of(context).stop;
    return Scaffold(
      appBar: AppBar(
        // The mark on its dark tile — the image the Windows and Linux tray
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
          // A button, not a word in the bar: it stops everything
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
            tooltip: 'Open www folder',
            icon: const Icon(Icons.folder_open, size: 20),
            onPressed: state.www.isEmpty ? null : () => daemon.open(openFolder,state.www),
          ),
          IconButton(
            tooltip: 'Add folder…',
            icon: const Icon(Icons.create_new_folder_outlined, size: 20),
            onPressed: () => pickAndAddFolder(daemon),
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
      // Leaving stands opposite arriving: Cast off bottom left, New project
      // bottom right (features/single-application.feature, "Casting off from
      // the main window"). The row between them lets taps through to the list.
      floatingActionButtonLocation: FloatingActionButtonLocation.centerFloat,
      floatingActionButton: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16),
        child: Row(
          children: [
            if (onCastOff != null)
              FloatingActionButton.extended(
                // Two buttons on one page need distinct hero tags.
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
              onPressed: () => _newProject(context),
              icon: const Icon(Icons.add, size: 20),
              label: const Text('New project'),
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

  /// Name, template, webserver, then the new project's settings — where its
  /// webserver config is one click away (features/quick-app-php.feature).
  Future<void> _newProject(BuildContext context) async {
    final webserver = daemon.state.services.webserver;
    final result = await showDialog<_NewProject>(
      context: context,
      builder: (_) => _NewProjectDialog(
        templates: daemon.templates,
        activeWebserver: webserver.active,
        webservers: webserver.available,
      ),
    );
    if (result == null) return;
    final created = await daemon.scaffold(result.templateId, result.name, webserver: result.webserver);
    if (created != null && context.mounted) {
      await showProjectSheet(context, daemon, created);
    }
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
            // Name, status and URL read as one item: "my-kirby-site,
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
                          // Only a running project has something serving it
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
      style: IconButton.styleFrom(
        backgroundColor: color.withValues(alpha: WharfColors.actionTint),
      ),
      icon: Icon(icon, size: 20),
      onPressed: () => daemon.projectAction(a, project.name),
    );

    final Widget? toggle;
    if (project.isBusy) {
      toggle = const Center(
        child: SizedBox(width: 16, height: 16, child: CircularProgressIndicator(strokeWidth: 2)),
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
        // The row itself opens the settings too, but nothing about a row
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

/// Folders in www/ that are not projects yet. Adding one is a single tap —
/// "drop a folder in www/" is the intended way in.
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
                TextButton(onPressed: () => daemon.addProject(name), child: const Text('Add')),
              ],
            ),
          ),
      ],
    );
  }
}

class _Empty extends StatelessWidget {
  const _Empty({required this.daemon});
  final Daemon daemon;

  @override
  Widget build(BuildContext context) {
    final muted = Theme.of(context).textTheme.bodySmall;
    final www = daemon.state.www;
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(48),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text('No projects yet', style: Theme.of(context).textTheme.titleMedium),
            const SizedBox(height: 10),
            Text(
              'A folder is a project. Add one from anywhere on this machine — it '
              'stays where it is — or put one in\n$www/ and it shows up here.',
              textAlign: TextAlign.center,
              style: muted,
            ),
            const SizedBox(height: 20),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              alignment: WrapAlignment.center,
              children: [
                OutlinedButton.icon(
                  onPressed: () => pickAndAddFolder(daemon),
                  icon: const Icon(Icons.create_new_folder_outlined, size: 18),
                  label: const Text('Add folder…'),
                ),
                TextButton.icon(
                  onPressed: www.isEmpty ? null : () => daemon.open(openFolder,www),
                  icon: const Icon(Icons.folder_open, size: 18),
                  label: const Text('Open www folder'),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

class _Notice extends StatelessWidget {
  const _Notice({required this.daemon});
  final Daemon daemon;

  @override
  Widget build(BuildContext context) {
    // A live region, so a screen reader announces what went wrong without
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
    return Container(
      width: double.infinity,
      color: Theme.of(context).colorScheme.errorContainer,
      padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 10),
      child: Text(
        'Wharf is starting…',
        style: TextStyle(color: Theme.of(context).colorScheme.onErrorContainer, fontSize: 12),
      ),
    );
  }
}

class _NewProject {
  const _NewProject(this.templateId, this.name, this.webserver);
  final String templateId;
  final String name;

  /// Empty when the active webserver was picked: the project follows the
  /// global one.
  final String webserver;
}

class _NewProjectDialog extends StatefulWidget {
  const _NewProjectDialog({
    required this.templates,
    required this.activeWebserver,
    required this.webservers,
  });
  final List<Template> templates;
  final String activeWebserver;
  final List<String> webservers;

  @override
  State<_NewProjectDialog> createState() => _NewProjectDialogState();
}

class _NewProjectDialogState extends State<_NewProjectDialog> {
  final _controller = TextEditingController();
  late final _focus = FocusNode()..addListener(_rewriteOnLeave);
  late String _template = widget.templates.isEmpty ? '' : widget.templates.first.id;
  late String _webserver = widget.activeWebserver;

  @override
  void dispose() {
    _focus.dispose();
    _controller.dispose();
    super.dispose();
  }

  // The field becomes the project name once the user is done with it, never
  // under the cursor while they type (features/quick-app-php.feature, "A
  // typed name becomes a project name").
  void _rewriteOnLeave() {
    if (!_focus.hasFocus) _rewrite();
  }

  void _rewrite() {
    final name = projectName(_controller.text);
    // Input with nothing usable stays as typed, next to the error explaining it.
    if (name.isEmpty || name == _controller.text) return;
    _controller.value = TextEditingValue(
      text: name,
      selection: TextSelection.collapsed(offset: name.length),
    );
  }

  void _submit() {
    final name = projectName(_controller.text);
    if (name.isEmpty) return;
    _rewrite();
    // The active webserver is what every project gets anyway; only the other
    // one is an override.
    final pinned = _webserver == widget.activeWebserver ? '' : _webserver;
    Navigator.pop(context, _NewProject(_template, name, pinned));
  }

  @override
  Widget build(BuildContext context) {
    final muted = Theme.of(context).textTheme.bodySmall;
    final typed = _controller.text.trim();
    final name = projectName(typed);
    return AlertDialog(
      title: const Text('New project'),
      content: ConstrainedBox(
        constraints: const BoxConstraints(minWidth: 360),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            TextField(
              controller: _controller,
              focusNode: _focus,
              autofocus: true,
              decoration: InputDecoration(
                labelText: 'Name',
                helperText: 'Becomes lowercase letters, digits and hyphens',
                errorText: typed.isNotEmpty && name.isEmpty ? 'Use at least one letter or digit' : null,
              ),
              onChanged: (_) => setState(() {}),
              onSubmitted: (_) => _submit(),
            ),
            const SizedBox(height: 20),
            DropdownButtonFormField<String>(
              initialValue: _template,
              decoration: const InputDecoration(labelText: 'Template'),
              items: [
                for (final t in widget.templates)
                  DropdownMenuItem(value: t.id, child: Text(t.name)),
              ],
              onChanged: (v) => setState(() => _template = v ?? _template),
            ),
            const SizedBox(height: 20),
            DropdownButtonFormField<String>(
              initialValue: _webserver,
              decoration: const InputDecoration(labelText: 'Webserver'),
              items: [
                for (final w in widget.webservers) DropdownMenuItem(value: w, child: Text(w)),
              ],
              onChanged: (v) => setState(() => _webserver = v ?? _webserver),
            ),
            const SizedBox(height: 16),
            // The URL is the same whichever webserver is picked: the front
            // door forwards, so it never needs a port.
            Text(
              name.isEmpty ? 'Its address will be http://<name>.localhost' : 'http://$name.localhost',
              style: muted,
            ),
          ],
        ),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.pop(context), child: const Text('Cancel')),
        FilledButton(onPressed: name.isEmpty ? null : _submit, child: const Text('Create')),
      ],
    );
  }
}
