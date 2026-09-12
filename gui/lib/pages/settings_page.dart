import 'package:flutter/material.dart';

import '../daemon.dart';
import '../folders.dart';
import '../models/state.dart';
import '../theme.dart';

/// The settings pages, in the order the navigation lists them. A roadmap
/// service — a database, mail, another runtime — becomes one more entry
/// here, with its page, once it exists; v1 lists none of them
/// (features/settings.feature, "Settings screen hides roadmap services in
/// v1").
enum SettingsSection {
  general('General', Icons.settings_outlined),
  webserver('Webserver', Icons.dns_outlined),
  php('PHP', Icons.code),
  ssl('SSL', Icons.lock_outline);

  const SettingsSection(this.label, this.icon);
  final String label;
  final IconData icon;
}

/// Global settings: a navigation on the left, one page on the right
/// (features/settings.feature, "Settings pages are chosen from a side
/// navigation"). Which page is showing is all this view keeps; every setting
/// on it comes from the daemon's snapshot.
class SettingsPage extends StatefulWidget {
  const SettingsPage({super.key, required this.daemon, this.initial = SettingsSection.general});

  final Daemon daemon;
  final SettingsSection initial;

  @override
  State<SettingsPage> createState() => _SettingsPageState();
}

class _SettingsPageState extends State<SettingsPage> {
  late var _section = widget.initial;

  Daemon get daemon => widget.daemon;

  @override
  void initState() {
    super.initState();
    _opened(_section);
  }

  void _select(SettingsSection section) {
    setState(() => _section = section);
    _opened(section);
  }

  /// Opening the PHP page looks up the release each download would fetch
  /// (features/php-runtime.feature, "A download offer names the release it
  /// downloads").
  void _opened(SettingsSection section) {
    if (section == SettingsSection.php) daemon.checkPhpReleases();
  }

  @override
  Widget build(BuildContext context) {
    return ListenableBuilder(
      listenable: daemon,
      builder: (context, _) {
        return Scaffold(
          appBar: AppBar(
            title: const Text('Settings'),
            bottom: WorkingBar(working: daemon.working),
          ),
          body: LayoutBuilder(
            builder: (context, constraints) => Row(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                _Navigation(
                  selected: _section,
                  // A narrow window keeps the room for the page's paths and
                  // buttons; the navigation shows its icons only.
                  compact: constraints.maxWidth < 560,
                  onSelect: _select,
                ),
                const VerticalDivider(width: 1),
                Expanded(
                  child: ListView(
                    key: ValueKey(_section),
                    padding: const EdgeInsets.only(top: 8, bottom: 40),
                    children: [
                      if (daemon.notice != null) _NoticeBar(daemon: daemon),
                      ..._page(),
                    ],
                  ),
                ),
              ],
            ),
          ),
        );
      },
    );
  }

  List<Widget> _page() {
    final state = daemon.state;
    final services = state.services;
    return switch (_section) {
      SettingsSection.general => [
        _SectionHeader('Appearance'),
        _AppearanceSection(daemon: daemon, mode: state.appearance),
        const SizedBox(height: 28),
        _SectionHeader('Config file'),
        _ConfigSection(daemon: daemon, config: state.config),
        const SizedBox(height: 28),
        _SectionHeader('Version'),
        _VersionSection(version: state.version),
        const SizedBox(height: 28),
        _SectionHeader('Reset'),
        _ResetSection(daemon: daemon),
      ],
      SettingsSection.webserver => [
        _SectionHeader('Webserver'),
        _WebserverSection(webserver: services.webserver, daemon: daemon),
      ],
      SettingsSection.php => [
        _SectionHeader('PHP runtime'),
        _PhpSection(daemon: daemon, php: services.php),
        const SizedBox(height: 28),
        _SectionHeader('PHP settings'),
        _PhpSettingsSection(daemon: daemon, php: services.php),
      ],
      SettingsSection.ssl => [
        _SectionHeader('SSL'),
        _SslSection(daemon: daemon, ssl: state.ssl),
      ],
    };
  }
}

