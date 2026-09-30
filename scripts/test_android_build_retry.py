import io
import os
from pathlib import Path
import runpy
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

from android_build_retry import run_android_build, transient_download_failure


NETWORK_FAILURE = '''FAILURE: Build failed with an exception.

* What went wrong:
Execution failed for task ':gradle:compileKotlin'.
> Could not isolate parameters BuildToolsApiClasspathEntrySnapshotTransform
   > Could not resolve all files for configuration ':gradle:kotlinBuildToolsApiClasspath'.
      > Could not download kotlin-compiler-embeddable-2.2.21.jar
         > Could not get resource 'https://repo.maven.apache.org/maven2/org/jetbrains/kotlin/kotlin-compiler-embeddable/2.2.21/kotlin-compiler-embeddable-2.2.21.jar'.
            > Could not GET 'https://github.com/JetBrains/kotlin/releases/download/v2.2.21/kotlin-compiler-embeddable-2.2.21.jar'. Received status code 500 from server: Internal Server Error

* Try:
> Run with --stacktrace option to get the stack trace.
BUILD FAILED in 4s
'''
COMPILE_FAILURE = '''FAILURE: Build failed with an exception.

* What went wrong:
Execution failed for task ':app:compileReleaseKotlin'.
> Compilation error. Unresolved reference: example

* Try:
> Run with --stacktrace option to get the stack trace.
'''


def fake_process(output, returncode):
    process = mock.MagicMock()
    process.__enter__.return_value = process
    process.stdout = iter(output.splitlines(keepends=True))
    process.wait.return_value = returncode
    return process


