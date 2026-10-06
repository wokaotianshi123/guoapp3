package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// MacCMS 标准 JSON API（/api.php/provide/vod/）
//
// 苹果 CMS / MacCMS 系站点普遍内置这套接口，返回结构固定：
//
//	{"code":1,"list":[{"vod_id":1,"vod_name":"...","vod_pic":"...","vod_remarks":"...",
//	 "vod_content":"...","vod_play_from":"m3u8$$$","vod_play_url":"第1集$url#第2集$url$$$...",
//	 "type_id":1,"type_name":"电影"}],"class":[{"type_id":1,"type_name":"电影"}]}
//
// 相比抓 HTML 模板，它有三个决定性优势：
//  1. 不依赖页面模板（indexphp / myui / 自定义前缀全都走同一套接口）；
//  2. 列表、搜索、详情、分类、播放地址一次拿全，不怕模板改版或反爬遮挡；
//  3. vod_play_from 直接给出多条线路，与播放页线路列表等价且更全。
//
// 因此自定义站源统一「API 优先、HTML 兜底」：探测到 API 就走这里，
// 探测不到或调用失败再退回原有模板解析，已接入的站点行为不变。
// ---------------------------------------------------------------------------

var maccmsAPIEndpointCache sync.Map // source -> maccmsAPIEndpointEntry（正面长期缓存，负面短 TTL）

// maccmsAPIEndpointEntry 缓存一次 API 探测结果：
// endpoint 非空为正面结论（长期有效）；endpoint 为空且 expires 非零为
// 确定性负面结论（TTL 内不再探测）；临时性失败不写缓存。
// 避免站点瞬时 503 限流把自定义源永久锁死在 HTML 兜底路径。
type maccmsAPIEndpointEntry struct {
	endpoint string
	expires  time.Time
}

// maccmsAPIStatusPattern 从 fetchProviderText 的错误串提取 HTTP 状态码。
var maccmsAPIStatusPattern = regexp.MustCompile(`HTTP (\d{3})`)

// maccmsAPIEndpointCandidates 罗列常见的 API 挂载位置，逐个探测。
func maccmsAPIEndpointCandidates(base string) []string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return nil
	}
	return []string{
		base + "/api.php/provide/vod/",
		base + "/api.php/provide/vod",
		base + "/inc/api.php/provide/vod/",
		base + "/api.php/provide/vod/from/m3u8/",
		base + "/api.php/app/vod/",
	}
}

// maccmsAPIEndpointFor 返回该站源可用的 API 地址，空串表示当前判定不支持。
// 正面结果长期缓存；临时性失败（5xx、网络错误）不缓存，
// 确定性负面结论（响应不是 JSON API）缓存 60 秒后过期重试。
func maccmsAPIEndpointFor(ctx context.Context, d *Downloader, source, base string) string {
	if cached, found := maccmsAPIEndpointCache.Load(source); found {
		entry := cached.(maccmsAPIEndpointEntry)
		if entry.endpoint != "" || time.Now().Before(entry.expires) {
			return entry.endpoint
		}
	}
	provisional := false
	found := ""
	for _, candidate := range maccmsAPIEndpointCandidates(base) {
		endpoint, permanent := d.maccmsAPIProbeOnce(ctx, candidate, base)
		if endpoint != "" {
			found = candidate
			break
		}
		if !permanent {
			provisional = true
		}
	}
	switch {
	case found != "":
		maccmsAPIEndpointCache.Store(source, maccmsAPIEndpointEntry{endpoint: found})
	case provisional:
		// 站点可能只是临时限流/抖动：短 TTL 后重试，既不让风暴期反复重探，
		// 也不会把瞬时 503 变成永久降级。
		maccmsAPIEndpointCache.Store(source, maccmsAPIEndpointEntry{expires: time.Now().Add(30 * time.Second)})
	default:
		maccmsAPIEndpointCache.Store(source, maccmsAPIEndpointEntry{expires: time.Now().Add(60 * time.Second)})
	}
	return found
}

// maccmsAPIProbeOnce 探测单个候选 API 地址。
// 返回 (endpoint, definitive)：endpoint 非空表示该候选可用；
// definitive=true 表示拿到了明确的「非 API 响应」，可放心缓存负面结论；
// definitive=false 表示传输层/5xx 等临时故障，不应据此否定 API 支持。
func (d *Downloader) maccmsAPIProbeOnce(ctx context.Context, candidate, base string) (string, bool) {
	body, err := d.fetchProviderText(ctx, candidate+"?ac=videolist&pg=1", base+"/")
	if err != nil {
		return "", maccmsAPIErrorDefinitive(err)
	}
	body = strings.TrimSpace(body)
	if !strings.HasPrefix(body, "{") {
		return "", true // 站点有响应但不是 JSON API：确定不支持
	}
	var payload map[string]any
	if json.Unmarshal([]byte(body), &payload) != nil {
		return candidate, false // JSON 却解析失败：形态可疑，不缓存负面结论
	}
	if _, hasList := payload["list"]; hasList {
		return candidate, false // list 键存在即认 API（空列表只是暂无数据）
	}
	if _, hasData := payload["data"]; hasData {
		return candidate, false
	}
	return "", true
}

