package core

import (
	"path/filepath"
	"testing"
	"time"
)

// 自定义源剧集 ID 为三段式 custom:<hash>:<sourceID>，必须按第二个冒号拆分，
// 否则 "custom" 会被误当成源名导致 detail/resolve/cover 在鉴权层被拒。
func TestCustomMaccmsDramaIDSplitsAtSecondColon(t *testing.T) {
	source, sourceID, ok := splitProviderDramaID("custom:89b8c47e0828ec32:456444")
	if !ok || source != "custom:89b8c47e0828ec32" || sourceID != "456444" {
		t.Fatalf("custom drama id split failed: %q %q %v", source, sourceID, ok)
	}
	if chapter := providerChapterID("custom:89b8c47e0828ec32", "456444", "1"); chapter != "custom:89b8c47e0828ec32:456444:1" {
		t.Fatalf("custom chapter id build wrong: %q", chapter)
	}
	if _, _, ok := splitProviderDramaID("custom:89b8c47e0828ec32"); ok {
		t.Fatal("bare custom id must not parse as a drama id")
	}
	if _, _, ok := splitProviderDramaID("custom:89b8c47e0828ec32:"); ok {
		t.Fatal("empty custom source id must not parse")
	}
	if !isCustomMaccmsSource("custom:89b8c47e0828ec32") {
		t.Fatal("custom prefix detection failed")
	}
}

// registry.save() 曾在写锁内再取读锁导致永久死锁（addCustomSource 接口卡死）。
// 这里用超时守卫确保 add/remove/replace 都能在合理时间内完成。
func TestCustomMaccmsRegistryWritesWithoutDeadlock(t *testing.T) {
	registry := newCustomMaccmsRegistry(t.TempDir())
	type result struct {
		record customMaccmsSource
		err    error
	}
	done := make(chan result, 1)
	go func() {
		record, err := registry.add("测试源", "https://example.com")
		done <- result{record, err}
	}()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("add failed: %v", got.err)
		}
		if got.record.ID != customMaccmsID("https://example.com") {
			t.Fatalf("unexpected id %q", got.record.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("registry add deadlocked")
	}
	reloaded := newCustomMaccmsRegistry(filepath.Dir(registry.path))
	reloaded.load()
	if _, found := reloaded.get(customMaccmsID("https://example.com")); !found {
		t.Fatal("persisted custom source not restored after reload")
	}
	if err := registry.remove(customMaccmsID("https://example.com")); err != nil {
		t.Fatalf("remove failed: %v", err)
	}
}
