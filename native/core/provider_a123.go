package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// A123（a123tv.com）原生站源：自命名 w4 模板的全站影视聚合站，
// 覆盖电影 / 连续剧 / 综艺 / 动漫，与 maccms 三大模板家族（indexphp、myui、myui 自定义前缀）
// 均不同，故独立成族。
//
// 上游页面（实测 2026-10）：
//   GET /                     首页推荐（w4-item 卡片，作为"全部"目录第 1 页）
//   GET /t/{id}.html          分类第 1 页；GET /t/{id}/p{n}.html 分类翻页
//   GET /s/{关键词}.html       搜索（服务端渲染，URL 编码中文；结果同样用 w4-item 卡片）
//   GET /v/{slug}.html        详情页（w4-player 默认线路首集直链、选集列表、线路切换块）
//   GET /v/{slug}/{code}.html 播放页（var pp={"no":slug,"ld":线路码,"la":[[码,线路名,集数,?,m3u8],…]}）
//
// 目录卡片是 <a class="w4-item" href="/v/{slug}.html">，封面取 figure 里 img[data-src]（可能是 // 协议相对），
// 标题取 info 区 div.t 的 title，备注取 div.i（"类型 / 年份"）。剧集 slug 是影片名的拼音串，直接当 sourceID。
//
// 取流：每个剧集播放页内嵌 var pp.la 表——每条线路一元的 m3u8 直链，无需云解析。
// 详情里先拿默认线路（w4-player data-src）建分集（章节 VideoURL 记播放页地址），
// 播放时再抓当集播放页解析 pp.la：data-src 优先（当集实际地址），再按线路码前缀匹配 la 行，
// 并把其余线路同集地址装进 Variants，播放器可逐条切换线路。

const a123Name = "A123"

var a123SiteBaseURL = "https://a123tv.com"

const a123UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

var a123Categories = []nativeCategory{
	{ID: "10", Name: "电影"},
	{ID: "11", Name: "连续剧"},
	{ID: "12", Name: "综艺"},
	{ID: "13", Name: "动漫"},
}

var (
	a123CardPattern = regexp.MustCompile(`(?is)<a[^>]*class="w4-item"[^>]*href="/v/([^"/]+)\.html"[^>]*>(.*?)</a>`)
	a123AttrHREF    = regexp.MustCompile(`(?i)href="([^"]+)"`)
	a123AttrSrc     = regexp.MustCompile(`(?i)(?:data-src|src)="([^"]+)"`)
	a123AttrTitle   = regexp.MustCompile(`(?i)title="([^"]*)"`)
	a123AttrAlt     = regexp.MustCompile(`(?i)alt="([^"]*)"`)
	a123InfoTitle   = regexp.MustCompile(`(?is)<div class="t" title="([^"]*)"[^>]*>([^<]*)</div>`)
	a123InfoMeta    = regexp.MustCompile(`(?is)<div class="i"[^>]*>(.*?)</div>`)
	a123PlayerSrc    = regexp.MustCompile(`(?is)<div[^>]*class="w4-player"[^>]*data-src="([^"]+)"`)
	a123PlayerPoster = regexp.MustCompile(`(?is)<div[^>]*class="w4-player"[^>]*data-poster="([^"]+)"`)
	a123PlayerTitle  = regexp.MustCompile(`(?is)<div[^>]*class="w4-player"[^>]*data-title="([^"]*)"`)
	a123DetailTitle = regexp.MustCompile(`(?is)<h1[^>]*>([^<]+)</h1>`)
	// 选集列表：<div class="w4-episode-list …">…<a href="/v/slug/xxx.html" title="…">…</a>…
	a123EpisodeBlock = regexp.MustCompile(`(?is)<div[^>]*class="w4-episode-list[^"]*"[^>]*>(.*?)(?:<h2|</div>\s*</div>\s*</div>|$)`)
	a123EpisodeLink  = regexp.MustCompile(`(?is)<a[^>]*href="(/v/[^"/]+/[^"]+\.html)"[^>]*>`)
	a123PPBlock      = regexp.MustCompile(`(?is)var\s+pp\s*=\s*(\{.*?\})\s*;`)
)

type a123RouteEntry struct {
	Code    string
	Name    string
	Count   int
	Address string
}

func validA123Category(category string) bool {
	if category == "" {
		return true
	}
	if !webProviderNumericID.MatchString(category) || len(category) > 8 {
		return false
	}
	for _, entry := range a123Categories {
		if entry.ID == category {
			return true
		}
	}
	return false
}

