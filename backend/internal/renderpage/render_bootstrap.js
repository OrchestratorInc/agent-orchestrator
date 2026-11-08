// Runs before the page's own styles and scripts. It hands the page the host
// theme and display mode, reports the page's content height, and asks the host
// to open links. A host that zooms the page (the artifact viewer) gets its
// pinch and Cmd+=/-/0 forwarded, hears when the pointer nears the bottom edge
// where its zoom controls float, and sets the page zoom.
// The messages are the MCP Apps postMessage methods T3 Code also uses, so this
// host could later show upstream MCP apps.
(function () {
  var theme = document.getElementById("ao-theme");
  // Created now, so it precedes the page's own styles and they win over it.
  var display = document.createElement("style");
  display.id = "ao-display";
  (document.head || document.documentElement).appendChild(display);
  var wrap = document.createElement("style");
  wrap.id = "ao-wrap";
  (document.head || document.documentElement).appendChild(wrap);
  // While zoomed the page pans by document scrolling, so a page that hides
  // its root overflow (slide decks, app shells) still scrolls.
  var zoomStyle = document.createElement("style");
  zoomStyle.id = "ao-zoom";
  (document.head || document.documentElement).appendChild(zoomStyle);
  var framed = window.parent !== window;
  var seq = 0;
  // Fullscreen gives the page the whole dialog width: center a top-level block
  // that has a max width, and show the scrollbar. A scrollable page (AO's own
  // field) scrolls inside a capped inline frame, so it shows the scrollbar
  // too. Inline otherwise stays as it is.
  var mode, scrollable = false, wrapContent = false;
  var showScrollbar = "html{scrollbar-width:auto}html::-webkit-scrollbar{display:block}";
  // Reflow is a reader choice, so it overrides the page's fixed widths and nowrap.
  var wrapCSS = "html,body,body *{min-width:0!important;max-width:100%!important;box-sizing:border-box!important;overflow-wrap:anywhere!important;text-wrap-mode:wrap!important}" +
    "table{width:100%!important;table-layout:fixed!important}colgroup,col,th,td{width:auto!important}img,video{height:auto!important}";
  function setDisplay(nextMode, nextScrollable, nextWrapContent) {
    if (nextMode !== undefined) mode = nextMode;
    if (nextScrollable !== undefined) scrollable = nextScrollable === true;
    if (nextWrapContent !== undefined) wrapContent = nextWrapContent === true;
    display.textContent = mode === "fullscreen"
      ? "body>*{margin-inline:auto}" + showScrollbar
      : scrollable ? showScrollbar : "";
    wrap.textContent = wrapContent ? wrapCSS : "";
  }
  function apply(t) {
    if (!theme || !t || typeof t !== "object" || !t.variables || typeof t.variables !== "object") return;
    var css = ":root{color-scheme:" + (t.appearance === "light" ? "light" : "dark") + ";";
    for (var k in t.variables) {
      if (/^--[a-z0-9-]+$/.test(k)) css += k + ":" + String(t.variables[k]).replace(/[;{}<>]/g, "") + ";";
    }
    theme.textContent = css + "}";
  }
  try {
    var m = /[#&]ao-theme=([^&]*)/.exec(location.hash);
    if (m) {
      var initial = JSON.parse(decodeURIComponent(m[1]));
      apply(initial);
      if (initial) setDisplay(initial.displayMode, initial.scrollable, initial.wrapContent);
      history.replaceState(history.state, "", location.pathname + location.search);
    }
  } catch (e) {}
  // The host owns the zoom level. The page scales with a transform, without
  // relayout, keeping the point under the pinch (or the viewport's center) in
  // place; below fit it stays centered. Zoomed, it pans by native scroll.
  var zoomable = false, zoom = 1, revealPx = 0;
  function setZoom(next, anchor) {
    next = Number(next);
    if (!(next >= 0.1 && next <= 10) || next === zoom) return;
    var ax = anchor && typeof anchor.x === "number" ? anchor.x : window.innerWidth / 2;
    var ay = anchor && typeof anchor.y === "number" ? anchor.y : window.innerHeight / 2;
    var ratio = next / zoom;
    var left = (window.scrollX + ax) * ratio - ax, top = (window.scrollY + ay) * ratio - ay;
    zoom = next;
    var root = document.documentElement;
    root.style.transformOrigin = next === 1 ? "" : "0 0";
    // Below fit the page stays centered: a percentage of its own width keeps
    // that true when the pane is resized.
    root.style.transform = next === 1 ? "" :
      "translateX(" + (next < 1 ? (1 - next) * 50 : 0) + "%) scale(" + next + ")";
    zoomStyle.textContent = next === 1 ? "" : "html,body{overflow:visible!important}";
    window.scrollTo(left, top);
  }
  window.addEventListener("message", function (e) {
    var d = e.data;
    var p = d && d.params;
    if (e.source !== window.parent || !d || d.jsonrpc !== "2.0" ||
        d.method !== "ui/notifications/host-context-changed" || !p) return;
    // MCP Apps context updates may be partial; an absent field is unchanged.
    setDisplay(p.displayMode, p.scrollable, p.wrapContent);
    if (p.styles) apply({ appearance: p.theme, variables: p.styles.variables });
    if (p.zoomable !== undefined) zoomable = p.zoomable === true;
    if (typeof p.zoomRevealPx === "number" && p.zoomRevealPx >= 0) revealPx = p.zoomRevealPx;
    if (p.zoom !== undefined) setZoom(p.zoom, p.zoomAnchor);
  });
  if (!framed) return;
  function zoomRequest(params) {
    window.parent.postMessage({ jsonrpc: "2.0", method: "ao/zoom", params: params }, "*");
  }
  // A trackpad pinch arrives as a wheel event with ctrlKey.
  window.addEventListener("wheel", function (e) {
    if (!zoomable || !e.ctrlKey) return;
    e.preventDefault();
    zoomRequest({ deltaY: e.deltaY, deltaMode: e.deltaMode, x: e.clientX, y: e.clientY });
  }, { passive: false });
  // The host's zoom controls float over the page's bottom edge, and pointer
  // moves inside the frame never reach the host; report entering that zone.
  var lastReveal = 0;
  window.addEventListener("pointermove", function (e) {
    if (!zoomable || !revealPx || window.innerHeight - e.clientY > revealPx) return;
    var now = Date.now();
    if (now - lastReveal < 250) return;
    lastReveal = now;
    zoomRequest({ reveal: true });
  }, { passive: true });
  document.addEventListener("keydown", function (e) {
    if (!zoomable || e.altKey || !(e.metaKey || e.ctrlKey)) return;
    var action = e.key === "=" || e.key === "+" ? "in" : e.key === "-" || e.key === "_" ? "out" : e.key === "0" ? "reset" : "";
    if (!action) return;
    e.preventDefault();
    zoomRequest({ action: action });
  }, true);
  document.addEventListener("click", function (e) {
    if (!e.isTrusted) return;
    var link = e.composedPath().find(function (n) { return n && n.matches && n.matches("a[href]"); });
    if (!link) return;
    var url;
    try { url = new URL(link.getAttribute("href"), document.baseURI); } catch (x) { return; }
    if (!/^https?:$/.test(url.protocol) || url.href.split("#")[0] === location.href.split("#")[0]) return;
    e.preventDefault();
    window.parent.postMessage({ jsonrpc: "2.0", id: "ao-link-" + (++seq), method: "ui/open-link", params: { url: url.href } }, "*");
  }, true);
  var last;
  function report() {
    var r = document.documentElement;
    var h = Math.ceil(r.scrollHeight > r.clientHeight ? r.scrollHeight : r.getBoundingClientRect().height);
    if (h === last) return;
    last = h;
    window.parent.postMessage({ jsonrpc: "2.0", method: "ui/notifications/size-changed", params: { height: h } }, "*");
  }
  var observer = window.ResizeObserver ? new ResizeObserver(report) : null;
  if (observer) observer.observe(document.documentElement);
  document.addEventListener("DOMContentLoaded", function () {
    if (observer && document.body) observer.observe(document.body);
    report();
  });
  window.addEventListener("load", report);
})();
