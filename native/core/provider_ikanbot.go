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
	"time"
)

// 爱看机器人（ikanbot）原生站源：搜索引擎式站点，聚合全网免费在线播放资源，
// 自身不存储影片，线路数量多、版本/清晰度各异。
//
// 上游页面（实测 2026-10，走国内网络）：
//   GET /hot/index-{kind}-{分类}.html       分类第 1 页（kind: movie / tv）
//   GET /hot/index-{kind}-{分类}-p-{n}.html 分类翻页
//   GET /search?q={关键词}                  搜索（服务端渲染，无翻页）
//   GET /play/{id}                          详情/播放页（含线路与分集所需的令牌）
//   GET /api/getResN?videoId={id}&mtype={1|2}&token={签名}  线路列表（JSON）
//
// 列表卡片是 <a class="item" href="/play/{id}">，封面取 img[data-src]，标题取 alt 或 <p>；
// 搜索结果用 Bootstrap media 结构。详情页把当前影片 id 与一次性令牌放在
// <input id="current_id"> / <input id="e_token">，取列表时必须先用两者算出 token，
// 否则接口返回 {"state":-401,"message":"unauthorized"}。
//
// 线路接口返回 data.list，每项 resData 是 JSON 字符串 "[{\"flag\":\"线路名\",
// \"url\":\"第01集$https://.../index.m3u8#第02集$https://...\"}]"，
// 分集之间用 # 分隔、名称与地址之间用 $ 分隔。
//
// 一部片子通常有 30 条左右线路（gsm3u8 / jsm3u8 / 1080zyk …），各线路的分集完整度
// 与 CDN 都不同，同一集可能有几十个可用地址。因此详情只拿分集最全的一条线路建分集，
// 播放时把每条线路同名的那一集一起作为 providerMedia.Variants 交给播放层，
// 这样播放器里的「播放线路」可以逐条切换，某条线路失效时不用重新解析。

const (
	ikanbotSiteBaseURL = "https://www1.ikanbot.com"
	ikanbotUserAgent   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	ikanbotPageSize    = 36
	ikanbotMaxLines    = 40
	ikanbotLineTTL     = 10 * time.Minute
)

// 分类 ID 形如 "{kind}-{名称}"，与 /hot/index-{id}.html 一一对应。
var ikanbotCategories = []nativeCategory{
	{ID: "movie-热门", Name: "热门电影"},
	{ID: "tv-热门", Name: "热门剧集"},
	{ID: "tv-国产剧", Name: "国产剧"},
	{ID: "tv-日剧", Name: "日剧"},
	{ID: "tv-韩剧", Name: "韩剧"},
	{ID: "tv-美剧", Name: "美剧"},
	{ID: "tv-英剧", Name: "英剧"},
	{ID: "tv-港剧", Name: "港剧"},
	{ID: "tv-日本动画", Name: "日本动画"},
	{ID: "tv-综艺", Name: "综艺"},
	{ID: "tv-纪录片", Name: "纪录片"},
}

