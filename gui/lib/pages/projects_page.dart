import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:url_launcher/url_launcher_string.dart';

import '../daemon.dart';
import '../folders.dart';
import '../models/state.dart';
import '../theme.dart';
import 'project_sheet.dart';
import 'settings_page.dart';

/// The one primary view: a list of projects, each showing name, status and
/// URL. Nothing else is visible by default (dev/design-principles.md §2).
class ProjectsPage extends StatelessWidget {
  const ProjectsPage({super.key, required this.daemon});

  final Daemon daemon;

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
    return Scaffold(
      appBar: AppBar(
        title: const Text('Wharf'),
        actions: [
          if (state.anyRunning)
            TextButton(onPressed: daemon.stopAll, child: const Text('Stop all')),
          IconButton(
            tooltip: 'Open www folder',
            icon: const Icon(Icons.folder_open, size: 20),
            onPressed: state.www.isEmpty ? null : () => openFolder(state.www),
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
        bottom: state.busy
            ? const PreferredSize(
                preferredSize: Size.fromHeight(1),
                child: LinearProgressIndicator(minHeight: 1),
              )
            : null,
      ),
      body: Column(
        children: [
          if (daemon.notice != null) _Notice(daemon: daemon),
          if (daemon.connection == Connection.disconnected) const _Disconnected(),
          Expanded(child: _body(context, state)),
        ],
      ),
      floatingActionButton: FloatingActionButton.extended(
        onPressed: () => _newProject(context),
        icon: const Icon(Icons.add, size: 20),
        label: const Text('New project'),
      ),
    );
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

  Future<void> _newProject(BuildContext context) async {
    final result = await showDialog<_NewProject>(
      context: context,
      builder: (_) => _NewProjectDialog(templates: daemon.templates),
    );
    if (result == null) return;
    await daemon.scaffold(result.templateId, result.name);
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
                              if (!project.hostsEntry) ...[
                                const SizedBox(width: 8),
                                Tooltip(
                                  message: 'No hosts entry — the elevation prompt was declined',
                                  child: Icon(Icons.lock_open, size: 13, color: muted?.color),
                                ),
                              ],
                            ],
                          ),
                        ],
                      ),
                    ),
                  ],
                ),
              ),
            ),
            if (project.isRunning)
              IconButton(
                tooltip: 'Open ${project.url}',
                icon: const Icon(Icons.open_in_new, size: 18),
                onPressed: () => launchUrlString(project.url),
              ),
            // The row itself opens the settings too, but nothing about a row
            // says so; this does (features/app-configuration.feature).
            IconButton(
              tooltip: 'Settings for ${project.name}',
              icon: const Icon(Icons.tune, size: 18),
              onPressed: () => showProjectSheet(context, daemon, project),
            ),
            _Actions(daemon: daemon, project: project),
          ],
        ),
      ),
    );
  }
}

/// The row's start, stop and restart buttons — whichever of them fit the
/// project's state (features/tray-actions.feature).
class _Actions extends StatelessWidget {
  const _Actions({required this.daemon, required this.project});

  final Daemon daemon;
  final Project project;

  static const _icons = {
    ProjectAction.start: Icons.play_arrow,
    ProjectAction.stop: Icons.stop,
    ProjectAction.restart: Icons.restart_alt,
  };

  @override
  Widget build(BuildContext context) {
    if (project.isBusy) {
      return const Padding(
        padding: EdgeInsets.all(12),
        child: SizedBox(width: 16, height: 16, child: CircularProgressIndicator(strokeWidth: 2)),
      );
    }
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        for (final action in project.actions)
          IconButton(
            tooltip: '${action.label} ${project.name}',
            icon: Icon(_icons[action], size: 20),
            onPressed: () => daemon.projectAction(action, project.name),
          ),
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
                  onPressed: www.isEmpty ? null : () => openFolder(www),
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
  const _NewProject(this.templateId, this.name);
  final String templateId;
  final String name;
}

class _NewProjectDialog extends StatefulWidget {
  const _NewProjectDialog({required this.templates});
  final List<Template> templates;

  @override
  State<_NewProjectDialog> createState() => _NewProjectDialogState();
}

class _NewProjectDialogState extends State<_NewProjectDialog> {
  final _controller = TextEditingController();
  late String _template = widget.templates.isEmpty ? '' : widget.templates.first.id;

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('New project'),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          TextField(
            controller: _controller,
            autofocus: true,
            decoration: const InputDecoration(
              labelText: 'Name',
              helperText: 'Lowercase letters, digits and hyphens',
            ),
            onChanged: (_) => setState(() {}),
          ),
          const SizedBox(height: 20),
          if (widget.templates.length > 1)
            DropdownButtonFormField<String>(
              initialValue: _template,
              decoration: const InputDecoration(labelText: 'Template'),
              items: [
                for (final t in widget.templates)
                  DropdownMenuItem(value: t.id, child: Text(t.name)),
              ],
              onChanged: (v) => setState(() => _template = v ?? _template),
            )
          else if (widget.templates.length == 1)
            Text(
              'From the ${widget.templates.first.name} starter kit',
              style: Theme.of(context).textTheme.bodySmall,
            ),
        ],
      ),
      actions: [
        TextButton(onPressed: () => Navigator.pop(context), child: const Text('Cancel')),
        FilledButton(
          onPressed: _controller.text.trim().isEmpty
              ? null
              : () => Navigator.pop(context, _NewProject(_template, _controller.text.trim())),
          child: const Text('Create'),
        ),
      ],
    );
  }
}
