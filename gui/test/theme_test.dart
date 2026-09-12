import 'dart:math';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:wharf_gui/theme.dart';

/// WCAG 2.x relative luminance and contrast ratio.
double _luminance(Color c) {
  double ch(double v) => v <= 0.03928 ? v / 12.92 : pow((v + 0.055) / 1.055, 2.4).toDouble();
  return 0.2126 * ch(c.r) + 0.7152 * ch(c.g) + 0.0722 * ch(c.b);
}

double contrast(Color a, Color b) {
  final la = _luminance(a), lb = _luminance(b);
  return (max(la, lb) + 0.05) / (min(la, lb) + 0.05);
}

/// Guards the palette against a well-meant tweak that breaks legibility:
/// text needs 4.5:1 (WCAG AA), status marks and other meaningful graphics 3:1.
void main() {
  for (final brightness in Brightness.values) {
    final theme = wharfTheme(brightness);
    final c = theme.extension<WharfColors>()!;
    final backgrounds = {
      'page': theme.scaffoldBackgroundColor,
      'surface': theme.colorScheme.surfaceContainerHighest,
    };

    group(brightness.name, () {
      for (final bg in backgrounds.entries) {
        test('text is readable on the ${bg.key}', () {
          for (final (name, color) in [
            ('body', theme.colorScheme.onSurface),
            ('dimmed', c.dimmed),
            ('error', c.failed),
            ('focus/link', c.focus),
          ]) {
            expect(contrast(color, bg.value), greaterThanOrEqualTo(4.5), reason: name);
          }
        });

        test('status marks stand out on the ${bg.key}', () {
          for (final (name, color) in [
            ('running', c.running),
            ('busy', c.busy),
            ('failed', c.failed),
            ('idle', c.idle),
          ]) {
            expect(contrast(color, bg.value), greaterThanOrEqualTo(3), reason: name);
          }
        });

        // The icon sits on its own tint, over the page or a hovered row.
        test('project actions stand out on their tint over the ${bg.key}', () {
          for (final (name, color) in [
            ('start', c.start),
            ('stop', c.stop),
            ('restart', c.restart),
          ]) {
            final button = Color.alphaBlend(
              color.withValues(alpha: WharfColors.actionTint),
              bg.value,
            );
            expect(contrast(color, button), greaterThanOrEqualTo(3), reason: name);
          }
        });
      }

      test('filled buttons are readable', () {
        expect(
          contrast(theme.colorScheme.onPrimary, theme.colorScheme.primary),
          greaterThanOrEqualTo(4.5),
        );
      });
    });
  }
}