var (
	reIkanbotCardLink = regexp.MustCompile(`(?is)<a class="item" href="/play/(\d+)"`)
	reIkanbotImgData  = regexp.MustCompile(`(?is)<img[^>]*data-src="([^"]+)"`)
	reIkanbotImgAlt   = regexp.MustCompile(`(?is)<img[^>]*alt="([^"]*)"`)
	reIkanbotCardText = regexp.MustCompile(`(?is)<p>(.*?)</p>`)
	reIkanbotPageMax  = regexp.MustCompile(`(?is)-p-(\d+)\.html`)

	reIkanbotMediaBlock = regexp.MustCompile(`(?is)<div class="media">(.*?)</div>\s*</div>`)
	reIkanbotMediaHref  = regexp.MustCompile(`(?is)href="/play/(\d+)"`)
	reIkanbotTitleText  = regexp.MustCompile(`(?is)<a href="/play/\d+" class="title-text">(.*?)</a>`)
	reIkanbotLineLabel  = regexp.MustCompile(`(?is)<span class="label"[^>]*>\[([^\]]*)\]</span>`)
	reIkanbotSmallText  = regexp.MustCompile(`(?is)<span class="small"[^>]*>(.*?)</span>`)

	reIkanbotCurrentID = regexp.MustCompile(`(?is)<input type="hidden" id="current_id" value="([^"]*)"/>`)
	reIkanbotToken     = regexp.MustCompile(`(?is)<input type="hidden" id="e_token" value="([^"]*)"/>`)
	reIkanbotMType     = regexp.MustCompile(`(?is)<input type="hidden" id="mtype" value="([^"]*)"/>`)
	reIkanbotTitle     = regexp.MustCompile(`(?is)<h1 id="video_title"[^>]*>(.*?)</h1>`)
	reIkanbotCoverImg  = regexp.MustCompile(`(?is)<img id="\d+"[^>]*class="cover lazy"[^>]*data-src="([^"]+)"`)
	reIkanbotMetaText  = regexp.MustCompile(`(?is)<h3 class="meta">(.*?)</h3>`)
	reIkanbotTagName   = regexp.MustCompile(`<[^>]+>`)
	reIkanbotEpisodeNo = regexp.MustCompile(`(\d+)`)
	reIkanbotYear      = regexp.MustCompile(`(19|20)\d{2}`)
	reIkanbotFlagRes   = regexp.MustCompile(`(2160|1440|1080|720|480)`)
)

// ikanbotEpisode 一条线路里的一集。Order 优先取名称里的数字（第01集 → 1），
// 电影这类没有集数的名称（正片 / HD中字）退化为列表位置，用于跨线路对齐同一集。
type ikanbotEpisode struct {
	Name  string
	URL   string
	Order int
}

// ikanbotLine 一条完整线路：线路名 + 该线路的分集列表。
type ikanbotLine struct {
	Flag     string
	Episodes []ikanbotEpisode
}

type ikanbotLineEntry struct {
	lines     []ikanbotLine
	expiresAt time.Time
}

type ikanbotLineCall struct {
	done  chan struct{}
	lines []ikanbotLine
	err   error
}

// ikanbotPlayMeta 详情页里线路接口需要的字段，顺带带上封面与简介。
type ikanbotPlayMeta struct {
	Title     string
	Cover     string
	Desc      string
	Year      string
	Region    string
	Actors    string
	CurrentID string
	Token     string
	MType     string
}

// ikanbotClean 去标签、解 HTML 实体、压缩空白。
func ikanbotClean(text string) string {
	if text == "" {
		return ""
	}
	text = reIkanbotTagName.ReplaceAllString(text, "")
	text = html.UnescapeString(text)
	text = strings.ReplaceAll(text, "\u3000", " ")
	return strings.Join(strings.Fields(text), " ")
}

func ikanbotNumericID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 12 {
		return false
	}
	number, err := strconv.Atoi(value)
	return err == nil && number > 0
}

// validIkanbotCategory 只放行内置分类，避免把任意字符串拼进上游路径。
func validIkanbotCategory(category string) bool {
	if category == "" {
		return true
	}
	if len(category) > 32 || strings.ContainsAny(category, "|/\\\x00\r\n") {
		return false
	}
	for _, entry := range ikanbotCategories {
		if entry.ID == category {
			return true
		}
	}
	return false
}

// ikanbotKind 从分类 ID 拆出 movie / tv，用于详情字段归类。
func ikanbotKind(category string) string {
	if strings.HasPrefix(category, "movie-") {
		return "电影"
	}
	return "剧集"
}

func ikanbotFixURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	} else if strings.HasPrefix(raw, "/") {
		raw = ikanbotSiteBaseURL + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || !validNativeCoverURL(parsed) {
		return ""
	}
	return parsed.String()
}

