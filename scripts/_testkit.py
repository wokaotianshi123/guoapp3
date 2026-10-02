"""单测用的临时目录工具。

直接用 tempfile.TemporaryDirectory() 清理含 Git 仓库的目录时会随机失败：
Linux 上 git 可能在后台写对象（gc），Windows 上 .git 里的对象文件是只读的，
两种情况都会在 rmtree 遍历之后又有新条目出现，最后报
“Directory not empty” 或 “Permission denied”。这里统一用「放开权限 + 重试」删除。
"""

import os
import shutil
import stat
import tempfile
import time


def force_remove(function, target, unused):
    """rmtree 的 onerror 回调：先放开只读属性再重试一次。"""
    try:
        os.chmod(target, stat.S_IWRITE | stat.S_IREAD)
    except OSError:
        pass
    try:
        function(target)
    except OSError:
        pass


def remove_tree(path):
    """尽力删掉整棵树，绝不抛错——临时目录的清理不该让单测失败。"""
    target = str(path)
    for attempt in range(3):
        if not os.path.lexists(target):
            return
        try:
            shutil.rmtree(target, onerror=force_remove)
        except OSError:
            # 后台进程还在往里写文件，等一下再试。
            time.sleep(0.2 * (attempt + 1))
    if os.path.lexists(target):
        shutil.rmtree(target, ignore_errors=True)


def make_temp_dir(prefix='duanju-test-'):
    """返回临时目录路径，配合 addCleanup(remove_tree, path) 使用。"""
    return tempfile.mkdtemp(prefix=prefix)
