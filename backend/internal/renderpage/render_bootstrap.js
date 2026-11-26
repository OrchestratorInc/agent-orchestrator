// Runs before the page's own styles and scripts. It hands the page the host
// theme and display mode, reports the page's content height, and asks the host
// to open links and, when the host asks for them, clicked images. A host that
// zooms the page (the artifact viewer) gets its pinch and Cmd+=/-/0 forwarded,
// hears when the pointer nears the bottom edge where its zoom controls float,
// and sets the page zoom. A host that keeps the page's scroll (the artifact
// viewer, to show the page again as it was left) hears where it is scrolled
// and can scroll it back.
// The messages are the MCP Apps postMessage methods, so this host could later
// show upstream MCP apps.
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
  var images = document.createElement("style");
  images.id = "ao-images";
  (document.head || document.documentElement).appendChild(images);
  var openImages = false;
  function setOpenImages(next) {
    openImages = framed && next === true;
    images.textContent = openImages ? "img:not(a[href] img,button img){cursor:zoom-in}" : "";
  }
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
  function applyTheme(context) {
    if (!theme || !context || typeof context.variables !== "object" || !context.variables) return;
    theme.textContent = ":root{}";
    if (!theme.sheet) return;
    var declarations = theme.sheet.cssRules[0].style;
    declarations.setProperty("color-scheme", context.appearance === "light" ? "light" : "dark");
    Object.keys(context.variables).forEach(function (name) {
      if (/^--[a-z0-9-]+$/.test(name)) declarations.setProperty(name, String(context.variables[name]));
    });
  }
  try {
    var fragment = new URLSearchParams(location.hash.slice(1));
    if (fragment.has("ao-theme")) {
      var initial = JSON.parse(fragment.get("ao-theme"));
      applyTheme(initial);
      if (initial) {
        setDisplay(initial.displayMode, initial.scrollable, initial.wrapContent);
        setOpenImages(initial.openImages);
      }
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
  // A page shown again goes back to where it was scrolled. It may still be
  // laying out, so this is tried again as its size changes, until it is
  // reached, the reader takes over, or a few seconds pass.
  var scrollReport = false, restoreTarget = null, restoreUntil = 0;
  function restoreScroll() {
    if (!restoreTarget) return;
    window.scrollTo(restoreTarget.x, restoreTarget.y);
    if ((Math.abs(window.scrollX - restoreTarget.x) <= 1 && Math.abs(window.scrollY - restoreTarget.y) <= 1) ||
        Date.now() > restoreUntil) stopRestore();
  }
  function stopRestore() {
    if (!restoreTarget) return;
    restoreTarget = null;
    scheduleScrollReport();
  }
  window.addEventListener("message", function (e) {
    var d = e.data;
    var p = d && d.params;
    if (e.source !== window.parent || !d || d.jsonrpc !== "2.0" ||
        d.method !== "ui/notifications/host-context-changed" || !p) return;
    // MCP Apps context updates may be partial; an absent field is unchanged.
    setDisplay(p.displayMode, p.scrollable, p.wrapContent);
    if (p.openImages !== undefined) setOpenImages(p.openImages);
    if (p.styles) applyTheme({ appearance: p.theme, variables: p.styles.variables });
    if (p.zoomable !== undefined) zoomable = p.zoomable === true;
    if (typeof p.zoomRevealPx === "number" && p.zoomRevealPx >= 0) revealPx = p.zoomRevealPx;
    if (p.zoom !== undefined) setZoom(p.zoom, p.zoomAnchor);
    if (p.scrollReport !== undefined) scrollReport = p.scrollReport === true;
    var to = p.scrollTo;
    if (to && typeof to.x === "number" && typeof to.y === "number" && isFinite(to.x) && isFinite(to.y)) {
      restoreTarget = { x: to.x, y: to.y };
      restoreUntil = Date.now() + 3000;
      restoreScroll();
    }
  });
  if (!framed) return;
  function notify(method, params, id) {
    var message = { jsonrpc: "2.0", method: method, params: params };
    if (id !== undefined) message.id = id;
    window.parent.postMessage(message, "*");
  }
  function zoomRequest(params) {
    notify("ao/zoom", params);
  }
  // Where the page is scrolled, once a frame at most, for a host that asked.
  // Nothing is reported while a restore is on its way there.
  var scrollScheduled = false;
  function scheduleScrollReport() {
    if (!scrollReport || restoreTarget || scrollScheduled) return;
    scrollScheduled = true;
    window.requestAnimationFrame(function () {
      scrollScheduled = false;
      if (scrollReport && !restoreTarget) notify("ao/scroll", { x: window.scrollX, y: window.scrollY });
    });
  }
  window.addEventListener("scroll", scheduleScrollReport, { passive: true });
  // The reader scrolling or pressing first ends a restore.
  window.addEventListener("pointerdown", stopRestore, { capture: true, passive: true });
  window.addEventListener("touchstart", stopRestore, { capture: true, passive: true });
  // A trackpad pinch arrives as a wheel event with ctrlKey.
  window.addEventListener("wheel", function (e) {
    stopRestore();
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
    stopRestore();
    if (!zoomable || e.altKey || !(e.metaKey || e.ctrlKey)) return;
    var action = e.key === "=" || e.key === "+" ? "in" : e.key === "-" || e.key === "_" ? "out" : e.key === "0" ? "reset" : "";
    if (!action) return;
    e.preventDefault();
    zoomRequest({ action: action });
  }, true);
  document.addEventListener("click", function (event) {
    if (!event.isTrusted) return;
    for (var node of event.composedPath()) {
      if (!node || node.nodeType !== 1 || node.localName !== "a" || !node.hasAttribute("href")) continue;
      try {
        var destination = new URL(node.getAttribute("href"), document.baseURI);
        if (destination.protocol !== "http:" && destination.protocol !== "https:") return;
        var href = destination.href;
        var currentPage = new URL(location.href);
        destination.hash = "";
        currentPage.hash = "";
        if (destination.href === currentPage.href) return;
        event.preventDefault();
        notify("ui/open-link", { url: href }, "ao-link-" + (++seq));
      } catch (error) {}
      return;
    }
  }, true);
  window.addEventListener("click", function (e) {
    var img = e.target;
    if (!openImages || !e.isTrusted || e.defaultPrevented || !img || img.tagName !== "IMG" || img.closest("a[href],button")) return;
    var src = img.currentSrc || img.src;
    if (!/^data:image\//i.test(src)) return;
    notify("ao/open-image", { src: src, alt: img.alt });
  });
  var reportedHeight, scheduled = false;
  function scheduleHeight() {
    if (scheduled) return;
    scheduled = true;
    window.requestAnimationFrame(function () {
      scheduled = false;
      restoreScroll();
      var root = document.documentElement;
      var overflowing = root.scrollHeight > root.clientHeight;
      var height = Math.ceil(overflowing ? root.scrollHeight : root.getBoundingClientRect().height);
      if (height !== reportedHeight) {
        reportedHeight = height;
        notify("ui/notifications/size-changed", { height: height });
      }
    });
  }
  var sizeObserver = window.ResizeObserver ? new window.ResizeObserver(scheduleHeight) : null;
  if (sizeObserver) sizeObserver.observe(document.documentElement);
  function pageReady() {
    if (sizeObserver && document.body) sizeObserver.observe(document.body);
    scheduleHeight();
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", pageReady, { once: true });
  else pageReady();
  window.addEventListener("load", scheduleHeight, { once: true });
})();
