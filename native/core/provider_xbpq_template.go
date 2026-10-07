package core

// ---- XBPQ 内置模板（对齐官方 XBPQ.jar 的「简写」能力）----
//
// 实测依据（XBPQ.json 334 份 csp_XBPQ 源全量统计）：
//   - 203/334（61%）不写「主页url」，主页由 分类url 的站点根推导；
//   - 185/334（55%）只写「分类url+分类」即正常工作——jar 按 分类url 形态
//     自动套用内置模板（数组/标题/链接/图片/播放/搜索全套默认）。
// 本文件为 provider_xbpq.go 补上同一能力：识别 MacCMS/stui 两大模板家族，
// 为缺失字段补 || 多组备选的默认截取 pattern，已写字段一律不覆盖。

import (
	"regexp"
	"strings"
)

// xbpqTemplateFamily 一种 分类url 形态对应的默认字段集。
type xbpqTemplateFamily struct {
	match  *regexp.Regexp
	fields map[string]string
}

var xbpqTemplateFamilies = []xbpqTemplateFamily{
	{
		// 苹果CMS/MacCMS 家族：/index.php/vod/show|type/…、/vodshow/…、/vodtype/…
		// 页面多为 stui 模板；详情/播放/搜索均有高置信默认。
		// || 备选链按 XBPQ.json 379 份真实规则聚类高频形态排序（stui→hl→module→通用）。
		match: regexp.MustCompile(`(?i)/index\.php/vod/(show|type)/|/vod(show|type)[/_]|vod-list-id-`),
		fields: map[string]string{
			"数组":     `class="stui-vodlist__thumb&&</a>||stui-vodlist__box">&&</div></div>||hl-item-thumb hl-lazy"&&</a>||module-poster-item&&</a>||<li&&</li>`,
			"标题":     `title="&&"||alt="&&"`,
			"图片":     `data-original="&&"||data-src="&&"||src="&&"`,
			"副标题":    `pic-text text-right">&&</span>||class="remark">&&<||module-item-text">&&</div>`,
			"链接":     `href="&&"`,
			"简介":     `detail-content" style=*>&&</span>||class="stui-content__detail&&</div>||简介：</em>&&`,
			"播放数组":   `<ul class="stui-content__playlist&&</ul>||id="hl-plays-list"&&</ul>||mod play-list&&</ul||class="play_box&&</div>||<ul class="item clearfix&&</ul>`,
			"播放列表":   `<a&&</a>`,
			"播放标题":   `>&&</a>||>&&<`,
			"播放链接":   `href="&&"`,
			"线路标题":   `<h3 class="title">&&</h3>||play_tit&&</`,
			// 聚类最高频跳转形态（15 处，含尾引号——单侧无尾锚会吃到页尾）：
			// var player_ 前缀锚点不会误命中同页 var maccms 的 "url":"。
			"跳转播放链接": `var player_*"url":"&&"`,
			"搜索url":  `{host}/index.php/vod/search.html?wd={wd}`,
			"搜索数组":   `class="stui-vodlist__thumb&&</a>||<li&&</li>`,
		},
	},
	{
		// 短剧/路径式 CMS：/show/{id}-{area}-…-{pg}---{year}.html、/vs/、/vshow/ 等。
		match: regexp.MustCompile(`(?i)/(show|vs|vshow|screen|list)/?\{?cateId\}?`),
		fields: map[string]string{
			"数组":     `class="stui-vodlist__thumb&&</a>||module-item&&</a>||<li&&</li>`,
			"标题":     `title="&&"||alt="&&"`,
			"图片":     `data-original="&&"||data-src="&&"||src="&&"`,
			"副标题":    `pic-text text-right">&&</span>||module-item-text">&&</div>`,
			"链接":     `href="&&"`,
			"简介":     `detail-content" style=*>&&</span>||class="video-info&&</div>`,
			"播放数组":   `<ul class="stui-content__playlist&&</ul>||class="module-play-list&&</div>`,
			"播放列表":   `<a&&</a>||<li&&</li>`,
			"播放标题":   `>&&</a>||>&&<`,
			"播放链接":   `href="&&"`,
			"线路标题":   `<h3 class="title">&&</h3>`,
			"跳转播放链接": `var player_*"url":"&&"`,
		},
	},
}

// xbpqApplyTemplate 按 分类url 形态补齐缺失字段（不覆盖已写字段）。幂等。
func (r *xbpqRule) xbpqApplyTemplate() {
	if r.fields == nil {
		r.fields = map[string]string{}
	}
	if r.fields["模板已套用"] != "" {
		return
	}
	category := r.field("分类url", "分类Url")
	if category == "" {
		return
	}
	for _, fam := range xbpqTemplateFamilies {
		if !fam.match.MatchString(category) {
			continue
		}
		origin := xbpqHostOrigin(category)
		for key, value := range fam.fields {
			key = xbpqNormalizeKey(key)
			if strings.Contains(value, "{host}") {
				if origin == "" {
					continue
				}
				value = strings.ReplaceAll(value, "{host}", origin)
			}
			if strings.TrimSpace(r.fields[key]) == "" {
				r.fields[key] = value
				r.order = append(r.order, key)
			}
		}
		break
	}
	r.fields["模板已套用"] = "1"
}

// xbpqHostOrigin 从绝对地址提取 scheme://host。
func xbpqHostOrigin(address string) string {
	index := strings.Index(address, "://")
	if index < 0 {
		return ""
	}
	rest := address[index+3:]
	if slash := strings.IndexAny(rest, "/?#"); slash >= 0 {
		rest = rest[:slash]
	}
	return address[:index+3] + rest
}

// xbpqEmptyPathSegment 匹配模板空值留下的路径段：/area//id → 整段删除。
// MacCMS 路径式 URL 对空筛选段返回 404；jar 的行为是自动省略空段。
var xbpqEmptyPathSegment = regexp.MustCompile(`/[a-zA-Z][a-zA-Z0-9_]*//`)

// xbpqEmptyTailSegment 匹配末段空值：/year/.html → .html。
var xbpqEmptyTailSegment = regexp.MustCompile(`/[a-zA-Z][a-zA-Z0-9_]*/(\.[a-zA-Z]{2,5}(?:\?|$))`)

// xbpqCleanEmptySegments 清洗 分类url 渲染后的空筛选段（含 query 空参数）。
func xbpqCleanEmptySegments(address string) string {
	for xbpqEmptyPathSegment.MatchString(address) {
		address = xbpqEmptyPathSegment.ReplaceAllString(address, "/")
	}
	for xbpqEmptyTailSegment.MatchString(address) {
		address = xbpqEmptyTailSegment.ReplaceAllString(address, "$1")
	}
	if queryIndex := strings.Index(address, "?"); queryIndex >= 0 {
		head, query := address[:queryIndex], address[queryIndex+1:]
		var kept []string
		dropped := false
		for _, pair := range strings.Split(query, "&") {
			if key, value, found := strings.Cut(pair, "="); found && value == "" && !strings.Contains(key, "{") {
				dropped = true
				continue
			}
			if pair != "" {
				kept = append(kept, pair)
			}
		}
		if dropped {
			if len(kept) == 0 {
				return head
			}
			return head + "?" + strings.Join(kept, "&")
		}
	}
	return address
}
