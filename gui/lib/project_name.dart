// The daemon's project.Slug (daemon/internal/project/registry.go), mirrored so
// the "New project" field can rewrite itself without a round trip. The daemon
// applies the same rewrite again and has the last word; both are tested
// against daemon/internal/project/testdata/slug.json
// (features/quick-app-php.feature, "A typed name becomes a project name").

// Letters with a conventional spelled-out form or no base letter to fall back
// to. The umlauts are listed decomposed as well, as macOS stores them.
const _spelled = <String, String>{
  'ä': 'ae',
  'ö': 'oe',
  'ü': 'ue',
  'a\u0308': 'ae',
  'o\u0308': 'oe',
  'u\u0308': 'ue',
  'ß': 'ss',
  'æ': 'ae',
  'œ': 'oe',
  'ø': 'o',
  'ł': 'l',
  'đ': 'd',
  'ð': 'd',
  'þ': 'th',
  'ı': 'i',
};

final Map<int, int> _accented = {
  for (final e in const {
    'a': 'àáâãåāăą',
    'c': 'çćĉċč',
    'd': 'ď',
    'e': 'èéêëēĕėęě',
    'g': 'ĝğġģ',
    'h': 'ĥħ',
    'i': 'ìíîïĩīĭį',
    'j': 'ĵ',
    'k': 'ķ',
    'l': 'ĺļľŀ',
    'n': 'ñńņň',
    'o': 'òóôõōŏő',
    'r': 'ŕŗř',
    's': 'śŝşšș',
    't': 'ţťŧț',
    'u': 'ùúûũūŭůűų',
    'w': 'ŵ',
    'y': 'ýÿŷ',
    'z': 'źżž',
  }.entries)
    for (final r in e.value.runes) r: e.key.codeUnitAt(0),
};

final _disallowed = RegExp(r'[^a-z0-9]+');
final _edgeHyphens = RegExp(r'^-+|-+$');

/// Turns what the user typed into a project name: "Müller & Söhne" becomes
/// "mueller-soehne". Returns "" when nothing usable is left.
String projectName(String typed) {
  var s = typed.toLowerCase();
  _spelled.forEach((from, to) => s = s.replaceAll(from, to));
  final out = StringBuffer();
  for (final r in s.runes) {
    final base = _accented[r];
    if (base != null) {
      out.writeCharCode(base);
    } else if (r < 0x300 || r > 0x36f) {
      // Anything else in that range is a combining accent, as in a
      // decomposed "é"; dropping it leaves the base letter.
      out.writeCharCode(r);
    }
  }
  s = out.toString().replaceAll(_disallowed, '-').replaceAll(_edgeHyphens, '');
  // A hostname label ends at 63 characters, and never on a hyphen.
  if (s.length > 63) s = s.substring(0, 63).replaceAll(_edgeHyphens, '');
  return s;
}
