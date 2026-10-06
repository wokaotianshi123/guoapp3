package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/net/html"
)

var (
	// 兼容 player_aaaa（标准 MacCMS/myui）与 player_data（macplus 模板，如 MGMGTV）两种变量名。
	maccmsPlayerData     = regexp.MustCompile(`(?is)player_(?:aaaa|data)\s*=\s*(\{.*?\})\s*(?:</script>|;|$)`)
	maccmsPlayerURLField = regexp.MustCompile(`(?i)"url"\s*:\s*"([^"]+)"`)
	maccmsPlayerFrom     = regexp.MustCompile(`(?i)"from"\s*:\s*"([A-Za-z0-9_]+)"`)
)

type maccmsSourceProfile struct {
	Source      string
	CategoryURL func(base, category string) string
	SearchURL   func(base, query string, page int) string
	PageURL     func(base, category string, page int) string
	Episodes    func(document *html.Node) []providerEpisode
}

// 剧集链接形态：/vod/play/、/index.php/vod/play/、/vodplay/、/xksiplay/ 等自定义前缀、/drama-play、episode_id=。
var maccmsEpisodeLink = regexp.MustCompile(`(?i)/(?:vod/)?play/|/[a-z0-9_]*play/|/drama-play|episode_id=|/nr/|/bofang/|/vplay/|/zplay/|/mplay/`)

// 详情链接形态：/detail/、/index.php/vod/detail/、/voddetail/、/xksidetail/ 等自定义前缀。
var maccmsDetailLink = regexp.MustCompile(`(?i)/(?:index\.php/vod/)?(?:detail|vod|show|view|movie|drama|zywview|xzyxvd)/|[a-z0-9_]*detail/`)

func maccmsEpisodeNodes(document *html.Node) []*html.Node {
	var nodes []*html.Node
	collect := func(match func(*html.Node) bool) {
		for _, list := range providerHTMLNodes(document, match) {
			nodes = append(nodes, providerHTMLNodes(list, func(node *html.Node) bool { return node.Data == "a" })...)
		}
	}
	for _, name := range []string{"content__playlist", "content__list", "playlink", "pcDrama_catalogItem", "catalogItem", "playlist"} {
		collect(maccmsClassMatcher(name))
	}
	if len(nodes) == 0 {
		for _, name := range []string{"tab-pane", "playlist", "playList"} {
			collect(maccmsClassMatcher(name))
		}
	}
	if len(nodes) == 0 {
		nodes = providerHTMLNodes(document, func(node *html.Node) bool {
			return node.Data == "a" && maccmsEpisodeLink.MatchString(providerHTMLAttr(node, "href"))
		})
	}
	return nodes
}

// maccmsEpisodeLinkLikely 过滤播放区里的占位链接（javascript:;、#、下载页等）。
func maccmsEpisodeLinkLikely(link string) bool {
	lower := strings.ToLower(strings.TrimSpace(link))
	if lower == "" || strings.HasPrefix(lower, "javascript") || strings.HasPrefix(lower, "#") ||
		strings.HasPrefix(lower, "mailto") || strings.HasPrefix(lower, "data:") {
		return false
	}
	return strings.HasPrefix(lower, "/") || strings.HasPrefix(lower, "http")
}

func maccmsEpisodesFromDocument(document *html.Node) []providerEpisode {
	var episodes []providerEpisode
	seen := map[string]bool{}
	for index, anchor := range maccmsEpisodeNodes(document) {
		link := strings.TrimSpace(providerHTMLAttr(anchor, "href"))
		title := providerHTMLText(anchor)
		if !maccmsEpisodeLinkLikely(link) {
			continue
		}
		if strings.Contains(title, "APP") || strings.Contains(title, "下载") {
			continue
		}
		key := link
		if seen[key] {
			continue
		}
		seen[key] = true
		episodes = append(episodes, providerEpisode{
			Key:   strconv.Itoa(index + 1),
			Title: title,
			URL:   link,
			Index: index + 1,
		})
	}
	return episodes
}

var (
	// macplus 形态：/index.php/vod/play/id/123/sid/1/nid/2.html
	maccmsRouteSIDNID = regexp.MustCompile(`(?i)sid/(\d+)[^0-9]+nid/(\d+)`)
	// myui 形态：/vodplay/123-1-2.html、/xksiplay/123-1-2.html
	maccmsRouteMyui = regexp.MustCompile(`(\d+)-(\d+)-(\d+)\.html`)
)

// maccmsPlayRoute 从播放页链接解析线路号（sid）与集号（nid），无法识别时返回 0。
func maccmsPlayRoute(link string) (sid int, nid int) {
	if matches := maccmsRouteSIDNID.FindStringSubmatch(link); len(matches) > 2 {
		sid, _ = strconv.Atoi(matches[1])
		nid, _ = strconv.Atoi(matches[2])
		return sid, nid
	}
	if matches := maccmsRouteMyui.FindStringSubmatch(link); len(matches) > 3 {
		sid, _ = strconv.Atoi(matches[2])
		nid, _ = strconv.Atoi(matches[3])
		return sid, nid
	}
	return 0, 0
}

// maccmsCollapseRouteEpisodes 把「线路 × 集数」的平铺列表折叠成单条线路的集数。
// 详情页播放区常把多条线路的链接全部列出（如 10 条线路 × 103 集 = 上千个链接），
// 展开会让详情出现大量重复「第 N 集」；线路属于播放页的切换维度，详情只保留一条线路，
// 其余线路在解析播放地址时作为备选线路返回（见 collectMaccmsRouteVariants）。
func maccmsCollapseRouteEpisodes(episodes []providerEpisode) []providerEpisode {
	if len(episodes) < 2 {
		return episodes
	}
	type routeGroup struct {
		order    int
		seen     map[int]bool
		episodes []providerEpisode
	}
	groups := map[int]*routeGroup{}
	var order []int
	for _, episode := range episodes {
		sid, nid := maccmsPlayRoute(episode.URL)
		if sid <= 0 || nid <= 0 {
			continue
		}
		entry, found := groups[sid]
		if !found {
			entry = &routeGroup{order: len(order), seen: map[int]bool{}}
			groups[sid] = entry
			order = append(order, sid)
		}
		if entry.seen[nid] {
			continue
		}
		entry.seen[nid] = true
		entry.episodes = append(entry.episodes, episode)
	}
	if len(groups) < 2 {
		// 只有一条线路（或无法识别线路结构），保持原有顺序与数量，避免误删分集。
		return episodes
	}
	// 取集数最全的线路；集数相同时取线路号最小的（sid=1 通常是站点默认线路，
	// 详情页的渲染顺序可能被「线路优选」之类的运营位打乱，不适合作为依据）。
	bestSID := order[0]
	best := groups[bestSID]
	for _, sid := range order {
		candidate := groups[sid]
		if len(candidate.episodes) > len(best.episodes) ||
			(len(candidate.episodes) == len(best.episodes) && sid < bestSID) {
			best, bestSID = candidate, sid
		}
	}
	sort.SliceStable(best.episodes, func(i, j int) bool {
		_, left := maccmsPlayRoute(best.episodes[i].URL)
		_, right := maccmsPlayRoute(best.episodes[j].URL)
		return left < right
	})
	for index := range best.episodes {
		best.episodes[index].Index = index + 1
		best.episodes[index].Key = strconv.Itoa(index + 1)
	}
	return best.episodes
}

