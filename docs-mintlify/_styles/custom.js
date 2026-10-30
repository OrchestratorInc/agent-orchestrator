/* Language data: live site ships en + ja + zh-CN */
var LANGS = [["English",""],["日本語","/ja"],["中文","/zh-CN"]];
var AO_TRANSLATED = ["", "/installation", "/quickstart"];
function aoCurLangPrefix() {
  var m = window.location.pathname.match(/^\/(ja|zh-CN)(\/|$)/);
  return m ? "/" + m[1] : "";
}
function aoLangTarget(prefix) {
  var p = window.location.pathname.replace(/\/+$/, "");
  var m = p.match(/^\/(ja|zh-CN)(\/|$)(.*)$/);
  if (m) p = m[3] ? "/" + m[3].replace(/\/+$/, "") : "";
  if (AO_TRANSLATED.indexOf(p) < 0) p = "";
  return prefix + p || "/";
}
function aoCurLangName() {
  var cur = aoCurLangPrefix();
  for (var i = 0; i < LANGS.length; i++) if (LANGS[i][1] === cur) return LANGS[i][0];
  return "English";
}
/* Custom adjustments: replicate original docs UI chrome where Mintlify's
   components differ structurally. Runs after hydration; re-runs on
   Mintlify SPA navigations and resize across breakpoints. */
