import argparse
import hashlib
import os
import platform
import re
import shutil
import subprocess
import zipfile
from pathlib import Path

from app_build import BuildVariant

root = Path(__file__).resolve().parents[1]
native = root / 'native'
parser = argparse.ArgumentParser(description='构建短剧视界 / 全剧视界本地服务版')
parser.add_argument('--edition', action='append', choices=['duanjushijie', 'quanjushijie'],
                    help='只构建指定版本；默认两个版本都构建')
options = parser.parse_args()

version = re.search(r'^version:\s*(\S+)', (root / 'pubspec.yaml').read_text(), re.MULTILINE).group(1)
editions = options.edition or ['duanjushijie', 'quanjushijie']

environment = os.environ.copy()
environment.setdefault('GOPROXY', 'https://goproxy.cn,direct')
environment.setdefault('GOSUMDB', 'off')
environment['CGO_ENABLED'] = '0'
go = shutil.which('go')
if not go:
    raise SystemExit('请先安装 Go 1.25.0 或更新版本。')
bootstrap = environment.copy()
bootstrap['GOSUMDB'] = os.environ.get('GOSUMDB', 'sum.golang.org')
toolchain = subprocess.check_output([go, 'env', 'GOROOT'], cwd=native, env=bootstrap, text=True).strip()
go = str(Path(toolchain) / 'bin' / ('go.exe' if platform.system() == 'Windows' else 'go'))

targets = [
    ('windows', 'amd64'),
    ('linux', 'amd64'),
    ('darwin', 'amd64'),
    ('darwin', 'arm64'),
]


def binary_name(slug, goos, goarch):
    if goos == 'windows':
        return slug + '-windows.exe'
    if goos == 'linux':
        return slug + '-linux'
    return slug + '-macos-arm' if goarch == 'arm64' else slug + '-macos-intel'


def build_binary(slug, all_sources, goos, goarch):
    name = binary_name(slug, goos, goarch)
    destination = root / 'build' / 'web' / name
    destination.parent.mkdir(parents=True, exist_ok=True)
    flags = '-s -w -X duanjuapp/native/core.buildAllSources=' + str(all_sources).lower() + \
        ' -X main.editionSlug=' + slug + ' -X main.appVersion=' + version
    build_environment = dict(environment, GOOS=goos, GOARCH=goarch)
    print('Building ' + name, flush=True)
    subprocess.run([go, 'build', '-trimpath', '-ldflags=' + flags, '-o', str(destination), './server'],
                   cwd=native, env=build_environment, check=True)
    return destination, name


def start_script(slug):
    return '\n'.join([
        '#!/bin/sh',
        'cd "$(dirname "$0")"',
        'case "$(uname -s)" in',
        '  Darwin)',
        '    if [ "$(uname -m)" = "arm64" ]; then',
        '      BIN="./' + slug + '-macos-arm"',
        '    else',
        '      BIN="./' + slug + '-macos-intel"',
        '    fi',
        '    ;;',
        '  *) BIN="./' + slug + '-linux" ;;',
        'esac',
        'chmod +x "$BIN"',
        'exec "$BIN" -open',
        '',
    ])


def windows_script(slug):
    return '\r\n'.join([
        '@echo off',
        'cd /d "%~dp0"',
        '"' + slug + '-windows.exe" -open',
        'pause',
        '',
    ])


def readme(name, slug):
    return '\n'.join([
        name + ' 本地服务版 ' + version,
        '',
        '使用方法：',
        '  Windows：双击 start.bat',
        '  macOS / Linux：终端执行 sh start.sh',
        '',
        '服务启动后浏览器打开：',
        '  http://127.0.0.1:8000/web/',
        '  同一局域网的其他设备打开：http://<本机局域网 IP>:8000/web/',
        '  启动时会打印所有可用地址；换端口运行：start.sh 后加 -port 8001。',
        '',
        '说明：',
        '  数据保存在本目录下的 ' + slug + '-data，直接删除即可清空缓存。',
        '  关闭终端窗口或按 Ctrl+C 即停止服务。',
        '',
        '关于 ffmpeg（可选，但强烈建议）：',
        '  红果等站源的视频是加密的 H.265，浏览器既不能解密也不能解码，',
        '  必须由服务端调用 ffmpeg 解密并转码成 H.264 才能播放。',
        '  安装后放在本目录，或加入 PATH，重启服务即可自动启用：',
        '    Windows：winget install ffmpeg',
        '    macOS：brew install ffmpeg',
        '    Linux：apt install ffmpeg / yum install ffmpeg',
        '  未安装时其余站源仍可正常播放；启动信息里会写明是否检测到 ffmpeg。',
        '',
    ])


def package_edition(slug):
    variant = BuildVariant(slug == 'quanjushijie')
    output = root / 'dist' / 'web'
    output.mkdir(parents=True, exist_ok=True)
    staging = root / 'build' / 'web' / slug
    if staging.exists():
        shutil.rmtree(staging)
    staging.mkdir(parents=True)
    for goos, goarch in targets:
        source, name = build_binary(slug, variant.all_sources, goos, goarch)
        target = staging / name
        shutil.copy2(source, target)
        target.chmod(0o755)
    (staging / 'start.sh').write_text(start_script(slug), encoding='utf-8')
    (staging / 'start.sh').chmod(0o755)
    (staging / 'start.bat').write_text(windows_script(slug), encoding='ascii')
    (staging / 'README.txt').write_text(readme(variant.name, slug), encoding='utf-8')
    archive = output / (slug + '.zip')
    archive.unlink(missing_ok=True)
    with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED) as bundle:
        for item in sorted(staging.rglob('*')):
            if item.is_file():
                bundle.write(item, slug + '/' + item.relative_to(staging).as_posix())
    print(archive)
    return archive


archives = [package_edition(slug) for slug in editions]
output = root / 'dist' / 'web'
bundle_path = output / 'guoappweb.zip'
bundle_path.unlink(missing_ok=True)
with zipfile.ZipFile(bundle_path, 'w', zipfile.ZIP_DEFLATED) as bundle:
    checksums = []
    for archive in archives:
        bundle.write(archive, archive.name)
        digest = hashlib.sha256()
        with archive.open('rb') as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b''):
                digest.update(chunk)
        checksums.append(f'{digest.hexdigest()}  {archive.name}')
    bundle.writestr('SHA256SUMS.txt', '\n'.join(checksums) + '\n')
print(bundle_path)