func maccmsCardCover(card *html.Node, pageURL string) string {
	for _, image := range providerHTMLNodes(card, func(node *html.Node) bool { return node.Data == "img" }) {
		for _, attribute := range []string{"data-original", "data-src", "src"} {
			if address := providerCoverAddress(providerHTMLAttr(image, attribute), pageURL); address != "" {
				return address
			}
		}
		// 部分模板（如 myui）封面写在 img 的 style="background: url(...)" 中。
		if address := maccmsStyleCover(providerHTMLAttr(image, "style"), pageURL); address != "" {
			return address
		}
	}
	for _, anchor := range providerHTMLNodes(card, func(node *html.Node) bool { return node.Data == "a" }) {
		for _, attribute := range []string{"data-original", "data-src"} {
			if address := providerCoverAddress(providerHTMLAttr(anchor, attribute), pageURL); address != "" {
				return address
			}
		}
		// myui 模板封面常挂在 a.myui-vodlist__thumb 的 style="background: url(...)" 上。
		if address := maccmsStyleCover(providerHTMLAttr(anchor, "style"), pageURL); address != "" {
			return address
		}
	}
	return ""
}

var maccmsStyleBackground = regexp.MustCompile(`(?i)background(?:-image)?\s*:\s*url\(\s*['"]?([^'")]+)`)

func maccmsStyleCover(style, pageURL string) string {
	if style == "" {
		return ""
	}
	if matches := maccmsStyleBackground.FindStringSubmatch(style); len(matches) > 1 {
		return providerCoverAddress(matches[1], pageURL)
	}
	return ""
}

func maccmsCardTitle(card *html.Node) string {
	for _, attribute := range []string{"title", "alt"} {
		for _, node := range providerHTMLNodes(card, func(node *html.Node) bool {
			return node.Data == "a" || node.Data == "img"
		}) {
			if text := strings.TrimSpace(providerHTMLAttr(node, attribute)); text != "" {
				return text
			}
		}
	}
	for _, node := range providerHTMLNodes(card, func(node *html.Node) bool { return node.Data == "a" }) {
		if text := providerHTMLText(node); text != "" {
			return text
		}
	}
	return ""
}

func maccmsCardLink(card *html.Node, base string) string {
	for _, anchor := range providerHTMLNodes(card, func(node *html.Node) bool { return node.Data == "a" }) {
		link := providerHTMLAttr(anchor, "href")
		if link == "" || strings.Contains(link, "javascript:") {
			continue
		}
		if address := duanjuAbsolute(base, link); address != "" {
			return address
		}
	}
	return ""
}

func maccmsCardRemark(card *html.Node) string {
	for _, name := range []string{"meta-post-type2", "imagelabel", "module-item-note", "pic-text", "lastChapter", "SecondList_totalChapterNum", "SecondList_bookType"} {
		if node := providerHTMLFirstClass(card, name); node != nil {
			if text := providerHTMLText(node); text != "" {
				return text
			}
		}
	}
	return ""
}

func maccmsCards(document *html.Node, source, base string) []Drama {
	var cards []*html.Node
	seen := map[*html.Node]bool{}
	for _, name := range maccmsCardClasses {
		for _, node := range providerHTMLNodes(document, maccmsClassMatcher(name)) {
			if !seen[node] {
				seen[node] = true
				cards = append(cards, node)
			}
		}
	}
	if len(cards) == 0 {
		cards = maccmsAnchorCards(document)
	}
	var items []Drama
	ids := map[string]bool{}
	for _, card := range cards {
		link := maccmsCardLink(card, base)
		title := maccmsCardTitle(card)
		if link == "" || title == "" {
			continue
		}
		sourceID := maccmsSourceIDFromURL(link)
		if sourceID == "" || ids[sourceID] {
			continue
		}
		ids[sourceID] = true
		remark := maccmsCardRemark(card)
		items = append(items, Drama{
			ID:           providerDramaID(source, sourceID),
			Source:       source,
			SourceID:     sourceID,
			Title:        title,
			Cover:        maccmsCardCover(card, link),
			Remark:       remark,
			EpisodeCount: json.Number(strconv.Itoa(duanjuEpisodeNumber(remark, 0))),
			ChannelName:  duanjuSourceName(source),
		})
	}
	return items
}

func maccmsClassMatcher(name string) func(*html.Node) bool {
	return func(node *html.Node) bool {
		for _, value := range strings.Fields(providerHTMLAttr(node, "class")) {
			if value == name || strings.HasSuffix(value, "-"+name) {
				return true
			}
		}
		return false
	}
}

func maccmsAnchorCards(document *html.Node) []*html.Node {
	var cards []*html.Node
	seen := map[*html.Node]bool{}
	for _, anchor := range providerHTMLNodes(document, func(node *html.Node) bool {
		return node.Data == "a" && maccmsDetailLink.MatchString(providerHTMLAttr(node, "href"))
	}) {
		for parent := anchor.Parent; parent != nil && parent.Type == html.ElementNode; parent = parent.Parent {
			if seen[parent] {
				break
			}
			if parent.Data != "li" && parent.Data != "div" {
				continue
			}
			seen[parent] = true
			cards = append(cards, parent)
		}
	}
	return cards
}

var maccmsCardClasses = []string{
	"col-lg-2", "col-xl-2", "module-item", "module-poster-item", "public-list-box",
	"videoBox", "detail-list-item", "col-md-6", "col-6", "listItem", "FeaturedList_featuredItem",
	"BrowseList_listItem", "SecondList_secondListItem", "vodlist__item", "v_list",
	"entry-wrapper", "TagBookList_tagItem",
	// myui / macplus 模板家族（qikantoukan、xiangmaile、mgmgtv 等）
	"myui-vodlist__box", "macplus-vodlist__bag", "myui-vodlist__thumb", "macplus-vodlist__thumb",
}

var maccmsDetailPath = regexp.MustCompile(`(?i)/(?:voddetail|detail|show|vod|drama|movie|tv)/([0-9]+)(?:[-./]|$)`)

func maccmsSourceIDFromURL(link string) string {
	parsed, err := url.Parse(link)
	if err != nil {
		return ""
	}
	path := strings.TrimSuffix(parsed.Path, ".html")
	if matches := maccmsDetailPath.FindStringSubmatch(path + "/"); len(matches) > 1 {
		return matches[1]
	}
	cleaned := strings.Trim(path, "/")
	parts := strings.Split(cleaned, "/")
	for index := len(parts) - 1; index >= 0; index-- {
		candidate := strings.TrimSuffix(parts[index], ".html")
		if webProviderNumericID.MatchString(candidate) {
			return candidate
		}
		if _, tail, found := strings.Cut(candidate, "-"); found && webProviderNumericID.MatchString(tail) {
			return tail
		}
	}
	if id := parsed.Query().Get("id"); webProviderNumericID.MatchString(id) {
		return id
	}
	return ""
}

