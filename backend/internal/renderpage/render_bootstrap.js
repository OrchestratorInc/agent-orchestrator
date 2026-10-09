// Runs before the page's own styles and scripts. It hands the page the host
// theme and display mode, reports the page's content height, and asks the host
// to open links.
// The messages are the MCP Apps postMessage methods T3 Code also uses, so this
// host could later show upstream MCP apps.
(function () {
  var theme = document.getElementById("ao-theme");
  // Created now, so it precedes the page's own styles and they win over it.
  var display = document.createElement("style");
  display.id = "ao-display";
  (document.head || document.documentElement).appendChild(display);
  var framed = window.parent !== window;
  var seq = 0;
  // Fullscreen gives the page the whole dialog width: center a top-level block
  // that has a max width, and show the scrollbar. A scrollable page (AO's own
  // field) scrolls inside a capped inline frame, so it shows the scrollbar
  // too. Inline otherwise stays as it is.
  var mode, scrollable = false;
  var showScrollbar = "html{scrollbar-width:auto}html::-webkit-scrollbar{display:block}";
  function setDisplay(nextMode, nextScrollable) {
    if (nextMode !== undefined) mode = nextMode;
    if (nextScrollable !== undefined) scrollable = nextScrollable === true;
    display.textContent = mode === "fullscreen"
      ? "body>*{margin-inline:auto}" + showScrollbar
      : scrollable ? showScrollbar : "";
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
      if (initial) setDisplay(initial.displayMode, initial.scrollable);
      history.replaceState(history.state, "", location.pathname + location.search);
    }
  } catch (e) {}
  window.addEventListener("message", function (e) {
    var d = e.data;
    var p = d && d.params;
    if (e.source !== window.parent || !d || d.jsonrpc !== "2.0" ||
        d.method !== "ui/notifications/host-context-changed" || !p) return;
    // MCP Apps context updates may be partial; an absent field is unchanged.
    setDisplay(p.displayMode, p.scrollable);
    if (p.styles) apply({ appearance: p.theme, variables: p.styles.variables });
  });
  if (!framed) return;
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
