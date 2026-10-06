package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// wwgz 风格规则：截取语法（A&&B）、播放数组 mac_url='&&'、$$$ 线路。
const xbpqCutRuleJSON = `{"主页url":"https://vip.example.com:5200","分类url":"https://vip.example.com:5200/vod-list-id-{cateId}-pg-{catePg}-order--by--class-0-year-0-letter--area--lang-.html","分类":"电影$1#电视剧$2#综艺$3#动漫$4","数组":"<a class=\"item\"&&</a>","标题":"title=\"&&\"","图片":"data-original=\"&&\"","链接":"href=\"&&\"","副标题":"class=\"updated\">&&</div>","播放数组":"mac_url='&&'","播放列表":"#","播放标题":"&&$","播放链接":"$&&"}`

const xbpqListPage = `<html><body>
<a class="item" href="/detail/88.html" title="山河样本"><img data-original="/pic/88.jpg"><div class="updated">更新至12集</div></a>
<a class="item" href="/detail/89.html" title="长夜样本"><img data-original="/pic/89.jpg"><div class="updated">完结</div></a>
<a class="item" href="/other/90.html"></a>
</body></html>`

const xbpqDetailPage = `<html><body><h1>山河样本</h1>
<script>
var mac_url='第1集$http://cdn.example.com/1.m3u8#第2集$http://cdn.example.com/2.m3u8$$$线路2$http://cdn.example.com/alt.m3u8';
</script>
</body></html>`

func xbpqFixtureServer(t *testing.T, handler http.HandlerFunc) (*Downloader, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	d := &Downloader{
		cfg:           Config{dataDir: t.TempDir(), Retries: 1, PageSize: 30, MaxPagesPerSort: 1},
		client:        server.Client(),
		limiter:       newRequestLimiter(3, 0),
		providerHosts: map[string]string{},
	}
	return d, server
}

func TestXBPQRuleParsesCutStyle(t *testing.T) {
	rule, ok := parseXBPQRule(xbpqCutRuleJSON)
	if !ok {
		t.Fatal("截取风格规则解析失败")
	}
	if home := rule.homeURL(); home != "https://vip.example.com:5200" {
		t.Fatalf("主页url 解析错误: %q", home)
	}
	categories := xbpqRuleCategories(rule)
	if len(categories) != 4 || categories[0].ID != "1" || categories[0].Name != "电影" || categories[3].Name != "动漫" {
		t.Fatalf("分类解析错误: %+v", categories)
	}
	pageURL := xbpqCategoryURL(rule, "https://vip.example.com:5200", "2", 3)
	if !strings.Contains(pageURL, "vod-list-id-2-pg-3") {
		t.Fatalf("分类url 占位符未填充: %q", pageURL)
	}
}

func TestXBPQCutCatalog(t *testing.T) {
	d, server := xbpqFixtureServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = writer.Write([]byte(xbpqListPage))
	})
	rule, _ := parseXBPQRule(xbpqCutRuleJSON)
	d.providerMu.Lock()
	d.providerHosts["custom:xbpqtest"] = server.URL
	d.providerMu.Unlock()
	items, more, err := d.xbpqCatalog(context.Background(), "custom:xbpqtest", server.URL, 1, "1", rule)
	if err != nil {
		t.Fatalf("xbpq 目录失败: %v", err)
	}
	if len(items) != 2 || !more {
		t.Fatalf("期望 2 条（第 3 条无图无 updated 被剔除），实际 %d more=%v", len(items), more)
	}
	if items[0].Title != "山河样本" || !strings.HasPrefix(items[0].SourceID, "u") {
		t.Fatalf("首条错误: %+v", items[0])
	}
	if restored := xbpqLinkFromID(server.URL, items[0].SourceID); restored != server.URL+"/detail/88.html" {
		t.Fatalf("链接型 ID 无法还原详情页: %q", restored)
	}
	if items[0].Cover != server.URL+"/pic/88.jpg" {
		t.Fatalf("封面绝对化错误: %q", items[0].Cover)
	}
	if items[0].Remark != "更新至12集" {
		t.Fatalf("副标题错误: %q", items[0].Remark)
	}
}

