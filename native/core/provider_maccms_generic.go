package core

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// ---- 通用链接抽取兜底 ----
//
// 思路来自 TVBox 爬虫包：当站点模板不在已知名单里（class 名陌生、导航链接形态陌生、
// JSON API 也不存在）时，不再依赖任何具体 class，而是直接扫描页面里所有 <a href>，
// 用两条与模板无关的判据把真正的剧集详情链接挑出来：
//   1. 剧集卡片一定带海报（<img>），分类导航 / 文字链一定不带；
//   2. 列表页里同一「路径骨架」（数字段归一化后的路径）会成批重复出现，
//      而孤立导航链接不会。
// 这样即使遇到完全没见过的模板，也能抽出可看的首页列表和分类。

var (
	// maccmsGenericNoiseSegments 是不会指向详情页的路径片段：分类、筛选、播放、会员等。
	// 注意只按「完整片段」匹配，避免误杀 index.php 这类详情页路径。
	maccmsGenericNoiseSegments = map[string]bool{
		"type": true, "types": true, "search": true, "so": true, "play": true, "player": true,
		"vodplay": true, "actor": true, "star": true, "letter": true, "area": true, "lang": true,
		"year": true, "tag": true, "topic": true, "news": true, "art": true, "gbook": true,
		"comment": true, "api": true, "static": true, "css": true, "js": true, "upload": true,
		"template": true, "index": true, "page": true, "member": true, "user": true, "login": true,
		"register": true, "app": true, "down": true, "about": true, "help": true, "link": true,
		"rss": true, "sitemap": true, "admin": true, "plus": true, "install": true, "update": true,
	}
	maccmsGenericAssetExt  = regexp.MustCompile(`(?i)\.(css|js|png|jpe?g|gif|webp|svg|ico|apk|zip|rar|mp4|m3u8|xml|txt|json)$`)
	maccmsGenericIDSegment = regexp.MustCompile(`^\d{1,10}$`)
	maccmsGenericSpaces    = regexp.MustCompile(`\s+`)
	maccmsGenericEpisode   = regexp.MustCompile(`(?:更新(?:至|到)?|共)?\s*(\d{1,4})\s*集|完结|HD`)
	maccmsGenericScheme    = regexp.MustCompile(`(?i)^(javascript|mailto|tel|data):`)
)

// maccmsGenericCandidate 是一个从链接抽取出来的候选剧集。
type maccmsGenericCandidate struct {
	link     string
	id       string
	title    string
	cover    string
	remark   string
	skeleton string
}

func maccmsGenericCleanText(value string) string {
	return strings.TrimSpace(maccmsGenericSpaces.ReplaceAllString(strings.ReplaceAll(value, "\u00a0", " "), " "))
}

// maccmsGenericIsNoisePath 判定路径是否不可能指向详情页。
// strict=true 时连「疑似分类形态」也一并排除；strict=false 只排除明确无关的路径，
// 用于整站非常规（详情链接长得像分类链接）时的二次放宽扫描。
func maccmsGenericIsNoisePath(path string, strict bool) bool {
	if path == "" || path == "/" {
		return true
	}
	lower := strings.ToLower(path)
	// myui 系分类/筛选页带连续横杠（/vodshow/1-----------.html），详情页不会。
	if strings.Contains(lower, "---") {
		return true
	}
	if strict {
		for _, pattern := range maccmsHomeTypePatterns {
			if pattern.MatchString(path) {
				return true
			}
		}
	}
	for _, segment := range strings.Split(strings.Trim(lower, "/"), "/") {
		if segment == "" {
			continue
		}
		if maccmsGenericNoiseSegments[segment] {
			return true
		}
		// xxxtype（vodtype / xksitype / arctype …）是分类路径。
		if strings.HasSuffix(segment, "type") {
			return true
		}
	}
	return false
}

// maccmsGenericSkeleton 把路径里的纯数字段归一化，用于统计「同类链接」的重复度。
func maccmsGenericSkeleton(path string) string {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for index, segment := range segments {
		if maccmsGenericIDSegment.MatchString(strings.TrimSuffix(segment, ".html")) {
			segments[index] = "{id}"
		}
	}
	return "/" + strings.Join(segments, "/")
}

func maccmsGenericTitleOK(value string) bool {
	runes := []rune(value)
	if len(runes) < 2 || len(runes) > 60 {
		return false
	}
	// 纯数字/纯符号不可能是片名。
	for _, r := range runes {
		if (r >= '0' && r <= '9') || r < 0x4e00 {
			continue
		}
		return true
	}
	return false
}