/// What went wrong with the last action, announced, and gone with one tap —
/// it belongs to that action, not to whichever page is showing.
class _NoticeBar extends StatelessWidget {
  const _NoticeBar({required this.daemon});
  final Daemon daemon;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
      child: Semantics(
        liveRegion: true,
        child: Material(
          color: Theme.of(context).colorScheme.surfaceContainerHighest,
          borderRadius: BorderRadius.circular(4),
          child: Padding(
            padding: const EdgeInsets.fromLTRB(16, 4, 4, 4),
            child: Row(
              children: [
                Expanded(child: Text(daemon.notice!)),
                IconButton(
                  tooltip: 'Dismiss',
                  icon: const Icon(Icons.close, size: 16),
                  onPressed: daemon.dismissNotice,
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// The list of settings pages. The selected one is marked by a bar, bold text
/// and its background, and announced as selected — never by colour alone.
class _Navigation extends StatelessWidget {
  const _Navigation({required this.selected, required this.compact, required this.onSelect});

  final SettingsSection selected;
  final bool compact;
  final ValueChanged<SettingsSection> onSelect;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: compact ? 64 : 176,
      child: FocusTraversalGroup(
        child: ListView(
          padding: const EdgeInsets.all(8),
          children: [
            for (final section in SettingsSection.values)
              _NavItem(
                section: section,
                selected: section == selected,
                compact: compact,
                onTap: () => onSelect(section),
              ),
          ],
        ),
      ),
    );
  }
}

class _NavItem extends StatelessWidget {
  const _NavItem({
    required this.section,
    required this.selected,
    required this.compact,
    required this.onTap,
  });

  final SettingsSection section;
  final bool selected;
  final bool compact;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final item = Material(
      color: selected ? theme.colorScheme.surfaceContainerHighest : Colors.transparent,
      borderRadius: BorderRadius.circular(4),
      child: InkWell(
        key: ValueKey('nav:${section.name}'),
        borderRadius: BorderRadius.circular(4),
        onTap: onTap,
        child: Container(
          height: 40,
          padding: const EdgeInsets.symmetric(horizontal: 12),
          decoration: BoxDecoration(
            border: Border(
              left: BorderSide(
                color: selected ? theme.colorScheme.onSurface : Colors.transparent,
                width: 3,
              ),
            ),
          ),
          child: Row(
            mainAxisAlignment: compact ? MainAxisAlignment.center : MainAxisAlignment.start,
            children: [
              Icon(section.icon, size: 18),
              if (!compact) ...[
                const SizedBox(width: 12),
                Flexible(
                  child: Text(
                    section.label,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(fontWeight: selected ? FontWeight.w600 : FontWeight.normal),
                  ),
                ),
              ],
            ],
          ),
        ),
      ),
    );
    return Padding(
      padding: const EdgeInsets.only(bottom: 2),
      child: Semantics(
        button: true,
        selected: selected,
        label: compact ? section.label : null,
        child: compact ? Tooltip(message: section.label, child: item) : item,
      ),
    );
  }
}

/// Light, dark, or whatever the system uses (features/settings.feature,
/// "Choosing light or dark appearance").
class _AppearanceSection extends StatelessWidget {
  const _AppearanceSection({required this.daemon, required this.mode});

  final Daemon daemon;
  final String mode;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 24),
      child: Align(
        alignment: Alignment.centerLeft,
        child: SegmentedButton<String>(
          showSelectedIcon: false,
          segments: const [
            ButtonSegment(value: 'system', label: Text('System')),
            ButtonSegment(value: 'light', label: Text('Light')),
            ButtonSegment(value: 'dark', label: Text('Dark')),
          ],
          selected: {mode},
          onSelectionChanged: (s) => daemon.setAppearance(s.first),
        ),
      ),
    );
  }
}

/// Where every setting lives, for someone who would rather edit the file.
class _ConfigSection extends StatelessWidget {
  const _ConfigSection({required this.daemon, required this.config});

