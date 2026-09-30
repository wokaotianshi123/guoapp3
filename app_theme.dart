import 'package:flutter/cupertino.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:lucide_icons_flutter/lucide_icons.dart';

import 'liquid_glass.dart';

abstract final class AppTheme {
  static final light = _theme(Brightness.light);
  static final dark = _theme(Brightness.dark);
  static final iosLight = _glass(Brightness.light, TargetPlatform.iOS);
  static final iosDark = _glass(Brightness.dark, TargetPlatform.iOS);
  static final _glassThemes = <(TargetPlatform, Brightness), ThemeData>{};

  static ThemeData get platformLight => _platformTheme(Brightness.light);
  static ThemeData get platformDark => _platformTheme(Brightness.dark);

  static ThemeData _platformTheme(Brightness brightness) {
    final platform = defaultTargetPlatform;
    if (platform == TargetPlatform.iOS) {
      return brightness == Brightness.dark ? iosDark : iosLight;
    }
    if (platform == TargetPlatform.android ||
        platform == TargetPlatform.windows) {
      return _glassThemes.putIfAbsent((
        platform,
        brightness,
      ), () => _glass(brightness, platform));
    }
    return brightness == Brightness.dark ? dark : light;
  }

  static ThemeMode mode(String preference) => switch (preference) {
    'light' => ThemeMode.light,
    'dark' => ThemeMode.dark,
    _ => ThemeMode.system,
  };

  static String label(String preference) => switch (preference) {
    'light' => '浅色',
    'dark' => '深色',
    _ => '跟随系统',
  };

  static SystemUiOverlayStyle systemBars(Brightness brightness) {
    final icons = brightness == Brightness.dark
        ? Brightness.light
        : Brightness.dark;
    return SystemUiOverlayStyle(
      statusBarColor: Colors.transparent,
      statusBarIconBrightness: icons,
      statusBarBrightness: brightness,
      systemStatusBarContrastEnforced: false,
      systemNavigationBarColor: Colors.transparent,
      systemNavigationBarDividerColor: Colors.transparent,
      systemNavigationBarIconBrightness: icons,
      systemNavigationBarContrastEnforced: false,
    );
  }