class AndroidBuildRetryTests(unittest.TestCase):
    def test_reported_kotlin_download_failure_is_retryable(self):
        self.assertTrue(transient_download_failure(NETWORK_FAILURE))

    def test_transient_http_and_connection_errors_are_retryable(self):
        for message in ['Received status code 408', 'Received status code 429',
                        'Received status code 502', 'Received status code 503',
                        'Read timed out', 'Connect timed out', 'Connection timed out',
                        'Connection reset', 'java.net.UnknownHostException: repo.maven.apache.org']:
            with self.subTest(message=message):
                self.assertTrue(transient_download_failure(
                    NETWORK_FAILURE.replace('Received status code 500 from server: Internal Server Error', message)))

    def test_permanent_http_certificate_and_compile_failures_are_not_retried(self):
        for message in ['Received status code 401', 'Received status code 403',
                        'Received status code 404', 'PKIX path building failed']:
            with self.subTest(message=message):
                self.assertFalse(transient_download_failure(
                    NETWORK_FAILURE.replace('Received status code 500 from server: Internal Server Error', message)))
        self.assertFalse(transient_download_failure(COMPILE_FAILURE))
        self.assertFalse(transient_download_failure('Gradle threw an error while downloading artifacts from the network.'))

    def test_last_failure_controls_retry_after_flutter_internal_retry(self):
        self.assertFalse(transient_download_failure(NETWORK_FAILURE + COMPILE_FAILURE))
        self.assertTrue(transient_download_failure(COMPILE_FAILURE + NETWORK_FAILURE))

    def test_network_failure_retries_same_command_and_preserves_live_logs(self):
        command = ['flutter', 'build', 'apk', '--dart-define=ALL_SOURCES=true']
        environment = {'PATH': '/tools'}
        with tempfile.TemporaryDirectory() as temporary, \
                mock.patch('android_build_retry.subprocess.Popen', side_effect=[
                    fake_process(NETWORK_FAILURE, 1), fake_process('Built app.apk\n', 0)]) as popen, \
                mock.patch('android_build_retry.time.sleep') as sleep, \
                mock.patch('sys.stdout', new_callable=io.StringIO) as stdout:
            run_android_build(command, cwd=temporary, env=environment, edition='quanjushijie')
            self.assertEqual(popen.call_count, 2)
            sleep.assert_called_once_with(10)
            for call in popen.call_args_list:
                self.assertEqual(call.args[0], command)
                self.assertEqual(call.kwargs['cwd'], temporary)
                self.assertEqual(call.kwargs['env'], environment)
            logs = sorted((Path(temporary) / 'build/logs/android').glob('*/*.log'))
            self.assertEqual([log.name for log in logs], ['attempt-1.log', 'attempt-2.log'])
            self.assertEqual(logs[0].read_text(encoding='utf-8'), NETWORK_FAILURE)
            self.assertEqual(logs[1].read_text(encoding='utf-8'), 'Built app.apk\n')
            self.assertIn(NETWORK_FAILURE, stdout.getvalue())
            self.assertIn('Built app.apk', stdout.getvalue())

    def test_success_has_no_retry_and_each_invocation_keeps_its_logs(self):
        with tempfile.TemporaryDirectory() as temporary, \
                mock.patch('android_build_retry.subprocess.Popen', side_effect=[
                    fake_process('first\n', 0), fake_process('second\n', 0)]) as popen, \
                mock.patch('android_build_retry.time.sleep') as sleep, \
                mock.patch('sys.stdout', new_callable=io.StringIO):
            for _ in range(2):
                run_android_build(['flutter'], cwd=temporary, env={}, edition='duanjushijie')
            self.assertEqual(popen.call_count, 2)
            sleep.assert_not_called()
            logs = list((Path(temporary) / 'build/logs/android').glob('*/attempt-1.log'))
            self.assertEqual(len(logs), 2)
            self.assertEqual({log.read_text(encoding='utf-8') for log in logs}, {'first\n', 'second\n'})

    def test_retries_stop_at_limit_and_preserve_exit_code(self):
        with tempfile.TemporaryDirectory() as temporary, \
                mock.patch('android_build_retry.subprocess.Popen', side_effect=[
                    fake_process(NETWORK_FAILURE, code) for code in [1, 1, 7]]) as popen, \
                mock.patch('android_build_retry.time.sleep') as sleep, \
                mock.patch('sys.stdout', new_callable=io.StringIO), \
                mock.patch('sys.stderr', new_callable=io.StringIO) as stderr:
            with self.assertRaises(subprocess.CalledProcessError) as raised:
                run_android_build(['flutter'], cwd=temporary, env={}, edition='duanjushijie')
            self.assertEqual(raised.exception.returncode, 7)
            self.assertEqual(popen.call_count, 3)
            self.assertEqual(sleep.call_args_list, [mock.call(10), mock.call(30)])
            self.assertIn('网络重试已用尽', stderr.getvalue())
            self.assertEqual(len(list((Path(temporary) / 'build/logs/android').glob('*/*.log'))), 3)

    def test_real_subprocess_keeps_stdout_and_stderr_in_log(self):
        command = [sys.executable, '-X', 'utf8', '-c',
                   "import sys; print('构建输出', flush=True); print('构建诊断', file=sys.stderr, flush=True)"]
        with tempfile.TemporaryDirectory() as temporary, \
                mock.patch('android_build_retry.time.sleep') as sleep, \
                mock.patch('sys.stdout', new_callable=io.StringIO) as stdout:
            run_android_build(command, cwd=temporary, env=os.environ.copy(), edition='duanjushijie')
            logs = list((Path(temporary) / 'build/logs/android').glob('*/*.log'))
            self.assertEqual(len(logs), 1)
            output = logs[0].read_text(encoding='utf-8')
            self.assertEqual(output, '构建输出\n构建诊断\n')
            self.assertIn(output, stdout.getvalue())
            sleep.assert_not_called()

    def test_compilation_error_stops_immediately(self):
        with tempfile.TemporaryDirectory() as temporary, \
                mock.patch('android_build_retry.subprocess.Popen', return_value=fake_process(COMPILE_FAILURE, 2)) as popen, \
                mock.patch('android_build_retry.time.sleep') as sleep, \
                mock.patch('sys.stdout', new_callable=io.StringIO), \
                mock.patch('sys.stderr', new_callable=io.StringIO):
            with self.assertRaises(subprocess.CalledProcessError) as raised:
                run_android_build(['flutter'], cwd=temporary, env={}, edition='duanjushijie')
            self.assertEqual(raised.exception.returncode, 2)
            popen.assert_called_once()
            sleep.assert_not_called()

    def test_failed_apk_build_does_not_package_a_release(self):
        script = Path(__file__).with_name('build_android.py')
        with mock.patch.object(sys, 'argv', [str(script), '--abi', 'arm64-v8a']), \
                mock.patch.dict(os.environ, {'PATH': '/tools'}, clear=True), \
                mock.patch('platform.system', return_value='Linux'), \
                mock.patch('shutil.which', return_value='/tools/flutter'), \
                mock.patch('subprocess.run') as run, \
                mock.patch('android_build_retry.run_android_build',
                           side_effect=subprocess.CalledProcessError(7, ['flutter'])) as build:
            with self.assertRaises(SystemExit) as raised:
                runpy.run_path(str(script), run_name='__main__')
        self.assertEqual(raised.exception.code, 7)
        self.assertEqual(run.call_count, 2)
        self.assertTrue(any(str(arg).endswith('build_native.py') for arg in run.call_args_list[0].args[0]))
        self.assertEqual(run.call_args_list[1].args[0], ['/tools/flutter', 'pub', 'get', '--enforce-lockfile'])
        command = build.call_args.args[0]
        self.assertIn('--dart-define=ALL_SOURCES=false', command)
        self.assertEqual(command[-2:], ['--target-platform', 'android-arm64'])


if __name__ == '__main__':
    unittest.main()