  final Daemon daemon;
  final String config;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 24),
          child: Text(
            'Every setting is stored in wharf.json, which can be edited by hand too.',
            style: Theme.of(context).textTheme.bodySmall,
          ),
        ),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 12),
          child: Align(
            alignment: Alignment.centerLeft,
            child: TextButton.icon(
              onPressed: config.isEmpty ? null : () => daemon.open(openFolder,config),
              icon: const Icon(Icons.folder_open, size: 16),
              label: const Text('Open config folder'),
            ),
          ),
        ),
      ],
    );
  }
}

/// The running version, selectable for a bug report, and the update check
/// that is not built yet (features/settings.feature, "General shows the
/// version, and no update check yet"). The button is shown disabled rather
/// than left out, and the note says why, so nobody goes looking for it.
class _VersionSection extends StatelessWidget {
  const _VersionSection({required this.version});

  final String version;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SelectableText(version.isEmpty ? 'Version unknown' : 'Wharf $version'),
          const SizedBox(height: 4),
          Text(
            'Wharf does not check for updates yet. When it does, it will only look when you ask.',
            style: Theme.of(context).textTheme.bodySmall,
          ),
          const SizedBox(height: 12),
          const OutlinedButton(onPressed: null, child: Text('Check for updates')),
        ],
      ),
    );
  }
}

/// The default webserver. "Stopped" on its own reads as broken when it only
/// means no project is running, so each server says when it runs
/// (features/settings.feature, "Webserver status while nothing is running").
/// Which projects it serves is in its tooltip, not the list: it is detail,
/// and it changes with every override. One that is missing offers to install
/// itself, or says how to get it (features/webserver-install.feature).
class _WebserverSection extends StatelessWidget {
  const _WebserverSection({required this.webserver, required this.daemon});

  final Webserver webserver;
  final Daemon daemon;

  @override
  Widget build(BuildContext context) {
    final muted = Theme.of(context).textTheme.bodySmall;
    final servers = webserver.servers.isNotEmpty
        ? webserver.servers
        : [
            for (final n in webserver.available)
              Server(name: n, installed: true, binary: '', projects: const []),
          ];

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(24, 0, 24, 4),
          child: Text(
            'The default for every project. A project can choose the other one in its settings.',
            style: muted,
          ),
        ),
        RadioGroup<String>(
          groupValue: webserver.active,
          onChanged: (v) {
            if (v != null && !webserver.switching) daemon.setWebserver(v);
          },
          child: Column(
            children: [
              for (final server in servers)
                Tooltip(
                  message: server.projects.isEmpty
                      ? 'Serves no project'
                      : 'Serves ${server.projects.join(', ')}',
                  child: RadioListTile<String>(
                    value: server.name,
                    // Enabled even when missing: its explanation must stay
                    // readable, and it may be chosen before it is installed.
                    enabled: !webserver.switching,
                    title: Text(_title(server)),
                    subtitle: Text(_describe(server)),
                    isThreeLine:
                        !server.installed && !server.installable && server.installHint.isNotEmpty,
                    secondary: server.installed
                        ? null
                        : _InstallAction(daemon: daemon, server: server),
                  ),
                ),
            ],
          ),
        ),
        if (webserver.error.isNotEmpty)
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 8),
            child: Text(
              webserver.error,
              style: TextStyle(color: WharfColors.of(context).failed, fontSize: 12.5),
            ),
          ),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 12),
          child: TextButton(onPressed: daemon.rescanWebservers, child: const Text('Re-scan')),
        ),
      ],
    );
  }

  /// Name and version. Where the copy came from is left out: the answer
  /// differs per platform and changes nothing the user does.
  String _title(Server server) =>
      !server.installed || server.version.isEmpty ? server.name : '${server.name} ${server.version}';

  String _describe(Server server) {
    if (server.installing) return 'Installing…';
    if (!server.installed) {
      final expected = 'Not installed — expected at ${server.binary}';
      if (server.installable || server.installHint.isEmpty) return expected;
      return '$expected\n${server.installHint}';
    }
    if (server.name != webserver.active) {
      return server.projects.isEmpty ? 'Not used by any project' : 'Used by projects that choose it';
    }
    if (webserver.switching) return 'switching webserver…';
    if (webserver.isRunning) return 'Running';
    return 'Starts with the first project';
  }
}

