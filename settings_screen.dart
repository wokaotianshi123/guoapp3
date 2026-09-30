import 'dart:convert';
import 'dart:io';

import 'package:file_picker/file_picker.dart';
import 'package:flutter/cupertino.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:lucide_icons_flutter/lucide_icons.dart';
import 'package:path_provider/path_provider.dart';

import 'core_bridge.dart';
import 'app_theme.dart';
import 'background_downloads.dart';
import 'ios_dialogs.dart';
import 'liquid_glass.dart';
import 'local_store.dart';
import 'profiles_screen.dart';
import 'remote_widgets.dart';
import 'app_build.dart';
import 'sources_screen.dart';
import 'widgets.dart';
import 'resource_settings_screen.dart';
import 'lan_screen.dart';

String storageSize(int bytes) {
  if (bytes < 0) return '暂不可用';
  if (bytes < 1024 * 1024) return '${(bytes / 1024).toStringAsFixed(1)} KB';
  if (bytes < 1024 * 1024 * 1024) {
    return '${(bytes / (1024 * 1024)).toStringAsFixed(1)} MB';
  }
  return '${(bytes / (1024 * 1024 * 1024)).toStringAsFixed(2)} GB';
}

class SettingsScreen extends StatefulWidget {
  const SettingsScreen({
    super.key,
    required this.repository,
    required this.store,
  });
  final AppRepository repository;
  final LocalStore store;
  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  bool _busy = false;
  String? _message;

  @override
  void initState() {
    super.initState();
    ensureTelevisionFocus(context);
  }

