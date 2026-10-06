package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"duanjuapp/native/core"

	_ "github.com/nathanstitt/omnidoc/pkg/heif"
)

var (
	editionSlug = "duanjushijie"
	appVersion  = "dev"
)

// 名称由 slug 推导，避免通过 -X 传入中文在 Windows 上被系统编码破坏。
func editionName() string {
	if editionSlug == "quanjushijie" {
		return "全剧视界"
	}
	return "短剧视界"
}

//go:embed web
var webAssets embed.FS

var mediaPort atomic.Value

var localMediaAddress = regexp.MustCompile(`http://(?:127\.0\.0\.1|localhost|\[::1\]):(\d+)/`)

// 与 lib/models.dart 的 allValues 保持同一顺序，名称与 Flutter 端一致。
var sourceNames = []struct {
	ID   string
	Name string
}{
	{"hongguo", "红果"},
	{"ikanbot", "爱看机器人"},
	{"a123", "A123"},
	{"hanxiaoquan", "韩小圈"},
	{"guipian", "鬼片"},
	{"huangdou", "黄豆"},
	{"huangju", "剧果"},
	{"yeguo", "野果"},
	{"dsd", "帝果"},
	{"huangguo-video", "黄果视频"},
	{"huangguoai", "黄果 AI"},
	{"cloudfront", "黄果旧版"},
	{"yaguo", "芽果"},
	{"maoguo", "猫果"},
	{"fanguo", "饭果"},
	{"guanguo", "观果"},
	{"heguo", "河果"},
	{"xingguo", "星果"},
	{"huaguo", "花果"},
	{"niuguo", "牛果"},
	{"wangguo", "网果"},
	{"faguo", "发果"},
	{"wuguo", "伍果"},
}

var lastSequence atomic.Int64

func nextSequence() int64 {
	now := time.Now().UnixNano()
	for {
		last := lastSequence.Load()
		next := now
		if next <= last {
			next = last + 1
		}
		if lastSequence.CompareAndSwap(last, next) {
			return next
		}
	}
}

func writeJSON(writer http.ResponseWriter, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		http.Error(writer, "响应编码失败", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_, _ = writer.Write(body)
}

func handleRequest(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "只接受 POST 请求", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
	if err != nil {
		http.Error(writer, "读取请求失败", http.StatusBadRequest)
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	payload := map[string]any{}
	if decoder.Decode(&payload) != nil {
		http.Error(writer, "请求不是合法 JSON", http.StatusBadRequest)
		return
	}
	// 播放解析要求 sequence 严格递增，由服务端统一分配，浏览器不需要关心。
	switch payload["action"] {
	case "resolve", "fallback", "selectRoute":
		payload["sequence"] = json.Number(strconv.FormatInt(nextSequence(), 10))
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		http.Error(writer, "请求编码失败", http.StatusBadRequest)
		return
	}
	reply := rewriteLocalMedia(core.NativeRequest(string(encoded)))
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(writer, reply)
}

// 本机播放服务永远在 127.0.0.1，代理设置只会碍事。
var localTransport = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 15 * time.Second}).DialContext}

func handleMedia(writer http.ResponseWriter, request *http.Request) {
	port, _ := mediaPort.Load().(string)
	if port == "" {
		http.Error(writer, "还没有正在播放的节目", http.StatusNotFound)
		return
	}
	if request.URL.Path == "/api/media" || request.URL.Path == "/api/media/" {
		http.Error(writer, "媒体地址无效", http.StatusNotFound)
		return
	}
	proxy := &httputil.ReverseProxy{
		Transport: localTransport,
		Director: func(outgoing *http.Request) {
			outgoing.URL.Scheme = "http"
			outgoing.URL.Host = net.JoinHostPort("127.0.0.1", port)
			outgoing.URL.Path = strings.TrimPrefix(outgoing.URL.Path, "/api/media")
			outgoing.Host = outgoing.URL.Host
		},
		ModifyResponse: func(response *http.Response) error {
			if !strings.Contains(response.Header.Get("Content-Type"), "mpegurl") ||
				response.StatusCode != http.StatusOK || response.Body == nil {
				return nil
			}
			body, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil {
				return err
			}
			// 播放列表里的地址指向本机播放服务，局域网设备无法访问，改写成经由本服务转发。
			for _, prefix := range []string{"http://127.0.0.1:" + port + "/", "http://localhost:" + port + "/"} {
				body = []byte(strings.ReplaceAll(string(body), prefix, "/api/media/"))
			}
			response.Body = io.NopCloser(bytes.NewReader(body))
			response.ContentLength = int64(len(body))
			response.Header.Set("Content-Length", strconv.Itoa(len(body)))
			response.Header.Del("Content-Encoding")
			return nil
		},
	}
	proxy.ServeHTTP(writer, request)
}

