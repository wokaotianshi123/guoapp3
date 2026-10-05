package core

import (
	"encoding/json"
	"testing"
)

// MacCMS 标准接口返回体（节选，字段名与线上一致）。
const maccmsAPISample = `{"code":1,"msg":"数据列表","page":"1","pagecount":11026,"limit":"20","total":220517,"list":[
{"vod_id":261911,"type_id":27,"type_name":"日韩动漫","vod_name":"大叔喜欢可爱小玩意",
 "vod_pic":"https:\/\/pic.example\/a.jpg","vod_remarks":"更新至第01集","vod_class":"喜剧,动画",
 "vod_content":"<p>小路三贵长相帅气<\/p>",
 "vod_play_from":"ffm3u8$$$bfzym3u8$$$rym3u8",
 "vod_play_url":"第01集$https://a.example/index.m3u8#第02集$https://a.example/2.m3u8$$$第01集$https://b.example/index.m3u8$$$第01集$https://c.example/index.m3u8"}
]}`

func TestMaccmsAPIItemsAndClasses(t *testing.T) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(maccmsAPISample), &payload); err != nil {
		t.Fatalf("解包失败: %v", err)
	}
	items := maccmsAPIItems(payload)
	if len(items) != 1 {
		t.Fatalf("期望 1 条，实得 %d", len(items))
	}
	if maccmsAPIString(items[0]["vod_id"]) != "261911" {
		t.Fatalf("vod_id 解析错误: %v", items[0]["vod_id"])
	}
	if got := maccmsAPIInt(payload["pagecount"]); got != 11026 {
		t.Fatalf("pagecount 解析错误: %d", got)
	}
	var withClass map[string]any
	body := `{"code":1,"class":[{"type_id":1,"type_name":"电影"},{"type_id":2,"type_name":"连续剧"}]}`
	if err := json.Unmarshal([]byte(body), &withClass); err != nil {
		t.Fatalf("解包失败: %v", err)
	}
	if rows := maccmsAPIClasses(withClass); len(rows) != 2 {
		t.Fatalf("class 解析错误: %d", len(rows))
	}
}

func TestMaccmsAPIDramaAndChapters(t *testing.T) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(maccmsAPISample), &payload); err != nil {
		t.Fatalf("解包失败: %v", err)
	}
	item := maccmsAPIItems(payload)[0]
	drama, chapters := maccmsAPIDrama("custom:test", "https://feikuai.in", item)
	if drama.ID != "custom:test:261911" {
		t.Fatalf("drama id 错误: %s", drama.ID)
	}
	if drama.Title != "大叔喜欢可爱小玩意" || drama.Category != "日韩动漫" {
		t.Fatalf("标题/分类错误: %q / %q", drama.Title, drama.Category)
	}
	if drama.Cover != "https://pic.example/a.jpg" {
		t.Fatalf("封面错误: %q", drama.Cover)
	}
	if drama.Intro != "小路三贵长相帅气" {
		t.Fatalf("简介未清洗标签: %q", drama.Intro)
	}
	// 三条线路分别 2 / 1 / 1 集，应取集数最全的第一条。
	if len(chapters) != 2 {
		t.Fatalf("应取 2 集，实得 %d", len(chapters))
	}
	if chapters[0].VideoURL != "https://a.example/index.m3u8" {
		t.Fatalf("分集地址错误: %q", chapters[0].VideoURL)
	}
	if chapters[0].Title != "第01集" {
		t.Fatalf("分集标题错误: %q", chapters[0].Title)
	}
	if number, ok := drama.EpisodeCount.(json.Number); !ok || number.String() != "2" {
		t.Fatalf("集数字段错误: %v", drama.EpisodeCount)
	}
}

func TestMaccmsAPIRouteGroupsFallback(t *testing.T) {
	// 老模板用换行分隔分集。
	item := map[string]any{
		"vod_play_from": "m3u8",
		"vod_play_url":  "第1集$https://x/1.m3u8\r\n第2集$https://x/2.m3u8\r\n第3集$https://x/3.m3u8",
	}
	routes := maccmsAPIRouteGroups(item)
	if len(routes) != 1 || len(routes[0]) != 3 {
		t.Fatalf("换行分隔解析失败: %+v", routes)
	}
}
