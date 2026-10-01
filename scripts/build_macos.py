import argparse
import os
import platform
import plistlib
import re
import shutil
import subprocess
import sys

from build_mirrors import china_mirror_environment, mirrored_pub_lockfile
from app_build import BuildVariant, add_variant_argument

root = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description='构建短剧视界 / 全剧视界 macOS 应用')
parser.add_argument('--cn-mirrors', action='store_true', help='使用 Flutter 中国镜像')
add_variant_argument(parser)
options = parser.parse_args()
variant = BuildVariant(options.all_sources)

if platform.system() != 'Darwin':
    raise SystemExit('macOS 构建需要 macOS 与 Xcode Command Line Tools。')

environment = os.environ.copy()
environment.setdefault('GOPROXY', 'https://goproxy.cn,direct')
environment.setdefault('GOSUMDB', 'off')
flutter = shutil.which('flutter')
if not flutter:
    raise SystemExit('请先将 Flutter SDK 的 bin 目录加入 PATH。')


def run(arguments, **kwargs):
    subprocess.run(arguments, cwd=kwargs.pop('cwd', root), check=True, **kwargs)


def ensure_platform(env):
    if (root / 'macos' / 'Runner.xcodeproj').exists():
        return
    print('未找到 macOS 平台目录，先生成。', flush=True)
    run([flutter, 'create', '--platforms=macos', '--org', 'com.duanju', '.'], env=env)


def configure_product_name():
    config = root / 'macos' / 'Runner' / 'Configs' / 'AppInfo.xcconfig'
    if not config.is_file():
        return
    text = config.read_text(encoding='utf-8')
    name = 'PRODUCT_NAME = ' + variant.slug
    identifier = 'PRODUCT_BUNDLE_IDENTIFIER = com.duanju.' + variant.slug
    for key, value in (('PRODUCT_NAME', name), ('PRODUCT_BUNDLE_IDENTIFIER', identifier)):
        pattern = re.compile(r'(?m)^' + key + r'\s*=.*$')
        text = pattern.sub(value, text) if pattern.search(text) else text.rstrip() + '\n' + value + '\n'
    config.write_text(text, encoding='utf-8')


def rename_application(application):
    if application.name == variant.slug + '.app':
        return application
    target = application.with_name(variant.slug + '.app')
    if target.exists():
        shutil.rmtree(target)
    application.rename(target)
    macos = target / 'Contents' / 'MacOS'
    current = sorted(path for path in macos.iterdir() if path.is_file())
    if len(current) == 1 and current[0].name != variant.slug:
        current[0].rename(macos / variant.slug)
    info = target / 'Contents' / 'Info.plist'
    if info.is_file():
        data = plistlib.loads(info.read_bytes())
        data['CFBundleName'] = variant.slug
        data['CFBundleDisplayName'] = variant.name
        data['CFBundleExecutable'] = variant.slug
        info.write_bytes(plistlib.dumps(data))
    return target


def embed_library(application):
    source = root / 'native' / 'build' / 'darwin' / 'libduanju_core.dylib'
    if not source.is_file():
        raise SystemExit('缺少 macOS 原生库：' + str(source))
    frameworks = application / 'Contents' / 'Frameworks'
    frameworks.mkdir(parents=True, exist_ok=True)
    library = frameworks / source.name
    shutil.copy2(source, library)
    run(['install_name_tool', '-id', '@rpath/' + source.name, str(library)])
    for executable in sorted(path for path in (application / 'Contents' / 'MacOS').iterdir() if path.is_file()):
        listing = subprocess.run(['otool', '-l', str(executable)], capture_output=True, text=True, check=False).stdout
        if '@executable_path/Frameworks' not in listing:
            subprocess.run(['install_name_tool', '-add_rpath', '@executable_path/Frameworks',
                            str(executable)], check=False)
    symbols = subprocess.run(['nm', '-gU', str(library)], capture_output=True, text=True, check=True).stdout
    for symbol in ('_DuanjuRequest', '_DuanjuFree'):
        if symbol not in symbols:
            raise SystemExit('macOS 原生库缺少 FFI 入口：' + symbol)
    codesign = shutil.which('codesign')
    if codesign:
        subprocess.run([codesign, '--force', '--sign', '-', '--timestamp=none', str(library)], check=False)
        subprocess.run([codesign, '--force', '--sign', '-', '--timestamp=none', str(application)], check=False)


with china_mirror_environment(environment, options.cn_mirrors, gradle=False) as env:
    with mirrored_pub_lockfile(root, env):
        ensure_platform(env)
        configure_product_name()
        run([sys.executable, str(root / 'scripts' / 'build_native.py'), '--platform', 'darwin',
             *variant.arguments], env=env)
        run([flutter, 'pub', 'get', '--enforce-lockfile'], env=env)
        run([flutter, 'build', 'macos', '--release', '--no-pub', '--verbose',
             *variant.flutter_arguments], env=env)
        release = root / 'build' / 'macos' / 'Build' / 'Products' / 'Release'
        applications = sorted(path for path in release.glob('*.app') if path.is_dir()) if release.is_dir() else []
        if not applications:
            raise SystemExit('未生成 macOS 应用包：' + str(release))
        application = rename_application(applications[0])
        embed_library(application)
        subprocess.run([sys.executable, str(root / 'scripts' / 'package_release.py'), '--platform', 'macos',
                        *variant.arguments], cwd=root, env=env, check=True)