// ikanbotSign 复刻页面混淆脚本里的 get_tks：取影片 ID 末 4 位，
// 每位数字 n 得出步长 k = n%3+1，从令牌串当前位置截取 8 个字符并推进 k+8 位。
// 站点用这个签名挡住直接调用线路接口，算错会返回 -401 unauthorized。
func ikanbotSign(currentID, eToken string) string {
	if len(currentID) < 4 || len(eToken) < 16 {
		return ""
	}
	tail := currentID[len(currentID)-4:]
	rest := eToken
	builder := strings.Builder{}
	for i := 0; i < len(tail); i++ {
		number, err := strconv.Atoi(string(tail[i]))
		if err != nil {
			return ""
		}
		start := number%3 + 1
		if start+8 > len(rest) {
			return ""
		}
		builder.WriteString(rest[start : start+8])
		rest = rest[start+8:]
	}
	return builder.String()
}

func (d *Downloader) fetchIkanbotCategories() []nativeCategory {
	return append([]nativeCategory(nil), ikanbotCategories...)
}

func (d *Downloader) ikanbotGet(ctx context.Context, path string) (string, error) {
	site := d.providerBaseURL(sourceIkanbot)
	address := path
	if !strings.HasPrefix(address, "http") {
		address = site + path
	}
	pageContext := context.WithValue(ctx, providerTextUserAgentKey{}, ikanbotUserAgent)
	return d.fetchProviderText(pageContext, address, site+"/")
}

// parseIkanbotCards 解析分类页/首页的 item 卡片。
func parseIkanbotCards(body string, category string) []Drama {
	items := make([]Drama, 0, ikanbotPageSize)
	seen := map[string]bool{}
	for _, match := range reIkanbotCardLink.FindAllStringSubmatchIndex(body, -1) {
		if len(match) < 4 {
			continue
		}
		id := body[match[2]:match[3]]
		if !ikanbotNumericID(id) || seen[id] {
			continue
		}
		chunk := body[match[1]:]
		if end := len(chunk); end > 900 {
			chunk = chunk[:900]
		}
		title := ""
		if alt := reIkanbotImgAlt.FindStringSubmatch(chunk); len(alt) > 1 {
			title = ikanbotClean(alt[1])
		}
		if title == "" {
			if text := reIkanbotCardText.FindStringSubmatch(chunk); len(text) > 1 {
				title = ikanbotClean(text[1])
			}
		}
		if title == "" {
			continue
		}
		cover := ""
		if image := reIkanbotImgData.FindStringSubmatch(chunk); len(image) > 1 {
			cover = ikanbotFixURL(image[1])
		}
		seen[id] = true
		items = append(items, Drama{
			ID: providerDramaID(sourceIkanbot, id), Source: sourceIkanbot, SourceID: id,
			Title: truncate(title, 512), Name: truncate(title, 512),
			Cover: cover, CoverURL: cover, ChannelName: "爱看机器人",
			CategoryName: ikanbotKind(category),
		})
		if len(items) > 500 {
			break
		}
	}
	return items
}

// parseIkanbotSearchCards 解析搜索结果的 media 区块。
func parseIkanbotSearchCards(body string) []Drama {
	items := make([]Drama, 0, 24)
	seen := map[string]bool{}
	for _, block := range reIkanbotMediaBlock.FindAllStringSubmatch(body, -1) {
		chunk := block[1]
		if len(chunk) > 2000 {
			chunk = chunk[:2000]
		}
		href := reIkanbotMediaHref.FindStringSubmatch(chunk)
		if len(href) < 2 {
			continue
		}
		id := href[1]
		if !ikanbotNumericID(id) || seen[id] {
			continue
		}
		title := ""
		if text := reIkanbotTitleText.FindStringSubmatch(chunk); len(text) > 1 {
			title = ikanbotClean(text[1])
		}
		if title == "" {
			if alt := reIkanbotImgAlt.FindStringSubmatch(chunk); len(alt) > 1 {
				title = ikanbotClean(alt[1])
			}
		}
		if title == "" {
			continue
		}
		cover := ""
		if image := reIkanbotImgData.FindStringSubmatch(chunk); len(image) > 1 {
			cover = ikanbotFixURL(image[1])
		}
		remark := ""
		if label := reIkanbotLineLabel.FindStringSubmatch(chunk); len(label) > 1 {
			remark = ikanbotClean(label[1])
		}
		note := ""
		for _, small := range reIkanbotSmallText.FindAllStringSubmatch(chunk, -1) {
			value := ikanbotClean(small[1])
			if value != "" {
				note = truncate(value, 96)
				break
			}
		}
		seen[id] = true
		items = append(items, Drama{
			ID: providerDramaID(sourceIkanbot, id), Source: sourceIkanbot, SourceID: id,
			Title: truncate(title, 512), Name: truncate(title, 512),
			Cover: cover, CoverURL: cover, ChannelName: "爱看机器人",
			CategoryName: "剧集", Remark: truncate(remark, 64), Tags: nil,
			Desc: truncate(note, 512),
		})
		if len(items) > 100 {
			break
		}
	}
	return items
}

