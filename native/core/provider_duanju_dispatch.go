package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

func (d *Downloader) fetchDuanjuCategories(ctx context.Context, source string) ([]nativeCategory, error) {
	source = canonicalProviderSource(source)
	if !isDuanjuProviderSource(source) {
		return nil, errors.New("该站源不支持分类读取")
	}
	if static, found := duanjuStaticCategories[source]; found {
		categories := make([]nativeCategory, 0, len(static))
		for _, entry := range static {
			categories = append(categories, nativeCategory{ID: entry.ID, Name: entry.Name})
		}
		return categories, nil
	}
	switch source {
	case sourceYaguo:
		return d.fetchYaguoCategories(ctx)
	case sourceMaoguo:
		return d.fetchMaoguoCategories(ctx)
	}
	if isCustomMaccmsSource(source) {
		return d.fetchMaccmsCustomCategories(ctx, source)
	}
	return nil, errors.New("该站源暂未提供分类")
}

func (d *Downloader) fetchDuanjuCatalogPage(ctx context.Context, source string, page int, category string) ([]Drama, bool, error) {
	source = canonicalProviderSource(source)
	switch source {
	case sourceYaguo:
		return d.fetchYaguoCatalogPage(ctx, page, category)
	case sourceMaoguo:
		return d.fetchMaoguoCatalogPage(ctx, page, category)
	case sourceFanguo:
		return d.fetchFanguoCatalogPage(ctx, page, category)
	case sourceGuanguo:
		return d.fetchGuanguoCatalogPage(ctx, page, category)
	case sourceHeguo:
		return d.fetchHeguoCatalogPage(ctx, page, category)
	case sourceXingguo:
		return d.fetchXingguoCatalogPage(ctx, page, category)
	case sourceNiuguo:
		return d.fetchNiuguoCatalogPage(ctx, page, category)
	case sourceHuaguo, sourceFaguo, sourceWuguo, sourceWangguo:
		return d.fetchMaccmsCatalogPage(ctx, source, page, category)
	default:
		if isCustomMaccmsSource(source) {
			return d.fetchMaccmsCatalogPage(ctx, source, page, category)
		}
	}
	return nil, false, errors.New("该站源暂未接入目录")
}

func (d *Downloader) fetchDuanjuDetail(ctx context.Context, source, sourceID string) (Drama, []Chapter, error) {
	source = canonicalProviderSource(source)
	switch source {
	case sourceYaguo:
		return d.fetchYaguoDetail(ctx, sourceID)
	case sourceMaoguo:
		return d.fetchMaoguoDetail(ctx, sourceID)
	case sourceFanguo:
		return d.fetchFanguoDetail(ctx, sourceID)
	case sourceGuanguo:
		return d.fetchGuanguoDetail(ctx, sourceID)
	case sourceHeguo:
		return d.fetchHeguoDetail(ctx, sourceID)
	case sourceXingguo:
		return d.fetchXingguoDetail(ctx, sourceID)
	case sourceNiuguo:
		return d.fetchNiuguoDetail(ctx, sourceID)
	case sourceHuaguo, sourceFaguo, sourceWuguo, sourceWangguo:
		return d.fetchMaccmsDetail(ctx, source, sourceID)
	default:
		if isCustomMaccmsSource(source) {
			return d.fetchMaccmsDetail(ctx, source, sourceID)
		}
	}
	return Drama{}, nil, errors.New("该站源暂未接入详情")
}

func (d *Downloader) searchDuanju(ctx context.Context, source, query string) ([]Drama, error) {
	source = canonicalProviderSource(source)
	switch source {
	case sourceYaguo:
		return d.searchYaguo(ctx, query)
	case sourceMaoguo:
		return d.searchMaoguo(ctx, query)
	case sourceFanguo:
		return d.searchFanguo(ctx, query)
	case sourceGuanguo:
		return d.searchGuanguo(ctx, query)
	case sourceHeguo:
		return d.searchHeguo(ctx, query)
	case sourceXingguo:
		return d.searchXingguo(ctx, query)
	case sourceNiuguo:
		return d.searchNiuguo(ctx, query)
	case sourceHuaguo, sourceFaguo, sourceWuguo, sourceWangguo:
		return d.searchMaccms(ctx, source, query)
	default:
		if isCustomMaccmsSource(source) {
			return d.searchMaccms(ctx, source, query)
		}
	}
	return nil, errors.New("该站源不支持在线搜索")
}

