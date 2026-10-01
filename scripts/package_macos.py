import os
import shutil
import subprocess
import zipfile
from pathlib import Path


def package_macos(root, variant, version, output):
    bundle = root / 'build' / 'macos' / 'Build' / 'Products' / 'Release'
    application = bundle / (variant.slug + '.app')
    if not application.is_dir():
        raise ValueError('macOS 应用不存在：' + str(application))
    macos = application / 'Contents' / 'MacOS'
    executables = sorted(path.name for path in macos.iterdir()) if macos.is_dir() else []
    if variant.slug not in executables:
        raise ValueError('macOS 应用缺少主程序：' + str(macos / variant.slug))
    frameworks = application / 'Contents' / 'Frameworks'
    library = frameworks / 'libduanju_core.dylib'
    missing = [name for name in ('libduanju_core.dylib',) if not (frameworks / name).is_file()]
    if not (frameworks / 'FlutterMacOS.framework').is_dir():
        missing.append('FlutterMacOS.framework')
    if not any(path.is_dir() for path in application.rglob('flutter_assets')):
        missing.append('flutter_assets')
    if missing:
        raise ValueError('macOS 应用缺少文件：' + ', '.join(missing))
    listing = subprocess.run(['file', str(library)], capture_output=True, text=True, check=False).stdout
    if 'universal' not in listing and 'Mach-O' not in listing:
        raise ValueError('macOS 原生库格式异常：' + str(library))
    output.mkdir(parents=True, exist_ok=True)
    prefix = f'{variant.slug}-{version}-macos'
    archive = output / f'{prefix}.zip'
    archive.unlink(missing_ok=True)
    ditto = shutil.which('ditto')
    if ditto:
        subprocess.run([ditto, '-c', '-k', '--sequesterRsrc', '--keepParent',
                        application.name, str(archive)], cwd=bundle, check=True)
    else:
        _zip_application(application, archive)
    if not archive.is_file() or archive.stat().st_size == 0:
        raise ValueError('macOS 压缩包无效：' + str(archive))
    return [archive]


def _zip_application(application, archive):
    with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED) as bundle:
        for source in sorted(application.rglob('*')):
            name = application.name + '/' + source.relative_to(application).as_posix()
            if source.is_symlink():
                entry = zipfile.ZipInfo(name)
                entry.create_system = 3
                entry.external_attr = source.lstat().st_mode << 16
                bundle.writestr(entry, os.readlink(source))
            elif source.is_file():
                bundle.write(source, name)
    with zipfile.ZipFile(archive) as bundle:
        if bundle.testzip() is not None:
            raise ValueError('macOS 压缩包完整性校验失败')