func (d *Downloader) fetchMaccmsCatalogPage(ctx context.Context, source string, page int, category string) ([]Drama, bool, error) {
	base := d.duanjuBaseURL(source)
	address := ""
	switch {
	case source == sourceHuaguo:
		if page <= 1 && strings.TrimSpace(category) == "" {
			address = base + "/"
		} else {
			class := strings.TrimSpace(category)
			if class == "" {
				class = "27"
			}
			address = fmt.Sprintf("%s/search.html?page=%d&searchtype=5&tid=%s&year=", base, page, url.QueryEscape(class))
		}
	case source == sourceFaguo:
		class := strings.TrimSpace(category)
		if class == "" {
			address = fmt.Sprintf("%s/xzyxvt/%dzmn.html", base, page)
		} else if strings.Contains(class, "zmn") {
			address = base + strings.SplitN(class, "zmn", 2)[0] + strconv.Itoa(page) + "zmn.html"
		} else {
			return nil, false, errors.New("发果分类无效")
		}
	case source == sourceWuguo:
		class := strings.TrimSpace(category)
		if class == "" {
			address = base + "/"
		} else {
			address = fmt.Sprintf("%s%s/page/%d.html", base, strings.TrimSuffix(class, ".html"), page)
		}
	case source == sourceWangguo:
		class := strings.TrimSpace(category)
		if class == "" {
			address = fmt.Sprintf("%s/show/duanju-----------.html", base)
		} else {
			address = fmt.Sprintf("%s%s%d---.html", base, strings.SplitN(class, "---.html", 2)[0], page)
		}
	case isCustomMaccmsSource(source):
		// 自定义 maccms 站点：按首页信号自动识别模板家族（index.php / myui 系），多候选探测。
		return d.fetchMaccmsCustomCatalog(ctx, source, page, category)
	default:
		return nil, false, errors.New("该站源没有网页目录")
	}
	document, finalURL, err := d.fetchProviderPage(ctx, address, base+"/", duanjuUserAgent)
	if err != nil {
		return nil, false, err
	}
	items := maccmsCards(document, source, base)
	_ = finalURL
	return items, len(items) > 0, nil
}

// fetchMaccmsCustomCatalog 面向自定义 maccms 站点的目录抓取：
// 先取首页（page<=1 且无分类），page>1 或带分类时按模板家族探测出的 URL 形态逐候选尝试。
func (d *Downloader) fetchMaccmsCustomCatalog(ctx context.Context, source string, page int, category string) ([]Drama, bool, error) {
	base := d.duanjuBaseURL(source)
	// XBPQ 规则源：一切按规则字段截取，不走模板识别。
	if rule, ok := customXBPQRule(source); ok {
		return d.xbpqCatalog(ctx, source, base, page, category, rule)
	}
	// 标准 JSON API 优先：命中就不必再猜页面模板。
	if endpoint := maccmsAPIEndpointFor(ctx, d, source, base); endpoint != "" {
		if items, hasMore, err := d.fetchMaccmsAPICatalog(ctx, source, endpoint, base, page, category); err == nil {
			return items, hasMore, nil
		}
	}
	if page <= 1 && strings.TrimSpace(category) == "" {
		document, _, err := d.fetchProviderPage(ctx, base+"/", base+"/", duanjuUserAgent)
		if err != nil {
			return nil, false, err
		}
		items := maccmsCards(document, source, base)
		if len(items) == 0 {
			// 模板 class 不认识时，退到通用链接抽取（扫所有 <a href> 找详情链接）。
			items = maccmsGenericItems(document, source, base, 0)
		}
		return items, len(items) > 0, nil
	}
	profile := maccmsCustomProfileFor(ctx, d, source, base)
	class := strings.TrimSpace(category)
	var candidates []string
	switch {
	case class == "":
		if profile.Family == "myui" {
			candidates = append(candidates, profile.AllPage(base, page))
			candidates = append(candidates, profile.Category(base, "1", page))
		} else {
			candidates = append(candidates, profile.AllPage(base, page))
			candidates = append(candidates, fmt.Sprintf("%s/index.php/vod/show/page/%d.html", base, page))
			candidates = append(candidates, fmt.Sprintf("%s/vodshow/1-----------%d.html", base, page))
			candidates = append(candidates, fmt.Sprintf("%s/xksishow/1-----------%d.html", base, page))
			candidates = append(candidates, fmt.Sprintf("%s/vodtype/1-%d.html", base, page))
		}
	case profile.Family == "myui":
		// myui 系分类 ID 直接是完整路径（如 /vodtype/2.html），需换算成分类编号。
		id := class
		if matches := regexp.MustCompile(`(\d{1,6})\.html$`).FindStringSubmatch(class); len(matches) > 1 {
			id = matches[1]
		}
		candidates = append(candidates, profile.Category(base, id, page))
		candidates = append(candidates, base+strings.TrimSuffix(class, ".html")+"-"+strconv.Itoa(page)+".html")
		candidates = append(candidates, fmt.Sprintf("%s/vodshow/%s-----------%d.html", base, id, page))
	case profile.Family == "listxx":
		// listnews/fenlei/… 系分类 ID 为数字，分页形态 /{prefix}/{id}-{page}.html。
		id := class
		if matches := regexp.MustCompile(`(\d{1,6})\.html$`).FindStringSubmatch(class); len(matches) > 1 {
			id = matches[1]
		} else if matches := regexp.MustCompile(`/(\d{1,6})(?:[-.].*)?\.html$`).FindStringSubmatch(class); len(matches) > 1 {
			id = matches[1]
		}
		if webProviderNumericID.MatchString(id) {
			candidates = append(candidates, profile.Category(base, id, page))
		}
	default:
		id := class
		if matches := regexp.MustCompile(`(\d{1,6})\.html$`).FindStringSubmatch(class); len(matches) > 1 {
			id = matches[1]
		} else if matches := regexp.MustCompile(`/type/id/(\d{1,6})\.html$`).FindStringSubmatch(class); len(matches) > 1 {
			id = matches[1]
		}
		if webProviderNumericID.MatchString(id) {
			candidates = append(candidates, profile.Category(base, id, page))
			candidates = append(candidates, fmt.Sprintf("%s/index.php/vod/type/id/%s/page/%d.html", base, id, page))
			candidates = append(candidates, fmt.Sprintf("%s/vodtype/%s-%d.html", base, id, page))
		} else {
			candidates = append(candidates, base+strings.TrimSuffix(class, ".html")+"/"+strconv.Itoa(page)+".html",
				base+strings.TrimSuffix(class, ".html")+"-"+strconv.Itoa(page)+".html")
		}
	}
	seen := map[string]bool{}
	var lastErr error
	for _, address := range candidates {
		if address == "" || seen[address] {
			continue
		}
		seen[address] = true
		document, _, err := d.fetchProviderPage(ctx, address, base+"/", duanjuUserAgent)
		if err != nil {
			lastErr = err
			continue
		}
		items := maccmsCards(document, source, base)
		if len(items) == 0 {
			items = maccmsGenericItems(document, source, base, 0)
		}
		if len(items) > 0 {
			return items, true, nil
		}
		lastErr = errors.New("目录页未解析到剧集")
	}
	if lastErr == nil {
		lastErr = errors.New("目录页无法访问")
	}
	return nil, false, lastErr
}

