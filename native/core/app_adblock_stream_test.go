package core

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestNativeStreamAdBlockEndToEnd 验证「去广告」贯穿流媒体代理：
// 同一份带插播广告的内存清单，adBlock=true 会话返回清洗后的改写结果、
// adBlock=false 会话返回含广告的完整结果。
func TestNativeStreamAdBlockEndToEnd(t *testing.T) {
	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stream, err := newNativeStreamServer(engine.downloader)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.server.Close()
	var builder strings.Builder
	builder.WriteString("#EXTM3U\n#EXT-X-TARGETDURATION:10\n")
	for index := 0; index < 6; index++ {
		builder.WriteString("#EXTINF:10.0,\n")
		builder.WriteString("https://cdn.example.test/vod/seg" + string(rune('0'+index)) + ".ts\n")
	}
	builder.WriteString("#EXT-X-DISCONTINUITY\n#EXTINF:5.0,\nhttps://cdn.example.test/ads/ad1.ts\n")
	builder.WriteString("#EXTINF:5.0,\nhttps://cdn.example.test/ads/ad2.ts\n")
	for index := 6; index < 10; index++ {
		builder.WriteString("#EXTINF:10.0,\n")
		builder.WriteString("https://cdn.example.test/vod/seg" + string(rune('0'+index)) + ".ts\n")
	}
	builder.WriteString("#EXT-X-ENDLIST\n")
	media := providerMedia{
		URL:      "https://cdn.example.test/vod/index.m3u8",
		Playlist: builder.String(),
		Referer:  "https://example.test/watch",
	}
	fetch := func(address string) string {
		response, err := http.Get(address)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("状态码 %d", response.StatusCode)
		}
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	cleanAddress, cleanToken := stream.nativeOpen(media, true)
	defer stream.nativeRelease(cleanToken)
	cleaned := fetch(cleanAddress)
	if strings.Contains(cleaned, ".ts") == false {
		t.Fatalf("清洗后的清单应仍有分片（代理地址）:\n%s", cleaned)
	}
	if strings.Count(cleaned, ".ts\n") != 10 {
		t.Fatalf("应有 10 个主分片的代理地址:\n%s", cleaned)
	}
	dirtyAddress, dirtyToken := stream.nativeOpen(media, false)
	defer stream.nativeRelease(dirtyToken)
	dirty := fetch(dirtyAddress)
	if strings.Count(dirty, ".ts\n") != 12 {
		t.Fatalf("关闭去广告应有全部 12 个分片:\n%s", dirty)
	}
}
