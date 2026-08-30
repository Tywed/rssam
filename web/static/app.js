(function () {
  'use strict';

  var PREFS_KEY = 'rssam.prefs';
  var defaults = {
    feedsWidth: 260,
    headlinesWidth: 380,
    headlinesHeight: 45,
    viewMode: 'horizontal',
    feedsCollapsed: false,
    theme: 'light',
    entrySort: 'newest'
  };

  function loadPrefs() {
    try {
      var raw = localStorage.getItem(PREFS_KEY);
      if (!raw) return Object.assign({}, defaults);
      return Object.assign({}, defaults, JSON.parse(raw));
    } catch (_) {
      return Object.assign({}, defaults);
    }
  }

  function savePrefs(prefs) {
    try {
      localStorage.setItem(PREFS_KEY, JSON.stringify(prefs));
    } catch (_) {}
  }

  function clamp(n, min, max) {
    return Math.max(min, Math.min(max, n));
  }

  function isMobile() {
    return window.matchMedia('(max-width: 900px)').matches;
  }

  function applyTheme(theme) {
    document.documentElement.setAttribute('data-theme', theme === 'dark' ? 'dark' : 'light');
    var btn = document.getElementById('theme-toggle');
    if (btn) btn.textContent = theme === 'dark' ? '☀' : '🌙';
  }

  function applyPrefs(prefs) {
    var root = document.documentElement;
    root.style.setProperty('--feeds-w', prefs.feedsWidth + 'px');
    root.style.setProperty('--headlines-w', prefs.headlinesWidth + 'px');
    root.style.setProperty('--headlines-h', prefs.headlinesHeight + '%');
    applyTheme(prefs.theme || 'light');

    var shell = document.getElementById('app-shell');
    if (!shell) return;

    shell.classList.remove('view-horizontal', 'view-vertical', 'feeds-collapsed', 'feeds-open');
    shell.classList.add(prefs.viewMode === 'vertical' ? 'view-vertical' : 'view-horizontal');
    if (prefs.feedsCollapsed && !isMobile()) {
      shell.classList.add('feeds-collapsed');
    }

    document.querySelectorAll('.view-mode-btn').forEach(function (btn) {
      btn.classList.toggle('active', btn.getAttribute('data-view-mode') === prefs.viewMode);
    });
    document.querySelectorAll('.entry-sort-btn').forEach(function (btn) {
      btn.classList.toggle('active', btn.getAttribute('data-entry-sort') === (prefs.entrySort || 'newest'));
    });
  }

  function initViewMode(prefs) {
    document.querySelectorAll('.view-mode-btn').forEach(function (btn) {
      btn.addEventListener('click', function () {
        prefs.viewMode = btn.getAttribute('data-view-mode') || 'horizontal';
        savePrefs(prefs);
        applyPrefs(prefs);
      });
    });
  }

  function initThemeToggle(prefs) {
    var btn = document.getElementById('theme-toggle');
    if (!btn) return;
    btn.addEventListener('click', function () {
      prefs.theme = prefs.theme === 'dark' ? 'light' : 'dark';
      savePrefs(prefs);
      applyPrefs(prefs);
    });
  }

  function isReaderPage() {
    var path = window.location.pathname || '';
    return path === '/ui/unread' || path === '/ui/search';
  }

  function syncEntrySortFromURL(prefs) {
    if (!isReaderPage()) return;
    var params = new URLSearchParams(window.location.search);
    prefs.entrySort = params.get('sort') === 'oldest' ? 'oldest' : 'newest';
    savePrefs(prefs);
  }

  function navigateWithEntrySort(sort) {
    if (!isReaderPage()) return;
    var url = new URL(window.location.href);
    if (sort === 'oldest') {
      url.searchParams.set('sort', 'oldest');
    } else {
      url.searchParams.delete('sort');
    }
    url.searchParams.delete('offset');
    window.location.href = url.toString();
  }

  function initEntrySort(prefs) {
    syncEntrySortFromURL(prefs);
    applyPrefs(prefs);
    document.querySelectorAll('.entry-sort-btn').forEach(function (btn) {
      btn.addEventListener('click', function () {
        var sort = btn.getAttribute('data-entry-sort') || 'newest';
        if ((prefs.entrySort || 'newest') === sort) return;
        prefs.entrySort = sort;
        savePrefs(prefs);
        navigateWithEntrySort(sort);
      });
    });
  }

  function initSplitters(prefs) {
    var shell = document.getElementById('app-shell');
    if (!shell || !shell.classList.contains('mode-reader')) return;

    document.querySelectorAll('.splitter-v').forEach(function (splitter) {
      splitter.addEventListener('mousedown', function (ev) {
        if (ev.button !== 0) return;
        ev.preventDefault();
        var panel = splitter.getAttribute('data-panel');
        var vertical = prefs.viewMode === 'vertical' && panel === 'headlines';
        splitter.classList.add('dragging');
        document.body.style.cursor = vertical ? 'row-resize' : 'col-resize';
        document.body.style.userSelect = 'none';

        function onMove(e) {
          if (panel === 'feeds') {
            prefs.feedsWidth = clamp(e.clientX, 180, Math.min(480, window.innerWidth * 0.45));
            prefs.feedsCollapsed = false;
            document.documentElement.style.setProperty('--feeds-w', prefs.feedsWidth + 'px');
            shell.classList.remove('feeds-collapsed');
          } else if (panel === 'headlines') {
            if (vertical) {
              var shellRect = shell.getBoundingClientRect();
              var pct = ((e.clientY - shellRect.top) / shellRect.height) * 100;
              prefs.headlinesHeight = clamp(pct, 20, 75);
              document.documentElement.style.setProperty('--headlines-h', prefs.headlinesHeight + '%');
            } else {
              var feedsW = shell.classList.contains('feeds-collapsed') ? 0 : prefs.feedsWidth + 5;
              prefs.headlinesWidth = clamp(e.clientX - feedsW, 240, window.innerWidth - feedsW - 200);
              document.documentElement.style.setProperty('--headlines-w', prefs.headlinesWidth + 'px');
            }
          }
        }

        function onUp() {
          splitter.classList.remove('dragging');
          document.body.style.cursor = '';
          document.body.style.userSelect = '';
          document.removeEventListener('mousemove', onMove);
          document.removeEventListener('mouseup', onUp);
          savePrefs(prefs);
        }

        document.addEventListener('mousemove', onMove);
        document.addEventListener('mouseup', onUp);
      });
    });
  }

  function initFeedsToggle(prefs) {
    var shell = document.getElementById('app-shell');
    var toggle = document.getElementById('feeds-toggle');
    var backdrop = document.getElementById('feeds-backdrop');
    if (!shell || !toggle) return;

    function closeMobile() {
      shell.classList.remove('feeds-open');
      if (backdrop) backdrop.hidden = true;
    }

    toggle.addEventListener('click', function () {
      if (isMobile()) {
        if (shell.classList.contains('feeds-open')) {
          closeMobile();
        } else {
          shell.classList.add('feeds-open');
          if (backdrop) backdrop.hidden = false;
        }
        return;
      }
      prefs.feedsCollapsed = !prefs.feedsCollapsed;
      savePrefs(prefs);
      applyPrefs(prefs);
    });

    if (backdrop) backdrop.addEventListener('click', closeMobile);
  }

  function initEntryPreview() {
    var content = document.getElementById('panel-content');
    if (!content) return;

    window.rssamSelectEntry = function (row) {
      if (!row) return;
      var link = row.querySelector('.hl-link');
      if (!link) return;
      var entryID = row.getAttribute('data-entry-id');
      if (!entryID) return;

      document.querySelectorAll('.hl-row.active').forEach(function (el) { el.classList.remove('active'); });
      row.classList.add('active');
      row.scrollIntoView({ block: 'nearest' });

      content.innerHTML = '<div class="content-placeholder"><p>Загрузка…</p></div>';
      fetch('/ui/entries/' + entryID + '/preview', { credentials: 'same-origin', headers: { Accept: 'text/html' } })
        .then(function (r) {
          if (!r.ok) throw new Error('load failed');
          return r.text();
        })
        .then(function (html) {
          if (!html || !html.trim()) throw new Error('empty preview');
          content.innerHTML = html;
          var url = link.getAttribute('href');
          if (url && window.history && window.history.replaceState) {
            window.history.replaceState(null, '', url);
          }
        })
        .catch(function () {
          window.location.href = link.getAttribute('href');
        });
    };

    document.addEventListener('click', function (ev) {
      if (ev.target.closest('.hl-actions')) return;

      var link = ev.target.closest('.hl-link');
      if (!link) return;
      var row = link.closest('.hl-row');
      if (!row) return;

      ev.preventDefault();
      window.rssamSelectEntry(row);
    });
  }

  function getCSRFToken() {
    var inp = document.querySelector('input[name="csrf_token"]');
    return inp ? inp.value : '';
  }

  function entryRows() {
    return Array.from(document.querySelectorAll('#entry-list .hl-row:not(.hl-empty)'));
  }

  function feedNavLinks() {
    return Array.from(document.querySelectorAll('.feeds-tree a.tree-feed'));
  }

  function activeEntryIndex(rows) {
    var active = document.querySelector('#entry-list .hl-row.active');
    if (active) return rows.indexOf(active);
    var id = new URLSearchParams(location.search).get('entry_id');
    if (id) {
      var idx = rows.findIndex(function (r) { return r.getAttribute('data-entry-id') === id; });
      if (idx >= 0) return idx;
    }
    return rows.length ? 0 : -1;
  }

  function navigateEntries(delta) {
    var rows = entryRows();
    if (!rows.length) return;
    var idx = activeEntryIndex(rows);
    if (idx < 0) idx = 0;
    idx = clamp(idx + delta, 0, rows.length - 1);
    if (window.rssamSelectEntry) window.rssamSelectEntry(rows[idx]);
  }

  function navigateFeeds(delta) {
    var links = feedNavLinks();
    if (!links.length) return;
    var feedID = new URLSearchParams(location.search).get('feed_id');
    var idx = 0;
    if (feedID) {
      var found = links.findIndex(function (a) {
        return new URL(a.href, location.origin).searchParams.get('feed_id') === feedID;
      });
      if (found >= 0) idx = found;
    }
    idx = clamp(idx + delta, 0, links.length - 1);
    window.location.href = links[idx].href;
  }

  function markActiveRead() {
    var row = document.querySelector('#entry-list .hl-row.active');
    if (!row || row.classList.contains('read')) return;
    var entryID = row.getAttribute('data-entry-id');
    if (!entryID) return;
    var token = getCSRFToken();
    if (!token) return;
    var body = new URLSearchParams();
    body.set('csrf_token', token);
    fetch('/ui/entries/' + entryID + '/read', {
      method: 'POST',
      body: body,
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'HX-Request': 'true' }
    })
      .then(function (r) { return r.text(); })
      .then(function (html) {
        if (html) row.outerHTML = html;
      })
      .catch(function () {});
  }

  function initKeyboardNav() {
    var shell = document.getElementById('app-shell');
    if (!shell || !shell.classList.contains('mode-reader')) return;

    document.addEventListener('keydown', function (ev) {
      var tag = ev.target && ev.target.tagName;
      if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || (ev.target && ev.target.isContentEditable)) {
        return;
      }
      if (ev.altKey || ev.ctrlKey || ev.metaKey) return;

      if (ev.key === 'ArrowDown' || ev.key === 'j') {
        ev.preventDefault();
        navigateEntries(1);
      } else if (ev.key === 'ArrowUp' || ev.key === 'k') {
        ev.preventDefault();
        navigateEntries(-1);
      } else if (ev.key === 'ArrowRight' || ev.key === 'n') {
        ev.preventDefault();
        navigateFeeds(1);
      } else if (ev.key === 'ArrowLeft' || ev.key === 'p') {
        ev.preventDefault();
        navigateFeeds(-1);
      } else if (ev.key === 'm' || ev.key === 'M') {
        ev.preventDefault();
        markActiveRead();
      } else if (ev.key === 'Enter') {
        var row = document.querySelector('#entry-list .hl-row.active');
        if (row && window.rssamSelectEntry) {
          ev.preventDefault();
          window.rssamSelectEntry(row);
        }
      }
    });
  }

  document.addEventListener('submit', function (e) {
    var form = e.target.closest ? e.target.closest('form') : e.target;
    if (!form || !form.hasAttribute('data-hx-post')) return;
    e.preventDefault();
    var url = form.getAttribute('action') || window.location.href;
    var body = new URLSearchParams(new FormData(form));
    fetch(url, {
      method: 'POST',
      body: body,
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'HX-Request': 'true' }
    })
      .then(function (r) { return r.text().then(function (t) { return { ok: r.ok, text: t, redirect: r.headers.get('HX-Redirect') }; }); })
      .then(function (res) {
        if (res.redirect) { window.location.href = res.redirect; return; }
        if (!res.ok) return;
        var target = form.getAttribute('data-hx-target');
        if (target) {
          var el = document.querySelector(target);
          if (el) el.outerHTML = res.text;
        }
      })
      .catch(function () { form.submit(); });
  });

  function connectWS() {
    var el = document.getElementById('ws-enabled');
    if (!el) return;
    var proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    var ws = new WebSocket(proto + '//' + location.host + '/ws/v1');
    ws.onmessage = function (ev) {
      try {
        var msg = JSON.parse(ev.data);
        if (msg.event === 'new_entry' && msg.data && msg.data.entry) {
          var list = document.getElementById('entry-list');
          if (!list) return;
          var row = document.createElement('li');
          row.className = 'hl-row';
          row.id = 'entry-' + msg.data.entry.id;
          row.setAttribute('data-entry-id', msg.data.entry.id);
          var title = msg.data.entry.title || '(без заголовка)';
          row.innerHTML = '<a class="hl-link" href="/ui/unread?entry_id=' + msg.data.entry.id + '"><div class="hl-title">' + escapeHtml(title) + '</div></a>';
          list.insertBefore(row, list.firstChild);
        }
      } catch (_) {}
    };
  }

  function escapeHtml(s) {
    return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
  }

  function initFeedForm() {
    var urlInput = document.getElementById('feed-url-input');
    if (!urlInput || urlInput.readOnly) return;

    var statusEl = document.getElementById('feed-type-status');
    var badgeEl = document.getElementById('feed-type-badge');
    var hintEl = document.getElementById('feed-type-hint');
    var errorEl = document.getElementById('feed-type-error');
    var titleInput = document.getElementById('feed-title-input');
    var submitBtn = document.getElementById('feed-submit-btn');
    var rutubeChannelEl = document.getElementById('rutube-channel-id');
    var vkSearchQueryEl = document.getElementById('vk-search-query');
    var dzenSearchQueryEl = document.getElementById('dzen-search-query');
    var smotrimBrandEl = document.getElementById('smotrim-brand-id');
    var tlsCheckbox = document.getElementById('feed-tls-insecure');
    var settingsPanels = Array.from(document.querySelectorAll('.feed-type-settings'));
    var detectTimer = null;
    var lastReq = 0;

    function showFeedType(feedType, data) {
      if (!statusEl) return;
      statusEl.hidden = false;
      if (badgeEl) badgeEl.textContent = (data && data.feed_type_label) || feedType || '';
      if (hintEl) {
        if (data && data.valid && data.title) {
          hintEl.textContent = 'Предложенное название: ' + data.title;
        } else if (data && data.valid) {
          hintEl.textContent = 'Тип определён по URL';
        } else {
          hintEl.textContent = '';
        }
      }
      if (errorEl) {
        if (data && data.error) {
          var errText = data.error;
          if (data.tls_cert_error) {
            errText += ' Включите «Не проверять сертификат» ниже — проверка повторится автоматически.';
          }
          errorEl.textContent = errText;
          errorEl.hidden = false;
        } else {
          errorEl.textContent = '';
          errorEl.hidden = true;
        }
      }
      settingsPanels.forEach(function (panel) {
        panel.hidden = panel.getAttribute('data-feed-type') !== feedType;
      });
      if (rutubeChannelEl) {
        rutubeChannelEl.textContent = (feedType === 'rutube' && data && data.channel_id)
          ? 'ID канала: ' + data.channel_id
          : '';
      }
      if (vkSearchQueryEl) {
        vkSearchQueryEl.textContent = (feedType === 'vk_search' && data && data.search_query)
          ? 'Поисковый запрос: ' + data.search_query
          : '';
      }
      if (dzenSearchQueryEl) {
        dzenSearchQueryEl.textContent = (feedType === 'dzen_news' && data && data.search_query)
          ? 'Поисковый запрос: ' + data.search_query
          : '';
      }
      if (smotrimBrandEl) {
        smotrimBrandEl.textContent = (feedType === 'smotrim' && data && data.brand_id)
          ? 'Brand ID: ' + data.brand_id
          : '';
      }
      if (titleInput && data && data.title && !titleInput.value.trim()) {
        titleInput.value = data.title;
      }
      if (submitBtn) {
        var allowTLSRetry = !!(data && data.tls_cert_error && tlsCheckbox && tlsCheckbox.checked);
        submitBtn.disabled = !!(data && data.valid === false && !allowTLSRetry);
      }
    }

    function resetFeedType() {
      if (statusEl) statusEl.hidden = true;
      settingsPanels.forEach(function (panel) { panel.hidden = true; });
      if (errorEl) { errorEl.hidden = true; errorEl.textContent = ''; }
      if (submitBtn) submitBtn.disabled = false;
    }

    function detectFeedType() {
      var url = urlInput.value.trim();
      if (!url) {
        resetFeedType();
        return;
      }
      var reqID = ++lastReq;
      var qs = 'url=' + encodeURIComponent(url);
      if (tlsCheckbox && tlsCheckbox.checked) {
        qs += '&tls_insecure=1';
      }
      fetch('/ui/feeds/detect-type?' + qs, { credentials: 'same-origin' })
        .then(function (r) { return r.json(); })
        .then(function (data) {
          if (reqID !== lastReq) return;
          showFeedType(data.feed_type || 'rss', data);
        })
        .catch(function () {
          if (reqID !== lastReq) return;
          resetFeedType();
        });
    }

    function scheduleDetect() {
      if (detectTimer) clearTimeout(detectTimer);
      detectTimer = setTimeout(detectFeedType, 400);
    }

    urlInput.addEventListener('input', scheduleDetect);
    urlInput.addEventListener('blur', detectFeedType);
    if (tlsCheckbox) tlsCheckbox.addEventListener('change', scheduleDetect);
    if (urlInput.value.trim()) detectFeedType();
  }

  function postFeedAction(url) {
    var token = getCSRFToken();
    if (!token) return;
    var form = document.createElement('form');
    form.method = 'POST';
    form.action = url;
    var inp = document.createElement('input');
    inp.type = 'hidden';
    inp.name = 'csrf_token';
    inp.value = token;
    form.appendChild(inp);
    document.body.appendChild(form);
    form.submit();
  }

  function initFeedContextMenu() {
    var menu = null;
    var activeFeedId = null;

    function closeMenu() {
      if (!menu) return;
      menu.hidden = true;
      activeFeedId = null;
    }

    function positionMenu(x, y) {
      menu.style.left = x + 'px';
      menu.style.top = y + 'px';
      menu.hidden = false;
      requestAnimationFrame(function () {
        var rect = menu.getBoundingClientRect();
        var left = x;
        var top = y;
        if (rect.right > window.innerWidth - 4) left = window.innerWidth - rect.width - 4;
        if (rect.bottom > window.innerHeight - 4) top = window.innerHeight - rect.height - 4;
        if (left < 4) left = 4;
        if (top < 4) top = 4;
        menu.style.left = left + 'px';
        menu.style.top = top + 'px';
      });
    }

    function ensureMenu() {
      if (menu) return menu;
      menu = document.createElement('div');
      menu.className = 'feed-context-menu';
      menu.hidden = true;
      menu.setAttribute('role', 'menu');
      menu.innerHTML =
        '<button type="button" class="feed-context-item" data-action="edit" role="menuitem">Редактировать</button>' +
        '<button type="button" class="feed-context-item" data-action="refresh" role="menuitem">Обновить</button>' +
        '<button type="button" class="feed-context-item feed-context-danger" data-action="delete" role="menuitem">Удалить</button>';
      document.body.appendChild(menu);

      menu.addEventListener('click', function (ev) {
        var btn = ev.target.closest('[data-action]');
        if (!btn || !activeFeedId) return;
        ev.preventDefault();
        var action = btn.getAttribute('data-action');
        var id = activeFeedId;
        closeMenu();
        if (action === 'edit') {
          window.location.href = '/ui/feeds/' + id + '/edit';
        } else if (action === 'refresh') {
          postFeedAction('/ui/feeds/' + id + '/refresh');
        } else if (action === 'delete') {
          if (confirm('Удалить ленту?')) {
            postFeedAction('/ui/feeds/' + id + '/delete');
          }
        }
      });

      return menu;
    }

    document.addEventListener('contextmenu', function (ev) {
      var feed = ev.target.closest('.feeds-tree a.tree-feed');
      if (!feed) return;
      ev.preventDefault();
      var feedId = feed.getAttribute('data-feed-id');
      if (!feedId) {
        try {
          feedId = new URL(feed.href, location.origin).searchParams.get('feed_id');
        } catch (_) {}
      }
      if (!feedId) return;
      ensureMenu();
      activeFeedId = feedId;
      positionMenu(ev.clientX, ev.clientY);
    });

    document.addEventListener('click', function (ev) {
      if (!menu || menu.hidden) return;
      if (ev.target.closest('.feed-context-menu')) return;
      closeMenu();
    });

    document.addEventListener('keydown', function (ev) {
      if (ev.key === 'Escape') closeMenu();
    });

    document.addEventListener('scroll', closeMenu, true);
  }

  function initCategoryTree() {
    document.querySelectorAll('.feeds-tree, .feeds-mgmt-tree').forEach(function (tree) {
      var lazyFeeds = tree.classList.contains('feeds-tree-lazy') || tree.getAttribute('data-lazy-feeds') === 'true';
      var TREE_KEY = lazyFeeds ? 'rssam.treeCollapsed.lazy' : 'rssam.treeCollapsed';
      var entrySort = tree.getAttribute('data-entry-sort') || '';
      var expandCategory = tree.getAttribute('data-expand-category') || '';
      var expandUncategorized = tree.getAttribute('data-expand-uncategorized') === 'true';
      var feedsFilter = tree.getAttribute('data-feeds-filter') || '';

      function loadTreeCollapsed() {
        try {
          var raw = localStorage.getItem(TREE_KEY);
          return raw ? JSON.parse(raw) : {};
        } catch (_) {
          return {};
        }
      }

      function saveTreeCollapsed(state) {
        if (!lazyFeeds) {
          try {
            localStorage.setItem(TREE_KEY, JSON.stringify(state));
          } catch (_) {}
          return;
        }
        var compact = {};
        Object.keys(state).forEach(function (k) {
          if (state[k] === true) compact[k] = true;
        });
        try {
          localStorage.setItem(TREE_KEY, JSON.stringify(compact));
        } catch (_) {}
      }

      function lazyFeedsURL(container, offset) {
        var url = container.getAttribute('data-lazy-url');
        if (!url) return '';
        var qs = [];
        if (entrySort && entrySort !== 'newest') qs.push('sort=' + encodeURIComponent(entrySort));
        if (feedsFilter) qs.push('filter=' + encodeURIComponent(feedsFilter));
        if (offset) qs.push('offset=' + encodeURIComponent(offset));
        var feedID = new URLSearchParams(location.search).get('feed_id');
        if (feedID) qs.push('feed_id=' + encodeURIComponent(feedID));
        return qs.length ? url + '?' + qs.join('&') : url;
      }

      function appendCategoryFeeds(container, html) {
        if (!container) return;
        var oldMore = container.querySelector('.tree-feeds-load-more-wrap');
        if (oldMore) oldMore.remove();
        var tmp = document.createElement('div');
        tmp.innerHTML = html;
        while (tmp.firstChild) {
          container.appendChild(tmp.firstChild);
        }
      }

      tree.addEventListener('click', function (ev) {
        var btn = ev.target.closest('.tree-feeds-load-more');
        if (!btn) return;
        ev.preventDefault();
        var container = btn.closest('.tree-cat-feeds');
        if (!container) return;
        var offset = btn.getAttribute('data-offset');
        if (!offset) return;
        var url = lazyFeedsURL(container, offset);
        if (!url) return;
        btn.disabled = true;
        fetch(url, { credentials: 'same-origin' })
          .then(function (r) {
            if (!r.ok) throw new Error('load failed');
            return r.text();
          })
          .then(function (html) {
            appendCategoryFeeds(container, html);
          })
          .catch(function () {
            btn.disabled = false;
          });
      });

      function clearCategoryFeeds(feedsContainer) {
        if (!feedsContainer) return;
        feedsContainer.innerHTML = '';
        feedsContainer.removeAttribute('data-loaded');
      }

      function setCategoryCollapsed(cat, toggle, collapsed) {
        if (collapsed) cat.classList.add('collapsed');
        else cat.classList.remove('collapsed');
        if (toggle) {
          toggle.textContent = collapsed ? '+' : '−';
          toggle.setAttribute('aria-expanded', collapsed ? 'false' : 'true');
          toggle.setAttribute('aria-label', collapsed ? 'Развернуть категорию' : 'Свернуть категорию');
        }
      }

      function loadCategoryFeeds(container) {
        if (!container) return Promise.resolve();
        var loaded = container.getAttribute('data-loaded');
        if (loaded === 'true' || loaded === 'loading') return Promise.resolve();
        var url = lazyFeedsURL(container);
        if (!url) return Promise.resolve();
        container.setAttribute('data-loaded', 'loading');
        return fetch(url, { credentials: 'same-origin' })
          .then(function (r) {
            if (!r.ok) throw new Error('load failed');
            return r.text();
          })
          .then(function (html) {
            container.innerHTML = html;
            container.setAttribute('data-loaded', 'true');
          })
          .catch(function () {
            container.removeAttribute('data-loaded');
          });
      }

      function isDefaultExpanded(catID) {
        if (!lazyFeeds) return true;
        if (catID === 'sidebar-uncategorized') return expandUncategorized;
        if (expandCategory && catID === 'sidebar-' + expandCategory) return true;
        return false;
      }

      function collapseCategory(cat, toggle, feedsContainer, state, id, persist) {
        setCategoryCollapsed(cat, toggle, true);
        clearCategoryFeeds(feedsContainer);
        if (persist) {
          state[id] = true;
          saveTreeCollapsed(state);
        }
      }

      function collapseOtherCategories(activeCat) {
        tree.querySelectorAll('[data-tree-cat]').forEach(function (other) {
          if (other === activeCat) return;
          var otherToggle = other.querySelector('.tree-toggle');
          var otherFeeds = other.querySelector('.tree-cat-feeds');
          if (!other.classList.contains('collapsed')) {
            collapseCategory(other, otherToggle, otherFeeds, state, other.getAttribute('data-tree-cat'), true);
          }
        });
      }

      var state = loadTreeCollapsed();
      tree.querySelectorAll('[data-tree-cat]').forEach(function (cat) {
        var id = cat.getAttribute('data-tree-cat');
        var toggle = cat.querySelector('.tree-toggle');
        var feedsContainer = cat.querySelector('.tree-cat-feeds');
        var collapsed;
        if (lazyFeeds) {
          collapsed = state[id] === true || !isDefaultExpanded(id);
        } else {
          collapsed = state[id] === true;
          if (state[id] === undefined) collapsed = false;
        }
        if (collapsed) {
          collapseCategory(cat, toggle, feedsContainer, state, id, false);
        } else if (lazyFeeds && feedsContainer) {
          loadCategoryFeeds(feedsContainer);
        }
        if (!toggle) return;
        toggle.addEventListener('click', function (ev) {
          ev.preventDefault();
          ev.stopPropagation();
          var willCollapse = !cat.classList.contains('collapsed');
          if (willCollapse) {
            collapseCategory(cat, toggle, feedsContainer, state, id, true);
            return;
          }
          if (lazyFeeds) collapseOtherCategories(cat);
          setCategoryCollapsed(cat, toggle, false);
          state[id] = false;
          saveTreeCollapsed(state);
          if (lazyFeeds && feedsContainer) loadCategoryFeeds(feedsContainer);
        });
      });
    });
  }

  function initFilterForm() {
    var form = document.getElementById('filter-form');
    if (!form) return;

    function bindRemove(btn, row) {
      if (!btn || !row) return;
      btn.addEventListener('click', function () { row.remove(); });
    }

    document.querySelectorAll('.filter-tab').forEach(function (btn) {
      btn.addEventListener('click', function () {
        document.querySelectorAll('.filter-tab').forEach(function (b) { b.classList.remove('active'); });
        btn.classList.add('active');
        var tab = btn.getAttribute('data-tab');
        document.querySelectorAll('.filter-panel').forEach(function (p) {
          p.classList.toggle('hidden', p.getAttribute('data-panel') !== tab);
        });
      });
    });

    var addRule = document.getElementById('add-rule');
    if (addRule) {
      addRule.addEventListener('click', function () {
        var tbody = document.querySelector('#rules-table tbody');
        var sample = tbody && tbody.querySelector('tr');
        if (!sample) return;
        var row = sample.cloneNode(true);
        row.querySelectorAll('input').forEach(function (inp) {
          if (inp.type === 'checkbox') inp.checked = false;
          else inp.value = '';
        });
        var sel = row.querySelector('select');
        if (sel) sel.selectedIndex = 0;
        tbody.appendChild(row);
        bindRemove(row.querySelector('.rule-remove'), row);
      });
    }
    document.querySelectorAll('.rule-remove').forEach(function (btn) {
      bindRemove(btn, btn.closest('tr'));
    });

    function syncActionSelect(row) {
      var type = row.querySelector('.action-type') && row.querySelector('.action-type').value;
      var sel = row.querySelector('.action-param-select');
      if (!sel) return;
      sel.querySelectorAll('option').forEach(function (opt) {
        if (!opt.value) return;
        var show = (type === 'delete') || (opt.getAttribute('data-for') === type);
        opt.hidden = !show;
        opt.disabled = !show;
      });
      if (type === 'delete') {
        sel.value = '';
        return;
      }
      var saved = row.querySelector('.action-param-value');
      if (saved && saved.value) {
        var wantPrefix = type + ':';
        if (saved.value.indexOf(wantPrefix) === 0) {
          sel.value = saved.value;
        }
      }
    }

    function bindActionRow(row) {
      syncActionSelect(row);
      var typeSel = row.querySelector('.action-type');
      if (typeSel) {
        typeSel.addEventListener('change', function () { syncActionSelect(row); });
      }
    }

    var addAction = document.getElementById('add-action');
    if (addAction) {
      addAction.addEventListener('click', function () {
        var tbody = document.querySelector('#actions-table tbody');
        if (!tbody) return;
        var tpl = document.getElementById('filter-action-row-tpl');
        if (tpl && tpl.content && tpl.content.firstElementChild) {
          var row = tpl.content.firstElementChild.cloneNode(true);
          tbody.appendChild(row);
          bindActionRow(row);
          bindRemove(row.querySelector('.action-remove'), row);
          return;
        }
        var sample = tbody.querySelector('tr');
        if (!sample) return;
        var cloned = sample.cloneNode(true);
        cloned.querySelector('.action-param-value') && cloned.querySelector('.action-param-value').remove();
        tbody.appendChild(cloned);
        bindActionRow(cloned);
        bindRemove(cloned.querySelector('.action-remove'), cloned);
      });
    }

    document.querySelectorAll('#actions-table tbody tr').forEach(bindActionRow);
    document.querySelectorAll('.action-remove').forEach(function (btn) {
      bindRemove(btn, btn.closest('tr'));
    });

    initFilterFeedPicker(form);
  }

  function initFilterFeedPicker(form) {
    var picker = document.getElementById('filter-scope-pickers');
    var chips = document.getElementById('filter-scope-chips');
    var input = document.getElementById('filter-feed-search');
    var list = document.getElementById('filter-feed-suggest');
    if (!picker || !chips || !input || !list) return;

    var suggestURL = picker.getAttribute('data-suggest') || '/ui/feeds/suggest';
    var timer = null;
    var abort = null;
    var activeIndex = -1;

    function scopeIsAll() {
      var checked = form.querySelector('input[name="feed_scope"]:checked');
      return !checked || checked.value === 'all';
    }

    function syncScopeVisibility() {
      var hide = scopeIsAll();
      picker.classList.toggle('hidden', hide);
      input.disabled = hide;
      chips.querySelectorAll('input[name="scope_feed_id"]').forEach(function (el) {
        el.disabled = hide;
      });
      picker.querySelectorAll('input[name="scope_category_id"]').forEach(function (el) {
        el.disabled = hide;
      });
      hideSuggest();
    }

    function selectedIDs() {
      var ids = {};
      chips.querySelectorAll('input[name="scope_feed_id"]').forEach(function (el) {
        ids[el.value] = true;
      });
      return ids;
    }

    function addChip(item) {
      if (!item || !item.id) return;
      if (selectedIDs()[String(item.id)]) return;
      var chip = document.createElement('span');
      chip.className = 'filter-chip';
      chip.setAttribute('data-id', String(item.id));
      var title = document.createElement('span');
      title.className = 'filter-chip-title';
      title.textContent = item.title || ('#' + item.id);
      chip.appendChild(title);
      if (item.category_title) {
        var meta = document.createElement('span');
        meta.className = 'filter-chip-meta';
        meta.textContent = item.category_title;
        chip.appendChild(meta);
      }
      var hidden = document.createElement('input');
      hidden.type = 'hidden';
      hidden.name = 'scope_feed_id';
      hidden.value = String(item.id);
      var rm = document.createElement('button');
      rm.type = 'button';
      rm.className = 'filter-chip-remove';
      rm.setAttribute('aria-label', 'Убрать');
      rm.textContent = '×';
      chip.appendChild(hidden);
      chip.appendChild(rm);
      chips.appendChild(chip);
    }

    chips.addEventListener('click', function (ev) {
      var btn = ev.target.closest('.filter-chip-remove');
      if (!btn) return;
      var chip = btn.closest('.filter-chip');
      if (chip) chip.remove();
    });

    function hideSuggest() {
      list.classList.add('hidden');
      list.innerHTML = '';
      activeIndex = -1;
      if (abort) { abort.abort(); abort = null; }
    }

    function renderSuggest(items) {
      list.innerHTML = '';
      activeIndex = -1;
      var ids = selectedIDs();
      var shown = 0;
      (items || []).forEach(function (item) {
        if (!item || ids[String(item.id)]) return;
        var li = document.createElement('li');
        li.setAttribute('role', 'option');
        var t = document.createElement('span');
        t.className = 'filter-suggest-title';
        t.textContent = item.title || ('#' + item.id);
        li.appendChild(t);
        var bits = [];
        if (item.category_title) bits.push(item.category_title);
        if (item.feed_url) bits.push(item.feed_url);
        if (bits.length) {
          var sub = document.createElement('span');
          sub.className = 'filter-suggest-meta';
          sub.textContent = bits.join(' · ');
          li.appendChild(sub);
        }
        li.addEventListener('mousedown', function (ev) {
          ev.preventDefault();
          addChip(item);
          hideSuggest();
          input.value = '';
          input.focus();
        });
        list.appendChild(li);
        shown += 1;
      });
      list.classList.toggle('hidden', shown === 0);
    }

    function queryReady() {
      return Array.from((input.value || '').trim()).length >= 2;
    }

    function runSearch() {
      if (scopeIsAll() || !queryReady()) {
        hideSuggest();
        return;
      }
      if (abort) abort.abort();
      abort = new AbortController();
      var params = new URLSearchParams();
      params.set('q', (input.value || '').trim());
      fetch(suggestURL + '?' + params.toString(), {
        credentials: 'same-origin',
        signal: abort.signal,
        headers: { Accept: 'application/json' }
      }).then(function (res) {
        if (!res.ok) return { data: [] };
        return res.json();
      }).then(function (body) {
        renderSuggest(body && body.data ? body.data : []);
      }).catch(function (err) {
        if (err && err.name === 'AbortError') return;
        hideSuggest();
      });
    }

    function scheduleSearch() {
      if (timer) clearTimeout(timer);
      timer = setTimeout(runSearch, 300);
    }

    input.addEventListener('input', scheduleSearch);
    input.addEventListener('keydown', function (ev) {
      if (list.classList.contains('hidden')) return;
      var items = list.querySelectorAll('li');
      if (!items.length) return;
      if (ev.key === 'Escape') {
        hideSuggest();
        return;
      }
      if (ev.key === 'ArrowDown') {
        ev.preventDefault();
        activeIndex = Math.min(activeIndex + 1, items.length - 1);
      } else if (ev.key === 'ArrowUp') {
        ev.preventDefault();
        activeIndex = Math.max(activeIndex - 1, 0);
      } else if (ev.key === 'Enter' && activeIndex >= 0) {
        ev.preventDefault();
        items[activeIndex].dispatchEvent(new MouseEvent('mousedown'));
        return;
      } else {
        return;
      }
      items.forEach(function (el, i) {
        el.classList.toggle('active', i === activeIndex);
      });
    });
    document.addEventListener('click', function (ev) {
      if (!picker.contains(ev.target)) hideSuggest();
    });

    form.querySelectorAll('input[name="feed_scope"]').forEach(function (radio) {
      radio.addEventListener('change', syncScopeVisibility);
    });
    picker.querySelectorAll('input[name="scope_category_id"]').forEach(function (cb) {
      cb.addEventListener('change', function () {
        var item = cb.closest('.filter-cat-item');
        if (item) item.classList.toggle('is-selected', cb.checked);
      });
    });
    syncScopeVisibility();
  }

  function initCategoryReorder() {
    var tbody = document.getElementById('categories-sortable');
    if (!tbody || !tbody.getAttribute('data-reorder')) return;

    var dragRow = null;

    function rowFromTarget(target) {
      return target && target.closest ? target.closest('#categories-sortable tr[data-category-id]') : null;
    }

    function clearDragState() {
      tbody.querySelectorAll('tr.drag-over').forEach(function (row) { row.classList.remove('drag-over'); });
      if (dragRow) dragRow.classList.remove('dragging');
      dragRow = null;
    }

    function saveOrder() {
      var token = getCSRFToken();
      if (!token) return;
      var ids = Array.from(tbody.querySelectorAll('tr[data-category-id]')).map(function (row) {
        return row.getAttribute('data-category-id');
      });
      if (ids.length < 2) return;
      var body = new URLSearchParams();
      body.set('csrf_token', token);
      ids.forEach(function (id) { body.append('order', id); });
      fetch('/ui/categories/reorder', {
        method: 'POST',
        body: body,
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' }
      }).catch(function () {});
    }

    tbody.addEventListener('dragstart', function (ev) {
      var row = rowFromTarget(ev.target);
      if (!row) return;
      dragRow = row;
      row.classList.add('dragging');
      if (ev.dataTransfer) {
        ev.dataTransfer.effectAllowed = 'move';
        ev.dataTransfer.setData('text/plain', row.getAttribute('data-category-id') || '');
      }
    });

    tbody.addEventListener('dragover', function (ev) {
      var row = rowFromTarget(ev.target);
      if (!row || row === dragRow) return;
      ev.preventDefault();
      tbody.querySelectorAll('tr.drag-over').forEach(function (el) {
        if (el !== row) el.classList.remove('drag-over');
      });
      row.classList.add('drag-over');
      if (ev.dataTransfer) ev.dataTransfer.dropEffect = 'move';
    });

    tbody.addEventListener('drop', function (ev) {
      var row = rowFromTarget(ev.target);
      ev.preventDefault();
      if (!dragRow || !row || row === dragRow) {
        clearDragState();
        return;
      }
      var rect = row.getBoundingClientRect();
      var after = ev.clientY > rect.top + rect.height / 2;
      if (after) {
        row.parentNode.insertBefore(dragRow, row.nextSibling);
      } else {
        row.parentNode.insertBefore(dragRow, row);
      }
      clearDragState();
      saveOrder();
    });

    tbody.addEventListener('dragend', clearDragState);
  }

  function initWebhookKindForm() {
    var sel = document.getElementById('webhook-kind');
    if (!sel) return;
    var panels = Array.from(document.querySelectorAll('.webhook-kind-settings'));
    var urlInput = document.getElementById('webhook-url');
    function sync() {
      var kind = sel.value;
      panels.forEach(function (panel) {
        panel.hidden = panel.getAttribute('data-webhook-kind') !== kind;
      });
      if (urlInput) {
        urlInput.required = kind === 'http';
      }
    }
    sel.addEventListener('change', sync);
    sync();
  }

  function initConfirmForms() {
    document.addEventListener('submit', function (ev) {
      var form = ev.target;
      if (!form || !form.getAttribute) return;
      var msg = form.getAttribute('data-confirm');
      if (!msg) return;
      if (!confirm(msg)) ev.preventDefault();
    });
  }

  function init() {
    var prefs = loadPrefs();
    applyPrefs(prefs);
    initViewMode(prefs);
    initThemeToggle(prefs);
    initEntrySort(prefs);
    initSplitters(prefs);
    initFeedsToggle(prefs);
    initCategoryTree();
    initCategoryReorder();
    initFeedContextMenu();
    initEntryPreview();
    initKeyboardNav();
    initFeedForm();
    initConfirmForms();
    initWebhookKindForm();
    initFilterForm();
    initSidebarVersion();
    initServiceControls();
    connectWS();
  }

  function initSidebarVersion() {
    var el = document.getElementById('sidebar-version');
    if (!el) return;
    fetch('/ui/version', { credentials: 'same-origin' }).then(function (r) {
      if (!r.ok) return null;
      return r.json();
    }).then(function (d) {
      if (!d || !d.current) return;
      el.textContent = d.current;
      if (d.update_available) {
        el.classList.add('is-stale');
        el.title = 'Доступна ' + (d.latest || '');
      } else if (d.checked) {
        el.classList.remove('is-stale');
        el.title = 'Актуальная';
      }
    }).catch(function () {});
  }

  function initServiceControls() {
    function postForm(url, csrf, extra) {
      var body = 'csrf_token=' + encodeURIComponent(csrf);
      if (extra) body += extra;
      return fetch(url, {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: body
      }).then(function (r) { return r.json().then(function (j) { return { ok: r.ok, j: j }; }); });
    }
    function waitHealthz(statusEl, done) {
      var n = 0;
      function tick() {
        n += 1;
        fetch('/healthz', { cache: 'no-store' }).then(function (r) {
          if (r.ok) { done(true); return; }
          throw new Error('down');
        }).catch(function () {
          if (n > 30) { done(false); return; }
          statusEl.textContent = 'Ожидание сервиса… (' + n + ')';
          setTimeout(tick, 1000);
        });
      }
      setTimeout(tick, 800);
    }
    var wbtn = document.getElementById('btn-workers-toggle');
    if (wbtn) {
      wbtn.addEventListener('click', function () {
        var status = document.getElementById('workers-toggle-status');
        var runEl = document.getElementById('workers-run-status');
        var paused = wbtn.getAttribute('data-paused') === '1';
        var url = paused ? '/ui/admin/system/workers/resume' : '/ui/admin/system/workers/pause';
        wbtn.disabled = true;
        if (status) { status.hidden = false; status.textContent = paused ? 'Запуск воркеров…' : 'Остановка воркеров…'; }
        postForm(url, wbtn.getAttribute('data-csrf')).then(function (res) {
          if (!res.ok || !res.j.ok) {
            if (status) status.textContent = (res.j && res.j.error) || 'Ошибка';
            wbtn.disabled = false;
            return;
          }
          var nowPaused = !!res.j.paused;
          wbtn.setAttribute('data-paused', nowPaused ? '1' : '0');
          wbtn.textContent = nowPaused ? 'Запустить сервис' : 'Остановить сервис';
          wbtn.className = 'btn ' + (nowPaused ? 'btn-primary' : 'btn-danger');
          if (runEl) {
            runEl.textContent = nowPaused
              ? 'Воркеры остановлены: опрос лент и вебхуки не выполняются. HTTP и UI работают.'
              : 'Воркеры работают.';
          }
          if (status) status.textContent = nowPaused ? 'Воркеры остановлены' : 'Воркеры запущены';
          wbtn.disabled = false;
        }).catch(function () {
          if (status) status.textContent = 'Сеть оборвалась';
          wbtn.disabled = false;
        });
      });
    }
    var rst = document.getElementById('btn-restart');
    if (rst) {
      rst.addEventListener('click', function () {
        var status = document.getElementById('restart-status');
        rst.disabled = true;
        if (status) { status.hidden = false; status.textContent = 'Перезапуск…'; }
        postForm('/ui/admin/system/restart', rst.getAttribute('data-csrf')).then(function (res) {
          if (!res.ok || !res.j.ok) {
            if (status) status.textContent = (res.j && res.j.error) || 'Ошибка';
            rst.disabled = false;
            return;
          }
          waitHealthz(status, function (ok) {
            if (status) status.textContent = ok ? 'Сервис снова отвечает' : 'Нет ответа /healthz';
            if (ok) location.reload();
            else rst.disabled = false;
          });
        }).catch(function () {
          if (status) status.textContent = 'Сеть оборвалась — жду healthz';
          waitHealthz(status, function (ok) {
            if (ok) location.reload();
            else rst.disabled = false;
          });
        });
      });
    }
    var upd = document.getElementById('btn-update');
    if (upd) {
      var modal = document.getElementById('update-modal');
      var stepEl = document.getElementById('update-modal-step');
      var logEl = document.getElementById('update-modal-log');
      var actions = document.getElementById('update-modal-actions');
      var closeBtn = document.getElementById('update-modal-close');
      function setStep(s) { if (stepEl) stepEl.textContent = s; }
      function setLog(t) {
        if (!logEl) return;
        logEl.textContent = t || '';
        logEl.scrollTop = logEl.scrollHeight;
      }
      function stepFromLog(t) {
        t = t || '';
        if (t.indexOf('\nok ') >= 0 || t.indexOf('\nerror:') >= 0) return null;
        if (t.indexOf('restarting') >= 0) return 'Перезапуск сервиса…';
        if (t.indexOf('installed binary') >= 0) return 'Бинарник заменён';
        if (t.indexOf('downloading') >= 0) return 'Скачивание релиза с GitHub…';
        if (t.indexOf('target ') >= 0) return 'Подготовка обновления…';
        return 'Обновление…';
      }
      function finishModal(ok, msg) {
        setStep(msg);
        if (actions) actions.hidden = false;
        upd.disabled = false;
        if (ok) {
          setTimeout(function () { location.reload(); }, 800);
        }
      }
      if (closeBtn) {
        closeBtn.addEventListener('click', function () {
          if (modal) modal.hidden = true;
        });
      }
      upd.addEventListener('click', function () {
        var ver = upd.getAttribute('data-version') || '';
        upd.disabled = true;
        if (modal) modal.hidden = false;
        if (actions) actions.hidden = true;
        setStep('Запуск обновления ' + (ver || '') + '…');
        setLog('');
        postForm('/ui/admin/system/update', upd.getAttribute('data-csrf'), '&version=' + encodeURIComponent(ver)).then(function (res) {
          if (!res.ok || !res.j.ok) {
            finishModal(false, (res.j && res.j.error) || 'Не удалось запустить обновление');
            return;
          }
          var n = 0;
          function poll() {
            n += 1;
            fetch('/ui/admin/system/update/status', { credentials: 'same-origin', cache: 'no-store' }).then(function (r) {
              if (!r.ok) throw new Error('down');
              return r.json();
            }).then(function (st) {
              if (st.log) setLog(st.log);
              var s = stepFromLog(st.log);
              if (s) setStep(s);
              if (st.done && st.success) {
                setStep('Сервис отвечает, проверяю healthz…');
                waitHealthz(stepEl, function (ok) {
                  finishModal(ok, ok ? 'Обновление завершено' : 'Бинарник заменён, но /healthz не ответил');
                });
                return;
              }
              if (st.done && !st.success) {
                finishModal(false, st.error || 'Обновление не удалось');
                return;
              }
              if (n > 180) {
                finishModal(false, 'Таймаут ожидания обновления');
                return;
              }
              setTimeout(poll, 600);
            }).catch(function () {
              setStep('Ожидание перезапуска сервиса…');
              if (n > 180) {
                finishModal(false, 'Сервис не поднялся после обновления');
                return;
              }
              setTimeout(poll, 1000);
            });
          }
          setTimeout(poll, 400);
        }).catch(function () {
          finishModal(false, 'Сеть оборвалась при запуске обновления');
        });
      });
    }
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