// ikanbotPageCount 从分页链接推断末页，拿不到时按是否满页保守判断。
func ikanbotPageCount(body string, page, count int) int {
	maxPage := page
	for _, match := range reIkanbotPageMax.FindAllStringSubmatch(body, -1) {
		if value, err := strconv.Atoi(match[1]); err == nil && value > maxPage && value < 100000 {
			maxPage = value
		}
	}
	if maxPage > page {
		return maxPage
	}
	if count >= ikanbotPageSize {
		return page + 1
	}
	return page
}

func (d *Downloader) fetchIkanbotCatalogPage(ctx context.Context, page int, category, query string) ([]Drama, bool, error) {
	if page < 1 || page > 100000 {
		return nil, false, errors.New("爱看机器人目录页码无效")
	}
	if query != "" {
		return d.searchIkanbot(ctx, page, query)
	}
	if !validIkanbotCategory(category) {
		return nil, false, errors.New("爱看机器人内容分类无效")
	}
	if category == "" {
		category = ikanbotCategories[0].ID
	}
	path := "/hot/index-" + category + ".html"
	if page > 1 {
		path = fmt.Sprintf("/hot/index-%s-p-%d.html", category, page)
	}
	body, err := d.ikanbotGet(ctx, path)
	if err != nil {
		return nil, false, err
	}
	items := parseIkanbotCards(body, category)
	if len(items) == 0 {
		return []Drama{}, false, nil
	}
	pageCount := ikanbotPageCount(body, page, len(items))
	return items, page < pageCount, nil
}

func (d *Downloader) searchIkanbot(ctx context.Context, page int, query string) ([]Drama, bool, error) {
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 256 {
		return nil, false, errors.New("爱看机器人搜索关键词无效")
	}
	if page > 1 {
		// 站点搜索结果不分页。
		return []Drama{}, false, nil
	}
	body, err := d.ikanbotGet(ctx, "/search?"+url.Values{"q": {query}}.Encode())
	if err != nil {
		return nil, false, err
	}
	return parseIkanbotSearchCards(body), false, nil
}

type ikanbotResEntry struct {
	Flag string `json:"flag"`
	URL  string `json:"url"`
}

type ikanbotResPayload struct {
	State   int    `json:"state"`
	Message string `json:"message"`
	Data    struct {
		List []struct {
			SiteID  int    `json:"siteId"`
			ID      int64  `json:"id"`
			ResData string `json:"resData"`
		} `json:"list"`
	} `json:"data"`
}

