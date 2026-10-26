import { describe, expect, it } from "vitest";
import { CHAT_KEYBOARD_HIDDEN, chatDockRiseOverride, dockInset, dockRestingInset, KEYBOARD_DOCK_GAP, keyboardVerticalOffset, MIN_DOCK_INSET, reduceChatKeyboard, rootKeyboardPad, type ChatKeyboardSignal } from "./keyboardInset";
import { CONTROL_KEYS } from "./keys";

describe("dockInset", () => {
	// The regression this exists for: the dock used to keep its own padding while
	// the root view was already padded by the keyboard height, so opening the
	// keyboard moved the bar twice in opposite directions.
	it("does not add a second dock gap while the root already clears the keyboard", () => {
		// The gap is a constant, not the inset: the root already clears the whole
		// keyboard, and adding the home-indicator inset again is what made the bar
		// kick twice.
		expect(dockInset(336, 34, true)).toBe(KEYBOARD_DOCK_GAP);
		expect(dockInset(1, 34, true)).toBe(KEYBOARD_DOCK_GAP);
		expect(dockInset(0, 34, true)).toBe(KEYBOARD_DOCK_GAP);
	});

	it("carries the home-indicator inset while the keyboard is down", () => {
		expect(dockInset(0, 34, false)).toBe(34);
	});

	it("falls back to a minimum on a device with no home indicator", () => {
		expect(dockInset(0, 0, false)).toBe(MIN_DOCK_INSET);
	});
});

// The chat dock holds this value at all times and rides the keyboard's progress
// to close the difference. Switching it on a visibility flag is what made the
// composer overshoot: the flag turns over when the keyboard has *finished*
// hiding, so the dock spent the whole close one inset too low and then jumped.
describe("dockRestingInset", () => {
	it("is the home-indicator inset with the keyboard down", () => {
		expect(dockRestingInset(34)).toBe(34);
	});

	it("falls back to the minimum without a home indicator", () => {
		expect(dockRestingInset(0)).toBe(MIN_DOCK_INSET);
	});

	it("never returns the keyboard gap, which the dock owes only mid-animation", () => {
		expect(dockRestingInset(34)).not.toBe(KEYBOARD_DOCK_GAP);
	});
});

describe("rootKeyboardPad", () => {
	// The regression this exists for: React Native's Android keyboard event
	// reports `imeInsets.bottom - systemBars.bottom` (ReactRootView.java), i.e.
	// the keyboard height with the navigation bar already subtracted. Our root
	// view runs edge-to-edge underneath that nav bar, so padding by the reported
	// height alone left the dock short by exactly the nav-bar inset - half an
	// input row under gesture nav, a whole one under 3-button nav.
	it("adds the navigation-bar inset back on Android", () => {
		expect(rootKeyboardPad("android", 336, 24)).toBe(360);
		expect(rootKeyboardPad("android", 336, 48)).toBe(384);
	});

	it("reserves only the reported height on iOS, which already spans the home indicator", () => {
		expect(rootKeyboardPad("ios", 336, 34)).toBe(336);
	});

	it("reserves nothing on either platform while the keyboard is down", () => {
		expect(rootKeyboardPad("android", 0, 48)).toBe(0);
		expect(rootKeyboardPad("ios", 0, 34)).toBe(0);
	});

	it("handles a device with the navigation bar hidden", () => {
		expect(rootKeyboardPad("android", 336, 0)).toBe(336);
	});
});

describe("keyboardVerticalOffset", () => {
	it("uses the measured native stack header instead of a device-specific constant", () => {
		expect(keyboardVerticalOffset(101)).toBe(101);
		expect(keyboardVerticalOffset(-1)).toBe(0);
	});
});

// The dock sits inside the root view, so the root's padding is what clears the
// keyboard. The dock only adds its own gap on top of that.
describe("root padding and dock inset together", () => {
	it("clear the full Android keyboard occupancy without double-counting", () => {
		const kb = 336; // as reported by RN: ime inset minus nav bar
		const nav = 48;
		expect(rootKeyboardPad("android", kb, nav) + dockInset(kb, nav)).toBe(kb + nav + KEYBOARD_DOCK_GAP);
	});

	it("fall back to the safe-area inset with the keyboard down", () => {
		expect(rootKeyboardPad("android", 0, 48) + dockInset(0, 48)).toBe(48);
	});
});