/// What a missing webserver offers: installing it, a progress mark while it
/// installs, or — where Wharf cannot install it — the folder it belongs in.
class _InstallAction extends StatelessWidget {
  const _InstallAction({required this.daemon, required this.server});

  final Daemon daemon;
  final Server server;

  @override
  Widget build(BuildContext context) {
    if (server.installing) {
      return Semantics(
        label: 'Installing ${server.name}',
        child: const SizedBox(
          width: 18,
          height: 18,
          child: CircularProgressIndicator(strokeWidth: 2),
        ),
      );
    }
    if (server.installable) {
      return Tooltip(
        message: server.installHint,
        child: FilledButton(
          onPressed: daemon.state.busy ? null : () => daemon.installWebserver(server.name),
          child: Text('Install ${server.name}'),
        ),
      );
    }
    return IconButton(
      tooltip: 'Open the folder it belongs in',
      icon: const Icon(Icons.folder_open, size: 18),
      onPressed: () => daemon.open(openFolder,_parent(server.binary)),
    );
  }
}

String _parent(String path) {
  final i = path.lastIndexOf(RegExp(r'[/\\]'));
  return i <= 0 ? path : path.substring(0, i);
}

/// The PHP version picker: every installation found on this machine, with its
/// support status, and every supported version that can be downloaded
/// (features/php-runtime.feature).
class _PhpSection extends StatelessWidget {
  const _PhpSection({required this.daemon, required this.php});

  final Daemon daemon;
  final Php php;