func a123Slug(raw string) bool {
	if raw == "" || len(raw) > 128 {
		return false
	}
	for _, r := range raw {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func a123FixURL(raw string) string {
	value := html.UnescapeString(strings.TrimSpace(raw))
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "//") {
		return "https:" + value
	}
	if strings.HasPrefix(value, "/") {
		return a123SiteBaseURL + value
	}
	return value
}

func (d *Downloader) a123Get(ctx context.Context, path string) (string, error) {
	site := d.providerBaseURL(sourceA123)
	address := path
	if !strings.HasPrefix(address, "http") {
		address = site + path
	}
	pageContext := context.WithValue(ctx, providerTextUserAgentKey{}, a123UserAgent)
	return d.fetchProviderText(pageContext, address, site+"/")
}

// parseA123Cards 解析 w4-item 卡片（目录页与搜索页共用）。
func parseA123Cards(body string, category string) []Drama {
	var items []Drama
	seen := map[string]bool{}
	for _, match := range a123CardPattern.FindAllStringSubmatch(body, -1) {
		slug := strings.TrimSpace(match[1])
		if !a123Slug(slug) || seen[slug] {
			continue
		}
		block := match[2]
		title := ""
		if named := a123InfoTitle.FindStringSubmatch(block); len(named) > 1 {
			title = html.UnescapeString(strings.TrimSpace(named[1]))
			if title == "" && len(named) > 2 {
				title = html.UnescapeString(strings.TrimSpace(named[2]))
			}
		}
		if title == "" {
			if alt := a123AttrAlt.FindStringSubmatch(block); len(alt) > 1 {
				title = html.UnescapeString(strings.TrimSpace(alt[1]))
			}
		}
		if title == "" || strings.Contains(title, "VIP") && len(title) < 3 {
			continue
		}
		seen[slug] = true
		cover := ""
		if src := a123AttrSrc.FindStringSubmatch(block); len(src) > 1 {
			cover = a123FixURL(src[1])
		}
		remark := ""
		if meta := a123InfoMeta.FindStringSubmatch(block); len(meta) > 1 {
			remark = duanjuPlainText(meta[1])
		}
		drama := Drama{
			ID: providerDramaID(sourceA123, slug), Source: sourceA123, SourceID: slug,
			Title: truncate(title, 512), Name: truncate(title, 512),
			Cover: cover, CoverURL: cover, ChannelName: a123Name,
			Remark: truncate(remark, 64),
		}
		if category != "" {
			drama.CategoryName = category
		}
		items = append(items, drama)
	}
	return items
}

// parseA123PP 从播放页提取内嵌 var pp 数据块（全线路 × 当集的 m3u8 直链表）。
func parseA123PP(body string) ([]a123RouteEntry, string, bool) {
	match := a123PPBlock.FindStringSubmatch(body)
	if len(match) < 2 {
		return nil, "", false
	}
	payload := struct {
		LD string            `json:"ld"`
		LA []json.RawMessage `json:"la"`
	}{}
	if json.Unmarshal([]byte(match[1]), &payload) != nil {
		return nil, "", false
	}
	var routes []a123RouteEntry
	for _, raw := range payload.LA {
		var row []any
		if json.Unmarshal(raw, &row) != nil || len(row) < 5 {
			continue
		}
		code, _ := row[0].(string)
		name, _ := row[1].(string)
		number, _ := row[2].(float64)
		address, _ := row[4].(string)
		if code == "" || !strings.HasPrefix(strings.ToLower(address), "http") {
			continue
		}
		routes = append(routes, a123RouteEntry{Code: code, Name: strings.TrimSpace(name), Count: int(number), Address: address})
	}
	if len(routes) == 0 {
		return nil, payload.LD, false
	}
	return routes, payload.LD, true
}

// a123EpisodeOrderFromTitle 从剧集标题猜集号："第12集" / "HD" / "第20260128期"。
func a123EpisodeOrderFromTitle(title string, fallback int) int {
	if match := regexp.MustCompile(`第\s*(\d{1,6})\s*[集期话部]`).FindStringSubmatch(title); len(match) > 1 {
		if number, err := strconv.Atoi(match[1]); err == nil && number > 0 && number <= 100000 {
			return number
		}
	}
	return fallback
}

// fetchA123CatalogPage 拉取分类或首页一页。category 为空用首页（首页只有 1 页）；
// 翻页地址 /t/{id}/p{n}.html；搜索走 /s/{query}.html。
func (d *Downloader) fetchA123CatalogPage(ctx context.Context, page int, category, query string) ([]Drama, bool, error) {
	if page < 1 || page > 100000 {
		return nil, false, errors.New("A123 目录页码无效")
	}
	if query != "" {
		return d.searchA123(ctx, page, query)
	}
	if !validA123Category(category) {
		return nil, false, errors.New("A123 内容分类无效")
	}
	site := d.providerBaseURL(sourceA123)
	var path string
	categoryName := ""
	switch {
	case category == "" && page == 1:
		path = "/"
	case category == "":
		// 首页无翻页：直接落到电影分类，保证"全部"能持续加载。
		path = fmt.Sprintf("/t/10%s.html", a123PageSuffix(page))
		categoryName = "电影"
	default:
		path = fmt.Sprintf("/t/%s%s.html", category, a123PageSuffix(page))
		for _, entry := range a123Categories {
			if entry.ID == category {
				categoryName = entry.Name
			}
		}
	}
	_ = site
	body, err := d.a123Get(ctx, path)
	if err != nil {
		return nil, false, err
	}
	items := parseA123Cards(body, categoryName)
	if len(items) == 0 {
		return []Drama{}, false, nil
	}
	return items, len(items) >= 24, nil
}

func a123PageSuffix(page int) string {
	if page <= 1 {
		return ""
	}
	return "/p" + strconv.Itoa(page)
}

func (d *Downloader) searchA123(ctx context.Context, page int, query string) ([]Drama, bool, error) {
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 256 {
		return nil, false, errors.New("A123 搜索关键词无效")
	}
	if page > 1 {
		return []Drama{}, false, nil
	}
	body, err := d.a123Get(ctx, "/s/"+url.PathEscape(query)+".html")
	if err != nil {
		return nil, false, err
	}
	return parseA123Cards(body, ""), false, nil
}

// fetchA123Detail 解析详情页：默认线路与选集，建成分集目录。
// 章节 VideoURL 记播放页地址（a123-play:// 前缀），播放时再逐集抓页取流，
// 避免详情阶段展开几十个播放页。
func (d *Downloader) fetchA123Detail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	if !a123Slug(sourceID) {
		return Drama{}, nil, errors.New("A123 影片标识无效")
	}
	site := d.providerBaseURL(sourceA123)
	body, err := d.a123Get(ctx, "/v/"+sourceID+".html")
	if err != nil {
		return Drama{}, nil, err
	}
	title := ""
	if head := a123DetailTitle.FindStringSubmatch(body); len(head) > 1 {
		title = html.UnescapeString(strings.TrimSpace(head[1]))
	}
	if title == "" {
		if player := a123PlayerTitle.FindStringSubmatch(body); len(player) > 1 {
			parts := strings.Split(html.UnescapeString(player[1]), " - ")
			title = strings.TrimSpace(parts[0])
		}
	}
	if title == "" {
		return Drama{}, nil, errors.New("A123 详情缺少影片名称")
	}
	cover := ""
	// 播放器封面取 data-poster（data-src 是当集 m3u8，不能当封面）。
	if poster := a123PlayerPoster.FindStringSubmatch(body); len(poster) > 1 {
		cover = a123FixURL(poster[1])
	}
	remark := ""
	if player := a123PlayerTitle.FindStringSubmatch(body); len(player) > 1 {
		parts := strings.Split(html.UnescapeString(player[1]), " - ")
		// data-title 形如"片名 - 状态 - 线路N"，状态段（HD国语/更新至X集）作为备注。
		if len(parts) >= 2 {
			remark = strings.TrimSpace(parts[1])
		}
	}
	drama := Drama{
		ID: providerDramaID(sourceA123, sourceID), Source: sourceA123, SourceID: sourceID,
		Title: truncate(title, 512), Name: truncate(title, 512),
		Cover: cover, CoverURL: cover, ChannelName: a123Name,
		Remark: truncate(remark, 64),
	}

	block := ""
	if list := a123EpisodeBlock.FindStringSubmatch(body); len(list) > 1 {
		block = list[1]
	}
	if block == "" {
		block = body
	}
	type episode struct {
		path  string
		title string
	}
	var episodes []episode
	seen := map[string]bool{}
	for _, anchor := range a123EpisodeLink.FindAllStringSubmatch(block, -1) {
		link := strings.TrimSpace(anchor[1])
		if !strings.HasPrefix(link, "/v/"+sourceID+"/") || seen[link] {
			continue
		}
		seen[link] = true
		name := ""
		if named := a123AttrTitle.FindStringSubmatch(anchor[0]); len(named) > 1 {
			name = html.UnescapeString(strings.TrimSpace(named[1]))
		}
		if name == "" {
			name = strings.TrimSpace(duanjuPlainText(anchor[0][strings.LastIndex(anchor[0], ">")+1:]))
		}
		episodes = append(episodes, episode{path: link, title: name})
	}
	if len(episodes) == 0 {
		return Drama{}, nil, errors.New("A123 详情缺少选集列表")
	}
	referer := site + "/"
	chapters := make([]Chapter, 0, len(episodes))
	for index, entry := range episodes {
		order := a123EpisodeOrderFromTitle(entry.title, index+1)
		if entry.title == "" {
			entry.title = fmt.Sprintf("第 %d 集", order)
		}
		chapters = append(chapters, Chapter{
			ID:             providerChapterID(sourceA123, sourceID, entry.path),
			Source:         sourceA123,
			Title:          truncate(entry.title, 128),
			CurrentEpisode: rawEpisode(order),
			VideoURL:       "a123-play://" + entry.path,
			PageURL:        site + entry.path,
			Referer:        referer,
		})
	}
	sort.SliceStable(chapters, func(i, j int) bool {
		left, _ := strconv.Atoi(chapters[i].EpisodeString(i + 1))
		right, _ := strconv.Atoi(chapters[j].EpisodeString(j + 1))
		if left == right {
			return chapters[i].PageURL < chapters[j].PageURL
		}
		return left < right
	})
	drama.TotalEpisode, drama.EpisodeCount = len(chapters), len(chapters)
	return drama, chapters, nil
}

