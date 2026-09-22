const TERMINAL_ENHANCE_JS = `
(function () {
  // The text layer (xterm-screen canvas) captures touches for selection, which
  // blocks the smooth native scroll. Make it (and the hidden input) transparent
  // to touch so drags fall through to the viewport's native scroll, and so a tap
  // can't focus the input (no surprise keyboard).
  var s = document.createElement('style');
  s.textContent =
    '.xterm-screen{pointer-events:none !important;}' +
    '.xterm-helper-textarea{pointer-events:none !important;}' +
    '.xterm-viewport{pointer-events:auto !important;-webkit-overflow-scrolling:touch !important;}' +
    // We drive scrolling ourselves, so the WebView scrollbar is pure wasted width
    // on the right. Hide it (and give the viewport a thin overlay one) so the fit
    // reclaims those pixels as extra columns instead of a gap.
    '.xterm-viewport{scrollbar-width:none !important;}' +
    '.xterm-viewport::-webkit-scrollbar{width:0 !important;height:0 !important;display:none !important;}';
  document.head.appendChild(s);

  // Report xterm's REAL grid size (measured by the FitAddon from the actual
  // rendered cell) back to RN through fressh's own debug channel, so RN can tell
  // the PTY the exact cols/rows xterm is using — no font/DPR guessing.
  // Report the phone's NATURAL fit (what would fill the screen at the current
  // font) WITHOUT resizing the terminal. The render grid is driven by the daemon
  // (the shared PTY's authoritative size), which we scale to fit below; we report
  // this fit only so the daemon can size the PTY to the phone when it is the sole
  // viewer. proposeDimensions measures without applying, unlike fit().
  function reportFit() {
    try {
      var F = window.fitAddon; if (!F || !F.proposeDimensions) return;
      // xterm can't measure a real scrollbar on mobile (overlay scrollbars are
      // 0px, so its "offsetWidth - offsetWidth || 15" falls back to assuming a
      // 15px one). proposeDimensions subtracts that phantom width and under-reports
      // cols, leaving a dead strip on the right. We drive our own scroll and hide
      // the bar (see injected CSS above), so zero it before measuring to reclaim
      // those columns for the fit.
      try {
        var vp = window.terminal && window.terminal._core && window.terminal._core.viewport;
        if (vp) vp.scrollBarWidth = 0;
      } catch (_) {}
      var d = F.proposeDimensions();
      if (d && d.cols > 0 && d.rows > 0 && window.ReactNativeWebView) {
        window.ReactNativeWebView.postMessage(
          JSON.stringify({ type: 'debug', message: 'FRESSH_DIMS ' + d.cols + ' ' + d.rows }));
      }
    } catch (_) {}
  }

  // ---- Zoom & pan -----------------------------------------------------------
  // The daemon may hold the grid wider than the phone (a co-viewing desktop
  // drives the size). Start at 1:1 so a desktop-sized grid remains readable on
  // the phone and is locally cropped instead of becoming a tiny overview. Pinch
  // and the native +/- controls zoom between fit-to-width and 2x; while zoomed,
  // one finger pans the viewport (vertical overshoot spills into scrollback) and
  // double-tap toggles overview <-> 1:1.
  // While zoomed we auto-pan to keep the cursor framed, so the prompt/output
  // stays in view without chasing it by hand.
  function term() { return window.terminal; }
  var Z = { s: 1, min: 1, max: 2, tx: 0, ty: 0,
            zoomed: false, followFit: false, lastPan: 0 };
  function box() {
    var root = document.querySelector('.xterm');
    var screen = document.querySelector('.xterm-screen');
    var host = document.getElementById('terminal') || document.body;
    if (!root || !screen || !host) return null;
    return { root: root, natW: screen.offsetWidth, natH: screen.offsetHeight,
             contW: host.clientWidth || window.innerWidth,
             contH: host.clientHeight || window.innerHeight };
  }
  function clampT(b) {
    var minTx = Math.min(0, b.contW - b.natW * Z.s);
    var minTy = Math.min(0, b.contH - b.natH * Z.s);
    if (Z.tx < minTx) Z.tx = minTx; if (Z.tx > 0) Z.tx = 0;
    if (Z.ty < minTy) Z.ty = minTy; if (Z.ty > 0) Z.ty = 0;
  }
  function applyTransform(b) {
    b.root.style.transformOrigin = 'top left';
    b.root.style.transform = 'translate(' + Z.tx + 'px,' + Z.ty + 'px) scale(' + Z.s + ')';
  }
  // Fit-to-width baseline, re-run on grid/container changes. followFit records
  // an explicit overview choice; otherwise authoritative grid resizes preserve
  // the default/user-selected actual-size crop and only re-clamp its pan.
  function applyScale() {
    try {
      var b = box(); if (!b || !b.natW || !b.contW) return;
      Z.min = Math.min(1, b.contW / b.natW);
      if (Z.followFit) { Z.s = Z.min; Z.tx = 0; Z.ty = 0; }
      else {
        if (Z.s < Z.min) Z.s = Z.min;
        if (Z.s > Z.max) Z.s = Z.max;
        clampT(b);
      }
      Z.zoomed = Z.s > Z.min + 0.001;
      applyTransform(b);
    } catch (_) {}
  }
  // Zoom to scale s keeping the content under screen point (ax, ay) fixed.
  function setZoom(s, ax, ay) {
    var b = box(); if (!b) return;
    if (s < Z.min) s = Z.min; if (s > Z.max) s = Z.max;
    var px = (ax - Z.tx) / Z.s, py = (ay - Z.ty) / Z.s;
    Z.s = s; Z.tx = ax - px * s; Z.ty = ay - py * s;
    Z.zoomed = s > Z.min + 0.001;
    Z.followFit = !Z.zoomed;
    if (!Z.zoomed) { Z.s = Z.min; Z.tx = 0; Z.ty = 0; }
    clampT(b); applyTransform(b);
  }
  // RN's +/- buttons call this through the WebView's imperative injection hook.
  // This changes only the phone's CSS viewport: xterm stays mounted and the
  // daemon-owned PTY grid is never resized, so a co-viewing desktop is unaffected.
  window.__aoAdjustTerminalZoom = function (direction) {
    var b = box(); if (!b || (direction !== 1 && direction !== -1)) return;
    setZoom(Z.s + direction * 0.2, b.contW / 2, b.contH / 2);
    Z.lastPan = Date.now();
  };
  // Auto-pan so the cursor stays framed while zoomed in. Backs off for a few
  // seconds after a manual pan/pinch (never fight the finger) and only follows
  // the live screen — not while the user is reading scrollback.
  function followCursor() {
    try {
      if (!Z.zoomed || Date.now() - Z.lastPan < 4000) return;
      var T = term(); var b = box(); if (!T || !b || !T.cols || !T.rows) return;
      var buf = T.buffer && T.buffer.active; if (!buf) return;
      if (buf.viewportY !== buf.baseY) return;
      var cx = (buf.cursorX + 0.5) * (b.natW / T.cols) * Z.s;
      var cy = (buf.cursorY + 0.5) * (b.natH / T.rows) * Z.s;
      var mX = Math.min(48, b.contW / 4), mY = Math.min(48, b.contH / 4);
      if (Z.tx + cx < mX) Z.tx = mX - cx;
      else if (Z.tx + cx > b.contW - mX) Z.tx = b.contW - mX - cx;
      if (Z.ty + cy < mY) Z.ty = mY - cy;
      else if (Z.ty + cy > b.contH - mY) Z.ty = b.contH - mY - cy;
      clampT(b); applyTransform(b);
    } catch (_) {}
  }

  // When the grid changes, keep it pinned to the bottom (latest output).
  function pinBottom() { try { window.terminal.scrollToBottom(); } catch (_) {} }
  var cfTimer = 0;
  (function wire() {
    if (window.terminal && window.terminal.onResize && window.fitAddon) {
      // The grid changes only when the daemon tells RN the authoritative size and
      // RN calls resize(); re-fit-to-width and pin on every such change.
      window.terminal.onResize(function () { setTimeout(function () { applyScale(); pinBottom(); }, 0); });
      // Throttled cursor-follow: cursor moves fire in bursts while output streams.
      if (window.terminal.onCursorMove) {
        window.terminal.onCursorMove(function () {
          if (cfTimer) return;
          cfTimer = setTimeout(function () { cfTimer = 0; followCursor(); }, 120);
        });
      }
      // On box changes (keyboard/rotation) re-report the fit and re-scale, but do
      // NOT fit() — the daemon owns the grid; fitting would fight it.
      try {
        var host = document.getElementById('terminal') || document.body;
        var ro = new ResizeObserver(function () { reportFit(); applyScale(); });
        ro.observe(host);
      } catch (_) {}
      reportFit(); applyScale();
      // Android's first measure often runs before layout/fonts settle, so the grid
      // comes out narrower than the WebView until some later resize nudges it. Since
      // nothing changes the host box in between (the ResizeObserver never fires until
      // e.g. the keyboard opens), re-measure a few times as things settle so the fit
      // reaches full width on its own.
      [60, 200, 500, 1000].forEach(function (t) {
        setTimeout(function () { reportFit(); applyScale(); }, t);
      });
    } else {
      setTimeout(wire, 200);
    }
  })();

  // Keyboard is handled by a React-Native TextInput, NOT the WebView. We disable
  // the WebView's hidden textarea (see harden) so it can never raise a keyboard
  // or steal first-responder. The keyboard button shows/hides the keyboard.

  // Gesture routing (canvas is pointer-events:none, so we read touches here):
  //  • quick drag -> scrollback scroll (overview) / viewport pan (zoomed)
  //  • pinch -> zoom between fit-to-width and 1:1
  //  • long-press -> select the line; drag extends by lines; release copies
  //  • single tap -> nothing   • double-tap -> toggle overview <-> 1:1
  function lineAt(clientY) {
    var T = term(), screen = document.querySelector('.xterm-screen');
    if (!T || !screen) return 0;
    var r = screen.getBoundingClientRect();
    var ch = r.height / T.rows;
    var vis = Math.floor((clientY - r.top) / ch);
    if (vis < 0) vis = 0; if (vis > T.rows - 1) vis = T.rows - 1;
    var top = (T.buffer && T.buffer.active) ? T.buffer.active.viewportY : 0;
    return top + vis;
  }
  function copySel() {
    var T = term(); if (!T) return; var txt = '';
    try { txt = T.getSelection(); } catch (_) {}
    if (!txt) return;
    try { if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(txt); } catch (_) {}
  }

  // ---- App-driven scrolling (harness-agnostic) ------------------------------
  // Full-screen TUIs (Claude Code, Codex, Gemini, aider, vim, less, ...) run in
  // the terminal's ALTERNATE screen buffer, which by design keeps NO xterm
  // scrollback — so .xterm-viewport has nothing to scroll and a drag "does
  // nothing". Rather than hand-encode scroll bytes per harness, we synthesize the
  // same 'wheel' event a desktop mouse produces and let xterm's own handler
  // translate it for WHATEVER the app negotiated: proper mouse-wheel bytes when
  // the app tracks the mouse (X10/UTF-8/SGR — xterm picks the right encoding),
  // else cursor-key presses (honoring application-cursor mode) in the alt buffer.
  // This means it works for every harness, not just one. The normal buffer (plain
  // shell scrollback) keeps its local viewport scroll below.
  function isAltScreen() {
    try { var b = term().buffer.active; return !!(b && b.type === 'alternate'); }
    catch (_) { return false; }
  }
  function mouseActive() {
    try { var m = term().modes; return !!(m && m.mouseTrackingMode && m.mouseTrackingMode !== 'none'); }
    catch (_) { return false; }
  }
  // Let xterm own the scroll only where a local viewport scroll wouldn't reach the
  // app: the alt buffer (no scrollback) or any buffer where the app tracks mouse.
  function appDrivesScroll() { return isAltScreen() || mouseActive(); }
  // Dispatch one wheel notch to xterm (up = toward older output). Coordinates are
  // the finger position so mouse-reporting apps get an accurate cell.
  function wheelTick(up, cx, cy) {
    var el = document.querySelector('.xterm'); if (!el) return;
    var ev;
    try {
      ev = new WheelEvent('wheel', { bubbles: true, cancelable: true,
        deltaX: 0, deltaY: up ? -1 : 1, deltaZ: 0,
        deltaMode: 1 /* DOM_DELTA_LINE */, clientX: cx, clientY: cy });
    } catch (_) {
      ev = document.createEvent('Event'); ev.initEvent('wheel', true, true);
      ev.deltaY = up ? -1 : 1; ev.deltaMode = 1; ev.clientX = cx; ev.clientY = cy;
    }
    el.dispatchEvent(ev);
  }

  var sX = 0, sY = 0, mode = 'idle', anchor = 0, lpTimer = 0;
  var MOVE = 10, LONGPRESS = 350, DBLTAP = 300;
  var altLines = 0;                        // wheel notches emitted to the app this gesture
  var SCROLL_STEP_PX = 24;                 // finger px per wheel notch (scale-independent)
  // Android: we drive the viewport's scrollTop directly off finger movement —
  // its native overflow-scroll doesn't respond to touch reliably in the WebView,
  // which is why the terminal felt unscrollable there. iOS keeps native momentum.
  var _vp = null, startScroll = 0;
  var lX = 0, lY = 0;                       // last touch point (zoomed pan deltas)
  var pinch0 = null;                        // pinch anchor {d, s, mx, my}
  var lastTap = 0, ltX = 0, ltY = 0;        // double-tap detection
  function clearLP() { if (lpTimer) { clearTimeout(lpTimer); lpTimer = 0; } }
  function touchDist(e) {
    var a = e.touches[0], b = e.touches[1];
    var dx = a.clientX - b.clientX, dy = a.clientY - b.clientY;
    return Math.sqrt(dx * dx + dy * dy);
  }

  document.addEventListener('touchstart', function (e) {
    // Taps on the scroll-to-top button are handled by the button itself; don't
    // let this capture-phase listener consume them as a terminal tap/long-press.
    var _tt = e.target;
    if (_tt && _tt.closest && _tt.closest('#ao-scrolltop')) return;
    if (e.touches && e.touches.length >= 2) {
      // Second finger down -> pinch. Cancel any pending tap/long-press/scroll.
      clearLP(); mode = 'pinch';
      pinch0 = { d: touchDist(e) || 1, s: Z.s,
                 mx: (e.touches[0].clientX + e.touches[1].clientX) / 2,
                 my: (e.touches[0].clientY + e.touches[1].clientY) / 2 };
      try { term() && term().clearSelection(); } catch (_) {}
      return;
    }
    var t = e.touches ? e.touches[0] : e;
    sX = t.clientX; sY = t.clientY; lX = sX; lY = sY; mode = 'pending';
    altLines = 0;
    _vp = document.querySelector('.xterm-viewport');
    startScroll = _vp ? _vp.scrollTop : 0;
    try { term() && term().clearSelection(); } catch (_) {}
    clearLP();
    lpTimer = setTimeout(function () {
      if (mode !== 'pending') return;
      mode = 'select'; anchor = lineAt(sY);
      try { term().selectLines(anchor, anchor); } catch (_) {}
    }, LONGPRESS);
  }, { capture: true, passive: true });

  document.addEventListener('touchmove', function (e) {
    if (mode === 'pinch') {
      if (!e.touches || e.touches.length < 2 || !pinch0) return;
      if (e.cancelable) e.preventDefault();  // keep the page/viewport from moving
      var mx = (e.touches[0].clientX + e.touches[1].clientX) / 2;
      var my = (e.touches[0].clientY + e.touches[1].clientY) / 2;
      // Two-finger drag pans while pinching; the scale keeps the content under
      // the midpoint anchored so the zoom feels centered on the fingers.
      Z.tx += mx - pinch0.mx; Z.ty += my - pinch0.my;
      setZoom(pinch0.s * (touchDist(e) / pinch0.d), mx, my);
      pinch0.mx = mx; pinch0.my = my;
      Z.lastPan = Date.now();
      return;
    }
    var t = e.touches ? e.touches[0] : e;
    if (mode === 'pending') {
      if (Math.abs(t.clientX - sX) > MOVE || Math.abs(t.clientY - sY) > MOVE) {
        mode = 'scroll'; clearLP(); lX = t.clientX; lY = t.clientY;
      }
      return;
    }
    if (mode === 'scroll') {
      if (appDrivesScroll()) {
        // The app owns scrolling here (alt buffer / mouse tracking): feed it wheel
        // notches instead of moving the (empty) xterm viewport. Content follows the
        // finger (drag down -> older), matching the normal buffer's direction below.
        // One notch per SCROLL_STEP_PX of travel; emit only on each boundary cross.
        if (e.cancelable) e.preventDefault();
        var moved = (t.clientY - sY) / SCROLL_STEP_PX;   // + = finger down = older
        var want = moved > 0 ? Math.floor(moved) : Math.ceil(moved);
        var diff = want - altLines;
        if (diff !== 0) {
          var up = diff > 0;                             // more "older" notches -> wheel up
          for (var i = 0; i < Math.abs(diff); i++) wheelTick(up, t.clientX, t.clientY);
          altLines = want;
        }
        // While zoomed, the horizontal component still pans the magnified grid.
        if (Z.zoomed) {
          var bz = box();
          if (bz) { Z.tx += t.clientX - lX; clampT(bz); applyTransform(bz); Z.lastPan = Date.now(); }
        }
        lX = t.clientX; lY = t.clientY;
        return;
      }
      if (Z.zoomed) {
        // Zoomed in: one finger pans the viewport over the big grid. Vertical
        // overshoot past the grid edge spills into scrollback scrolling (divide
        // by scale: scrollTop is in unscaled content px, the finger in screen px).
        if (e.cancelable) e.preventDefault();
        var b = box();
        if (b) {
          Z.tx += t.clientX - lX;
          var wantTy = Z.ty + (t.clientY - lY);
          Z.ty = wantTy;
          clampT(b);
          var spill = wantTy - Z.ty;
          if (spill !== 0 && _vp) _vp.scrollTop -= spill / Z.s;
          applyTransform(b);
        }
        Z.lastPan = Date.now();
        lX = t.clientX; lY = t.clientY;
        return;
      }
      // Overview: scrollback scroll. Android: move the viewport ourselves, 1:1
      // with the finger. iOS: leave it to native momentum (don't preventDefault).
      if (IS_ANDROID && _vp) {
        _vp.scrollTop = startScroll - (t.clientY - sY);
        if (e.cancelable) e.preventDefault();
      }
      return;
    }
    if (mode === 'select') {
      if (e.cancelable) e.preventDefault();  // stop native scroll while selecting
      var cur = lineAt(t.clientY);
      try { term().selectLines(Math.min(anchor, cur), Math.max(anchor, cur)); } catch (_) {}
    }
  }, { capture: true, passive: false });

  document.addEventListener('touchend', function (e) {
    clearLP();
    if (mode === 'pinch') {
      // Stay in pinch while two fingers remain; otherwise done (the leftover
      // finger must lift and re-touch to start a new gesture).
      if (!e.touches || e.touches.length < 2) { mode = 'idle'; pinch0 = null; }
      return;
    }
    if (mode === 'select') copySel();
    if (mode === 'pending') {
      // A tap. Two taps close together toggle overview <-> 1:1 at the tap point.
      var now = Date.now();
      if (now - lastTap < DBLTAP && Math.abs(sX - ltX) < 40 && Math.abs(sY - ltY) < 40) {
        lastTap = 0;
        if (Z.zoomed) setZoom(Z.min, 0, 0);
        else { setZoom(1, sX, sY); Z.lastPan = Date.now(); }
      } else { lastTap = now; ltX = sX; ltY = sY; }
    }
    mode = 'idle';
  }, { capture: true, passive: true });

  // ---- Scroll-to-top button ------------------------------------------------
  // A floating button that scrolls back to the oldest output. It lives in the
  // terminal DOM (not RN) because the WebView package exposes no inject hook.
  // Two regimes, mirroring the drag handler above:
  //  • normal buffer  -> xterm owns the scrollback; scrollToTop() is a true jump,
  //    and viewportY tells us whether there's anything above (hide at the top).
  //  • alt buffer / mouse-tracking (Claude Code, Codex, aider, vim, less, ...) ->
  //    the APP owns its scrollback, so scrollToTop() can't reach it. Send the same
  //    wheel notches a drag produces. We can't query the app's scroll position, so
  //    the button always shows there and the jump is a generous burst.
  (function scrollTopBtn() {
    var btn = document.createElement('div');
    btn.id = 'ao-scrolltop';
    btn.setAttribute('aria-label', 'Scroll to top');
    btn.innerHTML = '↑'; // up arrow
    var s = btn.style;
    s.position = 'fixed'; s.right = '12px'; s.bottom = '12px';
    s.width = '36px'; s.height = '36px'; s.lineHeight = '36px';
    s.textAlign = 'center'; s.borderRadius = '18px';
    s.background = 'rgba(20,28,40,0.72)'; s.color = '#dbe4f0';
    s.fontSize = '18px'; s.fontWeight = '600';
    s.border = '1px solid rgba(120,140,170,0.35)';
    s.boxShadow = '0 2px 8px rgba(0,0,0,0.4)';
    s.webkitBackdropFilter = 'blur(6px)'; s.backdropFilter = 'blur(6px)';
    s.zIndex = '2147483647'; s.display = 'none'; s.cursor = 'pointer';
    s.pointerEvents = 'auto'; s.userSelect = 'none'; s.webkitUserSelect = 'none';
    (document.getElementById('terminal') || document.body).appendChild(btn);

    function atTop() {
      try { var b = term().buffer.active; return !b || b.viewportY <= 0; }
      catch (_) { return true; }
    }
    function update() {
      // When the app drives scrolling we can't read its position, so always offer
      // the button; otherwise hide it once xterm's viewport is at the top.
      var show = appDrivesScroll() ? true : !atTop();
      btn.style.display = show ? 'block' : 'none';
    }
    // Walk the app's scrollback up with the same wheel notches a drag produces,
    // chunked across frames so a few hundred events don't block the renderer.
    var TOP_BURST = 400, BURST_CHUNK = 25;
    function burstUp() {
      var el = document.querySelector('.xterm');
      var r = el ? el.getBoundingClientRect() : null;
      var cx = r ? r.left + r.width / 2 : 0;
      var cy = r ? r.top + r.height / 2 : 0;
      var left = TOP_BURST;
      (function step() {
        for (var i = 0; i < BURST_CHUNK && left > 0; i++, left--) wheelTick(true, cx, cy);
        if (left > 0) requestAnimationFrame(step);
      })();
    }
    // Tap -> back to the oldest output. Swallow the event so nothing else reacts.
    function go(e) {
      if (e.stopPropagation) e.stopPropagation();
      if (e.cancelable && e.preventDefault) e.preventDefault();
      if (appDrivesScroll()) burstUp();
      else { try { term().scrollToTop(); } catch (_) {} }
      update();
    }
    btn.addEventListener('touchstart', go, { capture: true });
    btn.addEventListener('click', go, { capture: true });

    // Re-evaluate on xterm scroll and buffer switches; poll as a cheap fallback
    // for transitions those miss (our drag handler moves the viewport directly).
    (function wire() {
      var T = term();
      if (T && T.onScroll) {
        T.onScroll(update);
        try { if (T.buffer && T.buffer.onBufferChange) T.buffer.onBufferChange(update); } catch (_) {}
        update();
      } else { setTimeout(wire, 200); }
    })();
    setInterval(update, 500);
  })();

  // Disable the WebView's hidden textarea so it can NEVER show a keyboard or
  // steal first-responder from the RN input. RN handles all keyboard I/O.
  function harden() {
    var t = document.querySelector('.xterm-helper-textarea');
    if (t) {
      t.disabled = true;
      t.setAttribute('inputmode', 'none');
      t.setAttribute('readonly', 'readonly');
      t.setAttribute('autocorrect', 'off');
      t.setAttribute('autocapitalize', 'off');
      t.setAttribute('autocomplete', 'off');
      t.setAttribute('spellcheck', 'false');
    }
  }
  harden(); setTimeout(harden, 400); setTimeout(harden, 1500);
  setInterval(harden, 3000); // keep it disabled if xterm recreates it
  true;
})();
true;
`;

export function terminalEnhanceScript(platform: "ios" | "android"): string {
	return `var IS_ANDROID=${platform === "android"};\n${TERMINAL_ENHANCE_JS}`;
}
