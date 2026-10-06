import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';

import 'core_bridge.dart';
import 'local_store.dart';
import 'models.dart';
import 'remote_widgets.dart';
import 'widgets.dart';

/// 自定义站源管理界面。
///
/// 用户可新增、编辑、删除符合 MacCMS 模板的站点地址，或直接粘贴 XBPQ
/// 爬虫规则 JSON。保存后由原生核心识别并套用对应解析规则，完成列表展示、
/// 搜索与播放。支持把全部源导出为 JSON 文件、从 JSON/TXT 文件批量导入。
class CustomSourcesScreen extends StatefulWidget {
  const CustomSourcesScreen({
    super.key,
    required this.repository,
    required this.store,
  });
  final AppRepository repository;
  final LocalStore store;

  @override
  State<CustomSourcesScreen> createState() => _CustomSourcesScreenState();
}

class _CustomSourcesScreenState extends State<CustomSourcesScreen> {
  bool _loading = true;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    ensureTelevisionFocus(context);
    unawaited(_load());
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final sources = await widget.repository.customSources();
      await widget.store.syncCustomSources(sources);
      if (mounted) setState(() => _loading = false);
    } catch (error) {
      if (mounted) {
        setState(() {
          _loading = false;
          _error = error.toString();
        });
      }
    }
  }

  Future<void> _openEditor({CustomSourceSite? existing}) async {
    final result = await showDialog<bool>(
      context: context,
      builder: (_) => _CustomSourceEditor(
        repository: widget.repository,
        store: widget.store,
        existing: existing,
      ),
    );
    if (result == true && mounted) unawaited(_load());
  }

  Future<void> _export() async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      final content = await widget.repository.exportCustomSources();
      if (content.isEmpty) throw const FormatException('没有可导出的自定义源');
      final saved = await FilePicker.saveFile(
        fileName: 'duanju-custom-sources-'
            '${DateTime.now().toIso8601String().substring(0, 10)}.json',
        bytes: Uint8List.fromList(utf8.encode(content)),
        mimeType: 'application/json',
      );
      if (saved != null && mounted) {
        _showMessage('已导出到 $saved');
      }
    } catch (error) {
      if (mounted) _showMessage(error.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _import() async {
    if (_busy) return;
    final content = await showDialog<String>(
      context: context,
      builder: (_) => _CustomSourceImportDialog(),
    );
    if (content == null || content.trim().isEmpty || !mounted) return;
    await _runImport(content.trim());
  }

  Future<void> _runImport(String content) async {
    setState(() => _busy = true);
    try {
      final result = await widget.repository.importCustomSources(content);
      await widget.store.syncCustomSources(
        await widget.repository.customSources(),
      );
      if (!mounted) return;
      _showImportResult(result);
    } catch (error) {
      if (mounted) _showMessage(error.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _reset() async {
    if (_busy) return;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('恢复默认源'),
        content: const Text(
          '将删除全部自定义源（含 XBPQ 规则），仅保留内置源。'
          '此操作不可撤销，建议先「导出 JSON」备份。',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('确认恢复'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    setState(() => _busy = true);
    try {
      final removed = await widget.repository.resetCustomSources();
      await widget.store.syncCustomSources(
        await widget.repository.customSources(),
      );
      if (mounted) _showMessage('已删除 $removed 个自定义源，恢复为默认内置源');
    } catch (error) {
      if (mounted) _showMessage(error.toString());
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  void _showMessage(String message) {
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(SnackBar(content: Text(message)));
  }

  void _showImportResult(Map<String, dynamic> result) {
    final added = (result['added'] as List? ?? const []).length;
    final updated = (result['updated'] as List? ?? const []).length;
    final skipped = (result['skipped'] as List? ?? const [])
        .map((item) => item.toString())
        .toList();
    final failed = (result['failed'] as List? ?? const [])
        .map((item) => item.toString())
        .toList();
    final details = <String>[
      ...skipped.map((item) => '跳过：$item'),
      ...failed.map((item) => '失败：$item'),
    ];
    showDialog<void>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('导入完成'),
        content: SizedBox(
          width: 420,
          child: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('新增 $added 个，更新 $updated 个。'),
                if (details.isNotEmpty) ...[
                  const SizedBox(height: 8),
                  for (final line in details)
                    Padding(
                      padding: const EdgeInsets.only(bottom: 4),
                      child: Text(
                        line,
                        style: Theme.of(context).textTheme.bodySmall,
                      ),
                    ),
                ],
              ],
            ),
          ),
        ),
        actions: [
          FilledButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('知道了'),
          ),
        ],
      ),
    );
  }

  Future<void> _remove(CustomSourceSite source) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('删除自定义源'),
        content: Text('确定删除「${source.name}」吗？已加载的剧集仍会保留在设备上。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('删除'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    try {
      await saveUserChange(
        context,
        () async {
          await widget.repository.removeCustomSource(source.id);
          await widget.store.syncCustomSources(
            await widget.repository.customSources(),
          );
        },
      );
      if (mounted) unawaited(_load());
    } catch (_) {}
  }

  @override
  Widget build(BuildContext context) {
    final sources = widget.store.customSources;
    return Scaffold(
      appBar: AppBar(
        title: const Text('自定义源'),
        actions: [
          IconButton(
            key: const ValueKey('custom-source-refresh'),
            tooltip: '刷新',
            onPressed: _loading || _busy ? null : () => unawaited(_load()),
            icon: const Icon(Icons.refresh_rounded),
          ),
          IconButton(
            key: const ValueKey('custom-source-export'),
            tooltip: '导出 JSON',
            onPressed: _busy ? null : () => unawaited(_export()),
            icon: const Icon(Icons.upload_file_rounded),
          ),
          IconButton(
            key: const ValueKey('custom-source-import'),
            tooltip: '导入（文件或网络地址）',
            onPressed: _busy ? null : () => unawaited(_import()),
            icon: const Icon(Icons.download_rounded),
          ),
          IconButton(
            key: const ValueKey('custom-source-reset'),
            tooltip: '恢复默认源',
            onPressed: _busy ? null : () => unawaited(_reset()),
            icon: const Icon(Icons.restore_rounded),
          ),
          const SizedBox(width: 4),
          FilledButton.icon(
            key: const ValueKey('custom-source-add'),
            onPressed: _busy ? null : () => unawaited(_openEditor()),
            icon: const Icon(Icons.add_rounded, size: 20),
            label: const Text('新增'),
          ),
          const SizedBox(width: 12),
        ],
      ),
      body: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 960),
          child: AnimatedBuilder(
            animation: widget.store,
            builder: (context, _) {
              if (_error != null) {
                return StatusPanel(
                  title: '无法读取自定义源',
                  message: _error!,
                  onRetry: _load,
                );
              }
              if (_loading && sources.isEmpty) {
                return const Center(child: CircularProgressIndicator());
              }
              if (sources.isEmpty) {
                return const StatusPanel(
                  title: '还没有自定义源',
                  message: '点击「新增」填入 MacCMS 站点地址或粘贴 XBPQ 爬虫规则；也可以「导入文件」批量添加，「导出 JSON」备份迁移。',
                );
              }
              return ListView(
                padding: const EdgeInsets.fromLTRB(16, 16, 16, 24),
                children: [
                  const Padding(
                    padding: EdgeInsets.only(bottom: 16),
                    child: Text(
                      '自定义源读取网站地址并判断是否符合 MacCMS 规范；符合后套用通用规则自动完成列表展示、搜索与播放。录入 XBPQ 爬虫规则的站点按规则字段逐项截取。',
                    ),
                  ),
                  for (final source in sources) _sourceCard(source),
                ],
              );
            },
          ),
        ),
      ),
    );
  }

  Widget _sourceCard(CustomSourceSite source) {
    final colors = Theme.of(context).colorScheme;
    return Card(
      key: ValueKey('custom-${source.id}'),
      margin: const EdgeInsets.only(bottom: 12),
      child: ListTile(
        leading: Icon(
          source.hasRule ? Icons.code_rounded : Icons.dns_outlined,
        ),
        title: Text(
          source.name + (source.hasRule ? '（规则）' : ''),
        ),
        subtitle: Text(
          source.base,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: Theme.of(context).textTheme.bodySmall,
        ),
        trailing: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            IconButton(
              tooltip: '编辑',
              onPressed: () => unawaited(_openEditor(existing: source)),
              icon: const Icon(Icons.edit_outlined),
            ),
            IconButton(
              tooltip: '删除',
              onPressed: () => unawaited(_remove(source)),
              icon: Icon(Icons.delete_outline, color: colors.error),
            ),
          ],
        ),
      ),
    );
  }
}