(function () {
  var GH_BASE = "https://github.com/OrchestratorInc/agent-orchestrator/blob/main/frontend/src/docs/content/";

  function clean(t) { return (t || "").replace(/\u200b/g, "").trim(); }
  function isMobileToc() { return window.innerWidth < 1280; } /* original: xl:hidden */

  function pageMdxPath() {
    var p = location.pathname.replace(/\/+$/, "");
    return (p === "" ? "/index" : p) + ".mdx";
  }

  /* ---------- Shared ToC state + fumadocs-style track ---------- */
  var TOC_STATE = { headings: [], ringFg: null, activeLabel: null, listEl: null, mTrack: null, dAnchors: null, dTrack: null, ticking: false };

  function collectHeadings() {
    var hs = document.querySelectorAll("main h2, main h3, article h2, article h3");
    var used = {};
    return Array.prototype.filter.call(hs, function (h) {
      if (h.closest("a") || h.closest("[class*=card]") || h.closest("#ao-page-actions") || h.closest("aside") || h.closest("[class*=toc]") || h.closest("[class*=TableOfContents]")) return false;
      var t = clean(h.textContent);
      if (!t) return false;
      if (!h.id || h.id.indexOf("_R_") === 0) {
        var slug = t.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") || "section";
        var s = slug, i = 2;
        while (used[s]) s = slug + "-" + (i++);
        h.id = s;
      }
      used[h.id] = 1;
      return true;
    });
  }

  function removeToc() {
    ["ao-toc-bar", "ao-toc-backdrop"].forEach(function (id) {
      var el = document.getElementById(id);
      if (el) el.remove();
    });
    TOC_STATE.ringFg = null; TOC_STATE.activeLabel = null; TOC_STATE.listEl = null; TOC_STATE.mTrack = null;
    var crumbs = document.querySelectorAll("[data-ao-crumb-hidden]");
    crumbs.forEach(function (c) { c.style.display = ""; c.removeAttribute("data-ao-crumb-hidden"); });
  }

  /* fumadocs TOCThumb: path jogs right for nested items, bends at item boundaries */
  function renderTrack(container, anchors) {
    var old = container.querySelector(".ao-trackwrap");
    if (old) old.remove();
    if (!anchors.length) return null;
    var crect = container.getBoundingClientRect();
    var items = anchors.map(function (a) {
      var r = a.getBoundingClientRect();
      return { top: r.top - crect.top, h: r.height, x: a._aoDepth === 3 ? 16.5 : 8.5, cy: r.top - crect.top + r.height / 2 };
    });
    var H = Math.ceil(items[items.length - 1].top + items[items.length - 1].h);
    if (H <= 0) return null;
    var d = "M " + items[0].x + " " + items[0].top;
    for (var i = 1; i < items.length; i++) {
      var B = (items[i - 1].top + items[i - 1].h + items[i].top) / 2;
      d += " L " + items[i - 1].x + " " + (B - 6) +
           " C " + items[i - 1].x + " " + (B + 2) + " " + items[i].x + " " + (B - 2) + " " + items[i].x + " " + (B + 6);
    }
    d += " L " + items[items.length - 1].x + " " + H;
    var wrap = document.createElement("div");
    wrap.className = "ao-trackwrap";
    wrap.setAttribute("aria-hidden", "true");
    wrap.innerHTML =
      '<svg viewBox="0 0 24.5 ' + H + '" style="width:24.5px;height:' + H + 'px" fill="none">' +
        '<path d="' + d + '" stroke="rgba(255,255,255,0.1)" stroke-width="1"/>' +
        '<path class="ao-track-hi" d="' + d + '" stroke="rgb(232,232,227)" stroke-width="1"/>' +
        '<circle class="ao-track-dot" r="2" fill="rgb(232,232,227)"/>' +
      '</svg><div class="ao-thumb"></div>';
    container.insertBefore(wrap, container.firstChild);
    return { wrap: wrap, items: items, H: H, hi: wrap.querySelector(".ao-track-hi"), dot: wrap.querySelector(".ao-track-dot"), thumb: wrap.querySelector(".ao-thumb") };
  }

  function updateTrack(track, firstIdx, lastIdx) {
    if (!track || firstIdx == null || firstIdx < 0 || lastIdx == null || lastIdx < firstIdx) return;
    var itF = track.items[firstIdx], itL = track.items[lastIdx];
    if (!itF || !itL) return;
    /* original hi segment spans the VISIBLE active rows only: from the first
       active row top to the last active row bottom minus its 6px padding
       (measured on live site) */
    var start = itF.top;
    var center = itL.top + itL.h - 6;
    track.hi.style.clipPath = "polygon(0 " + start + "px, 100% " + start + "px, 100% " + center + "px, 0 " + center + "px)";
    /* dot position is owned by glideDot (transform-based, eased) */
    track.thumb.style.left = (itL.x - 2) + "px";
    track.thumb.style.top = (center + 2) + "px";
    track.thumb.style.opacity = "1";
  }

  /* continuous marker: original glides with scroll (measured live: fractional
     positions between rows), ours stepped between rows. Interpolate the dot
     along the item anchors by fractional heading index. */
  /* Original semantics (measured live): the marker sits ON the current
     section row and EASES (0.15s cubic-bezier) to the next row when the
     reading line crosses into it. The visible marker is the SVG circle;
     cx/cy attribute changes cannot transition, so position it with a
     transform, which can. */
  function glideDot(track, F) {
    if (!track || F == null || !isFinite(F)) return;
    var n = track.items.length;
    if (!n) return;
    var i = Math.max(0, Math.min(n - 1, Math.floor(F)));
    var A = track.items[i];
    var x = A.x, y = (A.cy != null ? A.cy : A.top + A.h / 2);
    if (track.dot) {
      if (!track.dot._aoGlide) {
        track.dot._aoGlide = true;
        track.dot.setAttribute("cx", "0");
        track.dot.setAttribute("cy", "0");
        track.dot.style.transition = "transform 0.15s cubic-bezier(.4,0,.2,1)";
      }
      track.dot.style.transform = "translate(" + x + "px," + y + "px)";
    }
    track.thumb.style.left = (x - 2) + "px";
    track.thumb.style.top = (y + 2) + "px";
    track.thumb.style.opacity = "1";
  }

  function spyAll() {
    if (TOC_STATE.ticking) return;
    TOC_STATE.ticking = true;
    requestAnimationFrame(function () {
      TOC_STATE.ticking = false;
      var hs = TOC_STATE.headings;
      if (!hs || !hs.length) return;
      /* Original (fumadocs) semantics, verified against the live site at
         multiple scroll positions including the page bottom:
         - bright ToC items + hi track segment = headings INTERSECTING the
           viewport right now (top < innerHeight AND bottom > 0); when none
           intersect, the previous state is kept;
         - pill label = the UPCOMING section (first heading below the
           viewport top edge);
         - ring progress tracks the deepest visible heading. */
      var vh = window.innerHeight;
      var firstVis = -1, lastVis = -1, labelIdx = -1;
      for (var i = 0; i < hs.length; i++) {
        var r = hs[i].getBoundingClientRect();
        if (r.top < vh && r.bottom > 0) { if (firstVis < 0) firstVis = i; lastVis = i; }
        if (r.top > 0 && labelIdx < 0) labelIdx = i;
      }
      if (labelIdx < 0) labelIdx = hs.length - 1;
      var n = hs.length;
      if (TOC_STATE.ringFg && lastVis >= 0) {
        var prog = (lastVis + 1) / n;
        TOC_STATE.ringFg.style.strokeDashoffset = String(47.1239 * (1 - prog));
        var svg = TOC_STATE.ringFg.closest("svg");
        if (svg) svg.setAttribute("aria-valuenow", String(prog.toFixed(4)));
      }
      if (TOC_STATE.activeLabel) TOC_STATE.activeLabel.textContent = clean(hs[labelIdx].textContent);
      /* fractional heading index: how far the reference line (96px below the
         viewport top, matching the original's reading position) has passed
         between consecutive heading offsets */
      var sy = window.scrollY || window.pageYOffset || 0;
      var line = sy + 96;
      var fracIdx = 0;
      for (var k = 0; k < n; k++) {
        var off = hs[k].getBoundingClientRect().top + sy;
        if (off <= line) fracIdx = k; else { var prev = k > 0 ? hs[k - 1].getBoundingClientRect().top + sy : off; fracIdx = off > prev ? (k - 1) + (line - prev) / (off - prev) : k - 1; break; }
      }
      if (fracIdx < 0) fracIdx = 0;
      if (fracIdx > n - 1) fracIdx = n - 1;
      /* at page bottom the original rests the marker on the last row */
      if (sy + vh >= document.documentElement.scrollHeight - 4) fracIdx = n - 1;
      if (firstVis < 0) {
        if (TOC_STATE.mTrack) glideDot(TOC_STATE.mTrack, fracIdx);
        if (TOC_STATE.dTrack) glideDot(TOC_STATE.dTrack, fracIdx);
        return;
      }
      if (TOC_STATE.listEl) {
        var as = TOC_STATE.listEl.querySelectorAll("a");
        as.forEach(function (a, j) { a.classList.toggle("ao-read", j >= firstVis && j <= lastVis); });
        updateTrack(TOC_STATE.mTrack, firstVis, lastVis);
        glideDot(TOC_STATE.mTrack, fracIdx);
      }
      if (TOC_STATE.dAnchors) {
        TOC_STATE.dAnchors.forEach(function (a, j) {
          if (!a) return;
          a.classList.toggle("ao-dtoc-active", j >= firstVis && j <= lastVis);
        });
        updateTrack(TOC_STATE.dTrack, firstVis, lastVis);
        glideDot(TOC_STATE.dTrack, fracIdx);
      }
    });
  }

  /* ---------- Mobile "On this page" bar (fumadocs-style: ring + active section) ---------- */
  function buildTocBar() {
    removeToc();
    if (!isMobileToc()) return;
    var hs = TOC_STATE.headings;
    if (!hs || !hs.length) return;
    var h1 = document.querySelector("main h1, article h1");
    var title = clean(h1 ? h1.textContent : document.title);

    var bar = document.createElement("div");
    bar.id = "ao-toc-bar";
    bar.innerHTML =
      '<button type="button" id="ao-toc-trigger" aria-expanded="false" aria-controls="ao-toc-panel" aria-haspopup="true" aria-label="On this page">' +
        '<svg class="ao-ring" width="18" height="18" viewBox="0 0 18 18" role="progressbar" aria-valuemin="0" aria-valuemax="1" aria-valuenow="0">' +
          '<circle cx="9" cy="9" r="7.5" fill="none" stroke-width="1.5" class="ao-ring-bg"/>' +
          '<circle cx="9" cy="9" r="7.5" fill="none" stroke-width="1.5" class="ao-ring-fg" stroke="currentColor" stroke-dasharray="47.1239" stroke-dashoffset="47.1239" stroke-linecap="round" transform="rotate(-90 9 9)"/>' +
        '</svg>' +
        '<span class="ao-toc-labels"><span class="ao-toc-title"></span><span class="ao-toc-active"></span></span>' +
        '<svg class="ao-toc-chevron" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m6 9 6 6 6-6"/></svg>' +
      '</button>' +
      '<div id="ao-toc-panel" role="menu" aria-label="On this page" hidden><div class="ao-toc-scroll"><ul id="ao-toc-list"></ul></div></div>';
    bar.querySelector(".ao-toc-title").textContent = title;
    TOC_STATE.ringFg = bar.querySelector(".ao-ring-fg");
    TOC_STATE.activeLabel = bar.querySelector(".ao-toc-active");
    var ul = bar.querySelector("#ao-toc-list");
    TOC_STATE.listEl = ul;
    hs.forEach(function (h) {
      var li = document.createElement("li");
      var a = document.createElement("a");
      var depth = h.tagName === "H3" ? 3 : 2;
      a._aoDepth = depth;
      li.className = depth === 3 ? "ao-toc-h3" : "ao-toc-h2";
      a.href = "#" + h.id;
      a.textContent = clean(h.textContent);
      a.setAttribute("role", "menuitem");
      a.dataset.target = h.id;
      li.appendChild(a);
      ul.appendChild(li);
    });

    var backdrop = document.createElement("div");
    backdrop.id = "ao-toc-backdrop";
    backdrop.hidden = true;

    var header = document.querySelector("header");
    var crumb = header && header.querySelector("button.h-14, button[class*=" + "'h-14'" + "]");
    if (crumb && window.innerWidth < 1024) {
      crumb.style.display = "none";
      crumb.setAttribute("data-ao-crumb-hidden", "1");
      crumb.insertAdjacentElement("afterend", bar);
    } else if (header && header.parentNode) {
      header.insertAdjacentElement("afterend", bar);
    } else {
      document.body.insertBefore(bar, document.body.firstChild);
    }
    document.body.appendChild(backdrop);

    var trigger = bar.querySelector("#ao-toc-trigger");
    var panel = bar.querySelector("#ao-toc-panel");

    function open() {
      trigger.setAttribute("aria-expanded", "true");
      panel.hidden = false;
      backdrop.hidden = false;
      TOC_STATE.mTrack = renderTrack(ul, Array.prototype.slice.call(ul.querySelectorAll("a")));
      spyAll();
    }
    function close() { trigger.setAttribute("aria-expanded", "false"); panel.hidden = true; backdrop.hidden = true; }
    trigger.addEventListener("click", function () {
      if (trigger.getAttribute("aria-expanded") === "true") { close(); } else { open(); }
    });
    backdrop.addEventListener("click", close);
    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape" && !panel.hidden) { close(); trigger.focus(); }
    });
    ul.addEventListener("click", function () { close(); });
    spyAll();
  }

  /* ---------- Desktop ToC: original indents, prefix-bright text, track + thumb ---------- */
  function buildDesktopToc() {
    if (window.innerWidth < 1280) return;
    var heads = Array.prototype.filter.call(document.querySelectorAll("div,p,span,h2,h5"), function (e) {
      return e.children.length <= 2 && clean(e.textContent) === "On this page";
    });
    if (!heads.length) return;
    Array.prototype.forEach.call(heads[0].querySelectorAll("svg"), function (sv) {
      if (!sv.closest(".ao-toc-title-icon")) sv.remove();
    });
    if (!heads[0].querySelector(".ao-toc-title-icon")) {
      var ic = document.createElement("span");
      ic.className = "ao-toc-title-icon";
      ic.innerHTML = '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><line x1="21" y1="6" x2="3" y2="6"/><line x1="15" y1="12" x2="3" y2="12"/><line x1="17" y1="18" x2="3" y2="18"/></svg>';
      heads[0].insertBefore(ic, heads[0].firstChild);
      heads[0].style.display = "flex";
      heads[0].style.alignItems = "center";
      heads[0].style.gap = "8px";
    }
    var cont = heads[0].parentElement;
    var guard = 0;
    while (cont && cont.querySelectorAll("a").length < 3 && guard++ < 4) cont = cont.parentElement;
    if (!cont || cont.classList.contains("ao-dtoc-done")) { spyAll(); return; }
    var anchors = Array.prototype.slice.call(cont.querySelectorAll("a"));
    if (anchors.length < 3) return;
    cont.classList.add("ao-dtoc-done");
    var listWrap = anchors[0].parentElement;
    while (listWrap && listWrap !== cont && !anchors.every(function (a) { return listWrap.contains(a); })) listWrap = listWrap.parentElement;
    if (!listWrap) listWrap = cont;
    listWrap.style.position = "relative";
    cont = listWrap;
    anchors.forEach(function (a, i) {
      var depth = parseFloat(getComputedStyle(a).paddingLeft) > 8 ? 3 : 2;
      a._aoDepth = depth;
      a.classList.add("ao-dtoc-a", depth === 3 ? "ao-dtoc-h3" : "ao-dtoc-h2");
      if (i === 0) a.classList.add("ao-dtoc-first");
    });
    /* align anchor order with collected headings by hash */
    var byHash = {};
    anchors.forEach(function (a) { byHash[a.getAttribute("href")] = a; });
    TOC_STATE.dAnchors = TOC_STATE.headings.map(function (h) { return byHash["#" + h.id] || null; });
    if (TOC_STATE.dAnchors.some(function (a) { return !a; })) TOC_STATE.dAnchors = anchors;
    TOC_STATE.dTrack = renderTrack(cont, anchors);
    spyAll();
  }

  /* ---------- Search keycaps: separate Ctrl / K like the original ---------- */
  function buildKeycaps() {
    document.querySelectorAll("[data-search-item='trigger-label']").forEach(function (el) {
      if (clean(el.textContent) === "Search...") el.textContent = "Search";
    });
    document.querySelectorAll("[data-search-item='trigger-hint']").forEach(function (hint) {
      if (hint.dataset.aoDone) return;
      hint.dataset.aoDone = "1";
      hint.className = "ao-kbd-group";
      hint.innerHTML = '<kbd class="ao-kbd">Ctrl</kbd><kbd class="ao-kbd">K</kbd>';
    });
  }

  /* ---------- Sidebar chrome: panel toggle + English selector (original parity) ---------- */
  function buildSidebarChrome() {
    if (window.innerWidth < 1024) return;
    var sb = document.querySelector("[id*='sidebar']") || document.querySelector("aside[class*='--sidebar-width'], div[class*='--sidebar-width']");
    if (!sb) return;
    if (!sb.querySelector(".ao-side-toggle")) {
      if (getComputedStyle(sb).position === "static") sb.style.position = "relative";
      var btn = document.createElement("button");
      btn.type = "button";
      btn.className = "ao-side-toggle";
      btn.setAttribute("aria-label", "Toggle sidebar");
      btn.innerHTML = '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18"/></svg>';
      sb.appendChild(btn);
    }
    if (!sb.querySelector(".ao-lang")) {
      var langWrap = document.createElement("div");
      langWrap.className = "ao-lang-wrap";
      var lang = document.createElement("button");
      lang.type = "button";
      lang.className = "ao-lang";
      lang.setAttribute("aria-haspopup", "menu");
      lang.setAttribute("aria-expanded", "false");
      lang.innerHTML = '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m5 8 6 6"/><path d="m4 14 6-6 2-3"/><path d="M2 5h12"/><path d="M7 2h1"/><path d="m22 22-5-10-5 10"/><path d="m14 18h6"/></svg><span>' + aoCurLangName() + '</span><svg class="ao-lang-chev" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m6 9 6 6 6-6"/></svg>';
      var menu = document.createElement("div");
      menu.className = "ao-lang-menu";
      menu.setAttribute("role", "menu");
      menu.hidden = true;
      LANGS.forEach(function (L) {
        var item = document.createElement("a");
        item.className = "ao-lang-item";
        item.setAttribute("role", "menuitem");
        if (L[1] === aoCurLangPrefix()) { item.textContent = L[0]; item.setAttribute("aria-current", "true"); menu.appendChild(item); return; }
        item.href = aoLangTarget(L[1]);
        item.textContent = L[0];
        menu.appendChild(item);
      });
      lang.addEventListener("click", function () {
        var open = menu.hidden;
        menu.hidden = !open;
        lang.setAttribute("aria-expanded", String(open));
      });
      document.addEventListener("click", function (e) {
        if (!menu.hidden && !langWrap.contains(e.target)) { menu.hidden = true; lang.setAttribute("aria-expanded", "false"); }
      });
      document.addEventListener("keydown", function (e) {
        if (e.key === "Escape" && !menu.hidden) { menu.hidden = true; lang.setAttribute("aria-expanded", "false"); lang.focus(); }
      });
      langWrap.appendChild(menu);
      langWrap.appendChild(lang);
      sb.appendChild(langWrap);
    }
  }

  /* ---------- Page actions: Copy Markdown / Open in / View on GitHub ---------- */
  function buildPageActions() {
    var old = document.getElementById("ao-page-actions");
    if (old) old.remove();
    var h1 = document.querySelector("main h1, article h1");
    if (!h1) return;
    var container = h1.closest("header") || h1.parentElement;
    if (!container) return;

    var wrap = document.createElement("div");
    wrap.id = "ao-page-actions";

    /* Copy Markdown: delegates to Mintlify's built-in Copy page handler */
    var copyBtn = document.createElement("button");
    copyBtn.type = "button";
    copyBtn.className = "ao-action";
    copyBtn.innerHTML =
      '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/></svg>' +
      '<span>Copy Markdown</span>';
    function mdFromArticle() {
      var root = document.querySelector("main article, article, main");
      if (!root) return "";
      var out = [];
      function inlineMd(el) {
        var s = "";
        el.childNodes.forEach(function (n) {
          if (n.nodeType === 3) { s += n.textContent; }
          else if (n.nodeType === 1) {
            if (n.tagName === "CODE" && !n.closest("pre")) s += "`" + n.textContent + "`";
            else if (n.tagName === "A") s += "[" + n.textContent + "](" + (n.getAttribute("href") || "") + ")";
            else if (n.tagName === "STRONG" || n.tagName === "B") s += "**" + inlineMd(n) + "**";
            else if (n.tagName === "EM" || n.tagName === "I") s += "*" + inlineMd(n) + "*";
            else s += inlineMd(n);
          }
        });
        return clean(s);
      }
      function walk(node) {
        node.childNodes.forEach(function (el) {
          if (el.nodeType !== 1) return;
          var tag = el.tagName;
          if (el.id === "ao-page-actions" || el.closest("#ao-page-actions, #ao-toc-bar, nav, [class*=card]")) return;
          if (tag === "H1") out.push("# " + clean(el.textContent));
          else if (tag === "H2") out.push("\n## " + clean(el.textContent));
          else if (tag === "H3") out.push("\n### " + clean(el.textContent));
          else if (tag === "H4") out.push("\n#### " + clean(el.textContent));
          else if (tag === "P") out.push("\n" + inlineMd(el));
          else if (tag === "UL") { Array.prototype.forEach.call(el.children, function (li) { if (li.tagName === "LI") out.push("- " + inlineMd(li)); }); }
          else if (tag === "OL") { var i = 1; Array.prototype.forEach.call(el.children, function (li) { if (li.tagName === "LI") out.push((i++) + ". " + inlineMd(li)); }); }
          else if (tag === "PRE") { out.push("\n```\n" + el.textContent.replace(/\n$/, "") + "\n```"); }
          else if (el.matches(".code-block, [class*=code-block]")) { var pre = el.querySelector("pre"); if (pre) out.push("\n```\n" + pre.textContent.replace(/\n$/, "") + "\n```"); }
          else if (el.matches("[class*=callout]")) {
            var box = el.querySelector(".prose, [class*=prose]") || el;
            var lines = [];
            box.childNodes.forEach(function (n) { if (n.nodeType === 1) { var t = inlineMd(n); if (t) lines.push(t); } });
            if (!lines.length) lines.push(inlineMd(el));
            out.push("\n" + lines.map(function (l) { return "> " + l; }).join("\n"));
          }
          else if (tag === "TABLE") { /* tables kept as text */ out.push("\n" + clean(el.textContent)); }
          else walk(el);
        });
      }
      walk(root);
      return out.join("\n").replace(/\n{3,}/g, "\n\n").trim();
    }
    copyBtn.addEventListener("click", function () {
      var label = copyBtn.querySelector("span");
      var md = mdFromArticle();
      function done(ok) {
        label.textContent = ok ? "Copied" : "Copy failed";
        setTimeout(function () { label.textContent = "Copy Markdown"; }, 2000);
      }
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(md).then(function () { done(true); }, function () { done(false); });
      } else {
        var ta = document.createElement("textarea");
        ta.value = md; document.body.appendChild(ta); ta.select();
        try { document.execCommand("copy"); done(true); } catch (e) { done(false); }
        ta.remove();
      }
    });
    wrap.appendChild(copyBtn);

    /* Open in: popover with ChatGPT / Claude, same URLs as original */
    var openWrap = document.createElement("div");
    openWrap.className = "ao-openin";
    var openBtn = document.createElement("button");
    openBtn.type = "button";
    openBtn.className = "ao-action";
    openBtn.setAttribute("aria-haspopup", "menu");
    openBtn.setAttribute("aria-expanded", "false");
    openBtn.innerHTML =
      '<span>Open in</span>' +
      '<svg class="ao-caret" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m6 9 6 6 6-6"/></svg>';
    var menu = document.createElement("div");
    menu.className = "ao-openin-menu";
    menu.setAttribute("role", "menu");
    menu.hidden = true;
    var q = encodeURIComponent("Read " + location.origin + pageMdxPath().replace(/\.mdx$/, ".md") + " and answer my questions about it.");
    menu.innerHTML =
      '<a role="menuitem" target="_blank" rel="noreferrer" href="https://chatgpt.com/?hints=search&q=' + q + '">Open in ChatGPT</a>' +
      '<a role="menuitem" target="_blank" rel="noreferrer" href="https://claude.ai/new?q=' + q + '">Open in Claude</a>';
    openBtn.addEventListener("click", function () {
      var open = menu.hidden;
      menu.hidden = !open;
      openBtn.setAttribute("aria-expanded", String(open));
    });
    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape" && !menu.hidden) { menu.hidden = true; openBtn.setAttribute("aria-expanded", "false"); openBtn.focus(); }
    });
    document.addEventListener("click", function (e) {
      if (!menu.hidden && !openWrap.contains(e.target)) { menu.hidden = true; openBtn.setAttribute("aria-expanded", "false"); }
    });
    openWrap.appendChild(openBtn);
    openWrap.appendChild(menu);
    wrap.appendChild(openWrap);

    /* View on GitHub: same target as original */
    var gh = document.createElement("a");
    gh.className = "ao-action";
    gh.href = GH_BASE.replace(/\/+$/, "") + pageMdxPath();
    gh.target = "_blank";
    gh.rel = "noreferrer";
    gh.innerHTML =
      '<span>View on GitHub</span>' +
      '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/><path d="M15 3h6v6"/><path d="M10 14 21 3"/></svg>';
    wrap.appendChild(gh);

    container.appendChild(wrap);
  }

  /* Mobile navbar: original header mark is ao-logo 20px + "Docs" text */
  function buildHeaderLogo() {
    if (window.innerWidth >= 1024) return;
    var header = document.querySelector("header");
    if (!header) return;
    var row = header.querySelector("div.flex.items-center.h-12[class*='lg:hidden']");
    if (!row) return;
    var old = row.querySelector("a.ao-hdr-logo");
    if (old) old.remove();
    var a = document.createElement("a");
    a.className = "ao-hdr-logo";
    a.href = "/";
    a.innerHTML = '<span><img src="/logo/ao-logo.svg" alt="Orchestrator.inc" width="20" height="20"><span>Docs</span></span>';
    var spacer = row.querySelector("div.h-full.relative.flex-1");
    if (spacer && spacer.parentElement === row) row.insertBefore(a, spacer);
    else row.insertBefore(a, row.firstChild);
  }

  /* Mobile navbar: replace kebab with a sidebar toggle like the original */

  /* Mobile navbar: replace Mintlify's sprite search icon with the original lucide-search glyph */
  function buildSearchIcon() {
    if (window.innerWidth >= 1024) return;
    var header = document.querySelector("header");
    if (!header) return;
    Array.prototype.forEach.call(header.querySelectorAll("svg.ao-search-svg"), function (s) { s.remove(); });
    Array.prototype.forEach.call(header.querySelectorAll("button svg[data-ao-hidden='1']"), function (s) { s.style.display = ""; s.removeAttribute("data-ao-hidden"); });
    var btn = Array.prototype.find.call(header.querySelectorAll("button"), function (b) {
      var r = b.getBoundingClientRect();
      return (b.getAttribute("aria-label") || "").toLowerCase().indexOf("search") !== -1 && r.top < 56 && r.width > 10;
    });
    if (!btn) return;
    var sprite = btn.querySelector("svg:not(.ao-search-svg)");
    if (sprite) { sprite.style.display = "none"; sprite.setAttribute("data-ao-hidden", "1"); }
    var s = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    s.setAttribute("class", "ao-search-svg");
    s.setAttribute("width", "18"); s.setAttribute("height", "18");
    s.setAttribute("viewBox", "0 0 24 24"); s.setAttribute("fill", "none");
    s.setAttribute("stroke", "currentColor"); s.setAttribute("stroke-width", "2");
    s.setAttribute("stroke-linecap", "round"); s.setAttribute("stroke-linejoin", "round");
    s.setAttribute("aria-hidden", "true");
    s.innerHTML = '<path d="m21 21-4.34-4.34"></path><circle cx="11" cy="11" r="8"></circle>';
    btn.appendChild(s);
  }

  function hideBuiltinLocaleSelect() {
    var t = document.getElementById("localization-select-trigger");
    if (t) {
      var wrap = t.closest("div");
      if (wrap && wrap.style.display !== "none") wrap.style.display = "none";
    }
  }

  function buildMobileNavToggle() {
    var old = document.getElementById("ao-nav-toggle");
    if (old) old.remove();
    if (window.innerWidth >= 1024) return;
    var header = document.querySelector("header");
    if (!header) return;
    var more = Array.prototype.find.call(header.querySelectorAll("button"), function (b) { var c = b.className || ""; return /\bsize-9\b/.test(c) && /\brounded-full\b/.test(c); });
    if (more) more.style.display = "none";
    /* hide Mintlify's mobile breadcrumb strip even when this page has no TOC bar
       (heading-less index pages): the original header has no breadcrumb strip */
    var crumb = header.querySelector("[data-ao-crumb-hidden]");
    if (!crumb) {
      var strip = header.querySelector("button.h-14, button[class*='h-14']");
      if (strip) { strip.style.display = "none"; strip.setAttribute("data-ao-crumb-hidden", "1"); }
    }
    var btn = document.createElement("button");
    btn.id = "ao-nav-toggle";
    btn.type = "button";
    btn.setAttribute("aria-label", "Open sidebar");
    btn.style.cssText = "display:flex;align-items:center;justify-content:center;width:34px;height:34px;color:oklch(0.737 0.021 106.9);background:transparent;border:none;cursor:pointer;";
    btn.innerHTML = '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18"/></svg>';
    btn.addEventListener("click", function () { openAoDrawer(btn); });
    var search = Array.prototype.find.call(header.querySelectorAll("button"), function (b) {
      return b.getAttribute("data-search-item") === "trigger" && b.getBoundingClientRect().width > 0;
    });
    if (search && search.parentElement) search.insertAdjacentElement("afterend", btn);
  }



  /* ---------- Pager: chevron beside title, inject target-page descriptions ---------- */
  var AO_DESC_CACHE = {};
  function buildPager() {
    var links = document.querySelectorAll("nav.grid > a:has(> div.space-y-1)");
    links.forEach(function (a) {
      var labelRow = a.querySelector("div.space-y-1 > div.flex.items-center");
      var title = a.querySelector("div.space-y-1 > div.truncate");
      if (!labelRow || !title) return;
      if (!a.dataset.aoPager) {
        a.dataset.aoPager = "1";
        var isNext = /next/i.test(labelRow.textContent);
        if (isNext) a.classList.add("ao-pager-next");
        var iconWrap = document.createElement("span");
        iconWrap.className = "ao-pager-chev";
        iconWrap.style.cssText = "display:inline-flex;flex:none;width:16px;height:16px";
        var chevPath = isNext ? "m9 18 6-6-6-6" : "m15 18-6-6 6-6";
        iconWrap.innerHTML = '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="' + chevPath + '"/></svg>';
        if (isNext) title.appendChild(iconWrap); else title.insertBefore(iconWrap, title.firstChild);
        labelRow.remove();
        /* description from the target page's meta description */
        var href = a.getAttribute("href");
        if (href) {
          var desc = document.createElement("p");
          desc.className = "ao-pager-desc";
          desc.textContent = "\u00a0";
          a.appendChild(desc);
          if (AO_DESC_CACHE[href]) { desc.textContent = AO_DESC_CACHE[href]; }
          else {
            fetch(href).then(function (r) { return r.text(); }).then(function (html) {
              var doc = new DOMParser().parseFromString(html, "text/html");
              var m = doc.querySelector('meta[name="description"]');
              var t = m && m.getAttribute("content") ? m.getAttribute("content").trim() : "";
              AO_DESC_CACHE[href] = t;
              document.querySelectorAll('a[href="' + href + '"] p.ao-pager-desc').forEach(function (p) { p.textContent = t || "\u00a0"; });
            }).catch(function () {});
          }
        }
      }
    });
  }

  /* ---------- Search dialog chrome: original placeholder + ESC keycap ---------- */
  function buildSearchChrome() {
    var dlg = document.querySelector('[data-component-part="search-modal"]');
    if (!dlg) return;
    var inp = dlg.querySelector('[data-component-part="search-input"]');
    if (inp && inp.placeholder !== "Search") inp.placeholder = "Search";
    var cont = dlg.querySelector('[data-component-part="search-input-container"], [data-component-part="search-cli-state"]');
    if (cont && !cont.querySelector(".ao-search-esc")) {
      var esc = document.createElement("button");
      esc.type = "button";
      esc.className = "ao-search-esc";
      esc.textContent = "ESC";
      esc.setAttribute("aria-label", "Close search");
      esc.addEventListener("click", function () {
        (inp || dlg).dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
      });
      cont.appendChild(esc);
    }
  }

  function styleCalloutLinks() {
    document.querySelectorAll("[class*=callout] a").forEach(function (a) {
      a.style.setProperty("color", "rgb(96,165,250)", "important");
      a.style.setProperty("text-decoration", "underline", "important");
    });
  }


  /* ---------- Mobile drawer: exact Fumadocs-parity replacement ---------- */
  var AO_DR = { panel: null, backdrop: null, opener: null, prevOverflow: null, langCleanup: null };
  var AO_DR_LANGS = LANGS;
  function aoDrLangSvg() {
    return '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m5 8 6 6"/><path d="m4 14 6-6 2-3"/><path d="M2 5h12"/><path d="M7 2h1"/><path d="m22 22-5-10-5 10"/><path d="m14 18h6"/></svg>';
  }
  function aoDrSideSvg() {
    return '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18"/></svg>';
  }
  function buildDrawerLang(head) {
    var wrap = document.createElement("div");
    wrap.className = "ao-dr-lang-wrap";
    var btn = document.createElement("button");
    btn.type = "button";
    btn.className = "ao-dr-lang";
    btn.setAttribute("aria-haspopup", "menu");
    btn.setAttribute("aria-expanded", "false");
    btn.innerHTML = aoDrLangSvg() + "<span>" + aoCurLangName() + "</span>";
    var menu = document.createElement("div");
    menu.className = "ao-dr-lang-menu";
    menu.setAttribute("role", "menu");
    menu.hidden = true;
    AO_DR_LANGS.forEach(function (L) {
      var item = document.createElement("a");
      item.className = "ao-dr-lang-item";
      item.setAttribute("role", "menuitem");
      if (L[1] === aoCurLangPrefix()) { item.textContent = L[0]; item.setAttribute("aria-current", "true"); menu.appendChild(item); return; }
      item.href = aoLangTarget(L[1]);
      item.textContent = L[0];
      menu.appendChild(item);
    });
    btn.addEventListener("click", function (e) {
      e.stopPropagation();
      var open = menu.hidden;
      menu.hidden = !open;
      btn.setAttribute("aria-expanded", String(open));
    });
    wrap.appendChild(btn);
    wrap.appendChild(menu);
    head.appendChild(wrap);
    return { btn: btn, menu: menu };
  }
  function normalizeDrawerClone(frag) {
    frag.querySelectorAll(":scope > div").forEach(function (d) {
      if (d.querySelector(".sidebar-group-header")) d.classList.add("ao-dr-group");
    });
    frag.querySelectorAll("script, style, [id]").forEach(function (n) {
      if (n.tagName === "SCRIPT" || n.tagName === "STYLE") n.remove(); else n.removeAttribute("id");
    });
    // collapsible groups: wire toggles + lucide chevron like the original
    // process nested groups first so parents see converted child rows
    var grpBtns = Array.prototype.slice.call(frag.querySelectorAll("button, [role='button']"));
    grpBtns.reverse().forEach(function (b) {
      b.querySelectorAll("svg").forEach(function (s) {
        var n = document.createElementNS("http://www.w3.org/2000/svg", "svg");
        n.setAttribute("viewBox", "0 0 24 24");
        n.setAttribute("width", "16"); n.setAttribute("height", "16");
        n.setAttribute("fill", "none"); n.setAttribute("stroke", "currentColor");
        n.setAttribute("stroke-width", "2"); n.setAttribute("stroke-linecap", "round");
        n.setAttribute("stroke-linejoin", "round"); n.setAttribute("aria-hidden", "true");
        var p = document.createElementNS("http://www.w3.org/2000/svg", "path");
        p.setAttribute("d", "m6 9 6 6 6-6");
        n.appendChild(p); s.replaceWith(n);
      });
      var li = b.closest("li") || b.parentElement;
      var list = li && (li.querySelector(":scope > ul") || li.querySelector(":scope > div > ul") || li.querySelector(":scope > div"));
      if (!list || !list.children.length) { if (li) li.classList.add("ao-dr-collapsed"); return; }
      // original behavior: row label navigates to the group index, chevron toggles.
      // the original drawer has no index row inside groups: drop a direct child
      // whose href is a path prefix of every sibling (that is the index page),
      // and navigate the group row to it. otherwise navigate to the children's
      // common path prefix (or the single child's parent directory).
      var norm = function (h) { return (h || "").replace(/\/+$/, ""); };
      var rows = Array.prototype.slice.call(list.children);
      var rowHref = function (row) {
        var ra = (row.matches && row.matches("a[href]")) ? row : row.querySelector(":scope > a[href], :scope > div > a[href]");
        return ra ? norm(ra.getAttribute("href")) : null;
      };
      var hrefs = rows.map(rowHref);
      var groupHref = null;
      if (rows.length > 1) {
        for (var i = 0; i < rows.length; i++) {
          if (!hrefs[i]) continue;
          var ok = true;
          for (var j = 0; j < rows.length; j++) {
            if (i === j) continue;
            if (!hrefs[j] || !(hrefs[j] === hrefs[i] || hrefs[j].indexOf(hrefs[i] + "/") === 0)) { ok = false; break; }
          }
          if (ok) { groupHref = hrefs[i]; rows[i].remove(); break; }
        }
      }
      if (!groupHref) {
        var hs = hrefs.filter(Boolean);
        if (hs.length === 1) {
          groupHref = hs[0].slice(0, hs[0].lastIndexOf("/")) || null;
        } else if (hs.length > 1) {
          var segs = hs[0].split("/"), len = segs.length;
          for (var k = 1; k < hs.length; k++) {
            var s2 = hs[k].split("/"), m = 0;
            while (m < len && m < s2.length && s2[m] === segs[m]) m++;
            len = m;
          }
          var common = segs.slice(0, len).join("/");
          var strict = common && common.split("/").length >= 2 && hs.every(function (h) { return h.indexOf(common + "/") === 0; });
          groupHref = strict ? common : null;
        }
      }
      var a = document.createElement("a");
      if (groupHref) a.setAttribute("href", groupHref);
      a.className = (b.className || "") + " ao-dr-group";
      a.setAttribute("aria-expanded", "false");
      while (b.firstChild) a.appendChild(b.firstChild);
      b.replaceWith(a);
      var chevWrap = a.querySelector("svg") ? a.querySelector("svg").parentElement : null;
      if (chevWrap) chevWrap.addEventListener("click", function (e) {
        e.preventDefault(); e.stopPropagation();
        li.classList.toggle("ao-dr-collapsed");
        li.classList.toggle("ao-dr-open");
        a.setAttribute("aria-expanded", li.classList.contains("ao-dr-open") ? "true" : "false");
      });
      var pathNow = location.pathname.replace(/\/+$/, "");
      var containsActive = [...list.querySelectorAll("a[href]")].some(function (x) {
        return (x.getAttribute("href") || "").replace(/\/+$/, "") === pathNow;
      });
      li.classList.add(containsActive ? "ao-dr-open" : "ao-dr-collapsed");
      if (containsActive) a.setAttribute("aria-expanded", "true");
    });
    // active page marker
    var path = location.pathname.replace(/\/+$/, "");
    frag.querySelectorAll("a[href]").forEach(function (a) {
      var h = (a.getAttribute("href") || "").replace(/\/+$/, "");
      if (h === path) { a.classList.add("ao-dr-active"); a.setAttribute("aria-current", "page"); }
    });
    return frag;
  }
  function openAoDrawer(opener) {
    if (AO_DR.panel) return;
    var src = document.getElementById("sidebar-content");
    if (src) {
      var need = [...src.querySelectorAll("button[aria-expanded]")].filter(function (b) {
        var li = b.closest("li");
        return b.getAttribute("aria-expanded") === "false" || (li && !li.querySelector("ul"));
      });
      if (need.length) {
        var origPath = location.pathname;
        need.forEach(function (b) { if (b.getAttribute("aria-expanded") === "false") b.click(); });
        /* expanding a root-linked group (e.g. Configuration) also SPA-navigates
           to its root page; go back and keep the expanded state */
        var backTries = 0;
        setTimeout(function settle() {
          if (location.pathname !== origPath && backTries++ < 10) {
            history.back();
            setTimeout(settle, 600);
            return;
          }
          /* quiet beat: only build once the path has stayed put */
          setTimeout(function () {
            if (location.pathname !== origPath) { settle(); return; }
            openAoDrawer(opener);
            setTimeout(function () {
              if (location.pathname !== origPath) history.back();
            }, 600);
          }, 400);
        }, 600);
        return;
      }
    }
    AO_DR.opener = opener || null;
    var backdrop = document.createElement("div");
    backdrop.className = "ao-dr-backdrop";
    var panel = document.createElement("div");
    panel.className = "ao-dr-panel";
    panel.setAttribute("role", "dialog");
    panel.setAttribute("aria-modal", "true");
    panel.setAttribute("aria-label", "Navigation");
    var head = document.createElement("div");
    head.className = "ao-dr-head";
    var langRef = buildDrawerLang(head);
    var closeBtn = document.createElement("button");
    closeBtn.type = "button";
    closeBtn.className = "ao-dr-close";
    closeBtn.setAttribute("aria-label", "Close sidebar");
    closeBtn.innerHTML = aoDrSideSvg();
    closeBtn.addEventListener("click", closeAoDrawer);
    head.appendChild(closeBtn);
    var nav = document.createElement("nav");
    nav.className = "ao-dr-nav";
    nav.setAttribute("aria-label", "Docs");
    if (src) {
      var clone = src.cloneNode(true);
      var frag = document.createElement("div");
      while (clone.firstChild) frag.appendChild(clone.firstChild);
      // unwrap scroll-area wrappers: keep only the list container
      var inner = frag.querySelector("div.text-sm") || frag;
      var holder = document.createElement("div");
      while (inner.firstChild) holder.appendChild(inner.firstChild);
      normalizeDrawerClone(holder);
      // ensure Main site / GitHub top links exist
      if (!/Main site/.test(holder.textContent)) {
        var top = document.createElement("div");
        top.className = "ao-dr-toplinks";
        var headerLinks = document.querySelectorAll("header a[href]");
        var found = {};
        headerLinks.forEach(function (a) {
          var t = clean(a.textContent);
          if ((t === "Main site" || t === "GitHub") && !found[t]) {
            found[t] = 1;
            var c = document.createElement("a");
            c.href = a.href; c.textContent = t;
            if (a.target) c.target = a.target;
            top.appendChild(c);
          }
        });
        if (top.children.length) holder.insertBefore(top, holder.firstChild);
      }
      nav.appendChild(holder);
    }
    panel.appendChild(head);
    panel.appendChild(nav);
    document.body.appendChild(backdrop);
    document.body.appendChild(panel);
    AO_DR.panel = panel; AO_DR.backdrop = backdrop; AO_DR.lang = langRef;
    AO_DR.prevOverflow = document.documentElement.style.overflow;
    document.documentElement.style.overflow = "hidden";
    requestAnimationFrame(function () {
      backdrop.classList.add("ao-dr-in");
      panel.classList.add("ao-dr-in");
    });
    backdrop.addEventListener("click", closeAoDrawer);
    panel.setAttribute("tabindex", "-1"); panel.focus({ preventScroll: true });
  }
  function closeAoDrawer() {
    if (!AO_DR.panel) return;
    var panel = AO_DR.panel, backdrop = AO_DR.backdrop;
    panel.classList.remove("ao-dr-in");
    backdrop.classList.remove("ao-dr-in");
    setTimeout(function () { panel.remove(); backdrop.remove(); }, 220);
    document.documentElement.style.overflow = AO_DR.prevOverflow || "";
    AO_DR.panel = null; AO_DR.backdrop = null;
    if (AO_DR.opener && document.contains(AO_DR.opener)) AO_DR.opener.focus();
    AO_DR.opener = null;
  }
  document.addEventListener("keydown", function (e) {
    if (!AO_DR.panel) return;
    if (e.key === "Escape") {
      if (AO_DR.lang && !AO_DR.lang.menu.hidden) { AO_DR.lang.menu.hidden = true; AO_DR.lang.btn.setAttribute("aria-expanded", "false"); AO_DR.lang.btn.focus(); return; }
      e.stopPropagation();
      closeAoDrawer();
      return;
    }
    if (e.key === "Tab") {
      var all = AO_DR.panel.querySelectorAll("a[href], button:not([disabled])");
      var f = Array.prototype.filter.call(all, function (el) { return el.getClientRects().length > 0; });
      if (!f.length) return;
      var first = f[0], last = f[f.length - 1];
      if (e.shiftKey && (document.activeElement === first || !AO_DR.panel.contains(document.activeElement))) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
    }
  }, true);
  /* hijack every mobile "Open sidebar" trigger (capture, before React) */
  document.addEventListener("click", function (e) {
    if (window.innerWidth >= 1024) return;
    var b = e.target.closest && e.target.closest("button[aria-label='Open sidebar']");
    if (!b || b.id === "ao-nav-toggle") return;
    e.preventDefault(); e.stopImmediatePropagation();
    openAoDrawer(b);
  }, true);
  window.addEventListener("resize", function () { if (AO_DR.panel && window.innerWidth >= 1024) closeAoDrawer(); });

  /* fumadocs parity: swap Mintlify copy glyph for the original lucide-clipboard;
     append a lucide-check symbol for the copied state */
  function fixCopyIcons() {
    var s = document.getElementById("mi-mint-1-Copy");
    if (s && !s.getAttribute("data-ao-icon")) {
      s.setAttribute("data-ao-icon", "1");
      s.innerHTML = '<svg xmlns="http://www.w3.org/2000/svg" width="100%" height="100%" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="overflow:visible"><rect width="8" height="4" x="8" y="2" rx="1" ry="1"></rect><path d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2"></path></svg>';
    }
    var ck = document.getElementById("mi-mint-1-Check");
    if (ck && !ck.getAttribute("data-ao-icon")) {
      ck.setAttribute("data-ao-icon", "1");
      ck.innerHTML = '<svg xmlns="http://www.w3.org/2000/svg" width="100%" height="100%" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="overflow:visible"><path d="M20 6 9 17l-5-5"></path></svg>';
    }
  }

  /* fumadocs parity: copied state = lucide-check, bright, ~2s, then restore */
  var AO_COPY_BOUND = false;
  function bindCopyFeedback() {
    if (AO_COPY_BOUND) return;
    AO_COPY_BOUND = true;
    document.addEventListener("click", function (e) {
      var b = null;
      var path = e.composedPath ? e.composedPath() : [];
      for (var i = 0; i < path.length; i++) {
        var n = path[i];
        if (n && n.matches && n.matches('button[data-testid="copy-code-button"]')) { b = n; break; }
      }
      if (!b && e.target && e.target.closest) b = e.target.closest('button[data-testid="copy-code-button"]');
      if (!b || b.getAttribute("data-ao-copied")) return;
      b.setAttribute("data-ao-copied", "1");
      setTimeout(fixCopyIcons, 120);
      setTimeout(function () { if (document.contains(b)) b.removeAttribute("data-ao-copied"); }, 2000);
    }, true);
  }

  /* ---------- Prose parity that loses to layered important utilities ----------
     Mintlify's layered Tailwind important rules beat stylesheet !important,
     so set inline important styles: ol>li decimal markers (original) and
     table-cell inline code at 0.875em (original 12.25px in 14px cells). */
  function fixProseParity() {
    var lis = document.querySelectorAll("main ol > li");
    Array.prototype.forEach.call(lis, function (li) {
      if (li.getAttribute("data-ao-ol")) return;
      li.setAttribute("data-ao-ol", "1");
      li.style.setProperty("list-style-type", "decimal", "important");
      li.style.setProperty("padding-left", "6px", "important"); /* original: 6px text inset after marker */
    });
    var codes = document.querySelectorAll("main table td code, main table th code");
    Array.prototype.forEach.call(codes, function (c) {
      if (c.getAttribute("data-ao-tc")) return;
      c.setAttribute("data-ao-tc", "1");
      c.style.setProperty("font-size", "0.875em", "important");
    });
    /* Mobile: long inline-code tokens (paths) must wrap; an unbreakable pill
       widened the layout viewport to 405 on /configuration/remote-access. */
    if (window.innerWidth < 1024) {
      var pcodes = document.querySelectorAll("main p code, main li code");
      Array.prototype.forEach.call(pcodes, function (c) {
        if (c.getAttribute("data-ao-pc")) return;
        c.setAttribute("data-ao-pc", "1");
        c.style.setProperty("overflow-wrap", "anywhere", "important");
      });
    }
  }
  /* ---------- /platforms support cards: replicate original PlatformSupport ----------
     Original: flex row, 3 compact cards (66px, radius 12, hairline border),
     letter glyph + name (13/600/28) over status (12/16, transparent dot + gap). */
  function fixPlatformCards() {
    if (location.pathname.replace(/\/+$/g, "") !== "/platforms") return;
    var grid = document.querySelector("main div.columns");
    if (!grid || grid.getAttribute("data-ao-pc")) return;
    var cards = grid.querySelectorAll(":scope > div.card");
    if (cards.length !== 3) return;
    grid.setAttribute("data-ao-pc", "1");
    grid.style.setProperty("display", "flex", "important");
    grid.style.setProperty("flex-wrap", "wrap", "important");
    grid.style.setProperty("gap", "8px", "important");
    grid.style.setProperty("margin", "0", "important");
    var letters = { "macos": "a", "linux": "l", "windows": "w" };
    Array.prototype.forEach.call(cards, function (card) {
      var title = card.querySelector("h1, h2, h3");
      if (!title) return;
      var key = title.textContent.trim().toLowerCase();
      card.style.setProperty("display", "flex", "important");
      card.style.setProperty("align-items", "center", "important");
      card.style.setProperty("gap", "8px", "important");
      card.style.setProperty("padding", "10px 12px", "important");
      card.style.setProperty("border-radius", "12px", "important");
      card.style.setProperty("border", "1px solid rgba(255,255,255,0.1)", "important");
      card.style.setProperty("background", "transparent", "important");
      card.style.setProperty("margin", "0", "important");
      card.style.setProperty("flex", "1 1 0", "important");
      card.style.setProperty("min-width", "0", "important");
      card.style.setProperty("height", "auto", "important");
      card.style.setProperty("box-shadow", "none", "important");
      var inner = card.querySelector(":scope > div");
      if (!inner) return;
      inner.style.setProperty("padding", "0", "important");
      inner.style.setProperty("display", "flex", "important");
      inner.style.setProperty("align-items", "center", "important");
      inner.style.setProperty("gap", "8px", "important");
      inner.style.setProperty("width", "100%", "important");
      var svg = card.querySelector("svg");
      if (svg) { var iw = svg.closest("div") || svg; iw.style.setProperty("display", "none", "important"); }
      var letter = document.createElement("span");
      letter.textContent = letters[key] || key.charAt(0);
      letter.style.cssText = "display:inline-block;width:18px;height:18px;font-size:16px;font-weight:700;color:rgba(255,255,255,0.7);line-height:18px;text-align:center;flex:none;";
      title.style.setProperty("font-size", "13px", "important");
      title.style.setProperty("font-weight", "600", "important");
      title.style.setProperty("line-height", "28px", "important");
      title.style.setProperty("color", "rgba(255,255,255,0.7)", "important");
      title.style.setProperty("margin", "0", "important");
      title.style.setProperty("padding", "0", "important");
      var status = inner.querySelector("div.w-full") || card.querySelector("p, span[data-as='p']");
      var col = document.createElement("div");
      col.style.cssText = "display:flex;flex-direction:column;min-width:0;";
      if (status) {
        status.textContent = "";
        var dot = document.createElement("span");
        dot.style.cssText = "display:inline-block;width:6px;height:6px;border-radius:9999px;background:transparent;flex:none;";
        status.appendChild(dot);
        status.appendChild(document.createTextNode("Supported"));
        status.style.setProperty("font-size", "12px", "important");
        status.style.setProperty("line-height", "16px", "important");
        status.style.setProperty("color", "rgba(255,255,255,0.7)", "important");
        status.style.setProperty("margin", "0", "important");
        status.style.setProperty("padding", "0", "important");
        status.style.setProperty("display", "inline-flex", "important");
        status.style.setProperty("align-items", "center", "important");
        status.style.setProperty("gap", "6px", "important");
        col.appendChild(title);
        col.appendChild(status);
      } else {
        col.appendChild(title);
      }
      inner.appendChild(col);
      inner.insertBefore(letter, col);
    });
    /* PlatformSupport note renders at 13px/22.75 muted in the original component */
    var note = grid.nextElementSibling;
    if (note && /Terminal UI sessions/.test(note.textContent)) {
      note.style.setProperty("font-size", "13px", "important");
      note.style.setProperty("line-height", "22.75px", "important");
      note.style.setProperty("color", "rgba(255,255,255,0.7)", "important");
    }
  }
  /* Original nested pages show a breadcrumb row above the h1 (fs14 lh20,
     muted links, last one primary) that pushes the h1 to y92. Mintlify has none. */
  var AO_PLUGIN_GROUPS = { agents: "Agents", runtimes: "Runtimes", workspaces: "Workspaces", trackers: "Trackers", scm: "SCM", notifiers: "Notifications", terminals: "Terminals" };
  function buildBreadcrumb() {
    var old = document.querySelector(".ao-crumb");
    if (old) old.remove();
    var segs = location.pathname.replace(/^\/+|\/+$/g, "").split("/").filter(Boolean);
    var crumbs = null;
    if (segs[0] === "guides" && segs.length >= 2 && segs[1] !== "index") {
      crumbs = [["Guides", "/guides/"]];
    } else if (segs[0] === "configuration" && segs.length >= 2 && segs[1] !== "index") {
      crumbs = [["Configuration", "/configuration/"]];
    } else if (segs[0] === "plugins" && segs.length >= 2 && segs[1] !== "index") {
      crumbs = [["Built-in capabilities", "/plugins/"]];
      if (segs.length >= 3 && segs[2] !== "index" && AO_PLUGIN_GROUPS[segs[1]]) {
        crumbs.push([AO_PLUGIN_GROUPS[segs[1]], "/plugins/" + segs[1] + "/"]);
      }
    }
    if (!crumbs) return;
    var header = document.querySelector("header#header, header[class*=\"page-header\"]");
    if (!header || !header.querySelector("h1")) return;
    var div = document.createElement("div");
    div.className = "ao-crumb";
    div.style.cssText = "display:flex;align-items:center;gap:6px;font-size:14px;line-height:20px;margin:0 0 16px 0;letter-spacing:-0.5px;";
    crumbs.forEach(function (c, i) {
      if (i > 0) {
        var svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
        svg.setAttribute("width", "12"); svg.setAttribute("height", "12"); svg.setAttribute("viewBox", "0 0 24 24");
        svg.setAttribute("fill", "none"); svg.setAttribute("stroke", "currentColor"); svg.setAttribute("stroke-width", "2");
        svg.setAttribute("stroke-linecap", "round"); svg.setAttribute("stroke-linejoin", "round");
        svg.style.color = "oklch(0.737 0.021 106.9)"; svg.style.flex = "none";
        var path = document.createElementNS("http://www.w3.org/2000/svg", "path");
        path.setAttribute("d", "m9 6 6 6-6 6");
        svg.appendChild(path);
        div.appendChild(svg);
      }
      var a = document.createElement("a");
      a.href = c[1];
      a.textContent = c[0];
      var last = i === crumbs.length - 1;
      a.style.cssText = "color:" + (last ? "oklch(0.93 0.007 106.5)" : "oklch(0.737 0.021 106.9)") + ";font-weight:" + (last ? "500" : "400") + ";text-decoration:none;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;";
      div.appendChild(a);
    });
    header.insertBefore(div, header.firstChild);
  }

  /* ---------- Desktop article/table geometry vs original (Fumadocs).
     Band 1024-1279.5px: original article container uses px-24 (content
     x344 w852 at vw1271); Mintlify keeps lg:px-8 (32px). Tables: original
     spans exactly the content column at every desktop width; Mintlify's
     wrapper bleeds -20px/+40px via -mx + w-[calc(100%+40px)] and an inner
     px-(--page-padding). Layered Tailwind importance beats CSS !important,
     so set inline styles with priority. Mobile (<1024) untouched. ------- */
  function fixBandArticle() {
    var inBand = window.matchMedia("(min-width:1024px) and (max-width:1279.5px)").matches;
    var desktop = window.innerWidth >= 1024;
    var mc = document.querySelector(".mdx-content");
    if (mc && mc.parentElement) {
      var c = mc.parentElement;
      if (inBand) {
        c.style.setProperty("padding-left", "24px", "important");
        c.style.setProperty("padding-right", "24px", "important");
      } else {
        c.style.removeProperty("padding-left");
        c.style.removeProperty("padding-right");
      }
    }
    var hdr = document.getElementById("header");
    if (hdr) {
      if (inBand) { hdr.style.setProperty("padding-top", "7px", "important"); }
      else { hdr.style.removeProperty("padding-top"); }
    }
    var h1el = hdr ? hdr.querySelector("h1") : null;
    if (h1el && h1el.parentElement) {
      if (inBand) { h1el.parentElement.style.setProperty("margin-top", "0", "important"); }
      else { h1el.parentElement.style.removeProperty("margin-top"); }
    }
    var inners = document.querySelectorAll('.mdx-content div[class*="px-(--page-padding)"]');
    Array.prototype.forEach.call(inners, function (w) {
      if (desktop) {
        w.style.setProperty("padding-left", "0", "important");
        w.style.setProperty("padding-right", "0", "important");
      } else {
        w.style.removeProperty("padding-left");
        w.style.removeProperty("padding-right");
      }
    });
    var outers = document.querySelectorAll('.mdx-content div[class*="w-[calc(100%+(var(--page-padding)*2))]"]');
    Array.prototype.forEach.call(outers, function (o) {
      if (desktop) {
        o.style.setProperty("width", "100%", "important");
        o.style.setProperty("margin-left", "0", "important");
        o.style.setProperty("margin-right", "0", "important");
      } else {
        o.style.removeProperty("width");
        o.style.removeProperty("margin-left");
        o.style.removeProperty("margin-right");
      }
    });
  }
  /* ---------- v37: fine chrome parity (parent review 20:24).
     1) shiki .line spans carry px-4 (16px) on the original; Mintlify lines
        have 0, so code text sat 16px left of original (x362 vs x378).
     2) The original shows a ~5px bottom scrollbar strip on code blocks ONLY
        when content horizontally overflows; the v36x CSS lost to layered
        Tailwind, so apply conditionally via inline styles.
     3) Inline code fragments: original box-decoration-break:slice with
        2.8px/5.6px padding (deployed had clone + 2.45/4.9).
     4) Sidebar nested children: original indents links 20px (deployed 8px).
     5) Callout title paragraph (first child, strong-only): original mt0 mb4;
        v36w gave every callout paragraph mt20/mb20. ---------- */
  function fixChromeParity() {
    /* Mintlify reserves 131px right padding on code for the copy button;
       the original reserves 0 - the excess made non-overflowing blocks scroll */
    Array.prototype.forEach.call(document.querySelectorAll(".mdx-content pre code"), function (c) {
      c.style.setProperty("padding-right", "0", "important");
    });
    Array.prototype.forEach.call(document.querySelectorAll(".mdx-content pre .line"), function (l) {
      l.style.setProperty("padding-left", "16px", "important");
      l.style.setProperty("padding-right", "32px", "important");
    });
    /* original shows a real 5px horizontal scrollbar on overflowing code blocks:
       undo Base UI's scrollbar suppression (class + layered scrollbar-width:none) */
    Array.prototype.forEach.call(document.querySelectorAll(".mdx-content .code-block div[class*=--scroll-area-fade-size]"), function (sc) {
      sc.classList.remove("base-ui-disable-scrollbar");
      sc.style.setProperty("scrollbar-width", "auto", "important");
    });
    var aoMobile = window.innerWidth < 1024;
    Array.prototype.forEach.call(document.querySelectorAll(".mdx-content :not(pre) > code"), function (c) {
      c.style.setProperty("box-decoration-break", "slice", "important");
      /* original code fragments use overflow/word-break normal; Mintlify's
         break-word shrank table min-content widths and moved column splits.
         On mobile, non-table pills keep overflow-wrap anywhere (set in
         fixProseParity) so long path tokens wrap instead of widening the
         layout viewport (v42/v43). */
      if (!(aoMobile && !c.closest("td, th"))) {
        c.style.setProperty("overflow-wrap", "normal", "important");
        c.style.setProperty("word-break", "normal", "important");
      }
      if (!c.closest("td, th")) {
        c.style.setProperty("padding", "2.8px 5.6px", "important");
      }
    });
    Array.prototype.forEach.call(document.querySelectorAll("ul.sidebar-group li.space-y-px > ul > li > a"), function (a) {
      a.style.setProperty("padding-left", "20px", "important");
    });
    Array.prototype.forEach.call(document.querySelectorAll('[class*="callout"] [class*="text-sm"]'), function (w) {
      var kids = Array.prototype.filter.call(w.children, function (c) { return c.tagName === "P" || (c.tagName === "SPAN" && c.getAttribute("data-as") === "p"); });
      kids.forEach(function (p, i) {
        var st = p.querySelector("strong");
        var isTitle = i === 0 && st && p.textContent.trim() === st.textContent.trim();
        if (isTitle) {
          p.style.setProperty("margin-top", "0", "important");
          p.style.setProperty("margin-bottom", "4px", "important");
        }
      });
    });
  }

  function init() {
    try { TOC_STATE.headings = collectHeadings(); } catch (e) {}
    try { buildTocBar(); } catch (e) {}
    try { buildBreadcrumb(); } catch (e) {}
    try { buildDesktopToc(); } catch (e) {}
    try { buildHeaderLogo(); } catch (e) {}
    try { buildSearchIcon(); } catch (e) {}
    try { buildMobileNavToggle(); } catch (e) {}
    try { buildPageActions(); } catch (e) {}
    try { buildKeycaps(); } catch (e) {}
    try { buildSidebarChrome(); } catch (e) {}
    try { styleCalloutLinks(); } catch (e) {}
    try { buildSearchChrome(); } catch (e) {}
    try { buildPager(); } catch (e) {}
    try { fixCopyIcons(); } catch (e) {}
    try { fixProseParity(); } catch (e) {}
    try { fixBandArticle(); } catch (e) {}
    try { fixChromeParity(); } catch (e) {}
    try { fixPlatformCards(); } catch (e) {}
    try { bindCopyFeedback(); } catch (e) {}
    try { spyAll(); } catch (e) {}
  }
  function fixTitle() {
    if (document.title.endsWith(" - Orchestrator")) {
      document.title = document.title.slice(0, -" - Orchestrator".length) + " | Orchestrator.inc Docs";
    }
  }
  function initAll() { init(); fixTitle(); try { hideBuiltinLocaleSelect(); } catch (e) {} }
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", initAll);
  } else { initAll(); }
  [150, 350, 700, 1200, 2500, 5000].forEach(function (t) { setTimeout(initAll, t); });
  window.addEventListener("load", function () { spyAll(); setTimeout(spyAll, 300); });
  if (document.fonts && document.fonts.ready) document.fonts.ready.then(function () { spyAll(); });
  window.addEventListener("scroll", spyAll, { passive: true });
  document.addEventListener("scroll", function (e) { if (e.target !== document) spyAll(); }, { passive: true, capture: true });
  var mo = new MutationObserver(function () {
    clearTimeout(mo._t); mo._t = setTimeout(function () {
      var needToc = !document.getElementById("ao-toc-bar") && window.innerWidth < 1280;
      var needToggle = !document.getElementById("ao-nav-toggle") && window.innerWidth < 1024;
      var needActions = !document.getElementById("ao-page-actions");
      var needDtoc = window.innerWidth >= 1280 && !document.querySelector(".ao-dtoc-done");
      var needKeys = !document.querySelector("[data-search-item='trigger-hint'].ao-kbd-group, [data-search-item='trigger-hint'][data-ao-done]");
      var more = Array.prototype.find.call(document.querySelectorAll("header button"), function (b) { var c = b.className || ""; return /\bsize-9\b/.test(c) && /\brounded-full\b/.test(c); });
      var kebabVisible = more && getComputedStyle(more).display !== "none" && window.innerWidth < 1024;
      var searchOpen = !!document.querySelector('[data-component-part="search-modal"]');
      var needEsc = searchOpen && !document.querySelector('.ao-search-esc');
      var needPh = false;
      if (searchOpen) {
        var si = document.querySelector('[data-component-part="search-modal"] input[data-component-part="search-input"]');
        needPh = si && si.placeholder !== "Search";
      }
      try { fixSearchTrigger(); } catch (e) {}
      try { hideBuiltinLocaleSelect(); } catch (e) {}
      try { fixBandArticle(); } catch (e) {}
      try { fixChromeParity(); } catch (e) {}
      try { fixTitle(); } catch (e) {}
      var needChev = !!document.querySelector('nav#sidebar-content button svg use[href*="ChevronRight"]');
      if (needChev) { try { fixGroupChevrons(); } catch (e) {} }
      try { buildBreadcrumb(); } catch (e) {}
      if (needToc || needToggle || needActions || needDtoc || needKeys || kebabVisible) init();
      else if (needEsc || needPh) { try { buildSearchChrome(); } catch (e) {} }
    }, 400);
  });
  /* ---------- Sidebar group chevrons: exact original lucide chevron ----------
     Original renders lucide chevron-down (path m6 9 6 6 6-6) rotated -90deg,
     i.e. pixels identical to lucide chevron-right. Mintlify uses its own
     ChevronRight sprite with different stroke geometry. Swap path + attrs;
     Mintlify's open-state rotation classes keep working. */
  function fixGroupChevrons() {
    var uses = document.querySelectorAll('nav#sidebar-content button svg use[href*="ChevronRight"]');
    Array.prototype.forEach.call(uses, function (u) {
      var svg = u.closest('svg');
      if (!svg || svg.getAttribute('data-ao-chev')) return;
      svg.setAttribute('data-ao-chev', '1');
      svg.setAttribute('viewBox', '0 0 24 24');
      svg.setAttribute('fill', 'none');
      svg.setAttribute('stroke', 'currentColor');
      svg.setAttribute('stroke-width', '2');
      svg.setAttribute('stroke-linecap', 'round');
      svg.setAttribute('stroke-linejoin', 'round');
      svg.innerHTML = '<path d="m6 9 6 6 6-6"></path>';
    });
  }

  /* ---------- Search trigger: original inner layout ----------
     Mintlify's layered important utilities (p-0!) beat unlayered CSS
     !important, so set inline important styles. Original: ps-2 pe-1.5,
     muted rgb(184) text, search icon stroke-width 2. */
  function fixSearchTrigger() {
    var b = document.getElementById('search-bar-entry');
    if (!b || b.getAttribute('data-ao-search')) return;
    b.setAttribute('data-ao-search', '1');
    b.style.setProperty('padding-left', '8px', 'important');
    b.style.setProperty('padding-right', '6px', 'important');
    b.style.setProperty('color', 'rgb(184, 184, 184)', 'important');
    var icon = b.querySelector('svg');
    if (icon) {
      icon.style.setProperty('stroke-width', '2', 'important');
      icon.style.setProperty('color', 'rgb(184, 184, 184)', 'important');
      icon.style.setProperty('stroke', 'rgb(184, 184, 184)', 'important');
    }
  }

  mo.observe(document.body, { childList: true, subtree: true });
  document.addEventListener("mintlify:navigate", function () { setTimeout(init, 300); });
  var rt;
  window.addEventListener("resize", function () { clearTimeout(rt); rt = setTimeout(function () { init(); }, 200); });
})();