  @override
  Widget build(BuildContext context) {
    final muted = Theme.of(context).textTheme.bodySmall;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (php.installs.isEmpty)
          Padding(
            padding: const EdgeInsets.fromLTRB(24, 0, 24, 8),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'No PHP installation found on this machine.',
                  style: Theme.of(context).textTheme.titleMedium,
                ),
                const SizedBox(height: 8),
                Text(
                  'Download one below, into ${php.dir}, or install PHP yourself and re-scan.',
                  style: muted,
                ),
              ],
            ),
          )
        // The selected version can be one that is not here: a first run with
        // nothing installed selects the newest supported release. Saying so
        // beats a list where no radio is filled in and nothing explains why.
        else if (!php.selectedIsInstalled)
          Padding(
            padding: const EdgeInsets.fromLTRB(24, 0, 24, 12),
            child: Text(
              'PHP ${php.version} is selected but is not installed. '
              'Pick one below, or download it.',
              style: muted,
            ),
          ),
        if (php.installs.isNotEmpty)
          RadioGroup<String>(
            groupValue: php.version,
            onChanged: (v) {
              if (v != null) daemon.setPhpVersion(v);
            },
            child: Column(
              children: [
                for (final install in php.installs)
                  RadioListTile<String>(
                    value: install.version,
                    enabled: install.servable,
                    // A Wrap, so a narrow window moves the badge under the
                    // version instead of cutting it off.
                    title: Wrap(
                      spacing: 10,
                      crossAxisAlignment: WrapCrossAlignment.center,
                      children: [
                        Text(
                          'PHP ${install.fullVersion.isEmpty ? install.version : install.fullVersion}',
                        ),
                        _SupportBadge(install.status),
                      ],
                    ),
                    subtitle: Text(
                      [
                        install.source == 'vendored' ? 'in Wharf' : 'found on this machine',
                        if (!install.servable) 'command line only — cannot serve requests',
                        install.dir,
                      ].join(' · '),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                    secondary: Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        IconButton(
                          tooltip: 'Open folder',
                          icon: const Icon(Icons.folder_open, size: 18),
                          onPressed: () => daemon.open(openFolder,install.dir),
                        ),
                        _RemovePhpButton(daemon: daemon, install: install),
                      ],
                    ),
                  ),
              ],
            ),
          ),
        if (php.hidden.isNotEmpty) ...[
          const SizedBox(height: 8),
          Padding(
            padding: const EdgeInsets.fromLTRB(24, 8, 24, 0),
            child: Text('Hidden', style: Theme.of(context).textTheme.titleSmall),
          ),
          for (final dir in php.hidden)
            ListTile(
              title: Text(dir, maxLines: 1, overflow: TextOverflow.ellipsis),
              trailing: Tooltip(
                message: 'Show $dir in the picker again',
                child: TextButton(
                  onPressed: () => daemon.unhidePhp(dir),
                  child: const Text('Show'),
                ),
              ),
            ),
        ],
        if (php.downloadable.isNotEmpty) ...[
          const SizedBox(height: 8),
          Padding(
            padding: const EdgeInsets.fromLTRB(24, 8, 24, 0),
            child: Text('Download', style: Theme.of(context).textTheme.titleSmall),
          ),
          for (final download in php.downloadable)
            ListTile(
              title: Wrap(
                spacing: 10,
                crossAxisAlignment: WrapCrossAlignment.center,
                children: [
                  Text(
                    'PHP ${download.fullVersion.isEmpty ? download.version : download.fullVersion}',
                  ),
                  _SupportBadge(download.status),
                ],
              ),
              trailing: php.downloading.contains(download.version)
                  ? const Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        SizedBox(
                          width: 14,
                          height: 14,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        ),
                        SizedBox(width: 10),
                        Text('Downloading…'),
                      ],
                    )
                  : TextButton(
                      onPressed: () => daemon.installPhp(download.version),
                      child: const Text('Download'),
                    ),
            ),
        ],
        Padding(
          padding: const EdgeInsets.fromLTRB(12, 8, 12, 0),
          child: Row(
            children: [
              TextButton(onPressed: daemon.rescanPhp, child: const Text('Re-scan')),
              IconButton(
                tooltip: 'Open ${php.dir}',
                icon: const Icon(Icons.folder_open, size: 18),
                onPressed: php.dir.isEmpty ? null : () => daemon.open(openFolder,php.dir),
              ),
            ],
          ),
        ),
      ],
    );
  }
}

/// Removes a PHP version Wharf downloaded, once the user has confirmed, or
/// hides one found on the machine — Wharf never deletes a PHP it did not put
/// there (features/php-runtime.feature).
class _RemovePhpButton extends StatelessWidget {
  const _RemovePhpButton({required this.daemon, required this.install});

  final Daemon daemon;
  final PhpInstall install;

  @override
  Widget build(BuildContext context) {
    final name = 'PHP ${install.fullVersion.isEmpty ? install.version : install.fullVersion}';
    if (install.source != 'vendored') {
      return IconButton(
        tooltip: 'Hide $name from Wharf',
        icon: const Icon(Icons.visibility_off_outlined, size: 18),
        onPressed: () => daemon.removePhp(install.version),
      );
    }
    return IconButton(
      tooltip: 'Remove $name',
      icon: const Icon(Icons.delete_outline, size: 18),
      onPressed: () async {
        final confirmed = await showDialog<bool>(
          context: context,
          builder: (context) {
            final scheme = Theme.of(context).colorScheme;
            return AlertDialog(
              title: Text('Remove $name?'),
              content: Text('Deletes ${install.dir}. You can download it again at any time.'),
              actions: [
                TextButton(
                  onPressed: () => Navigator.pop(context, false),
                  child: const Text('Cancel'),
                ),
                FilledButton(
                  style: FilledButton.styleFrom(
                    backgroundColor: scheme.error,
                    foregroundColor: scheme.onError,
                  ),
                  onPressed: () => Navigator.pop(context, true),
                  child: const Text('Remove'),
                ),
              ],
            );
          },
        );
        if (confirmed == true) await daemon.removePhp(install.version);
      },
    );
  }
}

