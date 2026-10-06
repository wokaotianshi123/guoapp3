package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// ---- XBPQ 规则驱动自定义源 ----
//
// 兼容 TVBox XBPQ 爬虫的「html 截取」规则：用户把爬虫 JSON（形如
// {"请求头":"...","主页url":"...","分类url":"...","分类":"电影$1#...","播放数组":"mac_url='&&'",...}）
// 直接作为自定义源录入，核心按规则逐字段截取页面内容，不再依赖模板家族猜测。
// 实现范围（XBPQ 语义的常用子集）：
//   - 截取语法：start&&end（|| 多组备选、&& 前后缀单侧截取）；
//   - 选择器语法：p:标签.类#id[attr=val] 空格分隔后代，提取 text/html/attr；
//   - 修饰符：[包含:x,y] [不包含:x,y] [替换:a>>b#c>>d] [含序号:n]；
//   - 占位符：{cateId} {catePg} {wd} {id} {name}（详情页播放链接用）；
//   - 播放串：$$$ 分线路、# 分集、标题$链接；跳转播放链接二次嗅探；
//   - 搜索 POST（分号形态 url;post;body）、请求头 UA、GBK/GB2312 页面转码。

type xbpqRule struct {
	fields map[string]string // 归一化键（去空白、小写保留原样）→ 值
	order  []string          // 原始键顺序（导入导出保真用）
}

// xbpqNormalizeKey 归一化字段名：全角方括号转半角、去空白。
func xbpqNormalizeKey(key string) string {
	key = strings.TrimSpace(key)
	key = strings.ReplaceAll(key, "〔", "[")
	key = strings.ReplaceAll(key, "〕", "]")
	key = strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "").Replace(key)
	return key
}

func xbpqNormalizeValue(value string) string {
	value = strings.ReplaceAll(value, "〔", "[")
	value = strings.ReplaceAll(value, "〕", "]")
	return strings.TrimSpace(value)
}

// parseXBPQRule 解析爬虫 JSON 文本；识别成功返回规则与 true。
// 兼容形态：整体对象、[{对象}] 数组、@long-text 拼接片段。
func parseXBPQRule(text string) (xbpqRule, bool) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > customSourceMaxBytes {
		return xbpqRule{}, false
	}
	// @long-text:"{...}" 形态：把引用段拼回主 JSON（本项目不执行其内容，只做结构合并）。
	if index := strings.Index(text, `@long-text:`); index >= 0 {
		head := xbpqTrimTrailingComma(text[:index])
		tail := text[index:]
		if json.Valid([]byte(head)) {
			if embedded := xbpqExtractLongText(tail); embedded != "" {
				if merged := xbpqMergeLongText(head, embedded); merged != "" {
					text = merged
				}
			}
		}
	}
	var decoded any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		repaired := xbpqTrimTrailingComma(text)
		if json.Unmarshal([]byte(repaired), &decoded) != nil {
			return xbpqRule{}, false
		}
	}
	fields := map[string]string{}
	var order []string
	collect := func(node map[string]any) {
		for key, value := range node {
			nk := xbpqNormalizeKey(key)
			if nk == "" {
				continue
			}
			nv := xbpqNormalizeValue(xbpqStringValue(value))
			if _, exists := fields[nk]; !exists {
				order = append(order, nk)
			}
			fields[nk] = nv
		}
	}
	switch typed := decoded.(type) {
	case map[string]any:
		collect(typed)
	case []any:
		for _, entry := range typed {
			if node, ok := entry.(map[string]any); ok {
				collect(node)
			}
		}
	default:
		return xbpqRule{}, false
	}
	if fields["主页url"] == "" && fields["首页url"] == "" && fields["请求"] == "" {
		return xbpqRule{}, false // 不是 XBPQ 爬虫 JSON
	}
	return xbpqRule{fields: fields, order: order}, true
}

func xbpqStringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case nil:
		return ""
	default:
		body, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(body)
	}
}

var xbpqLongTextPattern = regexp.MustCompile(`(?s)@long-text:\s*"((?:[^"\\]|\\.)*)"\s*;?\s*$`)

func xbpqExtractLongText(tail string) string {
	matches := xbpqLongTextPattern.FindStringSubmatch(strings.TrimSpace(tail))
	if len(matches) < 2 {
		return ""
	}
	var decoded string
	if err := json.Unmarshal([]byte(`"`+matches[1]+`"`), &decoded); err != nil {
		return ""
	}
	return decoded
}

var xbpqTrailingComma = regexp.MustCompile(`,\s*([}\]])`)

// xbpqTrimTrailingComma 去掉 JSON 尾部多余逗号（爬虫导出里很常见）。
func xbpqTrimTrailingComma(text string) string {
	for {
		cleaned := xbpqTrailingComma.ReplaceAllString(text, "$1")
		if cleaned == text {
			return text
		}
		text = cleaned
	}
}

// xbpqMergeLongText 把 long-text 中的附加字段合并进主 JSON（同键以主 JSON 优先）。
func xbpqMergeLongText(head, embedded string) string {
	var headNode map[string]any
	var embeddedNode map[string]any
	if json.Unmarshal([]byte(head), &headNode) != nil {
		return ""
	}
	if json.Unmarshal([]byte(embedded), &embeddedNode) != nil {
		return head
	}
	for key, value := range embeddedNode {
		if _, exists := headNode[key]; !exists {
			headNode[key] = value
		}
	}
	body, err := json.Marshal(headNode)
	if err != nil {
		return head
	}
	return string(body)
}

// field 依次尝试多个字段名（含 XBPQ 常见别名）。
func (r xbpqRule) field(names ...string) string {
	for _, name := range names {
		if value, found := r.fields[xbpqNormalizeKey(name)]; found && value != "" {
			return value
		}
	}
	return ""
}

