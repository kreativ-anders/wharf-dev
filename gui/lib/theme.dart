import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';

/// Kirby-plain: black on white, greys for structure, one blue for focus and
/// links, and colour only where it carries meaning — the status of a project
/// (dev/design-principles.md §4). The greys and hues are getkirby.com's
/// (`--color-gray-*`, `--color-green-h: 80`, …), with lightness chosen so
/// every text colour reaches WCAG AA (4.5:1) and every status mark 3:1 on its
/// background, in both themes.
@immutable
class WharfColors extends ThemeExtension<WharfColors> {
  const WharfColors({
    required this.dimmed,
    required this.border,
    required this.running,
    required this.busy,
    required this.failed,
    required this.idle,
    required this.focus,
    required this.castOff,
  });

  /// Secondary text.
  final Color dimmed;
  final Color border;

  /// Status marks; [failed] doubles as the error text colour.
  final Color running;
  final Color busy;
  final Color failed;
  final Color idle;

  /// Keyboard focus and links.
  final Color focus;

  /// "Cast off" — stop everything and quit: harbour teal, a hue no status
  /// uses, since leaving is not a state a project can be in. It fills its
  /// button, so the label is the page colour.
  final Color castOff;

  /// Project actions: Start green, Stop red, Restart blue — the status hues,
  /// so Start looks like what it leads to. Each action also has its own icon
  /// and label (features/tray-actions.feature, "Project actions keep their
  /// places in the list").
  Color get start => running;
  Color get stop => failed;
  Color get restart => focus;

  /// How much of its colour an action's button is tinted with.
  static const actionTint = 0.12;

  static const light = WharfColors(
    dimmed: Color(0xFF666666), // 5.7:1 on white
    border: Color(0xFFE0E0E0),
    running: Color(0xFF5C7A1F), // 4.9:1
    busy: Color(0xFFB86114), // 4.4:1
    failed: Color(0xFFDC1818), // 5.0:1
    idle: Color(0xFF858585), // 3.7:1
    focus: Color(0xFF266EB5), // 5.3:1
    castOff: Color(0xFF0F7C80), // white on it 5.0:1
  );

  static const dark = WharfColors(
    dimmed: Color(0xFFB2B2B2), // 8.0:1 on #1C1C1C
    border: Color(0xFF4C4C4C),
    running: Color(0xFFA3D147),
    busy: Color(0xFFEC9951),
    failed: Color(0xFFEE6363),
    idle: Color(0xFF999999),
    focus: Color(0xFF8DBAE7),
    castOff: Color(0xFF5FC4C8), // #1C1C1C on it 8.3:1
  );

  static WharfColors of(BuildContext context) =>
      Theme.of(context).extension<WharfColors>() ?? light;

  @override
  WharfColors copyWith() => this;

  @override
  WharfColors lerp(WharfColors? other, double t) {
    if (other == null) return this;
    Color l(Color a, Color b) => Color.lerp(a, b, t)!;
    return WharfColors(
      dimmed: l(dimmed, other.dimmed),
      border: l(border, other.border),
      running: l(running, other.running),
      busy: l(busy, other.busy),
      failed: l(failed, other.failed),
      idle: l(idle, other.idle),
      focus: l(focus, other.focus),
      castOff: l(castOff, other.castOff),
    );
  }
}