func (d *Downloader) fetchMaccmsDetail(ctx context.Context, source, sourceID string) (Drama, []Chapter, error) {
	base := d.duanjuBaseURL(source)
	// XBPQ 规则源：详情与分集按规则截取。
	if rule, ok := customXBPQRule(source); ok {
		return d.xbpqDetail(ctx, source, base, sourceID, rule)
	}
	candidates := maccmsDetailCandidates(source, base, sourceID)
	if isCustomMaccmsSource(source) {
		// 标准 JSON API 优先：详情与播放地址一次拿全，省掉播放页解析。
		if endpoint := maccmsAPIEndpointFor(ctx, d, source, base); endpoint != "" {
			if drama, chapters, apiErr := d.fetchMaccmsAPIDetail(ctx, source, endpoint, base, sourceID); apiErr == nil {
				return drama, chapters, nil
			}
		}
		profile := maccmsCustomProfileFor(ctx, d, source, base)
		candidates = append(profile.detailCandidates(base, sourceID), candidates...)
		candidates = append(candidates, fmt.Sprintf("%s/index.php/vod/detail/id/%s.html", base, sourceID),
			fmt.Sprintf("%s/voddetail/%s.html", base, sourceID),
			fmt.Sprintf("%s/detail/%s.html", base, sourceID))
	}
	var lastErr error
	for _, address := range candidates {
		document, finalURL, err := d.fetchProviderPage(ctx, address, base+"/", duanjuUserAgent)
		if err != nil {
			lastErr = err
			continue
		}
		episodes := maccmsCollapseRouteEpisodes(maccmsEpisodesFromDocument(document))
		if len(episodes) == 0 {
			lastErr = errors.New("未解析到分集列表")
			continue
		}
		drama := Drama{
			ID:          providerDramaID(source, sourceID),
			Source:      source,
			SourceID:    sourceID,
			Title:       maccmsDetailTitle(document),
			Intro:       maccmsDetailIntro(document),
			Cover:       maccmsDetailCover(document, finalURL),
			Category:    maccmsDetailCategory(document),
			ChannelName: duanjuSourceName(source),
		}
		var chapters []Chapter
		for index, episode := range episodes {
			number := duanjuEpisodeNumber(firstNonEmpty(episode.Title, episode.Key), index+1)
			link := duanjuAbsolute(base, episode.URL)
			if link == "" {
				continue
			}
			chapters = append(chapters, duanjuChapter(source, sourceID, number, episode.Title, link, link, base+"/"))
		}
		if len(chapters) == 0 {
			lastErr = errors.New("未解析到可播放分集")
			continue
		}
		sortDuanjuChapters(chapters)
		drama.EpisodeCount = json.Number(strconv.Itoa(len(chapters)))
		return drama, chapters, nil
	}
	if lastErr == nil {
		lastErr = errors.New("未找到该剧的详情页")
	}
	return Drama{}, nil, lastErr
}

func maccmsDetailCandidates(source, base, sourceID string) []string {
	switch {
	case source == sourceFaguo:
		return []string{
			fmt.Sprintf("%s/xzyxvd/%s.html", base, sourceID),
			fmt.Sprintf("%s/detail/%s.html", base, sourceID),
			fmt.Sprintf("%s/voddetail/%s.html", base, sourceID),
		}
	case source == sourceWuguo:
		return []string{
			fmt.Sprintf("%s/index.php/vod/detail/id/%s.html", base, sourceID),
			fmt.Sprintf("%s/dramaDetail/%s.html", base, sourceID),
			fmt.Sprintf("%s/detail/%s.html", base, sourceID),
		}
	case source == sourceWangguo:
		return []string{
			fmt.Sprintf("%s/vod/%s.html", base, sourceID),
			fmt.Sprintf("%s/index.php/vod/detail/id/%s.html", base, sourceID),
			fmt.Sprintf("%s/detail/%s.html", base, sourceID),
		}
	case source == sourceHuaguo:
		return []string{
			fmt.Sprintf("%s/zywview/%s.html", base, sourceID),
			fmt.Sprintf("%s/zywdetail/%s.html", base, sourceID),
			fmt.Sprintf("%s/detail/%s.html", base, sourceID),
		}
	case isCustomMaccmsSource(source):
		// 自定义站点：详情页候选由模板探测决定（兼容 index.php、voddetail、xksidetail 等形态）。
		return nil
	default:
		return nil
	}
}

func maccmsDetailTitle(document *html.Node) string {
	for _, name := range []string{"module-info-heading", "detail-title", "video-info-title", "page-title"} {
		if node := providerHTMLFirstClass(document, name); node != nil {
			if text := providerHTMLText(node); text != "" {
				return maccmsCleanTitle(text)
			}
		}
	}
	if node := providerHTMLNodes(document, func(node *html.Node) bool { return node.Data == "title" }); len(node) > 0 {
		return maccmsCleanTitle(providerHTMLText(node[0]))
	}
	return ""
}

var maccmsTitleSuffixes = []string{"在线观看", "免费观看", "高清完整版", "完整版", "全集", "在线播放", "高清", "免费"}

func maccmsCleanTitle(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	if cut, _, found := strings.Cut(text, " - "); found {
		text = strings.TrimSpace(cut)
	}
	if cut, _, found := strings.Cut(text, " _ "); found {
		text = strings.TrimSpace(cut)
	}
	for _, separator := range []string{"-", "_", "|"} {
		for {
			cut, _, found := strings.Cut(text, separator)
			if !found {
				break
			}
			head := strings.TrimSpace(cut)
			if !maccmsTitleTailIsSeo(text[len(cut)+len(separator):]) {
				break
			}
			text = head
		}
	}
	for changed := true; changed; {
		changed = false
		for _, suffix := range maccmsTitleSuffixes {
			if strings.HasSuffix(text, suffix) && len(text) > len(suffix) {
				text = strings.TrimSpace(strings.TrimSuffix(text, suffix))
				changed = true
			}
		}
	}
	text = strings.Trim(text, "-_|·— ")
	if text == "" {
		return strings.TrimSpace(raw)
	}
	return text
}

func maccmsTitleTailIsSeo(tail string) bool {
	tail = strings.TrimSpace(tail)
	if tail == "" {
		return true
	}
	for _, marker := range []string{"短剧", "全集", "在线观看", "免费", "高清", "完整版", "视频", "剧场", "影院", "网"} {
		if strings.Contains(tail, marker) {
			return true
		}
	}
	return false
}

func maccmsDetailIntro(document *html.Node) string {
	for _, name := range []string{"module-info-introduction-content", "detail-content", "video-info-content", "introduction_introEllipsis"} {
		if node := providerHTMLFirstClass(document, name); node != nil {
			if text := providerHTMLText(node); text != "" {
				return truncate(text, 2000)
			}
		}
	}
	for _, meta := range providerHTMLNodes(document, func(node *html.Node) bool { return node.Data == "meta" }) {
		if strings.EqualFold(providerHTMLAttr(meta, "name"), "description") {
			if content := strings.TrimSpace(providerHTMLAttr(meta, "content")); content != "" {
				return truncate(content, 2000)
			}
		}
	}
	return ""
}