func TestXBPQCutDetailEpisodes(t *testing.T) {
	d, server := xbpqFixtureServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = writer.Write([]byte(xbpqDetailPage))
	})
	rule, _ := parseXBPQRule(xbpqCutRuleJSON)
	id := xbpqIDFromLink(server.URL+"/detail/88.html", false)
	drama, chapters, err := d.xbpqDetail(context.Background(), "custom:xbpqtest", server.URL, id, rule)
	if err != nil {
		t.Fatalf("xbpq 详情失败: %v", err)
	}
	if drama.DisplayTitle() != "山河样本" {
		t.Fatalf("详情标题错误: %q", drama.Title)
	}
	// 取集数最多的线路：线路1 有 2 集。
	if len(chapters) != 2 || chapters[0].Title != "第1集" || chapters[0].VideoURL != "http://cdn.example.com/1.m3u8" {
		t.Fatalf("分集解析错误: %+v", chapters)
	}
}

// gimyai 风格规则：p: 选择器 + [包含:]/[替换:] 修饰 + 搜索 POST。
const xbpqSelectorRuleJSON = `{
"主页url":"https://sel.example.com",
"分类url":"https://sel.example.com/explore/{cateId}--time------{catePg}---.html",
"分类":"电视剧$2#电影$1#动漫$4",
"数组":"p:div.grid a",
"标题":"p:h3",
"链接":"p:a[href]",
"图片":"p:img[src]",
"副标题":"p:.poster__status",
"搜索url":"https://sel.example.com/search;post;wd={wd}",
"搜索数组":"p:div.search a",
"搜索标题":"p:a",
"搜索链接":"p:a[href]"
}`

const xbpqSelectorPage = `<html><body>
<div class="grid">
  <a href="/detail/201345.html"><h3>一号剧</h3><span class="poster__status">更新至8集</span></a>
  <a href="/detail/201346.html"><img src="/pic/2.jpg"><h3>二号剧</h3></a>
</div>
<div class="other"><a href="/ad/1.html">广告</a></div>
</body></html>`

// ffv 风格规则：分类 ID 是站点路径片段而非纯数字。
const xbpqSlugRuleJSON = `{"主页url":"https://ffv.example.com","分类":"电视剧$tv#电影$movie#动漫$cartoon","分类url":"https://ffv.example.com/type/{cateId}/{catePg}/","数组":"<li>&&</li>","列表图片":"data-original=\"&&\"","封面":"data-original=\"&&\"","链接":"href=\"&&\"","标题":"title=\"&&\"","详情url":"https://ffv.example.com/detail/{id}/","播放数组":"mac_url='&&'","播放列表":"#","播放标题":"&&$","播放链接":"$&&"}`

func TestXBPQSlugCategories(t *testing.T) {
	rule, ok := parseXBPQRule(xbpqSlugRuleJSON)
	if !ok {
		t.Fatal("slug 规则解析失败")
	}
	categories := xbpqRuleCategories(rule)
	if len(categories) != 3 || categories[0].ID != "tv" || categories[2].Name != "动漫" {
		t.Fatalf("分类解析错误: %+v", categories)
	}
	if pageURL := xbpqCategoryURL(rule, "https://ffv.example.com", "movie", 2); pageURL != "https://ffv.example.com/type/movie/2/" {
		t.Fatalf("分类url 未填充路径型分类: %q", pageURL)
	}
}

func TestXBPQDetailCoverUsesRuleField(t *testing.T) {
	d, server := xbpqFixtureServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = writer.Write([]byte(`<html><body><div class="cover"><img data-original="/pic/77.jpg"></div><h2>七号剧</h2><script>var mac_url='第1集$http://cdn.example.com/1.m3u8';</script></body></html>`))
	})
	rule, _ := parseXBPQRule(xbpqSlugRuleJSON)
	drama, _, err := d.xbpqDetail(context.Background(), "custom:slugtest", server.URL, "77", rule)
	if err != nil {
		t.Fatalf("xbpq 详情失败: %v", err)
	}
	if drama.Cover != server.URL+"/pic/77.jpg" {
		t.Fatalf("封面未按规则字段提取: %q", drama.Cover)
	}
}

