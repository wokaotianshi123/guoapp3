'use strict';

var view = document.getElementById('view');
var sourceSelect = document.getElementById('source');
var searchInput = document.getElementById('search-input');
var searchForm = document.getElementById('search-form');

// 自动连播的开关存在浏览器本地，换浏览器重开也保留上次的选择。
var AUTO_NEXT_KEY = 'duanjuweb:autoNext';
// 倍速同样记在本地：切集会重建 <video>，不记下来就会被打回 1 倍速。
var PLAY_RATE_KEY = 'duanjuweb:playbackRate';

var playbackRate = readPlaybackRate();
var resumeFullscreen = false;
var suppressHashRender = false;
var mountToken = 0;

function readPlaybackRate() {
  try {
    var value = Number(window.localStorage.getItem(PLAY_RATE_KEY));
    if (value >= 0.25 && value <= 4) {
      return value;
    }
  } catch (error) {
    // 隐私模式读不到本地存储时用默认倍速。
  }
  return 1;
}

function writePlaybackRate(value) {
  try {
    window.localStorage.setItem(PLAY_RATE_KEY, String(value));
  } catch (error) {
    // 写不进去只在当前会话生效，不影响播放。
  }
}

function readAutoNext() {
  try {
    return window.localStorage.getItem(AUTO_NEXT_KEY) !== 'off';
  } catch (error) {
    return true;
  }
}

function writeAutoNext(value) {
  try {
    window.localStorage.setItem(AUTO_NEXT_KEY, value ? 'on' : 'off');
  } catch (error) {
    // 隐私模式下写不了本地存储，只在当前会话生效。
  }
}

var state = {
  source: 'hongguo',
  categories: [],
  categoriesSource: '',
  category: '',
  page: 1,
  query: '',
  hasMore: false,
  drama: null,
  chapters: [],
  plan: null,
  index: 0,
  quality: 0,
  route: 0,
  sourceNames: {},
  ffmpeg: true,
  autoNext: readAutoNext(),
  library: { history: [], favorites: [] },
  libraryTab: 'history',
  libraryEditing: false,
  librarySelected: {},
};

var hlsInstance = null;
var blackWatchdog = null;
var lastProgressSave = 0;
var autoNextTimer = null;

// 播放失败自动换线（对齐 Flutter 端 fallback 机制）：
// hls.js 出现致命网络/媒体错误时，自动请求下一条线路重建播放，
// 全部线路试完才提示播放失败。
var routeTried = {};
var routeTotal = 1;
var failoverBusy = false;

function markRouteTried(route) {
  routeTried[route] = true;
}

function resetRouteTried(route, total) {
  routeTried = {};
  routeTotal = total || 1;
  markRouteTried(route || 0);
}

function nextRouteToTry() {
  for (var route = 0; route < routeTotal; route += 1) {
    if (!routeTried[route]) {
      return route;
    }
  }
  return -1;
}

function failoverRoute(reasonText) {
  var drama = state.drama;
  var chapters = state.chapters || [];
  var plan = state.plan;
  var video = document.getElementById('player');
  if (!drama || !plan || !video || failoverBusy) {
    return;
  }
  var mountAt = mountToken;
  var route = nextRouteToTry();
  if (route < 0) {
    showNotice('播放失败：' + (reasonText || '未知错误') + '（已尝试全部 ' + routeTotal + ' 条线路）');
    return;
  }
  failoverBusy = true;
  markRouteTried(route);
  showNotice('当前线路无法播放，正在自动切换到线路 ' + (route + 1) + '/' + routeTotal + '…', 'switching');
  apiPlay({
    drama: drama,
    chapter: chapters[state.index],
    index: state.index,
    quality: state.quality || 0,
    route: route,
  })
    .then(function (next) {
      if (mountAt !== mountToken) {
        return; // 用户已手动切线路/换集，放弃这次迟到的换线结果
      }
      state.plan = next;
      state.route = route;
      routeTotal = next.routeCount || routeTotal;
      var routeBox = document.getElementById('route-select');
      if (routeBox) {
        routeBox.value = String(route);
      }
      // 新线路是新的播放会话，沿用同一 <video> 就地重建，倍速/全屏都不丢。
      mountVideo(next.url, 0);
    })
    .catch(function () {
      if (mountAt !== mountToken) {
        return;
      }
      showNotice('线路 ' + (route + 1) + ' 解析失败，正在尝试其它线路…');
      setTimeout(function () {
        failoverBusy = false;
        if (mountAt === mountToken) {
          failoverRoute(reasonText);
        }
      }, 800);
    })
    .finally(function () {
      failoverBusy = false;
    });
}

function escapeHTML(value) {
  return String(value === undefined || value === null ? '' : value).replace(/[&<>"']/g, function (char) {
    return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[char];
  });
}