// rewriteLocalMedia 把核心返回的 127.0.0.1 播放地址改写成经由本服务转发的相对路径，
// 这样本机与局域网设备用的是同一个地址。
func rewriteLocalMedia(reply string) string {
	match := localMediaAddress.FindStringSubmatch(reply)
	if match == nil {
		return reply
	}
	mediaPort.Store(match[1])
	for _, prefix := range []string{
		"http://127.0.0.1:" + match[1] + "/",
		"http://localhost:" + match[1] + "/",
	} {
		reply = strings.ReplaceAll(reply, prefix, "/api/media/")
	}
	return reply
}

type playPlan struct {
	URL           string `json:"url"`
	Quality       int    `json:"quality"`
	Qualities     []int  `json:"qualities"`
	RouteIndex    int    `json:"routeIndex"`
	RouteCount    int    `json:"routeCount"`
	DecryptionKey string `json:"decryptionKey"`
	Session       string `json:"session"`
}

type playReply struct {
	OK    bool     `json:"ok"`
	Error string   `json:"error"`
	Data  playPlan `json:"data"`
}

// pickQuality 默认取 480P：够清晰，转码压力也可控。
func pickQuality(qualities []int, wanted int) int {
	if len(qualities) == 0 {
		return 0
	}
	best := 0
	for _, value := range qualities {
		if value == wanted {
			return value
		}
		if best == 0 {
			best = value
			continue
		}
		if abs(value-wanted) < abs(best-wanted) {
			best = value
		}
	}
	return best
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// probeCodec 抓一段开头判断编码。多数站源的 moov 在文件头部，足以判定；
// 判断不出来就按可直连处理。
func probeCodec(rawURL string) (encrypted bool, hevc bool) {
	client := &http.Client{Transport: localTransport, Timeout: 20 * time.Second}
	request, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return false, false
	}
	request.Header.Set("Range", "bytes=0-1048575")
	response, err := client.Do(request)
	if err != nil {
		return false, false
	}
	defer response.Body.Close()
	head, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return false, false
	}
	encrypted = bytes.Contains(head, []byte("encv")) || bytes.Contains(head, []byte("enca"))
	hevc = bytes.Contains(head, []byte("hvc1")) ||
		bytes.Contains(head, []byte("hev1")) ||
		bytes.Contains(head, []byte("hvcC"))
	return encrypted, hevc
}

