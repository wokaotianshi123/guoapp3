import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'app_layout.dart';
import 'liquid_glass.dart';

class SheetChoice<T> {
  const SheetChoice(
    this.value,
    this.label, {
    this.icon,
    this.selected = false,
    this.destructive = false,
    this.subtitle,
  });
  final T value;
  final String label;
  final IconData? icon;
  final bool selected;
  final bool destructive;
  final String? subtitle;
}

Future<T?> showChoiceSheet<T>(
  BuildContext context, {
  required String title,
  required List<SheetChoice<T>> choices,
  String? message,
}) {
  if (!iosDesign(context)) {
    return showDialog<T>(
      context: context,
      builder: (context) => SimpleDialog(
        title: Text(title, maxLines: 2, overflow: TextOverflow.ellipsis),
        children: [
          for (final choice in choices)
            SimpleDialogOption(
              onPressed: () => Navigator.pop(context, choice.value),
              child: Row(
                children: [
                  if (choice.icon != null) ...[
                    Icon(choice.icon, size: 22),
                    const SizedBox(width: 14),
                  ],
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Text(choice.label),
                        if (choice.subtitle != null)
                          Text(
                            choice.subtitle!,
                            style: Theme.of(context).textTheme.bodySmall,
                          ),
                      ],
                    ),
                  ),
                  if (choice.selected)
                    const Icon(Icons.check_rounded, size: 20),
                ],
              ),
            ),
          if (message != null)
            Padding(
              padding: const EdgeInsets.fromLTRB(24, 10, 24, 8),
              child: Text(
                message,
                style: Theme.of(context).textTheme.bodySmall,
              ),
            ),
        ],
      ),
    );
  }
  HapticFeedback.lightImpact();
  final colors = Theme.of(context).colorScheme;
  return showCupertinoModalPopup<T>(
    context: context,
    builder: (sheetContext) => CupertinoTheme(
      data: CupertinoThemeData(
        brightness: Theme.of(context).brightness,
        primaryColor: colors.primary,
      ),
      child: CupertinoActionSheet(
        title: Text(title, maxLines: 2, overflow: TextOverflow.ellipsis),
        message: message == null ? null : Text(message),
        actions: [
          for (final choice in choices)
            CupertinoActionSheetAction(
              isDefaultAction: choice.selected,
              isDestructiveAction: choice.destructive,
              onPressed: () => Navigator.pop(sheetContext, choice.value),
              child: Row(
                children: [
                  SizedBox(
                    width: 28,
                    child: choice.icon == null
                        ? null
                        : Icon(
                            choice.icon,
                            size: 20,
                            color: choice.destructive
                                ? CupertinoColors.destructiveRed
                                : colors.primary,
                          ),
                  ),
                  Expanded(
                    child: Text(
                      choice.subtitle == null
                          ? choice.label
                          : '${choice.label} · ${choice.subtitle}',
                      textAlign: TextAlign.center,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                  SizedBox(
                    width: 28,
                    child: choice.selected
                        ? Icon(
                            Icons.check_rounded,
                            size: 20,
                            color: colors.primary,
                          )
                        : null,
                  ),
                ],
              ),
            ),
        ],
        cancelButton: CupertinoActionSheetAction(
          onPressed: () => Navigator.pop(sheetContext),
          child: const Text('取消'),
        ),
      ),
    ),
  );
}

Future<bool> confirmAction(
  BuildContext context, {
  required String title,
  required String message,
  required String confirm,
  String cancel = '取消',
  bool destructive = false,
}) async {
  if (!iosDesign(context)) {
    final result = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(title),
        content: Text(message),
        actions: [
          TextButton(
            autofocus: AppLayout.isTelevision(context),
            onPressed: () => Navigator.pop(context, false),
            child: Text(cancel),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: Text(confirm),
          ),
        ],
      ),
    );
    return result == true;
  }
  final result = await showCupertinoDialog<bool>(
    context: context,
    builder: (dialogContext) => CupertinoTheme(
      data: CupertinoThemeData(
        brightness: Theme.of(context).brightness,
        primaryColor: Theme.of(context).colorScheme.primary,
      ),
      child: CupertinoAlertDialog(
        title: Text(title),
        content: Padding(
          padding: const EdgeInsets.only(top: 6),
          child: Text(message),
        ),
        actions: [
          CupertinoDialogAction(
            onPressed: () => Navigator.pop(dialogContext, false),
            child: Text(cancel),
          ),
          CupertinoDialogAction(
            isDefaultAction: !destructive,
            isDestructiveAction: destructive,
            onPressed: () => Navigator.pop(dialogContext, true),
            child: Text(confirm),
          ),
        ],
      ),
    ),
  );
  return result == true;
}

class PullDownEntry {
  const PullDownEntry({
    required this.label,
    required this.onPressed,
    this.icon,
    this.checked = false,
    this.destructive = false,
    this.dividerBefore = false,
  });
  final String label;
  final IconData? icon;
  final VoidCallback? onPressed;
  final bool checked;
  final bool destructive;
  final bool dividerBefore;
}

class PullDownButton extends StatelessWidget {
  const PullDownButton({
    super.key,
    required this.entries,
    required this.builder,
  });
  final List<PullDownEntry> entries;
  final Widget Function(BuildContext context, VoidCallback open) builder;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    if (!iosDesign(context)) {
      return MenuAnchor(
        menuChildren: [
          for (final entry in entries) ...[
            if (entry.dividerBefore)
              const Padding(
                padding: EdgeInsets.symmetric(horizontal: 12),
                child: Divider(height: 1),
              ),
            MenuItemButton(
              onPressed: entry.onPressed,
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  SizedBox(
                    width: 24,
                    child: entry.checked
                        ? Icon(
                            Icons.check_rounded,
                            size: 18,
                            color: colors.primary,
                          )
                        : null,
                  ),
                  Text(
                    entry.label,
                    style: entry.destructive
                        ? TextStyle(color: colors.error)
                        : null,
                  ),
                  if (entry.icon != null) ...[
                    const SizedBox(width: 20),
                    Icon(entry.icon, size: 20),
                  ],
                ],
              ),
            ),
          ],
        ],
        builder: (context, controller, _) => builder(context, () {
          HapticFeedback.selectionClick();
          if (controller.isOpen) {
            controller.close();
          } else {
            controller.open();
          }
        }),
      );
    }
    return CupertinoTheme(
      data: CupertinoThemeData(
        brightness: Theme.of(context).brightness,
        primaryColor: colors.primary,
      ),
      child: CupertinoMenuAnchor(
        menuChildren: [
          for (final entry in entries) ...[
            if (entry.dividerBefore) const CupertinoMenuDivider(),
            CupertinoMenuItem(
              leading: entry.checked
                  ? Icon(Icons.check_rounded, size: 18, color: colors.primary)
                  : null,
              trailing: entry.icon == null ? null : Icon(entry.icon, size: 20),
              isDestructiveAction: entry.destructive,
              onPressed: entry.onPressed,
              child: Text(entry.label),
            ),
          ],
        ],
        builder: (context, controller, _) => builder(context, () {
          HapticFeedback.selectionClick();
          if (controller.isOpen) {
            controller.close();
          } else {
            controller.open();
          }
        }),
      ),
    );
  }
}
