import { Platform } from "react-native";

/**
 * Lets an artifact page's video play in place, as it does on the desktop.
 * react-native-webview's iOS defaults require a tap before any
 * media plays, muted or not, and send video to the fullscreen player: a page's
 * `<video autoplay muted playsinline>` then never starts, and since iOS loads
 * no frame of a video it has not played, it draws as an empty box.
 *
 * It also lets a page play sound unprompted, so it is only for a page the
 * reader is looking at: a single page on screen (a version, a shared
 * artifact, the fullscreen render viewer), or the viewer's pager with
 * artifactMediaGateScript holding its off-screen neighbours. Inline chat
 * frames (live off screen, and a tap opens the viewer) and gallery cards (no
 * touches) keep the defaults.
 *
 * iOS only: Android's defaults already autoplay muted video and keep sound
 * behind a tap, and Android injects the gate after the page has started, so
 * it could not be relied on to hold sound there.
 */
export const ARTIFACT_MEDIA_PLAYBACK: { allowsInlineMediaPlayback?: boolean; mediaPlaybackRequiresUserAction?: boolean } = Platform.OS === "ios"
	? { allowsInlineMediaPlayback: true, mediaPlaybackRequiresUserAction: false }
	: {};

/** How many held elements a page going back on screen starts again, newest kept. */
const MAX_HELD = 16;

/**
 * Holds a page's media while it is off screen, without reloading it (its
 * scroll and form state stay). Run before the page's own scripts, in every
 * frame, it watches every media element (in the document, or detached:
 * `new Audio()`, or any element `play()` is called on) and every Web Audio
 * context. Off screen, `play()` and a context's `resume()` are held, autoplay
 * is paused as it starts, and running contexts are suspended; back on screen,
 * what was held starts again (the newest few, so a burst of effects isn't
 * replayed). `window.__aoMediaActive(on)` switches it, and passes the switch
 * on to the page's frames. Only what is playing, held, or an open context is
 * referenced, so a page that makes many sounds doesn't grow it.
 */
export function artifactMediaGateScript(active: boolean): string {
	return `(function(active){
if (window.__aoMediaActive) return;
var watched = new WeakSet(), playing = new Set(), held = [], contexts = [];
var proto = window.HTMLMediaElement && window.HTMLMediaElement.prototype;
var nativePlay = proto && proto.play;
function hold(el) { var at = held.indexOf(el); if (at >= 0) held.splice(at, 1); held.push(el); if (held.length > ${MAX_HELD}) held.shift(); }
function onPlay(event) { var el = event.target; watch(el); if (!active && el && el.pause) { el.pause(); hold(el); } else playing.add(el); }
function onStop(event) { playing.delete(event.target); }
function watch(el) {
	if (!el || !el.addEventListener || watched.has(el)) return;
	watched.add(el);
	el.addEventListener("play", onPlay, true);
	el.addEventListener("pause", onStop, true);
	el.addEventListener("ended", onStop, true);
}
document.addEventListener("play", onPlay, true);
document.addEventListener("pause", onStop, true);
document.addEventListener("ended", onStop, true);
if (proto && nativePlay) {
	proto.play = function () { watch(this); if (!active) { hold(this); return Promise.resolve(); } return nativePlay.apply(this, arguments); };
}
// Wrappers construct the native object with the caller's new.target, so a subclass keeps its own methods.
var NativeAudio = window.Audio;
if (NativeAudio) {
	var Audio = function Audio() { var el = Reflect.construct(NativeAudio, arguments, new.target || Audio); watch(el); return el; };
	Audio.prototype = NativeAudio.prototype;
	Object.setPrototypeOf(Audio, NativeAudio);
	window.Audio = Audio;
}
["AudioContext", "webkitAudioContext"].forEach(function (name) {
	var Native = window[name];
	if (!Native) return;
	var nativeResume = Native.prototype.resume;
	Native.prototype.resume = function () { if (!active) { this.__aoHeld = true; return Promise.resolve(); } return nativeResume.apply(this, arguments); };
	var Context = function () {
		var context = Reflect.construct(Native, arguments, new.target || Context);
		var entry = { context: context, resume: nativeResume };
		contexts.push(entry);
		if (context.addEventListener) context.addEventListener("statechange", function () {
			if (context.state === "closed") { var at = contexts.indexOf(entry); if (at >= 0) contexts.splice(at, 1); }
		});
		if (!active && context.suspend) { context.__aoHeld = true; context.suspend(); }
		return context;
	};
	Context.prototype = Native.prototype;
	Object.setPrototypeOf(Context, Native);
	window[name] = Context;
});
function setActive(on) {
	active = Boolean(on);
	var frames = window.frames || [];
	for (var f = 0; f < frames.length; f++) { try { frames[f].postMessage({ __aoMediaActive: active }, "*"); } catch (error) {} }
	if (!active) {
		var all = [];
		playing.forEach(function (el) { all.push(el); });
		var found = document.querySelectorAll ? document.querySelectorAll("video,audio") : [];
		for (var i = 0; i < found.length; i++) if (all.indexOf(found[i]) < 0) all.push(found[i]);
		all.forEach(function (el) { if (!el.paused) { el.pause(); hold(el); } });
		playing.clear();
		contexts.forEach(function (entry) { if (entry.context.state === "running") { entry.context.__aoHeld = true; entry.context.suspend(); } });
		return;
	}
	var resume = held; held = [];
	resume.forEach(function (el) { try { var started = nativePlay.call(el); if (started && started.catch) started.catch(function () {}); } catch (error) {} });
	contexts.forEach(function (entry) { if (entry.context.__aoHeld) { entry.context.__aoHeld = false; entry.resume.call(entry.context); } });
}
// A frame takes the switch from the page that holds it.
if (window.addEventListener) window.addEventListener("message", function (event) {
	if (event.source && event.source === window.parent && event.source !== window && event.data && typeof event.data.__aoMediaActive === "boolean") setActive(event.data.__aoMediaActive);
});
window.__aoMediaActive = setActive;
})(${active ? "true" : "false"});
true;`;
}

/** Tells a page whose gate is installed whether it is the page on screen. */
export function artifactMediaActiveScript(active: boolean): string {
	return `window.__aoMediaActive && window.__aoMediaActive(${active ? "true" : "false"}); true;`;
}
