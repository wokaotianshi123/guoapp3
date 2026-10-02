package main

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var (
	ffmpegOnce   sync.Once
	ffmpegBinary string
)

// ffmpegPath 优先使用放在可执行程序旁边的 ffmpeg，其次才是 PATH 上的。
func ffmpegPath() string {
	ffmpegOnce.Do(func() {
		candidates := []string{}
		if executable, err := os.Executable(); err == nil {
			directory := filepath.Dir(executable)
			if runtime.GOOS == "windows" {
				candidates = append(candidates, filepath.Join(directory, "ffmpeg.exe"))
			}
			candidates = append(candidates, filepath.Join(directory, "ffmpeg"))
		}
		if found, err := exec.LookPath("ffmpeg"); err == nil {
			candidates = append(candidates, found)
		}
		for _, candidate := range candidates {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				ffmpegBinary = candidate
				return
			}
		}
	})
	return ffmpegBinary
}

const (
	jobIdleTTL   = 30 * time.Minute
	jobReadyWait = 15 * time.Second
)

type transcodeJob struct {
	mu       sync.Mutex
	dir      string
	cmd      *exec.Cmd
	done     chan struct{}
	err      error
	finished bool
	lastUsed time.Time
}

func (job *transcodeJob) touch() {
	job.mu.Lock()
	job.lastUsed = time.Now()
	job.mu.Unlock()
}

func (job *transcodeJob) playlist() string {
	return filepath.Join(job.dir, "index.m3u8")
}

func (job *transcodeJob) ready() bool {
	info, err := os.Stat(job.playlist())
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func (job *transcodeJob) isFinished() bool {
	job.mu.Lock()
	defer job.mu.Unlock()
	return job.finished
}

func (job *transcodeJob) failure() string {
	job.mu.Lock()
	defer job.mu.Unlock()
	if !job.finished || job.err == nil {
		return ""
	}
	return job.err.Error()
}

type transcodeManager struct {
	mu   sync.Mutex
	jobs map[string]*transcodeJob
}

var transcoder = &transcodeManager{jobs: map[string]*transcodeJob{}}

func jobKey(parts ...string) string {
	digest := sha1.Sum([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(digest[:10])
}

func (manager *transcodeManager) get(key string) *transcodeJob {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	job := manager.jobs[key]
	if job != nil {
		job.touch()
	}
	return job
}

// start 启动（或复用）一个转码任务。红果等站源的视频是 CENC 加密的 H.265，
// 浏览器既不能解密也不能解码，必须由 ffmpeg 解密后重编码成 H.264 才能播放。
func (manager *transcodeManager) start(key, input, decryptionKey string) (*transcodeJob, error) {
	binary := ffmpegPath()
	if binary == "" {
		return nil, errors.New("未找到 ffmpeg")
	}
	manager.mu.Lock()
	if job := manager.jobs[key]; job != nil {
		manager.mu.Unlock()
		job.touch()
		return job, nil
	}
	root := filepath.Join(os.TempDir(), "duanjuweb-"+editionSlug+"-live")
	dir := filepath.Join(root, key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		manager.mu.Unlock()
		return nil, err
	}
	arguments := []string{"-nostdin", "-loglevel", "error", "-y"}
	if decryptionKey != "" {
		arguments = append(arguments, "-decryption_key", decryptionKey)
	}
	arguments = append(arguments,
		"-i", input,
		"-map", "0:v:0", "-map", "0:a:0?",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "25",
		"-pix_fmt", "yuv420p", "-profile:v", "main",
		"-c:a", "aac", "-b:a", "96k", "-ac", "2",
		"-max_muxing_queue_size", "1024",
		"-f", "hls", "-hls_time", "3", "-hls_playlist_type", "event",
		"-hls_flags", "independent_segments",
		"-hls_segment_filename", filepath.Join(dir, "seg_%05d.ts"),
		filepath.Join(dir, "index.m3u8"),
	)
	command := exec.Command(binary, arguments...)
	attachProcess(command)
	command.Dir = dir
	// 输入地址是本机播放服务，必须绕开任何代理设置。
	command.Env = append(os.Environ(),
		"http_proxy=", "https_proxy=", "all_proxy=",
		"HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=",
		"no_proxy=*", "NO_PROXY=*",
	)
	logFile, err := os.Create(filepath.Join(dir, "ffmpeg.log"))
	if err != nil {
		manager.mu.Unlock()
		return nil, err
	}
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		manager.mu.Unlock()
		return nil, err
	}
	job := &transcodeJob{dir: dir, cmd: command, done: make(chan struct{}), lastUsed: time.Now()}
	manager.jobs[key] = job
	manager.mu.Unlock()
	go func() {
		err := command.Wait()
		job.mu.Lock()
		job.err = err
		job.finished = true
		job.mu.Unlock()
		close(job.done)
		_ = logFile.Close()
	}()
	return job, nil
}

// wait 等到第一个分片就绪；转码失败会立刻返回错误，慢机器上最多等 jobReadyWait。
func (manager *transcodeManager) wait(job *transcodeJob) (bool, string) {
	if job.ready() {
		return true, ""
	}
	timer := time.NewTimer(jobReadyWait)
	defer timer.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-job.done:
			if job.ready() {
				return true, ""
			}
			if message := job.failure(); message != "" {
				return false, message
			}
			return false, "ffmpeg 未生成播放列表"
		case <-timer.C:
			// ffmpeg 还在跑，交给播放器重试，不要因此判定失败。
			return job.ready(), ""
		case <-ticker.C:
			if job.ready() {
				return true, ""
			}
		}
	}
}

// sweep 回收闲置任务，避免临时目录无限增长。
func (manager *transcodeManager) sweep() {
	manager.mu.Lock()
	var stale []*transcodeJob
	for key, job := range manager.jobs {
		job.mu.Lock()
		idle := time.Since(job.lastUsed) > jobIdleTTL
		job.mu.Unlock()
		if idle {
			stale = append(stale, job)
			delete(manager.jobs, key)
		}
	}
	manager.mu.Unlock()
	for _, job := range stale {
		if job.cmd != nil && job.cmd.Process != nil && !job.finished {
			_ = job.cmd.Process.Kill()
		}
		_ = os.RemoveAll(job.dir)
	}
}
