import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'app_layout.dart';

bool iosDesign(BuildContext context) =>
    Theme.of(context).platform == TargetPlatform.iOS &&
    !AppLayout.isTelevision(context);

bool glassDesign(BuildContext context) =>
    !AppLayout.isTelevision(context) &&
    switch (Theme.of(context).platform) {
      TargetPlatform.iOS ||
      TargetPlatform.android ||
      TargetPlatform.windows => true,
      _ => false,
    };

const _saturation = <double>[
  1.28, -0.24, -0.04, 0, 0, //
  -0.08, 1.16, -0.08, 0, 0, //
  -0.04, -0.24, 1.28, 0, 0, //
  0, 0, 0, 1, 0, //
];

class LiquidGlass extends StatelessWidget {
  const LiquidGlass({
    super.key,
    required this.child,
    this.borderRadius = const BorderRadius.all(Radius.circular(999)),
    this.blur = 22,
    this.tint,
    this.elevated = true,
  });

  final Widget child;
  final BorderRadius borderRadius;
  final double blur;
  final Color? tint;
  final bool elevated;

  @override
  Widget build(BuildContext context) {
    final dark = Theme.of(context).brightness == Brightness.dark;
    final blurSigma = Theme.of(context).platform == TargetPlatform.iOS
        ? blur
        : blur * .65;
    final base =
        tint ??
        (dark
            ? const Color(0xFF1C1C1F).withValues(alpha: .58)
            : Colors.white.withValues(alpha: .62));
    return DecoratedBox(
      decoration: BoxDecoration(
        borderRadius: borderRadius,
        boxShadow: elevated
            ? [
                BoxShadow(
                  color: Colors.black.withValues(alpha: dark ? .42 : .12),
                  blurRadius: 28,
                  spreadRadius: -6,
                  offset: const Offset(0, 12),
                ),
                BoxShadow(
                  color: Colors.black.withValues(alpha: dark ? .24 : .05),
                  blurRadius: 3,
                  offset: const Offset(0, 1),
                ),
              ]
            : null,
      ),
      child: ClipRSuperellipse(
        borderRadius: borderRadius,
        child: BackdropFilter(
          enabled: blurSigma > 0 && !MediaQuery.highContrastOf(context),
          filter: ui.ImageFilter.compose(
            outer: ui.ImageFilter.blur(sigmaX: blurSigma, sigmaY: blurSigma),
            inner: const ColorFilter.matrix(_saturation),
          ),
          child: CustomPaint(
            foregroundPainter: _GlassRimPainter(
              borderRadius: borderRadius,
              dark: dark,
            ),
            child: DecoratedBox(
              decoration: BoxDecoration(
                gradient: LinearGradient(
                  begin: Alignment.topCenter,
                  end: Alignment.bottomCenter,
                  colors: [
                    Color.alphaBlend(
                      Colors.white.withValues(alpha: dark ? .06 : .18),
                      base,
                    ),
                    base,
                  ],
                ),
              ),
              child: child,
            ),
          ),
        ),
      ),
    );
  }
}

class _GlassRimPainter extends CustomPainter {
  const _GlassRimPainter({required this.borderRadius, required this.dark});
  final BorderRadius borderRadius;
  final bool dark;

  @override
  void paint(Canvas canvas, Size size) {
    final rect = Offset.zero & size;
    final shape = borderRadius.toRSuperellipse(rect.deflate(.5));
    canvas.drawRSuperellipse(
      shape,
      Paint()
        ..style = PaintingStyle.stroke
        ..strokeWidth = 1
        ..shader = LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: [
            Colors.white.withValues(alpha: dark ? .34 : .95),
            Colors.white.withValues(alpha: dark ? .05 : .25),
            Colors.white.withValues(alpha: dark ? .16 : .6),
          ],
          stops: const [0, .55, 1],
        ).createShader(rect),
    );
  }

  @override
  bool shouldRepaint(_GlassRimPainter oldDelegate) =>
      oldDelegate.dark != dark || oldDelegate.borderRadius != borderRadius;
}

class PressableScale extends StatefulWidget {
  const PressableScale({
    super.key,
    required this.child,
    this.enabled = true,
    this.pressedScale = .95,
  });
  final Widget child;
  final bool enabled;
  final double pressedScale;

  @override
  State<PressableScale> createState() => _PressableScaleState();
}

class _PressableScaleState extends State<PressableScale> {
  bool _pressed = false;

  void _set(bool pressed) {
    if (!widget.enabled || _pressed == pressed) return;
    setState(() => _pressed = pressed);
  }

  @override
  Widget build(BuildContext context) => Listener(
    onPointerDown: (_) => _set(true),
    onPointerUp: (_) => _set(false),
    onPointerCancel: (_) => _set(false),
    child: AnimatedScale(
      scale: _pressed ? widget.pressedScale : 1,
      duration: Duration(milliseconds: _pressed ? 110 : 320),
      curve: _pressed ? Curves.easeOut : Curves.easeOutBack,
      child: widget.child,
    ),
  );
}

class GlassTapTarget extends StatefulWidget {
  const GlassTapTarget({
    super.key,
    required this.child,
    required this.onTap,
    this.onLongPress,
    this.onSecondaryTap,
    this.focusNode,
    this.autofocus = false,
    this.borderRadius = const BorderRadius.all(Radius.circular(999)),
  });

