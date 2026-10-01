import argparse
import os
import platform
import plistlib
import re
import shutil
import subprocess
import sys
from pathlib import Path

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


def apply_settings(config, settings):
    if not config.is_file():
        return
    text = config.read_text(encoding='utf-8')
    for key, value in settings.items():
        line = key + ' = ' + value
        pattern = re.compile(r'(?m)^' + key + r'\s*=.*$')
        # 追加在文件末尾，保证覆盖同文件里更早的 #include 带来的同名设置。
        text = pattern.sub('', text).rstrip() + '\n' + line + '\n'
    config.write_text(text, encoding='utf-8')


def configure_project():
    # CI 没有签名证书，必须关闭 Xcode 签名；flutter build macos 的 --no-codesign 曾加入又被回滚，
    # 不能依赖命令行开关，直接写构建设置最稳妥。
    settings = {
        'PRODUCT_NAME': variant.slug,
        'PRODUCT_BUNDLE_IDENTIFIER': 'com.duanju.' + variant.slug,
        'CODE_SIGNING_ALLOWED': 'NO',
        'CODE_SIGNING_REQUIRED': 'NO',
        'CODE_SIGN_IDENTITY': '-',
        'CODE_SIGN_STYLE': 'Manual',
        'ENABLE_HARDENED_RUNTIME': 'NO',
    }
    configs = root / 'macos' / 'Runner' / 'Configs'
    apply_settings(configs / 'AppInfo.xcconfig', settings)
    apply_settings(configs / 'Release.xcconfig', settings)


def build_arguments():
    help_text = subprocess.run([flutter, 'build', 'macos', '--help'],
                               capture_output=True, text=True, check=False).stdout
    return ['--no-codesign'] if '--no-codesign' in help_text else []


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
        # 关闭 Xcode 签名后产物是未签名的，Apple 芯片要求所有代码都有签名，
        # 这里按由内到外的顺序补 ad-hoc 签名，最后再签应用包本体。
        nested = sorted(path for path in frameworks.iterdir()
                        if path.suffix in ('.framework', '.dylib') or path.is_file())
        for item in nested:
            subprocess.run([codesign, '--force', '--sign', '-', '--timestamp=none', str(item)], check=False)
        for item in sorted(path for path in (application / 'Contents' / 'MacOS').iterdir() if path.is_file()):
            subprocess.run([codesign, '--force', '--sign', '-', '--timestamp=none', str(item)], check=False)
        subprocess.run([codesign, '--force', '--sign', '-', '--timestamp=none', str(application)], check=False)


with china_mirror_environment(environment, options.cn_mirrors, gradle=False) as env:
    with mirrored_pub_lockfile(root, env):
        ensure_platform(env)
        configure_project()
        run([sys.executable, str(root / 'scripts' / 'build_native.py'), '--platform', 'darwin',
             *variant.arguments], env=env)
        run([flutter, 'pub', 'get', '--enforce-lockfile'], env=env)
        build_env = dict(env, CODE_SIGNING_ALLOWED='NO', CODE_SIGNING_REQUIRED='NO', CODE_SIGN_IDENTITY='-')
        run([flutter, 'build', 'macos', '--release', '--no-pub', '--verbose',
             *build_arguments(), *variant.flutter_arguments], env=build_env)
        release = root / 'build' / 'macos' / 'Build' / 'Products' / 'Release'
        applications = sorted(path for path in release.glob('*.app') if path.is_dir()) if release.is_dir() else []
        if not applications:
            raise SystemExit('未生成 macOS 应用包：' + str(release))
        application = rename_application(applications[0])
        embed_library(application)
        subprocess.run([sys.executable, str(root / 'scripts' / 'package_release.py'), '--platform', 'macos',
                        *variant.arguments], cwd=root, env=env, check=True)