/// PHP's own settings, as a php.ini of Wharf's that every version reads after
/// its own — a file an editor highlights, like custom webserver configs
/// (features/php-settings.feature).
class _PhpSettingsSection extends StatelessWidget {
  const _PhpSettingsSection({required this.daemon, required this.php});

  final Daemon daemon;
  final Php php;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'Values here win over each PHP version\'s own php.ini. Saving restarts PHP.',
            style: Theme.of(context).textTheme.bodySmall,
          ),
          const SizedBox(height: 12),
          OutlinedButton.icon(
            onPressed: () => daemon.editPhpSettings(editFile),
            icon: const Icon(Icons.edit_note, size: 18),
            label: const Text('Edit php.ini'),
          ),
        ],
      ),
    );
  }
}

/// mkcert and its certificate authority. Nothing here is needed before the
/// first project turns SSL on — the switch sets up what is missing — but a
/// declined trust prompt needs somewhere to be asked again
/// (features/local-ssl.feature, "Trust is declined").
class _SslSection extends StatelessWidget {
  const _SslSection({required this.daemon, required this.ssl});

  final Daemon daemon;
  final SslStatus ssl;

  @override
  Widget build(BuildContext context) {
    final busy = daemon.state.busy;
    return Column(
      children: [
        _StatusTile(
          ok: ssl.installed,
          title: 'mkcert',
          subtitle: ssl.installed
              ? 'Installed — makes each project\'s certificate'
              : 'Installed the first time a project turns SSL on',
        ),
        _StatusTile(
          ok: ssl.trusted,
          title: 'Certificate authority',
          subtitle: ssl.trusted
              ? 'Trusted — browsers accept Wharf\'s certificates'
              : 'Not trusted — browsers warn until it is',
          // Trusting installs mkcert first if it is missing, so this is the
          // one action the page needs.
          action: ssl.trusted
              ? (ssl.caRoot.isEmpty
                    ? null
                    : IconButton(
                        tooltip: 'Open certificate authority folder',
                        icon: const Icon(Icons.folder_open, size: 18),
                        onPressed: () => daemon.open(openFolder,ssl.caRoot),
                      ))
              : FilledButton(
                  onPressed: busy ? null : daemon.setupSsl,
                  child: const Text('Trust certificates'),
                ),
        ),
      ],
    );
  }
}

/// One thing that is set up or not. The words say which; the mark beside
/// them only repeats it.
class _StatusTile extends StatelessWidget {
  const _StatusTile({required this.ok, required this.title, required this.subtitle, this.action});

  final bool ok;
  final String title;
  final String subtitle;
  final Widget? action;

  @override
  Widget build(BuildContext context) {
    final c = WharfColors.of(context);
    return ListTile(
      leading: Icon(
        ok ? Icons.check_circle : Icons.radio_button_unchecked,
        size: 20,
        color: ok ? c.running : c.idle,
      ),
      title: Text(title),
      subtitle: Text(subtitle),
      trailing: action,
    );
  }
}

/// How well supported a version still is — the reason to pick one over
/// another. A coloured mark beside plain text: the words carry the meaning,
/// the colour only reinforces it, and small coloured text would fail contrast.
class _SupportBadge extends StatelessWidget {
  const _SupportBadge(this.status);
  final String status;

