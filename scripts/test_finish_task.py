import tempfile
import unittest
from pathlib import Path

from finish_task import finish_task, git
from sync_source import REQUIRED_FILES, synchronize


class TaskSnapshotTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix='duanju-snapshot-test-')
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.source = self.root / '源码'
        self.destination = self.root / 'guoapp'
        for name in REQUIRED_FILES:
            self.write(self.source / name, name)
        self.write(self.source / 'pubspec.yaml', 'name: synthetic_app\nversion: 0.1.3+4\n')
        self.destination.mkdir()
        git(self.destination, 'init', '--initial-branch=main')
        git(self.destination, 'config', 'user.name', 'Snapshot Test')
        git(self.destination, 'config', 'user.email', 'snapshot@example.test')

    def write(self, path, content):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding='utf-8')

    def test_clean_source_snapshot_has_annotated_tag_and_no_build_dependencies(self):
        self.write(self.source / 'lib/电视.dart', '电视界面')
        self.write(self.source / 'build/app.apk', 'compiled binary')
        self.write(self.source / 'android/key.properties', 'private settings')
        result = finish_task(self.source, self.destination, '完成电视界面')
        self.assertEqual(result.tag, 'v0.1.3')
        self.assertTrue(result.created_commit)
        self.assertEqual(git(self.destination, 'cat-file', '-t', result.tag).stdout.strip(), 'tag')
        self.assertEqual(git(self.destination, 'rev-parse', result.tag + '^{commit}').stdout.strip(), result.commit)
        self.assertEqual(git(self.destination, 'show', result.tag + ':lib/电视.dart').stdout, '电视界面')
        self.assertFalse((self.destination / 'build').exists())
        self.assertFalse((self.destination / 'android/key.properties').exists())
        self.assertFalse(git(self.destination, 'status', '--porcelain').stdout.strip())
        self.assertFalse(synchronize(self.source, self.destination, check=True).changed)

    def test_old_tag_restores_exact_source_including_deleted_files(self):
        self.write(self.source / 'lib/old.dart', 'old behavior')
        first = finish_task(self.source, self.destination, '第一版')
        self.write(self.source / 'lib/main.dart', 'next behavior')
        self.write(self.source / 'pubspec.yaml', 'name: synthetic_app\nversion: 0.1.4+5\n')
        (self.source / 'lib/old.dart').unlink()
        second = finish_task(self.source, self.destination, '第二版')
        self.assertEqual(second.tag, 'v0.1.4')
        self.assertNotEqual(first.commit, second.commit)
        self.assertEqual(git(self.destination, 'show', first.tag + ':lib/main.dart').stdout, 'lib/main.dart')
        self.assertEqual(git(self.destination, 'show', first.tag + ':lib/old.dart').stdout, 'old behavior')
        self.assertNotIn('lib/old.dart', git(self.destination, 'ls-tree', '-r', '--name-only', second.tag).stdout)
        self.assertEqual(git(self.destination, 'show', second.tag + ':lib/main.dart').stdout, 'next behavior')

    def test_repeated_version_does_not_move_tag_and_unchanged_snapshot_is_reused(self):
        first = finish_task(self.source, self.destination, '第一版')
        reused = finish_task(self.source, self.destination, '核对第一版')
        self.assertFalse(reused.created_commit)
        self.assertEqual(reused.tag, first.tag)
        self.write(self.source / 'lib/main.dart', 'fixed behavior')
        fixed = finish_task(self.source, self.destination, '同版本修复')
        self.assertTrue(fixed.tag.startswith('v0.1.3-task-'))
        self.assertEqual(git(self.destination, 'rev-parse', first.tag + '^{commit}').stdout.strip(), first.commit)
        self.assertEqual(git(self.destination, 'rev-parse', fixed.tag + '^{commit}').stdout.strip(), fixed.commit)

    def test_existing_explicit_tag_and_nested_repository_are_rejected_before_sync(self):
        snapshot = finish_task(self.source, self.destination, '完成')
        before = (self.destination / 'lib/main.dart').read_bytes()
        self.write(self.source / 'lib/main.dart', 'not copied')
        with self.assertRaisesRegex(ValueError, 'tag 已存在'):
            finish_task(self.source, self.destination, '重复 tag', tag=snapshot.tag)
        self.assertEqual((self.destination / 'lib/main.dart').read_bytes(), before)
        nested = self.destination / 'nested'
        nested.mkdir()
        self.write(nested / 'preserved.txt', 'keep')
        with self.assertRaisesRegex(ValueError, '独立 Git 仓库'):
            finish_task(self.source, nested, '错误路径')
        self.assertEqual((nested / 'preserved.txt').read_text(), 'keep')

    def test_working_in_published_repository_keeps_local_build_files_untracked(self):
        finish_task(self.source, self.destination, '第一版')
        self.write(self.destination / 'build/app.apk', 'local build')
        self.write(self.destination / 'lib/main.dart', 'edited in guoapp')
        result = finish_task(self.destination, self.destination, '直接开发')
        self.assertTrue(result.created_commit)
        self.assertEqual((self.destination / 'build/app.apk').read_text(), 'local build')
        tracked = git(self.destination, 'ls-tree', '-r', '--name-only', result.tag).stdout
        self.assertNotIn('build/app.apk', tracked)


if __name__ == '__main__':
    unittest.main()