// maccmsGenericTitle 依次尝试 a[title] → img[alt] → 链接文本 → 邻近标题节点。
func maccmsGenericTitle(anchor *html.Node) string {
	if text := maccmsGenericCleanText(providerHTMLAttr(anchor, "title")); maccmsGenericTitleOK(text) {
		return text
	}
	for _, image := range providerHTMLNodes(anchor, func(node *html.Node) bool { return node.Data == "img" }) {
		if text := maccmsGenericCleanText(providerHTMLAttr(image, "alt")); maccmsGenericTitleOK(text) {
			return text
		}
	}
	if text := maccmsGenericCleanText(providerHTMLText(anchor)); maccmsGenericTitleOK(text) {
		return text
	}
	for depth, parent := 0, anchor.Parent; parent != nil && depth < 3; depth, parent = depth+1, parent.Parent {
		nodes := providerHTMLNodes(parent, func(node *html.Node) bool {
			if node.Type != html.ElementNode {
				return false
			}
			if node.Data == "h1" || node.Data == "h2" || node.Data == "h3" || node.Data == "h4" {
				return true
			}
			class := strings.ToLower(providerHTMLAttr(node, "class"))
			return strings.Contains(class, "title") || strings.Contains(class, "name")
		})
		for _, node := range nodes {
			if text := maccmsGenericCleanText(providerHTMLText(node)); maccmsGenericTitleOK(text) {
				return text
			}
		}
	}
	return ""
}

// maccmsGenericCover 取链接的海报。只在链接自身（或其所属卡片容器的直接子级）里找，
// 绝不向上跨层扫描——否则导航链接会因为"页面里别处有图"而被误判成剧集卡片。
func maccmsGenericCover(anchor *html.Node, link string) string {
	if address := maccmsCardCover(anchor, link); address != "" {
		return address
	}
	// 少数模板海报挂在 <li>/<div> 容器下、与链接并列，这里只看容器的直接子 img。
	parent := anchor.Parent
	if parent == nil || parent.Type != html.ElementNode {
		return ""
	}
	switch parent.Data {
	case "li", "div", "figure", "article":
	default:
		return ""
	}
	for child := parent.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.ElementNode || child.Data != "img" {
			continue
		}
		for _, attribute := range []string{"data-original", "data-src", "src"} {
			if address := providerCoverAddress(providerHTMLAttr(child, attribute), link); address != "" {
				return address
			}
		}
		if address := maccmsStyleCover(providerHTMLAttr(child, "style"), link); address != "" {
			return address
		}
	}
	return ""
}

func maccmsGenericRemark(anchor *html.Node) string {
	for depth, parent := 0, anchor.Parent; parent != nil && depth < 2; depth, parent = depth+1, parent.Parent {
		text := maccmsGenericCleanText(providerHTMLText(parent))
		runes := []rune(text)
		if len(runes) > 120 {
			text = string(runes[:120])
		}
		if matches := maccmsGenericEpisode.FindStringSubmatch(text); len(matches) > 0 {
			return strings.TrimSpace(matches[0])
		}
	}
	return ""
}

// maccmsGenericSameSite 判定链接是否与站源同域（允许 www / 子域差异），
// 用于过滤推荐位上的外站链接。
func maccmsGenericSameSite(linkHost, baseHost string) bool {
	if linkHost == "" || baseHost == "" {
		return false
	}
	link := strings.ToLower(strings.TrimPrefix(linkHost, "www."))
	origin := strings.ToLower(strings.TrimPrefix(baseHost, "www."))
	if link == origin {
		return true
	}
	return strings.HasSuffix(link, "."+origin) || strings.HasSuffix(origin, "."+link)
}