func maccmsDetailCover(document *html.Node, pageURL string) string {
	for _, name := range []string{"module-item-pic", "detail-pic", "video-info-pic", "pic"} {
		for _, node := range providerHTMLNodes(document, func(node *html.Node) bool { return providerHTMLClass(node, name) }) {
			if address := maccmsCardCover(node, pageURL); address != "" {
				return address
			}
		}
	}
	for _, image := range providerHTMLNodes(document, func(node *html.Node) bool { return node.Data == "img" }) {
		for _, attribute := range []string{"data-original", "data-src", "src"} {
			if address := providerCoverAddress(providerHTMLAttr(image, attribute), pageURL); address != "" {
				return address
			}
		}
	}
	return ""
}

func maccmsDetailCategory(document *html.Node) string {
	for _, name := range []string{"module-info-tag-link", "detail-tag", "video-info-actor"} {
		nodes := providerHTMLNodes(document, func(node *html.Node) bool { return providerHTMLClass(node, name) })
		if len(nodes) > 0 {
			var parts []string
			for _, anchor := range providerHTMLNodes(nodes[0], func(node *html.Node) bool { return node.Data == "a" }) {
				if text := providerHTMLText(anchor); text != "" {
					parts = append(parts, text)
				}
			}
			if len(parts) > 0 {
				return duanjuCSV(parts)
			}
		}
	}
	return ""
}

func (d *Downloader) searchMaccms(ctx context.Context, source, query string) ([]Drama, error) {
	base := d.duanjuBaseURL(source)
	var address string
	switch {
	case source == sourceHuaguo:
		address = fmt.Sprintf("%s/search.html?searchword=%s", base, url.QueryEscape(query))
	case source == sourceFaguo:
		address = fmt.Sprintf("%s/xzyxvc/%s-wdyswzqun1num.html", base, url.PathEscape(query))
	case source == sourceWuguo:
		address = fmt.Sprintf("%s/index.php/vod/search/page/1/wd/%s.html", base, url.PathEscape(query))
	case source == sourceWangguo:
		address = fmt.Sprintf("%s/search/%s----------1---.html", base, url.PathEscape(query))
	case isCustomMaccmsSource(source):
		// XBPQ 规则源：搜索走规则「搜索url」。
		if rule, ok := customXBPQRule(source); ok {
			return d.xbpqSearch(ctx, source, base, query, rule)
		}
		// 标准 JSON API 优先：搜索关键字直接交给接口。
		if endpoint := maccmsAPIEndpointFor(ctx, d, source, base); endpoint != "" {
			if items, apiErr := d.fetchMaccmsAPISearch(ctx, source, endpoint, base, query); apiErr == nil && len(items) > 0 {
				return items, nil
			}
		}
		// 自定义站点：按模板家族使用对应搜索路径（index.php / vodsearch / xksisearch 等）。
		profile := maccmsCustomProfileFor(ctx, d, source, base)
		address = profile.Search(base, query)
	default:
		return nil, errors.New("该站源不支持在线搜索")
	}
	document, _, err := d.fetchProviderPage(ctx, address, base+"/", duanjuUserAgent)
	if err != nil {
		if isCustomMaccmsSource(source) {
			fallback := fmt.Sprintf("%s/index.php/vod/search/wd/%s.html", base, url.PathEscape(query))
			if !strings.EqualFold(fallback, address) {
				if retry, _, retryErr := d.fetchProviderPage(ctx, fallback, base+"/", duanjuUserAgent); retryErr == nil {
					return maccmsCards(retry, source, base), nil
				}
			}
		}
		return nil, err
	}
	items := maccmsCards(document, source, base)
	if len(items) == 0 && isCustomMaccmsSource(source) {
		// 结果页模板不认识时，先试通用链接抽取，再退回 index.php 搜索形态。
		items = maccmsGenericItems(document, source, base, 0)
	}
	if len(items) == 0 && isCustomMaccmsSource(source) {
		fallback := fmt.Sprintf("%s/index.php/vod/search/wd/%s.html", base, url.PathEscape(query))
		if !strings.EqualFold(fallback, address) {
			if retry, _, retryErr := d.fetchProviderPage(ctx, fallback, base+"/", duanjuUserAgent); retryErr == nil {
				items = maccmsCards(retry, source, base)
				if len(items) == 0 {
					items = maccmsGenericItems(retry, source, base, 0)
				}
			}
		}
	}
	return items, nil
}

func maccmsPlayerURL(body string) string {
	fields := maccmsPlayerFields(body)
	if urlValue := fields["url"]; urlValue != "" {
		urlValue = maccmsDecryptPlayerURL(urlValue, fields["encrypt"])
		if strings.HasPrefix(strings.ToLower(urlValue), "http") {
			return urlValue
		}
	}
	for _, pattern := range []*regexp.Regexp{
		regexp.MustCompile(`(?i)\$\.url\s*=\s*"([^"]+)"`),
		regexp.MustCompile(`(?i)"url"\s*:\s*"([^"]+\.(?:m3u8|mp4)[^"]*)"`),
		regexp.MustCompile(`(?i)(https?://[^\s"'<>]+\.(?:m3u8|mp4)[^\s"'<>]*)`),
	} {
		if matches := pattern.FindStringSubmatch(body); len(matches) > 1 {
			return strings.ReplaceAll(matches[1], `\/`, `/`)
		}
	}
	if urlValue := fields["url"]; urlValue != "" {
		return maccmsDecryptPlayerURL(urlValue, fields["encrypt"])
	}
	return ""
}

// maccmsPlayerFields 解析播放页中的 player_aaaa / player_data 对象（macplus 模板使用 player_data）。
func maccmsPlayerFields(body string) map[string]string {
	fields := map[string]string{}
	matches := maccmsPlayerData.FindStringSubmatch(body)
	if len(matches) < 2 {
		return fields
	}
	blob := strings.ReplaceAll(matches[1], `\/`, "/")
	var payload map[string]any
	if json.Unmarshal([]byte(blob), &payload) == nil {
		for _, key := range []string{"url", "encrypt", "from"} {
			switch value := payload[key].(type) {
			case string:
				fields[key] = strings.TrimSpace(value)
			case float64:
				fields[key] = strconv.Itoa(int(value))
			}
		}
	}
	if fields["url"] == "" {
		if inner := maccmsPlayerURLField.FindStringSubmatch(matches[1]); len(inner) > 1 {
			fields["url"] = strings.ReplaceAll(inner[1], `\/`, `/`)
		}
	}
	if fields["encrypt"] == "" {
		if inner := regexp.MustCompile(`(?i)"encrypt"\s*:\s*"?([0-9])"?`).FindStringSubmatch(matches[1]); len(inner) > 1 {
			fields["encrypt"] = inner[1]
		}
	}
	return fields
}

// maccmsDecryptPlayerURL 复刻 player.js 的 encrypt 0/1/2 处理：原样、escape 编码、base64。
func maccmsDecryptPlayerURL(raw, encrypt string) string {
	switch encrypt {
	case "1":
		return maccmsUnescapeJS(raw)
	case "2":
		if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil {
			return maccmsUnescapeJS(string(decoded))
		}
		return raw
	default:
		return raw
	}
}

