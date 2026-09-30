import 'package:flutter/material.dart';
import 'package:lucide_icons_flutter/lucide_icons.dart';

import 'follow_state.dart';
import 'ios_dialogs.dart';
import 'liquid_glass.dart';
import 'local_store.dart';
import 'models.dart';
import 'widgets.dart';

Future<void> showDramaActions(
  BuildContext context, {
  required Drama drama,
  required LocalStore store,
  VoidCallback? onContinue,
  VoidCallback? onDownload,
  VoidCallback? onSelect,
  bool history = false,
}) async {
  final epoch = store.profileEpoch;
  final following = store.following(drama.id);
  final glass = glassDesign(context);
  final choice = await showChoiceSheet<String>(
    context,
    title: drama.title,
    message: '手动标记已看会保留真实播放进度。',
    choices: [
      if (onContinue != null && store.watched(drama.id) != null)
        SheetChoice(
          'continue',
          '继续观看',
          icon: glass ? LucideIcons.play : Icons.play_arrow_rounded,
        ),
      SheetChoice(
        'favorite',
        following == null ? '加入追剧' : '取消追剧',
        icon: glass
            ? following == null
                  ? LucideIcons.bookmarkPlus
                  : LucideIcons.bookmarkX
            : Icons.bookmark_outline,
      ),
      for (final status in FollowStatus.values)
        SheetChoice(
          'status:${status.name}',
          '标记${status.label}',
          selected: following?.status == status,
          icon: switch (status) {
            FollowStatus.planned =>
              glass ? LucideIcons.bookmarkPlus : Icons.bookmark_add_outlined,
            FollowStatus.watching =>
              glass ? LucideIcons.circlePlay : Icons.play_circle_outline,
            FollowStatus.watched =>
              glass ? LucideIcons.circleCheckBig : Icons.check_circle_outline,
          },
        ),
      if (following != null && following.hasUpdates)
        SheetChoice(
          'read',
          '标记 ${following.updateLabel}已读',
          icon: glass ? LucideIcons.mailCheck : Icons.mark_email_read_outlined,
        ),
      if (onDownload != null && store.canDownload)
        SheetChoice(
          'download',
          '下载选集',
          icon: glass ? LucideIcons.arrowDownToLine : Icons.download_outlined,
        ),
      if (onSelect != null && store.canDownload)
        SheetChoice(
          'select',
          '多选下载',
          icon: glass ? LucideIcons.listChecks : Icons.checklist_rounded,
        ),
      if (history && store.watched(drama.id) != null)
        SheetChoice(
          'removeHistory',
          '删除这条观看记录',
          destructive: true,
          icon: glass ? LucideIcons.trash2 : Icons.history_toggle_off,
        ),
    ],
  );
  if (choice == null ||
      !context.mounted ||
      epoch != store.profileEpoch ||
      !store.allowsSource(drama.source)) {
    return;
  }
  if (choice.startsWith('status:')) {
    final status = FollowStatus.values.firstWhere(
      (value) => choice == 'status:${value.name}',
    );
    await saveUserChange(context, () => store.setFollowStatus(drama, status));
    return;
  }
  switch (choice) {
    case 'continue':
      onContinue?.call();
    case 'favorite':
      await saveUserChange(context, () => store.toggleFavorite(drama));
    case 'read':
      await saveUserChange(context, () => store.markUpdatesRead(drama.id));
    case 'download':
      if (store.canDownload) onDownload?.call();
    case 'select':
      if (store.canDownload) onSelect?.call();
    case 'removeHistory':
      await saveUserChange(context, () => store.removeHistory(drama.id));
  }
}

class DramaActionButton extends StatelessWidget {
  const DramaActionButton({
    super.key,
    required this.drama,
    required this.onPressed,
  });
  final Drama drama;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) {
    if (glassDesign(context)) {
      return Padding(
        key: ValueKey('drama-actions-${drama.id}'),
        padding: const EdgeInsets.all(4),
        child: GlassIconButton(
          icon: LucideIcons.ellipsis,
          tooltip: '${drama.title} · 更多操作',
          size: 30,
          blur: 0,
          color: Colors.white,
          tint: Colors.black.withValues(alpha: .28),
          onPressed: onPressed,
        ),
      );
    }
    return IconButton.filledTonal(
      key: ValueKey('drama-actions-${drama.id}'),
      tooltip: '${drama.title} · 更多操作',
      onPressed: onPressed,
      icon: const Icon(Icons.more_horiz_rounded, size: 20),
      style: IconButton.styleFrom(
        backgroundColor: Colors.black.withValues(alpha: .64),
        foregroundColor: Colors.white,
        minimumSize: const Size(40, 40),
        padding: const EdgeInsets.all(8),
        visualDensity: VisualDensity.compact,
      ),
    );
  }
}