func duanjuSupportsSearch(source string) bool {
	spec, found := duanjuSourceSpecFor(source)
	return found && spec.Searcher
}

func duanjuSupportsPaging(source string) bool {
	spec, found := duanjuSourceSpecFor(source)
	return found && spec.Paged
}

func (d *Downloader) resolveDuanjuMedia(ctx context.Context, task Task) (providerMedia, error) {
	source := canonicalProviderSource(task.Chapter.Source)
	name := duanjuSourceName(source)
	switch source {
	case sourceHeguo:
		return d.resolveHeguoMedia(ctx, task)
	case sourceNiuguo:
		return d.resolveNiuguoMedia(ctx, task)
	}
	base := d.duanjuBaseURL(source)
	if base == "" {
		return providerMedia{}, errors.New("站源地址不可用")
	}
	// XBPQ 规则源：播放页按规则「跳转播放链接」→ player_data → 嗅探兜底。
	if rule, ok := customXBPQRule(source); ok {
		return d.xbpqResolveMedia(ctx, task, rule, base, name)
	}
	address := strings.TrimSpace(task.Chapter.VideoURL)
	pageURL := strings.TrimSpace(task.Chapter.PageURL)
	referer := firstNonEmpty(task.Chapter.Referer, base+"/")
	if !isProviderHTTPMediaURL(address) || !duanjuLooksLikeMedia(address) {
		if pageURL == "" {
			if source, sourceID, valid := splitProviderDramaID(task.DramaID); valid {
				if _, chapters, err := d.fetchDuanjuDetail(ctx, source, sourceID); err == nil {
					for _, fresh := range chapters {
						if fresh.ID == task.Chapter.ID {
							address = fresh.VideoURL
							pageURL = fresh.PageURL
							break
						}
					}
				}
			}
		}
		if pageURL != "" {
			resolved, err := d.resolveDuanjuWebPage(ctx, source, pageURL, referer)
			if err != nil {
				return providerMedia{}, err
			}
			prepared, err := d.prepareWebProviderMedia(ctx, resolved, name)
			if err == nil {
				return prepared, nil
			}
			// 默认线路可能被源站下架或限速（返回 404 / 403），自动改用同一集的其他线路。
			if fallback, ok := d.prepareWebProviderRouteFallback(ctx, resolved, name); ok {
				return fallback, nil
			}
			return providerMedia{}, err
		}
	}
	if !isProviderHTTPMediaURL(address) {
		return providerMedia{}, fmt.Errorf("%s未返回有效播放地址，请刷新章节后重试", name)
	}
	return d.prepareWebProviderMedia(ctx, providerMedia{URL: address, Referer: referer}, name)
}

// prepareWebProviderRouteFallback 在主线路拿不到可播内容时，依次试用备选线路，
// 首个可播线路提为主地址，其余线路（含原主线路）继续留在备选列表里供手动切换。
func (d *Downloader) prepareWebProviderRouteFallback(ctx context.Context, media providerMedia, name string) (providerMedia, bool) {
	if len(media.Variants) == 0 {
		return providerMedia{}, false
	}
	for index, variant := range media.Variants {
		if !isProviderHTTPMediaURL(variant.URL) {
			continue
		}
		prepared, err := d.prepareWebProviderMedia(ctx, providerMedia{URL: variant.URL, Referer: variant.Referer}, name)
		if err != nil {
			continue
		}
		remaining := make([]providerMedia, 0, len(media.Variants))
		remaining = append(remaining, media.Variants[:index]...)
		remaining = append(remaining, media.Variants[index+1:]...)
		if isProviderHTTPMediaURL(media.URL) {
			remaining = append([]providerMedia{{URL: media.URL, Referer: media.Referer}}, remaining...)
		}
		prepared.Variants = remaining
		return prepared, true
	}
	return providerMedia{}, false
}