// parseIkanbotLines 把线路接口返回的原始结构整理成按分集数降序的线路列表。
// 站点经常把同一份源挂在不同 flag 下（实测 xlm3u8 与 subm3u8 完全一致），
// 这里按全部分集地址去重，避免播放器里出现两条一模一样的线路。
func parseIkanbotLines(payload ikanbotResPayload) []ikanbotLine {
	lines := make([]ikanbotLine, 0, len(payload.Data.List))
	seen := map[string]bool{}
	for _, item := range payload.Data.List {
		entries := []ikanbotResEntry{}
		if err := json.Unmarshal([]byte(item.ResData), &entries); err != nil {
			continue
		}
		for _, entry := range entries {
			episodes := make([]ikanbotEpisode, 0, 8)
			orders := map[int]bool{}
			signature := strings.Builder{}
			for index, pair := range ikanbotSplitEpisodes(entry.URL) {
				address := ikanbotFixMediaURL(pair[1])
				if address == "" {
					continue
				}
				name := ikanbotClean(pair[0])
				order := index + 1
				if number := reIkanbotEpisodeNo.FindString(name); number != "" {
					if parsed, err := strconv.Atoi(number); err == nil && parsed > 0 && parsed <= 100000 {
						order = parsed
					}
				}
				if orders[order] {
					continue
				}
				orders[order] = true
				episodes = append(episodes, ikanbotEpisode{Name: name, URL: address, Order: order})
				signature.WriteString(address)
				signature.WriteByte('\n')
			}
			if len(episodes) == 0 {
				continue
			}
			key := signature.String()
			if seen[key] {
				continue
			}
			seen[key] = true
			lines = append(lines, ikanbotLine{Flag: strings.TrimSpace(entry.Flag), Episodes: episodes})
		}
	}
	// 分集最全的线路排在前面，详情用它建分集、播放时它作为默认线路。
	sort.SliceStable(lines, func(i, j int) bool { return len(lines[i].Episodes) > len(lines[j].Episodes) })
	if len(lines) > ikanbotMaxLines {
		lines = lines[:ikanbotMaxLines]
	}
	return lines
}

// fetchIkanbotLines 拉取线路列表，需要先用详情里的 current_id 与一次性令牌算出签名。
// meta 非空时复用调用方已经拿到的详情页，省一次请求。
func (d *Downloader) fetchIkanbotLines(ctx context.Context, sourceID string, meta *ikanbotPlayMeta) ([]ikanbotLine, error) {
	currentID, token, mType := sourceID, "", ""
	if meta != nil {
		currentID, token, mType = meta.CurrentID, meta.Token, meta.MType
	}
	if token == "" {
		body, err := d.ikanbotGet(ctx, "/play/"+sourceID)
		if err != nil {
			return nil, err
		}
		page, err := parseIkanbotPlayPage(body, sourceID)
		if err != nil {
			return nil, err
		}
		currentID, token, mType = page.CurrentID, page.Token, page.MType
	}
	sign := ikanbotSign(currentID, token)
	if sign == "" {
		return nil, errors.New("爱看机器人播放令牌无效，请刷新详情")
	}
	if mType == "" {
		mType = "1"
	}
	address := ikanbotGetResURL(d.providerBaseURL(sourceIkanbot), currentID, mType, sign)
	pageContext := context.WithValue(ctx, providerTextUserAgentKey{}, ikanbotUserAgent)
	body, err := d.fetchProviderText(pageContext, address, d.providerBaseURL(sourceIkanbot)+"/play/"+sourceID)
	if err != nil {
		return nil, err
	}
	payload := ikanbotResPayload{}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil, errors.New("爱看机器人线路返回无法解析")
	}
	if payload.State != 1 {
		message := strings.TrimSpace(payload.Message)
		if message == "" {
			message = "unauthorized"
		}
		return nil, errors.New("爱看机器人线路获取失败：" + message)
	}
	lines := parseIkanbotLines(payload)
	if len(lines) == 0 {
		return nil, errors.New("爱看机器人暂无可播放线路")
	}
	return lines, nil
}

