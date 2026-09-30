// engram viewer — the few behaviours a static page cannot have on its own.
// Every page works without this file; it only adds the "/" shortcut, the
// sidebar drawer on narrow screens, the repository filter and copy buttons.
(function () {
  'use strict';

  // "/" focuses the search box, as on GitHub — unless the reader is typing.
  document.addEventListener('keydown', function (e) {
    if (e.key !== '/' || e.ctrlKey || e.metaKey || e.altKey) return;
    var t = e.target;
    if (t && (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName))) return;
    var box = document.getElementById('q');
    if (!box) return;
    e.preventDefault();
    box.focus();
    box.select();
  });

  // The sidebar drawer (below 1012px).
  var body = document.body;
  function setNav(open) {
    body.classList.toggle('nav-open', open);
    var b = document.querySelector('.nav-toggle');
    if (b) b.setAttribute('aria-expanded', open ? 'true' : 'false');
  }
  document.addEventListener('click', function (e) {
    if (e.target.closest('.nav-toggle')) { setNav(!body.classList.contains('nav-open')); return; }
    if (e.target.closest('.backdrop')) setNav(false);
  });
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && body.classList.contains('nav-open')) setNav(false);
  });

  // "Find a repository…" — filters the sidebar list in place. While a filter
  // is typed every match shows, not only the first few.
  var list = document.getElementById('repo-list');
  var filter = document.getElementById('repo-filter');
  var more = document.getElementById('repo-more');
  if (list && filter) {
    filter.addEventListener('input', function () {
      var q = filter.value.trim().toLowerCase();
      list.classList.toggle('show-all', !!q || list.dataset.expanded === '1');
      list.querySelectorAll('li').forEach(function (li) {
        li.style.display = !q || li.dataset.name.indexOf(q) !== -1 ? '' : 'none';
      });
      if (more) more.style.display = q ? 'none' : '';
    });
  }
  if (list && more) {
    more.addEventListener('click', function () {
      var on = list.dataset.expanded !== '1';
      list.dataset.expanded = on ? '1' : '0';
      list.classList.toggle('show-all', on);
      more.textContent = on ? 'Show less' : 'Show more';
    });
  }

  // Copy buttons: on every code block of a rendered document, and on every
  // element marked data-copy (the setup page's commands).
  var SVG = '<svg class="octicon" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">';
  var COPY = SVG + '<rect x="5.25" y="5.25" width="9" height="9" rx="1.5"/><path d="M10.75 3.25v-.5a1 1 0 0 0-1-1h-7a1 1 0 0 0-1 1v7a1 1 0 0 0 1 1h.5"/></svg>';
  var DONE = SVG + '<path d="m2.75 8.5 3.5 3.5 7-7.5"/></svg>';

  function copyText(text) {
    if (navigator.clipboard && window.isSecureContext) return navigator.clipboard.writeText(text);
    // Plain-HTTP deployments (a LAN store) have no async clipboard API.
    return new Promise(function (resolve, reject) {
      var ta = document.createElement('textarea');
      ta.value = text; ta.setAttribute('readonly', '');
      ta.style.position = 'fixed'; ta.style.opacity = '0';
      document.body.appendChild(ta); ta.select();
      try { document.execCommand('copy') ? resolve() : reject(); } catch (err) { reject(err); }
      document.body.removeChild(ta);
    });
  }

  function addButton(host, getText) {
    var b = document.createElement('button');
    b.type = 'button'; b.className = 'copy-btn'; b.innerHTML = COPY;
    b.setAttribute('aria-label', 'Copy to clipboard'); b.title = 'Copy';
    b.addEventListener('click', function () {
      copyText(getText()).then(function () {
        b.innerHTML = DONE; b.classList.add('done'); b.title = 'Copied!';
        setTimeout(function () { b.innerHTML = COPY; b.classList.remove('done'); b.title = 'Copy'; }, 1600);
      });
    });
    host.appendChild(b);
  }

  document.querySelectorAll('.markdown-body pre').forEach(function (pre) {
    var wrap = document.createElement('div');
    wrap.className = 'copy-wrap';
    pre.parentNode.insertBefore(wrap, pre);
    wrap.appendChild(pre);
    addButton(wrap, function () { return pre.innerText.replace(/\n$/, ''); });
  });
  document.querySelectorAll('[data-copy]').forEach(function (el) {
    addButton(el, function () { return el.getAttribute('data-copy'); });
  });
  document.querySelectorAll('[data-copy-path]').forEach(function (b) {
    b.addEventListener('click', function () {
      var label = b.querySelector('span');
      copyText(b.getAttribute('data-copy-path')).then(function () {
        if (!label) return;
        label.textContent = 'Copied!';
        setTimeout(function () { label.textContent = 'Copy path'; }, 1600);
      });
    });
  });

  // The setup checklist remembers what this browser ticked. Storage can be
  // unavailable (private windows, blocked site data) — then it just forgets.
  var KEY = 'engram.setup';
  var ticks = {};
  try { ticks = JSON.parse(localStorage.getItem(KEY) || '{}'); } catch (e) { ticks = {}; }
  document.querySelectorAll('.step input[type=checkbox][data-step]').forEach(function (cb) {
    if (ticks[cb.dataset.step]) cb.checked = true;
    cb.addEventListener('change', function () {
      ticks[cb.dataset.step] = cb.checked;
      try { localStorage.setItem(KEY, JSON.stringify(ticks)); } catch (e) { /* forget */ }
    });
  });
})();