func duanjuLooksLikeMedia(address string) bool {
	lower := strings.ToLower(address)
	return strings.Contains(lower, ".m3u8") || strings.Contains(lower, ".mp4") || strings.Contains(lower, ".flv")
}

func (d *Downloader) resolveDuanjuWebPage(ctx context.Context, source, pageURL, referer string) (providerMedia, error) {
	body, err := d.fetchProviderText(ctx, pageURL, referer)
	if err != nil {
		return providerMedia{}, err
	}
	address := maccmsNormalizePlaybackURL(maccmsPlayerURL(body))
	if isProviderHTTPMediaURL(address) {
		media := providerMedia{URL: address, Referer: pageURL}
		// 同一集通常有多条线路（m3u8/云播/各视频站），详情只展示一条线路的集数，
		// 其余线路在这里补齐，播放页就能用「线路 N」切换。
		media.Variants = d.collectMaccmsRouteVariants(ctx, pageURL, address)
		return media, nil
	}
	// macplus 模板（如 MGMGTV）给出的是加密串：走 /static/player/<from>.js 的 iframe 云解析拿真实 m3u8。
	fields := maccmsPlayerFields(body)
	encrypted := fields["url"]
	if encrypted != "" && !strings.HasPrefix(strings.ToLower(encrypted), "http") {
		if parsed := d.resolveMaccmsCloudParse(ctx, body, encrypted, pageURL); parsed != "" {
			return providerMedia{URL: maccmsNormalizePlaybackURL(parsed), Referer: pageURL}, nil
		}
		// 当前线路云解析失败（如默认 sid 为 iqiyi 等重型加密线）：换该站其他线路逐条重试。
		if alt := d.resolveMaccmsAlternateRoutes(ctx, pageURL); alt != "" {
			return providerMedia{URL: maccmsNormalizePlaybackURL(alt), Referer: pageURL}, nil
		}
	}
	if address == "" || !isProviderHTTPMediaURL(address) {
		return providerMedia{}, fmt.Errorf("%s未返回有效播放地址，请刷新章节后重试", duanjuSourceName(source))
	}
	return providerMedia{URL: address, Referer: pageURL}, nil
}

var (
	maccmsPlaySIDSegment = regexp.MustCompile(`(?i)sid/(\d+)`)
	maccmsPlayMyuiTail   = regexp.MustCompile(`(?i)^(.*/\d+)-(\d+)-(\d+\.html)$`)
)

const (
	// maccmsMaxRouteSID 是探测线路号的上限（站点线路通常不超过 10 条）。
	maccmsMaxRouteSID = 12
	// maccmsMaxRouteVariants 是最多采集的备选线路数量（不含当前线路）。
	maccmsMaxRouteVariants = 3
)

// maccmsAlternateRoutePaths 由当前播放页 URL 生成同集其他线路（sid）的页面路径。
// macplus/myui 播放页 URL 中 sid 即线路号，结构固定，直接替换 sid 构造其他线路播放页即可
// （播放页里的 detail 链接多为推荐位，不可靠）。
func maccmsAlternateRoutePaths(pageURL string) (siteBase string, paths []string) {
	parsed, err := url.Parse(pageURL)
	if err != nil || parsed.Host == "" {
		return "", nil
	}
	currentSID := -1
	if matches := maccmsPlaySIDSegment.FindStringSubmatch(parsed.Path); len(matches) > 1 {
		currentSID, _ = strconv.Atoi(matches[1])
		for sid := 1; sid <= maccmsMaxRouteSID; sid++ {
			if sid == currentSID {
				continue
			}
			paths = append(paths, maccmsPlaySIDSegment.ReplaceAllString(parsed.Path, "sid/"+strconv.Itoa(sid)))
		}
	} else if matches := maccmsPlayMyuiTail.FindStringSubmatch(parsed.Path); len(matches) > 1 {
		currentSID, _ = strconv.Atoi(matches[2])
		for sid := 1; sid <= maccmsMaxRouteSID; sid++ {
			if sid == currentSID {
				continue
			}
			paths = append(paths, matches[1]+"-"+strconv.Itoa(sid)+"-"+matches[3])
		}
	}
	if len(paths) == 0 {
		return "", nil
	}
	return parsed.Scheme + "://" + parsed.Host, paths
}

