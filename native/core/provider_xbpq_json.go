package core

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
)

// 本文件实现 XBPQ 自学笔记 item 7「json 模式」与 item 8「Base64」：
//   - 字段值以 j: 前缀（或纯点路径）描述 JSON 取值，如 j:data.list[0].name；
//   - 下标 0-based（底层 fastjson JSONPath 与实测规则 data.urls[0] 一致；
//     笔记「最小下标为 1」的表述与实现不符，以实测规则为准）；
//   - 数组下标可写 [n]（第 n 个）、[]（全部）、[a,]（跳过前 a 个）；
//   - 二次截取填 "Base64" 表示整段只解码；Base64(a&&b) 对截取结果解码。

// xbpqJSONPathValue 按 json 路径从 JSON 文本取值。
func xbpqJSONPathValue(source, path string) (any, bool) {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "$")
	path = strings.TrimPrefix(path, "j:")
	if source == "" || path == "" {
		return nil, false
	}
	var root any
	if err := json.Unmarshal([]byte(source), &root); err != nil {
		return nil, false
	}
	cur := root
	for _, rawSeg := range strings.Split(path, ".") {
		key, idxText, hasIndex := xbpqJSONCutSegment(rawSeg)
		if key != "" {
			obj, ok := cur.(map[string]any)
			if !ok {
				// 数组上直接取键：对每个元素取键；全取不到则视为对数组本身继续
				// （SVIP 规则形态：二次截取=j:data.list 缩小后 数组=j:list）。
				if arr, isArr := cur.([]any); isArr {
					var picked []any
					for _, one := range arr {
						if o, ok := one.(map[string]any); ok {
							if v, exists := xbpqJSONLookupFold(o, key); exists {
								picked = append(picked, v)
							}
						}
					}
					if len(picked) == 0 {
						continue
					}
					cur = picked
					continue
				}
				return nil, false
			}
			v, exists := xbpqJSONLookupFold(obj, key)
			if !exists {
				return nil, false
			}
			cur = v
		}
		if hasIndex {
			cur = xbpqJSONApplyIndex(cur, idxText)
		}
	}
	return cur, true
}

func xbpqJSONLookupFold(obj map[string]any, key string) (any, bool) {
	if v, ok := obj[key]; ok {
		return v, true
	}
	for k, v := range obj {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return nil, false
}

// xbpqJSONCutSegment 拆 "list[0]" → key="list", index="0"。
func xbpqJSONCutSegment(seg string) (key, index string, hasIndex bool) {
	open := strings.Index(seg, "[")
	if open < 0 {
		return seg, "", false
	}
	key = seg[:open]
	close := strings.LastIndex(seg, "]")
	if close <= open {
		return seg, "", false
	}
	return key, seg[open+1 : close], true
}

// xbpqJSONApplyIndex 对数组应用下标/切片（0-based）：
// ""/"*"→全部；"n"→第 n 个；"a,"→自 a 起；"a-b"/"a:b"→[a,b)。
func xbpqJSONApplyIndex(value any, expr string) any {
	arr, ok := value.([]any)
	if !ok {
		return value
	}
	expr = strings.TrimSpace(expr)
	if expr == "" || expr == "*" {
		return arr
	}
	// 负数单下标（-1=倒数第一个）先于切片判断处理。
	if n, err := strconv.Atoi(expr); err == nil {
		if n < 0 {
			n += len(arr)
		}
		if n < 0 || n >= len(arr) {
			return nil
		}
		return arr[n]
	}
	if i := strings.IndexAny(expr, "-:,"); i >= 0 {
		left := strings.TrimSpace(expr[:i])
		right := strings.TrimSpace(expr[i+1:])
		start := 0
		end := len(arr)
		if left != "" {
			if n, err := strconv.Atoi(left); err == nil {
				start = xbpqJSONClamp(n, 0, len(arr))
			}
		}
		if right != "" {
			if n, err := strconv.Atoi(right); err == nil {
				end = xbpqJSONClamp(n, 0, len(arr))
			}
		}
		if start >= end {
			return []any{}
		}
		out := make([]any, end-start)
		copy(out, arr[start:end])
		return out
	}
	return arr
}

func xbpqJSONClamp(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

func xbpqJSONStringValue(source, path string) string {
	value, ok := xbpqJSONPathValue(source, path)
	if !ok {
		return ""
	}
	return xbpqStringValue(value)
}

// xbpqJSONPathRaw 路径取到的对象/数组序列化回 JSON 文本（二次截取缩小范围用）。
func xbpqJSONPathRaw(source, path string) string {
	value, ok := xbpqJSONPathValue(source, path)
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any, []any:
		body, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(body)
	default:
		return xbpqStringValue(value)
	}
}

// xbpqJSONArrayItems 路径迭代：数组逐元素序列化；对象返回单条。
func xbpqJSONArrayItems(source, path string) []string {
	value, ok := xbpqJSONPathValue(source, path)
	if !ok {
		return nil
	}
	arr, isArr := value.([]any)
	if !isArr {
		body, err := json.Marshal(value)
		if err != nil {
			return nil
		}
		return []string{string(body)}
	}
	var out []string
	for _, one := range arr {
		body, err := json.Marshal(one)
		if err != nil {
			continue
		}
		out = append(out, string(body))
	}
	return out
}

// xbpqJSONLikely source 是否 JSON 文档。
func xbpqJSONLikely(source string) bool {
	for _, r := range source {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		return r == '{' || r == '['
	}
	return false
}

// xbpqIsJSONPattern pattern 是否 json 模式（j: 前缀或纯点路径）。
func xbpqIsJSONPattern(pattern string) bool {
	if pattern == "" {
		return false
	}
	if strings.HasPrefix(pattern, "j:") || strings.HasPrefix(pattern, "$") {
		return true
	}
	if strings.ContainsAny(pattern, "&\"' ") || strings.Contains(pattern, "://") {
		return false
	}
	if strings.IndexByte(pattern, '.') < 0 {
		return false
	}
	for _, r := range pattern {
		if r == '[' || r == ']' {
			continue
		}
		if !(r == '_' || r == '.' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// xbpqDecodeBase64Segment Base64 解码（失败返回原串）。
func xbpqDecodeBase64Segment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if decoded, err := base64.StdEncoding.DecodeString(s); err == nil && !strings.ContainsRune(string(decoded), 0) {
		return string(decoded)
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(s); err == nil && !strings.ContainsRune(string(decoded), 0) {
		return string(decoded)
	}
	return s
}

// xbpqBase64Wrapper 识别 "Base64(inner)" 形态。
func xbpqBase64Wrapper(pattern string) (string, bool) {
	p := strings.TrimSpace(pattern)
	if !strings.HasPrefix(strings.ToLower(p), "base64(") || !strings.HasSuffix(p, ")") {
		return "", false
	}
	return p[len("Base64(") : len(p)-1], true
}
