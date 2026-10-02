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
	{"hanxiaoquan", "韩小圈"},
	{"guipian", "鬼片"},
	{"sorani", "青空"},
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
	{"piguo", "皮果"},
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
	reply := core.NativeRequest(string(encoded))
	if match := localMediaAddress.FindStringSubmatch(reply); match != nil {
		mediaPort.Store(match[1])
		// 播放地址指向本机播放服务，改成经由本服务转发的相对路径，
		// 这样本机与局域网设备用的是同一个地址。
		for _, prefix := range []string{
			"http://127.0.0.1:" + match[1] + "/",
			"http://localhost:" + match[1] + "/",
		} {
			reply = strings.ReplaceAll(reply, prefix, "/api/media/")
		}
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(writer, reply)
}

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
		encoded, err := json.Marshal(map[string]any{"action": "sourceStatus", "source": source.ID})
		if err != nil {
			continue
		}
		answer := struct {
			OK bool `json:"ok"`
		}{}
		if json.Unmarshal([]byte(core.NativeRequest(string(encoded))), &answer) == nil && answer.OK {
			items = append(items, map[string]string{"id": source.ID, "name": source.Name})
		}
	}
	writeJSON(writer, map[string]any{"items": items})
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
	mux.HandleFunc("/api/media/", handleMedia)
	mux.HandleFunc("/api/cover", handleCover)
	mux.HandleFunc("/api/sources", handleSources)
	mux.HandleFunc("/api/info", func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(writer, map[string]any{
			"name":    editionName(),
			"slug":    editionSlug,
			"version": appVersion,
			"port":    *port,
		})
	})

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