// maccmsAPIErrorDefinitive 判断一次抓取错误是否是「站点明确拒绝」而非临时抖动。
// fetchProviderText 的 HTTP 错误形如 "host HTTP 503"；5xx/408/429 与无状态码的
// 网络错误都属临时（下次调用应重试探测），其余 4xx 说明该路径确实没有 API。
func maccmsAPIErrorDefinitive(err error) bool {
	if err == nil {
		return false
	}
	var backoff *requestBackoff
	if errors.As(err, &backoff) {
		return backoff.status >= 400 && backoff.status < 500 && backoff.status != 408 && backoff.status != 429
	}
	match := maccmsAPIStatusPattern.FindStringSubmatch(err.Error())
	if len(match) != 2 {
		return false // 无状态码：连接失败/超时等，视为临时
	}
	status, convErr := strconv.Atoi(match[1])
	if convErr != nil {
		return false
	}
	return status >= 400 && status < 500 && status != 408 && status != 429
}

// maccmsAPICall 请求 API 并解出 list / class，失败时返回错误。
func (d *Downloader) maccmsAPICall(ctx context.Context, address, referer string) (map[string]any, error) {
	body, err := d.fetchProviderText(ctx, address, referer)
	if err != nil || strings.TrimSpace(body) == "" || len(body) > 8*1024*1024 {
		if err == nil {
			err = errMaccmsAPIEmpty
		}
		return nil, err
	}
	body = strings.TrimSpace(body)
	if !strings.HasPrefix(body, "{") {
		return nil, errMaccmsAPIEmpty
	}
	var payload map[string]any
	if json.Unmarshal([]byte(body), &payload) != nil {
		return nil, errMaccmsAPIEmpty
	}
	if code := maccmsAPIInt(payload["code"]); code == 0 && len(payload) > 1 {
		// 部分站点不返回 code 字段，只要有内容就放行。
		if _, hasList := payload["list"]; !hasList {
			if _, hasData := payload["data"]; !hasData {
				return nil, errMaccmsAPIEmpty
			}
		}
	}
	return payload, nil
}

var errMaccmsAPIEmpty = &maccmsAPIError{"接口未返回可用数据"}

type maccmsAPIError struct{ message string }

func (e *maccmsAPIError) Error() string { return e.message }

// maccmsAPIItems 取出 list（或 data）里的条目。
func maccmsAPIItems(payload map[string]any) []map[string]any {
	raw, found := payload["list"]
	if !found {
		raw = payload["data"]
	}
	rows, ok := raw.([]any)
	if !ok {
		return nil
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if entry, ok := row.(map[string]any); ok {
			items = append(items, entry)
		}
	}
	return items
}

func maccmsAPIClasses(payload map[string]any) []map[string]any {
	raw, found := payload["class"]
	if !found {
		raw = payload["classlist"]
	}
	rows, ok := raw.([]any)
	if !ok {
		return nil
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if entry, ok := row.(map[string]any); ok {
			items = append(items, entry)
		}
	}
	return items
}

func maccmsAPIString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case bool:
		if typed {
			return "1"
		}
		return ""
	case json.Number:
		return typed.String()
	}
	return ""
}

func maccmsAPIInt(value any) int {
	text := maccmsAPIString(value)
	if text == "" {
		return 0
	}
	number, err := strconv.Atoi(text)
	if err != nil {
		if parsed, perr := strconv.ParseFloat(text, 64); perr == nil {
			return int(parsed)
		}
		return 0
	}
	return number
}

var maccmsAPITagPattern = regexp.MustCompile(`(?s)<[^>]*>`)

func maccmsAPICleanText(value string) string {
	text := maccmsAPITagPattern.ReplaceAllString(value, " ")
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&amp;", "&")
	return strings.TrimSpace(text)
}

