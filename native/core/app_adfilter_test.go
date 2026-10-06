package core

import (
	"strconv"
	"strings"
	"testing"
)

func m3u8HeaderLines(duration int) []string {
	return []string{"#EXTM3U", "#EXT-X-VERSION:3", "#EXT-X-TARGETDURATION:" + strconv.Itoa(duration)}
}

func TestAdFilterRemovesForeignSegments(t *testing.T) {
	var builder []string
	builder = append(builder, m3u8HeaderLines(10)...)
	builder = append(builder, "#EXT-X-MEDIA-SEQUENCE:0")
	for index := 0; index < 8; index++ {
		builder = append(builder, "#EXTINF:10.0,", "https://cdn.main.com/vod/"+strconv.Itoa(index)+".ts")
	}
	// 插播广告：两个来自另一目录的分片
	builder = append(builder, "#EXT-X-DISCONTINUITY", "#EXTINF:5.0,", "https://cdn.main.com/ads/1.ts")
	builder = append(builder, "#EXTINF:5.0,", "https://cdn.main.com/ads/2.ts")
	for index := 8; index < 12; index++ {
		builder = append(builder, "#EXTINF:10.0,", "https://cdn.main.com/vod/"+strconv.Itoa(index)+".ts")
	}
	builder = append(builder, "#EXT-X-ENDLIST")
	original := strings.Join(builder, "\n")
	cleaned, removed := nativeFilterAdSegments(original, "https://cdn.main.com/vod/index.m3u8")
	if removed != 2 {
		t.Fatalf("应移除 2 个广告分片，实际 %d:\n%s", removed, cleaned)
	}
	if !strings.Contains(cleaned, "/vod/0.ts") || !strings.Contains(cleaned, "/vod/11.ts") {
		t.Fatalf("主分片不应被删:\n%s", cleaned)
	}
	if strings.Contains(cleaned, "/ads/") {
		t.Fatalf("广告分片未被移除:\n%s", cleaned)
	}
	if strings.Contains(cleaned, "#EXT-X-DISCONTINUITY") {
		t.Fatalf("广告段前置标签应一并移除:\n%s", cleaned)
	}
	if !strings.Contains(cleaned, "#EXT-X-ENDLIST") {
		t.Fatalf("尾部标签应保留:\n%s", cleaned)
	}
}

func TestAdFilterRelativeSegments(t *testing.T) {
	var builder []string
	builder = append(builder, m3u8HeaderLines(10)...)
	for index := 0; index < 5; index++ {
		builder = append(builder, "#EXTINF:10.0,", "seg/"+strconv.Itoa(index)+".ts")
	}
	original := strings.Join(builder, "\n")
	// 相对地址与主清单同源，不该被删
	cleaned, removed := nativeFilterAdSegments(original, "https://host.example/vod/index.m3u8")
	if removed != 0 || cleaned != original {
		t.Fatalf("全同源不应改动: removed=%d\n%s", removed, cleaned)
	}
}

func TestAdFilterMasterUntouched(t *testing.T) {
	master := strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-STREAM-INF:BANDWIDTH=2000000,RESOLUTION=1280x720",
		"720p.m3u8",
		"#EXT-X-STREAM-INF:BANDWIDTH=4000000,RESOLUTION=1920x1080",
		"1080p.m3u8",
	}, "\n")
	cleaned, removed := nativeFilterAdSegments(master, "https://host.example/vod/index.m3u8")
	if removed != 0 || cleaned != master {
		t.Fatalf("master 清单应原样放行: removed=%d\n%s", removed, cleaned)
	}
}

func TestAdFilterLowDominanceSkip(t *testing.T) {
	var builder []string
	builder = append(builder, m3u8HeaderLines(10)...)
	// 三种来源各 3 片，主来源占比 3/9=33% < 40%，应放弃清洗
	for index := 0; index < 3; index++ {
		builder = append(builder, "#EXTINF:10.0,", "https://a.example/x/"+strconv.Itoa(index)+".ts")
	}
	for index := 0; index < 3; index++ {
		builder = append(builder, "#EXTINF:10.0,", "https://b.example/x/"+strconv.Itoa(index)+".ts")
	}
	for index := 0; index < 3; index++ {
		builder = append(builder, "#EXTINF:10.0,", "https://c.example/x/"+strconv.Itoa(index)+".ts")
	}
	original := strings.Join(builder, "\n")
	cleaned, removed := nativeFilterAdSegments(original, "https://a.example/x/index.m3u8")
	if removed != 0 || cleaned != original {
		t.Fatalf("主来源占比 <40%% 时应放弃清洗: removed=%d", removed)
	}
}

func TestAdFilterKeyPropagation(t *testing.T) {
	var builder []string
	builder = append(builder, m3u8HeaderLines(10)...)
	builder = append(builder, `#EXT-X-KEY:METHOD=AES-128,URI="https://k.example/main.key"`)
	for index := 0; index < 6; index++ {
		builder = append(builder, "#EXTINF:10.0,", "https://cdn.main.com/vod/"+strconv.Itoa(index)+".ts")
	}
	// 广告分片自带另一个 KEY，应连同前置标签一起删
	builder = append(builder, "#EXT-X-DISCONTINUITY")
	builder = append(builder, `#EXT-X-KEY:METHOD=AES-128,URI="https://k.example/ad.key"`)
	builder = append(builder, "#EXTINF:5.0,", "https://cdn.main.com/ads/1.ts")
	for index := 6; index < 10; index++ {
		builder = append(builder, "#EXTINF:10.0,", "https://cdn.main.com/vod/"+strconv.Itoa(index)+".ts")
	}
	cleaned, removed := nativeFilterAdSegments(strings.Join(builder, "\n"), "https://cdn.main.com/vod/index.m3u8")
	if removed != 1 {
		t.Fatalf("应移除 1 个广告分片，实际 %d:\n%s", removed, cleaned)
	}
	if strings.Contains(cleaned, "ad.key") {
		t.Fatalf("广告 KEY 应一并移除:\n%s", cleaned)
	}
	if !strings.Contains(cleaned, "main.key") {
		t.Fatalf("主 KEY 应保留:\n%s", cleaned)
	}
}

func TestAdFilterNativeAdCleanedRespectsSwitch(t *testing.T) {
	var builder []string
	builder = append(builder, m3u8HeaderLines(10)...)
	for index := 0; index < 6; index++ {
		builder = append(builder, "#EXTINF:10.0,", "https://cdn.main.com/vod/"+strconv.Itoa(index)+".ts")
	}
	builder = append(builder, "#EXTINF:5.0,", "https://cdn.main.com/ads/1.ts")
	original := strings.Join(builder, "\n")
	// 开关关闭：原样返回
	if got := nativeAdCleaned(&nativeStreamSession{adBlock: false}, original, "https://cdn.main.com/vod/index.m3u8"); got != original {
		t.Fatalf("关闭去广告时不应改写:\n%s", got)
	}
	// 开关开启：广告被清洗
	if got := nativeAdCleaned(&nativeStreamSession{adBlock: true}, original, "https://cdn.main.com/vod/index.m3u8"); strings.Contains(got, "/ads/") {
		t.Fatalf("开启去广告时应移除广告:\n%s", got)
	}
	// 空会话安全
	if got := nativeAdCleaned(nil, original, "https://cdn.main.com/vod/index.m3u8"); got != original {
		t.Fatalf("空会话应原样返回:\n%s", got)
	}
}
