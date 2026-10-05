package core

import (
	"testing"
)

// gzmzpx 风格首页：分类走 /listnews/N.html，详情走 /news/N.html。
const listnewsHomeTemplate = `<html><body>
<nav>
  <a href="/listnews/1.html">电影</a>
  <a href="/listnews/2.html">电视剧</a>
  <a href="/listnews/3.html">动漫</a>
  <a href="/listnews/4.html">综艺</a>
</nav>
<a href="/news/68890.html">名侦探柯南</a>
<a href="/news/25604.html">乘龙怪婿第一季</a>
</body></html>`

func TestMaccmsCustomHomeSignalsListnews(t *testing.T) {
	document := parseHTMLForTest(t, listnewsHomeTemplate)
	categories, typePrefix := maccmsCustomHomeSignals(document)
	if typePrefix != "listnews" {
		t.Fatalf("typePrefix 期望 listnews，实际 %q", typePrefix)
	}
	got := map[string]string{}
	for _, signal := range categories {
		got[signal.ID] = signal.Name
	}
	for id, name := range map[string]string{"1": "电影", "2": "电视剧", "3": "动漫", "4": "综艺"} {
		if got[id] != name {
			t.Errorf("分类 %s 名称错误: %q", id, got[id])
		}
	}
	if len(categories) == 0 {
		t.Fatal("未从 /listnews/ 导航识别到任何分类")
	}
}

func TestMaccmsCustomHomeDetailPrefix(t *testing.T) {
	document := parseHTMLForTest(t, listnewsHomeTemplate)
	if prefix := maccmsCustomHomeDetailPrefix(document, "listnews"); prefix != "news" {
		t.Fatalf("详情前缀期望 news，实际 %q", prefix)
	}
}

func TestValidCustomCategoryPathForm(t *testing.T) {
	src := "custom:abcdef0123456789"
	// 路径形式分类 ID 应被放行。
	if !validNativeCategory(src, "/listnews/2") {
		t.Error("validNativeCategory 应接受自定义源的路径形式分类 ID")
	}
	if !validDuanjuCategory(src, "/fenlei/1") {
		t.Error("validDuanjuCategory 应接受自定义源的路径形式分类 ID")
	}
	// 缓存键分隔符 | 仍应被拒绝。
	if validNativeCategory(src, "a|b") {
		t.Error("含 | 的分类 ID 仍应被拒绝")
	}
	// 内置源的路径形式仍应被拒绝（不被自定义分支放行）。
	if validNativeCategory(sourceHuangguoVideo, "1/2") {
		t.Error("内置源的路径形式分类 ID 仍应被拒绝")
	}
}
