import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/physics.dart';
import 'package:flutter/services.dart';

import 'liquid_glass.dart';

class LiquidTabItem {
  const LiquidTabItem({
    required this.icon,
    required this.label,
    this.selectedIcon,
  });
  final IconData icon;
  final IconData? selectedIcon;
  final String label;
}

class LiquidTabBar extends StatefulWidget {
  const LiquidTabBar({
    super.key,
    required this.items,
    required this.selectedIndex,
    required this.onSelected,
  });

  final List<LiquidTabItem> items;
  final int selectedIndex;
  final ValueChanged<int> onSelected;

  @override
  State<LiquidTabBar> createState() => _LiquidTabBarState();
}

class _LiquidTabBarState extends State<LiquidTabBar>
    with TickerProviderStateMixin {
  static final _spring = SpringDescription.withDurationAndBounce(
    duration: const Duration(milliseconds: 460),
    bounce: .26,
  );
  late final AnimationController _position = AnimationController.unbounded(
    vsync: this,
    value: widget.selectedIndex.toDouble(),
  );
  late final AnimationController _lift = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 180),
    reverseDuration: const Duration(milliseconds: 380),
  );
  double _itemWidth = 1;
  int _hovered = -1;

  @override
  void didUpdateWidget(covariant LiquidTabBar oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.selectedIndex != widget.selectedIndex &&
        !_lift.status.isForwardOrCompleted) {
      _settle(widget.selectedIndex);
    }
  }

  @override
  void dispose() {
    _position.dispose();
    _lift.dispose();
    super.dispose();
  }

  void _settle(int target, [double velocity = 0]) {
    _position.animateWith(
      SpringSimulation(
        _spring,
        _position.value,
        target.toDouble(),
        velocity,
        tolerance: const Tolerance(velocity: .01, distance: .001),
      ),
    );
  }

  void _select(int index) {
    if (index != widget.selectedIndex) {
      HapticFeedback.selectionClick();
      widget.onSelected(index);
    }
    _settle(index);
  }

  void _dragStart(DragStartDetails details) {
    _position.stop();
    _hovered = _position.value.round();
    _lift.forward();
  }

  void _dragUpdate(DragUpdateDetails details) {
    final last = widget.items.length - 1;
    _position.value = (_position.value + details.delta.dx / _itemWidth).clamp(
      -.3,
      last + .3,
    );
    final hovered = _position.value.round().clamp(0, last);
    if (hovered != _hovered) {
      _hovered = hovered;
      HapticFeedback.selectionClick();
    }
  }

  void _dragEnd(DragEndDetails details) {
    _lift.reverse();
    final last = widget.items.length - 1;
    final velocity = (details.primaryVelocity ?? 0) / _itemWidth;
    final projected = _position.value + velocity * .08;
    final target = projected.round().clamp(0, last);
    if (target != widget.selectedIndex) widget.onSelected(target);
    _settle(target, velocity);
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final dark = Theme.of(context).brightness == Brightness.dark;
    final bottom = math.max(MediaQuery.paddingOf(context).bottom - 6, 12.0);
    final textScale = MediaQuery.textScalerOf(
      context,
    ).clamp(maxScaleFactor: 1.3);
    return Padding(
      padding: EdgeInsets.fromLTRB(16, 0, 16, bottom),
      child: LayoutBuilder(
        builder: (context, constraints) {
          final width = math.min(
            constraints.maxWidth,
            math.min(520.0, widget.items.length * 96.0),
          );
          _itemWidth = (width - 8) / widget.items.length;
          return Center(
            heightFactor: 1,
            child: MediaQuery(
              data: MediaQuery.of(context).copyWith(textScaler: textScale),
              child: SizedBox(
                width: width,
                height: 64,
                child: GestureDetector(
                  behavior: HitTestBehavior.opaque,
                  onHorizontalDragStart: _dragStart,
                  onHorizontalDragUpdate: _dragUpdate,
                  onHorizontalDragEnd: _dragEnd,
                  onHorizontalDragCancel: () {
                    _lift.reverse();
                    _settle(widget.selectedIndex);
                  },
                  child: LiquidGlass(
                    child: AnimatedBuilder(
                      animation: Listenable.merge([_position, _lift]),
                      builder: (context, _) {
                        final position = _position.value;
                        final offset = (position - position.roundToDouble())
                            .abs();
                        final lift = Curves.easeOut.transform(_lift.value);
                        return Stack(
                          clipBehavior: Clip.none,
                          children: [
                            Positioned(
                              left: 4 + position * _itemWidth,
                              top: 4,
                              bottom: 4,
                              width: _itemWidth,
                              child: Transform.scale(
                                scaleX: 1 + offset * .42 + lift * .1,
                                scaleY: 1 - offset * .14 + lift * .08,
                                child: _Lens(dark: dark, lifted: lift),
                              ),
                            ),
                            Padding(
                              padding: const EdgeInsets.all(4),
                              child: Row(
                                children: [
                                  for (final (index, item)
                                      in widget.items.indexed)
                                    Expanded(
                                      child: _TabButton(
                                        key: ValueKey('bottom-nav-$index'),
                                        item: item,
                                        selected: index == widget.selectedIndex,
                                        emphasis:
                                            1 -
                                            (position - index).abs().clamp(
                                              0.0,
                                              1.0,
                                            ),
                                        activeColor: colors.primary,
                                        color: colors.onSurface,
                                        onTap: () => _select(index),
                                      ),
                                    ),
                                ],
                              ),
                            ),
                          ],
                        );
                      },
                    ),
                  ),
                ),
              ),
            ),
          );
        },
      ),
    );
  }
}

