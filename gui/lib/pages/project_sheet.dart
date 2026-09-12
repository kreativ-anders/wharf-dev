import 'package:flutter/material.dart';
import 'package:url_launcher/url_launcher_string.dart';

import '../daemon.dart';
import '../folders.dart';
import '../models/state.dart';

/// Per-project settings live behind a tap on the project, never on the main
/// list — progressive disclosure, not a wall of settings up front
/// (dev/design-principles.md §2). A dialog rather than a bottom sheet: this is
/// a desktop app, where a sheet sliding up from the bottom edge of a wide
/// window reads as a phone pattern, and a dialog gets Escape and focus
/// trapping for free.
Future<void> showProjectSheet(BuildContext context, Daemon daemon, Project project) {
  return showDialog<void>(
    context: context,
    builder: (_) => ListenableBuilder(
      listenable: daemon,
      builder: (context, _) {
        final current = daemon.state.projects.firstWhere(
          (p) => p.name == project.name,
          orElse: () => project,
        );
        return Dialog(
          insetPadding: const EdgeInsets.all(24),
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 520),
            child: _ProjectSheet(daemon: daemon, project: current),
          ),
        );
      },
    ),
  );
}

class _ProjectSheet extends StatelessWidget {
  const _ProjectSheet({required this.daemon, required this.project});

  final Daemon daemon;
  final Project project;

