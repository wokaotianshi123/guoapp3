import base64
import os
from pathlib import Path
import plistlib
import runpy
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

from app_build import BuildVariant
from configure_ios_branding import configure


def dart_defines(*values):
    return ','.join(base64.b64encode(value.encode()).decode() for value in values)


class AppBuildTests(unittest.TestCase):
    def test_omitted_or_false_flag_keeps_hongguo_only(self):
        for encoded in ['', dart_defines('ALL_SOURCES=false'), dart_defines('OTHER=true')]:
            variant = BuildVariant.from_dart_defines(encoded)
            self.assertFalse(variant.all_sources)
            self.assertEqual(variant.name, '短剧视界')
            self.assertEqual(variant.slug, 'duanjushijie')

    def test_full_edition_decodes_among_other_flutter_defines(self):
        variant = BuildVariant.from_dart_defines(dart_defines(
            'OTHER=中文', 'ALL_SOURCES=true', 'VALUE=a=b'))
        self.assertTrue(variant.all_sources)
        self.assertEqual(variant.name, '全剧视界')
        self.assertEqual(variant.slug, 'quanjushijie')

    def test_ios_branding_can_switch_editions_without_replacing_bundle_identity(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / 'Info.plist'
            original = {'CFBundleIdentifier': 'com.duanju.duanjuApp', 'CFBundleVersion': '9'}
            path.write_bytes(plistlib.dumps(original))
            for enabled in [True, False]:
                configure(path, dart_defines('ALL_SOURCES=' + str(enabled).lower()))
                actual = plistlib.loads(path.read_bytes())
                self.assertEqual(actual['CFBundleDisplayName'], '全剧视界' if enabled else '短剧视界')
                self.assertEqual(actual['CFBundleName'], 'quanjushijie' if enabled else 'duanjushijie')
                for key, value in original.items():
                    self.assertEqual(actual[key], value)

    @unittest.skipUnless(shutil.which('cmake'), 'CMake is unavailable')
    def test_windows_reads_defines_from_flutter_tool_environment(self):
        branding = Path(__file__).resolve().parents[1] / 'windows/runner/app_branding.cmake'
        for flags, expected in [
            ([], '短剧视界'),
            (['ALL_SOURCES=true'], '全剧视界'),
            (['OTHER=true', 'ALL_SOURCES=false'], '短剧视界'),
            (['ALL_SOURCES=true', 'ALL_SOURCES=false'], '短剧视界'),
        ]:
            with self.subTest(flags=flags), tempfile.TemporaryDirectory() as temporary:
                script = Path(temporary) / 'check.cmake'
                result = Path(temporary) / 'name.txt'
                script.write_text(
                    'list(APPEND FLUTTER_TOOL_ENVIRONMENT "OTHER=1" "DART_DEFINES=' + dart_defines(*flags) + '")\n'
                    'include("' + branding.as_posix() + '")\n'
                    'file(WRITE "' + result.as_posix() + '" "${APP_DISPLAY_NAME}")\n',
                    encoding='utf-8',
                )
                subprocess.run(['cmake', '-P', str(script)], check=True, capture_output=True)
                self.assertEqual(result.read_text(encoding='utf-8'), expected)

    def test_android_and_windows_propagate_one_edition_to_core_flutter_and_package(self):
        root = Path(__file__).resolve().parent
        for target in ['android', 'windows']:
            for enabled in [False, True]:
                with self.subTest(target=target, all_sources=enabled):
                    script = root / f'build_{target}.py'
                    arguments = [str(script)] + (['--all-sources'] if enabled else [])
                    with mock.patch.object(sys, 'argv', arguments), \
                            mock.patch.dict(os.environ, {'PATH': '/tools'}, clear=True), \
                            mock.patch('platform.system', return_value='Windows' if target == 'windows' else 'Linux'), \
                            mock.patch('shutil.which', return_value='/tools/flutter'), \
                            mock.patch('android_build_retry.run_android_build') as android_build, \
                            mock.patch('subprocess.run') as run:
                        runpy.run_path(str(script), run_name='__main__')
                    calls = [call.args[0] for call in run.call_args_list]
                    calls += [call.args[0] for call in android_build.call_args_list]
                    native = next(call for call in calls if any(str(arg).endswith('build_native.py') for arg in call))
                    flutter = next(call for call in calls if 'build' in call)
                    package = next(call for call in calls if any(str(arg).endswith('package_release.py') for arg in call))
                    self.assertEqual('--all-sources' in native, enabled)
                    self.assertEqual('--all-sources' in package, enabled)
                    self.assertIn('--dart-define=ALL_SOURCES=' + str(enabled).lower(), flutter)
                    if target == 'android':
                        android_build.assert_called_once()
                        self.assertEqual(android_build.call_args.kwargs['edition'], BuildVariant(enabled).slug)
                    else:
                        android_build.assert_not_called()
                    self.assertIn('core.buildAllSources=' + str(enabled).lower(), BuildVariant(enabled).linker_flags)

    def test_android_resources_do_not_define_duplicate_names(self):
        resources = Path(__file__).resolve().parents[1] / 'android/app/src/main/res'
        owners = {}
        for folder in sorted(path for path in resources.iterdir() if path.is_dir()):
            for entry in sorted(path for path in folder.iterdir() if path.is_file()):
                identifier = f'{folder.name}/{entry.stem}'
                self.assertNotIn(
                    identifier, owners,
                    f'{identifier} 同时来自 {owners.get(identifier)} 和 {entry.name}，会造成资源重复',
                )
                owners[identifier] = entry.name


if __name__ == '__main__':
    unittest.main()
