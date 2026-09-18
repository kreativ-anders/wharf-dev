import 'package:flutter/material.dart';

import '../daemon.dart';
import '../models/state.dart';
import '../theme.dart';

/// The webservers every config template has rules for, as the daemon names
/// them and as the window labels them.
const _servers = {'nginx': 'nginx', 'apache': 'Apache'};

/// Opens a config template in Wharf's own editor: its nginx rules or its
/// Apache rules, one at a time, with line numbers (features/config-templates
/// .feature, "Editing a config template in Wharf"). The rules are the
/// daemon's; the window only holds what is typed until it is saved.
Future<void> showConfigTemplateEditor(
  BuildContext context,
  Daemon daemon,
  ConfigTemplate template,
) {
  return showDialog<void>(
    context: context,
    barrierDismissible: false,
    builder: (_) => _TemplateEditor(daemon: daemon, template: template),
  );
}

/// "New template…": asks for a name, creates the template, and opens the
/// editor on it (features/config-templates.feature, "Creating a config
/// template").
Future<void> showNewConfigTemplate(BuildContext context, Daemon daemon) async {
  final id = await showDialog<String>(
    context: context,
    builder: (_) => _NewTemplate(daemon: daemon),
  );
  if (id == null || !context.mounted) return;
  final created =
      daemon.state.configTemplates.where((t) => t.id == id).firstOrNull ??
      ConfigTemplate(id: id, name: id);
  await showConfigTemplateEditor(context, daemon, created);
}

/// Opens a project's custom config in Wharf's editor: the rules for the
/// webserver serving it, and only those — or, while it has none, its config
/// template's rules to start from (features/app-configuration.feature, "A
/// project's custom config starts from its config template").
Future<void> showCustomConfigEditor(BuildContext context, Daemon daemon, Project project) {
  return showDialog<void>(
    context: context,
    barrierDismissible: false,
    builder: (_) => _CustomConfigEditor(daemon: daemon, project: project),
  );
}

class _CustomConfigEditor extends StatefulWidget {
  const _CustomConfigEditor({required this.daemon, required this.project});

  final Daemon daemon;
  final Project project;

  @override
  State<_CustomConfigEditor> createState() => _CustomConfigEditorState();
}

class _CustomConfigEditorState extends State<_CustomConfigEditor> {
  final _text = TextEditingController();
  CustomConfigRules? _rules;
  var _saving = false;
  String? _error;