  final Widget child;
  final VoidCallback? onTap;
  final VoidCallback? onLongPress;
  final VoidCallback? onSecondaryTap;
  final FocusNode? focusNode;
  final bool autofocus;
  final BorderRadius borderRadius;

  @override
  State<GlassTapTarget> createState() => _GlassTapTargetState();
}

class _GlassTapTargetState extends State<GlassTapTarget> {
  bool _focused = false;
  bool _hovered = false;

  @override
  Widget build(BuildContext context) {
    final enabled = widget.onTap != null;
    final colors = Theme.of(context).colorScheme;
    return FocusableActionDetector(
      focusNode: widget.focusNode,
      autofocus: widget.autofocus,
      enabled: enabled,
      mouseCursor: enabled ? SystemMouseCursors.click : MouseCursor.defer,
      onShowFocusHighlight: (value) => setState(() => _focused = value),
      onShowHoverHighlight: (value) => setState(() => _hovered = value),
      actions: {
        ActivateIntent: CallbackAction<ActivateIntent>(
          onInvoke: (_) {
            widget.onTap?.call();
            return null;
          },
        ),
      },
      child: DecoratedBox(
        position: DecorationPosition.foreground,
        decoration: BoxDecoration(
          borderRadius: widget.borderRadius,
          border: Border.all(
            color: enabled && _focused
                ? colors.primary
                : enabled && _hovered
                ? colors.onSurface.withValues(alpha: .25)
                : Colors.transparent,
            width: 2,
          ),
        ),
        child: GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: widget.onTap,
          onLongPress: widget.onLongPress,
          onSecondaryTap: widget.onSecondaryTap,
          child: widget.child,
        ),
      ),
    );
  }
}

class GlassIconButton extends StatelessWidget {
  const GlassIconButton({
    super.key,
    required this.icon,
    required this.tooltip,
    required this.onPressed,
    this.size = 44,
    this.color,
    this.tint,
    this.active = false,
    this.blur = 22,
    this.focusNode,
    this.autofocus = false,
  });
  final IconData icon;
  final String tooltip;
  final VoidCallback? onPressed;
  final double size;
  final Color? color;
  final Color? tint;
  final bool active;
  final double blur;
  final FocusNode? focusNode;
  final bool autofocus;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final enabled = onPressed != null;
    return Semantics(
      button: true,
      enabled: enabled,
      label: tooltip,
      excludeSemantics: true,
      child: Tooltip(
        message: tooltip,
        excludeFromSemantics: true,
        child: PressableScale(
          enabled: enabled,
          pressedScale: .9,
          child: GlassTapTarget(
            focusNode: focusNode,
            autofocus: autofocus,
            onTap: enabled
                ? () {
                    HapticFeedback.selectionClick();
                    onPressed!();
                  }
                : null,
            child: SizedBox.square(
              dimension: size,
              child: LiquidGlass(
                blur: blur,
                tint: active ? colors.primary.withValues(alpha: .9) : tint,
                child: Center(
                  child: Icon(
                    icon,
                    size: size * .45,
                    color: !enabled
                        ? colors.onSurface.withValues(alpha: .3)
                        : active
                        ? colors.onPrimary
                        : color ?? colors.onSurface,
                  ),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class GlassButtonGroup extends StatelessWidget {
  const GlassButtonGroup({super.key, required this.children});
  final List<GlassGroupItem> children;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    return SizedBox(
      height: 44,
      child: LiquidGlass(
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 4),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              for (final item in children)
                Semantics(
                  key: item.key,
                  button: true,
                  enabled: item.onPressed != null,
                  label: item.tooltip,
                  excludeSemantics: true,
                  child: Tooltip(
                    message: item.tooltip,
                    excludeFromSemantics: true,
                    child: PressableScale(
                      enabled: item.onPressed != null,
                      pressedScale: .86,
                      child: GlassTapTarget(
                        focusNode: item.focusNode,
                        autofocus: item.autofocus,
                        onTap: item.onPressed == null
                            ? null
                            : () {
                                HapticFeedback.selectionClick();
                                item.onPressed!();
                              },
                        child: SizedBox(
                          width: 40,
                          height: 44,
                          child: Center(
                            child:
                                item.child ??
                                Icon(
                                  item.icon,
                                  size: 20,
                                  color: item.onPressed == null
                                      ? colors.onSurface.withValues(alpha: .3)
                                      : item.highlighted
                                      ? colors.primary
                                      : colors.onSurface,
                                ),
                          ),
                        ),
                      ),
                    ),
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

class GlassGroupItem {
  const GlassGroupItem({
    this.key,
    this.icon,
    this.child,
    required this.tooltip,
    required this.onPressed,
    this.highlighted = false,
    this.focusNode,
    this.autofocus = false,
  });
  final Key? key;
  final IconData? icon;
  final Widget? child;
  final String tooltip;
  final VoidCallback? onPressed;
  final bool highlighted;
  final FocusNode? focusNode;
  final bool autofocus;
}

class GlassBackIcon extends StatelessWidget {
  const GlassBackIcon({super.key, required this.icon});
  final IconData icon;

  @override
  Widget build(BuildContext context) => SizedBox.square(
    dimension: 38,
    child: LiquidGlass(
      elevated: false,
      child: Center(
        child: Icon(
          icon,
          size: 20,
          color: Theme.of(context).colorScheme.onSurface,
        ),
      ),
    ),
  );
}