var maccmsJSEscapeHex = regexp.MustCompile(`%([0-9a-fA-F]{2})`)

// maccmsJSUnicodeEscape 匹配 JS escape() 产出的 %uXXXX（url.QueryUnescape 不认这种写法）。
var maccmsJSUnicodeEscape = regexp.MustCompile(`(?i)%u([0-9a-f]{4})`)

func maccmsUnescapeJS(value string) string {
	if !strings.Contains(value, "%") {
		return value
	}
	// 先把 %uXXXX 还原成字符，再交给 QueryUnescape 处理 %XX，
	// 否则路径里的中文（如 /video/剧名/第01集/index.m3u8）会残留 %u7B2C 之类的转义导致 404。
	if strings.Contains(strings.ToLower(value), "%u") {
		value = maccmsJSUnicodeEscape.ReplaceAllStringFunc(value, func(match string) string {
			if number, err := strconv.ParseUint(match[2:], 16, 32); err == nil {
				return string(rune(number))
			}
			return match
		})
	}
	if unescaped, err := url.QueryUnescape(value); err == nil {
		return unescaped
	}
	return maccmsJSEscapeHex.ReplaceAllStringFunc(value, func(match string) string {
		if number, err := strconv.ParseInt(match[1:], 16, 8); err == nil {
			return string(rune(number))
		}
		return match
	})
}

type maccmsCustomProfile struct {
	Family     string // "indexphp" / "myui" / "listxx" / "unknown"
	TypePrefix string // myui 系分类路径前缀（vodtype / xksitype）
	// CategoryPrefix / DetailPrefix 是 listxx 系（listnews/fenlei/… + news/nr 等）的路径段，
	// 由首页导航与详情链接推导得出。
	CategoryPrefix string
	DetailPrefix   string
	Categories     []maccmsCategorySignal
	DetailFmts     []string
	// home 缓存首页文档，供通用链接抽取兜底复用，避免重复抓首页。
	home *html.Node
	Category func(base, category string, page int) string
	AllPage  func(base string, page int) string
	Search   func(base, query string) string
}

var maccmsCustomProfileCache sync.Map // source id -> maccmsCustomProfile

func (p maccmsCustomProfile) detailCandidates(base, id string) []string {
	var out []string
	for _, format := range p.DetailFmts {
		out = append(out, fmt.Sprintf(format, base, id))
	}
	return out
}

var (
	maccmsHomeCategoryLink = regexp.MustCompile(`(?i)^/(?:index\.php/)?vod/type/id/(\d{1,6})\.html$`)
	maccmsAnchorTag        = regexp.MustCompile(`(?s)<[^>]*>`)
	maccmsDetailSegment    = regexp.MustCompile(`(?i)^/([a-z0-9_]*detail)/`)
)

