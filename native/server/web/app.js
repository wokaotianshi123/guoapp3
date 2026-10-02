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
};

var hlsInstance = null;

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
    })
    .catch(function () {});
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
      '<div class="chips">' + chips + '</div>' +
      '<div class="grid">' + cards + '</div>' +
      '<div class="pager">' + previous + '<span>第 ' + state.page + ' 页</span>' + next + '</div>';
  });
}

function ensureDrama(id) {
  if (state.drama && state.drama.id === id) {
    return Promise.resolve(state.drama);
  }
  return api({ action: 'detail', drama: { id: id } }).then(function (data) {
    state.drama = data.drama;
    state.chapters = data.chapters || [];
    return state.drama;
  });
}

function renderDrama(id) {
  view.innerHTML = '<div class="loading">正在加载剧集详情…</div>';
  return ensureDrama(id).then(function (drama) {
    var episodes = (state.chapters || [])
      .map(function (chapter, index) {
        return (
          '<a href="#/play?id=' + encodeURIComponent(drama.id) + '&index=' + index + '">' +
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
      '</div></div>' +
      '<div class="episodes">' + episodes + '</div>';
  });
}

function stopPlayback() {
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

function mountVideo(url) {
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
}

function optionHTML(value, label, selected) {
  return (
    '<option value="' + escapeHTML(String(value)) + '"' + (selected ? ' selected' : '') + '>' +
    escapeHTML(label) +
    '</option>'
  );
}

function renderPlay(id, index) {
  view.innerHTML = '<div class="loading">正在解析播放地址…</div>';
  state.index = index;
  return ensureDrama(id)
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
      var next = index + 1 < (state.chapters || []).length
        ? '<a class="btn" href="#/play?id=' + encodeURIComponent(id) + '&index=' + (index + 1) + '">下一集</a>'
        : '';
      var previous = index > 0
        ? '<a class="btn" href="#/play?id=' + encodeURIComponent(id) + '&index=' + (index - 1) + '">上一集</a>'
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
        '<div class="player-bar">' + qualitySelect + routeSelect +
        '<span class="spacer"></span>' +
        '<a class="btn" href="#/drama?id=' + encodeURIComponent(id) + '">返回详情</a>' +
        previous +
        next +
        '</div></div>';
      mountVideo(plan.url);
      var qualityBox = document.getElementById('quality-select');
      if (qualityBox) {
        qualityBox.addEventListener('change', function () {
          state.quality = Number(qualityBox.value);
          state.route = 0;
          renderPlay(id, index);
        });
      }
      var routeBox = document.getElementById('route-select');
      if (routeBox) {
        routeBox.addEventListener('change', function () {
          state.route = Number(routeBox.value);
          renderPlay(id, index);
        });
      }
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
  var task;
  if (current.path === 'drama') {
    task = renderDrama(params.get('id') || '');
  } else if (current.path === 'play') {
    task = renderPlay(params.get('id') || '', Number(params.get('index') || 0));
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

loadInfo();
loadSources().then(function () {
  if (!location.hash) {
    location.hash = hashFor({});
  } else {
    render();
  }
});