  @override
  Widget build(BuildContext context) {
    final php = daemon.state.services.php;
    final webserver = daemon.state.services.webserver;
    final ssl = daemon.state.ssl;
    final muted = Theme.of(context).textTheme.bodySmall;

    return SafeArea(
      child: SingleChildScrollView(
        padding: const EdgeInsets.fromLTRB(24, 16, 16, 24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Expanded(
                  child: Semantics(
                    header: true,
                    child: Text(project.name, style: Theme.of(context).textTheme.titleLarge),
                  ),
                ),
                IconButton(
                  tooltip: 'Close',
                  icon: const Icon(Icons.close, size: 18),
                  // Drop focus before the dialog's subtree is torn down, or
                  // Windows logs an AXTree error trying to reconcile a
                  // focused node that vanished mid-frame.
                  onPressed: () => _closeDialog(context),
                ),
              ],
            ),
            const SizedBox(height: 6),
            InkWell(
              onTap: () => launchUrlString(project.url),
              child: Text(
                project.url,
                style: muted?.copyWith(decoration: TextDecoration.underline),
              ),
            ),
            const SizedBox(height: 8),
            // Where the files are, one tap from the file manager
            // (features/project-folders.feature).
            Row(
              children: [
                Expanded(
                  child: Text(
                    project.dir,
                    style: muted,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                TextButton.icon(
                  onPressed: () => openFolder(project.dir),
                  icon: const Icon(Icons.folder_open, size: 16),
                  label: const Text('Open folder'),
                ),
              ],
            ),
            const SizedBox(height: 16),

            _Row(
              label: 'PHP version',
              child: DropdownButton<String>(
                value: php.available.contains(project.phpVersion) ? project.phpVersion : null,
                underline: const SizedBox.shrink(),
                items: [
                  for (final v in php.available) DropdownMenuItem(value: v, child: Text('PHP $v')),
                ],
                // The global version is no override: picking it clears one,
                // and the project follows the global setting again
                // (features/app-configuration.feature).
                onChanged: (v) => daemon.updateSettings(
                  project.name,
                  phpOverride: v == null || v == php.version ? '' : v,
                ),
              ),
            ),
            _Row(
              label: 'Webserver',
              child: DropdownButton<String>(
                value: webserver.available.contains(project.webserver) ? project.webserver : null,
                underline: const SizedBox.shrink(),
                items: [
                  for (final w in webserver.available) DropdownMenuItem(value: w, child: Text(w)),
                ],
                // Picking the active webserver clears the override: the
                // project is served by the front door itself and follows the
                // global setting (features/app-configuration.feature).
                onChanged: (v) => daemon.updateSettings(
                  project.name,
                  webserverOverride: v == null || v == webserver.active ? '' : v,
                ),
              ),
            ),
            _Row(
              label: 'SSL',
              child: Switch(
                value: project.ssl,
                onChanged: daemon.state.busy
                    ? null
                    : (v) => daemon.updateSettings(project.name, ssl: v),
              ),
            ),
            // Nothing to install by hand: the switch sets up what is missing
            // (features/local-ssl.feature).
            if (!project.ssl && !ssl.installed)
              Text(
                'Turning SSL on installs mkcert and asks once to trust its certificates.',
                style: muted,
              ),
            if (project.ssl && !ssl.trusted)
              Row(
                children: [
                  Expanded(
                    child: Text(
                      'Browsers will warn about this certificate until Wharf\'s '
                      'certificate authority is trusted.',
                      style: muted,
                    ),
                  ),
                  TextButton(onPressed: daemon.setupSsl, child: const Text('Trust…')),
                ],
              ),

            const SizedBox(height: 20),
            Text('Custom webserver config', style: Theme.of(context).textTheme.titleSmall),
            const SizedBox(height: 4),
            Text(
              'Your own directives, added to this project\'s server block. '
              'Each webserver has its own file; saving it applies the change.',
              style: muted,
            ),
            // The file in use comes first: a project just created on a chosen
            // webserver opens here, one click from that webserver's config
            // (features/quick-app-php.feature).
            for (final custom in [
              ...project.customConfigs.where((c) => c.active),
              ...project.customConfigs.where((c) => !c.active),
            ])
              _Row(
                label: custom.active ? '${custom.webserver} · in use' : custom.webserver,
                child: TextButton(
                  onPressed: () =>
                      daemon.editCustomConfig(project.name, custom.webserver, editFile),
                  child: Text(custom.exists ? 'Edit' : 'Create'),
                ),
              ),

            const SizedBox(height: 20),
            Text('Logs', style: Theme.of(context).textTheme.titleSmall),
            const SizedBox(height: 4),
            // This project's requests and errors alone, PHP's warnings
            // included (features/project-logs.feature).
            Text('Requests and errors of this project, PHP\'s included.', style: muted),
            Row(
              children: [
                Expanded(
                  child: Text(
                    project.logDir,
                    style: muted,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                TextButton.icon(
                  onPressed: project.logDir.isEmpty ? null : () => openFolder(project.logDir),
                  icon: const Icon(Icons.article_outlined, size: 16),
                  label: const Text('Open logs'),
                ),
              ],
            ),

            const SizedBox(height: 24),
            Row(
              children: [
                OutlinedButton(
                  onPressed: () async {
                    final confirmed = await _confirmRemove(context, project);
                    if (confirmed && context.mounted) {
                      _closeDialog(context);
                      await daemon.removeProject(project.name);
                    }
                  },
                  child: const Text('Remove project'),
                ),
                const Spacer(),
                FilledButton(
                  onPressed: () => project.isRunning
                      ? daemon.stopProject(project.name)
                      : daemon.startProject(project.name),
                  child: Text(project.isRunning ? 'Stop' : 'Start'),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  Future<bool> _confirmRemove(BuildContext context, Project project) async {
    final result = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: Text('Remove ${project.name}?'),
        content: Text(
          'The project is unregistered and its hosts entry removed. '
          'Its folder${project.linked ? ' (${project.dir})' : ' in www/'} is left untouched.',
        ),
        actions: [
          TextButton(
            onPressed: () => _closeDialog(context, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => _closeDialog(context, true),
            child: const Text('Remove'),
          ),
        ],
      ),
    );
    return result ?? false;
  }
}

// Drop focus before popping: a control still focused when its dialog's
// subtree is torn out in the same frame makes Windows log an AXTree error
// while it reconciles the accessibility tree against the vanished node.
void _closeDialog<T extends Object?>(BuildContext context, [T? result]) {
  FocusManager.instance.primaryFocus?.unfocus();
  Navigator.pop(context, result);
}

class _Row extends StatelessWidget {
  const _Row({required this.label, required this.child});
  final String label;
  final Widget child;

  @override
  // Merged, so a screen reader says "PHP version, PHP 8.4, pop-up
  // button" instead of announcing a control with no name.
  Widget build(BuildContext context) => MergeSemantics(
    child: Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.spaceBetween,
        children: [
          Flexible(child: Text(label)),
          child,
        ],
      ),
    ),
  );
}
