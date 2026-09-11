import 'package:flutter/material.dart';

import '../daemon.dart';
import '../folders.dart';
import '../models/state.dart';

/// Global settings. Only Webserver, PHP runtime and SSL appear: database and
/// mail are roadmap capabilities and are not shown at all in v1
/// (features/settings.feature, "Settings screen hides roadmap services in v1").
class SettingsPage extends StatelessWidget {
  const SettingsPage({super.key, required this.daemon});

  final Daemon daemon;

  @override
  Widget build(BuildContext context) {
    return ListenableBuilder(
      listenable: daemon,
      builder: (context, _) {
        final state = daemon.state;
        final services = state.services;
        final muted = Theme.of(context).textTheme.bodySmall;
        return Scaffold(
          appBar: AppBar(title: const Text('Settings')),
          body: ListView(
            padding: const EdgeInsets.symmetric(vertical: 8),
            children: [
              if (daemon.notice != null)
                Padding(
                  padding: const EdgeInsets.fromLTRB(24, 0, 24, 8),
                  child: Text(daemon.notice!, style: muted),
                ),
              _SectionHeader('Webserver'),
              _WebserverSection(webserver: services.webserver, daemon: daemon),
              const SizedBox(height: 28),
              _SectionHeader('PHP runtime'),
              _PhpSection(daemon: daemon, php: services.php),
              const SizedBox(height: 28),
              _SectionHeader('SSL'),
              _SslSection(daemon: daemon, ssl: state.ssl),
              const SizedBox(height: 40),
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 24),
                child: Text(
                  'Everything here lives in ${state.config}/wharf.json and can be '
                  'edited by hand instead; the daemon picks changes up.',
                  style: muted,
                ),
              ),
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 12),
                child: Align(
                  alignment: Alignment.centerLeft,
                  child: TextButton.icon(
                    onPressed: state.config.isEmpty ? null : () => openFolder(state.config),
                    icon: const Icon(Icons.folder_open, size: 16),
                    label: const Text('Open config folder'),
                  ),
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}

/// The default webserver. "Stopped" on its own reads as broken when it only
/// means no project is running, so each server says what it serves and when
/// it runs (features/settings.feature, "Webserver status while nothing is
/// running").
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
            'The default for every project. Any project can use the other one '
            'instead, in its own settings.',
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
                RadioListTile<String>(
                  value: server.name,
                  enabled: !webserver.switching,
                  title: Text(server.name),
                  subtitle: Text(_describe(server)),
                  secondary: server.installed
                      ? null
                      : IconButton(
                          tooltip: 'Open the folder it belongs in',
                          icon: const Icon(Icons.folder_open, size: 18),
                          onPressed: () => openFolder(_parent(server.binary)),
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
              style: TextStyle(color: Theme.of(context).colorScheme.error, fontSize: 12),
            ),
          ),
      ],
    );
  }

  String _describe(Server server) {
    if (!server.installed) return 'Not installed — expected at ${server.binary}';
    final serves = server.projects.isEmpty ? null : 'serves ${server.projects.join(', ')}';
    if (server.name != webserver.active) {
      return serves == null ? 'Not used by any project' : 'Chosen per project — $serves';
    }
    if (webserver.switching) return 'switching webserver…';
    if (webserver.isRunning) return 'Running${serves == null ? '' : ' — $serves'}';
    return 'Starts with the first project${serves == null ? '' : ' — $serves'}';
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
                  'PHP ${php.version} is selected — the newest version in active support. '
                  'Download it below, or install PHP yourself and re-scan.',
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
                    title: Row(
                      children: [
                        Text(
                          'PHP ${install.fullVersion.isEmpty ? install.version : install.fullVersion}',
                        ),
                        const SizedBox(width: 10),
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
                    secondary: IconButton(
                      tooltip: 'Open folder',
                      icon: const Icon(Icons.folder_open, size: 18),
                      onPressed: () => openFolder(install.dir),
                    ),
                  ),
              ],
            ),
          ),
        if (php.downloadable.isNotEmpty) ...[
          const SizedBox(height: 8),
          Padding(
            padding: const EdgeInsets.fromLTRB(24, 8, 24, 0),
            child: Text('Download', style: Theme.of(context).textTheme.titleSmall),
          ),
          for (final download in php.downloadable)
            ListTile(
              title: Row(
                children: [
                  Text('PHP ${download.version}'),
                  const SizedBox(width: 10),
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
          padding: const EdgeInsets.fromLTRB(24, 12, 12, 0),
          child: Row(
            children: [
              Expanded(
                child: Text(
                  'PHP has no LTS track: ${php.recommended} is the newest version in '
                  'active support. Downloads go to ${php.dir}.',
                  style: muted,
                ),
              ),
              IconButton(
                tooltip: 'Open ${php.dir}',
                icon: const Icon(Icons.folder_open, size: 18),
                onPressed: php.dir.isEmpty ? null : () => openFolder(php.dir),
              ),
              TextButton(onPressed: daemon.rescanPhp, child: const Text('Re-scan')),
            ],
          ),
        ),
      ],
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
    final muted = Theme.of(context).textTheme.bodySmall;
    final busy = daemon.state.busy;

    final (String text, Widget? action) = switch (ssl) {
      SslStatus(installed: false) => (
        'mkcert, which makes the certificates, is installed the first time a '
            'project turns SSL on.',
        OutlinedButton(onPressed: busy ? null : daemon.setupSsl, child: const Text('Set up now')),
      ),
      SslStatus(trusted: false) => (
        'Browsers warn about Wharf\'s certificates until its certificate authority '
            'is trusted. This asks for your password once.',
        FilledButton(
          onPressed: busy ? null : daemon.setupSsl,
          child: const Text('Trust certificates'),
        ),
      ),
      _ => ('This machine trusts Wharf\'s certificates.', null),
    };

    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(text, style: muted),
          if (action != null) ...[const SizedBox(height: 12), action],
          if (ssl.trusted && ssl.caRoot.isNotEmpty)
            TextButton.icon(
              onPressed: () => openFolder(ssl.caRoot),
              icon: const Icon(Icons.folder_open, size: 16),
              label: const Text('Open certificate authority folder'),
            ),
        ],
      ),
    );
  }
}

/// How well supported a version still is — the reason to pick one over another.
class _SupportBadge extends StatelessWidget {
  const _SupportBadge(this.status);
  final String status;

  @override
  Widget build(BuildContext context) {
    final (label, color) = switch (status) {
      'active' => ('active support', const Color(0xFF3F9142)),
      'security' => ('security fixes only', const Color(0xFFB2841F)),
      'eol' => ('end of life', Theme.of(context).colorScheme.error),
      'unreleased' => ('not released yet', Colors.grey),
      _ => ('unrecognised version', Colors.grey),
    };
    return Text(label, style: TextStyle(fontSize: 11, color: color));
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader(this.title);
  final String title;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.fromLTRB(24, 16, 24, 8),
    child: Text(title, style: Theme.of(context).textTheme.titleMedium),
  );
}