// maccmsHomeTypePatterns 是分类导航链接的常见形态。自定义站点模板五花八门，
// 单靠一种正则识别率很低，这里把实测出现过的形态都列出来逐个匹配：
//   - /vodtype/1.html、/type/1.html、/xksitype/1.html（myui 系，前缀任意）
//   - /index.php/vod/type/id/1.html、/index.php/vod/type/1.html（indexphp 系）
//   - /show/1-----------.html、/vodshow/1.html、/list/1.html（macplus 等）
// 第 1 组固定为分类 ID，第 2 组（若有）为分类路径前缀。
var maccmsHomeTypePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^/(?:index\.php/)?vod/type/id/(\d{1,6})\.html$`),
	regexp.MustCompile(`(?i)^/(?:index\.php/)?vod/type/(\d{1,6})(?:\.html|/)?$`),
	regexp.MustCompile(`(?i)^/index\.php/vod/show/id/(\d{1,6})\.html$`),
	regexp.MustCompile(`(?i)^/([a-z][a-z0-9_]*type)/(\d{1,6})(?:\.html|/)?$`),
	regexp.MustCompile(`(?i)^/type/(\d{1,6})(?:\.html|/)?$`),
	regexp.MustCompile(`(?i)^/([a-z][a-z0-9_]*)?show/(\d{1,6})(?:[-.].*)?\.html$`),
	regexp.MustCompile(`(?i)^/list/(\d{1,6})(?:\.html|/)?$`),
	// 列表/分类段变体（listnews、fenlei、arttype、zylist 等，均为“分类列表”语义，
	// 不会与详情页前缀冲突），前缀捕获为分类路径段。
	// 注：movie/tv/art/news 等段既可能是分类也可能是详情，易与详情前缀歧义，不收录于此，
	// 否则会被 maccmsGenericIsNoisePath 误判为噪声、误杀通用抽取的详情链接。
	regexp.MustCompile(`(?i)^/((?:listnews|listnew|fenlei|arttype|zylist))/(\d{1,6})(?:\.html|/)?$`),
}

func maccmsCleanAnchorText(text string) string {
	text = strings.TrimSpace(maccmsAnchorTag.ReplaceAllString(text, ""))
	runes := []rune(text)
	if len(runes) == 0 || len(runes) > 8 {
		return ""
	}
	for _, r := range runes {
		if r < 0x4e00 || r > 0x9fff {
			return ""
		}
	}
	return text
}

type maccmsCategorySignal struct {
	ID   string
	Name string
}

// maccmsCustomHomeSignals 从首页解析分类导航（ID/名称）与 myui 系路径前缀。
func maccmsCustomHomeSignals(document *html.Node) ([]maccmsCategorySignal, string) {
	var categories []maccmsCategorySignal
	typePrefix := ""
	firstDetail := ""
	seen := map[string]bool{}
	for _, anchor := range providerHTMLNodes(document, func(node *html.Node) bool { return node.Data == "a" }) {
		href := strings.TrimSpace(providerHTMLAttr(anchor, "href"))
		parsed, err := url.Parse(href)
		if err != nil {
			continue
		}
		path := parsed.Path
		if path == "" {
			continue
		}
		// 导航链接未必带 .html 后缀（如 /vodtype/1/），这里放宽要求，
		// 只要形态能对上分类路径即可，避免整站识别不出来。
		if !strings.HasSuffix(path, ".html") && !strings.HasSuffix(path, "/") &&
			!maccmsDetailPath.MatchString(path+"/") {
			continue
		}
		name := maccmsCleanAnchorText(providerHTMLText(anchor))
		if name == "" {
			name = maccmsCleanAnchorText(providerHTMLAttr(anchor, "title"))
		}
		matched := false
		for _, pattern := range maccmsHomeTypePatterns {
			matches := pattern.FindStringSubmatch(path)
			if len(matches) < 2 {
				continue
			}
			id := matches[1]
			prefix := ""
			if len(matches) > 2 {
				prefix = matches[1]
				id = matches[2]
			}
			if prefix != "" && typePrefix == "" {
				typePrefix = prefix
			}
			if name != "" && !seen[id] {
				seen[id] = true
				categories = append(categories, maccmsCategorySignal{ID: id, Name: name})
			}
			matched = true
			break
		}
		if matched {
			continue
		}
		if firstDetail == "" && maccmsDetailPath.MatchString(path) {
			firstDetail = path
		}
	}
	if typePrefix == "" && firstDetail != "" {
		if matches := maccmsDetailSegment.FindStringSubmatch(firstDetail); len(matches) > 1 {
			typePrefix = strings.TrimSuffix(matches[1], "detail") + "type"
		}
	}
	if len(categories) > 12 {
		categories = categories[:12]
	}
	return categories, typePrefix
}

func maccmsCustomProfileFor(ctx context.Context, d *Downloader, source, base string) maccmsCustomProfile {
	if cached, found := maccmsCustomProfileCache.Load(source); found {
		return cached.(maccmsCustomProfile)
	}
	profile := fetchMaccmsCustomProfile(ctx, d, base)
	maccmsCustomProfileCache.Store(source, profile)
	return profile
}

// fetchMaccmsCustomCategories 返回自定义站点首页导航中的分类（缓存于模板探测结果）。
func (d *Downloader) fetchMaccmsCustomCategories(ctx context.Context, source string) ([]nativeCategory, error) {
	base := d.duanjuBaseURL(source)
	if base == "" {
		return nil, errors.New("站源地址不可用")
	}
	// XBPQ 规则源：分类直接来自规则「分类」字段。
	if rule, ok := customXBPQRule(source); ok {
		return d.xbpqCategories(rule), nil
	}
	// 先试标准 JSON API：分类是接口自带字段，比首页导航识别稳得多。
	if endpoint := maccmsAPIEndpointFor(ctx, d, source, base); endpoint != "" {
		if categories := d.fetchMaccmsAPICategories(ctx, endpoint, base); len(categories) > 0 {
			return categories, nil
		}
	}
	profile := maccmsCustomProfileFor(ctx, d, source, base)
	if profile.home == nil {
		// 首页抓取失败（连接超时 / HTTP 错误 / 反爬挑战页），给出明确提示而非笼统报错。
		return nil, errors.New("站点首页无法访问（连接超时或 HTTP 错误），请确认网址正确且站点可访问")
	}
	var categories []nativeCategory
	for _, signal := range profile.Categories {
		if signal.ID == "" || signal.Name == "" {
			continue
		}
		categories = append(categories, nativeCategory{ID: signal.ID, Name: signal.Name})
	}
	if len(categories) == 0 {
		// 认不出导航但认得出模板时不再报死：返回空分类，让「全部站源」照常可用。
		if profile.Family != "unknown" || profile.TypePrefix != "" {
			return nil, nil
		}
		return nil, errors.New("未能在首页识别到分类导航：该站点不是标准 MacCMS 模板，暂不支持作为自定义源")
	}
	return categories, nil
}

func fetchMaccmsCustomProfile(ctx context.Context, d *Downloader, base string) maccmsCustomProfile {
	profile := maccmsCustomProfile{
		Family: "unknown",
		DetailFmts: []string{
			"%s/index.php/vod/detail/id/%s.html",
			"%s/voddetail/%s.html",
			"%s/detail/%s.html",
			"%s/vod/%s.html",
		},
	}
	profile.Search = func(base, query string) string {
		return fmt.Sprintf("%s/index.php/vod/search/wd/%s.html", base, url.PathEscape(query))
	}
	// Category/Page 对 unknown 家族：先试 index.php show 带分类，再试纯页码。
	profile.Category = func(base, category string, page int) string {
		return fmt.Sprintf("%s/index.php/vod/show/id/%s/page/%d.html", base, category, page)
	}
	profile.AllPage = func(base string, page int) string {
		return fmt.Sprintf("%s/index.php/vod/show/page/%d.html", base, page)
	}
	document, _, err := d.fetchProviderPage(ctx, base+"/", base+"/", duanjuUserAgent)
	if err != nil || document == nil {
		return profile
	}
	profile.home = document
	categories, typePrefix := maccmsCustomHomeSignals(document)
	if len(categories) == 0 {
		// 导航链接形态陌生时改用统计法：同骨架链接成批出现且多数无海报 → 分类。
		categories = maccmsGenericCategories(document, base)
	}
	profile.Categories = categories
	// 首页链接推导详情页前缀（如 gzmzpx 的 /news/NNNNN.html）。
	detailPrefix := maccmsCustomHomeDetailPrefix(document, typePrefix)
	// 分类导航前缀不含 "type" 后缀的（listnews / fenlei / …）归为 listxx 家族；
	// 含 "type" 后缀（vodtype / xksitype / newstype）或仅由详情链推导出前缀的归 myui。
	switch {
	case typePrefix != "" && !strings.HasSuffix(typePrefix, "type"):
		profile.Family = "listxx"
		profile.CategoryPrefix = typePrefix
		profile.DetailPrefix = detailPrefix
	case typePrefix != "":
		profile.Family = "myui"
	default:
		profile.Family = "indexphp"
	}
	switch profile.Family {
	case "indexphp":
		profile.DetailFmts = []string{"%s/index.php/vod/detail/id/%s.html", "%s/vod/detail/id/%s.html", "%s/detail/%s.html"}
		profile.Category = func(base, category string, page int) string {
			return fmt.Sprintf("%s/index.php/vod/show/id/%s/page/%d.html", base, category, page)
		}
		profile.AllPage = func(base string, page int) string {
			return fmt.Sprintf("%s/index.php/vod/show/page/%d.html", base, page)
		}
	case "myui":
		dash := "vod"
		if typePrefix != "" && strings.HasSuffix(typePrefix, "type") {
			dash = strings.TrimSuffix(typePrefix, "type")
			if dash == "" {
				dash = "vod"
			}
		}
		profile.TypePrefix = typePrefix
		profile.DetailFmts = []string{
			fmt.Sprintf("%%s/%sdetail/%%s.html", dash),
			"%s/voddetail/%s.html",
			"%s/index.php/vod/detail/id/%s.html",
			"%s/detail/%s.html",
		}
		profile.Search = func(base, query string) string {
			return fmt.Sprintf("%s/%ssearch/%s-------------.html", base, dash, url.PathEscape(query))
		}
		profile.Category = func(base, category string, page int) string {
			return fmt.Sprintf("%s/%sshow/%s-----------%d.html", base, dash, category, page)
		}
		profile.AllPage = func(base string, page int) string {
			return fmt.Sprintf("%s/%sshow/1-----------%d.html", base, dash, page)
		}
	case "listxx":
		// listnews/fenlei/… 系：分类 /{catPrefix}/{id}.html、分页 /{catPrefix}/{id}-{page}.html；
		// 详情 /{detPrefix}/{id}.html；搜索走标准 MacCMS search.php。
		catPrefix := profile.CategoryPrefix
		detPrefix := profile.DetailPrefix
		if detPrefix == "" {
			detPrefix = "news"
		}
		profile.DetailFmts = []string{
			fmt.Sprintf("%%s/%s/%%s.html", detPrefix),
			"%s/index.php/vod/detail/id/%s.html",
			"%s/detail/%s.html",
		}
		profile.Category = func(base, category string, page int) string {
			if page <= 1 {
				return fmt.Sprintf("%s/%s/%s.html", base, catPrefix, category)
			}
			return fmt.Sprintf("%s/%s/%s-%d.html", base, catPrefix, category, page)
		}
		profile.AllPage = func(base string, page int) string {
			if page <= 1 {
				return base + "/"
			}
			return fmt.Sprintf("%s/%s/1-%d.html", base, catPrefix, page)
		}
		profile.Search = func(base, query string) string {
			return fmt.Sprintf("%s/search.php?searchword=%s", base, url.PathEscape(query))
		}
	}
	return profile
}

// maccmsCustomHomeDetailPrefix 从首页链接推导详情页路径段（如 gzmzpx 的 news）。
// 分类链接一般是 1~2 位数字 ID，详情链接是 4 位以上数字 ID，据此区分。
func maccmsCustomHomeDetailPrefix(document *html.Node, categoryPrefix string) string {
	if document == nil {
		return ""
	}
	pattern := regexp.MustCompile(`^/([a-z][a-z0-9_]*)/(\d{4,})\.html$`)
	for _, anchor := range providerHTMLNodes(document, func(node *html.Node) bool { return node.Data == "a" }) {
		href := strings.TrimSpace(providerHTMLAttr(anchor, "href"))
		parsed, err := url.Parse(href)
		if err != nil || parsed.Path == "" {
			continue
		}
		matches := pattern.FindStringSubmatch(parsed.Path)
		if len(matches) < 3 {
			continue
		}
		seg := matches[1]
		if seg == categoryPrefix || seg == "" {
			continue
		}
		return seg
	}
	return ""
}

func maccmsNormalizePlaybackURL(raw string) string {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, `\/`, `/`))
	raw = strings.Trim(raw, ",\\。，;；")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "p.") || strings.Contains(raw, "c1.") {
		parts := strings.Split(raw, "/")
		if len(parts) >= 3 {
			base := strings.Join(parts[:len(parts)-2], "/")
			folder := parts[len(parts)-2]
			if decoded := maccmsDecodeUnicode(folder); decoded != folder {
				return base + "/" + url.PathEscape(decoded) + "/index.m3u8"
			}
		}
	}
	return raw
}

var maccmsUnicodeEscape = regexp.MustCompile(`\\?u([0-9a-fA-F]{4})`)

func maccmsDecodeUnicode(value string) string {
	return maccmsUnicodeEscape.ReplaceAllStringFunc(value, func(match string) string {
		hex := match[len(match)-4:]
		if number, err := strconv.ParseInt(hex, 16, 32); err == nil {
			return string(rune(number))
		}
		return match
	})
}

// ---- macplus / player_data 加密资源的云解析链路（如 MGMGTV 的 mgtv_ 密文）----

var (
	maccmsPlayerScriptPath = regexp.MustCompile(`(?i)maccms\.path\s*\+\s*['"](/[^'"]*player[^'"]*)['"]`)
	maccmsMaccmsVar        = regexp.MustCompile(`(?i)var\s+maccms\s*=\s*(\{.*?\})\s*;`)
	maccmsPathField        = regexp.MustCompile(`(?i)"path"\s*:\s*"([^"]*)"`)
	maccmsIframeSrc        = regexp.MustCompile(`(?i)iframe[^>]*src=["'](https?://[^"'\s]+)["']`)
	maccmsVideoURLVar      = regexp.MustCompile(`(?i)var\s+video_url\s*=\s*['"]([^'"]+)['"]`)
)

