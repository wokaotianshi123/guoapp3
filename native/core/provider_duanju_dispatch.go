package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
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
	case sourceHuaguo, sourceFaguo, sourceWuguo, sourceWangguo, sourcePiguo:
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
	case sourceHuaguo, sourceFaguo, sourceWuguo, sourceWangguo, sourcePiguo:
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
	case sourceHuaguo, sourceFaguo, sourceWuguo, sourceWangguo, sourcePiguo:
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
			return d.prepareWebProviderMedia(ctx, resolved, name)
		}
	}
	if !isProviderHTTPMediaURL(address) {
		return providerMedia{}, fmt.Errorf("%s未返回有效播放地址，请刷新章节后重试", name)
	}
	return d.prepareWebProviderMedia(ctx, providerMedia{URL: address, Referer: referer}, name)
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
		return providerMedia{URL: address, Referer: pageURL}, nil
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

// resolveMaccmsAlternateRoutes 在当前播放页云解析失败时，尝试同一剧集的其他线路（sid）。
// macplus/myui 播放页 URL 中 sid 即线路号，结构固定，直接替换 sid 构造其他线路播放页即可
// （播放页里的 detail 链接多为推荐位，不可靠）。逐条抓页走云解析，返回首个成功的 m3u8。
func (d *Downloader) resolveMaccmsAlternateRoutes(ctx context.Context, pageURL string) string {
	parsed, err := url.Parse(pageURL)
	if err != nil || parsed.Host == "" {
		return ""
	}
	currentSID := -1
	var paths []string
	if matches := maccmsPlaySIDSegment.FindStringSubmatch(parsed.Path); len(matches) > 1 {
		currentSID, _ = strconv.Atoi(matches[1])
		for sid := 1; sid <= 9; sid++ {
			if sid == currentSID {
				continue
			}
			paths = append(paths, maccmsPlaySIDSegment.ReplaceAllString(parsed.Path, "sid/"+strconv.Itoa(sid)))
		}
	} else if matches := maccmsPlayMyuiTail.FindStringSubmatch(parsed.Path); len(matches) > 1 {
		currentSID, _ = strconv.Atoi(matches[2])
		for sid := 1; sid <= 9; sid++ {
			if sid == currentSID {
				continue
			}
			paths = append(paths, matches[1]+"-"+strconv.Itoa(sid)+"-"+matches[3])
		}
	}
	if len(paths) == 0 {
		return ""
	}
	siteBase := parsed.Scheme + "://" + parsed.Host
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
