import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:wharf_gui/models/state.dart';

/// The snapshot shape is a contract with daemon/internal/core/state.go. These
/// tests pin down the parts the GUI branches on, so a rename on the Go side
/// fails here rather than showing an empty list at runtime.
void main() {
  test('parses a full snapshot', () {
    final state = WharfState.fromJson(jsonDecode(_snapshot) as Map<String, dynamic>);

    expect(state.root, '/Users/x/Wharf');
    expect(state.version, 'v0.1.0');
    expect(state.domain, 'localhost');
    expect(state.projects, hasLength(2));
    expect(state.unregistered, ['dropped-in']);
    expect(state.services.webserver.active, 'nginx');
    expect(state.services.php.version, '8.4');
    expect(state.ssl.installed, isTrue);
    expect(state.ssl.trusted, isFalse);
  });

  test('every project is published under .localhost, with no port', () {
    final state = WharfState.fromJson(jsonDecode(_snapshot) as Map<String, dynamic>);
    // legacy-app is on its own Apache instance, behind the front door.
    final legacy = state.projects.firstWhere((p) => p.name == 'legacy-app');

    expect(legacy.url, 'http://legacy-app.localhost');
  });

  test('a project says what serves it, down to the patch version', () {
    final state = WharfState.fromJson(jsonDecode(_snapshot) as Map<String, dynamic>);
    final kirby = state.projects.firstWhere((p) => p.name == 'my-kirby-site');
    final legacy = state.projects.firstWhere((p) => p.name == 'legacy-app');

    expect(kirby.webserverVersion, '1.27.3');
    expect(kirby.phpFullVersion, '8.4.3');
    expect(kirby.servedBy, 'nginx 1.27.3 · PHP 8.4.3');
    // A binary that did not report its version falls back to the configured one.
    expect(legacy.servedBy, 'apache · PHP 8.4');
  });

  test('overrides are distinguishable from the resolved value', () {
    final state = WharfState.fromJson(jsonDecode(_snapshot) as Map<String, dynamic>);
    final plain = state.projects.firstWhere((p) => p.name == 'my-kirby-site');
    final overridden = state.projects.firstWhere((p) => p.name == 'legacy-app');

    expect(plain.webserverOverride, isNull);
    expect(plain.hasOverrides, isFalse);
    expect(overridden.webserverOverride, 'apache');
    expect(overridden.webserver, 'apache');
    expect(overridden.hasOverrides, isTrue);
  });

  test('a switching webserver is neither running nor stopped', () {
    final w = Webserver.fromJson({
      'active': 'apache',
      'available': ['apache', 'nginx'],
      'state': 'starting',
      'switching': true,
    });
    expect(w.switching, isTrue);
    expect(w.isRunning, isFalse);
  });

  test('a cli-only PHP install cannot be selected', () {
    final php = Php.fromJson({
      'version': '8.4',
      'available': ['8.4'],
      'installs': [
        {
          'version': '8.4',
          'full_version': '8.4.3',
          'dir': '/opt/php',
          'source': 'system',
          'status': 'active',
        },
      ],
      'recommended': '8.4',
      'status': 'active',
    });
    expect(php.installs.single.servable, isFalse);
    expect(php.selectedIsInstalled, isFalse);
  });

  test('an empty snapshot parses, so a disconnected GUI still renders', () {
    final state = WharfState.fromJson(const {});
    expect(state.projects, isEmpty);
    expect(state.version, isEmpty);
    expect(state.services.webserver.active, isEmpty);
    expect(state.anyRunning, isFalse);
  });
}

const _snapshot = '''
{
  "root": "/Users/x/Wharf",
  "version": "v0.1.0",
  "domain": "localhost",
  "services": {
    "webserver": {"active": "nginx", "available": ["apache", "nginx"], "state": "running", "switching": false},
    "php": {
      "version": "8.4",
      "available": ["8.3", "8.4"],
      "backends": [],
      "installs": [
        {"version": "8.4", "full_version": "8.4.3", "dir": "/opt/homebrew/opt/php@8.4/bin",
         "cli": "/opt/homebrew/opt/php@8.4/bin/php", "fastcgi": "/opt/homebrew/opt/php@8.4/sbin/php-fpm",
         "source": "system", "status": "active"}
      ],
      "recommended": "8.5",
      "status": "active"
    }
  },
  "projects": [
    {"name": "my-kirby-site", "state": "running", "url": "http://my-kirby-site.localhost",
     "webserver": "nginx", "php_version": "8.4", "ssl": false, "port": 8080,
     "webserver_version": "1.27.3", "php_full_version": "8.4.3"},
    {"name": "legacy-app", "state": "stopped", "url": "http://legacy-app.localhost",
     "webserver": "apache", "webserver_override": "apache", "php_version": "8.4",
     "ssl": false, "port": 8081}
  ],
  "unregistered": ["dropped-in"],
  "ssl": {"installed": true, "trusted": false, "ca_root": "/Users/x/ca"},
  "busy": false
}
''';
