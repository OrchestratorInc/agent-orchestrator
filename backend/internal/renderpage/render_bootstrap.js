// Runs before the page's own styles and scripts. It hands the page the host
// theme and display mode, reports the page's content height, and asks the host
// to open links and, when the host asks for them, clicked images. A host that
// zooms the page (the artifact viewer) gets its pinch and Cmd+=/-/0 forwarded,
// hears when the pointer nears the bottom edge where its zoom controls float,
// and sets the page zoom.
// The messages are the MCP Apps postMessage methods, so this host could later
// show upstream MCP apps.
(function () {
  // Enabled only by the local artifact viewer, never by a page itself.
  var findEngine, findBindings = [], findRequest, findAnchor, findAnchorQuery, findOpen = false, findTimer = 0;
  function findPost(method, params) {
    window.parent.postMessage({ jsonrpc: "2.0", method: method, params: params }, "*");
  }
  function runFind(params) {
    if (!findEngine || typeof params.query !== "string" || !Number.isSafeInteger(params.generation)) return;
    if (findRequest && params.generation < findRequest.generation) return;
    var retain = findAnchorQuery === params.query && !params.reveal && findAnchor;
    findRequest = params;
    if (!params.query.trim()) { findEngine.clear(); return; }
    var ranges = findEngine.ranges(document.body, params.query);
    var active = Math.max(0, Math.min(ranges.length - 1, Number.isSafeInteger(params.active) ? params.active : 0));
    var retained = retain ? findEngine.retained(ranges, findAnchor) : -1;
    if (retained >= 0) active = retained;
    var range = ranges[active];
    findAnchorQuery = params.query;
    findAnchor = findEngine.anchor(range);
    findEngine.paint(ranges, active, document);
    if (params.reveal) findEngine.reveal(ranges[active]);
    findPost("ao/find-result", { generation: params.generation, total: ranges.length, active: active });
  }
  window.addEventListener("message", function (event) {
    var data = event.data, params = data && data.params;
    if (event.source !== window.parent || !data || data.jsonrpc !== "2.0" || !params || window.parent === window) return;
    if (data.method === "ao/find-configure" && params.exceptions && Array.isArray(params.bindings)) {
      if (!findEngine) {
        findEngine = createArtifactSearchEngine(params.exceptions);
        var observe = function () {
          if (!document.body) return;
          new MutationObserver(function (changes) {
            if (!findRequest || !findRequest.query.trim() || changes.every(function (change) { return (change.target.nodeType === 1 ? change.target : change.target.parentElement).closest("style[data-artifact-find-style]"); })) return;
            clearTimeout(findTimer);
            findTimer = setTimeout(function () { findPost("ao/find-changed", { generation: findRequest.generation }); }, 150);
          }).observe(document.body, { childList: true, characterData: true, subtree: true, attributes: true, attributeFilter: ["hidden", "class", "style", "aria-hidden", "open"] });
        };
        if (document.body) observe(); else document.addEventListener("DOMContentLoaded", observe, { once: true });
      }
      findBindings = params.bindings;
      findOpen = params.open === true;
    } else if (data.method === "ao/find") runFind(params);
  });
  document.addEventListener("keydown", function (event) {
    if (!findEngine || event.isComposing || event.repeat || event.target.closest("input,textarea,[contenteditable='true'],.cm-editor,.monaco-editor")) return;
    var matches = findBindings.some(function (binding) {
      return (String(binding.key).toLowerCase() === event.key.toLowerCase() || binding.code && binding.code === event.code) &&
        binding.ctrl === event.ctrlKey && binding.meta === event.metaKey && binding.shift === event.shiftKey && binding.alt === event.altKey;
    });
    if (matches) { event.preventDefault(); event.stopImmediatePropagation(); findPost("ao/find-open", {}); }
    if (event.key === "Escape" && findOpen) { event.preventDefault(); event.stopImmediatePropagation(); findPost("ao/find-close", {}); }
  }, true);
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