func handlePlay(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "只接受 POST 请求", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
	if err != nil {
		http.Error(writer, "读取请求失败", http.StatusBadRequest)
		return
	}
	payload := map[string]any{}
	if json.Unmarshal(body, &payload) != nil {
		http.Error(writer, "请求不是合法 JSON", http.StatusBadRequest)
		return
	}
	dramaID, _ := payload["id"].(string)
	if dramaID == "" {
		if nested, ok := payload["drama"].(map[string]any); ok {
			dramaID, _ = nested["id"].(string)
		}
	}
	index := 0
	if value, ok := payload["index"].(float64); ok {
		index = int(value)
	}
	quality := 0
	if value, ok := payload["quality"].(float64); ok {
		quality = int(value)
	}
	route := 0
	if value, ok := payload["route"].(float64); ok {
		route = int(value)
	}
	force, _ := payload["force"].(bool)
	if quality <= 0 {
		quality = 480
	}
	if route < 0 {
		route = 0
	}

	// 先按默认画质探一次，拿到可选画质后再挑目标画质重新解析。
	probe := map[string]any{}
	for key, value := range payload {
		if key == "quality" || key == "route" || key == "force" || key == "action" {
			continue
		}
		probe[key] = value
	}
	probe["action"] = "resolve"
	probe["route"] = route
	probe["sequence"] = json.Number(strconv.FormatInt(nextSequence(), 10))
	encoded, err := json.Marshal(probe)
	if err != nil {
		http.Error(writer, "请求编码失败", http.StatusBadRequest)
		return
	}
	first := playReply{}
	if err := json.Unmarshal([]byte(core.NativeRequest(string(encoded))), &first); err != nil || !first.OK {
		message := first.Error
		if message == "" {
			message = "解析播放地址失败"
		}
		writeJSON(writer, map[string]any{"ok": false, "error": message})
		return
	}
	wanted := pickQuality(first.Data.Qualities, quality)
	if wanted != 0 && wanted != first.Data.Quality {
		probe["quality"] = wanted
		probe["sequence"] = json.Number(strconv.FormatInt(nextSequence(), 10))
		if encoded, err = json.Marshal(probe); err == nil {
			var second playReply
			if json.Unmarshal([]byte(core.NativeRequest(string(encoded))), &second) == nil && second.OK {
				first = second
			}
		}
	}

	plan := first.Data
	rawURL := plan.URL
	mediaURL := rewriteLocalMedia(rawURL)
	encryptedKey := plan.DecryptionKey
	detectedEncrypted, hevc := probeCodec(rawURL)
	encrypted := encryptedKey != "" || detectedEncrypted

	answer := map[string]any{
		"url":        mediaURL,
		"quality":    plan.Quality,
		"qualities":  plan.Qualities,
		"routeIndex": plan.RouteIndex,
		"routeCount": plan.RouteCount,
		"encrypted":  encrypted,
		"hevc":       hevc,
		"ffmpeg":     ffmpegPath() != "",
		"mode":       "direct",
	}
	binary := ffmpegPath()
	if binary != "" && (encrypted || hevc || force) {
		key := jobKey(dramaID, strconv.Itoa(index), strconv.Itoa(wanted), strconv.Itoa(route), encryptedKey)
		job, err := transcoder.start(key, rawURL, encryptedKey)
		if err == nil {
			ready, failure := transcoder.wait(job)
			if ready {
				answer["mode"] = "hls"
				answer["url"] = "/api/live/" + key + "/index.m3u8"
				answer["transcoding"] = !job.isFinished()
			} else if failure != "" {
				answer["message"] = "转码失败：" + failure
			} else {
				// ffmpeg 还在准备第一个分片，让播放器自己重试。
				answer["mode"] = "hls"
				answer["url"] = "/api/live/" + key + "/index.m3u8"
				answer["transcoding"] = true
			}
		} else {
			answer["message"] = err.Error()
		}
	} else if binary == "" && (encrypted || hevc) {
		answer["message"] = "需要 ffmpeg 转码"
	}
	writeJSON(writer, map[string]any{"ok": true, "data": answer})
}

func handleLive(writer http.ResponseWriter, request *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/live/"), "/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		http.NotFound(writer, request)
		return
	}
	job := transcoder.get(parts[0])
	if job == nil {
		http.NotFound(writer, request)
		return
	}
	target := filepath.Clean(filepath.Join(job.dir, filepath.FromSlash(parts[1])))
	if target != job.dir && !strings.HasPrefix(target, job.dir+string(filepath.Separator)) {
		http.NotFound(writer, request)
		return
	}
	// 明确给出类型，免得依赖系统 mime 表（.ts 在部分系统上会被识别错）。
	switch strings.ToLower(filepath.Ext(target)) {
	case ".m3u8":
		writer.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	case ".ts", ".m4s":
		writer.Header().Set("Content-Type", "video/mp2t")
	case ".mp4":
		writer.Header().Set("Content-Type", "video/mp4")
	}
	writer.Header().Set("Cache-Control", "no-store")
	http.ServeFile(writer, request, target)
}

// 封面转换串行执行：单张转换只需几十毫秒，串行足以撑住本地页面的并发。
var coverConvert sync.Mutex

