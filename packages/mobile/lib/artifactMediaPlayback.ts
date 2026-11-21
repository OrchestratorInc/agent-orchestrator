import { Platform } from "react-native";

/**
 * Lets an artifact page's video play in place, as it does on the desktop.
 * react-native-webview's iOS defaults require a tap before any
 * media plays, muted or not, and send video to the fullscreen player: a page's
 * `<video autoplay muted playsinline>` then never starts, and since iOS loads
 * no frame of a video it has not played, it draws as an empty box.
 *
 * It also lets a page play sound unprompted, so it goes with
 * artifactMediaGateScript wherever a page can be mounted off screen (the
 * viewer's neighbouring pages). Gallery cards take no touches and keep the
 * defaults.
 *
 * iOS only: Android's defaults already autoplay muted video and keep sound
 * behind a tap, and Android injects the gate after the page has started, so
 * it could not be relied on to hold sound there.
 */
export const ARTIFACT_MEDIA_PLAYBACK: { allowsInlineMediaPlayback?: boolean; mediaPlaybackRequiresUserAction?: boolean } = Platform.OS === "ios"
	? { allowsInlineMediaPlayback: true, mediaPlaybackRequiresUserAction: false }
	: {};

/**
 * Holds a page's media while it is off screen, without reloading it (its
 * scroll and form state stay). Run before the page's own scripts, it tracks
 * every media element (in the document, or detached: `new Audio()`, or any
 * element `play()` is called on) and every Web Audio context. Off screen,
 * `play()` and a context's `resume()` are held, autoplay is paused as it
 * starts, and running contexts are suspended; back on screen, exactly what was
 * held starts again. `window.__aoMediaActive(on)` switches it.
 */
export function artifactMediaGateScript(active: boolean): string {
	return `(function(active){
if (window.__aoMediaActive) return;
var media = [], held = [], contexts = [];
var proto = window.HTMLMediaElement && window.HTMLMediaElement.prototype;
var nativePlay = proto && proto.play;
function hold(el) { if (held.indexOf(el) < 0) held.push(el); }
function onPlay(event) { var el = event.target; track(el); if (!active && el && el.pause) { el.pause(); hold(el); } }
function track(el) { if (el && el.addEventListener && media.indexOf(el) < 0) { media.push(el); el.addEventListener("play", onPlay, true); } }
document.addEventListener("play", onPlay, true);
if (proto && nativePlay) {
	proto.play = function () { track(this); if (!active) { hold(this); return Promise.resolve(); } return nativePlay.apply(this, arguments); };
}
var NativeAudio = window.Audio;
if (NativeAudio) {
	var Audio = function (src) { var el = arguments.length ? new NativeAudio(src) : new NativeAudio(); track(el); return el; };
	Audio.prototype = NativeAudio.prototype;
	window.Audio = Audio;
}
["AudioContext", "webkitAudioContext"].forEach(function (name) {
	var Native = window[name];
	if (!Native) return;
	var nativeResume = Native.prototype.resume;
	Native.prototype.resume = function () { if (!active) { this.__aoHeld = true; return Promise.resolve(); } return nativeResume.apply(this, arguments); };
	var Context = function (options) {
		var context = arguments.length ? new Native(options) : new Native();
		contexts.push({ context: context, resume: nativeResume });
		if (!active && context.suspend) { context.__aoHeld = true; context.suspend(); }
		return context;
	};
	Context.prototype = Native.prototype;
	window[name] = Context;
});
window.__aoMediaActive = function (on) {
	active = Boolean(on);
	if (!active) {
		var all = media.slice();
		var found = document.querySelectorAll ? document.querySelectorAll("video,audio") : [];
		for (var i = 0; i < found.length; i++) if (all.indexOf(found[i]) < 0) all.push(found[i]);
		all.forEach(function (el) { if (!el.paused) { el.pause(); hold(el); } });
		contexts.forEach(function (entry) { if (entry.context.state === "running") { entry.context.__aoHeld = true; entry.context.suspend(); } });
		return;
	}
	var resume = held; held = [];
	resume.forEach(function (el) { try { var started = nativePlay.call(el); if (started && started.catch) started.catch(function () {}); } catch (error) {} });
	contexts.forEach(function (entry) { if (entry.context.__aoHeld) { entry.context.__aoHeld = false; entry.resume.call(entry.context); } });
};
})(${active ? "true" : "false"});
true;`;
}

/** Tells a page whose gate is installed whether it is the page on screen. */
export function artifactMediaActiveScript(active: boolean): string {
	return `window.__aoMediaActive && window.__aoMediaActive(${active ? "true" : "false"}); true;`;
}