class _CustomSourceEditor extends StatefulWidget {
  const _CustomSourceEditor({
    required this.repository,
    required this.store,
    this.existing,
  });
  final AppRepository repository;
  final LocalStore store;
  final CustomSourceSite? existing;

  @override
  State<_CustomSourceEditor> createState() => _CustomSourceEditorState();
}

class _CustomSourceEditorState extends State<_CustomSourceEditor> {
  late final TextEditingController _name;
  late final TextEditingController _base;
  late final TextEditingController _rule;
  bool _ruleMode = false;
  bool _busy = false;
  String? _errorFixed;

  @override
  void initState() {
    super.initState();
    _name = TextEditingController(text: widget.existing?.name ?? '');
    _base = TextEditingController(text: widget.existing?.base ?? '');
    _rule = TextEditingController(text: widget.existing?.rule ?? '');
    _ruleMode = widget.existing?.hasRule ?? false;
  }

  @override
  void dispose() {
    _name.dispose();
    _base.dispose();
    _rule.dispose();
    super.dispose();
  }

  void _toggleRuleMode() {
    setState(() => _ruleMode = !_ruleMode);
    _errorFixed = null;
  }

  Future<void> _save() async {
    if (_busy) return;
    final rule = _ruleMode ? _rule.text.trim() : '';
    if (_ruleMode && rule.isEmpty) {
      setState(() => _errorFixed = '粘贴规则 JSON 后再保存，或关闭规则开关');
      return;
    }
    if (!_ruleMode && _base.text.trim().isEmpty) {
      setState(() => _errorFixed = '请填写源网址');
      return;
    }
    setState(() {
      _busy = true;
      _errorFixed = null;
    });
    try {
      final existing = widget.existing;
      if (existing == null) {
        await widget.repository.addCustomSource(
          name: _name.text.trim(),
          base: _base.text.trim(),
          rule: rule,
        );
      } else {
        await widget.repository.updateCustomSource(
          existing.id,
          name: _name.text.trim(),
          base: _base.text.trim(),
          rule: rule,
        );
      }
      await widget.store.syncCustomSources(
        await widget.repository.customSources(),
      );
      if (mounted) Navigator.pop(context, true);
    } catch (error) {
      if (mounted) {
        setState(() {
          _busy = false;
          _errorFixed = error.toString();
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Text(widget.existing == null ? '新增自定义源' : '编辑自定义源'),
      content: SizedBox(
        width: 480,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              TextField(
                key: const ValueKey('custom-source-name'),
                controller: _name,
                enabled: !_busy,
                maxLength: 40,
                textInputAction: TextInputAction.next,
                decoration: InputDecoration(
                  labelText: '源名称',
                  hintText: _ruleMode ? '留空则用规则里的域名' : '例如：我的影院',
                ),
              ),
              const SizedBox(height: 12),
              TextField(
                key: const ValueKey('custom-source-base'),
                controller: _base,
                enabled: !_busy && !_ruleMode,
                keyboardType: TextInputType.url,
                autocorrect: false,
                decoration: InputDecoration(
                  labelText: '源网址',
                  hintText: 'https://example.com',
                  helperText: _ruleMode
                      ? '规则模式下自动取规则「主页url」'
                      : '支持符合 MacCMS 规范的站点，自动适配目录、搜索与播放。',
                  helperMaxLines: 3,
                ),
              ),
              SwitchListTile(
                key: const ValueKey('custom-source-rule-toggle'),
                contentPadding: EdgeInsets.zero,
                title: const Text('XBPQ 爬虫规则'),
                subtitle: const Text('粘贴 JSON 规则，按规则字段截取内容'),
                value: _ruleMode,
                onChanged: _busy ? null : (_) => _toggleRuleMode(),
              ),
              if (_ruleMode)
                Padding(
                  padding: const EdgeInsets.only(top: 4),
                  child: TextField(
                    key: const ValueKey('custom-source-rule'),
                    controller: _rule,
                    enabled: !_busy,
                    maxLines: 8,
                    minLines: 4,
                    keyboardType: TextInputType.multiline,
                    autocorrect: false,
                    style: Theme.of(context).textTheme.bodySmall?.copyWith(
                          fontFamily: 'monospace',
                        ),
                    decoration: const InputDecoration(
                      border: OutlineInputBorder(),
                      hintText:
                          '{"主页url":"https://…","分类url":"…{cateId}-{catePg}…","数组":"…&&…","标题":"…","链接":"…"}',
                      helperText: '兼容截取语法（A&&B）与 p: 选择器；保存时校验规则有效性。',
                      helperMaxLines: 2,
                    ),
                  ),
                ),
              if (_errorFixed != null)
                Padding(
                  padding: const EdgeInsets.only(top: 12),
                  child: Text(
                    _errorFixed!,
                    style: TextStyle(
                      color: Theme.of(context).colorScheme.error,
                    ),
                  ),
                ),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: _busy ? null : () => Navigator.pop(context, false),
          child: const Text('取消'),
        ),
        FilledButton(
          key: const ValueKey('custom-source-save'),
          onPressed: _busy ? null : _save,
          child: Text(_busy ? '保存中…' : '保存'),
        ),
      ],
    );
  }
}

/// 导入方式选择弹窗：本地文件（JSON/TXT）或远程网络配置地址。
class _CustomSourceImportDialog extends StatefulWidget {
  const _CustomSourceImportDialog();

  @override
  State<_CustomSourceImportDialog> createState() =>
      _CustomSourceImportDialogState();
}

class _CustomSourceImportDialogState extends State<_CustomSourceImportDialog> {
  final TextEditingController _url = TextEditingController();
  bool _fileMode = true;

  @override
  void dispose() {
    _url.dispose();
    super.dispose();
  }

  Future<void> _pickFile() async {
    final file = await FilePicker.pickFile(
      type: FileType.custom,
      allowedExtensions: ['json', 'txt'],
    );
    if (file == null || !mounted) return;
    final size = await file.length();
    if (size == null || size > 8 * 1024 * 1024) {
      ScaffoldMessenger.of(context)
        ..hideCurrentSnackBar()
        ..showSnackBar(const SnackBar(content: Text('导入文件过大或无法读取')));
      return;
    }
    try {
      final content = utf8.decode(await file.readAsBytes());
      if (mounted) Navigator.pop(context, content);
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context)
          ..hideCurrentSnackBar()
          ..showSnackBar(
            const SnackBar(content: Text('导入文件不是有效的 UTF-8 文本')),
          );
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('导入自定义源'),
      content: SizedBox(
        width: 460,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SwitchListTile(
              contentPadding: EdgeInsets.zero,
              title: const Text('从网络地址导入'),
              subtitle: const Text(
                '粘贴 TVBox 配置 / JSON / TXT 地址，核心自动抓取解析',
                style: TextStyle(fontSize: 12),
              ),
              value: !_fileMode,
              onChanged: (value) => setState(() => _fileMode = !value),
            ),
            if (_fileMode)
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    const Text('选择本地 JSON / TXT 文件导入。'),
                    const SizedBox(height: 12),
                    FilledButton.tonalIcon(
                      key: const ValueKey('custom-source-import-file'),
                      onPressed: () => unawaited(_pickFile()),
                      icon: const Icon(Icons.folder_open_rounded, size: 20),
                      label: const Text('选择文件'),
                    ),
                  ],
                ),
              )
            else
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: TextField(
                  key: const ValueKey('custom-source-import-url'),
                  controller: _url,
                  autofocus: true,
                  maxLines: 3,
                  decoration: const InputDecoration(
                    labelText: '配置地址',
                    hintText: 'https://…/config.json',
                    border: OutlineInputBorder(),
                  ),
                ),
              ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
        if (!_fileMode)
          FilledButton(
            key: const ValueKey('custom-source-import-submit'),
            onPressed: () {
              final url = _url.text.trim();
              if (url.isEmpty) return;
              Navigator.pop(context, url);
            },
            child: const Text('导入'),
          ),
      ],
    );
  }
}