ThemeData wharfTheme(Brightness brightness) {
  final dark = brightness == Brightness.dark;
  final colors = dark ? WharfColors.dark : WharfColors.light;
  final ink = dark ? Colors.white : Colors.black;
  final paper = dark ? const Color(0xFF1C1C1C) : Colors.white;
  final surface = dark ? const Color(0xFF262626) : const Color(0xFFF5F5F5);

  final scheme = ColorScheme(
    brightness: brightness,
    primary: ink,
    onPrimary: paper,
    secondary: ink,
    onSecondary: paper,
    error: colors.failed,
    onError: paper,
    surface: paper,
    onSurface: ink,
    onSurfaceVariant: colors.dimmed,
    surfaceContainerLowest: paper,
    surfaceContainerLow: surface,
    surfaceContainer: surface,
    surfaceContainerHigh: surface,
    surfaceContainerHighest: surface,
    outline: colors.border,
    outlineVariant: colors.border,
    errorContainer: dark ? const Color(0xFF3D1C1C) : const Color(0xFFFCE8E8),
    onErrorContainer: ink,
    secondaryContainer: surface,
    onSecondaryContainer: ink,
  );

  // The platform's own system font, as getkirby.com uses: San Francisco on
  // macOS, Segoe UI on Windows, the desktop's default on Linux.
  final typography = Typography.material2021(platform: defaultTargetPlatform);
  final base = (dark ? typography.white : typography.black).apply(
    bodyColor: ink,
    displayColor: ink,
  );
  // Every style derives from the platform's, so none loses its font family.
  final text = base.copyWith(
    bodySmall: base.bodySmall!.copyWith(color: colors.dimmed, fontSize: 12.5, height: 1.4),
    titleMedium: base.titleMedium!.copyWith(fontSize: 15, fontWeight: FontWeight.w600),
    titleSmall: base.titleSmall!.copyWith(fontSize: 13, fontWeight: FontWeight.w600),
  );

  return ThemeData(
    useMaterial3: true,
    brightness: brightness,
    colorScheme: scheme,
    scaffoldBackgroundColor: paper,
    canvasColor: paper,
    dividerColor: colors.border,
    focusColor: colors.focus.withValues(alpha: 0.24),
    extensions: [colors],
    dividerTheme: DividerThemeData(color: colors.border, space: 1, thickness: 1),
    textTheme: text,
    appBarTheme: AppBarTheme(
      backgroundColor: paper,
      foregroundColor: ink,
      surfaceTintColor: Colors.transparent,
      elevation: 0,
      scrolledUnderElevation: 0,
      centerTitle: false,
      titleTextStyle: text.titleMedium!.copyWith(fontSize: 16),
      iconTheme: IconThemeData(color: ink),
      actionsIconTheme: IconThemeData(color: ink),
      shape: Border(bottom: BorderSide(color: colors.border)),
    ),
    listTileTheme: const ListTileThemeData(
      contentPadding: EdgeInsets.symmetric(horizontal: 24, vertical: 4),
    ),
    dialogTheme: DialogThemeData(
      backgroundColor: paper,
      surfaceTintColor: Colors.transparent,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(8),
        side: BorderSide(color: colors.border),
      ),
    ),
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(
        backgroundColor: ink,
        foregroundColor: paper,
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(4)),
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(
        foregroundColor: ink,
        side: BorderSide(color: colors.border),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(4)),
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
      ),
    ),
    textButtonTheme: TextButtonThemeData(
      style: TextButton.styleFrom(
        foregroundColor: ink,
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(4)),
      ),
    ),
    floatingActionButtonTheme: FloatingActionButtonThemeData(
      backgroundColor: ink,
      foregroundColor: paper,
      elevation: 0,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(4)),
    ),
    // A control's outline needs 3:1 against the page (WCAG 1.4.11); the
    // hairline border grey would be 1.3:1.
    switchTheme: SwitchThemeData(
      trackOutlineColor: WidgetStatePropertyAll(colors.idle),
      thumbColor: WidgetStateProperty.resolveWith(
        (s) => s.contains(WidgetState.selected) ? paper : colors.idle,
      ),
    ),
    segmentedButtonTheme: SegmentedButtonThemeData(
      style: SegmentedButton.styleFrom(
        selectedBackgroundColor: ink,
        selectedForegroundColor: paper,
        side: BorderSide(color: colors.border),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(4)),
      ),
    ),
    tooltipTheme: TooltipThemeData(waitDuration: const Duration(milliseconds: 400)),
    snackBarTheme: const SnackBarThemeData(behavior: SnackBarBehavior.floating),
  );
}

/// How the window follows the `appearance` setting in the snapshot.
ThemeMode themeModeFor(String appearance) => switch (appearance) {
  'light' => ThemeMode.light,
  'dark' => ThemeMode.dark,
  _ => ThemeMode.system,
};

/// The words for a status, for screen readers and tooltips. Colour alone
/// never carries it (WCAG 1.4.1).
String statusLabel(String state) => switch (state) {
  'running' => 'Running',
  'starting' => 'Starting',
  'stopping' => 'Stopping',
  'failed' => 'Failed',
  _ => 'Stopped',
};

Color statusColor(BuildContext context, String state) {
  final c = WharfColors.of(context);
  return switch (state) {
    'running' => c.running,
    'starting' || 'stopping' => c.busy,
    'failed' => c.failed,
    _ => c.idle,
  };
}

/// The whole status indicator, and readable without colour: running is a
/// filled dot, stopped a ring, starting or stopping a half-filled ring,
/// failed a filled square.
class StatusDot extends StatelessWidget {
  const StatusDot(this.state, {super.key, this.size = 10});
  final String state;
  final double size;

  @override
  Widget build(BuildContext context) {
    final color = statusColor(context, state);
    final shape = switch (state) {
      'running' => BoxDecoration(color: color, shape: BoxShape.circle),
      'failed' => BoxDecoration(color: color, borderRadius: BorderRadius.circular(1.5)),
      'starting' || 'stopping' => BoxDecoration(
        shape: BoxShape.circle,
        border: Border.all(color: color, width: 1.5),
        gradient: LinearGradient(
          colors: [color, color, Colors.transparent, Colors.transparent],
          stops: const [0, 0.5, 0.5, 1],
        ),
      ),
      _ => BoxDecoration(
        shape: BoxShape.circle,
        border: Border.all(color: color, width: 1.5),
      ),
    };
    return Tooltip(
      message: statusLabel(state),
      excludeFromSemantics: true,
      child: Semantics(
        label: statusLabel(state),
        child: Container(width: size, height: size, decoration: shape),
      ),
    );
  }
}

/// The window's one sign that something is under way: a hairline under the
/// title bar that moves, announced as "Working…" — motion and words, not
/// colour. It always takes its one pixel, so the page never jumps.
class WorkingBar extends StatelessWidget implements PreferredSizeWidget {
  const WorkingBar({super.key, required this.working});
  final bool working;

  @override
  Size get preferredSize => const Size.fromHeight(1);

  @override
  Widget build(BuildContext context) => working
      ? const LinearProgressIndicator(minHeight: 1, semanticsLabel: 'Working…')
      : const SizedBox(height: 1);
}
