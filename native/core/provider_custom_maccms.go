package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 自定义 maccms 源：用户可在应用内录入符合 MacCMS 模板的站点地址，
// 由本模块在运行时注册为动态站源，套用通用 maccms 解析规则完成列表展示与播放。
// 自定义源以 custom: 前缀作为唯一标识，避免与内置源冲突。

const customSourcePrefix = "custom:"

const customSourceMaxCount = 32

const customSourceMaxBytes = 256 * 1024

type customMaccmsSource struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Base      string    `json:"base"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
}

type customMaccmsRegistry struct {
	mu      sync.RWMutex
	path    string
	sources map[string]customMaccmsSource
}

func newCustomMaccmsRegistry(directory string) *customMaccmsRegistry {
	return &customMaccmsRegistry{
		path:    filepath.Join(directory, "custom_sources.json"),
		sources: map[string]customMaccmsSource{},
	}
}

func (registry *customMaccmsRegistry) load() {
	body, err := os.ReadFile(registry.path)
	if err != nil || len(body) > customSourceMaxBytes {
		return
	}
	var records []customMaccmsSource
	if json.Unmarshal(body, &records) != nil {
		return
	}
	registry.mu.Lock()
	registry.sources = map[string]customMaccmsSource{}
	for _, record := range records {
		record.ID = strings.TrimSpace(record.ID)
		record.Base = strings.TrimRight(strings.TrimSpace(record.Base), "/")
		record.Name = strings.TrimSpace(record.Name)
		if !validCustomMaccmsRecord(record) {
			continue
		}
		if _, exists := registry.sources[record.ID]; exists {
			continue
		}
		registry.sources[record.ID] = record
	}
	registry.mu.Unlock()
}

func (registry *customMaccmsRegistry) save() error {
	registry.mu.RLock()
	records := make([]customMaccmsSource, 0, len(registry.sources))
	for _, record := range registry.sources {
		records = append(records, record)
	}
	registry.mu.RUnlock()
	body, err := json.Marshal(records)
	if err != nil || len(body) > customSourceMaxBytes {
		return err
	}
	return writeNativeCacheFile(registry.path, body)
}

func (registry *customMaccmsRegistry) all() []customMaccmsSource {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	records := make([]customMaccmsSource, 0, len(registry.sources))
	for _, record := range registry.sources {
		records = append(records, record)
	}
	return records
}

func (registry *customMaccmsRegistry) get(id string) (customMaccmsSource, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	record, found := registry.sources[id]
	return record, found
}

func (registry *customMaccmsRegistry) add(name, base string) (customMaccmsSource, error) {
	name = strings.TrimSpace(name)
	base = strings.TrimSpace(base)
	if name == "" || len([]rune(name)) > 40 {
		return customMaccmsSource{}, errors.New("源名称需要 1 至 40 个字符")
	}
	base = strings.TrimRight(base, "/")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return customMaccmsSource{}, errors.New("源网址需为 http 或 https 开头的完整地址")
	}
	if parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
		return customMaccmsSource{}, errors.New("源网址不能包含用户名、参数或锚点")
	}
	if !strings.Contains(parsed.Host, ".") {
		return customMaccmsSource{}, errors.New("源网址域名无效")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if len(registry.sources) >= customSourceMaxCount {
		return customMaccmsSource{}, fmt.Errorf("自定义源最多 %d 个，请先删除再添加", customSourceMaxCount)
	}
	baseKey := strings.ToLower(base)
	for _, record := range registry.sources {
		if strings.ToLower(record.Base) == baseKey {
			return customMaccmsSource{}, errors.New("该网址已存在，请直接编辑或更换")
		}
	}
	id := customMaccmsID(base)
	record := customMaccmsSource{ID: id, Name: name, Base: base, CreatedAt: time.Now()}
	registry.sources[id] = record
	if err := registry.save(); err != nil {
		delete(registry.sources, id)
		return customMaccmsSource{}, err
	}
	return record, nil
}

func (registry *customMaccmsRegistry) remove(id string) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, found := registry.sources[id]; !found {
		return errors.New("自定义源不存在")
	}
	delete(registry.sources, id)
	return registry.save()
}

func (registry *customMaccmsRegistry) replace(id, name, base string) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	id = strings.TrimSpace(id)
	existing, found := registry.sources[id]
	if !found {
		return errors.New("自定义源不存在")
	}
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 40 {
		return errors.New("源名称需要 1 至 40 个字符")
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("源网址需为 http 或 https 开头的完整地址")
	}
	if parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
		return errors.New("源网址不能包含用户名、参数或锚点")
	}
	if !strings.Contains(parsed.Host, ".") {
		return errors.New("源网址域名无效")
	}
	baseKey := strings.ToLower(base)
	for otherID, other := range registry.sources {
		if otherID == id {
			continue
		}
		if strings.ToLower(other.Base) == baseKey {
			return errors.New("该网址已被其他源使用")
		}
	}
	newID := customMaccmsID(base)
	record := customMaccmsSource{ID: newID, Name: name, Base: base, CreatedAt: existing.CreatedAt}
	if newID != id {
		delete(registry.sources, id)
	}
	registry.sources[newID] = record
	return registry.save()
}

func validCustomMaccmsRecord(record customMaccmsSource) bool {
	if record.ID == "" || record.Name == "" || record.Base == "" {
		return false
	}
	if !strings.HasPrefix(record.ID, customSourcePrefix) {
		return false
	}
	parsed, err := url.Parse(record.Base)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func customMaccmsID(base string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimRight(base, "/"))))
	return customSourcePrefix + hex.EncodeToString(sum[:8])
}

func isCustomMaccmsSource(source string) bool {
	return strings.HasPrefix(canonicalProviderSource(source), customSourcePrefix)
}

func (engine *nativeEngine) customMaccmsSources() []customMaccmsSource {
	return engine.customRegistry.all()
}

func (engine *nativeEngine) addCustomMaccmsSource(name, base string) (customMaccmsSource, error) {
	return engine.customRegistry.add(name, base)
}

func (engine *nativeEngine) removeCustomMaccmsSource(id string) error {
	return engine.customRegistry.remove(id)
}

func (engine *nativeEngine) updateCustomMaccmsSource(id, name, base string) (customMaccmsSource, error) {
	if err := engine.customRegistry.replace(id, name, base); err != nil {
		return customMaccmsSource{}, err
	}
	record, found := engine.customRegistry.get(customMaccmsID(base))
	if !found {
		record, found = engine.customRegistry.get(id)
		if !found {
			return customMaccmsSource{}, errors.New("自定义源不存在")
		}
	}
	return record, nil
}

// resolveCustomMaccmsSource 供站源注册表回退，返回指定自定义源的规格。
func customMaccmsSpecFor(id string) (duanjuSourceSpec, bool) {
	engine := nativeEngineSnapshot()
	if engine == nil {
		return duanjuSourceSpec{}, false
	}
	record, found := engine.customRegistry.get(id)
	if !found {
		return duanjuSourceSpec{}, false
	}
	return duanjuSourceSpec{ID: record.ID, Name: record.Name, Base: record.Base, Kind: "maccms", Searcher: true, Paged: true}, true
}

// nativeEngineSnapshot 读取全局引擎实例，仅用于注册表查询。
func nativeEngineSnapshot() *nativeEngine {
	nativeState.Lock()
	defer nativeState.Unlock()
	return nativeState.engine
}
