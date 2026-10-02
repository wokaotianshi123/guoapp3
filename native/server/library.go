package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 播放记录与收藏存在数据目录下，换浏览器、换设备访问同一台机器都能看到。
const (
	libraryFile    = "library.json"
	historyLimit   = 300
	favoriteLimit  = 600
	maxTitleLength = 256
	maxCoverLength = 2048
)

type libraryEntry struct {
	ID           string  `json:"id"`
	Source       string  `json:"source"`
	SourceName   string  `json:"sourceName"`
	Title        string  `json:"title"`
	Cover        string  `json:"cover"`
	Episodes     string  `json:"episodes"`
	Index        int     `json:"index"`
	EpisodeTitle string  `json:"episodeTitle"`
	Position     float64 `json:"position"`
	Duration     float64 `json:"duration"`
	UpdatedAt    int64   `json:"updatedAt"`
}

type library struct {
	Favorites []libraryEntry `json:"favorites"`
	History   []libraryEntry `json:"history"`
}

var (
	libraryMu     sync.Mutex
	libraryPath   string
	libraryLoaded bool
	libraryCache  library
)

func setLibraryDirectory(directory string) {
	libraryMu.Lock()
	libraryPath = filepath.Join(directory, libraryFile)
	libraryMu.Unlock()
}

func entryKey(entry libraryEntry) string {
	return strings.TrimSpace(entry.Source) + "|" + strings.TrimSpace(entry.ID)
}

func clampText(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

// sanitize 只保留页面需要的字段，顺手限制长度，避免客户端塞进超长封面地址。
func sanitize(entry libraryEntry) libraryEntry {
	clean := libraryEntry{
		ID:           clampText(entry.ID, 128),
		Source:       clampText(entry.Source, 64),
		SourceName:   clampText(entry.SourceName, 64),
		Title:        clampText(entry.Title, maxTitleLength),
		Cover:        clampText(entry.Cover, maxCoverLength),
		Episodes:     clampText(entry.Episodes, 32),
		EpisodeTitle: clampText(entry.EpisodeTitle, maxTitleLength),
		Index:        entry.Index,
		Position:     entry.Position,
		Duration:     entry.Duration,
		UpdatedAt:    entry.UpdatedAt,
	}
	if clean.Index < 0 {
		clean.Index = 0
	}
	if clean.Position < 0 {
		clean.Position = 0
	}
	if clean.Duration < 0 {
		clean.Duration = 0
	}
	if clean.Title == "" {
		clean.Title = "未命名"
	}
	if clean.UpdatedAt <= 0 {
		clean.UpdatedAt = time.Now().UnixMilli()
	}
	return clean
}

func loadLibrary() library {
	libraryMu.Lock()
	defer libraryMu.Unlock()
	if libraryLoaded {
		return cloneLibrary(libraryCache)
	}
	data := library{Favorites: []libraryEntry{}, History: []libraryEntry{}}
	if libraryPath != "" {
		if raw, err := os.ReadFile(libraryPath); err == nil {
			var parsed library
			if json.Unmarshal(raw, &parsed) == nil {
				data.Favorites = sanitizeList(parsed.Favorites, favoriteLimit)
				data.History = sanitizeList(parsed.History, historyLimit)
			}
		}
	}
	sort.SliceStable(data.History, func(i, j int) bool {
		return data.History[i].UpdatedAt > data.History[j].UpdatedAt
	})
	libraryCache = cloneLibrary(data)
	libraryLoaded = true
	return data
}

func sanitizeList(items []libraryEntry, limit int) []libraryEntry {
	clean := []libraryEntry{}
	if len(items) > limit {
		items = items[:limit]
	}
	for _, item := range items {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		clean = append(clean, sanitize(item))
	}
	return clean
}

func cloneLibrary(data library) library {
	clone := library{Favorites: []libraryEntry{}, History: []libraryEntry{}}
	clone.Favorites = append(clone.Favorites, data.Favorites...)
	clone.History = append(clone.History, data.History...)
	return clone
}

func saveLibrary(data library) error {
	libraryMu.Lock()
	defer libraryMu.Unlock()
	libraryCache = cloneLibrary(data)
	libraryLoaded = true
	if libraryPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(libraryPath), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	part := libraryPath + ".tmp"
	if err := os.WriteFile(part, body, 0o644); err != nil {
		return err
	}
	// 先写临时文件再改名，避免中途退出把收藏夹写坏。
	return os.Rename(part, libraryPath)
}

func applyLibrary(data library, list string, op string, entry libraryEntry, keys []string) library {
	target := data.Favorites
	if list == "history" {
		target = data.History
	}
	switch op {
	case "clear":
		target = []libraryEntry{}
	case "remove":
		wanted := map[string]bool{}
		for _, key := range keys {
			wanted[key] = true
		}
		kept := []libraryEntry{}
		for _, item := range target {
			if wanted[entryKey(item)] {
				continue
			}
			kept = append(kept, item)
		}
		target = kept
	case "toggle":
		key := entryKey(entry)
		kept := []libraryEntry{}
		removed := false
		for _, item := range target {
			if entryKey(item) == key {
				removed = true
				continue
			}
			kept = append(kept, item)
		}
		if !removed {
			kept = append([]libraryEntry{sanitize(entry)}, kept...)
		}
		target = kept
	default: // put：同剧只保留一条，并移到最前面。
		key := entryKey(entry)
		kept := []libraryEntry{sanitize(entry)}
		for _, item := range target {
			if entryKey(item) == key {
				continue
			}
			kept = append(kept, item)
		}
		target = kept
	}
	if list == "history" {
		data.History = trimList(target, historyLimit)
	} else {
		data.Favorites = trimList(target, favoriteLimit)
	}
	return data
}

func trimList(items []libraryEntry, limit int) []libraryEntry {
	if len(items) > limit {
		return items[:limit]
	}
	return items
}

func libraryKeys(value any) []string {
	keys := []string{}
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				keys = append(keys, text)
			}
		}
	case string:
		if strings.TrimSpace(typed) != "" {
			keys = append(keys, typed)
		}
	}
	return keys
}