  Future<void> _chooseTheme() async {
    final selected = await showDialog<String>(
      context: context,
      builder: (context) => SimpleDialog(
        title: const Text('外观主题'),
        children: [
          RadioGroup<String>(
            groupValue: widget.store.themeMode,
            onChanged: (value) => Navigator.pop(context, value),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                for (final mode in ['light', 'dark', 'system'])
                  RadioListTile<String>(
                    value: mode,
                    title: Text(AppTheme.label(mode)),
                    subtitle: mode == 'system'
                        ? const Text('随设备的深色模式自动切换')
                        : null,
                  ),
              ],
            ),
          ),
        ],
      ),
    );
    if (selected != null && mounted) {
      await saveUserChange(context, () => widget.store.setThemeMode(selected));
    }
  }

  Future<void> _backup(bool restore) async {
    setState(() {
      _busy = true;
      _message = null;
    });
    try {
      if (!restore) {
        final content = await widget.store.exportBackup();
        final saved = await FilePicker.saveFile(
          fileName:
              '$appSlug-backup-${DateTime.now().toIso8601String().substring(0, 10)}.json',
          bytes: Uint8List.fromList(utf8.encode(content)),
          mimeType: 'application/json',
        );
        if (saved != null && mounted) setState(() => _message = '备份已保存');
      } else {
        final file = await FilePicker.pickFile(
          type: FileType.custom,
          allowedExtensions: ['json'],
        );
        if (file == null || !mounted) return;
        final size = await file.length();
        if (size == null || size > 8 * 1024 * 1024) {
          throw const FormatException('备份文件过大或无法读取');
        }
        final content = utf8.decode(await file.readAsBytes());
        final data = widget.store.validateBackup(content);
        if (!mounted) return;
        final accepted = await showDialog<bool>(
          context: context,
          builder: (context) => AlertDialog(
            title: const Text('恢复备份？'),
            content: Text(
              '包含 ${(data['profiles'] as List).length} 个用户。将替换本机的用户、追剧、观看记录和偏好设置；已下载视频保留。恢复后使用备份内的管理员密码登录。',
            ),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(context, false),
                child: const Text('取消'),
              ),
              FilledButton(
                onPressed: () => Navigator.pop(context, true),
                child: const Text('恢复'),
              ),
            ],
          ),
        );
        if (accepted != true || !mounted) return;
        await widget.store.importBackup(content);
        if (mounted) Navigator.of(context).popUntil((route) => route.isFirst);
      }
    } catch (error) {
      if (mounted) setState(() => _message = error.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: widget.store,
    builder: (_, _) => Scaffold(
      appBar: AppBar(title: const Text('设置与备份')),
      body: glassDesign(context) ? _glassBody() : _materialBody(),
    ),
  );

  Widget _materialBody() => Center(
    child: ConstrainedBox(
      constraints: const BoxConstraints(maxWidth: 720),
      child: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          ListTile(
            key: const ValueKey('lan-settings'),
            leading: const Icon(Icons.devices_rounded),
            title: const Text('设备互联'),
            subtitle: const Text('局域网自动同步追剧与推送播放'),
            trailing: const Icon(Icons.chevron_right_rounded),
            onTap: () => openLanSync(context),
          ),
          if (widget.repository.supportsSourceManagement)
            ListTile(
              leading: const Icon(Icons.dns_outlined),
              title: const Text('站源管理'),
              subtitle: const Text('独立更新、连接与播放检测'),
              trailing: const Icon(Icons.chevron_right_rounded),
              onTap: () => Navigator.push(
                context,
                MaterialPageRoute<void>(
                  builder: (_) => SourcesScreen(
                    repository: widget.repository,
                    store: widget.store,
                  ),
                ),
              ),
            ),
          ListTile(
            key: const ValueKey('theme-setting'),
            leading: const Icon(Icons.palette_outlined),
            title: const Text('外观主题'),
            subtitle: Text(AppTheme.label(widget.store.themeMode)),
            trailing: const Icon(Icons.chevron_right_rounded),
            onTap: _chooseTheme,
          ),
          ListTile(
            leading: const Icon(Icons.people_outline),
            title: const Text('用户管理'),
            subtitle: Text('当前：${widget.store.profile.name}'),
            onTap: () => Navigator.push(
              context,
              MaterialPageRoute<void>(
                builder: (_) => ProfilesScreen(store: widget.store),
              ),
            ),
          ),
          if (widget.store.canDownload)
            ListTile(
              leading: const Icon(Icons.download_outlined),
              title: const Text('下载偏好'),
              subtitle: Text(widget.store.downloadPreferences.qualityLabel),
              trailing: const Icon(Icons.chevron_right_rounded),
              onTap: () => Navigator.push(
                context,
                MaterialPageRoute<void>(
                  builder: (_) =>
                      DownloadPreferencesScreen(store: widget.store),
                ),
              ),
            ),
          if (widget.store.canDownload)
            ListTile(
              leading: const Icon(Icons.folder_outlined),
              title: const Text('下载目录与空间'),
              subtitle: const Text('查看存储用量、迁移已下载文件'),
              onTap: () => Navigator.push(
                context,
                MaterialPageRoute<void>(
                  builder: (_) => StorageScreen(
                    repository: widget.repository,
                    store: widget.store,
                  ),
                ),
              ),
            ),
          if (widget.store.profile.admin) ...[
            ListTile(
              leading: const Icon(Icons.settings_ethernet_rounded),
              title: const Text('网络与资源'),
              subtitle: const Text('代理、目录请求间隔、下载并发与站源目录'),
              trailing: const Icon(Icons.chevron_right_rounded),
              onTap: () => Navigator.push(
                context,
                MaterialPageRoute<void>(
                  builder: (_) => ResourceSettingsScreen(
                    repository: widget.repository,
                    store: widget.store,
                  ),
                ),
              ),
            ),
            SwitchListTile.adaptive(
              value: widget.store.autoExport,
              title: const Text('下载完成后自动导出 Emby'),
              subtitle: const Text(
                '在下载目录的 exports 中生成视频和海报 URL 元数据，可将该目录加入 Emby 媒体库。',
              ),
              onChanged: _busy
                  ? null
                  : (value) async {
                      try {
                        if (value) {
                          await BackgroundDownloads.ensureStarted();
                        }
                        await widget.store.setAutoExport(value);
                      } catch (error) {
                        if (mounted) {
                          setState(() => _message = error.toString());
                        }
                      }
                    },
            ),
            SwitchListTile.adaptive(
              value: widget.store.exportPosters,
              title: const Text('同时导出海报文件'),
              subtitle: const Text('默认只写海报 URL。源站海报需要解密或外部读取失败时可开启。'),
              onChanged: _busy
                  ? null
                  : (value) => saveUserChange(
                      context,
                      () => widget.store.setExportPosters(value),
                    ),
            ),
            ListTile(
              leading: const Icon(Icons.backup_outlined),
              title: const Text('导出配置备份'),
              subtitle: const Text('包含本地用户、追剧、历史和设置，不含视频文件'),
              onTap: _busy ? null : () => _backup(false),
            ),
            ListTile(
              leading: const Icon(Icons.restore),
              title: const Text('恢复配置备份'),
              onTap: _busy ? null : () => _backup(true),
            ),
          ],
          if (Platform.isIOS)
            const Padding(
              padding: EdgeInsets.all(16),
              child: Text('iOS 下载和媒体处理需要保持应用在前台；切到后台会暂停，回到前台后可继续。'),
            ),
          if (_busy) const LinearProgressIndicator(),
          if (_message != null)
            Padding(
              padding: const EdgeInsets.all(16),
              child: SelectableText(_message!),
            ),
        ],
      ),
    ),
  );

  Future<void> _push(Widget page) =>
      Navigator.push(context, MaterialPageRoute<void>(builder: (_) => page));

  Future<void> _chooseGlassTheme() async {
    final selected = await showChoiceSheet<String>(
      context,
      title: '外观主题',
      choices: [
        for (final mode in ['system', 'light', 'dark'])
          SheetChoice(
            mode,
            AppTheme.label(mode),
            selected: widget.store.themeMode == mode,
            icon: switch (mode) {
              'light' => LucideIcons.sun,
              'dark' => LucideIcons.moon,
              _ => LucideIcons.sunMoon,
            },
          ),
      ],
    );
    if (selected != null && mounted) {
      await saveUserChange(context, () => widget.store.setThemeMode(selected));
    }
  }

  Future<void> _setAutoExport(bool value) async {
    try {
      if (value) await BackgroundDownloads.ensureStarted();
      await widget.store.setAutoExport(value);
    } catch (error) {
      if (mounted) setState(() => _message = error.toString());
    }
  }

  Widget _glassIcon(IconData icon, Color color) => DecoratedBox(
    decoration: BoxDecoration(
      color: color,
      borderRadius: BorderRadius.circular(8),
    ),
    child: SizedBox.square(
      dimension: 30,
      child: Icon(icon, size: 18, color: Colors.white),
    ),
  );

  Widget _glassSwitch({
    required bool value,
    required ValueChanged<bool>? onChanged,
  }) => iosDesign(context)
      ? CupertinoSwitch(value: value, onChanged: onChanged)
      : Switch.adaptive(value: value, onChanged: onChanged);

  Widget _glassSection(
    String? header,
    List<Widget> children, {
    String? footer,
  }) {
    final colors = Theme.of(context).colorScheme;
    final caption = TextStyle(
      fontSize: 13,
      letterSpacing: 0,
      color: colors.onSurfaceVariant,
    );
    if (iosDesign(context)) {
      return CupertinoListSection.insetGrouped(
        backgroundColor: Colors.transparent,
        decoration: BoxDecoration(
          color: colors.surface,
          borderRadius: BorderRadius.circular(18),
        ),
        separatorColor: colors.outlineVariant,
        header: header == null ? null : Text(header, style: caption),
        footer: footer == null ? null : Text(footer, style: caption),
        children: children,
      );
    }
    return Padding(
      padding: const EdgeInsets.only(bottom: 18),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (header != null)
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 4, 20, 8),
              child: Text(header, style: caption),
            ),
          ClipRRect(
            borderRadius: BorderRadius.circular(8),
            child: ColoredBox(
              color: colors.surface,
              child: Column(children: children),
            ),
          ),
          if (footer != null)
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 8, 20, 0),
              child: Text(footer, style: caption),
            ),
        ],
      ),
    );
  }

  Widget _glassRow({
    Key? key,
    required IconData icon,
    required Color color,
    required String title,
    String? detail,
    Widget? trailing,
    VoidCallback? onTap,
  }) {
    final colors = Theme.of(context).colorScheme;
    if (!iosDesign(context)) {
      return GlassTapTarget(
        key: key,
        onTap: onTap,
        borderRadius: BorderRadius.zero,
        child: SizedBox(
          height: 62,
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 14),
            child: Row(
              children: [
                _glassIcon(icon, color),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    mainAxisAlignment: MainAxisAlignment.center,
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        title,
                        style: Theme.of(context).textTheme.bodyLarge?.copyWith(
                          color: onTap == null && trailing == null
                              ? colors.onSurface.withValues(alpha: .4)
                              : colors.onSurface,
                        ),
                      ),
                      if (detail != null)
                        Text(
                          detail,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(color: colors.onSurfaceVariant),
                        ),
                    ],
                  ),
                ),
                if (trailing != null) trailing,
                if (trailing == null && onTap != null)
                  Icon(
                    LucideIcons.chevronRight,
                    size: 18,
                    color: colors.onSurfaceVariant,
                  ),
              ],
            ),
          ),
        ),
      );
    }
    return CupertinoListTile.notched(
      key: key,
      leading: _glassIcon(icon, color),
      title: Text(
        title,
        style: TextStyle(
          fontSize: 17,
          letterSpacing: 0,
          color: onTap == null && trailing == null
              ? colors.onSurface.withValues(alpha: .4)
              : colors.onSurface,
        ),
      ),
      additionalInfo: detail == null
          ? null
          : ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 150),
              child: Text(
                detail,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(color: colors.onSurfaceVariant),
              ),
            ),
      trailing:
          trailing ?? (onTap == null ? null : const CupertinoListTileChevron()),
      backgroundColorActivated: colors.surfaceContainerHigh,
      onTap: onTap,
    );
  }

  Widget _glassBody() {
    final store = widget.store;
    return ListView(
      padding: EdgeInsets.fromLTRB(
        0,
        4,
        0,
        32 + MediaQuery.paddingOf(context).bottom,
      ),
      children: [
        _glassSection('通用', [
          _glassRow(
            key: const ValueKey('lan-settings'),
            icon: LucideIcons.monitorSmartphone,
            color: const Color(0xFF0A84FF),
            title: '设备互联',
            detail: '同步与推送',
            onTap: () => openLanSync(context),
          ),
          if (widget.repository.supportsSourceManagement)
            _glassRow(
              icon: LucideIcons.server,
              color: const Color(0xFF30B0C7),
              title: '站源管理',
              onTap: () => _push(
                SourcesScreen(repository: widget.repository, store: store),
              ),
            ),
          _glassRow(
            key: const ValueKey('theme-setting'),
            icon: LucideIcons.palette,
            color: const Color(0xFFAF52DE),
            title: '外观主题',
            detail: AppTheme.label(store.themeMode),
            onTap: _chooseGlassTheme,
          ),
          _glassRow(
            icon: LucideIcons.users,
            color: const Color(0xFF34C759),
            title: '用户管理',
            detail: store.profile.name,
            onTap: () => _push(ProfilesScreen(store: store)),
          ),
        ]),
        if (store.canDownload)
          _glassSection(
            '下载',
            [
              _glassRow(
                icon: LucideIcons.arrowDownToLine,
                color: const Color(0xFFFF9F0A),
                title: '下载偏好',
                detail: store.downloadPreferences.qualityLabel,
                onTap: () => _push(DownloadPreferencesScreen(store: store)),
              ),
              _glassRow(
                icon: LucideIcons.hardDrive,
                color: const Color(0xFF8E8E93),
                title: '下载目录与空间',
                onTap: () => _push(
                  StorageScreen(repository: widget.repository, store: store),
                ),
              ),
            ],
            footer: Platform.isIOS
                ? 'iOS 下载和媒体处理需要保持应用在前台；切到后台会暂停，回到前台后可继续。'
                : null,
          ),
        if (store.profile.admin) ...[
          _glassSection('管理', [
            _glassRow(
              icon: LucideIcons.network,
              color: const Color(0xFF5E5CE6),
              title: '网络与资源',
              detail: '代理与并发',
              onTap: () => _push(
                ResourceSettingsScreen(
                  repository: widget.repository,
                  store: store,
                ),
              ),
            ),
            _glassRow(
              icon: LucideIcons.film,
              color: const Color(0xFF34C759),
              title: '完成后导出 Emby',
              trailing: _glassSwitch(
                value: store.autoExport,
                onChanged: _busy ? null : _setAutoExport,
              ),
            ),
            _glassRow(
              icon: LucideIcons.image,
              color: const Color(0xFFFF6482),
              title: '同时导出海报文件',
              trailing: _glassSwitch(
                value: store.exportPosters,
                onChanged: _busy
                    ? null
                    : (value) => saveUserChange(
                        context,
                        () => store.setExportPosters(value),
                      ),
              ),
            ),
          ], footer: '导出写入下载目录的 exports，可加入 Emby 媒体库；默认只写海报 URL。'),
          _glassSection('备份', [
            _glassRow(
              icon: LucideIcons.databaseBackup,
              color: const Color(0xFF0A84FF),
              title: '导出配置备份',
              onTap: _busy ? null : () => _backup(false),
            ),
            _glassRow(
              icon: LucideIcons.archiveRestore,
              color: const Color(0xFFFF9F0A),
              title: '恢复配置备份',
              onTap: _busy ? null : () => _backup(true),
            ),
          ], footer: '包含本地用户、追剧、历史和设置，不含视频文件。'),
        ],
        if (_busy)
          const Padding(
            padding: EdgeInsets.all(20),
            child: Center(child: CupertinoActivityIndicator()),
          ),
        if (_message != null)
          Padding(
            padding: const EdgeInsets.fromLTRB(32, 8, 32, 0),
            child: SelectableText(
              _message!,
              style: TextStyle(
                fontSize: 13,
                color: Theme.of(context).colorScheme.onSurfaceVariant,
              ),
            ),
          ),
      ],
    );
  }
}

