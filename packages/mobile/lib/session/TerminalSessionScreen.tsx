import { XtermJsWebView, type XtermWebViewHandle } from "@fressh/react-native-xtermjs-webview";
import { Stack, useLocalSearchParams, useNavigation, useRouter } from "expo-router";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactElement } from "react";
import { Alert, Keyboard, LayoutAnimation, Platform, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { ApiError, getPreview, killSession, killSessionReviewer, sendMessage } from "../api";
import { isConfigured } from "../config";
import { terminalTheme, type Theme } from "../theme";
import { haptics } from "../haptics";
import { resetHeaderRightForSwap } from "../headerRightSwap";
import { MuxClient, type MuxStatus } from "../mux";
import { filesRoute } from "../files/navigation";
import { glassHeaderControl } from "../native-header-items";
import { Composer } from "./Composer";
import { dockInset, rootKeyboardPad } from "./keyboardInset";
import { KeyRow } from "./KeyRow";
import {
	REROUTED_NOTICE,
	routeForSend,
	terminalPayload,
	TERMINAL_MODE_NOTICE,
	TERMINAL_UNAVAILABLE_NOTICE,
	type SendTarget,
} from "./sendRoute";
import { useApp } from "../store";
import { useVoiceInput } from "../voice/useVoiceInput";
import { useTheme, useThemedStyles, useThemeState } from "../ThemeProvider";
import { closeShellTerminal } from "../chat/api";
import {
	interfaceSwitchAlert,
	type InterfaceSwitchRecheck,
	mobileInterfaceTransitionIsActive,
	mobileInterfaceTransitionIsCancellable,
	mobileInterfaceTransitionRecoveryMessage,
	useInterfaceTransition,
} from "./useInterfaceTransition";
import { terminalInterfaceFailureRecovery } from "./terminalInterfaceRecovery";
import { adjustTerminalViewport, fitTerminalViewport, scrollTerminalToTop, scrollTopReport } from "./terminalViewport";
import { ScrollTopButton } from "./ScrollTopButton";
import { isRuntimeGone, type RouteSession } from "./sessionRoute";
import { font, fontScaleCap, iconSize, radius, space, touchTarget, type } from "../tokens";
import { toolbarIcon } from "../toolbarIcon";
import { Spinner } from "../Spinner";
import { AppSymbol } from "../AppSymbol";
import { rowDividerWidth } from "../divider";
import { Button, EmptyState } from "../ui";
import type { PlatformSymbol } from "../symbols";
import { TerminalNotice, TerminalNoticeHost, type TerminalNoticeMessage, type TerminalNoticeTone } from "./TerminalNotice";
import { InterfaceSwitchConfirm, type InterfaceSwitchPolicy } from "./InterfaceSwitchConfirm";
import { backOr } from "../backNavigation";
import { userFacingError } from "../connectionError";
import { ARCHIVE_SESSION_CONFIRM } from "../worker-action-model";
import { isTerminalGoneError, TERMINAL_STATUS_LABEL, terminalErrorCopy, terminalExitedCopy } from "./terminalCopy";

const FONT_SIZE = 12;

/** A terminal with nothing running behind it: power, switched off. */
const TERMINATED_GLYPH: PlatformSymbol = { ios: "power", android: "power_settings_new" };

// Injected into the xterm WebView after load. xterm has its own touch handlers
// that scroll by discrete lines (the janky "1 line per swipe"). We intercept in
// the CAPTURE phase and stopPropagation so those handlers never fire, then drive
// the viewport's scrollTop in proportion to finger movement (+ momentum). Taps
// (no significant movement) are left alone so tap-to-focus / keyboard still work.
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
  // and the header menu's Smaller / Larger zoom between fit-to-width and 2x;
  // while zoomed, one finger pans the viewport (vertical overshoot spills into
  // scrollback) and double-tap toggles overview <-> 1:1.
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
  // The header menu's Smaller / Larger / Fit width call these through the
  // WebView's injectJavaScript. They change only the phone's CSS viewport: xterm
  // stays mounted and the daemon-owned PTY grid is never resized, so a
  // co-viewing desktop is unaffected.
  window.__aoAdjustTerminalZoom = function (direction) {
    var b = box(); if (!b || (direction !== 1 && direction !== -1)) return;
    setZoom(Z.s + direction * 0.2, b.contW / 2, b.contH / 2);
    Z.lastPan = Date.now();
  };
  // Back to the overview: the whole grid's width on screen, as a double-tap
  // out of a zoom does.
  window.__aoFitTerminalWidth = function () {
    setZoom(Z.min, 0, 0);
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

  // ---- Scroll to top -------------------------------------------------------
  // The button is React Native's, floating over the WebView; RN calls
  // __aoScrollTop() through injectJavaScript and learns whether to show the
  // button from the AO_SCROLLTOP reports below. Two regimes, mirroring the
  // drag handler above:
  //  • normal buffer  -> xterm owns the scrollback; scrollToTop() is a true jump,
  //    and viewportY tells us whether there's anything above (hide at the top).
  //  • alt buffer / mouse-tracking (Claude Code, Codex, aider, vim, less, ...) ->
  //    the APP owns its scrollback, so scrollToTop() can't reach it. Send the same
  //    wheel notches a drag produces. We can't query the app's scroll position, so
  //    the button always shows there and the jump is a generous burst.
  (function scrollTop() {
    function atTop() {
      try { var b = term().buffer.active; return !b || b.viewportY <= 0; }
      catch (_) { return true; }
    }
    // Reported through fressh's debug channel like FRESSH_DIMS, and only when
    // the answer changes, so a stream of output costs a comparison per write.
    var shown = null;
    function update() {
      // When the app drives scrolling we can't read its position, so always offer
      // the button; otherwise hide it once xterm's viewport is at the top.
      var show = appDrivesScroll() ? true : !atTop();
      if (show === shown || !window.ReactNativeWebView) return;
      shown = show;
      window.ReactNativeWebView.postMessage(
        JSON.stringify({ type: 'debug', message: 'AO_SCROLLTOP ' + (show ? 1 : 0) }));
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
    // The RN button's tap: back to the oldest output.
    window.__aoScrollTop = function () {
      if (appDrivesScroll()) burstUp();
      else { try { term().scrollToTop(); } catch (_) {} }
      update();
    };

    // Re-evaluate on xterm scroll and buffer switches, after each write (an app
    // turning mouse tracking on or off changes the answer without scrolling),
    // and when a finger lifts, since the drag handler moves the viewport itself.
    (function wire() {
      var T = term();
      if (T && T.onScroll) {
        T.onScroll(update);
        try { if (T.buffer && T.buffer.onBufferChange) T.buffer.onBufferChange(update); } catch (_) {}
        try { if (T.onWriteParsed) T.onWriteParsed(update); } catch (_) {}
        document.addEventListener('touchend', update, { passive: true });
        update();
      } else { setTimeout(wire, 200); }
    })();
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


function terminalInterfacePhaseLabel(phase?: string): string {
	switch (phase) {
		case "draining":
			return "Waiting for the current terminal turn to finish. New messages are queued safely.";
		case "source_stopping":
			return "Stopping the terminal controller before Chat starts.";
		case "source_stopped":
			return "Terminal controller stopped. The worktree and native conversation are unchanged.";
		case "target_starting":
			return "Resuming the same native conversation in Chat.";
		case "activating":
			return "Opening the Chat interface.";
		default:
			return "Checking that Chat can resume this agent's native conversation.";
	}
}

/**
 * `session` is what the route resolved when the board's lists do not hold this
 * id (see `sessionRouteView`); the lists still win whenever they have it, since
 * they are refreshed on every poll. A session from that lookup is not refreshed.
 * The shell route passes nothing.
 */
export default function TerminalScreen({ session: resolved }: { session?: RouteSession }) {
	const t = useTheme();
	const { scheme, style } = useThemeState();
	const styles = useThemedStyles(makeStyles);
	const params = useLocalSearchParams<{ id?: string; handleId?: string; projectId?: string; sessionId?: string; title?: string; kind?: string; hostId?: string }>();
	const shellOnly = Boolean(params.handleId);
	// A reviewer pane is attached by handle like a shell, but the daemon does not
	// own it as a shell terminal: closing it means stopping the worker's reviewer.
	const reviewerPane = params.kind === "reviewer" && Boolean(params.sessionId);
	const id = String(params.handleId ?? params.id ?? "");
	const sessionId = shellOnly ? String(params.sessionId ?? "") : id;
	const projectId = params.projectId ? String(params.projectId) : undefined;
	const router = useRouter();
	const navigation = useNavigation();
	const [headerRightReady, setHeaderRightReady] = useState(false);
	useLayoutEffect(
		() => resetHeaderRightForSwap(
			() => navigation.setOptions(glassHeaderControl("right")),
			() => setHeaderRightReady(true),
		),
		[navigation],
	);
	const insets = useSafeAreaInsets();

	// Leaving the screen. This route is cold-started with no back stack often
	// enough — a reload while on the terminal, or a deep link — that the rule lives
	// in one place; see `backOr`.
	const leave = useCallback(() => backOr(router), [router]);

	const xtermRef = useRef<XtermWebViewHandle | null>(null);
	// Theme changes still replace xterm. Retain bytes arriving between the old
	// WebView unmount and the replacement's onInitialized callback in wire order.
	const xtermReadyRef = useRef(false);
	const pendingOutputRef = useRef<Uint8Array[]>([]);
	const muxRef = useRef<MuxClient | null>(null);
	const openedRef = useRef(false);
	// Last grid size reported by the WebView's FitAddon, so we can send it to the
	// PTY the moment the terminal opens (dims may arrive before or after open).
	const lastDimsRef = useRef<{ cols: number; rows: number } | null>(null);
	// The authoritative grid the daemon told us the shared PTY is actually using
	// (driven by the largest/primary client — e.g. a co-viewing desktop). We render
	// THIS grid, not the phone's own fit, so the display matches the PTY and a
	// full-screen TUI doesn't mis-render. The WebView crops/scales it locally.
	const authRef = useRef<{ cols: number; rows: number } | null>(null);

	const [status, setStatus] = useState<MuxStatus>("connecting");
	const [size, setSize] = useState<{ cols: number; rows: number } | null>(null);
	// One notice at a time, the newest winning: an inline row on iOS, a snackbar
	// on Android (see TerminalNotice).
	const [notice, setNotice] = useState<TerminalNoticeMessage | null>(null);
	const noticeId = useRef(0);
	const showNotice = useCallback((text: string, tone: TerminalNoticeTone = "error") => {
		noticeId.current += 1;
		setNotice({ id: noticeId.current, text, tone });
	}, []);
	const clearNotice = useCallback(() => setNotice(null), []);
	const [dockHeight, setDockHeight] = useState(0);
	const [kbHeight, setKbHeight] = useState(0); // iOS: space to reserve for keyboard
	const [kbVisible, setKbVisible] = useState(false); // both platforms
	const [msg, setMsg] = useState("");
	const [sending, setSending] = useState(false);
	const [sendTarget, setSendTarget] = useState<SendTarget>(shellOnly ? "terminal" : "agent");
	// A terminated session has no live PTY (the mux answers "Session not found").
	// Track that + the known status so we can offer Restore instead of a dead term.
	const [notFound, setNotFound] = useState(false);
	const [restoring, setRestoring] = useState(false);
	// Poll generated previews so the globe can advertise the optional App preview tab.
	const [preview, setPreview] = useState<{ entry: string; url: string } | null>(null);

	const { sessions, orchestrators, restore, refresh, config: activeConfig } = useApp();
	const known =
		sessions.find((s) => s.id === sessionId) ??
		orchestrators.find((o) => o.id === sessionId) ??
		(!shellOnly && resolved?.id === sessionId ? resolved : null);
	// Runtime handles are opaque. Native macOS PTYs are versioned (ptyhost-v1:),
	// so using the session id here would incorrectly route the attach to legacy
	// tmux. Older daemons omit terminalHandleId and retain the historical
	// session-id handle, which keeps the fallback backward-compatible.
	const terminalHandleId = shellOnly ? id : known?.terminalHandleId || id;
	const interfaceSwitch = useInterfaceTransition(activeConfig, shellOnly ? "" : sessionId, refresh);
	// Owned by the screen rather than the hook: the background poll calls the same
	// `refresh()`, so a hook-wide busy flag would let a poll tick disable the
	// user's own button and swallow the very tap the recheck exists to serve.
	// The ref is the guard (state updates are async); the state drives the spinner.
	const [rechecking, setRechecking] = useState(false);
	const recheckingRef = useRef(false);
	// A recheck can be in flight for up to REQUEST_TIMEOUT_MS, long enough for the
	// user to leave. Alerting from a screen they already navigated away from would
	// interrupt whatever they went to instead.
	const mountedRef = useRef(true);
	useEffect(() => {
		mountedRef.current = true;
		return () => {
			mountedRef.current = false;
		};
	}, []);
	// The status the hook holds right now, readable after an await. The tap's
	// closure is from the render it started in, which is the pre-tap status.
	const interfaceStatusRef = useRef(interfaceSwitch.status);
	interfaceStatusRef.current = interfaceSwitch.status;
	const interfaceTransitionActive = mobileInterfaceTransitionIsActive(interfaceSwitch.transition);
	const interfaceRecoveryMessage = mobileInterfaceTransitionRecoveryMessage(interfaceSwitch.transition);
	const interfaceTransitionNotice =
		!interfaceTransitionActive &&
		!interfaceSwitch.transition?.noticeAcknowledgedAt &&
		(interfaceSwitch.transition?.phase === "failed" ||
			interfaceSwitch.transition?.phase === "recovery_required")
			? interfaceSwitch.transition
			: undefined;
	const interfaceTransitionRecovered =
		interfaceTransitionNotice?.phase === "recovery_required" &&
		interfaceTransitionNotice.errorCode === "DAEMON_RESTARTED";
	const interfaceTransitionNoticeText =
		interfaceTransitionNotice?.errorDetail ||
		(interfaceTransitionRecovered
			? "Orchestrator restored the session in its last committed interface."
			: interfaceTransitionNotice?.phase === "recovery_required"
				? "The interface switch needs recovery before more work is sent."
				: "The interface switch failed; Terminal UI remains available.");
	const interfaceFailureRecovery = useMemo(
		() => terminalInterfaceFailureRecovery(interfaceSwitch.transition),
		[interfaceSwitch.transition?.errorCode],
	);
	const interfaceBusy = Boolean(
		known &&
		(known.status === "working" ||
			known.status === "needs_input" ||
			known.activity === "active" ||
			known.activity === "waiting_input" ||
			known.activity === "blocked"),
	);
	const dead = notFound || (!shellOnly && known ? isRuntimeGone(known) : false);
	// What counts as a live preview: any file the daemon surfaces (an .html build, or
	// a generated doc like plan.md / a report) EXCEPT a repo's README, which the
	// detector's markdown fallback always matches on a fresh checkout. Filtering the
	// README out keeps the globe's green dot meaningful — it means "there's something
	// the agent produced to view", not just "this repo has a README".
	const previewBase = (preview?.entry ?? "").split("/").pop() ?? "";
	const isReadme = /^readme\.(md|markdown)$/i.test(previewBase);
	const hasPreview = !!preview && !isReadme;

	// Neither platform shrinks the layout for the keyboard: iOS never has, and on
	// Android edge-to-edge (edgeToEdgeEnabled) defeats windowSoftInputMode=adjustResize
	// so the window no longer resizes - the keyboard just draws over our content.
	// So reserve the keyboard on BOTH platforms and let the screen pad itself above
	// it, else the input dock (and its send button) hide behind it. What the event
	// reports is not the same quantity on the two platforms — rootKeyboardPad owns
	// that difference. This is the ONLY place the keyboard height is applied — the
	// dock adds nothing on top of it (see dockInset), because doing both is what
	// made the bar kick.
	// Deliberately still on the platform listeners, unlike the spawn sheet, the
	// Workers board and the chat screen, which all moved to keyboard-controller's
	// useKeyboardState.
	//
	// That hook subscribes to keyboardWillShow and keyboardDidHide only. This
	// screen needs keyboardDidShow as well — see the corrector below, which exists
	// because willShow reports a height that still includes the accessory bar we
	// hide. Migrating here would reinstate exactly the gap that corrector removes,
	// and would lose the iOS-only didHide backup whose absence on Android is itself
	// a fix (registering it there subscribed the same handler twice and collapsed
	// the dock). The gain would be earlier Android events on the one screen that
	// reserves keyboard space by hand, which is not worth reopening two fixed bugs.
	useEffect(() => {
		const isIOS = Platform.OS === "ios";
		const showEvt = isIOS ? "keyboardWillShow" : "keyboardDidShow";
		const hideEvt = isIOS ? "keyboardWillHide" : "keyboardDidHide";
		// Ride the system keyboard curve. Without this the relayout lands as an
		// instant jump while the keyboard is still sliding, which is most of why
		// the dock looked like it kicked.
		const animate = (duration?: number) =>
			LayoutAnimation.configureNext({
				duration: duration || 250,
				update: { type: LayoutAnimation.Types.keyboard },
			});
		const show = Keyboard.addListener(showEvt, (e) => {
			animate(e.duration);
			setKbVisible(true);
			setKbHeight(e.endCoordinates.height);
		});
		const hide = Keyboard.addListener(hideEvt, (e) => {
			animate(e?.duration);
			setKbVisible(false);
			setKbHeight(0);
		});
		// willShow can report a height that still includes the accessory bar we hid,
		// leaving a gap. didShow reports the actual final frame - use it to correct.
		// Guarded so the common case where the two agree is a no-op instead of a
		// second visible nudge.
		const didShow = isIOS
			? Keyboard.addListener("keyboardDidShow", (e) => {
					const next = e.endCoordinates.height;
					setKbHeight((h) => {
						if (h === next) return h;
						animate(e.duration);
						return next;
					});
				})
			: null;
		// Backup: guarantee the reserved space collapses even if willHide is missed.
		// iOS-only, like didShow above — Android's hide listener is already bound to
		// keyboardDidHide, so registering this there subscribed the same handler to
		// the same event twice and collapsed the dock on both.
		const didHide = isIOS
			? Keyboard.addListener("keyboardDidHide", () => {
					setKbVisible(false);
					setKbHeight(0);
				})
			: null;
		return () => {
			show.remove();
			hide.remove();
			didShow?.remove();
			didHide?.remove();
		};
	}, []);

	// The title is just the short id; everything else the bar holds is a toolbar item.
	useLayoutEffect(() => {
		navigation.setOptions({
			title: shellOnly ? (params.title || "Worktree shell") : id.length > 22 ? `${id.slice(0, 20)}...` : id,
		});
	}, [navigation, id, params.title, shellOnly]);

	// Load config, then connect the mux socket.
	// Rebuilt whenever the active endpoint changes, not only when the session
	// does. The app re-races its endpoints when the network moves — losing
	// Wi-Fi hands the session to Tailscale or the tunnel — and a mux still
	// pointed at the previous address stays disconnected on a blank screen
	// until the screen is closed and reopened.
	useEffect(() => {
		let disposed = false;
		(async () => {
			// A selected machine without a verified connection has no terminal.
			// The global saved config may belong to a different machine.
			const config = activeConfig;
			if (disposed) return;
			if (!config || !isConfigured(config)) return;

			const mux = new MuxClient(config, {
				onStatus: (s) => setStatus(s),
				onTerminalData: (tid, bytes) => {
					if (tid !== terminalHandleId) return;
					if (!xtermReadyRef.current || !xtermRef.current) {
						pendingOutputRef.current.push(bytes.slice());
						return;
					}
					xtermRef.current.write(bytes);
				},
				onTerminalExited: (tid, code) => {
					if (tid === terminalHandleId) {
						showNotice(terminalExitedCopy(code), code === 0 ? "info" : "error");
						setNotFound(true);
					}
				},
				onTerminalError: (tid, msg) => {
					if (tid !== terminalHandleId) return;
					// A missing PTY means the session is terminated - offer Restore
					// instead of surfacing it as a raw error notice.
					if (isTerminalGoneError(msg)) setNotFound(true);
					else showNotice(terminalErrorCopy(msg));
				},
				onTerminalResize: (tid, cols, rows) => {
					if (tid !== terminalHandleId) return;
					// Render the daemon's authoritative grid exactly. The WebView applies
					// only local crop/zoom, so the phone cannot disturb a desktop owner.
					authRef.current = { cols, rows };
					setSize({ cols, rows });
					xtermRef.current?.resize({ cols, rows });
				},
			});
			muxRef.current = mux;
			mux.connect();
		})();
		return () => {
			disposed = true;
			muxRef.current?.disconnect();
			muxRef.current = null;
			xtermReadyRef.current = false;
			pendingOutputRef.current = [];
		};
		// The store preserves config identity when the endpoint and credential
		// did not change, so this also handles two hosts sharing one URL.
	}, [terminalHandleId, activeConfig]);

	// Whether there is older output to jump to, as the WebView last reported it.
	const [canScrollTop, setCanScrollTop] = useState(false);

	useLayoutEffect(() => {
		xtermReadyRef.current = false;
		// The replacement WebView reports its own answer once it is up.
		setCanScrollTop(false);
	}, [scheme, style]);

	// Poll the daemon's on-demand preview detector while the terminal is open, just
	// to keep `preview` current for the globe button. We never auto-open the overlay
	// (see the note by the state above): the detector's markdown fallback matches a
	// repo README, so auto-popping would surface a blank/unbuilt page.
	useEffect(() => {
		if (shellOnly) return;
		if (!activeConfig || !isConfigured(activeConfig)) return;
		let cancelled = false;
		let timer: ReturnType<typeof setTimeout> | null = null;
		const tick = async () => {
			try {
				const p = await getPreview(activeConfig, id);
				if (cancelled) return;
				setPreview(p ? { entry: p.entry, url: p.url } : null);
			} catch {
				/* transient - keep polling */
			}
			if (!cancelled) timer = setTimeout(tick, 5000);
		};
		tick();
		return () => {
			cancelled = true;
			if (timer) clearTimeout(timer);
		};
	}, [activeConfig, id, shellOnly]);

	// The WebView reports the phone's NATURAL fit (proposeDimensions, measure-only).
	// We forward it to the daemon as this client's requested size — used only when
	// the phone is the sole viewer (a co-viewing desktop, being primary, wins). The
	// render grid comes back via onTerminalResize; until it does, render the fit so
	// the terminal isn't blank.
	const applyDims = useCallback(
		(cols: number, rows: number) => {
			lastDimsRef.current = { cols, rows };
			if (openedRef.current) muxRef.current?.resize(terminalHandleId, cols, rows, projectId);
			if (!authRef.current) {
				setSize({ cols, rows });
				xtermRef.current?.resize({ cols, rows });
			}
		},
		[terminalHandleId, projectId],
	);

	// fressh routes WebView {type:'debug'} messages to logger.log(prefix, message).
	// We piggyback on it for the FRESSH_DIMS and AO_SCROLLTOP reports (using a
	// custom onMessage would clobber fressh's own bridge).
	const logger = useMemo(
		() => ({
			log: (...args: unknown[]) => {
				const m = args[args.length - 1];
				if (typeof m === "string" && m.startsWith("FRESSH_DIMS ")) {
					const parts = m.split(" ");
					const cols = parseInt(parts[1], 10);
					const rows = parseInt(parts[2], 10);
					if (cols > 0 && rows > 0) applyDims(cols, rows);
					return;
				}
				const scrollTop = scrollTopReport(m);
				if (scrollTop !== undefined) setCanScrollTop(scrollTop);
			},
		}),
		[applyDims],
	);

	const onInitialized = useCallback(() => {
		xtermReadyRef.current = true;
		const pending = pendingOutputRef.current;
		pendingOutputRef.current = [];
		for (const bytes of pending) xtermRef.current?.write(bytes);
		// A fresh xterm (first mount, or a remount after a font-zoom) starts at its
		// default grid — restore the daemon's authoritative grid onto it so it keeps
		// mirroring the shared PTY rather than snapping to the default.
		if (authRef.current) xtermRef.current?.resize(authRef.current);
		// Guard against a second open if the WebView re-fires onInitialized (e.g.
		// remount on orientation change) - that would attach the PTY twice.
		if (openedRef.current) return;
		openedRef.current = true;
		muxRef.current?.openTerminal(terminalHandleId, projectId);
		// If the FitAddon already reported dims before open, send them to the PTY now.
		const d = lastDimsRef.current;
		if (d) muxRef.current?.resize(terminalHandleId, d.cols, d.rows, projectId);
	}, [terminalHandleId, projectId]);

	const onData = useCallback(
		(data: string) => {
			muxRef.current?.sendInput(terminalHandleId, data, projectId);
		},
		[terminalHandleId, projectId],
	);

	const sendKey = useCallback(
		(seq: string) => {
			muxRef.current?.sendInput(terminalHandleId, seq, projectId);
		},
		[terminalHandleId, projectId],
	);

	// Send the composed text to the selected route. The agent route can still
	// auto-engage the terminal route when the daemon reports a blocked prompt.
	//
	// AO's /send is the right route for a message: the daemon hands it to the
	// harness and submits it. But it sanitises control characters and refuses
	// outright while a session is paused on a permission prompt — answering 409
	// SESSION_AWAITING_DECISION with the advice "answer it in the session
	// terminal first". So on exactly that code we do what it says and write the
	// line to the PTY instead. `shouldRetryOnTerminal` keeps that narrow: a
	// terminated or exited session has no PTY, and rerouting there would swallow
	// the user's text and report success.
	//
	// The reroute is announced in a notice rather than done silently, and the
	// text stays in the field on any failure we did NOT handle, so nothing typed
	// is ever lost.
	const sendPrompt = useCallback(async () => {
		const text = msg.trim();
		if (!text) return;
		if (routeForSend(sendTarget) === "terminal") {
			if (muxRef.current && status === "open") {
				muxRef.current.sendInput(terminalHandleId, terminalPayload(text), projectId);
				haptics.success();
				setMsg("");
				showNotice(TERMINAL_MODE_NOTICE, "info");
			} else {
				haptics.error();
				showNotice(TERMINAL_UNAVAILABLE_NOTICE);
			}
			return;
		}
		setSending(true);
		try {
			if (!activeConfig) throw new Error("No active machine connection");
			await sendMessage(activeConfig, id, text);
			haptics.success();
			setMsg("");
		} catch (e) {
			const failure = e instanceof ApiError ? e : null;
			// Only reroute onto a mux we actually hold open — otherwise the write is
			// a no-op and we would clear the field having sent nothing.
			if (routeForSend(sendTarget, failure) === "terminal" && muxRef.current && status === "open") {
				muxRef.current.sendInput(terminalHandleId, terminalPayload(text), projectId);
				haptics.success();
				setMsg("");
				setSendTarget("terminal");
				showNotice(REROUTED_NOTICE, "info");
			} else {
				haptics.error();
				showNotice(`Message not sent. ${userFacingError(e)}`);
			}
		} finally {
			setSending(false);
		}
	}, [msg, sendTarget, activeConfig, id, terminalHandleId, projectId, status, showNotice]);

	// Push-to-talk dictation, captured on the PHONE rather than by the harness.
	//
	// Driving a harness's own voice mode from here (Claude Code's /voice) looks
	// easy — the key row already sends arbitrary bytes to the PTY — but it records
	// through the *daemon host's* microphone, which is a machine the user is not
	// sitting at. It would also only ever work for the harnesses that ship a voice
	// mode at all.
	//
	// Capturing here inverts both: the transcript is just text, so it leaves
	// through sendPrompt like anything typed, and every harness gets it for free.
	const voice = useVoiceInput({
		onTranscript: useCallback((text: string) => {
			// Land in the composer as an editable draft rather than sending. This
			// text reaches an agent with tool access and speech-to-text mishears code
			// vocabulary often enough that silent submission isn't acceptable.
			//
			// The composer is always mounted now, so there is nothing to open — and
			// nothing focuses the field either, which would pop the keyboard over the
			// terminal mid-phrase.
			// Append, so several held phrases build one prompt (the same way holding
			// the key again appends in Claude Code's own dictation).
			setMsg((m) => (m ? `${m} ${text}` : text));
			haptics.success();
		}, []),
	});

	// Recognition failures surface as a notice rather than new UI.
	useEffect(() => {
		if (voice.error) showNotice(voice.error);
	}, [voice.error, showNotice]);

	// Preview is one session-level destination: the live desktop browser first,
	// with the generated app/document preview available as a second tab.
	const toggleBrowser = useCallback(() => {
		router.push({
			pathname: "/preview/[id]",
			params: {
				id: sessionId,
				hostId: params.hostId,
				previewUrl: known && "previewUrl" in known ? known.previewUrl ?? undefined : undefined,
			},
		});
	}, [known, params.hostId, router, sessionId]);

	const startInterfaceSwitch = useCallback(
		async (policy: "drain" | "interrupt") => {
			try {
				await interfaceSwitch.start("chat", policy);
				clearNotice();
			} catch (cause) {
				showNotice(`Couldn't switch interface. ${userFacingError(cause)}`);
			}
		},
		[clearNotice, interfaceSwitch, showNotice],
	);

	// The detail of the "Switch to Chat?" question while it is being asked.
	const [switchQuestion, setSwitchQuestion] = useState<string | null>(null);
	const requestInterfaceSwitch = useCallback(async () => {
		// A second tap would start a second recheck against a link already slow
		// enough for the first one to be visible. `starting` and an active
		// transition are guarded too, so the button blocks exactly what it
		// announces as busy — otherwise a tap during the start POST fires a
		// duplicate the daemon answers with 409.
		if (recheckingRef.current || interfaceSwitch.starting || interfaceTransitionActive) return;
		haptics.tap();
		let status = interfaceSwitch.status;
		let recheck: InterfaceSwitchRecheck = { outcome: "answered" };
		if (!status?.supported) {
			// A tap can land between two polls, or before the first answer: ask
			// once more before telling the user it is unavailable.
			recheckingRef.current = true;
			setRechecking(true);
			try {
				const result = await interfaceSwitch.refresh();
				if (!mountedRef.current) return;
				if (!result) recheck = { outcome: "not-attempted" };
				// A superseded answer is not a verdict, same rule as the hook's own
				// loop: the readiness tick can overtake a slow tap request, and acting
				// on the tap's payload would alert "Couldn't reach AO" (or "Not ready
				// yet" off an older body) while the button had just enabled itself.
				// The newer request owns the answer; use whatever the hook adopted.
				else if (result.stale) status = interfaceStatusRef.current;
				else if (result.ok) status = result.status;
				else recheck = { outcome: "failed", error: result.error, status: result.status };
			} finally {
				recheckingRef.current = false;
				if (mountedRef.current) setRechecking(false);
			}
		}
		if (!status?.supported) {
			const { title, message } = interfaceSwitchAlert(status, interfaceSwitch.error, recheck);
			Alert.alert(title, message);
			return;
		}
		// The guard at the top saw the pre-tap render. The fresh answer can carry
		// a transition someone else started while the recheck was in flight (the
		// desktop button, say); starting another gets a 409 and a "Switch failed"
		// banner for a switch that is in fact proceeding. The card takes over and
		// explains it — but after a visible spinner, returning in silence reads
		// as a tap that was dropped, so confirm the outcome the user asked for
		// actually happened rather than leaving the card to arrive unannounced.
		if (mobileInterfaceTransitionIsActive(status.transition)) {
			haptics.success();
			return;
		}
		if (!interfaceBusy) {
			void startInterfaceSwitch("drain");
			return;
		}
		// Asked by InterfaceSwitchConfirm: an action sheet on iOS, the system
		// dialog on Android.
		setSwitchQuestion(
			known?.activity === "waiting_input" || known?.activity === "blocked"
				? "This turn is waiting for your input. Finish waits for your answer; stop cancels it and switches now."
				: "Keep the same Orchestrator session, worktree, and native agent conversation.",
		);
	}, [interfaceBusy, interfaceSwitch, interfaceTransitionActive, known?.activity, startInterfaceSwitch]);
	const closeSwitchQuestion = useCallback(() => setSwitchQuestion(null), []);
	const answerSwitchQuestion = useCallback((policy: InterfaceSwitchPolicy) => {
		setSwitchQuestion(null);
		void startInterfaceSwitch(policy);
	}, [startInterfaceSwitch]);

	// The poll keeps retrying on its own at up to 8s; this is for the user who can
	// see the network is back and does not want to wait for the tick. The header
	// button is disabled for the whole transition, so this is the only tap that
	// re-asks while one is live. Same guard as the header recheck: it is the same
	// request, and two of them against a slow link is what the guard prevents.
	const retryInterfaceCheck = useCallback(async () => {
		if (recheckingRef.current) return;
		recheckingRef.current = true;
		setRechecking(true);
		try {
			await interfaceSwitch.refresh();
		} finally {
			recheckingRef.current = false;
			if (mountedRef.current) setRechecking(false);
		}
	}, [interfaceSwitch]);

	const requestInterfaceFailureRecovery = useCallback(() => {
		if (!interfaceFailureRecovery) return;
		interfaceFailureRecovery.confirm((policy) => void startInterfaceSwitch(policy));
	}, [interfaceFailureRecovery, startInterfaceSwitch]);

	const openFiles = useCallback(() => {
		router.push(filesRoute(sessionId, params.hostId, params.title));
	}, [params.hostId, params.title, router, sessionId]);

	const confirmKill = useCallback(() => {
		const doKill = async () => {
			try {
				if (!activeConfig) throw new Error("No active machine connection");
				if (reviewerPane) await killSessionReviewer(activeConfig, String(params.sessionId));
				else if (shellOnly) await closeShellTerminal(activeConfig, id);
				else await killSession(activeConfig, id);
				haptics.success();
				leave();
			} catch (e) {
				haptics.error();
				showNotice(`${reviewerPane ? "Couldn't stop the reviewer" : shellOnly ? "Couldn't close the shell" : "Couldn't archive the session"}. ${userFacingError(e)}`);
			}
		};
		if (Platform.OS === "web") {
			doKill();
			return;
		}
		if (reviewerPane) {
			haptics.warning();
			Alert.alert("Stop reviewer session?", "This closes the persistent reviewer and cancels any review it is currently running. Review history is preserved.", [
				{ text: "Keep reviewer", style: "cancel" },
				{ text: "Stop reviewer", style: "destructive", onPress: doKill },
			]);
			return;
		}
		haptics.warning();
		Alert.alert(shellOnly ? "Close shell?" : ARCHIVE_SESSION_CONFIRM.title, shellOnly ? "This stops the worktree shell." : ARCHIVE_SESSION_CONFIRM.message, [
			{ text: shellOnly ? "Cancel" : ARCHIVE_SESSION_CONFIRM.cancel, style: "cancel" },
			{ text: shellOnly ? "Close" : ARCHIVE_SESSION_CONFIRM.confirm, style: "destructive", onPress: doKill },
		]);
	}, [activeConfig, id, leave, params.sessionId, reviewerPane, shellOnly, showNotice]);

	// Restore a terminated session: the daemon re-attaches its worktree agent and
	// its PTY comes back, so we re-open the terminal once restore succeeds.
	const onRestore = useCallback(async () => {
		setRestoring(true);
		try {
			await restore(id);
			clearNotice();
			setNotFound(false);
			openedRef.current = false;
			// Give the daemon a moment to bring the PTY up, then re-attach.
			setTimeout(() => {
				if (openedRef.current) return;
				openedRef.current = true;
				muxRef.current?.openTerminal(terminalHandleId, projectId);
				const d = lastDimsRef.current;
				if (d) muxRef.current?.resize(terminalHandleId, d.cols, d.rows, projectId);
			}, 1200);
		} catch (e) {
			showNotice(`Couldn't restore the session. ${userFacingError(e)}`);
		} finally {
			setRestoring(false);
		}
	}, [restore, id, terminalHandleId, projectId, clearNotice, showNotice]);

	const xtermOptions = useMemo(
		() => ({
			fontSize: FONT_SIZE,
			cursorBlink: true,
			scrollback: 5000,
			// Move more rows per swipe so touch scrolling feels responsive.
			scrollSensitivity: 3,
			fastScrollSensitivity: 8,
			// The terminal's own palette, not the product one: agent TUIs own the
			// meaning of the ANSI slots.
			theme: terminalTheme(scheme, style),
			// Agent TUIs pick their colours assuming a dark canvas, so on the light
			// theme they can land white-on-white — the prompt row is exactly that
			// once its background band is collapsed away. xterm nudges any pair that
			// falls below this ratio until it is readable, which is cheaper and far
			// more reliable than trying to anticipate every colour a TUI might emit.
			minimumContrastRatio: 4.5,
		}),
		// `scheme` and `style` matter: without them the terminal keeps the palette
		// it was built with and stays dark after a theme switch.
		[scheme, style],
	);

	// Adjust only the phone's CSS viewport. The xterm renderer, mux attachment,
	// replay buffer, and daemon-owned PTY dimensions all remain untouched.
	const zoom = useCallback((delta: number) => {
		adjustTerminalViewport(xtermRef.current, delta > 0 ? 1 : -1);
	}, []);
	const fitWidth = useCallback(() => {
		fitTerminalViewport(xtermRef.current);
	}, []);
	const scrollToTop = useCallback(() => {
		scrollTerminalToTop(xtermRef.current);
	}, []);

	// The header items reach their handlers through a ref. The transition hook
	// returns a new object every render, so `requestInterfaceSwitch` is new every
	// render too, and a new toolbar element registers the header's options again:
	// keyed on it, the header would rebuild on every keystroke.
	const headerActions = useRef({ requestInterfaceSwitch, toggleBrowser, openFiles, confirmKill, zoom, fitWidth });
	headerActions.current = { requestInterfaceSwitch, toggleBrowser, openFiles, confirmKill, zoom, fitWidth };
	const chatSwitchBusy = rechecking || interfaceSwitch.starting;
	const chatSwitchReady = Boolean(interfaceSwitch.status?.supported);
	const grid = size ? `${size.cols} × ${size.rows}` : undefined;
	const headerItems = useMemo(() => {
		// Held back for the frame `resetHeaderRightForSwap` keeps the slot empty,
		// so the bar never draws these over what the Chat renderer left there.
		if (!headerRightReady) return null;
		const ios = Platform.OS === "ios";
		// A dead session's way back is Restore, in the overlay; a dead shell can
		// still be closed.
		const canClose = shellOnly || !dead;
		const close = reviewerPane
			? { label: "Stop reviewer", symbol: "stop.circle" as const }
			: shellOnly
				? { label: "Close shell", symbol: "xmark" as const }
				: { label: "Archive session", symbol: "archivebox" as const };
		// Menu groups, separated the way each platform separates them: the first
		// sits at the top, each later one in an inline section (a divider on
		// Android), so no menu opens on a divider.
		const groups: ReactElement[][] = [];
		// iOS heads the menu with the shared grid's size; an M3 menu has no title,
		// so Android lists it as an inert first row.
		if (grid && !ios && !dead) groups.push([<Stack.Toolbar.MenuAction key="grid" disabled>{`Grid ${grid}`}</Stack.Toolbar.MenuAction>]);
		// Zoom only the mobile viewport; the shared PTY grid stays unchanged.
		// Pinch and double-tap zoom too.
		if (!dead) {
			groups.push([
				<Stack.Toolbar.MenuAction key="smaller" icon={ios ? "textformat.size.smaller" : undefined} onPress={() => headerActions.current.zoom(-1)}>Smaller</Stack.Toolbar.MenuAction>,
				<Stack.Toolbar.MenuAction key="larger" icon={ios ? "textformat.size.larger" : undefined} onPress={() => headerActions.current.zoom(1)}>Larger</Stack.Toolbar.MenuAction>,
				<Stack.Toolbar.MenuAction key="fit" icon={ios ? "arrow.left.and.right" : undefined} onPress={() => headerActions.current.fitWidth()}>Fit width</Stack.Toolbar.MenuAction>,
			]);
		}
		if (canClose) {
			groups.push([
				<Stack.Toolbar.MenuAction key="close" destructive icon={ios ? close.symbol : undefined} onPress={() => headerActions.current.confirmKill()}>
					{close.label}
				</Stack.Toolbar.MenuAction>,
			]);
		}
		const menuItems = groups.flatMap((group, index) =>
			index === 0 ? group : [<Stack.Toolbar.Menu key={`group-${index}`} inline>{group}</Stack.Toolbar.Menu>],
		);
		return (
			<Stack.Toolbar placement="right">
				{/* A spinner rather than a dimmed glyph: the recheck only runs while
				    the glyph is already faint, so fading it further reads as broken.
				    `starting` shows it too — that POST is disabled-but-silent for up
				    to REQUEST_TIMEOUT_MS otherwise, the same dead-button shape the
				    recheck spinner exists to remove. */}
				{shellOnly ? null : chatSwitchBusy ? (
					<Stack.Toolbar.View hidesSharedBackground>
						<View style={styles.headerSpinner}>
							<Spinner color={t.accent} accessibilityLabel="Checking whether Chat can open" />
						</View>
					</Stack.Toolbar.View>
				) : (
					<Stack.Toolbar.Button
						icon={toolbarIcon("chat")}
						accessibilityLabel="Open Chat interface"
						disabled={interfaceTransitionActive}
						// Faint until the daemon says Chat can resume this session. It stays
						// tappable: the tap asks again and explains what is missing.
						tintColor={chatSwitchReady ? undefined : t.textFaint}
						onPress={() => void headerActions.current.requestInterfaceSwitch()}
					/>
				)}
				{shellOnly ? null : (
					<Stack.Toolbar.Button
						icon={toolbarIcon("globe")}
						accessibilityLabel={hasPreview ? "Open preview, preview ready" : "Open preview"}
						// iOS draws a bar-button badge only from iOS 26, and an empty one is
						// a blank capsule there, so on iOS the globe itself turns green.
						tintColor={hasPreview && ios ? t.green : undefined}
						onPress={() => headerActions.current.toggleBrowser()}
					>
						{hasPreview && !ios ? <Stack.Toolbar.Badge style={{ backgroundColor: t.green }} /> : null}
					</Stack.Toolbar.Button>
				)}
				{shellOnly ? null : (
					<Stack.Toolbar.Button
						icon={toolbarIcon("fileSearch")}
						accessibilityLabel="Session files and changes"
						onPress={() => headerActions.current.openFiles()}
					/>
				)}
				{menuItems.length ? (
					<Stack.Toolbar.Menu icon={toolbarIcon("more")} accessibilityLabel="Terminal actions" title={grid && ios && !dead ? `Grid ${grid}` : undefined}>
						{menuItems}
					</Stack.Toolbar.Menu>
				) : null}
			</Stack.Toolbar>
		);
	}, [chatSwitchBusy, chatSwitchReady, dead, grid, hasPreview, headerRightReady, interfaceTransitionActive, reviewerPane, shellOnly, styles, t]);

	const webViewOptions = useMemo(
		() => ({
			// Removes the extra "< > Done" / autofill bar iOS shows above the keyboard.
			hideKeyboardAccessoryView: true,
			// Custom drag/momentum scroll + input hardening (see TERMINAL_ENHANCE_JS).
			// Prepend the platform flag the enhance script branches on for scrolling.
			injectedJavaScript: `var IS_ANDROID=${Platform.OS === "android"};\n${TERMINAL_ENHANCE_JS}`,
			// NOTE: do NOT force androidLayerType:"hardware" here. xterm renders into a
			// <canvas>, and a hardware layer makes the Android WebView's render process
			// composite/crash blank on many devices (black terminal, no dims ever
			// reported). Leaving it at the default keeps the canvas visible.
			nestedScrollEnabled: true,
			// Surface an Android WebView render-process crash instead of a silent black
			// screen, so the user can tell the terminal died vs. never loaded.
			onRenderProcessGone: () => showNotice("The terminal view stopped working. Go back and open the session again."),
		}),
		[showNotice],
	);

	if (activeConfig && !isConfigured(activeConfig)) {
		return (
			<View style={styles.center}>
				{headerItems}
				<EmptyState icon="devices" title="No machine paired" />
			</View>
		);
	}

	// One inset for the whole dock. The root already reserves the keyboard, so the
	// dock owes nothing more while the keyboard is up — see dockInset.
	const bottomPad = dockInset(kbHeight, insets.bottom);
	// Android's reported kbHeight excludes the nav bar our root view draws under,
	// so rootKeyboardPad adds it back; on iOS it passes the height through.
	const rootPad = rootKeyboardPad(Platform.OS === "android" ? "android" : "ios", kbHeight, insets.bottom);

	return (
		<View style={[styles.screen, rootPad > 0 && { paddingBottom: rootPad }]}>
			{headerItems}
			<TerminalNotice notice={notice} onDismiss={clearNotice} />
			{interfaceTransitionNotice ? (
				// Not a transient notice: it waits for the user to acknowledge it, and
				// it can carry a recovery action, so it is a row on both platforms.
				<View style={[styles.transitionNotice, !interfaceTransitionRecovered && styles.transitionNoticeFailed]} accessibilityRole="alert">
					<View style={styles.transitionNoticeRow}>
						<AppSymbol
							name={interfaceTransitionRecovered ? "info" : "error"}
							size={iconSize.md}
							color={interfaceTransitionRecovered ? t.textSecondary : t.red}
						/>
						<Text maxFontSizeMultiplier={fontScaleCap.body} style={[styles.transitionNoticeText, !interfaceTransitionRecovered && styles.transitionNoticeTextFailed]}>
							{interfaceTransitionNoticeText}
							{interfaceSwitch.acknowledgeNoticeError
								? ` Couldn't dismiss: ${interfaceSwitch.acknowledgeNoticeError}`
								: ""}
						</Text>
					</View>
					<View style={styles.transitionNoticeActions}>
						{interfaceFailureRecovery ? (
							<Button variant="danger" title={interfaceFailureRecovery.actionLabel} onPress={requestInterfaceFailureRecovery} />
						) : null}
						<Button
							variant="ghost"
							title="Dismiss"
							loading={interfaceSwitch.acknowledgingNotice}
							onPress={() => {
								void interfaceSwitch
									.acknowledgeNotice(interfaceTransitionNotice.id)
									.catch(() => {});
							}}
						/>
					</View>
				</View>
			) : null}

			<View style={styles.termWrap}>
				<XtermJsWebView
					// Remount only on theme changes: xterm applies its palette at
					// construction, so already-painted rows otherwise keep old colours.
					key={`term-${style}-${scheme}`}
					ref={xtermRef}
					autoFit={false}
					xtermOptions={xtermOptions}
					webViewOptions={webViewOptions}
					logger={logger}
					onInitialized={onInitialized}
					onData={onData}
					style={{ flex: 1, backgroundColor: t.bgBase }}
				/>
				{/* The connection says something only while it is not live. It floats
				    over the grid rather than taking a row, so the grid keeps its
				    rows when the link comes and goes. */}
				{status !== "open" && !dead ? (
					<View pointerEvents="none" style={styles.statusSlot}>
						<View style={styles.statusPill} accessibilityLiveRegion="polite">
							{status === "error" ? <AppSymbol name="offline" size={iconSize.sm} color={t.red} /> : <Spinner size={iconSize.sm} />}
							<Text style={styles.statusText} numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.chrome}>
								{TERMINAL_STATUS_LABEL[status]}
							</Text>
						</View>
					</View>
				) : null}
				{canScrollTop && !dead ? <ScrollTopButton onPress={scrollToTop} /> : null}
				{interfaceTransitionActive ? (
					<View style={styles.interfaceOverlay}>
						<View style={styles.interfaceCard}>
							{/* The system's own indicator while the switch runs: a turning
							    glyph that never turned read as frozen. */}
							{interfaceRecoveryMessage ? (
								<AppSymbol name="warning" size={iconSize.xl} color={t.red} />
							) : (
								<Spinner size="large" />
							)}
							<Text accessibilityRole="header" maxFontSizeMultiplier={fontScaleCap.title} style={styles.interfaceTitle}>
								{interfaceRecoveryMessage ? "Interface recovery blocked" : "Switching to Chat"}
							</Text>
							<Text maxFontSizeMultiplier={fontScaleCap.body} style={styles.interfaceCopy}>
								{interfaceRecoveryMessage || terminalInterfacePhaseLabel(interfaceSwitch.transition?.phase)}
							</Text>
							{mobileInterfaceTransitionIsCancellable(interfaceSwitch.transition) ? (
								<Button
									variant="secondary"
									title="Cancel switch"
									loading={interfaceSwitch.cancelling}
									onPress={() => void interfaceSwitch.cancel().catch(() => {})}
									style={styles.interfaceAction}
								/>
							) : null}
							{interfaceSwitch.error ? (
								<Text maxFontSizeMultiplier={fontScaleCap.body} style={styles.interfaceError}>{interfaceSwitch.error}</Text>
							) : null}
							{interfaceSwitch.fetchFailed || interfaceRecoveryMessage ? (
								<Button
									variant="secondary"
									title="Retry"
									icon="refresh"
									loading={rechecking}
									onPress={() => void retryInterfaceCheck()}
									style={styles.interfaceAction}
								/>
							) : null}
						</View>
					</View>
				) : null}
				{dead && (
					<View style={styles.deadOverlay}>
						<EmptyState
							icon={TERMINATED_GLYPH}
							title={shellOnly ? "Shell closed" : "Session terminated"}
							message={shellOnly ? "This worktree shell is no longer running." : "This session has no live terminal. Restore it to bring the agent back."}
							action={shellOnly ? undefined : (
								<Button title="Restore session" icon="refresh" loading={restoring} onPress={() => void onRestore()} />
							)}
						/>
					</View>
				)}
			</View>

			{/* The input dock. One container, one bottom inset, fixed slots — every
			    control sits in the same place in every state the screen can reach. */}
			<View style={[styles.dock, { paddingBottom: bottomPad }]} onLayout={(e) => setDockHeight(Math.round(e.nativeEvent.layout.height))}>
				{/* Live dictation readout. Deliberately not inside the field: the
				    partial transcript changes on every syllable, and rewriting the
				    input under the user's caret is hostile. */}
				{(voice.state === "starting" || voice.state === "recording" || voice.state === "transcribing") && (
					<View style={[styles.voiceStrip, voice.state === "starting" && styles.voiceStripWarmup]}>
						<AppSymbol name="mic" size={iconSize.sm} color={voice.state === "starting" ? t.textTertiary : t.accent} />
						<Text style={styles.voiceText} numberOfLines={2}>
							{voice.partial ||
								(voice.state === "starting"
									? // The mic is not capturing yet. Saying "Listening" here would
										// invite speech that gets dropped during warm-up.
										"Keep holding…"
									: voice.state === "transcribing"
										? "Transcribing…"
										: voice.mode === "latched"
											? "Recording hands-free — tap the mic to finish"
											: "Speak now — release to insert")}
						</Text>
					</View>
				)}

				<KeyRow onKey={sendKey} />

				<Composer
					value={msg}
					onChangeText={setMsg}
					onSend={sendPrompt}
					sending={sending || interfaceTransitionActive}
					target={sendTarget}
					onTargetChange={setSendTarget}
					voice={voice}
					keyboardVisible={kbVisible}
					onDismissKeyboard={Keyboard.dismiss}
					targetLocked={shellOnly}
				/>
			</View>
			{/* Absolutely placed, so it comes last and draws over the dock's
			    neighbours; the offset clears the keyboard pad and the dock. */}
			<TerminalNoticeHost notice={notice} onDismiss={clearNotice} bottomOffset={rootPad + dockHeight + space.sm} />
			<InterfaceSwitchConfirm message={switchQuestion} onChoose={answerSwitchQuestion} onCancel={closeSwitchQuestion} />
		</View>
	);
}

const makeStyles = (t: Theme) =>
	StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: {
		flex: 1,
		alignItems: "center",
		justifyContent: "center",
		backgroundColor: t.bgBase,
	},
	statusSlot: { position: "absolute", top: space.sm, left: space.lg, right: space.lg, alignItems: "center" },
	statusPill: {
		flexDirection: "row",
		alignItems: "center",
		gap: space.xs,
		maxWidth: "100%",
		paddingHorizontal: space.md,
		paddingVertical: space.xs,
		borderRadius: radius.pill,
		backgroundColor: t.bgElevated,
		borderWidth: rowDividerWidth,
		borderColor: t.borderDefault,
	},
	statusText: { flexShrink: 1, fontFamily: font.medium, color: t.textSecondary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	transitionNotice: {
		paddingTop: space.sm,
		paddingLeft: space.lg,
		paddingRight: space.sm,
		backgroundColor: t.bgElevated,
		borderBottomWidth: rowDividerWidth,
		borderBottomColor: t.borderSubtle,
	},
	transitionNoticeFailed: { backgroundColor: t.tintRed },
	transitionNoticeRow: { flexDirection: "row", alignItems: "flex-start", gap: space.sm, paddingRight: space.sm },
	transitionNoticeText: { flex: 1, fontFamily: font.regular, color: t.textPrimary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
	transitionNoticeTextFailed: { color: t.red },
	transitionNoticeActions: { flexDirection: "row", alignItems: "center", justifyContent: "flex-end", gap: space.xs },
	termWrap: { flex: 1, backgroundColor: t.bgBase },
	// The page's own surface, as under the chat composer: the composer's
	// elevated fill and the keys' tonal fill both need it to stand apart from.
	dock: {
		borderTopWidth: rowDividerWidth,
		borderTopColor: t.borderSubtle,
		backgroundColor: t.bgBase,
	},
	// The size of the button it stands in for, so the items beside it hold still.
	headerSpinner: { width: touchTarget, height: touchTarget, alignItems: "center", justifyContent: "center" },
	interfaceOverlay: {
		...StyleSheet.absoluteFill,
		alignItems: "center",
		justifyContent: "center",
		paddingHorizontal: space.xxl,
		backgroundColor: t.scrim,
	},
	// A surface on the scrim needs no outline of its own.
	interfaceCard: {
		width: "100%",
		maxWidth: 340,
		alignItems: "center",
		gap: space.sm,
		paddingHorizontal: space.xl,
		paddingVertical: space.xxl,
		borderRadius: radius.lg, borderCurve: "continuous",
		backgroundColor: t.bgSurface,
	},
	interfaceTitle: { ...type.headline, color: t.textPrimary, textAlign: "center" },
	interfaceCopy: { ...type.footnote, color: t.textSecondary, textAlign: "center" },
	interfaceAction: { marginTop: space.xxs, alignSelf: "stretch" },
	interfaceError: { ...type.footnote, color: t.red, textAlign: "center" },
	deadOverlay: { ...StyleSheet.absoluteFill, backgroundColor: t.bgBase },
	// Accent-blue like the rest of the chrome. Red is reserved for the mic button
	// alone: one small saturated element reads as "recording", a whole red panel
	// reads as an error. It sits at the top of the dock, so it divides itself from
	// the keys below rather than redrawing the dock's own top border.
	voiceStrip: {
		flexDirection: "row",
		alignItems: "center",
		gap: space.sm,
		paddingHorizontal: space.md,
		paddingVertical: space.xs,
		backgroundColor: t.accentTint,
		borderBottomWidth: rowDividerWidth,
		borderBottomColor: t.accent,
	},
	// Muted while the mic warms up, so "ready to speak" is a visible state change
	// and not just a wording difference.
	voiceStripWarmup: { backgroundColor: t.bgElevated, borderBottomColor: t.borderDefault },
	voiceText: { flex: 1, fontFamily: font.regular, color: t.textPrimary, fontSize: type.footnote.fontSize, lineHeight: type.footnote.lineHeight },
});
