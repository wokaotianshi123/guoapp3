import argparse
import datetime
import re
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

from sync_source import source_files, synchronize


@dataclass(frozen=True)
class TaskSnapshot:
    commit: str
    tag: str
    created_commit: bool


def git(repository, *arguments, allowed=(0,)):
    result = subprocess.run(['git', '-C', str(repository), *arguments],
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, encoding='utf-8')
    if result.returncode not in allowed:
        raise ValueError(result.stderr.strip() or result.stdout.strip() or 'Git 操作失败')
    return result


def tag_exists(repository, tag):
    return git(repository, 'show-ref', '--verify', '--quiet', 'refs/tags/' + tag,
               allowed=(0, 1)).returncode == 0


def finish_task(source, destination, message, tag=None):
    if destination.is_symlink():
        raise ValueError('同步目标不能是符号链接')
    source, destination = source.resolve(), destination.resolve()
    selected = source_files(source)
    if not message.strip():
        raise ValueError('请填写本次完成的变更说明')
    repository = Path(git(destination, 'rev-parse', '--show-toplevel').stdout.strip()).resolve()
    if repository != destination:
        raise ValueError('目标必须是独立 Git 仓库的根目录')
    for name in ('MERGE_HEAD', 'CHERRY_PICK_HEAD', 'REVERT_HEAD', 'rebase-merge', 'rebase-apply'):
        pending = Path(git(destination, 'rev-parse', '--git-path', name).stdout.strip())
        if not pending.is_absolute():
            pending = destination / pending
        if pending.exists():
            raise ValueError('Git 正在合并或变基，请完成该操作后再保存版本')
    if git(destination, 'symbolic-ref', '--quiet', 'HEAD', allowed=(0, 1)).returncode:
        raise ValueError('请在开发分支上保存版本，当前处于 detached HEAD 状态')
    version = re.search(r'^version:\s*(\d+\.\d+\.\d+(?:-[\w.-]+)?)(?:\+[\w.-]+)?\s*$',
                        (source / 'pubspec.yaml').read_text(encoding='utf-8'), re.MULTILINE)
    if not version:
        raise ValueError('pubspec.yaml 缺少有效版本号')
    base_tag = tag or 'v' + version.group(1)
    git(destination, 'check-ref-format', 'refs/tags/' + base_tag)
    if tag and tag_exists(destination, tag):
        raise ValueError('tag 已存在，不会覆盖：' + tag)
    if source != destination:
        synchronize(source, destination)
        if synchronize(source, destination, check=True).changed:
            raise ValueError('源码同步校验未通过')
    names = sorted(path.as_posix() for path in selected)
    tracked = set(git(destination, 'ls-files', '-z').stdout.strip('\x00').split('\x00')) - {''}
    removed = sorted(tracked - set(names))
    for offset in range(0, len(removed), 100):
        git(destination, 'rm', '--cached', '--force', '--ignore-unmatch', '--', *removed[offset:offset + 100])
    for offset in range(0, len(names), 100):
        git(destination, 'add', '--force', '--', *names[offset:offset + 100])
    staged = set(git(destination, 'ls-files', '-z').stdout.strip('\x00').split('\x00')) - {''}
    if staged != set(names):
        raise ValueError('Git 暂存区不符合纯源码清单，未创建提交')
    changes = git(destination, 'diff', '--cached', '--quiet', allowed=(0, 1)).returncode == 1
    if changes:
        git(destination, 'commit', '-m', message)
    commit = git(destination, 'rev-parse', 'HEAD').stdout.strip()
    git(destination, 'diff', '--quiet', 'HEAD')
    if source != destination and synchronize(source, destination, check=True).changed:
        raise ValueError('提交后源码发生变化，请重新完成同步')
    chosen = base_tag
    if tag_exists(destination, chosen):
        previous = git(destination, 'rev-parse', chosen + '^{commit}').stdout.strip()
        if previous == commit:
            return TaskSnapshot(commit, chosen, changes)
        timestamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%d-%H%M%S')
        chosen = base_tag + '-task-' + timestamp
        suffix = 2
        while tag_exists(destination, chosen):
            chosen = base_tag + '-task-' + timestamp + '-' + str(suffix)
            suffix += 1
    git(destination, 'tag', '-a', chosen, '-m', message + '\n\n版本：' + version.group(1))
    if git(destination, 'rev-parse', chosen + '^{commit}').stdout.strip() != commit:
        raise ValueError('tag 未指向本次提交')
    return TaskSnapshot(commit, chosen, changes)


def main():
    source = Path(__file__).resolve().parents[1]
    parser = argparse.ArgumentParser(description='同步干净源码并创建本地提交和恢复 tag，不推送远程仓库。')
    parser.add_argument('--message', required=True, help='本次实际完成的变更')
    parser.add_argument('--tag', help='指定 tag；已存在时拒绝覆盖')
    parser.add_argument('--destination', type=Path, default=source.parent / 'guoapp')
    options = parser.parse_args()
    try:
        result = finish_task(source, options.destination.expanduser(), options.message, options.tag)
    except (OSError, ValueError) as error:
        print(str(error), file=sys.stderr)
        return 1
    print('已保存恢复版本：' + result.tag)
    print('提交：' + result.commit)
    print('源码目录：' + str(options.destination.resolve()))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
