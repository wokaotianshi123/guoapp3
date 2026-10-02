import subprocess
import unittest
import zipfile
from pathlib import Path
from unittest import mock

from _testkit import make_temp_dir, remove_tree
from app_build import BuildVariant
from package_windows import find_inno_compiler, package_windows


class WindowsPackageTests(unittest.TestCase):
    def setUp(self):
        self.root = Path(make_temp_dir(prefix='windows-package-')) / 'synthetic project'
        self.addCleanup(remove_tree, self.root.parent)
        self.bundle = self.root / 'build/windows/x64/runner/Release'
        self.output = self.root / 'dist/windows'
        self.files = {
            name: ('synthetic ' + name).encode()
            for name in ['duanjushijie.exe', 'duanju_core.dll', 'flutter_windows.dll',
                         'libffmpegkit.dll', 'libmpv-2.dll', 'msvcp140.dll',
                         'vcruntime140.dll', 'vcruntime140_1.dll',
                         'data/icudtl.dat', 'data/app.so',
                         'data/flutter_assets/assets/fixture.txt']
        }
        for name, content in self.files.items():
            target = self.bundle / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(content)

    def test_both_editions_preserve_bundle_and_create_distinct_packages(self):
        for enabled in (False, True):
            with self.subTest(all_sources=enabled):
                variant = BuildVariant(enabled)
                prefix = f'{variant.slug}-0.2.61+72-windows-x64'
                installer = self.output / (prefix + '-setup.exe')

                def compile_installer(command, **kwargs):
                    self.assertIn('/DAppName=' + variant.name, command)
                    self.assertIn('/DAppSlug=' + variant.slug, command)
                    self.assertIn('/DAppVersion=0.2.61+72', command)
                    self.assertIn('/DBundleDir=' + str(self.bundle), command)
                    self.assertIn('/F' + installer.stem, command)
                    self.assertTrue(kwargs['check'])
                    installer.write_bytes(b'synthetic installer')

                with mock.patch('package_windows.find_inno_compiler', return_value=Path('ISCC.exe')), \
                        mock.patch('package_windows.subprocess.run', side_effect=compile_installer):
                    artifacts = package_windows(self.root, variant, '0.2.61+72', self.output)
                self.assertEqual(artifacts, [installer, self.output / (prefix + '-portable.zip')])
                with zipfile.ZipFile(artifacts[1]) as archive:
                    expected = dict(self.files)
                    expected[variant.slug + '.exe'] = expected.pop('duanjushijie.exe')
                    self.assertEqual(set(archive.namelist()), set(expected))
                    self.assertIsNone(archive.testzip())
                    for name, content in expected.items():
                        self.assertEqual(archive.read(name), content)
                self.assertEqual((self.bundle / 'duanjushijie.exe').read_bytes(), self.files['duanjushijie.exe'])

    def test_incomplete_bundle_stops_before_compilation(self):
        (self.bundle / 'vcruntime140.dll').unlink()
        with mock.patch('package_windows.find_inno_compiler') as compiler:
            with self.assertRaisesRegex(ValueError, 'vcruntime140.dll'):
                package_windows(self.root, BuildVariant(), '0.2.61+72', self.output)
        compiler.assert_not_called()
        self.assertFalse(self.output.exists())

    def test_missing_compiler_has_actionable_error(self):
        with mock.patch('package_windows.shutil.which', return_value=None), \
                mock.patch.dict('os.environ', {}, clear=True):
            with self.assertRaisesRegex(ValueError, 'Inno Setup 6'):
                find_inno_compiler()

    def test_compiler_failure_does_not_succeed_with_stale_installer(self):
        self.output.mkdir(parents=True)
        stale = self.output / 'duanjushijie-0.2.61+72-windows-x64-setup.exe'
        stale.write_bytes(b'old installer')
        with mock.patch('package_windows.find_inno_compiler', return_value=Path('ISCC.exe')), \
                mock.patch('package_windows.subprocess.run',
                           side_effect=subprocess.CalledProcessError(1, 'ISCC.exe')):
            with self.assertRaises(subprocess.CalledProcessError):
                package_windows(self.root, BuildVariant(), '0.2.61+72', self.output)
        self.assertFalse(stale.exists())
        self.assertFalse(list(self.output.glob('*.zip')))

    def test_missing_or_empty_installer_is_rejected(self):
        for empty in (False, True):
            with self.subTest(empty=empty):
                def compile_installer(*args, **kwargs):
                    if empty:
                        (self.output / 'duanjushijie-0.2.61+72-windows-x64-setup.exe').touch()

                with mock.patch('package_windows.find_inno_compiler', return_value=Path('ISCC.exe')), \
                        mock.patch('package_windows.subprocess.run', side_effect=compile_installer):
                    with self.assertRaisesRegex(ValueError, '未生成有效'):
                        package_windows(self.root, BuildVariant(), '0.2.61+72', self.output)
                self.assertFalse(list(self.output.glob('*.zip')))


if __name__ == '__main__':
    unittest.main()
