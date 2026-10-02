'use strict';

var view = document.getElementById('view');
var sourceSelect = document.getElementById('source');
var searchInput = document.getElementById('search-input');
var searchForm = document.getElementById('search-form');

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
  library: { history: [], favorites: [] },
  libraryTab: 'history',
  libraryEditing: false,
  librarySelected: {},
};

var hlsInstance = null;
var lastProgressSave = 0;

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

function stopPlayback() {
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

function mountVideo(url, resumeAt) {
  var video = document.getElementById('player');
  if (hlsInstance) {
    hlsInstance.destroy();
    hlsInstance = null;
  }
  video.onerror = function () {
    showNotice('视频加载失败，可以换个线路或画质重试。');
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
          timeoutRetry: { maxNumRetry: 10, retryDelayMs: 1000, maxRetryDelayMs: 8000, backoff: 'linear' },
          errorRetry: { maxNumRetry: 10, retryDelayMs: 2000, maxRetryDelayMs: 10000, backoff: 'linear' },
        },
      },
    });
    hlsInstance.on(window.Hls.Events.ERROR, function (event, data) {
      if (!data || !data.fatal) {
        return;
      }
      // 播放追上转码进度时会缓冲停滞，这属于正常现象，恢复播放等转码跟上即可。
      if (data.type === window.Hls.ErrorTypes.MEDIA_ERROR && mediaRecoveries < 4) {
        mediaRecoveries += 1;
        showNotice('缓冲中…服务端转码需要一点时间，会自动继续播放。', 'transcode');
        hlsInstance.recoverMediaError();
        return;
      }
      showNotice('播放失败：' + (data.details || data.type || '未知错误'));
    });
    hlsInstance.loadSource(url);
    hlsInstance.attachMedia(video);
  } else {
    video.src = url;
  }
  video.play().catch(function (error) {
    // 浏览器可能因为自动播放策略拦截首次播放，提示用户手动点一下。
    if (error && error.name === 'NotAllowedError') {
      showNotice('浏览器阻止了自动播放，请点击播放按钮开始。');
    }
  });
  // 开始播放后撤掉“正在转码”的提示。
  video.addEventListener('playing', function () {
    var box = document.getElementById('play-note');
    if (box && box.getAttribute('data-kind') === 'transcode') {
      box.style.display = 'none';
    }
  });
  // 续播：有播放记录时跳回上次看到的位置。
  if (resumeAt > 0) {
    var resumed = false;
    video.addEventListener('loadedmetadata', function () {
      if (resumed) {
        return;
      }
      resumed = true;
      if (video.duration && resumeAt > video.duration - 5) {
        return;
      }
      try {
        video.currentTime = resumeAt;
        showNotice('已从上次进度 ' + formatTime(resumeAt) + ' 继续播放。');
      } catch (error) {
        // 部分流不允许跳转，忽略即可。
      }
    });
  }
  video.addEventListener('timeupdate', function () {
    reportProgress(false);
  });
  video.addEventListener('pause', function () {
    reportProgress(true);
  });
  video.addEventListener('ended', function () {
    reportProgress(true);
  });
}

function optionHTML(value, label, selected) {
  return (
    '<option value="' + escapeHTML(String(value)) + '"' + (selected ? ' selected' : '') + '>' +
    escapeHTML(label) +
    '</option>'
  );
}

function renderPlay(id, index, source) {
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
      var next = index + 1 < (state.chapters || []).length
        ? '<a class="btn" href="#/play?id=' + encodeURIComponent(id) + '&index=' + (index + 1) + suffix + '">下一集</a>'
        : '';
      var previous = index > 0
        ? '<a class="btn" href="#/play?id=' + encodeURIComponent(id) + '&index=' + (index - 1) + suffix + '">上一集</a>'
        : '';
      var favorite = isFavorite(state.drama)
        ? '<button type="button" class="btn active" id="fav-toggle">已收藏</button>'
        : '<button type="button" class="btn" id="fav-toggle">收藏</button>';
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
        '<div class="player-bar">' + qualitySelect + routeSelect +
        '<span class="spacer"></span>' +
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

function render() {
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
  var task;
  if (current.path === 'drama') {
    task = renderDrama(params.get('id') || '', params.get('source') || '');
  } else if (current.path === 'play') {
    task = renderPlay(params.get('id') || '', Number(params.get('index') || 0), params.get('source') || '');
  } else if (current.path === 'favorites') {
    task = renderFavorites(params.get('tab') || '');
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
