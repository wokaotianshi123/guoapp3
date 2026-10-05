package core

import (
	"strconv"
	"strings"
	"testing"
)

func TestMaccmsPlayRouteParsesBothFamilies(t *testing.T) {
	if sid, nid := maccmsPlayRoute("/index.php/vod/play/id/110925/sid/3/nid/12.html"); sid != 3 || nid != 12 {
		t.Fatalf("macplus route wrong: sid=%d nid=%d", sid, nid)
	}
	if sid, nid := maccmsPlayRoute("/vodplay/110925-6-1.html"); sid != 6 || nid != 1 {
		t.Fatalf("myui route wrong: sid=%d nid=%d", sid, nid)
	}
	if sid, nid := maccmsPlayRoute("/xksiplay/110925-2-30.html"); sid != 2 || nid != 30 {
		t.Fatalf("myui prefix route wrong: sid=%d nid=%d", sid, nid)
	}
	if sid, nid := maccmsPlayRoute("/voddetail/110925.html"); sid != 0 || nid != 0 {
		t.Fatalf("detail link must not be a route: sid=%d nid=%d", sid, nid)
	}
}

func TestMaccmsCollapseRouteEpisodesKeepsSingleLine(t *testing.T) {
	var episodes []providerEpisode
	// 10 条线路 × 3 集，模拟详情页把「线路 × 集数」全部平铺。
	for sid := 1; sid <= 10; sid++ {
		for nid := 1; nid <= 3; nid++ {
			episodes = append(episodes, providerEpisode{
				Key:   "",
				Title: "第0" + string(rune('0'+nid)) + "集",
				URL:   "/vodplay/110925-" + strconv.Itoa(sid) + "-" + strconv.Itoa(nid) + ".html",
			})
		}
	}
	collapsed := maccmsCollapseRouteEpisodes(episodes)
	if len(collapsed) != 3 {
		t.Fatalf("expected 3 episodes on one route, got %d", len(collapsed))
	}
	sid, _ := maccmsPlayRoute(collapsed[0].URL)
	for index, episode := range collapsed {
		current, nid := maccmsPlayRoute(episode.URL)
		if current != sid {
			t.Fatalf("episode %d comes from another route: sid=%d", index, current)
		}
		if nid != index+1 {
			t.Fatalf("episode %d is not sorted by number: nid=%d", index, nid)
		}
		if episode.Index != index+1 || episode.Key != strconv.Itoa(index+1) {
			t.Fatalf("episode %d index/key not renumbered: %+v", index, episode)
		}
	}
}

func TestMaccmsCollapseRouteEpisodesPrefersRichestLine(t *testing.T) {
	episodes := []providerEpisode{
		{URL: "/vodplay/1-1-1.html"},
		{URL: "/vodplay/1-1-2.html"},
		{URL: "/vodplay/1-2-1.html"},
		{URL: "/vodplay/1-2-2.html"},
		{URL: "/vodplay/1-2-3.html"},
	}
	collapsed := maccmsCollapseRouteEpisodes(episodes)
	if len(collapsed) != 3 {
		t.Fatalf("expected the 3-episode route, got %d", len(collapsed))
	}
	if sid, _ := maccmsPlayRoute(collapsed[0].URL); sid != 2 {
		t.Fatalf("expected route 2, got %d", sid)
	}
}

func TestMaccmsCollapseRouteEpisodesKeepsSingleRouteSites(t *testing.T) {
	// 只有一条线路的站点（或无法识别线路结构）必须保持原样，不能误删分集。
	episodes := []providerEpisode{
		{URL: "/vodplay/110925-1-1.html"},
		{URL: "/vodplay/110925-1-2.html"},
		{URL: "/drama/110925/episode-3"},
	}
	if got := maccmsCollapseRouteEpisodes(episodes); len(got) != 3 {
		t.Fatalf("single route site must stay untouched, got %d", len(got))
	}
}

func TestMaccmsEpisodeLinkLikelyRejectsPlaceholders(t *testing.T) {
	for _, link := range []string{"javascript:;", "#", "mailto:a@b.c", "", "data:text/html,x"} {
		if maccmsEpisodeLinkLikely(link) {
			t.Fatalf("%q should be rejected", link)
		}
	}
	for _, link := range []string{"/vodplay/110925-1-1.html", "https://feikuai.in/vodplay/110925-1-1.html"} {
		if !maccmsEpisodeLinkLikely(link) {
			t.Fatalf("%q should be accepted", link)
		}
	}
}

func TestMaccmsUnescapeJSDecodesUnicodeEscapes(t *testing.T) {
	// player.js 的 escape() 会把中文写成 %uXXXX，url.QueryUnescape 不认，必须先还原。
	raw := "%68%74%74%70%73%3A%2F%2F%76%2E%66%65%6E%67%62%61%6F%38%2E%63%6F%6D%2F%76%69%64%65%6F%2F%6D%75%73%68%65%6E%6A%69%2F%u7B2C%30%31%u96C6%2F%69%6E%64%65%78%2E%6D%33%75%38"
	got := maccmsUnescapeJS(raw)
	if !strings.HasPrefix(got, "https://v.fengbao8.com/video/mushenji/") || !strings.HasSuffix(got, "/index.m3u8") {
		t.Fatalf("unexpected decode result: %q", got)
	}
	if strings.Contains(got, "%u") {
		t.Fatalf("unicode escapes left in result: %q", got)
	}
}