// Measured on a Pixel emulator under gesture navigation: keyboard-controller
// reported 336 while React Native's keyboardDidShow reported 312 for the same
// keyboard, the difference being the 24dp navigation bar.
describe("reduceChatKeyboard", () => {
	const run = (...signals: ChatKeyboardSignal[]) => signals.reduce((state, signal) => reduceChatKeyboard("android", state, signal), CHAT_KEYBOARD_HIDDEN);

	it("clears keyboard-controller's height as-is, since it already spans the nav bar", () => {
		// Adding the inset again is what held the composer 24dp above the keyboard.
		expect(run({ kind: "controller-show", height: 336 })).toEqual({ visible: true, height: 336, progressTrusted: true });
	});

	it("leaves a normal opening to the keyboard's progress when React Native agrees", () => {
		const state = run({ kind: "controller-show", height: 336 }, { kind: "platform-shown", height: 312, insetsBottom: 24 });
		expect(state).toEqual({ visible: true, height: 336, progressTrusted: true });
		expect(chatDockRiseOverride(state)).toBeUndefined();
	});

	// The settle window means React Native's early keyboardDidHide reaches the
	// reducer after keyboard-controller's own close, as a no-op.
	it("ends a normal close hidden, with React Native's report changing nothing", () => {
		expect(run({ kind: "controller-show", height: 336 }, { kind: "controller-hidden" }, { kind: "platform-hidden" })).toEqual(CHAT_KEYBOARD_HIDDEN);
	});

	it("treats a sub-dp height difference as the same keyboard", () => {
		const state = run({ kind: "controller-show", height: 336.38 }, { kind: "platform-shown", height: 312, insetsBottom: 24 });
		expect(chatDockRiseOverride(state)).toBeUndefined();
	});

	// The empty-space bug: keyboard-controller never heard the keyboard close, so
	// the root kept a keyboard-sized pad with nothing under it.
	it("drops the pad when React Native sees a close keyboard-controller missed", () => {
		const state = run({ kind: "controller-show", height: 336 }, { kind: "platform-hidden" });
		expect(state.visible).toBe(false);
		expect(state.height).toBe(0);
		expect(chatDockRiseOverride(state)).toBe(0);
	});

	// The reverse: keyboard-controller reported nothing for an opening, and the
	// composer stayed under the keyboard.
	it("clears a keyboard only React Native reported, adding the nav bar back", () => {
		const state = run({ kind: "platform-shown", height: 312, insetsBottom: 24 });
		expect(state).toEqual({ visible: true, height: 336, progressTrusted: false });
		expect(chatDockRiseOverride(state)).toBe(1);
	});

	// Its progress never left 0 for an open it missed, so riding it during the
	// close would drop the composer by a resting inset.
	it("keeps a pinned dock pinned until keyboard-controller reports the close", () => {
		const pinned = run({ kind: "platform-shown", height: 312, insetsBottom: 24 });
		expect(chatDockRiseOverride(pinned)).toBe(1);
		expect(chatDockRiseOverride(reduceChatKeyboard("android", pinned, { kind: "controller-hidden" }))).toBeUndefined();
	});

	it("treats iOS heights from either source as the full occupancy", () => {
		expect(reduceChatKeyboard("ios", CHAT_KEYBOARD_HIDDEN, { kind: "platform-shown", height: 336, insetsBottom: 34 }).height).toBe(336);
	});
});

describe("CONTROL_KEYS", () => {
	// The row divides its width between exactly these, with no wrapping — a ninth
	// key would silently make them too narrow to hit.
	it("is exactly eight keys", () => {
		expect(CONTROL_KEYS).toHaveLength(8);
	});

	it("sends the bytes a PTY expects", () => {
		const seq = Object.fromEntries(CONTROL_KEYS.map((k) => [k.label, k.seq]));
		expect(seq.esc).toBe("\x1b");
		expect(seq.tab).toBe("\t");
		expect(seq["^C"]).toBe("\x03");
		expect(seq["↵"]).toBe("\r");
		// CSI cursor keys: ESC [ A/B/C/D
		expect(seq["↑"]).toBe("\x1b[A");
		expect(seq["↓"]).toBe("\x1b[B");
		expect(seq["→"]).toBe("\x1b[C");
		expect(seq["←"]).toBe("\x1b[D");
	});

	// Every one of these would be stripped by SanitizeControlChars on the REST
	// route, which is why the key row writes to the mux instead.
	it("carries only control bytes, never plain text", () => {
		// Every sequence begins with a C0 control byte (ESC, TAB, ETX, CR).
		for (const k of CONTROL_KEYS) {
			expect(k.seq.charCodeAt(0)).toBeLessThan(0x20);
		}
	});

	it("labels every key for accessibility", () => {
		for (const k of CONTROL_KEYS) expect(k.hint.length).toBeGreaterThan(0);
	});
});