// ikanbotLines 带短缓存与单飞的线路入口：详情与播放解析共用同一份结果，
// 避免连续切换分集时反复抓详情页换令牌。
func (d *Downloader) ikanbotLines(ctx context.Context, sourceID string, meta *ikanbotPlayMeta) ([]ikanbotLine, error) {
	d.providerMu.Lock()
	if cached, found := d.ikanbotLineCache[sourceID]; found && time.Now().Before(cached.expiresAt) {
		d.providerMu.Unlock()
		return cached.lines, nil
	}
	if pending := d.ikanbotLinePending[sourceID]; pending != nil {
		d.providerMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-pending.done:
			if (errors.Is(pending.err, context.Canceled) || errors.Is(pending.err, context.DeadlineExceeded)) && ctx.Err() == nil {
				return d.ikanbotLines(ctx, sourceID, meta)
			}
			return pending.lines, pending.err
		}
	}
	if d.ikanbotLinePending == nil {
		d.ikanbotLinePending = make(map[string]*ikanbotLineCall)
	}
	call := &ikanbotLineCall{done: make(chan struct{})}
	d.ikanbotLinePending[sourceID] = call
	d.providerMu.Unlock()
	lines, err := d.fetchIkanbotLines(ctx, sourceID, meta)
	d.providerMu.Lock()
	if err == nil {
		if d.ikanbotLineCache == nil || len(d.ikanbotLineCache) >= 64 {
			d.ikanbotLineCache = make(map[string]ikanbotLineEntry)
		}
		d.ikanbotLineCache[sourceID] = ikanbotLineEntry{lines: lines, expiresAt: time.Now().Add(ikanbotLineTTL)}
	}
	call.lines, call.err = lines, err
	delete(d.ikanbotLinePending, sourceID)
	close(call.done)
	d.providerMu.Unlock()
	return lines, err
}

// ikanbotLineEpisode 在一条线路里按集号取对应分集。电影各线路的分集名不同
// （正片 / HD中字 / TC），只有一集时直接取它。
func ikanbotLineEpisode(line ikanbotLine, order int) (ikanbotEpisode, bool) {
	for _, episode := range line.Episodes {
		if episode.Order == order {
			return episode, true
		}
	}
	if len(line.Episodes) == 1 && order == 1 {
		return line.Episodes[0], true
	}
	return ikanbotEpisode{}, false
}

// ikanbotEpisodeOptions 把各条线路里同一集的地址整理成可切换的线路列表。
func ikanbotEpisodeOptions(lines []ikanbotLine, order int, referer string) []providerMedia {
	options := make([]providerMedia, 0, len(lines))
	seen := map[string]bool{}
	for _, line := range lines {
		episode, found := ikanbotLineEpisode(line, order)
		if !found || seen[episode.URL] {
			continue
		}
		seen[episode.URL] = true
		options = append(options, providerMedia{
			URL: episode.URL, Referer: referer, Quality: ikanbotLineQuality(line.Flag),
		})
	}
	return options
}

// ikanbotLineQuality 线路名里带分辨率时（如 1080zyk）标出画质，供播放器排序。
func ikanbotLineQuality(flag string) int {
	match := reIkanbotFlagRes.FindString(strings.ToLower(flag))
	if match == "" {
		return 0
	}
	value, err := strconv.Atoi(match)
	if err != nil || value < 480 || value > 2160 {
		return 0
	}
	return value
}

func ikanbotGetResURL(site, videoID, mType, sign string) string {
	return fmt.Sprintf(
		"%s/api/getResN?videoId=%s&mtype=%s&token=%s",
		strings.TrimRight(site, "/"),
		url.QueryEscape(videoID),
		url.QueryEscape(mType),
		url.QueryEscape(sign),
	)
}

// ikanbotSplitEpisodes 把 "第01集$url#第02集$url" 拆成 名称/地址 对。
func ikanbotSplitEpisodes(raw string) [][2]string {
	pairs := [][2]string{}
	for _, part := range strings.Split(raw, "#") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		pieces := strings.SplitN(part, "$", 2)
		if len(pieces) != 2 {
			continue
		}
		name := strings.TrimSpace(pieces[0])
		address := strings.TrimSpace(pieces[1])
		// 少数线路会在地址后面再挂一个 "$线路名" 标记（实测 yhm3u8 每条都带），
		// 带着尾巴去请求会 500，截掉后正常。
		if cut, _, found := strings.Cut(address, "$"); found {
			address = strings.TrimSpace(cut)
		}
		if address == "" {
			continue
		}
		pairs = append(pairs, [2]string{name, address})
	}
	return pairs
}