func TestXBPQSelectorCatalog(t *testing.T) {
	d, server := xbpqFixtureServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = writer.Write([]byte(xbpqSelectorPage))
	})
	rule, ok := parseXBPQRule(xbpqSelectorRuleJSON)
	if !ok {
		t.Fatal("选择器风格规则解析失败")
	}
	items, _, err := d.xbpqCatalog(context.Background(), "custom:seltest", server.URL, 1, "2", rule)
	if err != nil {
		t.Fatalf("选择器目录失败: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("期望 2 条，实际 %d: %+v", len(items), items)
	}
	if items[0].Title != "一号剧" || !strings.HasPrefix(items[0].SourceID, "u") {
		t.Fatalf("首条错误: %+v", items[0])
	}
	// 规则未给「详情url」模板，链接应整链编码，详情阶段可无损还原
	if restored := xbpqLinkFromID(server.URL, items[0].SourceID); restored != server.URL+"/detail/201345.html" {
		t.Fatalf("链接型 ID 无法还原详情页: %q", restored)
	}
	if items[1].Cover != server.URL+"/pic/2.jpg" {
		t.Fatalf("图片属性提取错误: %q", items[1].Cover)
	}
}

func TestXBPQSelectorSearchPost(t *testing.T) {
	var sawPost bool
	d, server := xbpqFixtureServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		if request.Method == http.MethodPost {
			sawPost = true
			_ = request.ParseForm()
			if request.Form.Get("wd") != "样本" {
				t.Errorf("POST 搜索参数错误: %q", request.Form.Get("wd"))
			}
			_, _ = writer.Write([]byte(`<html><body><div class="search"><a href="/detail/777.html">搜索样本</a></div></body></html>`))
			return
		}
		_, _ = writer.Write([]byte(xbpqSelectorPage))
	})
	rule, _ := parseXBPQRule(xbpqSelectorRuleJSON)
	items, err := d.xbpqSearch(context.Background(), "custom:seltest", server.URL, "样本", rule)
	if err != nil {
		t.Fatalf("选择器搜索失败: %v", err)
	}
	if !sawPost {
		t.Fatal("搜索未走 POST")
	}
	if len(items) != 1 || items[0].Title != "搜索样本" {
		t.Fatalf("搜索结果错误: %+v", items)
	}
	if restored := xbpqLinkFromID(server.URL, items[0].SourceID); restored != server.URL+"/detail/777.html" {
		t.Fatalf("搜索结果 ID 无法还原详情页: %q", restored)
	}
}

func TestXBPQGBKDecodeAndLongText(t *testing.T) {
	if got := xbpqDecodeBody("x"); got != "x" {
		t.Fatalf("纯文本直通失败: %q", got)
	}
	// GBK 中文页面必须转成 UTF-8（含「电」字 GBK 编码 D3 B5）。
	gbk := "<meta charset=\"gbk\">" + string([]byte{0xb5, 0xe7, 0xd3, 0xb0}) // 电影
	decoded := xbpqDecodeBody(gbk)
	if !strings.Contains(decoded, "电影") {
		t.Fatalf("GBK 转码失败: %q", decoded)
	}
	// 尾逗号 + @long-text 合并
	text := `{"主页url":"https://a.example.com","分类":"电影$1","分类url":"https://a.example.com/{cateId}-{catePg}.html",}
@long-text:"{\"播放数组\":\"mac_url='&&'\"}";`
	rule, ok := parseXBPQRule(text)
	if !ok {
		t.Fatal("long-text 规则解析失败")
	}
	if rule.field("播放数组") != "mac_url='&&'" {
		t.Fatalf("long-text 合并失败: %+v", rule.fields)
	}
	if got := xbpqCategoryURL(rule, "https://a.example.com", "1", 2); got != "https://a.example.com/1-2.html" {
		t.Fatalf("long-text 分类url 渲染错误: %q", got)
	}
}

func TestXBPQCustomSourceRegistration(t *testing.T) {
	registry := newCustomMaccmsRegistry(t.TempDir())
	record, err := registry.add("", "", xbpqCutRuleJSON)
	if err != nil {
		t.Fatalf("规则源录入失败: %v", err)
	}
	if record.Base != "https://vip.example.com:5200" {
		t.Fatalf("规则源主页地址未提取: %q", record.Base)
	}
	if record.Name != "vip.example.com:5200" {
		t.Fatalf("规则源默认名称错误: %q", record.Name)
	}
	if record.Rule == "" {
		t.Fatal("规则未持久化")
	}
	reloaded := newCustomMaccmsRegistry(t.TempDir())
	reloaded.path = registry.path
	reloaded.load()
	restored, found := reloaded.get(record.ID)
	if !found || restored.Rule != record.Rule {
		t.Fatal("规则源重载后规则丢失")
	}
}