  static ThemeData _theme(Brightness brightness) {
    final dark = brightness == Brightness.dark;
    final background = dark ? const Color(0xFF101114) : const Color(0xFFFAF8F6);
    final scheme = ColorScheme.fromSeed(
      seedColor: const Color(0xFFFF664F),
      brightness: brightness,
      primary: dark ? const Color(0xFFFF765F) : const Color(0xFFAD3826),
      onPrimary: dark ? const Color(0xFF3B0E07) : Colors.white,
      primaryContainer: dark
          ? const Color(0xFF5C3027)
          : const Color(0xFFFFE0D8),
      onPrimaryContainer: dark
          ? const Color(0xFFFFE0D8)
          : const Color(0xFF4B160D),
      tertiary: dark ? const Color(0xFFF6C86B) : const Color(0xFF805500),
      surface: dark ? const Color(0xFF16171B) : Colors.white,
      onSurface: dark ? const Color(0xFFF2F0F4) : const Color(0xFF252328),
      onSurfaceVariant: dark
          ? const Color(0xFFB9B6C2)
          : const Color(0xFF68636C),
      outline: dark ? const Color(0xFF85818B) : const Color(0xFF7C757D),
      outlineVariant: dark ? const Color(0xFF34343D) : const Color(0xFFE3DFDC),
      surfaceContainerLowest: dark ? const Color(0xFF0D0E11) : Colors.white,
      surfaceContainerLow: dark
          ? const Color(0xFF191A20)
          : const Color(0xFFF7F3F0),
      surfaceContainer: dark
          ? const Color(0xFF1E2026)
          : const Color(0xFFF2EEEB),
      surfaceContainerHigh: dark
          ? const Color(0xFF25262D)
          : const Color(0xFFEDE8E5),
      surfaceContainerHighest: dark
          ? const Color(0xFF30313A)
          : const Color(0xFFE6E0DC),
      surfaceTint: Colors.transparent,
    );
    return ThemeData(
      useMaterial3: true,
      brightness: brightness,
      colorScheme: scheme,
      scaffoldBackgroundColor: background,
      appBarTheme: AppBarTheme(
        backgroundColor: background,
        foregroundColor: scheme.onSurface,
        scrolledUnderElevation: 0,
        elevation: 0,
        centerTitle: false,
        systemOverlayStyle: systemBars(brightness),
      ),
      dividerTheme: DividerThemeData(color: scheme.outlineVariant),
      navigationRailTheme: NavigationRailThemeData(
        backgroundColor: background,
        useIndicator: false,
        selectedIconTheme: IconThemeData(color: scheme.primary),
        unselectedIconTheme: IconThemeData(color: scheme.onSurfaceVariant),
        selectedLabelTextStyle: TextStyle(
          color: scheme.primary,
          fontWeight: FontWeight.w700,
        ),
        unselectedLabelTextStyle: TextStyle(color: scheme.onSurfaceVariant),
      ),
      inputDecorationTheme: InputDecorationTheme(
        filled: true,
        fillColor: scheme.surfaceContainer,
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(16),
          borderSide: BorderSide.none,
        ),
      ),
    );
  }

  static ThemeData _glass(Brightness brightness, TargetPlatform platform) {
    final dark = brightness == Brightness.dark;
    final ios = platform == TargetPlatform.iOS;
    final background = dark
        ? ios
              ? Colors.black
              : const Color(0xFF101114)
        : const Color(0xFFF7F6F4);
    final primary = dark ? const Color(0xFFFF6D57) : const Color(0xFFD53A25);
    final label = dark ? Colors.white : const Color(0xFF111113);
    final secondary = dark ? const Color(0xFF9D9DA6) : const Color(0xFF6C6C72);
    final scheme = ColorScheme.fromSeed(
      seedColor: primary,
      brightness: brightness,
      primary: primary,
      onPrimary: Colors.white,
      primaryContainer: dark
          ? const Color(0xFF4A1C15)
          : const Color(0xFFFFE3DC),
      onPrimaryContainer: dark
          ? const Color(0xFFFFDAD2)
          : const Color(0xFF5A150B),
      secondaryContainer: dark
          ? const Color(0xFF2C2C2E)
          : const Color(0xFFEDEBE8),
      onSecondaryContainer: label,
      tertiary: dark ? const Color(0xFFFFD166) : const Color(0xFF9A6A00),
      surface: dark ? const Color(0xFF1C1C1E) : Colors.white,
      onSurface: label,
      onSurfaceVariant: secondary,
      outline: dark ? const Color(0xFF545458) : const Color(0xFFC6C6C8),
      outlineVariant: dark ? const Color(0xFF2C2C2E) : const Color(0xFFE3E2E0),
      surfaceContainerLowest: dark ? const Color(0xFF0B0B0C) : Colors.white,
      surfaceContainerLow: dark
          ? const Color(0xFF151517)
          : const Color(0xFFF2F1EF),
      surfaceContainer: dark
          ? const Color(0xFF1C1C1E)
          : const Color(0xFFEDECEA),
      surfaceContainerHigh: dark
          ? const Color(0xFF2C2C2E)
          : const Color(0xFFE5E4E2),
      surfaceContainerHighest: dark
          ? const Color(0xFF3A3A3C)
          : const Color(0xFFDAD9D6),
      surfaceTint: Colors.transparent,
    );
    const continuous = RoundedRectangleBorder(
      borderRadius: BorderRadius.all(Radius.circular(18)),
    );
    final base = ThemeData(
      useMaterial3: true,
      brightness: brightness,
      platform: platform,
      colorScheme: scheme,
      scaffoldBackgroundColor: background,
      canvasColor: background,
      splashFactory: NoSplash.splashFactory,
      splashColor: Colors.transparent,
      highlightColor: label.withValues(alpha: .06),
      hoverColor: label.withValues(alpha: .04),
      cupertinoOverrideTheme: NoDefaultCupertinoThemeData(
        brightness: brightness,
        primaryColor: primary,
        scaffoldBackgroundColor: background,
      ),
      appBarTheme: AppBarTheme(
        backgroundColor: background,
        foregroundColor: label,
        scrolledUnderElevation: 0,
        elevation: 0,
        centerTitle: true,
        titleTextStyle: TextStyle(
          fontSize: 17,
          fontWeight: FontWeight.w600,
          letterSpacing: 0,
          color: label,
        ),
        systemOverlayStyle: systemBars(brightness),
      ),
      actionIconTheme: ActionIconThemeData(
        backButtonIconBuilder: (_) =>
            const GlassBackIcon(icon: LucideIcons.chevronLeft),
        closeButtonIconBuilder: (_) => const GlassBackIcon(icon: LucideIcons.x),
      ),
      iconTheme: IconThemeData(color: label),
      dividerTheme: DividerThemeData(
        color: scheme.outlineVariant,
        thickness: .5,
        space: .5,
      ),
      chipTheme: ChipThemeData(
        backgroundColor: dark
            ? const Color(0xFF1C1C1E)
            : const Color(0xFFEAE8E5),
        selectedColor: label,
        disabledColor: scheme.surfaceContainer,
        showCheckmark: false,
        side: BorderSide.none,
        shape: ios
            ? const StadiumBorder()
            : const RoundedRectangleBorder(
                borderRadius: BorderRadius.all(Radius.circular(14)),
              ),
        padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 2),
        labelStyle: TextStyle(
          fontSize: 14,
          fontWeight: FontWeight.w600,
          letterSpacing: 0,
          color: WidgetStateColor.resolveWith(
            (states) =>
                states.contains(WidgetState.selected) ? background : label,
          ),
        ),
        secondaryLabelStyle: TextStyle(
          fontSize: 14,
          fontWeight: FontWeight.w600,
          color: background,
        ),
        iconTheme: IconThemeData(color: secondary, size: 16),
      ),
      inputDecorationTheme: InputDecorationTheme(
        filled: true,
        fillColor: dark ? const Color(0xFF1C1C1E) : const Color(0xFFEAE8E5),
        isDense: true,
        contentPadding: const EdgeInsets.symmetric(
          horizontal: 12,
          vertical: 11,
        ),
        hintStyle: TextStyle(color: secondary),
        prefixIconColor: secondary,
        suffixIconColor: secondary,
        border: const OutlineInputBorder(
          borderRadius: BorderRadius.all(Radius.circular(14)),
          borderSide: BorderSide.none,
        ),
      ),
      filledButtonTheme: FilledButtonThemeData(
        style: FilledButton.styleFrom(
          shape: const StadiumBorder(),
          minimumSize: const Size(64, 50),
          textStyle: const TextStyle(
            fontSize: 17,
            fontWeight: FontWeight.w600,
            letterSpacing: 0,
          ),
        ),
      ),
      outlinedButtonTheme: OutlinedButtonThemeData(
        style: OutlinedButton.styleFrom(
          shape: const StadiumBorder(),
          side: BorderSide(color: scheme.outline),
          minimumSize: const Size(64, 44),
        ),
      ),
      textButtonTheme: TextButtonThemeData(
        style: TextButton.styleFrom(
          shape: const StadiumBorder(),
          textStyle: const TextStyle(fontSize: 16, fontWeight: FontWeight.w500),
        ),
      ),
      cardTheme: CardThemeData(
        elevation: 0,
        margin: const EdgeInsets.symmetric(vertical: 5),
        color: dark ? const Color(0xFF1C1C1E) : Colors.white,
        shape: ios
            ? continuous
            : const RoundedRectangleBorder(
                borderRadius: BorderRadius.all(Radius.circular(8)),
              ),
        clipBehavior: Clip.antiAlias,
      ),
      listTileTheme: ListTileThemeData(
        iconColor: secondary,
        shape: continuous,
        titleTextStyle: TextStyle(fontSize: 17, letterSpacing: 0, color: label),
        subtitleTextStyle: TextStyle(fontSize: 13, color: secondary),
      ),
      dialogTheme: DialogThemeData(
        backgroundColor: dark ? const Color(0xFF232325) : Colors.white,
        shape: const RoundedRectangleBorder(
          borderRadius: BorderRadius.all(Radius.circular(30)),
        ),
      ),
      popupMenuTheme: PopupMenuThemeData(
        color: dark ? const Color(0xFF252527) : Colors.white,
        elevation: 12,
        shadowColor: Colors.black.withValues(alpha: .3),
        shape: continuous,
      ),
      bottomSheetTheme: BottomSheetThemeData(
        backgroundColor: dark ? const Color(0xFF1C1C1E) : Colors.white,
        showDragHandle: true,
        shape: const RoundedRectangleBorder(
          borderRadius: BorderRadius.vertical(top: Radius.circular(34)),
        ),
      ),
      snackBarTheme: SnackBarThemeData(
        behavior: SnackBarBehavior.floating,
        backgroundColor: dark
            ? const Color(0xFF2C2C2E)
            : const Color(0xFF1C1C1E),
        contentTextStyle: const TextStyle(color: Colors.white, fontSize: 15),
        actionTextColor: primary,
        shape: continuous,
        elevation: 0,
      ),
      progressIndicatorTheme: ProgressIndicatorThemeData(
        color: primary,
        linearTrackColor: Colors.transparent,
      ),
      navigationRailTheme: NavigationRailThemeData(
        backgroundColor: background,
        useIndicator: false,
        selectedIconTheme: IconThemeData(color: primary),
        unselectedIconTheme: IconThemeData(color: secondary),
      ),
    );
    return base.copyWith(
      textTheme: base.textTheme.copyWith(
        titleLarge: base.textTheme.titleLarge?.copyWith(
          fontSize: 22,
          fontWeight: FontWeight.w700,
          letterSpacing: 0,
        ),
        titleMedium: base.textTheme.titleMedium?.copyWith(
          fontWeight: FontWeight.w600,
          letterSpacing: 0,
        ),
      ),
    );
  }
}
