import 'dart:io';

import 'package:flutter/material.dart';

import '../daemon.dart';
import '../folders.dart';
import '../ipc/client.dart';
import '../models/state.dart';
import '../project_name.dart';
import 'project_sheet.dart';
import 'settings_page.dart';

/// "Add project…": the one way in. A folder picker, then a sheet proposing
/// the name, config template and webserver, then the project runs
/// (features/project-folders.feature). With [path] — a folder in www/ added
/// from the list — the picker is skipped.
// TODO(drag-and-drop): a folder dropped on the window opens the same sheet —
// once people ask for it; the picker is the whole way in until then.
Future<void> addProject(BuildContext context, Daemon daemon, {String? path}) async {
  path ??= await pickFolder(daemon.state.www);
  if (path == null || !context.mounted) return;
  final proposal = await daemon.inspectFolder(path);
  if (proposal == null || !context.mounted) return;

  // INFO: A folder that is a project already is shown, not added twice
  // (features/project-folders.feature, "Choosing a folder that is already a
  // project").
  if (proposal.project.isNotEmpty) {
    for (final p in daemon.state.projects) {
      if (p.name == proposal.project) {
        return showProjectSheet(context, daemon, p);
      }
    }
    return;
  }
  await showDialog<void>(
    context: context,
    builder: (_) => AddProjectSheet(daemon: daemon, proposal: proposal),
  );
}

/// The path of a folder in www/ that is not a project yet.
String wwwFolder(Daemon daemon, String name) => '${daemon.state.www}${Platform.pathSeparator}$name';

/// What Wharf proposes for the chosen folder, to change or confirm.
class AddProjectSheet extends StatefulWidget {
  const AddProjectSheet({super.key, required this.daemon, required this.proposal});

  final Daemon daemon;
  final FolderProposal proposal;

  @override
  State<AddProjectSheet> createState() => _AddProjectSheetState();
}

class _AddProjectSheetState extends State<AddProjectSheet> {
  late final _name = TextEditingController(text: widget.proposal.name);
  late final _focus = FocusNode()..addListener(_rewriteOnLeave);
  late String _template = widget.proposal.template;
  late String _webserver = widget.daemon.state.services.webserver.active;
  var _adding = false;

  /// Why the daemon refused the last "Add" — a name that is taken, mostly.
  String? _refused;

  @override
  void dispose() {
    _focus.dispose();
    _name.dispose();
    super.dispose();
  }

  // INFO: The field becomes the project name once the user is done with it, never
  // under the cursor while they type (features/project-folders.feature, "The
  // proposed name comes from the folder name").
  void _rewriteOnLeave() {
    if (!_focus.hasFocus) _rewrite();
  }

  void _rewrite() {
    final name = projectName(_name.text);
    // INFO: Input with nothing usable stays as typed, next to the error explaining it.
    if (name.isEmpty || name == _name.text) return;
    _name.value = TextEditingValue(
      text: name,
      selection: TextSelection.collapsed(offset: name.length),
    );
  }