// parseIkanbotPlayPage 从播放页取出线路接口需要的 current_id / e_token / mtype，
// 以及标题、封面与简介。
func parseIkanbotPlayPage(body, sourceID string) (ikanbotPlayMeta, error) {
	meta := ikanbotPlayMeta{CurrentID: sourceID, MType: "1"}
	if match := reIkanbotTitle.FindStringSubmatch(body); len(match) > 1 {
		meta.Title = ikanbotClean(match[1])
	}
	if meta.Title == "" {
		return meta, errors.New("爱看机器人详情缺少视频名称")
	}
	if match := reIkanbotCoverImg.FindStringSubmatch(body); len(match) > 1 {
		meta.Cover = ikanbotFixURL(match[1])
	}
	if meta.Cover == "" {
		if match := reIkanbotImgData.FindStringSubmatch(body); len(match) > 1 {
			meta.Cover = ikanbotFixURL(match[1])
		}
	}
	if match := reIkanbotCurrentID.FindStringSubmatch(body); len(match) > 1 {
		if value := strings.TrimSpace(match[1]); value != "" {
			meta.CurrentID = value
		}
	}
	if match := reIkanbotToken.FindStringSubmatch(body); len(match) > 1 {
		meta.Token = strings.TrimSpace(match[1])
	}
	if match := reIkanbotMType.FindStringSubmatch(body); len(match) > 1 {
		if value := strings.TrimSpace(match[1]); value != "" {
			meta.MType = value
		}
	}
	if meta.CurrentID == "" || meta.Token == "" {
		return meta, errors.New("爱看机器人缺少播放令牌，请刷新页面重试")
	}
	texts := []string{}
	for _, match := range reIkanbotMetaText.FindAllStringSubmatch(body, -1) {
		if value := ikanbotClean(match[1]); value != "" {
			texts = append(texts, value)
		}
	}
	meta.Desc = strings.Join(texts, " · ")
	for _, value := range texts {
		if meta.Year == "" && strings.HasPrefix(value, reIkanbotYear.FindString(value)) && len(value) <= 12 {
			meta.Year = value
			continue
		}
		if meta.Actors == "" && strings.Contains(value, "/") {
			meta.Actors = value
			continue
		}
		if meta.Region == "" && !strings.Contains(value, "/") && value != "" {
			meta.Region = value
		}
	}
	return meta, nil
}

func (d *Downloader) fetchIkanbotDetail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	if !ikanbotNumericID(sourceID) {
		return Drama{}, nil, errors.New("爱看机器人视频 ID 无效")
	}
	body, err := d.ikanbotGet(ctx, "/play/"+sourceID)
	if err != nil {
		return Drama{}, nil, err
	}
	page, err := parseIkanbotPlayPage(body, sourceID)
	if err != nil {
		return Drama{}, nil, err
	}
	drama := Drama{
		ID: providerDramaID(sourceIkanbot, sourceID), Source: sourceIkanbot, SourceID: sourceID,
		Title: truncate(page.Title, 512), Name: truncate(page.Title, 512),
		Desc: truncate(page.Desc, 12000), Intro: truncate(page.Desc, 12000),
		Cover: page.Cover, CoverURL: page.Cover, ChannelName: "爱看机器人",
		OnlineDate: truncate(page.Year, 32),
	}

	lines, err := d.ikanbotLines(ctx, sourceID, &page)
	if err != nil {
		return Drama{}, nil, err
	}
	primary := lines[0]

	chapters := []Chapter{}
	pageURL := fmt.Sprintf("%s/play/%s", strings.TrimRight(d.providerBaseURL(sourceIkanbot), "/"), sourceID)
	referer := strings.TrimRight(d.providerBaseURL(sourceIkanbot), "/") + "/"
	for _, episode := range primary.Episodes {
		order := episode.Order
		if order < 1 {
			order = len(chapters) + 1
		}
		title := episode.Name
		if title == "" {
			title = fmt.Sprintf("第 %d 集", order)
		}
		chapters = append(chapters, Chapter{
			// 分集 ID 用集号而不是地址：同一集在各线路上地址不同，
			// 播放时再按集号把每条线路的地址取出来供切换。
			ID:             providerChapterID(sourceIkanbot, sourceID, strconv.Itoa(order)),
			Source:         sourceIkanbot,
			Title:          truncate(title, 128),
			CurrentEpisode: rawEpisode(order),
			VideoURL:       episode.URL,
			PageURL:        pageURL,
			Referer:        referer,
		})
	}
	if len(chapters) == 0 {
		return Drama{}, nil, errors.New("爱看机器人暂无可播放分集")
	}
	sort.SliceStable(chapters, func(i, j int) bool {
		left, _ := strconv.Atoi(chapters[i].EpisodeString(i + 1))
		right, _ := strconv.Atoi(chapters[j].EpisodeString(j + 1))
		return left < right
	})
	drama.TotalEpisode, drama.EpisodeCount = len(chapters), len(chapters)
	if page.Actors != "" {
		drama.Tags = append(drama.Tags, truncate(page.Actors, 64))
	}
	if page.Region != "" {
		drama.Tags = append(drama.Tags, truncate(page.Region, 32))
	}
	return drama, chapters, nil
}