class _Lens extends StatelessWidget {
  const _Lens({required this.dark, required this.lifted});
  final bool dark;
  final double lifted;

  @override
  Widget build(BuildContext context) => DecoratedBox(
    decoration: BoxDecoration(
      borderRadius: BorderRadius.circular(999),
      gradient: LinearGradient(
        begin: Alignment.topCenter,
        end: Alignment.bottomCenter,
        colors: dark
            ? [
                Colors.white.withValues(alpha: .2 + lifted * .08),
                Colors.white.withValues(alpha: .1 + lifted * .06),
              ]
            : [
                Colors.black.withValues(alpha: .07 + lifted * .03),
                Colors.black.withValues(alpha: .04 + lifted * .03),
              ],
      ),
      border: Border.all(
        color: Colors.white.withValues(alpha: dark ? .18 : .7),
        width: .8,
      ),
      boxShadow: lifted > 0
          ? [
              BoxShadow(
                color: Colors.black.withValues(alpha: .18 * lifted),
                blurRadius: 18,
                offset: Offset(0, 6 * lifted),
              ),
            ]
          : null,
    ),
  );
}

class _TabButton extends StatelessWidget {
  const _TabButton({
    super.key,
    required this.item,
    required this.selected,
    required this.emphasis,
    required this.activeColor,
    required this.color,
    required this.onTap,
  });
  final LiquidTabItem item;
  final bool selected;
  final double emphasis;
  final Color activeColor;
  final Color color;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final tint = Color.lerp(color, activeColor, emphasis)!;
    return Semantics(
      container: true,
      button: true,
      selected: selected,
      label: item.label,
      excludeSemantics: true,
      child: GlassTapTarget(
        onTap: onTap,
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            Transform.scale(
              scale: 1 + emphasis * .06,
              child: Icon(
                selected ? item.selectedIcon ?? item.icon : item.icon,
                size: 23,
                color: tint,
              ),
            ),
            const SizedBox(height: 3),
            Text(
              item.label,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(
                fontSize: 10.5,
                height: 1.1,
                letterSpacing: 0,
                fontWeight: selected ? FontWeight.w700 : FontWeight.w500,
                color: tint,
              ),
            ),
          ],
        ),
      ),
    );
  }
}