// maccmsGenericScan 扫描页面所有链接，返回候选剧集。
func maccmsGenericScan(document *html.Node, base string, strict bool) []maccmsGenericCandidate {
	if document == nil {
		return nil
	}
	baseHost := ""
	if parsed, err := url.Parse(base); err == nil {
		baseHost = parsed.Host
	}
	var candidates []maccmsGenericCandidate
	seen := map[string]bool{}
	skeletons := map[string]int{}
	for _, anchor := range providerHTMLNodes(document, func(node *html.Node) bool { return node.Data == "a" }) {
		href := strings.TrimSpace(providerHTMLAttr(anchor, "href"))
		if href == "" || strings.HasPrefix(href, "#") || maccmsGenericScheme.MatchString(href) {
			continue
		}
		link := duanjuAbsolute(base, href)
		if link == "" || !strings.HasPrefix(link, "http") {
			continue
		}
		parsed, err := url.Parse(link)
		if err != nil || parsed.Host == "" {
			continue
		}
		if baseHost != "" && !maccmsGenericSameSite(parsed.Host, baseHost) {
			continue
		}
		path := parsed.Path
		if maccmsGenericAssetExt.MatchString(path) {
			continue
		}
		if maccmsGenericIsNoisePath(path, strict) {
			continue
		}
		id := maccmsSourceIDFromURL(link)
		if id == "" || seen[id] {
			continue
		}
		title := maccmsGenericTitle(anchor)
		if title == "" {
			continue
		}
		cover := maccmsGenericCover(anchor, link)
		if strict && cover == "" {
			// 严格模式下无海报的链接一律当作导航/文字链丢弃。
			continue
		}
		seen[id] = true
		skeleton := maccmsGenericSkeleton(path)
		skeletons[skeleton]++
		candidates = append(candidates, maccmsGenericCandidate{
			link:     link,
			id:       id,
			title:    title,
			cover:    cover,
			remark:   maccmsGenericRemark(anchor),
			skeleton: skeleton,
		})
	}
	if strict {
		return candidates
	}
	// 放宽模式：只保留成批出现的骨架（≥3 条同形态），过滤孤立文字链。
	var filtered []maccmsGenericCandidate
	for _, candidate := range candidates {
		if skeletons[candidate.skeleton] >= 3 {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

// maccmsGenericItems 是 maccmsCards 的兜底：模板识别不出来时直接抽详情链接出列表。
func maccmsGenericItems(document *html.Node, source, base string, limit int) []Drama {
	if document == nil || base == "" {
		return nil
	}
	candidates := maccmsGenericScan(document, base, true)
	if len(candidates) == 0 {
		// 整站没有海报（纯文字列表）或详情链接形态特殊时才放宽；
		// 用分类数字 ID 集合把已被判定为分类的链接剔掉，避免分类混进剧集列表。
		// 注意：分类 signal.ID 是路径形式（如 /fenlei/1），但候选链接的 id 是数字，
		// 必须用 maccmsGenericCategoryScan 返回的数字 ID 集合来排除，而非 signal.ID。
		_, categoryIDs := maccmsGenericCategoryScan(document, base)
		for _, candidate := range maccmsGenericScan(document, base, false) {
			if !categoryIDs[candidate.id] {
				candidates = append(candidates, candidate)
			}
		}
	}
	if limit <= 0 {
		limit = 60
	}
	var items []Drama
	for _, candidate := range candidates {
		if len(items) >= limit {
			break
		}
		items = append(items, Drama{
			ID:           providerDramaID(source, candidate.id),
			Source:       source,
			SourceID:     candidate.id,
			Title:        candidate.title,
			Cover:        candidate.cover,
			Remark:       candidate.remark,
			EpisodeCount: json.Number(strconv.Itoa(duanjuEpisodeNumber(candidate.remark, 0))),
			ChannelName:  duanjuSourceName(source),
		})
	}
	return items
}

// maccmsGenericCategoryKey 把分类链接归一化成「骨架 + 分类 ID」：
//   /vodtype/2.html            → /vodtype/{id}, 2
//   /vodshow/1-----------.html → /vodshow/{id}, 1
//   /index.php/vod/type/id/3.html → /index.php/vod/type/id/{id}, 3
// 详情页（含 detail / play）一律排除。第三个返回值是归一化后的分类路径。
func maccmsGenericCategoryKey(path string) (string, string, string, bool) {
	clean := strings.TrimSuffix(path, ".html")
	if index := strings.Index(clean, "---"); index >= 0 {
		clean = clean[:index]
	}
	segments := strings.Split(strings.Trim(clean, "/"), "/")
	if len(segments) < 2 {
		return "", "", "", false
	}
	last := segments[len(segments)-1]
	id := ""
	switch {
	case maccmsGenericIDSegment.MatchString(last):
		id = last
		segments[len(segments)-1] = "{id}"
	default:
		if head, _, found := strings.Cut(last, "-"); found && maccmsGenericIDSegment.MatchString(head) {
			id = head
			segments[len(segments)-1] = "{id}"
		}
	}
	if id == "" {
		return "", "", "", false
	}
	skeleton := "/" + strings.Join(segments, "/")
	lower := strings.ToLower(skeleton)
	if strings.Contains(lower, "detail") || strings.Contains(lower, "play") || !strings.HasSuffix(lower, "/{id}") {
		return "", "", "", false
	}
	return skeleton, id, "/" + strings.Trim(clean, "/"), true
}

func maccmsGenericCategoryName(anchor *html.Node) string {
	if text := maccmsCleanAnchorText(providerHTMLText(anchor)); text != "" {
		return text
	}
	if text := maccmsCleanAnchorText(providerHTMLAttr(anchor, "title")); text != "" {
		return text
	}
	text := maccmsGenericCleanText(providerHTMLText(anchor))
	runes := []rune(text)
	if len(runes) < 1 || len(runes) > 16 {
		return ""
	}
	for _, r := range runes {
		if r == '\n' || r == '\t' {
			return ""
		}
	}
	return text
}

// maccmsGenericLooksLikeCategoryIDs 区分「分类编号」与「剧集编号」：
// 分类 ID 一般很小且彼此接近（1、2、3…），剧集 ID 多是自增到五位以上的大数。
// 用于避免把无海报的文字列表误判成分类导航。
func maccmsGenericLooksLikeCategoryIDs(ids []string) bool {
	if len(ids) == 0 {
		return false
	}
	minimum, maximum := -1, -1
	for _, id := range ids {
		number, err := strconv.Atoi(id)
		if err != nil || number <= 0 || number > 99999 {
			return false
		}
		if minimum < 0 || number < minimum {
			minimum = number
		}
		if number > maximum {
			maximum = number
		}
	}
	return maximum-minimum <= 5*len(ids)+20
}

// maccmsGenericCategories 是导航识别的兜底：靠「同骨架链接成批出现且多数无海报」
// 反推出分类链接，即使该模板的分类路径形态从没见过。
// 分类 ID 用「去掉 .html 与筛选段的原始路径」表示（如 /fenlei/1），
// 这样即便模板家族猜错，分页 URL 也能按路径直接拼出来。
func maccmsGenericCategories(document *html.Node, base string) []maccmsCategorySignal {
	signals, _ := maccmsGenericCategoryScan(document, base)
	return signals
}

// maccmsGenericCategoryScan 同时返回分类条目与「分类纯数字 ID 集合」，
// 后者供列表抽取在放宽模式下剔除分类链接。
func maccmsGenericCategoryScan(document *html.Node, base string) ([]maccmsCategorySignal, map[string]bool) {
	if document == nil || base == "" {
		return nil, nil
	}
	type group struct {
		order   int
		ids     []string
		names   map[string]string
		paths   map[string]string
		noCover int
		total   int
	}
	groups := map[string]*group{}
	order := 0
	for _, anchor := range providerHTMLNodes(document, func(node *html.Node) bool { return node.Data == "a" }) {
		href := strings.TrimSpace(providerHTMLAttr(anchor, "href"))
		if href == "" || strings.HasPrefix(href, "#") || maccmsGenericScheme.MatchString(href) {
			continue
		}
		link := duanjuAbsolute(base, href)
		if link == "" || !strings.HasPrefix(link, "http") {
			continue
		}
		parsed, err := url.Parse(link)
		if err != nil || parsed.Host == "" {
			continue
		}
		if maccmsGenericAssetExt.MatchString(parsed.Path) {
			continue
		}
		skeleton, id, categoryPath, ok := maccmsGenericCategoryKey(parsed.Path)
		if !ok {
			continue
		}
		name := maccmsGenericCategoryName(anchor)
		entry, found := groups[skeleton]
		if !found {
			order++
			entry = &group{order: order, names: map[string]string{}, paths: map[string]string{}}
			groups[skeleton] = entry
		}
		if _, exists := entry.paths[id]; !exists {
			entry.ids = append(entry.ids, id)
			entry.paths[id] = categoryPath
			entry.names[id] = name
		} else if entry.names[id] == "" && name != "" {
			entry.names[id] = name
		}
		entry.total++
		if maccmsGenericCover(anchor, link) == "" {
			entry.noCover++
		}
	}
	var list []*group
	for _, entry := range groups {
		// 成批出现（≥3 个同类链接）、多数没有海报、且编号像分类（小且集中）→ 分类导航。
		if len(entry.ids) >= 3 && entry.noCover*2 >= entry.total && maccmsGenericLooksLikeCategoryIDs(entry.ids) {
			list = append(list, entry)
		}
	}
	if len(list) > 1 {
		for i := 1; i < len(list); i++ {
			for j := i; j > 0 && list[j].order < list[j-1].order; j-- {
				list[j], list[j-1] = list[j-1], list[j]
			}
		}
	}
	var signals []maccmsCategorySignal
	ids := map[string]bool{}
	for _, entry := range list {
		for _, id := range entry.ids {
			name := entry.names[id]
			path := entry.paths[id]
			if name == "" || path == "" {
				continue
			}
			ids[id] = true
			signals = append(signals, maccmsCategorySignal{ID: path, Name: name})
			if len(signals) >= 12 {
				return signals, ids
			}
		}
	}
	return signals, ids
}