  @override
  Widget build(BuildContext context) {
    final c = WharfColors.of(context);
    final (label, color) = switch (status) {
      'active' => ('active support', c.running),
      'security' => ('security fixes only', c.busy),
      'eol' => ('end of life', c.failed),
      'unreleased' => ('not released yet', c.idle),
      _ => ('unrecognised version', c.idle),
    };
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Container(
          width: 7,
          height: 7,
          decoration: BoxDecoration(color: color, shape: BoxShape.circle),
        ),
        const SizedBox(width: 6),
        Flexible(child: Text(label, style: Theme.of(context).textTheme.bodySmall)),
      ],
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader(this.title);
  final String title;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.fromLTRB(24, 16, 24, 8),
    child: Semantics(
      header: true,
      child: Text(title, style: Theme.of(context).textTheme.titleMedium),
    ),
  );
}

/// Starting over, behind a warning that names what goes and what stays
/// (features/settings.feature, "Reset asks first").
class _ResetSection extends StatelessWidget {
  const _ResetSection({required this.daemon});
  final Daemon daemon;

  @override
  Widget build(BuildContext context) {
    final muted = Theme.of(context).textTheme.bodySmall;
    final error = Theme.of(context).colorScheme.error;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'Deletes every project in www/ and all configuration. Downloaded PHP '
            'versions and webservers are kept.',
            style: muted,
          ),
          const SizedBox(height: 12),
          OutlinedButton(
            style: OutlinedButton.styleFrom(
              foregroundColor: error,
              side: BorderSide(color: error),
            ),
            onPressed: () => _confirmReset(context, daemon),
            child: const Text('Reset Wharf…'),
          ),
        ],
      ),
    );
  }
}

/// Resets only once the user has seen, by name, every folder that is deleted
/// and every folder that is kept.
Future<void> _confirmReset(BuildContext context, Daemon daemon) async {
  final state = daemon.state;
  final deleted = [
    for (final p in state.projects)
      if (!p.linked) p.name,
    ...state.unregistered,
  ];
  final kept = [
    for (final p in state.projects)
      if (p.linked) p,
  ];
  final confirmed = await showDialog<bool>(
    context: context,
    builder: (context) {
      final theme = Theme.of(context);
      final muted = theme.textTheme.bodySmall;
      return AlertDialog(
        title: const Text('Reset Wharf?'),
        content: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 480),
          child: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Text('Everything stops, and Wharf starts over as on its first start.'),
                const SizedBox(height: 16),
                if (deleted.isEmpty)
                  const Text('There are no folders in www/ to delete.')
                else ...[
                  Text(
                    'These folders in ${state.www} are deleted, with everything in them:',
                    style: theme.textTheme.titleSmall,
                  ),
                  const SizedBox(height: 4),
                  for (final name in deleted) Text('•  $name'),
                ],
                if (kept.isNotEmpty) ...[
                  const SizedBox(height: 16),
                  Text(
                    'Added from elsewhere — removed from Wharf, but left where they are:',
                    style: theme.textTheme.titleSmall,
                  ),
                  const SizedBox(height: 4),
                  for (final p in kept) Text('•  ${p.name}  (${p.dir})'),
                ],
                const SizedBox(height: 16),
                Text(
                  'Everything in ${state.config} goes too — settings, custom webserver '
                  'configs, php.ini — along with certificates, generated files and logs. '
                  'Downloaded PHP versions and webservers are kept.',
                  style: muted,
                ),
              ],
            ),
          ),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('Cancel')),
          FilledButton(
            style: FilledButton.styleFrom(
              backgroundColor: theme.colorScheme.error,
              foregroundColor: theme.colorScheme.onError,
            ),
            onPressed: () => Navigator.pop(context, true),
            child: Text(deleted.isEmpty ? 'Reset' : 'Delete and reset'),
          ),
        ],
      );
    },
  );
  if (confirmed == true) await daemon.reset();
}
