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

function coverURL(drama) {
  var cover = drama && drama.cover ? String(drama.cover) : '';
  if (!cover) {
    return '';
  }
  if (cover.indexOf('http://') === 0 || cover.indexOf('https://') === 0 || cover.indexOf('/') === 0) {
    return cover;
  }
  return '';
}

function coverHTML(drama) {
  var url = coverURL(drama);
  if (!url) {
    return '<div class="cover"><div class="fallback">' + escapeHTML(drama.title || '无封面') + '</div></div>';
  }
  return (
    '<div class="cover"><img loading="lazy" referrerpolicy="no-referrer" src="' +
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
  if (state.plan && state.plan.session) {
    api({ action: 'release', session: state.plan.session }).catch(function () {});
  }
  state.plan = null;
}

function mountVideo(url) {
  var video = document.getElementById('player');
  if (hlsInstance) {
    hlsInstance.destroy();
    hlsInstance = null;
  }
  if (url.indexOf('.m3u8') !== -1 && window.Hls && window.Hls.isSupported()) {
    hlsInstance = new window.Hls({ enableWorker: true });
    hlsInstance.loadSource(url);
    hlsInstance.attachMedia(video);
  } else {
    video.src = url;
  }
  video.play().catch(function () {});
}

function resolve(index, quality, route) {
  var chapter = (state.chapters || [])[index];
  var payload = {
    action: route === undefined ? 'resolve' : 'selectRoute',
    drama: state.drama,
    chapter: chapter,
    index: index,
    quality: quality || 0,
  };
  if (route === undefined) {
    payload.route = 0;
  } else {
    payload.session = state.plan ? state.plan.session : '';
    payload.route = route;
  }
  return api(payload);
}

function renderPlay(id, index) {
  view.innerHTML = '<div class="loading">正在解析播放地址…</div>';
  state.index = index;
  return ensureDrama(id)
    .then(function () {
      return resolve(index, 0, undefined);
    })
    .then(function (plan) {
      state.plan = plan;
      var qualities = plan.qualities || [];
      var qualityButtons = qualities.length
        ? qualities
            .map(function (item) {
              return '<button class="btn" data-quality="' + item + '">' + item + 'P</button>';
            })
            .join('')
        : '';
      var routes = plan.routeCount > 1
        ? '<button class="btn" id="route">换线路（' + (plan.routeIndex + 1) + '/' + plan.routeCount + '）</button>'
        : '';
      var next = index + 1 < (state.chapters || []).length
        ? '<a class="btn" href="#/play?id=' + encodeURIComponent(id) + '&index=' + (index + 1) + '">下一集</a>'
        : '';
      view.innerHTML =
        '<div class="player-wrap">' +
        '<div class="player-title">' +
        escapeHTML(state.drama.title) + ' · ' + escapeHTML(((state.chapters || [])[index] || {}).title || '') +
        '</div>' +
        '<video id="player" controls autoplay playsinline></video>' +
        '<div class="player-bar">' + qualityButtons + routes +
        '<span class="spacer"></span>' +
        '<a class="btn" href="#/drama?id=' + encodeURIComponent(id) + '">返回详情</a>' +
        next +
        '</div></div>';
      mountVideo(plan.url);
      Array.prototype.forEach.call(view.querySelectorAll('[data-quality]'), function (button) {
        button.addEventListener('click', function () {
          resolve(index, Number(button.getAttribute('data-quality')), undefined).then(function (updated) {
            state.plan = updated;
            mountVideo(updated.url);
          });
        });
      });
      var routeButton = document.getElementById('route');
      if (routeButton) {
        routeButton.addEventListener('click', function () {
          var current = state.plan && state.plan.routeIndex ? state.plan.routeIndex : 0;
          resolve(index, 0, (current + 1) % (state.plan.routeCount || 1)).then(function (updated) {
            state.plan = updated;
            mountVideo(updated.url);
          });
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