  String get _server => _servers[_rules?.webserver] ?? _rules?.webserver ?? '';
  bool get _dirty => _rules != null && _text.text != _rules!.content;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final rules = await widget.daemon.readCustomConfig(widget.project.name);
      _text.text = rules.content;
      _rules = rules;
    } catch (e) {
      _error = '$e';
    }
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    _text.dispose();
    super.dispose();
  }

  Future<void> _run(Future<void> Function() action) async {
    setState(() {
      _saving = true;
      _error = null;
    });
    try {
      await action();
      if (mounted) _close(context);
    } catch (e) {
      if (mounted) setState(() => _error = '$e');
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  Future<void> _useTemplate() async {
    final template = widget.daemon.state.configTemplates
        .where((t) => t.id == widget.project.template)
        .firstOrNull;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Use the config template again?'),
        content: Text(
          'This project\'s custom $_server config is deleted, and ${widget.project.name} is served '
          'with ${template == null ? 'Wharf\'s plain block' : 'the ${template.name} config template'} again.',
        ),
        actions: [
          TextButton(onPressed: () => _close(context, false), child: const Text('Cancel')),
          FilledButton(onPressed: () => _close(context, true), child: const Text('Delete')),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    await _run(() => widget.daemon.deleteCustomConfig(widget.project.name, _rules!.webserver));
  }

  Future<void> _cancel() async {
    if (_dirty) {
      final discard = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('Discard changes?'),
          content: Text('Your changes to ${widget.project.name}\'s $_server config are not saved.'),
          actions: [
            TextButton(onPressed: () => _close(context, false), child: const Text('Keep editing')),
            FilledButton(onPressed: () => _close(context, true), child: const Text('Discard')),
          ],
        ),
      );
      if (discard != true) return;
    }
    if (mounted) _close(context);
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final colors = WharfColors.of(context);
    final muted = theme.textTheme.bodySmall;
    final size = MediaQuery.sizeOf(context);
    final rules = _rules;

    return Dialog(
      insetPadding: const EdgeInsets.all(24),
      child: ConstrainedBox(
        constraints: BoxConstraints(maxWidth: 860, maxHeight: size.height * 0.9),
        child: Padding(
          padding: const EdgeInsets.fromLTRB(24, 20, 24, 16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Semantics(
                header: true,
                child: Text(
                  'Custom $_server config for ${widget.project.name}',
                  style: theme.textTheme.titleLarge,
                ),
              ),
              const SizedBox(height: 4),
              Text(
                rules == null || rules.exists
                    ? 'Used instead of the config template while $_server serves this project.'
                    : 'Starts from the project\'s config template. Saving makes it this project\'s own, '
                          'used instead of the template while $_server serves it.',
                style: muted,
              ),
              const SizedBox(height: 12),
              // INFO: The warning says so in words and with an icon, never by
              // colour alone (dev/design-principles.md §4).
              DecoratedBox(
                decoration: BoxDecoration(
                  border: Border.all(color: colors.busy),
                  borderRadius: BorderRadius.circular(6),
                ),
                child: Padding(
                  padding: const EdgeInsets.all(10),
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Icon(Icons.warning_amber_rounded, size: 18, color: colors.busy),
                      const SizedBox(width: 8),
                      Expanded(
                        child: Text(
                          'Keep every {{…}}. Wharf fills them in each time the project starts — '
                          'its ports, certificate, host name, folder and PHP — and they change. '
                          'A fixed port or server name in their place makes the project unreachable '
                          'or takes a port another project needs.',
                          style: theme.textTheme.bodySmall?.copyWith(
                            color: theme.colorScheme.onSurface,
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
              ),
              const SizedBox(height: 12),
              Expanded(
                child: rules == null && _error == null
                    ? const Center(child: CircularProgressIndicator())
                    : LineNumberedEditor(
                        controller: _text,
                        label: '$_server rules of ${widget.project.name}',
                      ),
              ),
              if (_error != null)
                Padding(
                  padding: const EdgeInsets.only(top: 8),
                  child: Text(_error!, style: TextStyle(color: colors.failed, fontSize: 12.5)),
                ),
              const SizedBox(height: 12),
              Row(
                children: [
                  if (rules != null && rules.exists)
                    TextButton(
                      onPressed: _saving ? null : _useTemplate,
                      child: const Text('Use config template'),
                    ),
                  Expanded(
                    child: Text(
                      'Saving restarts $_server for this project.',
                      style: muted,
                      textAlign: TextAlign.end,
                    ),
                  ),
                  const SizedBox(width: 8),
                  TextButton(onPressed: _saving ? null : _cancel, child: const Text('Cancel')),
                  const SizedBox(width: 8),
                  FilledButton(
                    onPressed: rules == null || _saving
                        ? null
                        : () => _run(
                            () => widget.daemon.saveCustomConfig(
                              widget.project.name,
                              rules.webserver,
                              _text.text,
                            ),
                          ),
                    child: const Text('Save'),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _TemplateEditor extends StatefulWidget {
  const _TemplateEditor({required this.daemon, required this.template});

  final Daemon daemon;
  final ConfigTemplate template;

  @override
  State<_TemplateEditor> createState() => _TemplateEditorState();
}

class _TemplateEditorState extends State<_TemplateEditor> {
  final _text = {for (final s in _servers.keys) s: TextEditingController()};
  final _saved = <String, String>{};
  var _server = _servers.keys.first;
  var _loading = true;
  var _saving = false;
  String? _error;

  bool get _dirty => _servers.keys.any((s) => _saved.containsKey(s) && _text[s]!.text != _saved[s]);

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      for (final server in _servers.keys) {
        final body = await widget.daemon.readConfigTemplate(widget.template.id, server);
        _saved[server] = body;
        _text[server]!.text = body;
      }
    } catch (e) {
      _error = '$e';
    }
    if (mounted) setState(() => _loading = false);
  }

  @override
  void dispose() {
    for (final c in _text.values) {
      c.dispose();
    }
    super.dispose();
  }

  Future<void> _save() async {
    setState(() {
      _saving = true;
      _error = null;
    });
    try {
      for (final server in _servers.keys) {
        final body = _text[server]!.text;
        if (!_saved.containsKey(server) || body == _saved[server]) continue;
        await widget.daemon.saveConfigTemplate(widget.template.id, server, body);
        _saved[server] = body;
      }
      if (mounted) _close(context);
    } catch (e) {
      if (mounted) setState(() => _error = '$e');
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  Future<void> _cancel() async {
    if (_dirty) {
      final discard = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('Discard changes?'),
          content: Text('Your changes to ${widget.template.name} are not saved.'),
          actions: [
            TextButton(onPressed: () => _close(context, false), child: const Text('Keep editing')),
            FilledButton(onPressed: () => _close(context, true), child: const Text('Discard')),
          ],
        ),
      );
      if (discard != true) return;
    }
    if (mounted) _close(context);
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final muted = theme.textTheme.bodySmall;
    final size = MediaQuery.sizeOf(context);

    return Dialog(
      insetPadding: const EdgeInsets.all(24),
      child: ConstrainedBox(
        constraints: BoxConstraints(maxWidth: 860, maxHeight: size.height * 0.9),
        child: Padding(
          padding: const EdgeInsets.fromLTRB(24, 20, 24, 16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Semantics(
                header: true,
                child: Text('Edit ${widget.template.name}', style: theme.textTheme.titleLarge),
              ),
              const SizedBox(height: 4),
              Text(
                'A whole server block, as a CMS\'s docs show it. Wharf fills in each {{…}} — '
                'where the project listens, its folder, logs and PHP — and uses the rest as written.',
                style: muted,
              ),
              const SizedBox(height: 12),
              SegmentedButton<String>(
                segments: [
                  for (final e in _servers.entries)
                    ButtonSegment(value: e.key, label: Text(e.value)),
                ],
                selected: {_server},
                showSelectedIcon: false,
                onSelectionChanged: (s) => setState(() => _server = s.first),
              ),
              const SizedBox(height: 12),
              Expanded(
                child: _loading
                    ? const Center(child: CircularProgressIndicator())
                    : LineNumberedEditor(
                        key: ValueKey(_server),
                        controller: _text[_server]!,
                        label: '${_servers[_server]} rules of ${widget.template.name}',
                      ),
              ),
              if (_error != null)
                Padding(
                  padding: const EdgeInsets.only(top: 8),
                  child: Text(
                    _error!,
                    style: TextStyle(color: WharfColors.of(context).failed, fontSize: 12.5),
                  ),
                ),
              const SizedBox(height: 12),
              Row(
                children: [
                  Expanded(
                    child: Text(
                      'Saving restarts the webserver of every running project using it.',
                      style: muted,
                    ),
                  ),
                  TextButton(onPressed: _saving ? null : _cancel, child: const Text('Cancel')),
                  const SizedBox(width: 8),
                  FilledButton(
                    onPressed: _loading || _saving || _saved.isEmpty ? null : _save,
                    child: const Text('Save'),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _NewTemplate extends StatefulWidget {
  const _NewTemplate({required this.daemon});

  final Daemon daemon;

  @override
  State<_NewTemplate> createState() => _NewTemplateState();
}

class _NewTemplateState extends State<_NewTemplate> {
  final _name = TextEditingController();
  String? _error;
  var _creating = false;

  @override
  void dispose() {
    _name.dispose();
    super.dispose();
  }

  Future<void> _create() async {
    if (_name.text.trim().isEmpty) return;
    setState(() {
      _creating = true;
      _error = null;
    });
    try {
      final id = await widget.daemon.createConfigTemplate(_name.text);
      if (mounted) _close(context, id);
    } catch (e) {
      if (mounted) setState(() => _error = '$e');
    } finally {
      if (mounted) setState(() => _creating = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('New config template'),
      content: SizedBox(
        width: 380,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              'It starts from the block a project without a template gets, for nginx and Apache.',
              style: Theme.of(context).textTheme.bodySmall,
            ),
            const SizedBox(height: 12),
            TextField(
              controller: _name,
              autofocus: true,
              decoration: InputDecoration(labelText: 'Name', errorText: _error),
              onSubmitted: (_) => _create(),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(onPressed: () => _close(context), child: const Text('Cancel')),
        FilledButton(onPressed: _creating ? null : _create, child: const Text('Create')),
      ],
    );
  }
}

/// A plain text field with line numbers beside it, so an error that names a
/// line can be found. Lines do not wrap — a wrapped line would put the
/// numbers out of step — so a long one scrolls sideways.
class LineNumberedEditor extends StatefulWidget {
  const LineNumberedEditor({super.key, required this.controller, required this.label});

  final TextEditingController controller;

  /// What a screen reader calls the field.
  final String label;

  @override
  State<LineNumberedEditor> createState() => _LineNumberedEditorState();
}

class _LineNumberedEditorState extends State<LineNumberedEditor> {
  final _vertical = ScrollController();
  final _horizontal = ScrollController();

  @override
  void dispose() {
    _vertical.dispose();
    _horizontal.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final colors = WharfColors.of(context);
    // INFO: The numbers and the text share one font, size and fixed line
    // height, so line 40 of the numbers sits beside line 40 of the text.
    final style = TextStyle(
      fontFamily: 'monospace',
      fontFamilyFallback: const ['Menlo', 'Consolas', 'DejaVu Sans Mono', 'Courier New'],
      fontSize: 13,
      height: 1.45,
      color: theme.colorScheme.onSurface,
    );
    final strut = StrutStyle.fromTextStyle(style, forceStrutHeight: true);

    return DecoratedBox(
      decoration: BoxDecoration(
        border: Border.all(color: colors.border),
        borderRadius: BorderRadius.circular(6),
      ),
      child: ListenableBuilder(
        listenable: widget.controller,
        builder: (context, _) {
          final lines = '\n'.allMatches(widget.controller.text).length + 1;
          final longest = widget.controller.text
              .split('\n')
              .fold(0, (max, line) => line.length > max ? line.length : max);
          final painter = TextPainter(
            text: TextSpan(text: 'M' * (longest + 2), style: style),
            textDirection: TextDirection.ltr,
          )..layout();
          final textWidth = painter.width;
          painter.dispose();

          return Scrollbar(
            controller: _vertical,
            child: SingleChildScrollView(
              controller: _vertical,
              padding: const EdgeInsets.symmetric(vertical: 10),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  ExcludeSemantics(
                    child: Container(
                      padding: const EdgeInsets.only(left: 10, right: 12),
                      child: Text(
                        List.generate(lines, (i) => '${i + 1}').join('\n'),
                        textAlign: TextAlign.right,
                        style: style.copyWith(color: colors.dimmed),
                        strutStyle: strut,
                      ),
                    ),
                  ),
                  Expanded(
                    child: LayoutBuilder(
                      builder: (context, constraints) => Scrollbar(
                        controller: _horizontal,
                        child: SingleChildScrollView(
                          controller: _horizontal,
                          scrollDirection: Axis.horizontal,
                          child: SizedBox(
                            width: textWidth > constraints.maxWidth
                                ? textWidth
                                : constraints.maxWidth,
                            // INFO: Merged, so a screen reader names the field itself —
                            // it cannot see which segment is selected above it.
                            child: MergeSemantics(
                              child: Semantics(
                                label: widget.label,
                                child: TextField(
                                  controller: widget.controller,
                                  maxLines: null,
                                  keyboardType: TextInputType.multiline,
                                  style: style,
                                  strutStyle: strut,
                                  autocorrect: false,
                                  enableSuggestions: false,
                                  smartDashesType: SmartDashesType.disabled,
                                  smartQuotesType: SmartQuotesType.disabled,
                                  decoration: const InputDecoration.collapsed(hintText: null),
                                ),
                              ),
                            ),
                          ),
                        ),
                      ),
                    ),
                  ),
                ],
              ),
            ),
          );
        },
      ),
    );
  }
}

// WARNING: Drop focus before popping: a control still focused when its dialog's
// subtree is torn out in the same frame makes Windows log an AXTree error
// while it reconciles the accessibility tree against the vanished node.
void _close<T extends Object?>(BuildContext context, [T? result]) {
  FocusManager.instance.primaryFocus?.unfocus();
  Navigator.pop(context, result);
}
