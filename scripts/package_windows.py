import os
import shutil
import subprocess
import zipfile
from pathlib import Path


def find_inno_compiler():
    compiler = shutil.which('ISCC.exe')
    if compiler:
        return Path(compiler)
    for variable in ('ProgramFiles(x86)', 'ProgramFiles', 'LOCALAPPDATA'):
        directory = os.environ.get(variable)
        if not directory:
            continue
        base = Path(directory)
        if variable == 'LOCALAPPDATA':
            base /= 'Programs'
        candidate = base / 'Inno Setup 6' / 'ISCC.exe'
        if candidate.is_file():
            return candidate
    raise ValueError('缺少 Inno Setup 6，请安装并将 ISCC.exe 所在目录加入 PATH。')


def package_windows(root, variant, version, output):
    bundle = root / 'build' / 'windows' / 'x64' / 'runner' / 'Release'
    required = ['duanjushijie.exe', 'duanju_core.dll', 'flutter_windows.dll',
                'libffmpegkit.dll', 'libmpv-2.dll', 'msvcp140.dll',
                'vcruntime140.dll', 'data/icudtl.dat', 'data/app.so']
    missing = [name for name in required if not (bundle / name).is_file()]
    if not (bundle / 'data' / 'flutter_assets').is_dir():
        missing.append('data/flutter_assets')
    if missing:
        raise ValueError('Windows 安装包缺少文件：' + ', '.join(missing))
    compiler = find_inno_compiler()
    output.mkdir(parents=True, exist_ok=True)
    prefix = f'{variant.slug}-{version}-windows-x64'
    installer = output / f'{prefix}-setup.exe'
    installer.unlink(missing_ok=True)
    subprocess.run([
        str(compiler), '/Qp',
        '/DAppName=' + variant.name,
        '/DAppSlug=' + variant.slug,
        '/DAppVersion=' + version,
        '/DBundleDir=' + str(bundle),
        '/O' + str(output),
        '/F' + installer.stem,
        str(root / 'windows' / 'installer.iss'),
    ], cwd=root, check=True)
    if not installer.is_file() or installer.stat().st_size == 0:
        raise ValueError('Inno Setup 未生成有效的 Windows 安装程序。')
    portable = output / f'{prefix}-portable.zip'
    with zipfile.ZipFile(portable, 'w', zipfile.ZIP_DEFLATED) as archive:
        for source in sorted(bundle.rglob('*')):
            if source.is_file():
                relative = source.relative_to(bundle).as_posix()
                if relative == 'duanjushijie.exe':
                    relative = variant.slug + '.exe'
                archive.write(source, relative)
    return [installer, portable]