// maccmsAPIDrama 把一条 API 记录转成剧集卡片。
func maccmsAPIDrama(source, base string, item map[string]any) (Drama, []Chapter) {
	id := maccmsAPIString(item["vod_id"])
	if id == "" {
		id = maccmsAPIString(item["id"])
	}
	if id == "" || !webProviderNumericID.MatchString(id) {
		return Drama{}, nil
	}
	title := strings.TrimSpace(maccmsAPICleanText(maccmsAPIString(item["vod_name"])))
	if title == "" {
		title = strings.TrimSpace(maccmsAPICleanText(maccmsAPIString(item["name"])))
	}
	if title == "" {
		return Drama{}, nil
	}
	cover := providerCoverAddress(maccmsAPIString(item["vod_pic"]), base+"/")
	if cover == "" {
		cover = providerCoverAddress(maccmsAPIString(item["vod_pic_slide"]), base+"/")
	}
	category := strings.TrimSpace(maccmsAPIString(item["type_name"]))
	if category == "" {
		category = strings.TrimSpace(maccmsAPIString(item["vod_class"]))
	}
	intro := maccmsAPICleanText(maccmsAPIString(item["vod_content"]))
	if intro == "" {
		intro = maccmsAPICleanText(maccmsAPIString(item["vod_blurb"]))
	}
	remarks := strings.TrimSpace(maccmsAPIString(item["vod_remarks"]))
	drama := Drama{
		ID:          providerDramaID(source, id),
		Source:      source,
		SourceID:    id,
		Title:       title,
		Intro:       truncate(intro, 2000),
		Cover:       cover,
		Category:    category,
		ChannelName: duanjuSourceName(source),
		Remark:      remarks,
	}
	chapters := maccmsAPIChapters(source, base, id, item)
	drama.EpisodeCount = json.Number(strconv.Itoa(len(chapters)))
	return drama, chapters
}

type maccmsAPIEpisode struct {
	Title string
	URL   string
}

// maccmsAPIRouteGroups 解析 vod_play_from / vod_play_url：
// 线路之间用 $$$ 分隔，线路内分集用 # 分隔，每集形如「第1集$地址」。
func maccmsAPIRouteGroups(item map[string]any) [][]maccmsAPIEpisode {
	playURL := maccmsAPIString(item["vod_play_url"])
	if playURL == "" {
		playURL = maccmsAPIString(item["vod_url"])
	}
	if playURL == "" {
		return nil
	}
	// 少数老模板用 \r\n 分隔分集，先归一成 #。
	playURL = strings.ReplaceAll(playURL, "\r\n", "#")
	playURL = strings.ReplaceAll(playURL, "\n", "#")
	groups := strings.Split(playURL, "$$$")
	var routes [][]maccmsAPIEpisode
	for _, group := range groups {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		var episodes []maccmsAPIEpisode
		for _, entry := range strings.Split(group, "#") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			name, address := entry, ""
			if index := strings.LastIndex(entry, "$"); index >= 0 {
				name = strings.TrimSpace(entry[:index])
				address = strings.TrimSpace(entry[index+1:])
			}
			address = strings.ReplaceAll(address, `\/`, "/")
			if address == "" {
				continue
			}
			episodes = append(episodes, maccmsAPIEpisode{
				Title: strings.TrimSpace(maccmsAPICleanText(name)),
				URL:   address,
			})
		}
		if len(episodes) > 0 {
			routes = append(routes, episodes)
		}
	}
	return routes
}

// maccmsAPIChapters 取「集数最全」的那条线路；并列时取靠前的一条。
func maccmsAPIChapters(source, base, sourceID string, item map[string]any) []Chapter {
	routes := maccmsAPIRouteGroups(item)
	if len(routes) == 0 {
		return nil
	}
	best := 0
	for index, route := range routes {
		if len(route) > len(routes[best]) {
			best = index
		}
	}
	detail := base + "/index.php/vod/detail/id/" + sourceID + ".html"
	chapters := make([]Chapter, 0, len(routes[best]))
	for index, episode := range routes[best] {
		number := duanjuEpisodeNumber(episode.Title, index+1)
		chapters = append(chapters, duanjuChapter(source, sourceID, number, episode.Title, episode.URL, detail, base+"/"))
	}
	sortDuanjuChapters(chapters)
	return chapters
}

// fetchMaccmsAPICategories 取分类（ac=list 或直接取 videolist 带回的 class）。
func (d *Downloader) fetchMaccmsAPICategories(ctx context.Context, endpoint, base string) []nativeCategory {
	var categories []nativeCategory
	var classes []map[string]any
	for _, address := range []string{endpoint + "?ac=list", endpoint + "?ac=videolist&pg=1"} {
		payload, err := d.maccmsAPICall(ctx, address, base+"/")
		if err != nil {
			continue
		}
		if rows := maccmsAPIClasses(payload); len(rows) > 0 {
			classes = rows
			break
		}
	}
	for _, row := range classes {
		id := maccmsAPIString(row["type_id"])
		name := strings.TrimSpace(maccmsAPICleanText(maccmsAPIString(row["type_name"])))
		if id == "" || name == "" {
			continue
		}
		categories = append(categories, nativeCategory{ID: id, Name: name})
	}
	if len(categories) > 0 {
		return categories
	}
	// 很多站点的 ac=list 并不返回 class 数组（实测直接回 videolist）。
	// 退化方案：从列表接口返回的条目里回收 type_id / type_name，够用且稳。
	seen := map[string]bool{}
	for page := 1; page <= 3 && len(seen) < 12; page++ {
		payload, err := d.maccmsAPICall(ctx, endpoint+"?ac=videolist&pg="+strconv.Itoa(page), base+"/")
		if err != nil {
			break
		}
		for _, item := range maccmsAPIItems(payload) {
			id := maccmsAPIString(item["type_id"])
			name := strings.TrimSpace(maccmsAPICleanText(maccmsAPIString(item["type_name"])))
			if id == "" || name == "" || seen[id] {
				continue
			}
			seen[id] = true
			categories = append(categories, nativeCategory{ID: id, Name: name})
		}
	}
	return categories
}