func (r xbpqRule) homeURL() string {
	value := r.field("主页url", "首页url", "请求")
	if value == "" {
		return ""
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return ""
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func (r xbpqRule) userAgent() string {
	value := r.field("请求头", "User-Agent", "UA")
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if index := strings.Index(lower, "user-agent"); index >= 0 {
		value = strings.TrimSpace(value[index+len("user-agent"):])
		value = strings.TrimLeft(value, "$:：= \t")
		if first := strings.Fields(value); len(first) == 1 {
			value = first[0]
		}
	}
	if value == "" || len(value) > 240 || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}

// ---- 截取引擎 ----

type xbpqStep struct {
	start       string
	end         string
	contains    []string
	notContains []string
	replaces    [][2]string
	index       int // [含序号:n] / [序号:n]，1 起；0 表示不启用
}

var xbpqModifierKeyword = regexp.MustCompile(`^(包含|不包含|替换|序号|含序号|截右|右截)$`)

// xbpqTrimModifier 从 token 尾部剥出 [修饰符] 列表，返回剩余字面与修饰符。
func xbpqTrimModifier(token string) (string, []xbpqStep) {
	var mods []xbpqStep
	work := token
	for {
		if !strings.HasSuffix(work, "]") {
			break
		}
		open := xbpqMatchBracket(work)
		if open < 0 {
			break
		}
		body := work[open+1 : len(work)-1]
		colon := strings.IndexAny(body, ":：")
		word := body
		payload := ""
		if colon >= 0 {
			word, payload = body[:colon], body[colon+1:]
		}
		word = strings.TrimSpace(word)
		if !xbpqModifierKeyword.MatchString(word) {
			break
		}
		mod := xbpqStep{}
		if len(mods) > 0 {
			mod = mods[0]
		}
		switch word {
		case "包含":
			mod.contains = append(mod.contains, xbpqSplitList(payload)...)
		case "不包含":
			mod.notContains = append(mod.notContains, xbpqSplitList(payload)...)
		case "替换":
			for _, pair := range strings.Split(payload, "#") {
				before, after, found := strings.Cut(pair, ">>")
				if found {
					mod.replaces = append(mod.replaces, [2]string{before, after})
				}
			}
		case "序号", "含序号":
			if number, err := strconv.Atoi(strings.TrimSpace(payload)); err == nil && number >= 1 {
				mod.index = number
			}
		default:
			mod.index = -1 // [右截] 等未支持项：保留字面，不再剥
			open = -1
		}
		if open < 0 {
			break
		}
		work = strings.TrimSpace(work[:open])
		mods = append([]xbpqStep{mod}, mods...)
	}
	return work, mods
}

// xbpqMatchBracket 从右往左找与末尾 ] 配对的 [（不嵌套，简单取最右）。
func xbpqMatchBracket(token string) int {
	for index := len(token) - 2; index >= 0; index-- {
		switch token[index] {
		case ']':
			return -1
		case '[':
			return index
		}
	}
	return -1
}

func xbpqSplitList(payload string) []string {
	var out []string
	for _, item := range strings.Split(payload, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// xbpqParseSteps 把「数组/列表」形态的 pattern 拆成截取步骤链。
// pattern 形如 A&&B&&C&&D：(A,B)、(C,D) 两步链；单侧截取（A&& 或 &&B）亦兼容。
func xbpqParseSteps(pattern string) []xbpqStep {
	var steps []xbpqStep
	parts := strings.Split(pattern, "&&")
	if len(parts) == 1 {
		return nil // 无 && 的单 token 不构成截取指令
	}
	for i := 0; i < len(parts); i++ {
		startToken, startMods := xbpqTrimModifier(strings.TrimSpace(parts[i]))
		end := ""
		endMods := []xbpqStep{}
		if i+1 < len(parts) {
			endRaw := strings.TrimSpace(parts[i+1])
			end, endMods = xbpqTrimModifier(endRaw)
		}
		step := xbpqStep{start: startToken, end: end}
		if len(startMods) > 0 {
			step = startMods[0]
			step.start, step.end = startToken, end
			if len(endMods) > 0 {
				step.contains = append(step.contains, endMods[0].contains...)
				step.notContains = append(step.notContains, endMods[0].notContains...)
				step.replaces = append(step.replaces, endMods[0].replaces...)
				if endMods[0].index != 0 && step.index == 0 {
					step.index = endMods[0].index
				}
			}
		} else if len(endMods) > 0 {
			step = endMods[0]
			step.start, step.end = startToken, end
		}
		steps = append(steps, step)
		i++
	}
	return steps
}

// xbpqCutOnce 按 pattern 在 source 上截取第一段。pattern 多组用 || 分隔时依次尝试。
func xbpqCutOnce(source, pattern string) string {
	if source == "" || pattern == "" {
		return ""
	}
	if strings.HasPrefix(pattern, "p:") || strings.HasPrefix(pattern, "jsoup:") {
		return xbpqSelectorFirstString(source, pattern)
	}
	for _, part := range strings.Split(pattern, "||") {
		steps := xbpqParseSteps(part)
		if len(steps) == 0 {
			continue
		}
		segment := source
		ok := true
		for _, step := range steps {
			segment, ok = xbpqApplyStepOnce(segment, step)
			if !ok {
				break
			}
		}
		if ok && strings.TrimSpace(segment) != "" {
			return segment
		}
	}
	return ""
}

func xbpqApplyStepOnce(source string, step xbpqStep) (string, bool) {
	if step.start == "" && step.end == "" {
		return source, true
	}
	rest := source
	startOffset := 0
	if step.start != "" {
		index := strings.Index(rest, step.start)
		if index < 0 {
			return "", false
		}
		rest = rest[index+len(step.start):]
		startOffset = index + len(step.start)
	}
	if step.end != "" {
		index := strings.Index(rest, step.end)
		if index < 0 {
			return "", false
		}
		rest = rest[:index]
		_ = startOffset
	}
	return xbpqFinishSegment(rest, step), true
}

func xbpqFinishSegment(segment string, step xbpqStep) string {
	if step.index > 0 {
		matches := xbpqSplitNumbered(segment)
		if step.index <= len(matches) {
			segment = matches[step.index-1]
		}
	}
	for _, word := range step.contains {
		if !strings.Contains(segment, word) {
			return ""
		}
	}
	for _, word := range step.notContains {
		if strings.Contains(segment, word) {
			return ""
		}
	}
	for _, pair := range step.replaces {
		segment = strings.ReplaceAll(segment, pair[0], pair[1])
	}
	return strings.TrimSpace(segment)
}

// xbpqSplitNumbered 按数字序号切分（[含序号:n] 语义：第 n 个 <*> 类块），
// 简化实现：按换行或空白切分取第 n 段。
func xbpqSplitNumbered(segment string) []string {
	return strings.FieldsFunc(segment, func(r rune) bool { return r == '\n' || r == '\t' })
}

// xbpqList 按数组 pattern 迭代抽取全部条目（列表层）。
func xbpqList(source, pattern string) []string {
	if source == "" || pattern == "" {
		return nil
	}
	var items []string
	for _, part := range strings.Split(pattern, "||") {
		steps := xbpqParseSteps(part)
		if len(steps) == 0 {
			continue
		}
		first := steps[0]
		rest := source
		for {
			segment, consumed, ok := xbpqApplyStepScan(rest, first)
			if !ok {
				break
			}
			if len(steps) > 1 {
				for _, step := range steps[1:] {
					segment, ok = xbpqApplyStepOnce(segment, step)
					if !ok {
						segment = ""
						break
					}
				}
			}
			if segment != "" {
				items = append(items, segment)
			}
			if consumed >= len(rest) {
				break
			}
			rest = rest[consumed:]
		}
		if len(items) > 0 {
			break
		}
	}
	return items
}

// xbpqApplyStepScan 找第一个 start&&end 段；返回段内容与「消费到的绝对偏移」。
func xbpqApplyStepScan(source string, step xbpqStep) (string, int, bool) {
	if step.start == "" {
		return "", 0, false
	}
	index := strings.Index(source, step.start)
	if index < 0 {
		return "", 0, false
	}
	body := source[index+len(step.start):]
	stop := len(source)
	if step.end != "" {
		endIndex := strings.Index(body, step.end)
		if endIndex < 0 {
			return "", 0, false
		}
		body = body[:endIndex]
		stop = index + len(step.start) + endIndex
	}
	segment := xbpqFinishSegment(body, step)
	if segment == "" {
		// 修饰符不满足：跳过本 start，继续找下一个
		if next := strings.Index(source[index+1:], step.start); next >= 0 {
			skip := index + 1 + next
			innerSegment, innerStop, ok := xbpqApplyStepScan(source[skip:], step)
			return innerSegment, skip + innerStop, ok
		}
		return "", stop, false
	}
	return segment, stop, true
}

// ---- 选择器子集（p: 语法）----

type xbpqSelectorPart struct {
	tag         string
	classes     []string
	id          string
	attrs       [][2]string // key=value（value 可为空表示存在即可）
	attr        string      // 末尾 [href] 形式：取值属性
	contains    []string
	notContains []string
}

type xbpqSelector struct {
	ancestors []xbpqSelectorPart
	target    xbpqSelectorPart
	extract   string // text / html；空 = text
}

func xbpqParseSelector(pattern string) (xbpqSelector, bool) {
	body := pattern
	switch {
	case strings.HasPrefix(body, "p:"):
		body = body[2:]
	case strings.HasPrefix(body, "jsoup:"):
		body = body[6:]
	default:
		return xbpqSelector{}, false
	}
	// 修饰符后缀：[包含:xx] 作用于目标
	var contains, notContains []string
	for strings.HasSuffix(body, "]") {
		open := xbpqMatchBracket(body)
		if open < 0 {
			break
		}
		inner := body[open+1 : len(body)-1]
		word, payload, _ := strings.Cut(inner, ":")
		switch strings.TrimSpace(word) {
		case "包含":
			contains = append(contains, xbpqSplitList(payload)...)
			body = strings.TrimSpace(body[:open])
			continue
		case "不包含":
			notContains = append(notContains, xbpqSplitList(payload)...)
			body = strings.TrimSpace(body[:open])
			continue
		}
		break
	}
	parts := strings.FieldsFunc(body, func(r rune) bool { return r == ' ' || r == '>' })
	var parsed []xbpqSelectorPart
	for _, token := range parts {
		part, ok := xbpqParseSelectorPart(token)
		if !ok {
			return xbpqSelector{}, false
		}
		parsed = append(parsed, part)
	}
	if len(parsed) == 0 {
		return xbpqSelector{}, false
	}
	selector := xbpqSelector{target: parsed[len(parsed)-1], extract: "text"}
	if len(parsed) > 1 {
		selector.ancestors = parsed[:len(parsed)-1]
	}
	selector.target.contains = append(selector.target.contains, contains...)
	selector.target.notContains = append(selector.target.notContains, notContains...)
	return selector, true
}

func xbpqParseSelectorPart(token string) (xbpqSelectorPart, bool) {
	part := xbpqSelectorPart{}
	// 拆出方括号段
	var brackets []string
	var base strings.Builder
	for index := 0; index < len(token); {
		if token[index] == '[' {
			closing := strings.IndexByte(token[index:], ']')
			if closing < 0 {
				return part, false
			}
			brackets = append(brackets, token[index+1:index+closing])
			index += closing + 2
			continue
		}
		base.WriteByte(token[index])
		index++
	}
	name := base.String()
	if dot := strings.IndexByte(name, '.'); dot >= 0 {
		for _, class := range strings.Split(name[dot+1:], ".") {
			if class != "" {
				part.classes = append(part.classes, class)
			}
		}
		name = name[:dot]
	}
	if hash := strings.IndexByte(name, '#'); hash >= 0 {
		if len(name) > hash+1 {
			part.id = name[hash+1:]
		}
		name = name[:hash]
	}
	if name != "" {
		part.tag = strings.ToLower(name)
	}
	for _, bracket := range brackets {
		if bracket == "" {
			continue
		}
		if strings.ContainsAny(bracket, "=`") {
			key, value, _ := strings.Cut(strings.Trim(bracket, "`"), "=")
			value = strings.Trim(value, "'\"")
			part.attrs = append(part.attrs, [2]string{strings.ToLower(strings.TrimSpace(key)), value})
			continue
		}
		// [href] / [data-value] / [class="x"] 简写：取属性
		part.attr = bracket
	}
	return part, true
}

func xbpqPartMatches(node *html.Node, part xbpqSelectorPart, requireAttrValue bool) bool {
	if node == nil || node.Type != html.ElementNode {
		return false
	}
	if part.tag != "" && strings.ToLower(node.Data) != part.tag {
		return false
	}
	if part.id != "" && providerHTMLAttr(node, "id") != part.id {
		return false
	}
	for _, class := range part.classes {
		if !providerHTMLClass(node, class) {
			return false
		}
	}
	for _, pair := range part.attrs {
		value, found := "", false
		for _, attribute := range node.Attr {
			if strings.EqualFold(attribute.Key, pair[0]) {
				value, found = attribute.Val, true
				break
			}
		}
		if !found || (pair[1] != "" && value != pair[1]) {
			return false
		}
	}
	for _, word := range part.contains {
		if !strings.Contains(providerHTMLText(node), word) {
			return false
		}
	}
	for _, word := range part.notContains {
		if strings.Contains(providerHTMLText(node), word) {
			return false
		}
	}
	_ = requireAttrValue
	return true
}

func xbpqNodeHasAncestorChain(node *html.Node, ancestors []xbpqSelectorPart) bool {
	if len(ancestors) == 0 {
		return true
	}
	need := len(ancestors) - 1
	current := node.Parent
	for current != nil && need >= 0 {
		if current.Type == html.ElementNode && xbpqPartMatches(current, ancestors[need], false) {
			need--
		}
		current = current.Parent
	}
	return need < 0
}

// xbpqSelectorNodes 在 document 上执行选择器，返回目标节点列表。
func xbpqSelectorNodes(document *html.Node, selector xbpqSelector) []*html.Node {
	var found []*html.Node
	for _, node := range providerHTMLNodes(document, func(n *html.Node) bool { return n.Type == html.ElementNode }) {
		if !xbpqPartMatches(node, selector.target, false) {
			continue
		}
		if !xbpqNodeHasAncestorChain(node, selector.ancestors) {
			continue
		}
		found = append(found, node)
	}
	return found
}

func xbpqRenderHTML(node *html.Node) string {
	var builder strings.Builder
	if node != nil {
		_ = html.Render(&builder, node)
	}
	return builder.String()
}

// xbpqSelectorExtractString 从 html 字符串重新解析后执行选择器取第一项。
func xbpqSelectorFirstString(sourceHTML, pattern string) string {
	values := xbpqSelectorStrings(sourceHTML, pattern)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func xbpqSelectorStrings(sourceHTML, pattern string) []string {
	selector, ok := xbpqParseSelector(pattern)
	if !ok {
		return nil
	}
	document, err := html.Parse(strings.NewReader(sourceHTML))
	if err != nil {
		return nil
	}
	var values []string
	for _, node := range xbpqSelectorNodes(document, selector) {
		if selector.target.attr != "" {
			values = append(values, strings.TrimSpace(providerHTMLAttr(node, selector.target.attr)))
			continue
		}
		if selector.extract == "html" {
			values = append(values, xbpqRenderHTML(node))
			continue
		}
		values = append(values, providerHTMLText(node))
	}
	return values
}

// ---- URL 组装 ----

// xbpqRenderURL 填充占位符并把相对路径转绝对。规则里的绝对域名与站源当前
// 地址不一致时（镜像、换域名），以站源地址为准重写 host——XBPQ 规则惯例
// 「分类url 域名 = 主页url 域名」，重写不影响内容链接（只重写请求模板）。
func xbpqRenderURL(template, base string, values map[string]string) string {
	template = strings.TrimSpace(template)
	if index := strings.Index(template, ";;"); index >= 0 {
		template = template[:index]
	}
	for key, value := range values {
		template = strings.ReplaceAll(template, "{"+key+"}", value)
	}
	template = strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(strings.TrimSpace(template))
	if template == "" {
		return ""
	}
	if !strings.HasPrefix(template, "http") && !strings.HasPrefix(template, "//") {
		if !strings.HasPrefix(template, "/") {
			template = "/" + template
		}
		template = base + template
	}
	if strings.HasPrefix(template, "//") {
		template = "https:" + template
	}
	parsed, err := url.Parse(template)
	if err != nil || parsed.Host == "" {
		return ""
	}
	if baseParsed, baseErr := url.Parse(base); baseErr == nil && baseParsed.Host != "" && parsed.Host != baseParsed.Host {
		parsed.Scheme = baseParsed.Scheme
		parsed.Host = baseParsed.Host
		template = parsed.String()
	}
	return template
}

// xbpqJoinLink 处理「多线链接」风格的 + 拼接（字面量用引号包裹，其余段丢弃）。
func xbpqJoinLink(pageURL, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "$") {
		return ""
	}
	// href="xxx 形态：开头片段
	if lowered := strings.ToLower(raw); strings.HasPrefix(lowered, "href=") {
		raw = raw[len("href="):]
	}
	if strings.Contains(raw, "+") {
		var builder strings.Builder
		for _, segment := range strings.Split(raw, "+") {
			if len(segment) >= 2 && strings.HasPrefix(segment, `"`) && strings.HasSuffix(segment, `"`) {
				builder.WriteString(segment[1 : len(segment)-1])
			}
		}
		raw = builder.String()
	}
	if raw == "" {
		return ""
	}
	return duanjuAbsolute(pageURL, raw)
}

// xbpqIDFromLink 从详情链接提取稳定 ID。规则给了 详情url 模板时提数字 ID，
// 否则整个链接编码成 ID（详情阶段可无损还原页面地址）。
func xbpqIDFromLink(link string, numeric bool) string {
	if numeric {
		if id := maccmsSourceIDFromURL(link); id != "" {
			return id
		}
	}
	return "u" + url.QueryEscape(link)
}

// xbpqLinkFromID 还原 SourceID 对应的详情页 URL。
func xbpqLinkFromID(base, id string) string {
	if strings.HasPrefix(id, "u") {
		if decoded, err := url.QueryUnescape(strings.TrimPrefix(id, "u")); err == nil && strings.HasPrefix(decoded, "http") {
			return decoded
		}
	}
	return ""
}

// ---- 抓取 ----

var xbpqCharsetPattern = regexp.MustCompile(`(?i)charset\s*=\s*["' ]?(gbk|gb2312|gb18030)`)

// xbpqFetch 按规则抓取页面文本：自定义 UA、GBK 转码、POST 支持。
// spec 形如 url;post;body 或 url;get 时取第一段为 URL，第二段为 post 则发 POST。
func (d *Downloader) xbpqFetch(ctx context.Context, raw, referer, agent string) (string, error) {
	address, method, body := xbpqSplitSpec(raw)
	if address == "" {
		return "", fmt.Errorf("规则地址为空")
	}
	if method == "" {
		if agent = strings.TrimSpace(agent); agent != "" {
			ctx = context.WithValue(ctx, providerTextUserAgentKey{}, agent)
		}
		payload, err := d.fetchProviderText(ctx, address, referer)
		if err != nil {
			return "", err
		}
		return xbpqDecodeBody(payload), nil
	}
	request := duanjuRequest{
		Method:  method,
		Address: address,
		Referer: referer,
		Agent:   xbpqAgent(agent),
		Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
		Body:    []byte(body),
	}
	payload, err := d.duanjuDo(ctx, request)
	if err != nil {
		return "", err
	}
	return xbpqDecodeBody(string(payload)), nil
}

func xbpqAgent(agent string) string {
	if strings.TrimSpace(agent) != "" {
		return agent
	}
	return duanjuUserAgent
}

// xbpqSplitSpec 解析 `url;post;body` / `url;get` 形态。
func xbpqSplitSpec(raw string) (address, method, body string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", ""
	}
	parts := strings.Split(raw, ";")
	address = strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		switch strings.ToLower(strings.TrimSpace(parts[1])) {
		case "post":
			method = http.MethodPost
			if len(parts) > 2 {
				body = strings.Join(parts[2:], ";")
			}
		}
	}
	return address, method, body
}

// xbpqDecodeBody GBK/GB2312 页面转 UTF-8。
func xbpqDecodeBody(body string) string {
	if body == "" {
		return body
	}
	head := body
	if len(head) > 4096 {
		head = head[:4096]
	}
	if !xbpqCharsetPattern.MatchString(head) {
		return body
	}
	decoded, _, err := transform.String(simplifiedchinese.GBK.NewDecoder(), body)
	if err != nil || strings.TrimSpace(decoded) == "" {
		return body
	}
	return decoded
}

// ---- 管道 ----

// xbpqPage 是分类 URL 的一页：渲染 + 抓取 + 抽取条目。
func (d *Downloader) xbpqPage(ctx context.Context, base, pageURL string) (string, error) {
	return d.xbpqFetch(ctx, pageURL, base+"/", base)
}

// xbpqItems 抽取列表条目（selector 模式返回节点，cut 模式返回字符串段）。
type xbpqItem struct {
	text string
	node *html.Node
	doc  *html.Node
}

func (d *Downloader) xbpqExtractItems(ctx context.Context, rule xbpqRule, base, pageURL, prefix string) []xbpqItem {
	body, err := d.xbpqFetch(ctx, pageURL, base+"/", rule.userAgent())
	if err != nil || body == "" {
		return nil
	}
	arrayPattern := rule.field(prefix+"数组", "数组")
	if arrayPattern == "" {
		if jsonItems := xbpqJSONList(body, rule, prefix); len(jsonItems) > 0 {
			return jsonItems
		}
		return nil
	}
	document, _ := html.Parse(strings.NewReader(body))
	if strings.HasPrefix(arrayPattern, "p:") || strings.HasPrefix(arrayPattern, "jsoup:") {
		selector, ok := xbpqParseSelector(arrayPattern)
		if !ok {
			return nil
		}
		var items []xbpqItem
		for _, node := range xbpqSelectorNodes(document, selector) {
			items = append(items, xbpqItem{node: node, doc: document, text: xbpqRenderHTML(node)})
		}
		return items
	}
	// 截掉 [列表] 之外的部分：二次截取先行
	if region := rule.field(prefix+"二次截取", "二次截取"); region != "" {
		if cut := xbpqCutOnce(body, region); cut != "" {
			body = cut
			document, _ = html.Parse(strings.NewReader(body))
		}
	}
	var items []xbpqItem
	for _, segment := range xbpqList(body, arrayPattern) {
		items = append(items, xbpqItem{text: segment, doc: document})
	}
	if len(items) == 0 {
		if jsonItems := xbpqJSONList(body, rule, prefix); len(jsonItems) > 0 {
			return jsonItems
		}
	}
	return items
}

// xbpqJSONList 兜底：页面里嵌着 MacCMS 风格 JSON（vod_name/vod_id…）时直接解析。
func xbpqJSONList(body string, rule xbpqRule, prefix string) []xbpqItem {
	start := strings.Index(body, `[{`)
	if start < 0 {
		return nil
	}
	decoder := json.NewDecoder(strings.NewReader(body[start:]))
	var payload any
	if decoder.Decode(&payload) != nil {
		return nil
	}
	var rows []any
	switch typed := payload.(type) {
	case []any:
		rows = typed
	case map[string]any:
		for _, key := range []string{"list", "data", "result", "rows"} {
			if entries, found := typed[key].([]any); found {
				rows = entries
				break
			}
		}
	}
	var items []xbpqItem
	for _, row := range rows {
		node, ok := row.(map[string]any)
		if !ok {
			continue
		}
		body, err := json.Marshal(node)
		if err != nil {
			continue
		}
		items = append(items, xbpqItem{text: string(body)})
	}
	return items
}

func xbpqItemField(item xbpqItem, pattern string) string {
	if pattern == "" || item.text == "" {
		return ""
	}
	if strings.HasPrefix(pattern, "p:") || strings.HasPrefix(pattern, "jsoup:") {
		if item.node != nil {
			selector, ok := xbpqParseSelector(pattern)
			if !ok {
				return ""
			}
			nodes := xbpqSelectorNodes(item.node, selector)
			if len(nodes) == 0 && item.doc != nil {
				// 条目节点外的兄弟容器：在整页里找同选择器且属于本条目的第一个
				nodes = xbpqSelectorNodes(item.doc, selector)
			}
			for _, node := range nodes {
				if !xbpqPartWithin(node, item.node) {
					continue
				}
				if selector.target.attr != "" {
					return strings.TrimSpace(providerHTMLAttr(node, selector.target.attr))
				}
				return providerHTMLText(node)
			}
			if len(nodes) > 0 {
				node := nodes[0]
				if selector.target.attr != "" {
					return strings.TrimSpace(providerHTMLAttr(node, selector.target.attr))
				}
				return providerHTMLText(node)
			}
			return ""
		}
		return xbpqSelectorFirstString(item.text, pattern)
	}
	// 形如 "vod_name":"&&" 的 JSON 字段截取
	if strings.Contains(pattern, `"`) && strings.Contains(item.text, "{") && json.Valid([]byte(item.text)) {
		value := xbpqJSONField(item.text, pattern)
		if value != "" {
			return value
		}
	}
	return xbpqCutOnce(item.text, pattern)
}

func xbpqPartWithin(node, scope *html.Node) bool {
	if scope == nil {
		return true
	}
	for current := node; current != nil; current = current.Parent {
		if current == scope {
			return true
		}
	}
	return false
}

func xbpqJSONField(itemJSON, pattern string) string {
	var node map[string]any
	if json.Unmarshal([]byte(itemJSON), &node) != nil {
		return ""
	}
	key := xbpqCutOnce(pattern, `"&&"`)
	if key == "" {
		key = strings.NewReplacer(`"`, "", ":", "", "&&", "", "+", "").Replace(pattern)
		key = strings.TrimSpace(key)
	}
	for name, value := range node {
		if strings.EqualFold(strings.TrimSpace(name), key) {
			return xbpqStringValue(value)
		}
	}
	return ""
}

// xbpqCleanText 去除标签实体噪音。
var xbpqTagPattern = regexp.MustCompile(`(?s)<[^>]*>`)

func xbpqCleanText(value string) string {
	value = xbpqTagPattern.ReplaceAllString(value, "")
	value = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&#13;", "", "\t", " ").Replace(value)
	value = strings.Join(strings.Fields(value), " ")
	return strings.TrimSpace(value)
}

// xbpqCategoryIDPattern 规则「分类」字段里的分类标识：既收 MacCMS 模板的纯数字
// ID，也收站点把分类写成路径片段的形态（tv、/fenlei/1…）——validNativeCategory
// 已为自定义源放行这类 ID，规则解析这里不应再按纯数字过滤。
var xbpqCategoryIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_/-]{0,79}$`)

// categories：分类串 name$id#name$id（含 `1--子分类$5#...` 变体）
func xbpqRuleCategories(rule xbpqRule) []nativeCategory {
	raw := rule.field("分类")
	if raw == "" {
		return nil
	}
	var categories []nativeCategory
	seen := map[string]bool{}
	add := func(id, name string) {
		if id == "" || name == "" || seen[id] {
			return
		}
		seen[id] = true
		categories = append(categories, nativeCategory{ID: id, Name: name})
	}
	// 先扫所有行：主分类行形如 `电视剧$2`，子分类行形如 `1--动作片$5#喜剧片$6`
	type subEntry struct {
		main string
		id   string
		name string
	}
	var subs []subEntry
	for _, line := range strings.Split(raw, "||") {
		line = strings.TrimSpace(line)
		if line == "" || line == "空" {
			continue
		}
		for _, entry := range strings.Split(line, "#") {
			name, id, found := strings.Cut(entry, "$")
			if !found {
				continue
			}
			name = xbpqCleanText(name)
			id = strings.TrimSpace(id)
			if main, subName, hasMain := strings.Cut(name, "--"); hasMain && xbpqCategoryIDPattern.MatchString(main) {
				if subName != "" {
					subs = append(subs, subEntry{main: main, id: id, name: subName})
				}
				continue
			}
			if xbpqCategoryIDPattern.MatchString(id) && name != "" {
				add(id, name)
			}
		}
	}
	// 子分类挂到主分类 ID 之后（形如 5 的子 ID 独立成项，名称带「主分类-子」前缀）
	for _, sub := range subs {
		if seen[sub.id] {
			continue
		}
		seen[sub.id] = true
		categories = append(categories, nativeCategory{ID: sub.id, Name: sub.name})
	}
	return categories
}

// xbpqCategoryURL 渲染分类页地址（含翻页）。
func xbpqCategoryURL(rule xbpqRule, base, categoryID string, page int) string {
	template := rule.field("分类url", "分类Url")
	if template == "" {
		return ""
	}
	values := map[string]string{
		"cateId":  categoryID,
		"catePg":  strconv.Itoa(page),
		"handurl": base + "/",
		"limit":   "20",
		"area":    "", "class": "", "lang": "", "year": "", "letter": "", "by": "",
	}
	if primary, _, hasSecond := strings.Cut(template, "#"); hasSecond && strings.Contains(template, "二级") {
		template = primary
	}
	return xbpqRenderURL(template, base, values)
}

// ---- 目录 ----

func (d *Downloader) xbpqCatalog(ctx context.Context, source, base string, page int, category string, rule xbpqRule) ([]Drama, bool, error) {
	categoryID := strings.TrimSpace(category)
	if matches := regexp.MustCompile(`^/?(\d{1,6})(?:\.html)?$`).FindStringSubmatch(strings.TrimPrefix(categoryID, "/")); len(matches) > 1 {
		categoryID = matches[1]
	}
	if categoryID == "" {
		if categories := xbpqRuleCategories(rule); len(categories) > 0 {
			categoryID = categories[0].ID
		}
	}
	pageURL := xbpqCategoryURL(rule, base, categoryID, page)
	if pageURL == "" {
		return nil, false, fmt.Errorf("规则缺少分类url")
	}
	prefix := ""
	if categoryID != "" {
		prefix = "列表"
	}
	items := d.xbpqExtractItems(ctx, rule, base, pageURL, prefix)
	if len(items) == 0 {
		return nil, false, fmt.Errorf("规则未抽取到列表条目")
	}
	titlePattern := rule.field("标题", prefix+"标题")
	linkPattern := rule.field("链接", prefix+"链接")
	picturePattern := rule.field("图片", prefix+"图片", "主图")
	remarkPattern := rule.field("副标题", prefix+"副标题", "备注")
	numericID := rule.field("详情url", "详情页url") != ""
	var dramas []Drama
	for _, item := range items {
		title := xbpqCleanText(xbpqItemField(item, titlePattern))
		link := xbpqItemField(item, linkPattern)
		link = xbpqJoinLink(pageURL, link)
		if title == "" || link == "" || !strings.HasPrefix(link, "http") {
			continue
		}
		cover := xbpqJoinLink(pageURL, xbpqItemField(item, picturePattern))
		remark := xbpqCleanText(xbpqItemField(item, remarkPattern))
		id := xbpqIDFromLink(link, numericID)
		drama := Drama{
			ID:          providerDramaID(source, id),
			Source:      source,
			SourceID:    id,
			Title:       title,
			Cover:       cover,
			Remark:      remark,
			ChannelName: duanjuSourceName(source),
		}
		if number := duanjuEpisodeNumber(remark, 0); number > 0 {
			drama.EpisodeCount = json.Number(strconv.Itoa(number))
		}
		dramas = append(dramas, drama)
	}
	if len(dramas) == 0 {
		return nil, false, fmt.Errorf("规则抽取的条目缺少标题或链接")
	}
	return dramas, true, nil
}

// ---- 搜索 ----

func (d *Downloader) xbpqSearch(ctx context.Context, source, base, query string, rule xbpqRule) ([]Drama, error) {
	template := rule.field("搜索url", "搜索Url")
	if template == "" {
		return nil, fmt.Errorf("规则缺少搜索url")
	}
	encoded := url.QueryEscape(query)
	pathEscaped := url.PathEscape(query)
	candidates := []string{
		strings.NewReplacer("{wd}", encoded).Replace(template),
		strings.NewReplacer("{wd}", pathEscaped).Replace(template),
	}
	var lastErr error
	for _, candidate := range candidates {
		address, method, body := xbpqSplitSpec(candidate)
		if address == "" {
			continue
		}
		if !strings.Contains(address, "{wd}") {
			pageURL := xbpqRenderURL(address, base, map[string]string{"wd": encoded})
			if method != "" && strings.Contains(body, "{wd}") {
				body = strings.ReplaceAll(body, "{wd}", query)
			}
			items := d.xbpqExtractItems(ctx, rule, base, xbpqComposeSpec(pageURL, method, body), "搜索")
			if len(items) == 0 {
				items = d.xbpqExtractItems(ctx, rule, base, xbpqComposeSpec(pageURL, method, body), "")
			}
			if len(items) == 0 {
				lastErr = fmt.Errorf("搜索页未抽取到结果")
				continue
			}
			dramas, _, err := d.xbpqItemsToCatalog(ctx, source, base, pageURL, items, rule, "搜索")
			if err != nil {
				lastErr = err
				continue
			}
			return dramas, nil
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("搜索规则不可用")
	}
	return nil, lastErr
}

func xbpqComposeSpec(address, method, body string) string {
	if method == "" {
		return address
	}
	return address + ";" + method + ";" + body
}

func (d *Downloader) xbpqItemsToCatalog(ctx context.Context, source, base, pageURL string, items []xbpqItem, rule xbpqRule, prefix string) ([]Drama, bool, error) {
	titlePattern := rule.field(prefix+"标题", "标题")
	linkPattern := rule.field(prefix+"链接", "链接")
	picturePattern := rule.field(prefix+"图片", "图片")
	remarkPattern := rule.field(prefix+"副标题", "副标题")
	numericID := rule.field("详情url", "详情页url") != ""
	var dramas []Drama
	for _, item := range items {
		title := xbpqCleanText(xbpqItemField(item, titlePattern))
		link := xbpqJoinLink(pageURL, xbpqItemField(item, linkPattern))
		if title == "" || link == "" || !strings.HasPrefix(link, "http") {
			continue
		}
		id := xbpqIDFromLink(link, numericID)
		remark := xbpqCleanText(xbpqItemField(item, remarkPattern))
		dramas = append(dramas, Drama{
			ID: providerDramaID(source, id), Source: source, SourceID: id, Title: title,
			Cover: xbpqJoinLink(pageURL, xbpqItemField(item, picturePattern)), Remark: remark,
			ChannelName: duanjuSourceName(source),
		})
	}
	return dramas, len(dramas) >= 8, nil
}

// ---- 详情与分集 ----

func (d *Downloader) xbpqDetail(ctx context.Context, source, base, sourceID string, rule xbpqRule) (Drama, []Chapter, error) {
	pageURL := xbpqLinkFromID(base, sourceID)
	if pageURL == "" {
		template := rule.field("详情url", "详情页url")
		pageURL = xbpqRenderURL(template, base, map[string]string{"id": sourceID})
	}
	if pageURL == "" {
		candidates := maccmsDetailCandidates(source, base, sourceID)
		if len(candidates) > 0 {
			pageURL = candidates[0]
		}
	}
	if pageURL == "" {
		return Drama{}, nil, fmt.Errorf("无法定位详情页地址")
	}
	body, err := d.xbpqFetch(ctx, pageURL, base+"/", rule.userAgent())
	if err != nil {
		return Drama{}, nil, err
	}
	document, _ := html.Parse(strings.NewReader(body))
	item := xbpqItem{text: body, doc: document}
	pick := func(patterns ...string) string {
		for _, pattern := range patterns {
			if pattern == "" {
				continue
			}
			if value := xbpqItemField(item, pattern); value != "" {
				return xbpqCleanText(value)
			}
		}
		return ""
	}
	// 详情标题优先用规则「影片名称」（「标题」字段是列表层语义，这里不借用）。
	title := pick(rule.field("影片名称"), rule.field("name"))
	if title == "" {
		title = maccmsDetailTitle(document)
	}
	if title == "" {
		for _, name := range []string{"h1", "h2"} {
			nodes := providerHTMLNodes(document, func(node *html.Node) bool { return node.Data == name })
			if len(nodes) > 0 {
				if text := xbpqCleanText(providerHTMLText(nodes[0])); len([]rune(text)) >= 2 && len([]rune(text)) <= 60 {
					title = text
				}
				break
			}
		}
	}
	if title == "" {
		if nodes := providerHTMLNodes(document, func(node *html.Node) bool { return node.Data == "h1" }); len(nodes) > 0 {
			title = xbpqCleanText(providerHTMLText(nodes[0]))
		}
	}
	intro := pick(rule.field("简介"), maccmsDetailIntro(document))
	cover := ""
	if pattern := rule.field("封面", "图片"); pattern != "" {
		cover = xbpqJoinLink(pageURL, xbpqItemField(item, pattern))
	}
	if cover == "" {
		cover = maccmsDetailCover(document, pageURL)
	}
	category := pick(rule.field("类型"), maccmsDetailCategory(document))
	drama := Drama{
		ID: providerDramaID(source, sourceID), Source: source, SourceID: sourceID,
		Title: title, Intro: intro, Cover: cover, Category: category,
		Remark: pick(rule.field("状态", "影片状态")),
		Desc:   intro,
	}
	if director := pick(rule.field("导演")); director != "" {
		drama.Tags = append(drama.Tags, "导演:"+director)
	}
	if actor := pick(rule.field("主演")); actor != "" {
		drama.Tags = append(drama.Tags, "主演:"+actor)
	}
	episodes := xbpqEpisodes(body, pageURL, rule)
	if len(episodes) == 0 {
		return Drama{}, nil, fmt.Errorf("规则未解析到分集")
	}
	var chapters []Chapter
	number := 0
	for _, episode := range episodes {
		number++
		link := xbpqJoinLink(pageURL, episode.url)
		if link == "" {
			continue
		}
		chapters = append(chapters, duanjuChapter(source, sourceID, number, episode.title, link, link, base+"/"))
	}
	if len(chapters) == 0 {
		return Drama{}, nil, fmt.Errorf("规则未解析到可播放分集")
	}
	sortDuanjuChapters(chapters)
	drama.EpisodeCount = json.Number(strconv.Itoa(len(chapters)))
	return drama, chapters, nil
}

type xbpqEpisode struct {
	title string
	url   string
}

// xbpqEpisodes 解析播放串：播放数组→$$$ 线路→# 分集→标题$链接。
// 取集数最多的一条线路（与 API 链路同策略）。
func xbpqEpisodes(body, pageURL string, rule xbpqRule) []xbpqEpisode {
	arrayPattern := rule.field("播放数组")
	if arrayPattern == "" {
		return nil
	}
	raw := xbpqCutOnce(body, arrayPattern)
	if raw == "" {
		return nil
	}
	raw = strings.ReplaceAll(raw, "\r\n", "#")
	if index := strings.Index(raw, `\/`); index >= 0 {
		raw = strings.ReplaceAll(raw, `\/`, "/")
	}
	listSplit := rule.field("播放列表")
	if listSplit == "" {
		listSplit = "#"
	} else if listSplit == "&&" {
		listSplit = "#"
	}
	titlePattern := rule.field("播放标题")
	linkPattern := rule.field("播放链接")
	routePattern := rule.field("线路标题")
	groups := strings.Split(raw, "$$$")
	var best []xbpqEpisode
	for routeIndex, group := range groups {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		var entries []xbpqEpisode
		for _, item := range strings.Split(group, listSplit) {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			name, address := item, ""
			if head, tail, found := strings.Cut(item, "$"); found {
				name, address = head, tail
			}
			if titlePattern != "" {
				if cut := xbpqCutOnce(item, titlePattern); cut != "" {
					name = cut
				}
			}
			if linkPattern != "" {
				if cut := xbpqCutOnce(item, linkPattern); cut != "" {
					address = cut
				}
			}
			if strings.HasPrefix(address, "$") {
				continue
			}
			address = maccmsNormalizePlaybackURL(strings.TrimSpace(address))
			name = xbpqCleanText(name)
			if name == "" && strings.Contains(address, "://") {
				name = "第" + strconv.Itoa(len(entries)+1) + "集"
			}
			if name == "" || address == "" {
				continue
			}
			if routePattern != "" && routeIndex == 0 {
				// 线路标题仅用于展示，不影响分集解析
			}
			entries = append(entries, xbpqEpisode{title: name, url: address})
		}
		if len(entries) > len(best) {
			best = entries
		}
	}
	// 相对播放地址（如仅数字 ID）用链接补全；纯文字条目丢弃。
	var out []xbpqEpisode
	for _, entry := range best {
		link := entry.url
		if !strings.Contains(link, "://") && !strings.HasPrefix(link, "/") {
			continue
		}
		if !strings.HasPrefix(link, "http") {
			link = duanjuAbsolute(pageURL, link)
		}
		out = append(out, xbpqEpisode{title: entry.title, url: link})
	}
	return out
}

// xbpqResolvePage 播放页取直链：跳转播放链接模板 → 标准 player_data → MacCMS 嗅探兜底。
func (d *Downloader) xbpqResolveMedia(ctx context.Context, task Task, rule xbpqRule, base, name string) (providerMedia, error) {
	pageURL := strings.TrimSpace(task.Chapter.PageURL)
	address := strings.TrimSpace(task.Chapter.VideoURL)
	referer := firstNonEmpty(task.Chapter.Referer, base+"/")
	if isProviderHTTPMediaURL(address) && duanjuLooksLikeMedia(address) {
		return d.prepareWebProviderMedia(ctx, providerMedia{URL: address, Referer: referer}, name)
	}
	if pageURL == "" {
		pageURL = address
	}
	body, err := d.xbpqFetch(ctx, pageURL, referer, rule.userAgent())
	if err != nil {
		return providerMedia{}, err
	}
	if jump := rule.field("跳转播放链接"); jump != "" {
		for _, candidate := range xbpqJumpCandidates(body, jump) {
			candidate = xbpqJoinLink(pageURL, candidate)
			candidate = maccmsNormalizePlaybackURL(candidate)
			if isProviderHTTPMediaURL(candidate) && duanjuLooksLikeMedia(candidate) {
				return d.prepareWebProviderMedia(ctx, providerMedia{URL: candidate, Referer: pageURL}, name)
			}
			if strings.HasPrefix(candidate, "http") && !duanjuLooksLikeMedia(candidate) {
				// 跳转到二级播放页：再取一次
				if second, secondErr := d.xbpqFetch(ctx, candidate, pageURL, rule.userAgent()); secondErr == nil {
					if direct := xbpqJumpValue(second, jump); direct != "" {
						direct = maccmsNormalizePlaybackURL(direct)
						if isProviderHTTPMediaURL(direct) {
							return d.prepareWebProviderMedia(ctx, providerMedia{URL: direct, Referer: candidate}, name)
						}
					}
				}
			}
		}
	}
	if media := maccmsPlayerURL(body); media != "" {
		media = maccmsNormalizePlaybackURL(media)
		if isProviderHTTPMediaURL(media) {
			return d.prepareWebProviderMedia(ctx, providerMedia{URL: media, Referer: pageURL}, name)
		}
		if parsed := d.resolveMaccmsCloudParse(ctx, body, media, pageURL); parsed != "" {
			return d.prepareWebProviderMedia(ctx, providerMedia{URL: maccmsNormalizePlaybackURL(parsed), Referer: pageURL}, name)
		}
	}
	return providerMedia{}, fmt.Errorf("%s未解析到播放地址", name)
}

func xbpqJumpValue(body, jump string) string {
	value := xbpqCutOnce(body, jump)
	if value == "" {
		return ""
	}
	if index := strings.IndexAny(value, "\r\n"); index >= 0 {
		value = value[:index]
	}
	return strings.TrimSpace(value)
}

func xbpqJumpCandidates(body, jump string) []string {
	var out []string
	if value := xbpqJumpValue(body, jump); value != "" {
		out = append(out, value)
	}
	for _, part := range strings.Split(jump, "||") {
		steps := xbpqParseSteps(part)
		if len(steps) == 0 {
			continue
		}
		segment, _, ok := xbpqApplyStepScan(body, steps[0])
		if ok && !strings.Contains(segment, "\n") && !strings.Contains(segment, " ") {
			out = append(out, segment)
		}
	}
	return dedupeStrings(out)
}

func dedupeStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// ---- 规则存取 ----

// customXBPQRule 读取自定义源携带的 XBPQ 规则；无规则返回 ok=false。
func customXBPQRule(id string) (xbpqRule, bool) {
	engine := nativeEngineSnapshot()
	if engine == nil {
		return xbpqRule{}, false
	}
	record, found := engine.customRegistry.get(canonicalProviderSource(id))
	if !found || strings.TrimSpace(record.Rule) == "" {
		return xbpqRule{}, false
	}
	return parseXBPQRule(record.Rule)
}

// xbpqCategories 分类列表（规则驱动）。
func (d *Downloader) xbpqCategories(rule xbpqRule) []nativeCategory {
	return xbpqRuleCategories(rule)
}
