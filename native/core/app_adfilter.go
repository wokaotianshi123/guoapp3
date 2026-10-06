package core

// m3u8 广告分片过滤（去广告）。
//
// 只处理媒体分片清单（media playlist，含 #EXTINF）；master 多码率清单
// 原样放行，由上层逐变体各自处理。判定思路：
//   1. 收集所有媒体分片（#EXTINF 等注释行 + 紧随的 URI 行）；
//   2. 为每个分片算「来源指纹」= 片段绝对地址的 host+目录：
//      绝对 URL 用自身；相对 URL 解析到主清单后取主清单的 host+目录；
//   3. 按数量选出出现最多的「主指纹」；若主指纹占比 < 40%，
//      认为结构不可信（多半是切片命名差异而非插播），放弃清洗；
//   4. 丢弃指纹≠主指纹的分片及其前置注释行。
// 广告插播的常见形态是主时间线中夹带来自另一 host/目录的片段，
// 上述规则即可稳定剔除。纯前置贴片（整段同来源）无法与正片区分，不猜删。

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	masterVariantTag = regexp.MustCompile(`(?i)^#EXT-X-(STREAM-INF|MEDIA:|I-FRAME-STREAM-INF|IMAGE-STREAM-INF)`)
	m3u8SegmentTag   = regexp.MustCompile(`(?i)^#EXTINF:`)
	// adTagLine 与参考实现（Player.tsx fetchAndCleanM3u8）一致：删除广告分片时
	// 只回收这四种分片级标签。播放列表级头部注释（VERSION / TARGETDURATION /
	// MEDIA-SEQUENCE / PLAYLIST-TYPE / MAP 等）必须保留——广告若是贴片时它们
	// 就挂在第一个分片前面，误删会让清单失去必需要素而整体无法播放。
	adTagLine = regexp.MustCompile(`(?i)^#EXT(INF:|-X-(BYTERANGE|KEY|DISCONTINUITY))`)
)

// nativeAdCleaned 按会话开关决定是否清洗；关闭或清洗无收益时原样返回。
func nativeAdCleaned(session *nativeStreamSession, text, baseURL string) string {
	if session == nil || !session.adBlock {
		return text
	}
	cleaned, removed := nativeFilterAdSegments(text, baseURL)
	if removed == 0 {
		return text
	}
	return cleaned
}

// nativeFilterAdSegments 清洗 m3u8 媒体清单，返回清洗文本与移除分片数。
// removed==0 时返回原文，调用方据此判断是否需要改写。
func nativeFilterAdSegments(text, baseURL string) (string, int) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "#EXTM3U" {
		return text, 0
	}
	baseFP := "-"
	if parsed, err := url.Parse(baseURL); err == nil {
		if fp := m3u8HostDir(parsed); fp != "" {
			baseFP = fp
		}
	}
	// hasExtinf 用于确认是媒体清单；master 清单没有分片级 #EXTINF 后紧跟 URI。
	type segment struct {
		comment []int  // 归属该分片的注释行下标（含 #EXTINF、BYTERANGE、DISCONTINUITY 等）
		uri     int    // 分片 URI 行下标
		fp      string // 来源指纹
	}
	var (
		segments  []segment
		pending   []int
		sawExtINF bool
		isMaster  bool
	)
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			// 空行不归属任何分片，保持原样即可（不参与删除）。
		case strings.HasPrefix(trimmed, "#"):
			if masterVariantTag.MatchString(trimmed) {
				isMaster = true
			}
			if m3u8SegmentTag.MatchString(trimmed) {
				sawExtINF = true
			}
			pending = append(pending, index)
		default:
			if isMaster || !sawExtINF {
				return text, 0 // master 或没有分片级标注：不改写
			}
			fp := m3u8LineFingerprint(trimmed, baseURL, baseFP)
			if fp == "" {
				fp = baseFP
			}
			segments = append(segments, segment{comment: pending, uri: index, fp: fp})
			pending = nil
		}
		if isMaster {
			return text, 0
		}
	}
	if len(segments) < 2 {
		return text, 0
	}
	counts := map[string]int{}
	for _, seg := range segments {
		counts[seg.fp]++
	}
	dominant, dominantCount := "", 0
	for fp, count := range counts {
		if count > dominantCount {
			dominant, dominantCount = fp, count
		}
	}
	if dominant == "" || float64(dominantCount)/float64(len(segments)) < 0.4 {
		return text, 0
	}
	remove := map[int]bool{}
	removed := 0
	for _, seg := range segments {
		if seg.fp == dominant {
			continue
		}
		removed++
		remove[seg.uri] = true
		// 从最靠近分片的注释行向上回溯（与参考实现一致）：
		// 命中分片级标签（EXTINF/BYTERANGE/KEY/DISCONTINUITY）→ 删除并继续；
		// 普通注释（# 开头但非 #EXT）→ 保留但继续；
		// 其它 #EXT 头部标签（VERSION/TARGETDURATION/MAP…）→ 停止。
		// 广告若是贴片时头部注释就挂在第一个分片前，误删会让清单整体失效。
		for index := len(seg.comment) - 1; index >= 0; index-- {
			line := strings.TrimSpace(lines[seg.comment[index]])
			if adTagLine.MatchString(line) {
				remove[seg.comment[index]] = true
				continue
			}
			if strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "#EXT") {
				continue
			}
			break
		}
	}
	if removed == 0 || removed >= len(segments) {
		return text, 0
	}
	output := make([]string, 0, len(lines)-removed)
	for index, line := range lines {
		if !remove[index] {
			output = append(output, line)
		}
	}
	cleaned := strings.Join(output, "\n")
	// 清洗后若再无任何 #EXTINF（极端情况）则回退原文，宁可不删也不黑屏。
	if !strings.Contains(cleaned, "#EXTINF") {
		return text, 0
	}
	return cleaned, removed
}

// m3u8LineFingerprint 计算一行 URI 的来源指纹：绝对地址用自身 host+目录，
// 相对地址解析到 baseURL 后取 host+目录（通常等于 baseFP）。
func m3u8LineFingerprint(reference, baseURL, baseFP string) string {
	reference = strings.TrimSpace(reference)
	if reference == "" || strings.HasPrefix(reference, "data:") {
		return ""
	}
	parsed, err := url.Parse(reference)
	if err != nil {
		return ""
	}
	if parsed.Scheme != "" && parsed.Host != "" {
		return m3u8HostDir(parsed)
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return baseFP
	}
	return m3u8HostDir(base.ResolveReference(parsed))
}

// m3u8HostDir 返回 host+目录 指纹（丢文件名与 query，同目录视为同源）。
func m3u8HostDir(parsed *url.URL) string {
	if parsed == nil || parsed.Host == "" {
		return ""
	}
	dir := strings.ToLower(parsed.EscapedPath())
	if slash := strings.LastIndex(dir, "/"); slash >= 0 {
		dir = dir[:slash]
	} else {
		dir = ""
	}
	return strings.ToLower(parsed.Host) + "|" + dir
}
