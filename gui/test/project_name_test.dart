import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:wharf_gui/project_name.dart';

void main() {
  // features/quick-app-php.feature — "A typed name becomes a project name"
  test('typed names are rewritten exactly as the daemon rewrites them', () {
    // INFO: The daemon is tested against the same table, so the two cannot drift.
    final raw = File('../daemon/internal/project/testdata/slug.json')
        .readAsStringSync();
    for (final c in (jsonDecode(raw) as List).cast<List<dynamic>>()) {
      expect(
        projectName(c[0] as String),
        c[1],
        reason: 'typed ${jsonEncode(c[0])}',
      );
    }
    // INFO: A hostname label ends at 63 characters, and never on a hyphen.
    expect(projectName('${'a' * 62} b'), 'a' * 62);
  });
}