class StorageScreen extends StatefulWidget {
  const StorageScreen({
    super.key,
    required this.repository,
    required this.store,
  });
  final AppRepository repository;
  final LocalStore store;
  @override
  State<StorageScreen> createState() => _StorageScreenState();
}

class _StorageScreenState extends State<StorageScreen> {
  Map<String, dynamic>? _info;
  String? _error;
  bool _busy = false;
  @override
  void initState() {
    super.initState();
    _refresh();
  }

  Future<void> _refresh() async {
    try {
      final info = await widget.repository.storage();
      if (mounted) {
        setState(() {
          _info = info;
          _error = null;
        });
      }
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    }
  }

  Future<void> _move() async {
    String? parent;
    if (Platform.isAndroid || Platform.isIOS) {
      final support = await getApplicationSupportDirectory();
      final directories = <String, String>{support.path: '应用内部存储'};
      if (Platform.isAndroid) {
        final external = await getExternalStorageDirectories() ?? [];
        for (var i = 0; i < external.length; i++) {
          directories[external[i].path] = i == 0
              ? '设备共享存储（应用目录）'
              : 'SD 卡 ${i + 1}（应用目录）';
        }
      } else {
        final documents = await getApplicationDocumentsDirectory();
        directories[documents.path] = '文件 App 可见目录';
      }
      if (!mounted) return;
      parent = await showDialog<String>(
        context: context,
        builder: (context) => SimpleDialog(
          title: const Text('选择下载位置'),
          children: [
            for (final entry in directories.entries)
              SimpleDialogOption(
                onPressed: () => Navigator.pop(context, entry.key),
                child: Text(entry.value),
              ),
          ],
        ),
      );
    } else {
      parent = await FilePicker.getDirectoryPath(dialogTitle: '选择下载保存位置');
    }
    if (parent == null || !mounted) return;
    final yes = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('迁移已下载内容？'),
        content: Text(
          '将视频、合并成品及导出内容迁移到：\n$parent\n\n下载会先暂停，复制成功后清理旧目录。请保证目标有足够空间，并在完成后继续下载。',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('迁移'),
          ),
        ],
      ),
    );
    if (yes != true || !mounted) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await widget.repository.moveDownloads(parent);
      await _refresh();
    } catch (error) {
      if (mounted) setState(() => _error = error.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => PopScope(
    canPop: !_busy,
    child: Scaffold(
      appBar: AppBar(title: const Text('下载目录与空间')),
      body: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 720),
          child: ListView(
            padding: const EdgeInsets.all(20),
            children: [
              if (_info != null) ...[
                Text(
                  '已使用 ${storageSize((_info!['bytes'] as num?)?.toInt() ?? 0)}',
                  style: Theme.of(context).textTheme.headlineSmall,
                ),
                const SizedBox(height: 8),
                Text(
                  '剩余 ${storageSize((_info!['free'] as num?)?.toInt() ?? -1)} · ${_info!['files'] ?? 0} 个文件',
                ),
                const SizedBox(height: 24),
                const Text('当前下载目录'),
                const SizedBox(height: 8),
                SelectableText(_info!['directory'] as String? ?? ''),
                TextButton.icon(
                  onPressed: () => Clipboard.setData(
                    ClipboardData(text: _info!['directory'] as String? ?? ''),
                  ),
                  icon: const Icon(Icons.copy),
                  label: const Text('复制路径'),
                ),
                const SizedBox(height: 20),
                const Text('在下载页删除不需要的分集，在本地媒体页删除合并成品或 Emby 导出，可释放空间。'),
              ],
              if (_busy || (_info == null && _error == null))
                const Padding(
                  padding: EdgeInsets.symmetric(vertical: 20),
                  child: LinearProgressIndicator(),
                ),
              if (_busy) const Text('正在迁移，请保持应用运行…'),
              if (_error != null)
                Padding(
                  padding: const EdgeInsets.symmetric(vertical: 16),
                  child: Text(_error!),
                ),
              if (widget.store.profile.admin)
                FilledButton.icon(
                  onPressed: _busy ? null : _move,
                  icon: const Icon(Icons.drive_file_move_outline),
                  label: const Text('更改并迁移目录'),
                ),
              TextButton(
                onPressed: _busy ? null : _refresh,
                child: const Text('刷新用量'),
              ),
            ],
          ),
        ),
      ),
    ),
  );
}
