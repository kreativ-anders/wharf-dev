import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:qr_flutter/qr_flutter.dart';

import '../daemon.dart';
import '../folders.dart';
import '../models/state.dart';
import '../theme.dart';

/// "Share": shares a running project on the local network, then shows how to
/// open it on a phone — its URL as a QR code and as text. A project with SSL
/// adds a notice that the phone needs Wharf's network certificate once, which
/// expands to offer it (features/sharing.feature).
Future<void> shareProject(BuildContext context, Daemon daemon, Project project) async {
  if (!project.shared) {
    await daemon.shareProject(project.name);
  }
  if (!context.mounted) return;
  // INFO: Not shared after all — not running, no network: the notice says why.
  if (!daemon.state.projects.any((p) => p.name == project.name && p.shared)) return;
  await showDialog<void>(
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
            constraints: const BoxConstraints(maxWidth: 420),
            child: ShareSheet(daemon: daemon, project: current, network: daemon.state.network),
          ),
        );
      },
    ),
  );
}

/// What the Share dialog shows. It follows the snapshot: a new address on
/// the network redraws the QR code, and a project that stopped says so.
class ShareSheet extends StatelessWidget {
  const ShareSheet({super.key, required this.daemon, required this.project, required this.network});

  final Daemon daemon;
  final Project project;
  final Network network;

  @override
  Widget build(BuildContext context) {
    final muted = Theme.of(context).textTheme.bodySmall;
    final certificate = network.certificate;
    // INFO: The certificate only where the phone needs it — a project shared
    // over HTTPS — and folded away behind a notice: a phone needs it once, so
    // the project's own QR code comes first every time (features/sharing.feature,
    // "The certificate step is shown only for a project with SSL").
    final withCertificate = project.shared && project.ssl && certificate != null;
    final port = Uri.tryParse(project.shareUrl)?.port;

    // WARNING: The button stays outside the scroll view: inside it, a window
    // shorter than the QR codes scrolls "Done" out of sight.
    return SafeArea(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Flexible(
            child: SingleChildScrollView(
              padding: const EdgeInsets.fromLTRB(24, 16, 16, 0),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Expanded(
                        child: Semantics(
                          header: true,
                          child: Text('Share ${project.name}', style: Theme.of(context).textTheme.titleLarge),
                        ),
                      ),
                      IconButton(
                        tooltip: 'Close',
                        icon: const Icon(Icons.close, size: 18),
                        onPressed: () => _close(context),
                      ),
                    ],
                  ),
                  const SizedBox(height: 4),
                  if (!project.shared)
                    // INFO: Stopped meanwhile — sharing ends with the project.
                    Padding(
                      padding: const EdgeInsets.only(top: 12, bottom: 12),
                      child: Text('${project.name} is no longer shared.'),
                    )
                  else ...[
                    Text(
                      'Scan with a phone on the same network. Only this project is shared, '
                      'until you stop it or quit Wharf.',
                      style: muted,
                    ),
                    const SizedBox(height: 20),
                    _Step(title: 'Open the project', url: project.shareUrl),
                    if (withCertificate) ...[
                      const SizedBox(height: 16),
                      _CertificateNotice(daemon: daemon, network: network, certificate: certificate),
                    ],
                    const SizedBox(height: 20),
                    Text(
                      'Nothing loads? Phone and computer must be on the same network — a guest '
                      'network keeps devices apart. On Linux, a firewall may need '
                      'port ${port ?? ''} opened.',
                      style: muted,
                    ),
                  ],
                ],
              ),
            ),
          ),
          Padding(
            padding: const EdgeInsets.fromLTRB(24, 20, 16, 24),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.end,
              // INFO: Done alone: sharing ends with the project, so the dialog
              // offers nothing that would leave it running unshared
              // (features/sharing.feature, "Sharing ends with the project").
              children: [
                FilledButton(onPressed: () => _close(context), child: const Text('Done')),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

/// The notice under a project shared over HTTPS: the phone needs Wharf's
/// network certificate once. Folded, it says so; expanded, it offers the
/// certificate to scan.
class _CertificateNotice extends StatelessWidget {
  const _CertificateNotice({required this.daemon, required this.network, required this.certificate});

  final Daemon daemon;
  final Network network;
  final NetworkCertificate certificate;

  @override
  Widget build(BuildContext context) {
    final colors = WharfColors.of(context);
    final shape = RoundedRectangleBorder(
      side: BorderSide(color: colors.busy),
      borderRadius: BorderRadius.circular(4),
    );
    // INFO: Icon and wording carry the warning; the colour only repeats it
    // (dev/design-principles.md §4).
    return Material(
      color: colors.busy.withValues(alpha: WharfColors.actionTint),
      shape: shape,
      clipBehavior: Clip.antiAlias,
      child: ExpansionTile(
        shape: shape,
        collapsedShape: shape,
        tilePadding: const EdgeInsets.symmetric(horizontal: 12),
        childrenPadding: const EdgeInsets.fromLTRB(12, 0, 12, 12),
        expandedCrossAxisAlignment: CrossAxisAlignment.start,
        leading: Icon(Icons.warning_amber_rounded, color: colors.busy, semanticLabel: 'Note'),
        title: const Text('HTTPS needs the network certificate on the phone'),
        subtitle: Text(
          'Once per phone. Already installed? Nothing to do.',
          style: Theme.of(context).textTheme.bodySmall,
        ),
        children: [
          _Step(
            title: 'Install the network certificate',
            url: network.certificateUrl,
            children: [_CertificateDetails(daemon: daemon, certificate: certificate)],
          ),
        ],
      ),
    );
  }
}

/// One thing to scan: a heading, the QR code, and the URL as text to copy.
class _Step extends StatelessWidget {
  const _Step({required this.title, required this.url, this.children = const []});

  final String title;
  final String url;
  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: 260,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Semantics(header: true, child: Text(title, style: Theme.of(context).textTheme.titleSmall)),
          const SizedBox(height: 10),
          ShareQr(url: url),
          const SizedBox(height: 8),
          Row(
            children: [
              Expanded(
                child: SelectableText(url, style: const TextStyle(fontFamily: 'monospace')),
              ),
              IconButton(
                tooltip: 'Copy $url',
                icon: const Icon(Icons.copy, size: 16),
                onPressed: () => Clipboard.setData(ClipboardData(text: url)),
              ),
            ],
          ),
          ...children,
        ],
      ),
    );
  }
}

