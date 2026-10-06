package core

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func parseHTMLForTest(t *testing.T, body string) *html.Node {
	t.Helper()
	document, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("解析测试页面失败: %v", err)
	}
	return document
}

// 完全陌生的模板：class 名不在任何白名单、导航路径形态也没见过。
const genericUnknownTemplate = `<html><body>
<nav>
  <a href="/fenlei/1.html">电影</a>
  <a href="/fenlei/2.html">电视剧</a>
  <a href="/fenlei/3.html">动漫</a>
  <a href="/fenlei/4.html">综艺</a>
  <a href="/fenlei/5.html">短剧</a>
</nav>
<div class="zzz-unknown-1">
  <a href="/movie/101.html"><img data-src="/pic/101.jpg" alt="山河旧梦"><span class="zzz-note">更新至12集</span></a>
</div>
<div class="zzz-unknown-2">
  <a href="/movie/102.html"><img data-src="/pic/102.jpg" alt="长夜将明"><span class="zzz-note">更新至30集</span></a>
</div>
<div class="zzz-unknown-3">
  <a href="/movie/103.html"><img data-src="/pic/103.jpg" alt="春山可望"><span class="zzz-note">完结</span></a>
</div>
<a href="/movie/101-1-1.html">山河旧梦 第1集</a>
<a href="/index.php/vod/search/wd/%E6%98%A5.html">搜索</a>
<a href="https://other.example.com/movie/999.html">外链</a>
<a href="/static/app.css">样式</a>
</body></html>`

func TestMaccmsGenericItemsUnknownTemplate(t *testing.T) {
	document := parseHTMLForTest(t, genericUnknownTemplate)
	items := maccmsGenericItems(document, "custom:test", "https://example.com", 0)
	if len(items) != 3 {
		t.Fatalf("期望抽出 3 条剧集，实际 %d：%v", len(items), itemTitles(items))
	}
	want := map[string]string{
		"101": "山河旧梦",
		"102": "长夜将明",
		"103": "春山可望",
	}
	for _, item := range items {
		if want[item.SourceID] != item.Title {
			t.Errorf("集 %s 标题错误: %q", item.SourceID, item.Title)
		}
		if item.Cover == "" {
			t.Errorf("集 %s 没抽到封面", item.SourceID)
		}
		if item.ID != providerDramaID("custom:test", item.SourceID) {
			t.Errorf("集 %s ID 组装错误: %s", item.SourceID, item.ID)
		}
	}
	for _, item := range items {
		if item.SourceID == "1" || item.SourceID == "999" {
			t.Errorf("导航/外链被误当剧集: %s", item.SourceID)
		}
	}
}

func TestMaccmsGenericItemsRemarkAndEpisodes(t *testing.T) {
	document := parseHTMLForTest(t, genericUnknownTemplate)
	items := maccmsGenericItems(document, "custom:test", "https://example.com", 0)
	remarks := map[string]string{}
	for _, item := range items {
		remarks[item.SourceID] = item.Remark
	}
	if !strings.Contains(remarks["101"], "12") {
		t.Errorf("101 集数备注错误: %q", remarks["101"])
	}
	// 完结也应当出现在备注里（供集数解析使用）。
	if remarks["103"] == "" {
		t.Errorf("103 备注为空")
	}
}

func TestMaccmsGenericCategoriesUnknownTemplate(t *testing.T) {
	document := parseHTMLForTest(t, genericUnknownTemplate)
	signals := maccmsGenericCategories(document, "https://example.com")
	if len(signals) < 3 {
		t.Fatalf("期望至少抽出 3 个分类，实际 %d", len(signals))
	}
	names := map[string]string{}
	for _, signal := range signals {
		names[signal.ID] = signal.Name
	}
	for id, name := range map[string]string{"/fenlei/1": "电影", "/fenlei/2": "电视剧", "/fenlei/3": "动漫"} {
		if names[id] != name {
			t.Errorf("分类 %s 名称错误: %q", id, names[id])
		}
	}
	for _, signal := range signals {
		if strings.Contains(signal.ID, "movie") {
			t.Errorf("详情链接被误当分类: %s", signal.ID)
		}
	}
}

// 纯文字列表：没有海报，靠「同骨架成批出现」在放宽模式下抽出来。
const genericTextOnlyTemplate = `<html><body>
<div class="q"><a href="/zy/201345.html">一号剧</a></div>
<div class="q"><a href="/zy/201346.html">二号剧</a></div>
<div class="q"><a href="/zy/201347.html">三号剧</a></div>
<div class="q"><a href="/zy/201348.html">四号剧</a></div>
<a href="/other/999.html">孤立链接</a>
</body></html>`

func TestMaccmsGenericItemsTextOnlyFallback(t *testing.T) {
	document := parseHTMLForTest(t, genericTextOnlyTemplate)
	// 严格模式要求有海报，这里应当一条都抽不到。
	if got := maccmsGenericScan(document, "https://example.com", true); len(got) != 0 {
		t.Fatalf("严格模式不应抽出无海报链接，实际 %d 条", len(got))
	}
	items := maccmsGenericItems(document, "custom:test", "https://example.com", 0)
	if len(items) != 4 {
		t.Fatalf("放宽模式期望 4 条，实际 %d：%v", len(items), itemTitles(items))
	}
	for _, item := range items {
		if item.Title == "" || item.SourceID == "" {
			t.Errorf("条目字段缺失: %+v", item)
		}
		// 编号很大的文字列表不应被当成分类导航剔除。
		if item.SourceID == "999" {
			t.Errorf("孤立链接被误收: %+v", item)
		}
	}
}