func ikanbotKindFromMType(mType string) string {
	if mType == "2" {
		return "剧集"
	}
	return "电影"
}

// ikanbotFixMediaURL 校验并归一化播放地址，只放行 http(s) 的 m3u8/mp4。
func ikanbotFixMediaURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil {
		return ""
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return ""
	}
	if parsed.Hostname() == "" {
		return ""
	}
	if !isProviderHTTPMediaURL(raw) {
		return ""
	}
	return parsed.String()
}

func (d *Downloader) resolveIkanbotMedia(ctx context.Context, task Task) (providerMedia, error) {
	source, sourceID, valid := splitProviderDramaID(task.DramaID)
	prefix := providerChapterID(sourceIkanbot, sourceID, "")
	if !valid || source != sourceIkanbot || !ikanbotNumericID(sourceID) || !strings.HasPrefix(task.Chapter.ID, prefix) {
		return providerMedia{}, errors.New("爱看机器人播放分集信息无效，请刷新详情")
	}
	payload := strings.TrimSpace(strings.TrimPrefix(task.Chapter.ID, prefix))
	referer := firstNonEmpty(task.Chapter.Referer, d.providerBaseURL(sourceIkanbot)+"/")

	// 分集 ID 记的是集号，按集号把每条线路的这一集都取出来，供播放器切换线路。
	options := []providerMedia{}
	lineErr := error(nil)
	if order, err := strconv.Atoi(payload); err == nil && order > 0 && order <= 100000 {
		var lines []ikanbotLine
		lines, lineErr = d.ikanbotLines(ctx, sourceID, nil)
		if lineErr == nil {
			options = ikanbotEpisodeOptions(lines, order, referer)
		}
	}
	// 兜底：旧格式的分集 ID（直接存地址）或线路接口临时失败时，用详情里的地址先播起来。
	if len(options) == 0 {
		if address := ikanbotFixMediaURL(payload); address != "" {
			options = append(options, providerMedia{URL: address, Referer: referer})
		} else if address := ikanbotFixMediaURL(task.Chapter.VideoURL); address != "" {
			options = append(options, providerMedia{URL: address, Referer: referer})
		}
	}
	if len(options) == 0 {
		if lineErr != nil {
			return providerMedia{}, lineErr
		}
		return providerMedia{}, errors.New("爱看机器人该集没有可用线路，请刷新详情")
	}
	media := options[0]
	media.Variants = options
	if strings.HasSuffix(strings.ToLower(media.URL), ".m3u8") {
		return d.prepareWebProviderMedia(ctx, media, "爱看机器人")
	}
	return media, nil
}