/// A URL as a QR code. Black on white in both themes: a phone's camera reads
/// dark modules on a light ground most reliably. A screen reader hears what
/// it encodes, which the text below repeats.
class ShareQr extends StatelessWidget {
  const ShareQr({super.key, required this.url});

  final String url;

  @override
  Widget build(BuildContext context) {
    return Semantics(
      image: true,
      label: 'QR code for $url',
      child: ExcludeSemantics(
        child: Container(
          key: ValueKey('qr:$url'),
          color: Colors.white,
          padding: const EdgeInsets.all(8),
          child: QrImageView(data: url, size: 180, padding: EdgeInsets.zero, backgroundColor: Colors.white),
        ),
      ),
    );
  }
}

/// The network certificate: its fingerprint to compare on the phone, how to
/// install it, and "Replace…" (features/sharing.feature).
class _CertificateDetails extends StatelessWidget {
  const _CertificateDetails({required this.daemon, required this.certificate});

  final Daemon daemon;
  final NetworkCertificate certificate;

  @override
  Widget build(BuildContext context) {
    final muted = Theme.of(context).textTheme.bodySmall;
    final pairs = certificate.fingerprint.split(' ');
    final half = (pairs.length / 2).ceil();
    final expires = certificate.expires;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const SizedBox(height: 4),
        // INFO: The download is plain HTTP — the phone cannot check HTTPS
        // before it has the certificate — so the fingerprint is how the user
        // knows the file on the phone is this one.
        Text('SHA-256 fingerprint — the phone shows it before installing:', style: muted),
        const SizedBox(height: 4),
        SelectableText(
          '${pairs.take(half).join(' ')}\n${pairs.skip(half).join(' ')}',
          style: const TextStyle(fontFamily: 'monospace', fontSize: 11),
        ),
        if (expires != null) ...[
          const SizedBox(height: 4),
          Text('Valid until ${_date(expires)}. Only for addresses on a local network.', style: muted),
        ],
        const SizedBox(height: 10),
        // INFO: Kept short and the same for every phone: the phone itself
        // offers the downloaded certificate in its settings, and menu paths
        // change from one OS version to the next. The one step it does not
        // offer is iOS's trust switch, so that is named.
        Text(
          'Open the link on the phone and install the certificate it offers in its '
          'settings. On an iPhone or iPad, then turn on full trust for it under '
          'Settings → General → About → Certificate Trust Settings.',
          style: muted,
        ),
        const SizedBox(height: 6),
        Wrap(
          spacing: 4,
          children: [
            TextButton(onPressed: () => daemon.open(openFolder, certificate.file), child: const Text('Show file')),
            TextButton(onPressed: () => _replace(context), child: const Text('Replace…')),
          ],
        ),
      ],
    );
  }

  /// Asks first: every phone that installed the certificate must install
  /// the new one.
  Future<void> _replace(BuildContext context) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Replace the network certificate?'),
        content: const Text(
          'A new one is made and the old one is deleted with its key. Every phone that '
          'installed the old one must install the new one — and can then remove the old one.',
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('Cancel')),
          FilledButton(onPressed: () => Navigator.pop(context, true), child: const Text('Replace')),
        ],
      ),
    );
    if (confirmed == true) await daemon.replaceNetworkCertificate();
  }
}

String _date(DateTime d) {
  final local = d.toLocal();
  String two(int n) => n.toString().padLeft(2, '0');
  return '${local.year}-${two(local.month)}-${two(local.day)}';
}

/// Drops focus before the dialog closes, as the project sheet does: Windows
/// logs an AXTree error for a focused node that vanished mid-frame.
void _close(BuildContext context) {
  FocusManager.instance.primaryFocus?.unfocus();
  Navigator.pop(context);
}