func handleLibrary(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodGet {
		data := loadLibrary()
		writeJSON(writer, map[string]any{"ok": true, "data": data})
		return
	}
	if request.Method != http.MethodPost {
		http.Error(writer, "只接受 GET 或 POST 请求", http.StatusMethodNotAllowed)
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
	list, _ := payload["list"].(string)
	if list != "history" && list != "favorites" {
		http.Error(writer, "list 只能是 history 或 favorites", http.StatusBadRequest)
		return
	}
	op, _ := payload["op"].(string)
	if op == "" {
		op = "put"
	}
	if op != "put" && op != "remove" && op != "clear" && op != "toggle" {
		http.Error(writer, "不支持的操作", http.StatusBadRequest)
		return
	}
	entry := libraryEntry{}
	if raw, ok := payload["item"]; ok && raw != nil {
		if encoded, err := json.Marshal(raw); err == nil {
			_ = json.Unmarshal(encoded, &entry)
		}
	}
	keys := libraryKeys(payload["keys"])
	if len(keys) == 0 {
		keys = libraryKeys(payload["key"])
	}
	if op == "put" || op == "toggle" {
		if strings.TrimSpace(entry.ID) == "" {
			http.Error(writer, "缺少剧集 id", http.StatusBadRequest)
			return
		}
	}
	// 一次只允许一个写操作，免得并发上报把彼此的修改覆盖掉。
	data := loadLibrary()
	data = applyLibrary(data, list, op, entry, keys)
	if err := saveLibrary(data); err != nil {
		http.Error(writer, "收藏夹写入失败："+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(writer, map[string]any{"ok": true, "data": data})
}
