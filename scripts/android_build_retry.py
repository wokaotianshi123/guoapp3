from collections import deque
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time


def transient_download_failure(output):
    failures = output.split('FAILURE: Build failed with an exception.')
    if len(failures) < 2:
        return False
    failure = failures[-1].split('* Try:', 1)[0]
    downloading = re.search(r'Could not (?:download|resolve|get resource|GET|HEAD)\b', failure, re.IGNORECASE)
    transient = re.search(
        r'status code\s+(?:408|429|5\d\d)\b|(?:read|connect|connection) timed out|'
        r'connection reset|UnknownHostException|temporary failure in name resolution|'
        r'unexpected end of file from server', failure, re.IGNORECASE)
    return bool(downloading and transient)


def run_android_build(command, *, cwd, env, edition):
    log_root = Path(cwd) / 'build' / 'logs' / 'android'
    log_root.mkdir(parents=True, exist_ok=True)
    log_directory = Path(tempfile.mkdtemp(prefix=edition + '-', dir=log_root))
    retry_delays = (10, 30)
    attempts = len(retry_delays) + 1
    for attempt in range(1, attempts + 1):
        log_path = log_directory / f'attempt-{attempt}.log'
        print(f'Android APK 构建 {attempt}/{attempts}，日志：{log_path}', flush=True)
        tail = deque(maxlen=2048)
        with log_path.open('w', encoding='utf-8') as log:
            with subprocess.Popen(command, cwd=cwd, env=env, stdout=subprocess.PIPE,
                                  stderr=subprocess.STDOUT, text=True, encoding='utf-8',
                                  errors='replace', bufsize=1) as process:
                for line in process.stdout:
                    log.write(line)
                    log.flush()
                    sys.stdout.write(line)
                    sys.stdout.flush()
                    tail.append(line)
                returncode = process.wait()
        if returncode == 0:
            return
        retryable = transient_download_failure(''.join(tail))
        if not retryable or attempt == attempts:
            reason = '网络重试已用尽' if retryable else '未识别为临时依赖下载故障，不自动重试'
            print(f'Android APK 构建失败：{reason}。完整日志：{log_path}', file=sys.stderr, flush=True)
            raise subprocess.CalledProcessError(returncode, command)
        delay = retry_delays[attempt - 1]
        print(f'检测到临时依赖下载故障，{delay} 秒后重试 APK 构建；保留已有构建缓存。', flush=True)
        time.sleep(delay)
