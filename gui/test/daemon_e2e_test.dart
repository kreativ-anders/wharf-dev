@Tags(['e2e'])
library;

import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:wharf_gui/daemon.dart';
import 'package:wharf_gui/ipc/client.dart';
import 'package:wharf_gui/ipc/endpoint.dart';

import 'e2e_daemon.dart';

/// Drives the real wharfd binary from the GUI's own client, which is the only
/// way to prove the two halves actually fit: the Go snapshot shape, the
/// endpoint file, the transport handshake and the Dart models, end to end.
///
/// Run with: flutter test --tags e2e   (after `make build` at the repo root)
void main() {
  late Directory root;
  late Process daemon;

  setUpAll(() async {
    final binary = e2eDaemonBinary();

    root = await Directory.systemTemp.createTemp('wharf-e2e');
    await Directory('${root.path}/www/my-kirby-site').create(recursive: true);

    daemon = await Process.start(binary, [
      '--root', root.path,
      // WARNING: No password prompt.
      '--elevator', 'direct',
      // WARNING: The user's shell startup files are never changed either.
      '--terminal=false',
    ]);
    // WARNING: Keep the daemon's stderr drained; a full pipe would block it.
    daemon.stderr.drain<void>();
  });

  tearDownAll(() async {
    daemon.kill();
    await daemon.exitCode;
    await root.delete(recursive: true);
  });

  test('the GUI connects through the endpoint file the daemon publishes', () async {
    final endpointPath = endpointPathFor(root.path);
    await _waitForFile(endpointPath);

    final endpoint = await Endpoint.read(endpointPath);
    expect(endpoint.transport, Platform.isWindows ? 'tcp' : 'unix');
    if (endpoint.transport == 'tcp') {
      expect(endpoint.token, isNotEmpty, reason: 'loopback needs a token to authorise');
    }

    final client = await IpcClient.connect(endpoint);
    addTearDown(client.close);

    final pong = await client.call('ping');
    expect(pong['pong'], 'wharf');
  });

  // features/php-runtime.feature — "Re-scanning after installing a PHP version"
  test('a first run picks a PHP default and reports what it found', () async {
    await _waitForFile(endpointPathFor(root.path));
    final gui = Daemon(root: root.path);
    addTearDown(gui.dispose);
    await gui.start();

    expect(gui.connection, Connection.connected);
    expect(gui.state.root, root.path);

    final php = gui.state.services.php;
    expect(php.recommended, isNotEmpty);
    expect(php.version, isNotEmpty, reason: 'a default must be chosen even with nothing installed');
    // INFO: Whatever this machine has, the picker and the selection agree about it.
    for (final install in php.installs) {
      expect(install.version, isNotEmpty);
      expect(install.status, isNotEmpty);
    }
  });

  // features/pretty-urls.feature — "Adding a project never asks for a password"
  test('adding a folder from the GUI registers it under .localhost', () async {
    await _waitForFile(endpointPathFor(root.path));
    final gui = Daemon(root: root.path);
    addTearDown(gui.dispose);
    await gui.start();

    expect(gui.state.unregistered, contains('my-kirby-site'));

    await gui.addProject('my-kirby-site');

    final project = gui.state.projects.firstWhere((p) => p.name == 'my-kirby-site');
    expect(project.url, 'http://my-kirby-site.localhost');
    expect(gui.state.unregistered, isNot(contains('my-kirby-site')));

    // INFO: The config the daemon wrote stays readable by hand.
    final config = await File('${root.path}/config/wharf.json').readAsString();
    expect(config, contains('"my-kirby-site"'));
    expect(config, isNot(contains('null')));
  });

  // features/project-folders.feature — "Adding a project with the folder picker"
  test('a folder from anywhere is proposed, then added as the sheet confirmed it', () async {
    await _waitForFile(endpointPathFor(root.path));
    final gui = Daemon(root: root.path);
    addTearDown(gui.dispose);
    await gui.start();

    final code = await Directory.systemTemp.createTemp('wharf-e2e-code');
    addTearDown(() => code.delete(recursive: true));
    final shop = await Directory('${code.path}/Client Shop').create();
    await File('${shop.path}/artisan').writeAsString('#!/usr/bin/env php');

    // INFO: The Go proposal arrives in the Dart model field by field.
    final proposal = await gui.inspectFolder(shop.path);
    expect(proposal, isNotNull, reason: gui.notice);
    expect(proposal!.name, 'client-shop');
    expect(proposal.template, 'laravel');
    expect(proposal.fixed, isFalse);
    expect(proposal.project, isEmpty);

    // WARNING: Not started: a start would run this machine's real webserver.
    final added = await gui.addFolderAs(proposal.path, name: 'shop', template: '', start: false);
    expect(added.name, 'shop');
    final project = gui.state.projects.firstWhere((p) => p.name == 'shop');
    expect(project.linked, isTrue);
    expect(project.template, isEmpty);

    // INFO: Asked again, the folder names the project it now is.
    expect((await gui.inspectFolder(shop.path))!.project, 'shop');
    await expectLater(
      gui.addFolderAs(shop.path, name: 'again', template: '', start: false),
      throwsA(isA<DaemonError>()),
    );
  });

  // features/tray-actions.feature — "Opening the main window"
  test('a change made elsewhere reaches the GUI without it asking', () async {
    await _waitForFile(endpointPathFor(root.path));
    final gui = Daemon(root: root.path);
    addTearDown(gui.dispose);
    await gui.start();

    final other = Daemon(root: root.path);
    addTearDown(other.dispose);
    await other.start();

    final before = gui.state.services.webserver.active;
    final next = before == 'nginx' ? 'apache' : 'nginx';

    await other.setWebserver(next);

    await _waitFor(
      () => gui.state.services.webserver.active == next,
      reason: 'the pushed snapshot never arrived',
    );
  });

  test('a daemon error arrives as a code the GUI can branch on', () async {
    await _waitForFile(endpointPathFor(root.path));
    final endpoint = await Endpoint.read(endpointPathFor(root.path));
    final client = await IpcClient.connect(endpoint);
    addTearDown(client.close);

    await expectLater(
      client.call('projects.add', {'name': 'does-not-exist'}),
      throwsA(isA<DaemonError>()),
    );
  });
}

Future<void> _waitForFile(String path) =>
    _waitFor(() => File(path).existsSync(), reason: 'the daemon never published $path');

Future<void> _waitFor(bool Function() condition, {required String reason}) async {
  final deadline = DateTime.now().add(const Duration(seconds: 10));
  while (DateTime.now().isBefore(deadline)) {
    if (condition()) return;
    await Future<void>.delayed(const Duration(milliseconds: 25));
  }
  fail(reason);
}