// resolveMaccmsAlternateRoutes 在当前播放页云解析失败时，尝试同一剧集的其他线路（sid）。
// 逐条抓页走云解析，返回首个成功的 m3u8。
func (d *Downloader) resolveMaccmsAlternateRoutes(ctx context.Context, pageURL string) string {
	siteBase, paths := maccmsAlternateRoutePaths(pageURL)
	if siteBase == "" {
		return ""
	}
	for _, path := range paths {
		altURL := siteBase + path
		altBody, err := d.fetchProviderText(ctx, altURL, pageURL)
		if err != nil || altBody == "" {
			continue
		}
		altFields := maccmsPlayerFields(altBody)
		encrypted := altFields["url"]
		if encrypted == "" || strings.HasPrefix(strings.ToLower(encrypted), "http") {
			continue
		}
		if parsedResult := d.resolveMaccmsCloudParse(ctx, altBody, encrypted, altURL); parsedResult != "" {
			return parsedResult
		}
	}
	return ""
}

// maccmsResolveRoutePage 抓取一条线路的播放页并返回可播放地址（明文直取，加密串走云解析）。
func (d *Downloader) maccmsResolveRoutePage(ctx context.Context, altURL, referer string) string {
	body, err := d.fetchProviderText(ctx, altURL, referer)
	if err != nil || body == "" {
		return ""
	}
	if address := maccmsNormalizePlaybackURL(maccmsPlayerURL(body)); isProviderHTTPMediaURL(address) {
		return address
	}
	fields := maccmsPlayerFields(body)
	encrypted := fields["url"]
	if encrypted == "" || strings.HasPrefix(strings.ToLower(encrypted), "http") {
		return ""
	}
	if parsed := d.resolveMaccmsCloudParse(ctx, body, encrypted, altURL); parsed != "" {
		return maccmsNormalizePlaybackURL(parsed)
	}
	return ""
}

// collectMaccmsRouteVariants 采集同一集其他线路的播放地址，作为播放页的备选线路。
// 详情页只保留一条线路的集数，线路切换靠这里补齐；采集失败不影响主线路，只做尽力而为。
func (d *Downloader) collectMaccmsRouteVariants(ctx context.Context, pageURL, primary string) []providerMedia {
	siteBase, paths := maccmsAlternateRoutePaths(pageURL)
	if siteBase == "" {
		return nil
	}
	if len(paths) > maccmsMaxRouteVariants {
		paths = paths[:maccmsMaxRouteVariants]
	}
	// 备选线路只是锦上添花，给一个总时限，避免拖慢主线路的解析返回。
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	found := make([]string, len(paths))
	var waiter sync.WaitGroup
	for index, path := range paths {
		waiter.Add(1)
		go func(slot int, address string) {
			defer waiter.Done()
			found[slot] = d.maccmsResolveRoutePage(ctx, address, pageURL)
		}(index, siteBase+path)
	}
	waiter.Wait()
	seen := map[string]bool{}
	if primary != "" {
		seen[primary] = true
	}
	var variants []providerMedia
	for index, address := range found {
		if !isProviderHTTPMediaURL(address) || seen[address] {
			continue
		}
		seen[address] = true
		variants = append(variants, providerMedia{URL: address, Referer: siteBase + paths[index]})
	}
	return variants
}

func (d *Downloader) duanjuSourceProbe(ctx context.Context, source string) (string, error) {
	source = canonicalProviderSource(source)
	items, _, err := d.fetchDuanjuCatalogPage(ctx, source, 1, "")
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "", errors.New("站源入口可达，但没有解析到有效剧集")
	}
	return fmt.Sprintf("已解析 %d 部剧", len(items)), nil
}

func duanjuFirstCategory(source string) string {
	if static, found := duanjuStaticCategories[canonicalProviderSource(source)]; found && len(static) > 0 {
		return static[0].ID
	}
	return ""
}