// convertHEIC 把核心缓存的 HEIC 封面转码为浏览器可显示的 JPEG，
// 结果与 Flutter 端 CoverDecoder 一样缓存在 covers 目录的 compatible-v1 下。
func convertHEIC(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("封面缓存文件不存在")
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	directory := filepath.Join(filepath.Dir(path), "compatible-v1")
	target := filepath.Join(directory, fmt.Sprintf("%s-%d-%d.jpg", base, info.ModTime().UnixNano(), info.Size()))
	if stat, err := os.Stat(target); err == nil && stat.Mode().IsRegular() && stat.Size() > 4 {
		return target, nil
	}
	coverConvert.Lock()
	defer coverConvert.Unlock()
	if stat, err := os.Stat(target); err == nil && stat.Mode().IsRegular() && stat.Size() > 4 {
		return target, nil
	}
	source, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer source.Close()
	// 解码器遇到异常数据可能 panic，兜底成普通错误让页面回退到占位图。
	defer func() {
		if recover() != nil {
			err = errors.New("封面解码失败")
		}
	}()
	decoded, _, err := image.Decode(source)
	if err != nil {
		return "", err
	}
	buffered := new(bytes.Buffer)
	if err := jpeg.Encode(buffered, decoded, &jpeg.Options{Quality: 82}); err != nil {
		return "", err
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", err
	}
	part := target + ".part"
	if err := os.WriteFile(part, buffered.Bytes(), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(part, target); err != nil {
		_ = os.Remove(part)
		return "", err
	}
	return target, nil
}

func handleCover(writer http.ResponseWriter, request *http.Request) {
	id := strings.TrimSpace(request.URL.Query().Get("id"))
	if id == "" {
		http.Error(writer, "缺少剧集 id", http.StatusBadRequest)
		return
	}
	drama := map[string]any{"id": id}
	if source := strings.TrimSpace(request.URL.Query().Get("source")); source != "" {
		drama["source"] = source
	}
	if cover := strings.TrimSpace(request.URL.Query().Get("u")); cover != "" {
		parsed, err := url.Parse(cover)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			http.Error(writer, "封面地址无效", http.StatusBadRequest)
			return
		}
		drama["cover"] = cover
	}
	encoded, err := json.Marshal(map[string]any{"action": "cover", "drama": drama})
	if err != nil {
		http.Error(writer, "请求编码失败", http.StatusInternalServerError)
		return
	}
	reply := struct {
		OK   bool `json:"ok"`
		Data struct {
			Path string `json:"path"`
			HEIC bool   `json:"heic"`
		} `json:"data"`
		Error string `json:"error"`
	}{}
	if err := json.Unmarshal([]byte(core.NativeRequest(string(encoded))), &reply); err != nil || !reply.OK || reply.Data.Path == "" {
		http.Error(writer, "封面暂不可用", http.StatusNotFound)
		return
	}
	path := reply.Data.Path
	if reply.Data.HEIC {
		converted, err := convertHEIC(path)
		if err == nil {
			path = converted
		}
	}
	file, err := os.Open(path)
	if err != nil {
		http.Error(writer, "封面暂不可用", http.StatusNotFound)
		return
	}
	defer file.Close()
	head := make([]byte, 512)
	count, _ := io.ReadFull(file, head)
	contentType := "application/octet-stream"
	if count > 0 {
		if detected := http.DetectContentType(head[:count]); detected != "" {
			contentType = detected
		}
	}
	writer.Header().Set("Content-Type", contentType)
	writer.Header().Set("Cache-Control", "public, max-age=86400")
	if _, err := file.Seek(0, io.SeekStart); err == nil {
		_, _ = io.Copy(writer, file)
	} else {
		_, _ = writer.Write(head[:count])
	}
}

