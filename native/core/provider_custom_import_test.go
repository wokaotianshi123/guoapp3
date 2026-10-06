package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newCustomTestEngine(t *testing.T) *nativeEngine {
	t.Helper()
	return &nativeEngine{customRegistry: newCustomMaccmsRegistry(t.TempDir())}
}

func newCustomTestEngineWithDownloader(t *testing.T, client *http.Client) *nativeEngine {
	t.Helper()
	return &nativeEngine{
		customRegistry: newCustomMaccmsRegistry(t.TempDir()),
		downloader: &Downloader{
			cfg:           Config{dataDir: t.TempDir(), Retries: 1},
			client:        client,
			limiter:       newRequestLimiter(3, 0),
			providerHosts: map[string]string{},
		},
	}
}

func TestCustomSourceImportTVBoxMessyJSON(t *testing.T) {
	engine := newCustomTestEngine(t)
	// 真实 TVBox 配置常见形态：行注释、尾逗号、字段值里裸制表符/换行
	content := `{
	// 注释一
	"sites": [
		{
			"key": "ok",
			"name": "站点甲",
			"type": 1,
			"api": "https://a.example.com/api.php/provide/vod/",
			"note": "带	制表符	的值",
		},
		{
			"key": "tab",
			"name": "站点乙",   "type": 1,
			"api": "https://b.example.com/api.php/provide/vod/",
			"searchable": 1,
		},
		{
			"key": "esc",
			"name": "站点丙",
			"type": 1,
			"api": "https://c.example.com/api.php/provide/vod/",
			"note": "class=\'video\'&&</ul>",
		},
	],
}`
	result, err := engine.importCustomMaccmsSources(content)
	if err != nil {
		t.Fatalf(" messy TVBox 导入失败: %v", err)
	}
	if len(result.Added) != 3 {
		t.Fatalf("应导入 3 站，实际 %+v", result.Added)
	}
	if result.Added[0].Base != "https://a.example.com" {
		t.Fatalf("api 应收敛为站点根地址: %s", result.Added[0].Base)
	}
	if result.Added[2].Base != "https://c.example.com" {
		t.Fatalf("含非法转义 \\' 的条目应导入: %s", result.Added[2].Base)
	}
}

func TestCustomSourceRemoteURLImport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/config/app.json" {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"sites":[{"key":"a","name":"远程站","type":1,"url":"https://remote.example.com"}]}`))
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()
	engine := newCustomTestEngineWithDownloader(t, server.Client())
	// 带 .json 后缀的远程地址：应抓取后解析
	result, err := engine.importCustomMaccmsSources(server.URL + "/config/app.json")
	if err != nil {
		t.Fatalf("远程导入失败: %v", err)
	}
	if len(result.Added) != 1 || result.Added[0].Name != "远程站" {
		t.Fatalf("远程导入结果错误: %+v", result)
	}
	// 站点根地址不触发抓取（当单站导入处理）
	result2, err := engine.importCustomMaccmsSources("https://plain.example.com")
	if err != nil {
		t.Fatalf("站点根地址导入失败: %v", err)
	}
	if len(result2.Added) != 1 || result2.Added[0].Base != "https://plain.example.com" {
		t.Fatalf("站点根地址应按单站导入: %+v", result2.Added)
	}
	// 无法访问的远程地址应报错而不是静默
	if _, err := engine.importCustomMaccmsSources(server.URL + "/missing.json"); err == nil {
		t.Fatal("404 远程地址应报错")
	}
}

func TestCustomSourceResetRestoresBuiltInOnly(t *testing.T) {
	engine := newCustomTestEngine(t)
	if _, err := engine.addCustomMaccmsSource("站一", "https://one.example.com", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.addCustomMaccmsSource("站二", "https://two.example.com", ""); err != nil {
		t.Fatal(err)
	}
	removed, err := engine.resetCustomMaccmsSources()
	if err != nil {
		t.Fatalf("恢复默认失败: %v", err)
	}
	if removed != 2 {
		t.Fatalf("应移除 2 个: %d", removed)
	}
	if len(engine.customMaccmsSources()) != 0 {
		t.Fatal("恢复默认后仍有残留")
	}
	// 持久化文件也应清空：重载后依然为空
	reloaded := newCustomMaccmsRegistry("")
	reloaded.path = engine.customRegistry.path
	reloaded.load()
	if len(reloaded.all()) != 0 {
		t.Fatal("恢复默认未落盘")
	}
	// 恢复后可以继续正常添加
	if _, err := engine.addCustomMaccmsSource("站三", "https://three.example.com", ""); err != nil {
		t.Fatalf("恢复后添加失败: %v", err)
	}
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