function api(payload) {
  return fetch('/api/request', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
    .then(function (response) {
      return response.json();
    })
    .then(function (data) {
      if (!data.ok) {
        throw new Error(data.error || '请求失败');
      }
      return data.data;
    });
}

function coverSrc(drama) {
  if (!drama || !drama.id) {
    return '';
  }
  var query = new URLSearchParams();
  query.set('id', drama.id);
  if (drama.source) {
    query.set('source', String(drama.source));
  }
  var cover = drama.cover ? String(drama.cover) : '';
  if (/^https?:\/\//i.test(cover)) {
    query.set('u', cover);
  }
  return '/api/cover?' + query.toString();
}

function coverHTML(drama) {
  var url = coverSrc(drama);
  if (!url) {
    return '<div class="cover"><div class="fallback">' + escapeHTML(drama.title || '无封面') + '</div></div>';
  }
  return (
    '<div class="cover"><img loading="lazy" src="' +
    escapeHTML(url) +
    '" alt="" onerror="this.style.display=\'none\'" /></div>'
  );
}

/* ---------- 收藏夹与播放记录 ---------- */

function libraryRequest(payload) {
  return fetch('/api/library', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
    .then(function (response) {
      return response.json();
    })
    .then(function (data) {
      if (!data.ok) {
        throw new Error(data.error || '收藏夹更新失败');
      }
      state.library = { history: data.data.history || [], favorites: data.data.favorites || [] };
      return state.library;
    });
}

function loadLibrary() {
  return fetch('/api/library')
    .then(function (response) {
      return response.json();
    })
    .then(function (data) {
      if (data && data.ok && data.data) {
        state.library = { history: data.data.history || [], favorites: data.data.favorites || [] };
      }
      return state.library;
    })
    .catch(function () {
      return state.library;
    });
}

function entryKey(drama) {
  var source = (drama && (drama.source || state.source)) || '';
  return String(source) + '|' + String((drama && drama.id) || '');
}

function sourceLabel(source) {
  return state.sourceNames[source] || source || '';
}

// 播放记录要带上海报、片名、站源名称与看到第几集、看到几分几秒。
function currentEntry(position, duration) {
  var drama = state.drama || {};
  var chapter = (state.chapters || [])[state.index] || {};
  return {
    id: drama.id || '',
    source: drama.source || state.source || '',
    sourceName: sourceLabel(drama.source || state.source),
    title: drama.title || '',
    cover: drama.cover || '',
    episodes: drama.episodes ? String(drama.episodes) : '',
    index: state.index,
    episodeTitle: chapter.title || '',
    position: position || 0,
    duration: duration || 0,
    updatedAt: Date.now(),
  };
}

function findEntry(list, key, index) {
  for (var i = 0; i < list.length; i += 1) {
    if (entryKey(list[i]) !== key) {
      continue;
    }
    if (typeof index === 'number' && Number(list[i].index || 0) !== index) {
      continue;
    }
    return list[i];
  }
  return null;
}

function isFavorite(drama) {
  return !!findEntry(state.library.favorites, entryKey(drama || state.drama));
}

function formatTime(seconds) {
  var total = Math.max(0, Math.floor(Number(seconds) || 0));
  var hours = Math.floor(total / 3600);
  var minutes = Math.floor((total % 3600) / 60);
  var rest = total % 60;
  var mm = String(minutes).padStart(hours ? 2 : 1, '0');
  var ss = String(rest).padStart(2, '0');
  return (hours ? hours + ':' + mm : mm) + ':' + ss;
}

function progressText(entry) {
  var parts = ['第 ' + (Number(entry.index || 0) + 1) + ' 集'];
  if (entry.duration > 0 && entry.position > 0) {
    parts.push(formatTime(entry.position) + ' / ' + formatTime(entry.duration));
  } else if (entry.position > 0) {
    parts.push('看到 ' + formatTime(entry.position));
  } else {
    parts.push('刚开始');
  }
  if (entry.episodeTitle && !/^第\s*\d+\s*集$/.test(entry.episodeTitle.trim())) {
    parts.push(entry.episodeTitle);
  }
  return parts.join(' · ');
}

function progressPercent(entry) {
  if (!(entry.duration > 0) || !(entry.position > 0)) {
    return 0;
  }
  return Math.min(100, Math.round((entry.position / entry.duration) * 100));
}

// 播放进度每 5 秒上报一次，切集、暂停、离开页面时立刻补一次。
function reportProgress(force) {
  if (!state.drama) {
    return;
  }
  var video = document.getElementById('player');
  if (!video) {
    return;
  }
  var position = Number(video.currentTime) || 0;
  if (!(position > 0)) {
    return;
  }
  var now = Date.now();
  if (!force && now - lastProgressSave < 5000) {
    return;
  }
  lastProgressSave = now;
  libraryRequest({
    list: 'history',
    op: 'put',
    item: currentEntry(position, Number(video.duration) || 0),
  }).catch(function () {});
}

function flushProgress() {
  reportProgress(true);
}

function hashParams() {
  var raw = location.hash.replace(/^#\/?/, '');
  var parts = raw.split('?');
  return { path: parts[0] || 'home', params: new URLSearchParams(parts[1] || '') };
}

function link(hash) {
  return hash;
}

function loadSources() {
  return fetch('/api/sources')
    .then(function (response) {
      return response.json();
    })
    .then(function (data) {
      var items = (data && data.items) || [];
      if (!items.length) {
        items = [{ id: 'hongguo', name: '红果' }];
      }
      state.sourceNames = {};
      items.forEach(function (item) {
        state.sourceNames[item.id] = item.name;
      });
      sourceSelect.innerHTML = items
        .map(function (item) {
          return '<option value="' + escapeHTML(item.id) + '">' + escapeHTML(item.name) + '</option>';
        })
        .join('');
      if (!items.some(function (item) { return item.id === state.source; })) {
        state.source = items[0].id;
      }
      sourceSelect.value = state.source;
    })
    .catch(function () {});
}

function loadInfo() {
  return fetch('/api/info')
    .then(function (response) {
      return response.json();
    })
    .then(function (data) {
      if (data && data.name) {
        document.getElementById('brand').textContent = data.name;
        document.title = data.name;
      }
      state.ffmpeg = !(data && data.ffmpeg === '');
    })
    .catch(function () {});
}

var FFMPEG_MISSING =
  '未检测到 ffmpeg：红果等加密站源需要服务端转码才能播放，其余站源不受影响。' +
  'Windows 上重新运行 start.bat 会提示下载 ffmpeg；也可以执行 winget install ffmpeg 后重启服务。';

function ffmpegNotice() {
  if (state.ffmpeg) {
    return '';
  }
  return (
    '<div class="notice" style="display:block">' +
    escapeHTML(FFMPEG_MISSING) +
    ' <button type="button" id="ffmpeg-recheck" class="link-btn">重新检测</button>' +
    '</div>'
  );
}

function bindFFmpegRecheck() {
  var button = document.getElementById('ffmpeg-recheck');
  if (!button) {
    return;
  }
  button.addEventListener('click', function () {
    button.disabled = true;
    button.textContent = '检测中…';
    loadInfo().then(function () {
      render();
    });
  });
}

function loadCategories(force) {
  if (!force && state.categories.length && state.categoriesSource === state.source) {
    return Promise.resolve(state.categories);
  }
  return api({ action: 'categories', source: state.source })
    .then(function (data) {
      state.categories = (data.items || []).map(function (item) {
        return { id: item.id || '', name: item.name };
      });
      state.categoriesSource = state.source;
      return state.categories;
    })
    .catch(function () {
      state.categories = [{ id: '', name: '全部' }];
      state.categoriesSource = state.source;
      return state.categories;
    });
}

function hashFor(overrides) {
  var merged = {
    source: state.source,
    category: state.category,
    page: state.page,
    q: state.query,
  };
  Object.keys(overrides || {}).forEach(function (key) {
    merged[key] = overrides[key];
  });
  var query = new URLSearchParams();
  query.set('source', merged.source);
  if (merged.category) query.set('category', merged.category);
  if (merged.q) query.set('q', merged.q);
  query.set('page', String(merged.page));
  return '#/home?' + query.toString();
}

function renderCatalog() {
  view.innerHTML = '<div class="loading">正在加载剧库…</div>';
  return loadCategories(false).then(function (categories) {
    var payload = {
      action: 'catalog',
      source: state.source,
      category: state.category,
      page: state.page,
    };
    if (state.query) {
      payload.query = state.query;
    }
    return api(payload).catch(function (error) {
      if (state.category === '' && categories.length > 1) {
        state.category = categories[1].id;
        return api({ action: 'catalog', source: state.source, category: state.category, page: state.page });
      }
      throw error;
    });
  }).then(function (result) {
    var items = result.items || [];
    state.hasMore = !!result.hasMore;
    var chips = state.categories
      .map(function (item) {
        var active = item.id === state.category ? ' active' : '';
        return (
          '<a class="chip' + active + '" href="' +
          escapeHTML(link(hashFor({ category: item.id, page: 1 }))) +
          '">' + escapeHTML(item.name) + '</a>'
        );
      })
      .join('');
    var cards = items
      .map(function (drama) {
        return (
          '<div class="card"><a href="#/drama?id=' + encodeURIComponent(drama.id) + '">' +
          coverHTML(drama) +
          '<div class="title">' + escapeHTML(drama.title) + '</div>' +
          '<div class="meta">' + escapeHTML(drama.episodes ? drama.episodes + ' 集' : '') + '</div>' +
          '</a></div>'
        );
      })
      .join('');
    if (!cards) {
      cards = '<div class="empty">没有找到内容，换个分类或关键词试试。</div>';
    }
    var previous = state.page > 1
      ? '<a href="' + escapeHTML(link(hashFor({ page: state.page - 1 }))) + '">上一页</a>'
      : '<span class="disabled">上一页</span>';
    var next = state.hasMore
      ? '<a href="' + escapeHTML(link(hashFor({ page: state.page + 1 }))) + '">下一页</a>'
      : '<span class="disabled">下一页</span>';
    view.innerHTML =
      ffmpegNotice() +
      '<div class="chips">' + chips + '</div>' +
      '<div class="grid">' + cards + '</div>' +
      '<div class="pager">' + previous + '<span>第 ' + state.page + ' 页</span>' + next + '</div>';
    bindFFmpegRecheck();
  });
}

function ensureDrama(id, source) {
  if (
    state.drama &&
    state.drama.id === id &&
    (!source || !state.drama.source || state.drama.source === source)
  ) {
    return Promise.resolve(state.drama);
  }
  var drama = { id: id };
  if (source) {
    drama.source = source;
  }
  return api({ action: 'detail', drama: drama }).then(function (data) {
    state.drama = data.drama;
    state.chapters = data.chapters || [];
    return state.drama;
  });
}

function renderDrama(id, source) {
  view.innerHTML = '<div class="loading">正在加载剧集详情…</div>';
  return ensureDrama(id, source).then(function (drama) {
    var episodes = (state.chapters || [])
      .map(function (chapter, index) {
        return (
          '<a href="#/play?id=' + encodeURIComponent(drama.id) +
          '&index=' + index +
          (drama.source ? '&source=' + encodeURIComponent(drama.source) : '') + '">' +
          escapeHTML(chapter.title || '第 ' + (index + 1) + ' 集') +
          '</a>'
        );
      })
      .join('');
    view.innerHTML =
      '<div class="detail">' +
      '<div class="poster">' + coverHTML(drama) + '</div>' +
      '<div class="info">' +
      '<h1>' + escapeHTML(drama.title) + '</h1>' +
      '<div class="desc">' + escapeHTML(drama.description || '暂无简介') + '</div>' +
      '<div class="meta" style="color:var(--muted)">共 ' + (state.chapters || []).length + ' 集</div>' +
      '<div class="detail-actions">' +
      '<button type="button" class="btn" id="fav-toggle">' +
      (isFavorite(drama) ? '已收藏' : '收藏') +
      '</button>' +
      '</div>' +
      '</div></div>' +
      '<div class="episodes">' + episodes + '</div>';
    bindFavoriteToggle(drama);
  });
}

function favoritePayload(drama) {
  return {
    id: drama.id || '',
    source: drama.source || state.source || '',
    sourceName: sourceLabel(drama.source || state.source),
    title: drama.title || '',
    cover: drama.cover || '',
    episodes: drama.episodes ? String(drama.episodes) : '',
    index: 0,
    episodeTitle: '',
    position: 0,
    duration: 0,
    updatedAt: Date.now(),
  };
}

// 点一次加入收藏，再点一次取消。
function bindFavoriteToggle(drama) {
  var button = document.getElementById('fav-toggle');
  if (!button) {
    return;
  }
  button.addEventListener('click', function () {
    button.disabled = true;
    libraryRequest({ list: 'favorites', op: 'toggle', item: favoritePayload(drama) })
      .then(function () {
        var active = isFavorite(drama);
        button.textContent = active ? '已收藏' : '收藏';
        button.classList.toggle('active', active);
        button.disabled = false;
      })
      .catch(function (error) {
        button.disabled = false;
        showNotice(error.message || '收藏失败');
      });
  });
}

function episodeHash(id, source, index) {
  return (
    '#/play?id=' + encodeURIComponent(id) +
    '&index=' + index +
    (source ? '&source=' + encodeURIComponent(source) : '')
  );
}

// 自动连播不再做倒计时确认，播完直接加载下一集。
function stopAutoNextTimer() {
  if (autoNextTimer) {
    clearInterval(autoNextTimer);
    autoNextTimer = null;
  }
}

function cancelAutoNext() {
  stopAutoNextTimer();
  var box = document.getElementById('next-up');
  if (box) {
    box.style.display = 'none';
  }
}

// 换集时同步标题与上一集/下一集按钮状态，并让地址栏跟上（不触发整页重建）。
function syncEpisodeChrome(index) {
  var drama = state.drama;
  var chapters = state.chapters || [];
  var title = document.querySelector('.player-title');
  if (title && drama) {
    title.textContent = drama.title + ' · ' + ((chapters[index] || {}).title || '');
  }
  var next = document.getElementById('episode-next');
  if (next && drama) {
    if (index + 1 < chapters.length) {
      next.disabled = false;
      next.style.display = '';
    } else {
      next.style.display = 'none';
    }
  }
  var previous = document.getElementById('episode-previous');
  if (previous && drama) {
    if (index > 0) {
      previous.disabled = false;
      previous.style.display = '';
    } else {
      previous.style.display = 'none';
    }
  }
  var hash = drama ? episodeHash(drama.id, drama.source, index) : '';
  if (hash && location.hash !== hash) {
    // 只改地址栏，并标记本次 hashchange 不重建页面（就地换集复用同一个 <video>）。
    suppressHashRender = true;
    location.hash = hash;
  }
}

// 就地换集：复用同一个 <video>，全屏和倍速都不会被重置。
function playNextEpisode() {
  var drama = state.drama;
  var chapters = state.chapters || [];
  var nextIndex = state.index + 1;
  var video = document.getElementById('player');
  if (!drama || nextIndex >= chapters.length) {
    return Promise.resolve(false);
  }
  cancelAutoNext();
  if (!video) {
    // 播放器还没建好时退回整页跳转。
    location.hash = episodeHash(drama.id, drama.source, nextIndex);
    window.scrollTo(0, 0);
    return Promise.resolve(true);
  }
  state.route = 0;
  flushProgress();
  showNotice('正在加载下一集…');
  return apiPlay({
    drama: drama,
    chapter: chapters[nextIndex],
    index: nextIndex,
    quality: state.quality || 0,
    route: 0,
  })
    .then(function (plan) {
      if (!document.body.contains(video)) {
        return false;
      }
      state.index = nextIndex;
      state.plan = plan;
      resetRouteTried(plan.routeIndex || 0, plan.routeCount || 1);
      var note = document.getElementById('play-note');
      if (note) {
        note.textContent = '';
        note.style.display = 'none';
      }
      syncEpisodeChrome(nextIndex);
      mountVideo(plan.url, 0);
      libraryRequest({ list: 'history', op: 'put', item: currentEntry(0, 0) }).catch(function () {});
      return true;
    })
    .catch(function (error) {
      showNotice(error.message || '下一集加载失败，请手动点“下一集”重试。');
      return false;
    });
}

// 上一集：和下一集一样就地换集，复用同一个 <video>，倍速与全屏都不丢。
function playPreviousEpisode() {
  var drama = state.drama;
  var chapters = state.chapters || [];
  var prevIndex = state.index - 1;
  var video = document.getElementById('player');
  if (!drama || prevIndex < 0) {
    return Promise.resolve(false);
  }
  cancelAutoNext();
  if (!video) {
    location.hash = episodeHash(drama.id, drama.source, prevIndex);
    window.scrollTo(0, 0);
    return Promise.resolve(true);
  }
  state.route = 0;
  flushProgress();
  showNotice('正在加载上一集…');
  return apiPlay({
    drama: drama,
    chapter: chapters[prevIndex],
    index: prevIndex,
    quality: state.quality || 0,
    route: 0,
  })
    .then(function (plan) {
      if (!document.body.contains(video)) {
        return false;
      }
      state.index = prevIndex;
      state.plan = plan;
      resetRouteTried(plan.routeIndex || 0, plan.routeCount || 1);
      var note = document.getElementById('play-note');
      if (note) {
        note.textContent = '';
        note.style.display = 'none';
      }
      syncEpisodeChrome(prevIndex);
      mountVideo(plan.url, 0);
      libraryRequest({ list: 'history', op: 'put', item: currentEntry(0, 0) }).catch(function () {});
      return true;
    })
    .catch(function (error) {
      showNotice(error.message || '上一集加载失败，请手动点“上一集”重试。');
      return false;
    });
}

// 本集播完直接进下一集；已是最后一集或开关关闭时只做提示。
function scheduleAutoNext() {
  var chapters = state.chapters || [];
  var nextIndex = state.index + 1;
  if (!state.drama || nextIndex >= chapters.length) {
    showNotice('已经是最后一集了。');
    return;
  }
  if (!state.autoNext) {
    return;
  }
  playNextEpisode();
}

function fitVideo(video) {
  var width = video.videoWidth;
  var height = video.videoHeight;
  if (!width || !height) {
    return;
  }
  // 竖屏短剧很常见，按真实比例收窄并居中，画面最大化又不超出屏幕。
  if (height > width) {
    video.classList.add('portrait');
    video.style.setProperty('--ratio', String(width / height));
  } else {
    video.classList.remove('portrait');
    video.style.removeProperty('--ratio');
  }
}

function stopPlayback() {
  cancelAutoNext();
  flushProgress();
  if (hlsInstance) {
    hlsInstance.destroy();
    hlsInstance = null;
  }
  state.plan = null;
}

function apiPlay(payload) {
  return fetch('/api/play', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
    .then(function (response) {
      return response.json();
    })
    .then(function (data) {
      if (!data.ok) {
        throw new Error(data.error || '解析播放地址失败');
      }
      return data.data;
    });
}

// 红果等站源的视频是加密的 H.265，浏览器既不能解密也不能解码，
// 只能由服务端用 ffmpeg 解密重编码后再播。
var FFMPEG_HINT =
  '这一集是加密的 H.265 视频，浏览器无法直接播放，需要服务端转码。' +
  '请先安装 ffmpeg（Windows：winget install ffmpeg；macOS：brew install ffmpeg），' +
  '放到本程序同一个目录或加入 PATH，然后重启本地服务。';

var HEVC_NOTICE =
  '该视频采用 H.265/HEVC 编码，当前浏览器无法解码。' +
  '建议改用 Safari / Edge；Windows 用户可在「设置 → 应用 → 可选功能」安装' +
  '「HEVC 视频扩展」后重试。';

function showNotice(message, kind) {
  var box = document.getElementById('play-note');
  if (!box) {
    return;
  }
  box.setAttribute('data-kind', kind || '');
  box.textContent = message;
  box.style.display = 'block';
}

function applyPlaybackRate(video) {
  if (!video || !(playbackRate > 0)) {
    return;
  }
  try {
    if (video.playbackRate !== playbackRate) {
      video.playbackRate = playbackRate;
    }
  } catch (error) {
    // 个别浏览器在加载初期不允许改倍速，loadedmetadata 时会再试一次。
  }
}

function rememberPlaybackRate(value) {
  if (!(value > 0)) {
    return;
  }
  playbackRate = value;
  writePlaybackRate(value);
}

// 播放器事件只绑一次：就地换集复用同一个 <video>，避免监听器叠加。
function bindPlayerEvents(video) {
  video.addEventListener('ratechange', function () {
    rememberPlaybackRate(video.playbackRate);
  });
  video.addEventListener('playing', function () {
    var box = document.getElementById('play-note');
    if (box && (box.getAttribute('data-kind') === 'transcode' ||
      box.getAttribute('data-kind') === 'switching')) {
      box.style.display = 'none';
    }
  });
  video.addEventListener('loadedmetadata', function () {
    fitVideo(video);
    applyPlaybackRate(video);
  });
  video.addEventListener('timeupdate', function () {
    reportProgress(false);
  });
  video.addEventListener('pause', function () {
    reportProgress(true);
  });
  video.addEventListener('ended', function () {
    reportProgress(true);
    scheduleAutoNext();
  });
}

// 整页重建前记下倍速与全屏状态，重建后好还原。
function capturePlaybackState() {
  var video = document.getElementById('player');
  if (video) {
    rememberPlaybackRate(video.playbackRate);
  }
  resumeFullscreen =
    !!video &&
    (document.fullscreenElement === video || document.webkitFullscreenElement === video);
}

// 只有用户手势里才可能重新进全屏（点“下一集”链接属于这种情况）；
// 自动连播走的是就地换集，元素压根没换，不需要这里恢复。
function restoreFullscreen() {
  if (!resumeFullscreen) {
    return;
  }
  resumeFullscreen = false;
  var video = document.getElementById('player');
  if (!video) {
    return;
  }
  var request =
    video.requestFullscreen || video.webkitRequestFullscreen || video.webkitEnterFullscreen;
  if (!request) {
    return;
  }
  try {
    var result = request.call(video);
    if (result && typeof result.catch === 'function') {
      result.catch(function () {});
    }
  } catch (error) {
    // 没有用户手势时浏览器会拒绝，保持非全屏即可。
  }
}

// 黑屏看门狗：部分线路的 TS 把 H.265 藏在私有流（PMT stream_type=0x06）里，
// 浏览器 MSE 认不出可解码的视频轨，表现为"进度条在走、画面全黑"。
// 检测到时间推进但 videoWidth 始终为 0 时，同样走自动换线。
function startBlackScreenWatchdog(video, token) {
  var lastTime = -1;
  var advancing = 0;
  var timer = setInterval(function () {
    if (token !== mountToken) {
      clearInterval(timer);
      return;
    }
    if (video.paused && !hlsInstance) {
      return;
    }
    if (video.videoWidth > 0) {
      clearInterval(timer); // 画面已出来，看门狗使命完成
      return;
    }
    if (video.currentTime > lastTime + 1) {
      advancing += 1;
    } else {
      advancing = 0;
    }
    lastTime = video.currentTime;
    if (advancing >= 3) {
      clearInterval(timer);
      showNotice('该线路画面无法解码（可能是浏览器不支持的视频编码），正在自动切换线路…', 'switching');
      failoverRoute('视频编码不支持');
    }
  }, 3000);
  if (blackWatchdog) {
    clearInterval(blackWatchdog);
  }
  blackWatchdog = timer;
}

function mountVideo(url, resumeAt) {
  var video = document.getElementById('player');
  if (!video) {
    return;
  }
  if (hlsInstance) {
    hlsInstance.destroy();
    hlsInstance = null;
  }
  if (!video.dataset.bound) {
    video.dataset.bound = '1';
    bindPlayerEvents(video);
  }
  var token = ++mountToken;
  video.onerror = function () {
    cancelAutoNext();
    // 加载失败先自动换线；hls.js 路径下它的 fatal 回调同样走 failoverRoute，
    // 内部有并发锁与已试线路去重，不会重复触发。
    failoverRoute('视频加载失败');
  };
  var isPlaylist = /\.m3u8($|\?)/i.test(url);
  var mediaRecoveries = 0;
  if (isPlaylist && window.Hls && window.Hls.isSupported()) {
    hlsInstance = new window.Hls({
      enableWorker: true,
      manifestLoadPolicy: {
        default: {
          maxTimeToFirstByteMs: 60000,
          maxLoadTimeMs: 120000,
          // 重试次数收敛：线路 403/限流属于"重试也不会好"的错误，
          // 快速判死交给自动换线，避免用户干等一两分钟。
          timeoutRetry: { maxNumRetry: 2, retryDelayMs: 500, maxRetryDelayMs: 2000, backoff: 'linear' },
          errorRetry: { maxNumRetry: 2, retryDelayMs: 800, maxRetryDelayMs: 3000, backoff: 'linear' },
        },
      },
    });
    hlsInstance.on(window.Hls.Events.ERROR, function (event, data) {
      if (!data || !data.fatal) {
        return;
      }
      if (token !== mountToken) {
        return; // 旧实例的迟到回调，忽略
      }
      // 播放追上转码进度时会缓冲停滞，这属于正常现象，恢复播放等转码跟上即可。
      if (data.type === window.Hls.ErrorTypes.MEDIA_ERROR && mediaRecoveries < 4) {
        mediaRecoveries += 1;
        showNotice('缓冲中…服务端转码需要一点时间，会自动继续播放。', 'transcode');
        hlsInstance.recoverMediaError();
        return;
      }
      // 致命网络错误（含 manifestLoadError）或已耗尽媒体恢复次数：自动切换线路。
      var reason = data.details || data.type || '未知错误';
      if (data.type === window.Hls.ErrorTypes.MEDIA_ERROR) {
        reason = '画面解码失败';
      }
      failoverRoute(reason);
    });
    hlsInstance.loadSource(url);
    hlsInstance.attachMedia(video);
  } else {
    video.src = url;
  }
  applyPlaybackRate(video);
  video.play().catch(function (error) {
    // 浏览器可能因为自动播放策略拦截首次播放，提示用户手动点一下。
    if (error && error.name === 'NotAllowedError') {
      showNotice('浏览器阻止了自动播放，请点击播放按钮开始。');
    }
  });
  // 续播：有播放记录时跳回上次看到的位置。
  if (resumeAt > 0) {
    video.addEventListener(
      'loadedmetadata',
      function () {
        if (token !== mountToken) {
          return;
        }
        if (video.duration && resumeAt > video.duration - 5) {
          return;
        }
        try {
          video.currentTime = resumeAt;
          showNotice('已从上次进度 ' + formatTime(resumeAt) + ' 继续播放。', 'resume');
          // 播放真正开始由 playing 事件清掉它；万一用户一直不点播放，5 秒后也自动收起。
          setTimeout(function () {
            if (token !== mountToken) {
              return;
            }
            var box = document.getElementById('play-note');
            if (box && box.getAttribute('data-kind') === 'resume') {
              box.style.display = 'none';
            }
          }, 5000);
        } catch (error) {
          // 部分流不允许跳转，忽略即可。
        }
      },
      { once: true },
    );
  }
  // 浏览器拿不到视频帧但时间在走（HEVC/私有流），9 秒内自动换线。
  startBlackScreenWatchdog(video, token);
}

function optionHTML(value, label, selected) {
  return (
    '<option value="' + escapeHTML(String(value)) + '"' + (selected ? ' selected' : '') + '>' +
    escapeHTML(label) +
    '</option>'
  );
}

function renderPlay(id, index, source) {
  // 整页重建会换掉 <video>，先把倍速和全屏状态记下来。
  capturePlaybackState();
  flushProgress();
  view.innerHTML = '<div class="loading">正在解析播放地址…</div>';
  state.index = index;
  return ensureDrama(id, source)
    .then(function () {
      return apiPlay({
        drama: state.drama,
        chapter: (state.chapters || [])[index],
        index: index,
        quality: state.quality || 0,
        route: state.route || 0,
      });
    })
    .then(function (plan) {
      state.plan = plan;
      resetRouteTried(plan.routeIndex || 0, plan.routeCount || 1);
      var qualities = plan.qualities || [];
      var current = plan.quality || 0;
      if (qualities.indexOf(current) === -1 && qualities.length) {
        current = qualities[0];
      }
      var qualitySelect = qualities.length
        ? '<label>画质 <select id="quality-select">' +
          qualities
            .map(function (item) {
              return optionHTML(item, item + 'P', item === current);
            })
            .join('') +
          '</select></label>'
        : '';
      var routeTotal = plan.routeCount || 1;
      var routeSelect =
        '<label>线路 <select id="route-select">' +
        Array.apply(null, new Array(routeTotal))
          .map(function (unused, position) {
            return optionHTML(position, '线路 ' + (position + 1) + '/' + routeTotal, position === (plan.routeIndex || 0));
          })
          .join('') +
        '</select></label>';
      var suffix = (state.drama.source ? '&source=' + encodeURIComponent(state.drama.source) : '');
      var hasNextEp = index + 1 < (state.chapters || []).length;
      var hasPrevEp = index > 0;
      // 用按钮而不是超链接：点击走就地换集（playNextEpisode / playPreviousEpisode），
      // 复用同一个 <video>，倍速和全屏都不会被重置。
      var next = hasNextEp
        ? '<button type="button" class="btn" id="episode-next">下一集</button>'
        : '';
      var previous = hasPrevEp
        ? '<button type="button" class="btn" id="episode-previous">上一集</button>'
        : '';
      var favorite = isFavorite(state.drama)
        ? '<button type="button" class="btn active" id="fav-toggle">已收藏</button>'
        : '<button type="button" class="btn" id="fav-toggle">收藏</button>';
      var hasNext = index + 1 < (state.chapters || []).length;
      var autoNextBox = hasNext
        ? '<label class="toggle" title="播完自动跳到下一集">' +
          '<input type="checkbox" id="auto-next"' + (state.autoNext ? ' checked' : '') + '>自动连播</label>'
        : '';
      var message = '';
      var kind = '';
      if (plan.message === '需要 ffmpeg 转码' || (plan.mode === 'direct' && plan.encrypted)) {
        message = FFMPEG_HINT;
      } else if (plan.mode === 'direct' && plan.hevc) {
        message = HEVC_NOTICE;
      } else if (plan.transcoding) {
        message = '服务端正在转码，首次缓冲可能需要十几秒…';
        kind = 'transcode';
      } else if (plan.message) {
        message = plan.message;
      }
      view.innerHTML =
        '<div class="player-wrap">' +
        '<div class="player-title">' +
        escapeHTML(state.drama.title) + ' · ' + escapeHTML(((state.chapters || [])[index] || {}).title || '') +
        '</div>' +
        '<video id="player" controls autoplay playsinline></video>' +
        '<div class="notice" id="play-note" data-kind="' + kind + '"' +
        (message ? ' style="display:block"' : '') + '>' +
        escapeHTML(message) +
        '</div>' +
        '<div class="notice next-up" id="next-up"></div>' +
        '<div class="player-bar">' + qualitySelect + routeSelect +
        '<span class="spacer"></span>' +
        autoNextBox +
        '<a class="btn" href="#/drama?id=' + encodeURIComponent(id) + suffix + '">返回详情</a>' +
        favorite +
        previous +
        next +
        '</div></div>';
      var resumeAt = 0;
      var remembered = findEntry(state.library.history, entryKey(state.drama), index);
      if (remembered && remembered.position > 5) {
        resumeAt = remembered.position;
      }
      mountVideo(plan.url, resumeAt);
      bindFavoriteToggle(state.drama);
      // 打开播放页就先记一条播放记录，之后的进度由播放器持续更新。
      libraryRequest({ list: 'history', op: 'put', item: currentEntry(resumeAt, 0) }).catch(function () {});
      var qualityBox = document.getElementById('quality-select');
      if (qualityBox) {
        qualityBox.addEventListener('change', function () {
          state.quality = Number(qualityBox.value);
          state.route = 0;
          renderPlay(id, index, source);
        });
      }
      var routeBox = document.getElementById('route-select');
      if (routeBox) {
        routeBox.addEventListener('change', function () {
          state.route = Number(routeBox.value);
          renderPlay(id, index, source);
        });
      }
      var autoNextToggle = document.getElementById('auto-next');
      if (autoNextToggle) {
        autoNextToggle.addEventListener('change', function () {
          state.autoNext = autoNextToggle.checked;
          writeAutoNext(state.autoNext);
          if (!state.autoNext) {
            cancelAutoNext();
          }
        });
      }
      var nextButton = document.getElementById('episode-next');
      if (nextButton) {
        nextButton.addEventListener('click', function () {
          playNextEpisode();
        });
      }
      var previousButton = document.getElementById('episode-previous');
      if (previousButton) {
        previousButton.addEventListener('click', function () {
          playPreviousEpisode();
        });
      }
      // 整页重建（换画质/线路）后，若进入本页前处于全屏，重新进全屏。
      // 就地换集（下一集/上一集/自动连播）不会走这里，全屏由 <video> 保留。
      restoreFullscreen();
    });
}

function libraryCardHTML(entry, tab) {
  var key = entryKey(entry);
  var suffix = entry.source ? '&source=' + encodeURIComponent(entry.source) : '';
  var href = tab === 'history'
    ? '#/play?id=' + encodeURIComponent(entry.id) + '&index=' + Number(entry.index || 0) + suffix
    : '#/drama?id=' + encodeURIComponent(entry.id) + suffix;
  var second = tab === 'history'
    ? progressText(entry)
    : (entry.episodes ? entry.episodes + ' 集' : '点击查看剧集');
  var bar = tab === 'history'
    ? '<div class="progress"><span style="width:' + progressPercent(entry) + '%"></span></div>'
    : '';
  var checked = state.libraryEditing && state.librarySelected[key] ? ' checked' : '';
  var check = state.libraryEditing
    ? '<label class="lib-check"><input type="checkbox" data-check="' +
      escapeHTML(key) + '"' + checked + ' /></label>'
    : '';
  return (
    '<div class="card lib-card">' +
    check +
    '<button type="button" class="lib-remove" data-remove="' + escapeHTML(key) + '" title="删除">×</button>' +
    '<a class="lib-link" href="' + escapeHTML(href) + '">' +
    coverHTML(entry) +
    '<div class="title">' + escapeHTML(entry.title || '未命名') + '</div>' +
    '<div class="meta">' + escapeHTML(entry.sourceName || sourceLabel(entry.source) || '未知站源') + '</div>' +
    '<div class="meta">' + escapeHTML(second) + '</div>' +
    bar +
    '</a></div>'
  );
}

function renderFavoritesView() {
  var tab = state.libraryTab === 'favorites' ? 'favorites' : 'history';
  var items = tab === 'history' ? state.library.history : state.library.favorites;
  var selectedKeys = Object.keys(state.librarySelected).filter(function (key) {
    return state.librarySelected[key];
  });
  var toolbar = state.libraryEditing
    ? '<div class="lib-toolbar">' +
      '<label class="lib-all"><input type="checkbox" id="lib-all"' +
      (selectedKeys.length && selectedKeys.length === items.length ? ' checked' : '') +
      ' /> 全选</label>' +
      '<button type="button" class="btn" id="lib-delete">删除选中（' + selectedKeys.length + '）</button>' +
      '<button type="button" class="btn danger" id="lib-clear">清空全部</button>' +
      '<button type="button" class="btn" id="lib-done">完成</button>' +
      '</div>'
    : '';
  var cards = items.map(function (entry) {
    return libraryCardHTML(entry, tab);
  }).join('');
  if (!cards) {
    cards = '<div class="empty">' +
      (tab === 'history' ? '还没有播放记录，看过的剧会自动出现在这里。' : '还没有收藏，可在播放页点「收藏」加入。') +
      '</div>';
  }
  var editButton = items.length
    ? '<button type="button" class="btn" id="lib-edit">' + (state.libraryEditing ? '退出编辑' : '编辑') + '</button>'
    : '';
  view.innerHTML =
    '<div class="lib">' +
    '<div class="lib-tabs">' +
    '<button type="button" class="lib-tab' + (tab === 'history' ? ' active' : '') +
    '" data-tab="history">播放记录（' + state.library.history.length + '）</button>' +
    '<button type="button" class="lib-tab' + (tab === 'favorites' ? ' active' : '') +
    '" data-tab="favorites">收藏（' + state.library.favorites.length + '）</button>' +
    '<span class="spacer"></span>' + editButton +
    '</div>' +
    toolbar +
    '<div class="grid">' + cards + '</div>' +
    '</div>';
  bindLibraryView();
}

function libraryMutate(list, op, keys) {
  return libraryRequest({ list: list, op: op, keys: keys || [] }).then(function () {
    state.librarySelected = {};
    renderFavoritesView();
  });
}

function bindLibraryView() {
  var tabs = document.querySelectorAll('.lib-tab');
  Array.prototype.forEach.call(tabs, function (button) {
    button.addEventListener('click', function () {
      state.libraryTab = button.getAttribute('data-tab');
      state.librarySelected = {};
      renderFavoritesView();
    });
  });
  var editButton = document.getElementById('lib-edit');
  if (editButton) {
    editButton.addEventListener('click', function () {
      state.libraryEditing = !state.libraryEditing;
      state.librarySelected = {};
      renderFavoritesView();
    });
  }
  var doneButton = document.getElementById('lib-done');
  if (doneButton) {
    doneButton.addEventListener('click', function () {
      state.libraryEditing = false;
      state.librarySelected = {};
      renderFavoritesView();
    });
  }
  var allBox = document.getElementById('lib-all');
  if (allBox) {
    allBox.addEventListener('change', function () {
      var list = state.libraryTab === 'favorites' ? state.library.favorites : state.library.history;
      state.librarySelected = {};
      if (allBox.checked) {
        list.forEach(function (entry) {
          state.librarySelected[entryKey(entry)] = true;
        });
      }
      renderFavoritesView();
    });
  }
  Array.prototype.forEach.call(document.querySelectorAll('[data-check]'), function (box) {
    box.addEventListener('change', function () {
      state.librarySelected[box.getAttribute('data-check')] = box.checked;
      renderFavoritesView();
    });
  });
  Array.prototype.forEach.call(document.querySelectorAll('[data-remove]'), function (button) {
    button.addEventListener('click', function (event) {
      event.preventDefault();
      libraryMutate(state.libraryTab, 'remove', [button.getAttribute('data-remove')]);
    });
  });
  var deleteButton = document.getElementById('lib-delete');
  if (deleteButton) {
    deleteButton.addEventListener('click', function () {
      var keys = Object.keys(state.librarySelected).filter(function (key) {
        return state.librarySelected[key];
      });
      if (!keys.length) {
        return;
      }
      libraryMutate(state.libraryTab, 'remove', keys);
    });
  }
  var clearButton = document.getElementById('lib-clear');
  if (clearButton) {
    clearButton.addEventListener('click', function () {
      var label = state.libraryTab === 'history' ? '播放记录' : '收藏';
      if (!window.confirm('确定清空全部' + label + '？此操作不可撤销。')) {
        return;
      }
      libraryMutate(state.libraryTab, 'clear', []);
    });
  }
}

function renderFavorites(tab) {
  if (tab) {
    state.libraryTab = tab;
  }
  view.innerHTML = '<div class="loading">正在加载收藏夹…</div>';
  return loadLibrary().then(function () {
    renderFavoritesView();
  });
}

// —— 自定义 maccms 源管理 ——

function customSourceList() {
  return fetch('/api/custom-sources')
    .then(function (response) {
      return response.json();
    })
    .then(function (data) {
      return (data && data.items) || [];
    });
}

function customSourceSave(record, editing) {
  return fetch('/api/custom-sources' + (editing ? '/' + encodeURIComponent(record.id) : ''), {
    method: editing ? 'PUT' : 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name: record.name, base: record.base }),
  }).then(function (response) {
    return response.json().then(function (data) {
      if (!data || !data.id) {
        throw new Error(data && data.error ? data.error : '保存自定义源失败');
      }
      return data;
    });
  });
}

function customSourceRemove(id) {
  return fetch('/api/custom-sources/' + encodeURIComponent(id), { method: 'DELETE' })
    .then(function (response) {
      return response.json();
    })
    .then(function (data) {
      if (!data || data.ok !== true) {
        throw new Error(data && data.error ? data.error : '删除自定义源失败');
      }
      return data;
    });
}

function renderCustomSources() {
  return customSourceList().then(function (items) {
    var rows = items.map(function (item) {
      return (
        '<div class="lib-tab custom-row" style="display:flex;align-items:center;justify-content:space-between;border:1px solid var(--border);border-radius:8px;padding:10px 14px;margin:6px 0;cursor:default">' +
        '<div style="min-width:0">' +
        '<div style="font-weight:600">' + escapeHTML(item.name || '') + '</div>' +
        '<div style="font-size:12px;opacity:0.7;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">' +
        escapeHTML(item.base || '') + '</div>' +
        '</div>' +
        '<div style="display:flex;gap:6px;flex:none">' +
        '<button type="button" class="btn" data-edit="' + escapeHTML(item.id) + '">编辑</button>' +
        '<button type="button" class="btn danger" data-remove="' + escapeHTML(item.id) + '" data-name="' +
        escapeHTML(item.name || '') + '">删除</button>' +
        '</div>' +
        '</div>'
      );
    }).join('');
    if (!rows) {
      rows = '<div class="empty">还没有自定义源。填入符合 MacCMS 规范的站点名称与网址，即可自动适配列表与播放。</div>';
    }
    view.innerHTML =
      '<div class="lib">' +
      '<div class="lib-tabs">' +
      '<span class="lib-tab active">自定义源（' + items.length + '）</span>' +
      '<span class="spacer"></span>' +
      '<button type="button" class="btn" id="custom-add">新增</button>' +
      '</div>' +
      '<div style="font-size:12px;opacity:0.7;padding:4px 2px 8px">' +
      '自定义源读取网址并判断是否符合 MacCMS 规范；符合后套用通用规则自动完成列表、搜索与播放。</div>' +
      rows +
      '</div>';
    bindCustomSourcesView(items);
  });
}

function bindCustomSourcesView(items) {
  var addButton = document.getElementById('custom-add');
  if (addButton) {
    addButton.addEventListener('click', function () {
      openCustomSourceEditor(null);
    });
  }
  Array.prototype.forEach.call(view.querySelectorAll('[data-edit]'), function (button) {
    button.addEventListener('click', function () {
      var id = button.getAttribute('data-edit');
      var item = items.filter(function (entry) { return entry.id === id; })[0];
      if (item) {
        openCustomSourceEditor(item);
      }
    });
  });
  Array.prototype.forEach.call(view.querySelectorAll('[data-remove]'), function (button) {
    button.addEventListener('click', function () {
      var id = button.getAttribute('data-remove');
      var name = button.getAttribute('data-name') || '';
      var confirmed = window.confirm('确定删除「' + name + '」吗？已加载的剧集仍会保留在浏览器本地。');
      if (!confirmed) {
        return;
      }
      customSourceRemove(id)
        .then(function () {
          return Promise.all([customSourceList(), loadSources()]);
        })
        .then(function (results) {
          state.sourceNames = {};
          (results[0] || []).forEach(function (item) {
            state.sourceNames[item.id] = item.name;
          });
          renderCustomSources();
        })
        .catch(function (error) {
          window.alert(error.message || '删除自定义源失败');
        });
    });
  });
}

function openCustomSourceEditor(existing) {
  var overlay = document.createElement('div');
  overlay.style.cssText =
    'position:fixed;inset:0;background:rgba(0,0,0,0.55);z-index:1000;display:flex;align-items:center;justify-content:center';
  var title = existing ? '编辑自定义源' : '新增自定义源';
  overlay.innerHTML =
    '<div style="background:var(--panel,#1e2430);border:1px solid var(--border,#333);border-radius:12px;' +
    'width:min(440px,92vw);padding:20px;box-sizing:border-box">' +
    '<h3 style="margin:0 0 14px">' + title + '</h3>' +
    '<label style="display:block;font-size:12px;opacity:0.8;margin-bottom:4px">源名称</label>' +
    '<input id="cs-name" class="search" type="text" maxlength="40" placeholder="例如：我的影院" ' +
    'style="width:100%;margin-bottom:12px" value="' + escapeHTML(existing ? existing.name : '') + '" />' +
    '<label style="display:block;font-size:12px;opacity:0.8;margin-bottom:4px">源网址</label>' +
    '<input id="cs-base" class="search" type="url" placeholder="https://example.com" ' +
    'style="width:100%" value="' + escapeHTML(existing ? existing.base : '') + '" />' +
    '<div id="cs-error" style="color:#ff8b90;font-size:12px;min-height:18px;margin-top:8px"></div>' +
    '<div style="display:flex;justify-content:flex-end;gap:8px;margin-top:6px">' +
    '<button type="button" class="btn" id="cs-cancel">取消</button>' +
    '<button type="button" class="btn" id="cs-save">保存</button>' +
    '</div>' +
    '</div>';
  document.body.appendChild(overlay);
  var nameInput = document.getElementById('cs-name');
  var baseInput = document.getElementById('cs-base');
  var errorBox = document.getElementById('cs-error');
  var saveButton = document.getElementById('cs-save');
  var cancelButton = document.getElementById('cs-cancel');
  if (nameInput) nameInput.focus();
  function close() {
    document.body.removeChild(overlay);
  }
  if (cancelButton) {
    cancelButton.addEventListener('click', close);
  }
  if (saveButton) {
    saveButton.addEventListener('click', function () {
      var name = (nameInput.value || '').trim();
      var base = (baseInput.value || '').trim();
      if (!name || !base) {
        errorBox.textContent = '源名称与源网址都需要填写';
        return;
      }
      saveButton.disabled = true;
      customSourceSave({ id: existing ? existing.id : '', name: name, base: base }, !!existing)
        .then(function () {
          return Promise.all([customSourceList(), loadSources()]);
        })
        .then(function (results) {
          state.sourceNames = {};
          (results[0] || []).forEach(function (item) {
            state.sourceNames[item.id] = item.name;
          });
          close();
          renderCustomSources();
        })
        .catch(function (error) {
          errorBox.textContent = error.message || '保存自定义源失败';
          saveButton.disabled = false;
        });
    });
  }
}

function render() {
  cancelAutoNext();
  var current = hashParams();
  var params = current.params;
  state.source = params.get('source') || state.source;
  state.query = params.get('q') || '';
  if (sourceSelect.value !== state.source) {
    sourceSelect.value = state.source;
  }
  if (searchInput.value !== state.query) {
    searchInput.value = state.query;
  }
  var favoritesButton = document.getElementById('favorites-btn');
  if (favoritesButton) {
    favoritesButton.classList.toggle('active', current.path === 'favorites');
  }
  var customSourcesButton = document.getElementById('custom-sources-btn');
  if (customSourcesButton) {
    customSourcesButton.classList.toggle('active', current.path === 'custom-sources');
  }
  var task;
  if (current.path === 'drama') {
    task = renderDrama(params.get('id') || '', params.get('source') || '');
  } else if (current.path === 'play') {
    task = renderPlay(params.get('id') || '', Number(params.get('index') || 0), params.get('source') || '');
  } else if (current.path === 'favorites') {
    task = renderFavorites(params.get('tab') || '');
  } else if (current.path === 'custom-sources') {
    task = Promise.resolve().then(renderCustomSources);
  } else {
    state.category = params.get('category') || '';
    state.page = Number(params.get('page') || 1);
    task = renderCatalog();
  }
  task.catch(function (error) {
    view.innerHTML = '<div class="empty">' + escapeHTML(error.message || '加载失败') + '</div>';
  });
}

sourceSelect.addEventListener('change', function () {
  state.source = sourceSelect.value;
  state.category = '';
  state.page = 1;
  state.query = '';
  state.categories = [];
  location.hash = hashFor({});
});

searchForm.addEventListener('submit', function (event) {
  event.preventDefault();
  state.query = searchInput.value.trim();
  state.page = 1;
  state.category = '';
  location.hash = hashFor({});
});

window.addEventListener('hashchange', function () {
  // 就地换集时只改了地址栏（syncEpisodeChrome 会置位 suppressHashRender），
  // 这时不能再整页重建，否则刚复用的 <video> 会被销毁、倍速与全屏都丢。
  if (suppressHashRender) {
    suppressHashRender = false;
    return;
  }
  var current = hashParams();
  if (current.path !== 'play') {
    stopPlayback();
  } else if (Number(current.params.get('index') || 0) !== state.index) {
    // 换集时回到默认线路，画质沿用上次的选择。
    state.route = 0;
  }
  render();
});

// 关闭页面时用 sendBeacon 补一次进度，普通 fetch 在页面卸载时来不及发出。
window.addEventListener('beforeunload', function () {
  if (!state.drama || !navigator.sendBeacon) {
    return;
  }
  var video = document.getElementById('player');
  if (!video || !(Number(video.currentTime) > 0)) {
    return;
  }
  var body = JSON.stringify({
    list: 'history',
    op: 'put',
    item: currentEntry(Number(video.currentTime) || 0, Number(video.duration) || 0),
  });
  navigator.sendBeacon('/api/library', new Blob([body], { type: 'application/json' }));
});

loadInfo();
Promise.all([loadSources(), loadLibrary()]).then(function () {
  if (!location.hash) {
    location.hash = hashFor({});
  } else {
    render();
  }
});