func TestMaccmsGenericCategoryKey(t *testing.T) {
	cases := []struct {
		path         string
		skeleton     string
		id           string
		categoryPath string
		wantFound    bool
	}{
		{"/vodtype/2.html", "/vodtype/{id}", "2", "/vodtype/2", true},
		{"/vodshow/1-----------.html", "/vodshow/{id}", "1", "/vodshow/1", true},
		{"/index.php/vod/type/id/3.html", "/index.php/vod/type/id/{id}", "3", "/index.php/vod/type/id/3", true},
		{"/fenlei/7.html", "/fenlei/{id}", "7", "/fenlei/7", true},
		{"/voddetail/123.html", "", "", "", false},
		{"/", "", "", "", false},
	}
	for _, item := range cases {
		skeleton, id, categoryPath, found := maccmsGenericCategoryKey(item.path)
		if found != item.wantFound || skeleton != item.skeleton || id != item.id || categoryPath != item.categoryPath {
			t.Errorf("分类键解析错误 %s -> (%q,%q,%q,%v)", item.path, skeleton, id, categoryPath, found)
		}
	}
}

func TestMaccmsGenericIsNoisePath(t *testing.T) {
	noise := []string{"/vodplay/1-1-1.html", "/index.php/vod/search/wd/x.html", "/vodtype/2.html", "/static/a.js", "/index.php/vod/type/id/1.html"}
	for _, path := range noise {
		if !maccmsGenericIsNoisePath(path, true) {
			t.Errorf("应当判为噪声: %s", path)
		}
	}
	keep := []string{"/voddetail/123.html", "/index.php/vod/detail/id/123.html", "/movie/45.html"}
	for _, path := range keep {
		if maccmsGenericIsNoisePath(path, true) {
			t.Errorf("不应当判为噪声: %s", path)
		}
	}
}

func itemTitles(items []Drama) string {
	var out []string
	for _, item := range items {
		out = append(out, item.SourceID+"="+item.Title)
	}
	return strings.Join(out, ", ")
}

// TestMaccmsCategoryNavLinkRejectsNavPaths 固化 iqiyizyapi 类站点的修复：
// 首页混排的分类导航（/index.php/vod/type/id/N.html、/vodtype/N.html、
// /vod/show/id、/show 筛选、page 分页）不得被当成剧集卡片；
// 真详情形态（/vod/detail/id、/voddetail、/show/N.html 详情）必须放行。
func TestMaccmsCategoryNavLinkRejectsNavPaths(t *testing.T) {
	category := []string{
		"https://iqiyizyapi.com/index.php/vod/type/id/7.html",
		"https://iqiyizyapi.com/index.php/vod/type/page/1.html",
		"https://iqiyizyapi.com/vodtype/2.html",
		"https://iqiyizyapi.com/xksitype/3.html",
		"https://iqiyizyapi.com/index.php/vod/show/id/8/page/1.html",
		"https://iqiyizyapi.com/index.php/vod/show/page/2.html",
		"https://iqiyizyapi.com/vodshow/1-----------.html",
	}
	for _, link := range category {
		if !maccmsCategoryNavLink(link) {
			t.Errorf("分类导航应被识别: %s", link)
		}
	}
	detail := []string{
		"https://iqiyizyapi.com/index.php/vod/detail/id/86188.html",
		"https://iqiyizyapi.com/voddetail/123.html",
		"https://example.com/vodshow/123.html",
		"https://example.com/vod/123.html",
		"https://example.com/detail/123.html",
	}
	for _, link := range detail {
		if maccmsCategoryNavLink(link) {
			t.Errorf("详情链接不应被误判为分类: %s", link)
		}
	}
}

// TestMaccmsCardsSkipsCategoryNavOnIqiyiLikeHome 验证非标准模板首页上
// 「分类导航被剔除、剧集卡片被保留」（对应 iqiyizyapi 首页形态）。
func TestMaccmsCardsSkipsCategoryNavOnIqiyiLikeHome(t *testing.T) {
	body := `<ul class="nav">` +
		`<li><a href="/index.php/vod/type/id/7.html">电影</a></li>` +
		`<li><a href="/index.php/vod/type/id/8.html">连续剧</a></li>` +
		`</ul>` +
		`<ul><li><a class="this-link flex" href="/index.php/vod/detail/id/86188.html" title="联邦调查局第九季"><h5>联邦调查局第九季</h5></a></li>` +
		`<li><a class="this-link flex" href="/index.php/vod/detail/id/7887.html" title="遮天"><h5>遮天</h5></a></li></ul>`
	document, err := html.Parse(strings.NewReader(`<html><body>` + body + `</body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	cards := maccmsCards(document, "custom:dbg", "https://iqiyizyapi.com")
	if len(cards) != 2 {
		t.Fatalf("应只剩 2 个剧集卡片，实际 %d: %+v", len(cards), titlesOf(cards))
	}
	for _, c := range cards {
		if c.SourceID == "7" || c.SourceID == "8" {
			t.Fatalf("分类导航混入列表: %+v", c)
		}
	}
}

func titlesOf(items []Drama) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Title)
	}
	return out
}