// resolveA123Media 抓当集播放页，从 var pp.la 取直连 m3u8；
// 默认线路（data-src / pp.ld）优先，其余线路同集地址作为可切换备选。
func (d *Downloader) resolveA123Media(ctx context.Context, task Task) (providerMedia, error) {
	source, sourceID, valid := splitProviderDramaID(task.DramaID)
	if !valid || source != sourceA123 {
		return providerMedia{}, errors.New("A123 剧集信息无效，请刷新详情")
	}
	pageURL := strings.TrimSpace(task.Chapter.PageURL)
	if pageURL == "" {
		if video := strings.TrimSpace(task.Chapter.VideoURL); strings.HasPrefix(video, "a123-play://") {
			pageURL = d.providerBaseURL(sourceA123) + strings.TrimPrefix(video, "a123-play://")
		}
	}
	if pageURL == "" || !strings.HasPrefix(pageURL, "http") || !strings.Contains(pageURL, "/v/"+sourceID+"/") {
		return providerMedia{}, errors.New("A123 播放分集信息无效，请刷新详情")
	}
	referer := d.providerBaseURL(sourceA123) + "/"
	_ = referer
	body, err := d.a123Get(ctx, pageURL)
	if err != nil {
		return providerMedia{}, err
	}
	var options []providerMedia
	seen := map[string]bool{}
	add := func(address string) {
		address = strings.TrimSpace(address)
		if !isProviderHTTPMediaURL(address) || seen[address] {
			return
		}
		seen[address] = true
		options = append(options, providerMedia{URL: address, Referer: pageURL})
	}
	// 当集实际地址（w4-player data-src）排第一，保证默认线路即当前页可播地址。
	if player := a123PlayerSrc.FindStringSubmatch(body); len(player) > 1 {
		add(a123FixURL(player[1]))
	}
	// 当前线路（pp.ld）对应行其次，其余线路同集地址依次作为可切换备选。
	if routes, currentCode, ok := parseA123PP(body); ok {
		for _, route := range routes {
			if route.Code == currentCode {
				add(route.Address)
			}
		}
		for _, route := range routes {
			if route.Code != currentCode {
				add(route.Address)
			}
		}
	}
	if len(options) == 0 {
		return providerMedia{}, errors.New("A123 该集暂无可用播放地址，请换线路或稍后重试")
	}
	media := options[0]
	media.Variants = options
	return d.prepareWebProviderMedia(ctx, media, a123Name)
}

func (d *Downloader) fetchA123Categories() []nativeCategory {
	return append([]nativeCategory(nil), a123Categories...)
}