// fetchMaccmsAPICatalog 取目录/分类分页。category 为空表示全部。
func (d *Downloader) fetchMaccmsAPICatalog(ctx context.Context, source, endpoint, base string, page int, category string) ([]Drama, bool, error) {
	if page < 1 {
		page = 1
	}
	query := url.Values{}
	query.Set("ac", "videolist")
	query.Set("pg", strconv.Itoa(page))
	if trimmed := strings.TrimSpace(category); trimmed != "" {
		query.Set("t", trimmed)
	}
	address := endpoint + "?" + query.Encode()
	payload, err := d.maccmsAPICall(ctx, address, base+"/")
	if err != nil {
		return nil, false, err
	}
	return d.maccmsAPICatalogFromPayload(source, base, payload, page)
}

func (d *Downloader) maccmsAPICatalogFromPayload(source, base string, payload map[string]any, page int) ([]Drama, bool, error) {
	items := maccmsAPIItems(payload)
	var dramas []Drama
	for _, item := range items {
		drama, _ := maccmsAPIDrama(source, base, item)
		if drama.ID == "" {
			continue
		}
		dramas = append(dramas, drama)
	}
	if len(dramas) == 0 {
		return nil, false, errMaccmsAPIEmpty
	}
	hasMore := true
	if count := maccmsAPIInt(payload["pagecount"]); count > 0 {
		hasMore = page < count
	}
	return dramas, hasMore, nil
}

// fetchMaccmsAPISearch 用 wd 参数搜索。
func (d *Downloader) fetchMaccmsAPISearch(ctx context.Context, source, endpoint, base, query string) ([]Drama, error) {
	keyword := strings.TrimSpace(query)
	if keyword == "" {
		return nil, nil
	}
	values := url.Values{}
	values.Set("ac", "videolist")
	values.Set("wd", keyword)
	address := endpoint + "?" + values.Encode()
	payload, err := d.maccmsAPICall(ctx, address, base+"/")
	if err == nil {
		dramas, _, convErr := d.maccmsAPICatalogFromPayload(source, base, payload, 1)
		if convErr == nil {
			return dramas, nil
		}
		err = convErr
	}
	// 部分站点把搜索挂在 ac=search 上。
	values.Set("ac", "search")
	address = endpoint + "?" + values.Encode()
	if retry, retryErr := d.maccmsAPICall(ctx, address, base+"/"); retryErr == nil {
		if dramas, _, convErr := d.maccmsAPICatalogFromPayload(source, base, retry, 1); convErr == nil {
			return dramas, nil
		}
	}
	if err == nil {
		err = errMaccmsAPIEmpty
	}
	return nil, err
}

// fetchMaccmsAPIDetail 用 ids 取单部剧；返回剧集与分集（分集地址多为直链）。
func (d *Downloader) fetchMaccmsAPIDetail(ctx context.Context, source, endpoint, base, sourceID string) (Drama, []Chapter, error) {
	addresses := []string{
		endpoint + "?ac=detail&ids=" + url.QueryEscape(sourceID),
		endpoint + "?ac=videolist&ids=" + url.QueryEscape(sourceID),
	}
	var lastErr error
	for _, address := range addresses {
		payload, err := d.maccmsAPICall(ctx, address, base+"/")
		if err != nil {
			lastErr = err
			continue
		}
		for _, item := range maccmsAPIItems(payload) {
			drama, chapters := maccmsAPIDrama(source, base, item)
			if drama.ID == "" {
				continue
			}
			if len(chapters) == 0 {
				lastErr = &maccmsAPIError{"接口未返回分集地址"}
				continue
			}
			return drama, chapters, nil
		}
		lastErr = errMaccmsAPIEmpty
	}
	if lastErr == nil {
		lastErr = &maccmsAPIError{"接口未返回该剧"}
	}
	return Drama{}, nil, lastErr
}