  Future<void> _add() async {
    final name = projectName(_name.text);
    if (name.isEmpty || _adding) return;
    _rewrite();
    setState(() {
      _adding = true;
      _refused = null;
    });
    try {
      await widget.daemon.addFolderAs(
        widget.proposal.path,
        name: name,
        template: _template,
        // INFO: The active webserver is what every project gets anyway; only the
        // other one is an override.
        webserver: _webserver == widget.daemon.state.services.webserver.active ? '' : _webserver,
      );
      if (mounted) Navigator.pop(context);
    } on DaemonError catch (e) {
      // INFO: The sheet stays open on the answer, so a taken name is fixed right
      // here (features/project-folders.feature, "A folder whose name is
      // already taken").
      if (mounted) setState(() => _refused = e.message);
    } catch (e) {
      if (mounted) setState(() => _refused = '$e');
    } finally {
      if (mounted) setState(() => _adding = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    // INFO: The snapshot keeps changing while the sheet is open — a webserver
    // installing, for one — so the sheet follows it.
    return ListenableBuilder(listenable: widget.daemon, builder: (context, _) => _sheet(context));
  }

  Widget _sheet(BuildContext context) {
    final muted = Theme.of(context).textTheme.bodySmall;
    final state = widget.daemon.state;
    final typed = _name.text.trim();
    final name = projectName(typed);
    final detected = widget.proposal.template;
    final templates = state.configTemplates;
    final fixed = widget.proposal.fixed;
    return AlertDialog(
      title: const Text('Add project'),
      content: ConstrainedBox(
        constraints: const BoxConstraints(minWidth: 380, maxWidth: 460),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(widget.proposal.path, style: muted, maxLines: 2, overflow: TextOverflow.ellipsis),
            const SizedBox(height: 16),
            TextField(
              controller: _name,
              focusNode: _focus,
              autofocus: !fixed,
              readOnly: fixed,
              decoration: InputDecoration(
                labelText: 'Name',
                helperText: fixed
                    ? 'The name of its folder in www/'
                    : 'Lowercase letters, digits and hyphens',
                errorText: typed.isNotEmpty && name.isEmpty
                    ? 'Use at least one letter or digit'
                    : _refused,
                errorMaxLines: 3,
              ),
              onChanged: (_) => setState(() => _refused = null),
              onSubmitted: (_) => _add(),
            ),
            const SizedBox(height: 6),
            // INFO: The name alone in the field, its address beneath: the same
            // whichever webserver serves it, since the front door forwards.
            Text(name.isEmpty ? 'http://<name>.localhost' : 'http://$name.localhost', style: muted),
            const SizedBox(height: 20),
            DropdownButtonFormField<String>(
              initialValue: _template.isEmpty || templates.any((t) => t.id == _template)
                  ? _template
                  : '',
              decoration: InputDecoration(
                labelText: 'Config template',
                helperText: detected.isEmpty
                    ? 'Nothing detected — plain PHP needs none'
                    : _template == detected
                    ? 'Detected in the folder'
                    : null,
              ),
              items: [
                const DropdownMenuItem(value: '', child: Text('None')),
                for (final t in templates) DropdownMenuItem(value: t.id, child: Text(t.name)),
              ],
              onChanged: (v) => setState(() => _template = v ?? ''),
            ),
            const SizedBox(height: 20),
            DropdownButtonFormField<String>(
              initialValue: _webserver,
              decoration: const InputDecoration(labelText: 'Webserver'),
              items: [
                for (final w in state.services.webserver.available)
                  DropdownMenuItem(value: w, child: Text(w)),
              ],
              onChanged: (v) => setState(() => _webserver = v ?? _webserver),
            ),
            ..._missingWebserver(context),
          ],
        ),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.pop(context), child: const Text('Cancel')),
        FilledButton(onPressed: name.isEmpty || _adding ? null : _add, child: const Text('Add')),
      ],
    );
  }

  /// The picked webserver is not installed: say so and offer to install it
  /// (features/project-folders.feature, "Adding a project for a webserver
  /// that is not installed").
  List<Widget> _missingWebserver(BuildContext context) {
    final server = widget.daemon.state.services.webserver.servers
        .where((s) => s.name == _webserver)
        .firstOrNull;
    if (server == null || server.installed) return const [];
    return [
      const SizedBox(height: 12),
      Row(
        children: [
          Expanded(
            child: Text(
              server.installable || server.installHint.isEmpty
                  ? '${server.name} is not installed.'
                  : '${server.name} is not installed. ${server.installHint}',
              style: Theme.of(context).textTheme.bodySmall,
            ),
          ),
          const SizedBox(width: 12),
          InstallWebserverAction(daemon: widget.daemon, server: server),
        ],
      ),
    ];
  }
}
