package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func newCustomTestEngine(t *testing.T) *nativeEngine {
	t.Helper()
	return &nativeEngine{customRegistry: newCustomMaccmsRegistry(t.TempDir())}
}

func TestCustomSourceExportImportRoundTrip(t *testing.T) {
	engine := newCustomTestEngine(t)
	if _, err := engine.addCustomMaccmsSource("我的影院", "https://my.example.com", ""); err != nil {
		t.Fatalf("添加失败: %v", err)
	}
	if _, err := engine.addCustomMaccmsSource("", "", xbpqCutRuleJSON); err != nil {
		t.Fatalf("规则源添加失败: %v", err)
	}
	content, err := engine.exportCustomMaccmsSources()
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	var envelope struct {
		Format string `json:"format"`
		Sites  []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
			Rule string `json:"rule"`
		} `json:"sites"`
	}
	if err := json.Unmarshal([]byte(content), &envelope); err != nil {
		t.Fatalf("导出内容不是 JSON: %v", err)
	}
	if envelope.Format != customSourceExportFormat || len(envelope.Sites) != 2 {
		t.Fatalf("导出结构错误: %+v", envelope)
	}
	// 规则必须原样随行
	var ruleSite *struct {
		Name string `json:"name"`
		URL  string `json:"url"`
		Rule string `json:"rule"`
	}
	for index := range envelope.Sites {
		if envelope.Sites[index].Rule != "" {
			ruleSite = &envelope.Sites[index]
		}
	}
	if ruleSite == nil || !strings.Contains(ruleSite.Rule, "主页url") {
		t.Fatal("导出未包含 XBPQ 规则原文")
	}
	// 新引擎导入同一份导出文件
	fresh := newCustomTestEngine(t)
	result, err := fresh.importCustomMaccmsSources(content)
	if err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if len(result.Added) != 2 || len(result.Failed) != 0 {
		t.Fatalf("导入结果错误: added=%d failed=%v skipped=%v", len(result.Added), result.Failed, result.Skipped)
	}
	for _, record := range result.Added {
		if strings.Contains(record.ID, " ") {
			t.Fatalf("导入条目 ID 非法: %+v", record)
		}
	}
	// 二次导入：同网址同规则 → 走更新而不是报错重复
	again, err := fresh.importCustomMaccmsSources(content)
	if err != nil {
		t.Fatalf("二次导入失败: %v", err)
	}
	if len(again.Updated) != 2 || len(again.Added) != 0 || len(again.Failed) != 0 {
		t.Fatalf("二次导入应全部更新: %+v", again)
	}
}

func TestCustomSourceImportPlainArrayAndTxt(t *testing.T) {
	engine := newCustomTestEngine(t)
	// 纯数组（字符串行）
	result, err := engine.importCustomMaccmsSources(`["https://a.example.com", "https://b.example.com"]`)
	if err != nil {
		t.Fatalf("数组导入失败: %v", err)
	}
	if len(result.Added) != 2 {
		t.Fatalf("数组导入数量错误: %+v", result)
	}
	// 纯文本每行一个，带名称前缀
	result2, err := engine.importCustomMaccmsSources("C站,https://c.example.com\nhttps://d.example.com\n# 注释行\ngarbage line here")
	if err != nil {
		t.Fatalf("文本导入失败: %v", err)
	}
	if len(result2.Added) != 2 {
		t.Fatalf("文本导入数量错误: %+v", result2)
	}
	names := map[string]bool{}
	for _, record := range result2.Added {
		names[record.Name] = true
	}
	if !names["C站"] {
		t.Fatalf("名称前缀未解析: %v", names)
	}
}

func TestCustomSourceImportTVBoxSites(t *testing.T) {
	engine := newCustomTestEngine(t)
	content := `{
	  "sites": [
		{"key": "cntv", "name": "💕推荐采集", "type": 1, "api": "https://collect.example.com/api.php/provide/vod/"},
		{"key": "sp", "name": "爬虫站", "type": 3, "jar": "csp_XBPQ", "api": "https://sp.example.com"},
		{"key": "xbpq", "name": "规则站", "type": 1, "api": "https://x.example.com", "ext": {"主页url": "https://x.example.com", "分类url": "https://x.example.com/{cateId}-{catePg}.html", "分类": "电影$1#电视剧$2", "数组": "<a&&</a>", "标题": "title=\"&&\"", "链接": "href=\"&&\""}}
	  ]
	}`
	result, err := engine.importCustomMaccmsSources(content)
	if err != nil {
		t.Fatalf("TVBox 导入失败: %v", err)
	}
	if len(result.Added) != 2 {
		t.Fatalf("期望 2 条成功: %+v", result)
	}
	if len(result.Skipped) != 1 || !strings.Contains(result.Skipped[0], "jar") {
		t.Fatalf("爬虫条目应跳过: %+v", result.Skipped)
	}
	var ruleRecord *customMaccmsSource
	for index := range result.Added {
		if result.Added[index].Rule != "" {
			ruleRecord = &result.Added[index]
		}
	}
	if ruleRecord == nil {
		t.Fatal("ext 规则未识别")
	}
	if ruleRecord.Base != "https://x.example.com" {
		t.Fatalf("规则源 base 错误: %q", ruleRecord.Base)
	}
	// 采集站收敛到根地址
	var apiRecord *customMaccmsSource
	for index := range result.Added {
		if result.Added[index].Rule == "" {
			apiRecord = &result.Added[index]
		}
	}
	if apiRecord == nil || apiRecord.Base != "https://collect.example.com" {
		t.Fatalf("api 地址未收敛: %+v", result.Added)
	}
}

func TestCustomSourceImportGarbageFails(t *testing.T) {
	engine := newCustomTestEngine(t)
	if _, err := engine.importCustomMaccmsSources("hello world 不是站点"); err == nil {
		t.Fatal("垃圾内容应整体失败")
	}
	if _, err := engine.importCustomMaccmsSources(""); err == nil {
		t.Fatal("空内容应失败")
	}
}