// resolveMaccmsCloudParse 处理播放页给出非 http 加密 url 的场景：
// 按 from 字段定位 /static/player/<from>.js，取出 iframe 解析接口地址，请求后从返回页面提取 m3u8。
func (d *Downloader) resolveMaccmsCloudParse(ctx context.Context, body, encrypted, pageURL string) string {
	return d.resolveMaccmsCloudParseLogged(ctx, body, encrypted, pageURL, nil)
}

func (d *Downloader) resolveMaccmsCloudParseLogged(ctx context.Context, body, encrypted, pageURL string, logf func(string, ...any)) string {
	note := func(format string, args ...any) {
		if logf != nil {
			logf(format, args...)
		}
	}
	if encrypted == "" || !strings.Contains(pageURL, "http") {
		note("cloud: encrypted or pageURL empty")
		return ""
	}
	parsed, err := url.Parse(pageURL)
	if err != nil || parsed.Host == "" {
		note("cloud: pageURL parse failed: %v", err)
		return ""
	}
	siteBase := parsed.Scheme + "://" + parsed.Host
	from := ""
	if matches := maccmsPlayerFrom.FindStringSubmatch(body); len(matches) > 1 {
		from = matches[1]
	}
	note("cloud: from=%q encrypted_len=%d", from, len(encrypted))
	playerDir := "/static/player/"
	if matches := maccmsPlayerScriptPath.FindStringSubmatch(body); len(matches) > 1 {
		playerDir = matches[1]
	} else if matches := maccmsMaccmsVar.FindStringSubmatch(body); len(matches) > 1 {
		if inner := maccmsPathField.FindStringSubmatch(matches[1]); len(inner) > 1 && strings.TrimSpace(inner[1]) != "" {
			playerDir = strings.TrimRight(inner[1], "/") + "/static/player/"
		}
	}
	var scripts []string
	if from != "" {
		scripts = append(scripts, siteBase+playerDir+from+".js")
	}
	scripts = append(scripts, siteBase+"/static/player/m3u8.js", siteBase+"/static/player/mac.js")
	referer := pageURL
	var parseBase string
	for _, script := range scripts {
		content, err := d.fetchProviderText(ctx, script, referer)
		if err != nil || content == "" || len(content) > 512*1024 {
			note("cloud: script %s err=%v len=%d", script, err, len(content))
			continue
		}
		if matches := maccmsIframeSrc.FindStringSubmatch(content); len(matches) > 1 {
			candidate := strings.TrimSpace(matches[1])
			if strings.Contains(candidate, "?") {
				parseBase = candidate
			} else {
				parseBase = candidate + "?url="
			}
			note("cloud: iframe base=%s", parseBase)
			break
		}
		note("cloud: no iframe in %s", script)
	}
	if parseBase == "" {
		note("cloud: no parseBase")
		return ""
	}
	endpoint := parseBase + encrypted
	note("cloud: endpoint len=%d", len(endpoint))
	if !strings.HasPrefix(endpoint, "http") {
		return ""
	}
	response, err := d.fetchProviderText(ctx, endpoint, referer)
	if err != nil {
		note("cloud: endpoint fetch err=%v", err)
		return ""
	}
	note("cloud: endpoint resp len=%d", len(response))
	if matches := maccmsVideoURLVar.FindStringSubmatch(response); len(matches) > 1 {
		if strings.HasPrefix(strings.ToLower(matches[1]), "http") {
			note("cloud: video_url matched")
			return matches[1]
		}
	}
	if matches := regexp.MustCompile(`(?i)(https?://[^\s"'<>]+\.m3u8[^\s"'<>]*)`).FindStringSubmatch(response); len(matches) > 1 {
		note("cloud: fallback m3u8 matched")
		return matches[1]
	}
	note("cloud: no m3u8 in response")
	return ""
}