func handleSources(writer http.ResponseWriter, request *http.Request) {
	items := []map[string]string{}
	for _, source := range sourceNames {
		if sourceAvailable(source.ID) {
			items = append(items, map[string]string{"id": source.ID, "name": source.Name})
		}
	}
	// 并入用户自定义的 maccms 站源，使其出现在站源下拉框。
	// 核心返回的是 {"ok":true,"data":{"items":[...]}} 信封，必须从 data 里取。
	var customReply struct {
		OK   bool `json:"ok"`
		Data struct {
			Items []map[string]string `json:"items"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(core.NativeRequest(`{"action":"customSources"}`)), &customReply) == nil && customReply.OK {
		for _, custom := range customReply.Data.Items {
			if sourceAvailable(custom["id"]) {
				items = append(items, map[string]string{"id": custom["id"], "name": custom["name"]})
			}
		}
	}
	writeJSON(writer, map[string]any{"items": items})
}

func sourceAvailable(source string) bool {
	encoded, err := json.Marshal(map[string]any{"action": "sourceStatus", "source": source})
	if err != nil {
		return false
	}
	answer := struct {
		OK bool `json:"ok"`
	}{}
	return json.Unmarshal([]byte(core.NativeRequest(string(encoded))), &answer) == nil && answer.OK
}

// customSourceRecords 返回引擎中全部自定义 maccms 站源。
// 核心响应是 {"ok":true,"data":{"items":[...]}} 信封，必须从 data 里取 items。
func customSourceRecords() []map[string]any {
	var reply struct {
		OK   bool `json:"ok"`
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(core.NativeRequest(`{"action":"customSources"}`)), &reply) != nil || !reply.OK {
		return []map[string]any{}
	}
	return reply.Data.Items
}

// customSourceAction 新增或更新自定义源。核心记录放在 data 字段；失败时
// 返回核心的真实错误（如"该网址已存在"），供前端原样展示而不是被当成乱码。
func customSourceAction(action, id, name, base, rule string) (map[string]any, error) {
	payload := map[string]any{"action": action, "name": name, "base": base, "rule": rule}
	if id != "" {
		payload["source"] = id
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var reply struct {
		OK    bool           `json:"ok"`
		Data  map[string]any `json:"data"`
		Error string         `json:"error"`
	}
	if err := json.Unmarshal([]byte(core.NativeRequest(string(encoded))), &reply); err != nil {
		return nil, errors.New("核心响应无法解析")
	}
	if !reply.OK || reply.Data == nil || reply.Data["id"] == nil {
		message := strings.TrimSpace(reply.Error)
		if message == "" {
			message = "操作失败"
		}
		return nil, errors.New(message)
	}
	return reply.Data, nil
}

// customSourceDelete 删除自定义源，返回核心的真实错误信息。
func customSourceDelete(id string) error {
	var reply struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(core.NativeRequest(`{"action":"removeCustomSource","source":`+quoteJSON(id)+`}`)), &reply); err != nil {
		return errors.New("核心响应无法解析")
	}
	if !reply.OK {
		message := strings.TrimSpace(reply.Error)
		if message == "" {
			message = "删除失败"
		}
		return errors.New(message)
	}
	return nil
}

func quoteJSON(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// customSourceExport 导出全部自定义源为 JSON 文本（含 XBPQ 规则原文）。
func customSourceExport() (string, string, error) {
	var reply struct {
		OK   bool `json:"ok"`
		Data struct {
			Content  string `json:"content"`
			Filename string `json:"filename"`
		} `json:"data"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(core.NativeRequest(`{"action":"exportCustomSources"}`)), &reply); err != nil || !reply.OK {
		message := strings.TrimSpace(reply.Error)
		if message == "" {
			message = "导出失败"
		}
		return "", "", errors.New(message)
	}
	filename := strings.TrimSpace(reply.Data.Filename)
	if filename == "" {
		filename = "duanju-custom-sources.json"
	}
	return reply.Data.Content, filename, nil
}

// customSourceImport 导入 JSON 文本，返回核心给出的逐条结果。
func customSourceImport(content string) (map[string]any, error) {
	payload, err := json.Marshal(map[string]any{"action": "importCustomSources", "content": content})
	if err != nil {
		return nil, err
	}
	var reply struct {
		OK    bool           `json:"ok"`
		Data  map[string]any `json:"data"`
		Error string         `json:"error"`
	}
	if err := json.Unmarshal([]byte(core.NativeRequest(string(payload))), &reply); err != nil {
		return nil, errors.New("核心响应无法解析")
	}
	if !reply.OK || reply.Data == nil {
		message := strings.TrimSpace(reply.Error)
		if message == "" {
			message = "导入失败"
		}
		return nil, errors.New(message)
	}
	return reply.Data, nil
}

// customSourceReset 清空全部自定义源，恢复默认内置源状态。
func customSourceReset() (int, error) {
	var reply struct {
		OK   bool `json:"ok"`
		Data struct {
			Removed int `json:"removed"`
		} `json:"data"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(core.NativeRequest(`{"action":"resetCustomSources"}`)), &reply); err != nil || !reply.OK {
		message := strings.TrimSpace(reply.Error)
		if message == "" {
			message = "恢复默认失败"
		}
		return 0, errors.New(message)
	}
	return reply.Data.Removed, nil
}

// customSourceError 把失败写成前端可解析的 JSON，前端据此显示真实的错误原因。
func customSourceError(writer http.ResponseWriter, message string) {
	writeJSON(writer, map[string]any{"ok": false, "error": message})
}

// 自定义 maccms 源管理：GET 列表 / POST 新增 / PUT {id} 更新 / DELETE {id} 删除；
// 以及 GET export 导出文件、POST import 批量导入（JSON / TVBox / 纯文本清单）。
func handleCustomSources(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/api/custom-sources/export" {
		if request.Method != http.MethodGet {
			customSourceError(writer, "导出不支持该请求方法")
			return
		}
		content, filename, err := customSourceExport()
		if err != nil {
			customSourceError(writer, err.Error())
			return
		}
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		writer.Header().Set("Content-Disposition", "attachment; filename="+filename)
		_, _ = writer.Write([]byte(content))
		return
	}
	if request.URL.Path == "/api/custom-sources/import" {
		if request.Method != http.MethodPost {
			customSourceError(writer, "导入不支持该请求方法")
			return
		}
		body, err := io.ReadAll(io.LimitReader(request.Body, 8<<20))
		if err != nil {
			customSourceError(writer, "读取导入文件失败")
			return
		}
		// 允许三种请求体：① {"url":"https://…/config.json"} 远程地址导入
		// ② {"content":"…"} 包装 ③ 直接上传文件体
		content := string(body)
		var wrapped struct {
			Content string `json:"content"`
			URL     string `json:"url"`
		}
		if json.Unmarshal(body, &wrapped) == nil {
			if strings.TrimSpace(wrapped.URL) != "" {
				content = strings.TrimSpace(wrapped.URL)
			} else if strings.TrimSpace(wrapped.Content) != "" && strings.TrimSpace(content)[0] == '{' {
				var probe map[string]any
				if json.Unmarshal(body, &probe) == nil && probe["content"] != nil {
					content = wrapped.Content
				}
			}
		}
		result, err := customSourceImport(content)
		if err != nil {
			customSourceError(writer, err.Error())
			return
		}
		writeJSON(writer, map[string]any{"ok": true, "result": result})
		return
	}
	if request.URL.Path == "/api/custom-sources/reset" {
		if request.Method != http.MethodPost {
			customSourceError(writer, "恢复默认不支持该请求方法")
			return
		}
		removed, err := customSourceReset()
		if err != nil {
			customSourceError(writer, err.Error())
			return
		}
		writeJSON(writer, map[string]any{"ok": true, "removed": removed})
		return
	}
	switch request.Method {
	case http.MethodGet:
		writeJSON(writer, map[string]any{"items": customSourceRecords()})
		return
	case http.MethodPost:
		body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
		if err != nil {
			customSourceError(writer, "读取请求失败")
			return
		}
		payload := struct {
			Name string `json:"name"`
			Base string `json:"base"`
			Rule string `json:"rule"`
		}{}
		if json.Unmarshal(body, &payload) != nil {
			customSourceError(writer, "请求不是合法 JSON")
			return
		}
		record, err := customSourceAction("addCustomSource", "", payload.Name, payload.Base, payload.Rule)
		if err != nil {
			customSourceError(writer, err.Error())
			return
		}
		writeJSON(writer, record)
		return
	}
	id := strings.TrimPrefix(strings.TrimSuffix(request.URL.Path, "/"), "/api/custom-sources/")
	if id == "" {
		customSourceError(writer, "缺少自定义源标识")
		return
	}
	switch request.Method {
	case http.MethodPut:
		body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
		if err != nil {
			customSourceError(writer, "读取请求失败")
			return
		}
		payload := struct {
			Name string `json:"name"`
			Base string `json:"base"`
			Rule string `json:"rule"`
		}{}
		if json.Unmarshal(body, &payload) != nil {
			customSourceError(writer, "请求不是合法 JSON")
			return
		}
		record, err := customSourceAction("updateCustomSource", id, payload.Name, payload.Base, payload.Rule)
		if err != nil {
			customSourceError(writer, err.Error())
			return
		}
		writeJSON(writer, record)
		return
	case http.MethodDelete:
		if err := customSourceDelete(id); err != nil {
			customSourceError(writer, err.Error())
			return
		}
		writeJSON(writer, map[string]any{"ok": true})
		return
	}
	customSourceError(writer, "不支持的方法")
}

func lanAddresses(port int) []string {
	addresses := []string{fmt.Sprintf("127.0.0.1:%d", port)}
	interfaces, err := net.Interfaces()
	if err != nil {
		return addresses
	}
	for _, item := range interfaces {
		if item.Flags&net.FlagUp == 0 || item.Flags&net.FlagLoopback != 0 {
			continue
		}
		values, err := item.Addrs()
		if err != nil {
			continue
		}
		for _, value := range values {
			address, ok := value.(*net.IPNet)
			if !ok || address.IP.To4() == nil || !address.IP.IsPrivate() {
				continue
			}
			addresses = append(addresses, fmt.Sprintf("%s:%d", address.IP.String(), port))
		}
	}
	return addresses
}

func openBrowser(target string) {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	case "darwin":
		command = exec.Command("open", target)
	default:
		command = exec.Command("xdg-open", target)
	}
	_ = command.Start()
}

func main() {
	port := flag.Int("port", 8000, "监听端口")
	host := flag.String("host", "0.0.0.0", "监听地址")
	data := flag.String("data", "", "数据目录")
	open := flag.Bool("open", false, "启动后打开浏览器")
	flag.Parse()

	directory := *data
	if directory == "" {
		if executable, err := os.Executable(); err == nil {
			directory = filepath.Join(filepath.Dir(executable), editionSlug+"-data")
		} else {
			directory = filepath.Join(".", editionSlug+"-data")
		}
	}
	// 核心要求数据目录是绝对路径，-data 传相对路径时先归一化。
	if !filepath.IsAbs(directory) {
		if absolute, err := filepath.Abs(directory); err == nil {
			directory = absolute
		}
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		log.Fatalf("无法创建数据目录 %s：%v", directory, err)
	}
	// 播放记录与收藏跟着数据目录走，删掉数据目录即可一并清空。
	setLibraryDirectory(directory)
	encoded, err := json.Marshal(map[string]any{"action": "initialize", "directory": directory})
	if err != nil {
		log.Fatalf("初始化请求编码失败：%v", err)
	}
	answer := struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}{}
	if err := json.Unmarshal([]byte(core.NativeRequest(string(encoded))), &answer); err != nil || !answer.OK {
		log.Fatalf("本地核心初始化失败：%s", answer.Error)
	}

	site, err := fs.Sub(webAssets, "web")
	if err != nil {
		log.Fatalf("读取内置网页失败：%v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "/web/", http.StatusFound)
	})
	mux.Handle("/web/", http.StripPrefix("/web/", http.FileServer(http.FS(site))))
	mux.HandleFunc("/api/request", handleRequest)
	mux.HandleFunc("/api/play", handlePlay)
	mux.HandleFunc("/api/media/", handleMedia)
	mux.HandleFunc("/api/live/", handleLive)
	mux.HandleFunc("/api/cover", handleCover)
	mux.HandleFunc("/api/sources", handleSources)
	mux.HandleFunc("/api/custom-sources", handleCustomSources)
	mux.HandleFunc("/api/custom-sources/", handleCustomSources)
	mux.HandleFunc("/api/library", handleLibrary)
	mux.HandleFunc("/api/info", func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(writer, map[string]any{
			"name":    editionName(),
			"slug":    editionSlug,
			"version": appVersion,
			"port":    *port,
			"ffmpeg":  ffmpegPath(),
		})
	})
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			transcoder.sweep()
		}
	}()

	address := net.JoinHostPort(*host, strconv.Itoa(*port))
	listener, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatalf("无法监听 %s：%v；可换一个端口，例如 -port 8001", address, err)
	}
	fmt.Println()
	fmt.Printf("%s %s 本地服务已启动\n", editionName(), appVersion)
	fmt.Println("浏览器打开下面任一地址即可使用：")
	for _, item := range lanAddresses(*port) {
		fmt.Printf("  http://%s/web/\n", item)
	}
	fmt.Printf("数据目录：%s\n", directory)
	// 加密的 H.265 站源（如红果）必须靠 ffmpeg 转码才能在浏览器里播放。
	if binary := ffmpegPath(); binary != "" {
		fmt.Printf("已检测到 ffmpeg：%s（加密/HEVC 站源将自动转码播放）\n", binary)
	} else {
		fmt.Println("未检测到 ffmpeg：红果等加密站源无法在浏览器播放，")
		fmt.Println("  安装 ffmpeg 后放到本目录或加入 PATH 并重启即可，其余站源不受影响。")
	}
	fmt.Println("关闭本窗口或按 Ctrl+C 停止服务。")
	fmt.Println()
	if *open {
		openBrowser(fmt.Sprintf("http://127.0.0.1:%d/web/", *port))
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	if err := server.Serve(listener); err != nil {
		log.Fatalf("本地服务已停止：%v", err)
	}
}
