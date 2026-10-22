// Bottom-inset arithmetic for the session screen's input dock. Pure so the rule
// is testable — getting it wrong is what made the dock jump twice whenever the
// keyboard appeared. The root reserves the keyboard (see `rootKeyboardPad`),
// so the dock contributes nothing while it is visible.
//
// With the keyboard down there is no root padding, so the dock owes the
// home-indicator inset itself.

/** Minimum breathing room under the dock on a device with no home indicator. */
export const MIN_DOCK_INSET = 8;

/**
 * Breathing room between the dock and the top edge of the keyboard.
 *
 * Not zero: the root view clears exactly the keyboard's height, so without this
 * the pill's bottom edge *is* the keyboard's top edge. iOS' own sheets leave a
 * gap there, and the flush version read as the bar being cut off by the
 * keyboard rather than sitting above it.
 */
export const KEYBOARD_DOCK_GAP = 8;

// KeyboardAvoidingView measures against the window while this screen begins
// below a native-stack header. Supplying that stack's measured header height
// reconciles those coordinate spaces without assuming a particular iPhone,
// orientation, status bar, or Dynamic Type size.
export function keyboardVerticalOffset(headerHeight: number): number {
	return Math.max(0, headerHeight);
}

/**
 * Bottom padding the root view owes to clear the keyboard.
 *
 * `height` is whatever the platform's keyboard event reported, and the two
 * platforms report different things:
 *
 * - iOS measures from the bottom of the screen, so the value already spans the
 *   home indicator. Reserve it as-is.
 * - Android reports `imeInsets.bottom - systemBars.bottom` (see
 *   `checkForKeyboardEvents` in ReactRootView.java) — the keyboard height with
 *   the navigation bar already subtracted. But our root view runs edge-to-edge
 *   underneath that nav bar, so the keyboard actually covers the full
 *   `imeInsets.bottom`. Padding by the reported height alone left the dock
 *   short by exactly the nav-bar inset, which is why the input row was cut in
 *   half under gesture nav (~24dp) and hidden outright under 3-button nav
 *   (48dp). Add the inset back.
 */
export function rootKeyboardPad(
	platform: "android" | "ios",
	height: number,
	insetsBottom: number,
): number {
	if (height <= 0) return 0;
	return platform === "android" ? height + insetsBottom : height;
}

export function dockInset(kbHeight: number, insetsBottom: number, keyboardVisible = kbHeight > 0): number {
	// `keyboardVisible` covers Android's adjustResize path, where the window may
	// already have shifted before a non-zero height is reported.
	if (keyboardVisible) return KEYBOARD_DOCK_GAP;
	return insetsBottom > 0 ? insetsBottom : MIN_DOCK_INSET;
}

/**
 * What the dock owes with the keyboard down: the home-indicator inset, or a
 * minimum on a device without one.
 *
 * The chat dock holds this value *always* and rides the keyboard's own progress
 * to close the difference (see `ChatComposer`). Switching between this and
 * `KEYBOARD_DOCK_GAP` on a visibility flag is what made the composer overshoot:
 * the flag turns over when the keyboard has finished hiding, so the dock spent
 * the whole closing animation one resting-inset too low and then jumped up into
 * place at the end — and the model label above it with it.
 */
export function dockRestingInset(insetsBottom: number): number {
	return insetsBottom > 0 ? insetsBottom : MIN_DOCK_INSET;
}

/**
 * What the chat screens believe about the keyboard, reconciled from two sources.
 *
 * keyboard-controller reports when the keyboard *starts* moving, which is what
 * lets the composer travel with it. But it only learns anything from its own
 * window-insets animation callback, and some transitions never reach that
 * callback: a system dialog (the microphone permission prompt) taking focus
 * hides the keyboard without telling it, and after other sheets it has been
 * seen to report nothing at all for the next opening. Its height then stays
 * where it was, and the screen holds a keyboard-sized hole with no keyboard in
 * it, or leaves the composer under a keyboard it never saw.
 *
 * React Native's own `Keyboard` events come from the root view's layout pass,
 * which compares the real window insets every time, so they fire for those
 * transitions too. They are not used to drive the animation, only to correct
 * whatever is left over when the two disagree. On Android they can arrive as a
 * transition *starts*: the root insets already hold the end state, so
 * `keyboardDidHide` fires before keyboard-controller's `keyboardWillHide`.
 * Applying that at once dropped the pad mid-close and the composer snapped
 * instead of following the keyboard down, so the screen holds each platform
 * report for `PLATFORM_KEYBOARD_SETTLE_MS` and applies it only if
 * keyboard-controller still disagrees once the keyboard has stopped moving.
 *
 * `height` is the whole occupancy from the bottom of the screen, which is
 * what the root has to clear. The two sources measure that differently on
 * edge-to-edge Android: keyboard-controller's height already spans the
 * navigation bar, while React Native's subtracts it (see `rootKeyboardPad`).
 */
export type ChatKeyboardState = {
	visible: boolean;
	height: number;
	/** keyboard-controller's own progress matches this state, so the dock may ride it. */
	progressTrusted: boolean;
};

export type ChatKeyboardSignal =
	| { kind: "controller-show"; height: number }
	| { kind: "controller-hidden" }
	| { kind: "platform-shown"; height: number; insetsBottom: number }
	| { kind: "platform-hidden" };

/**
 * How long a React Native keyboard report waits before it may correct
 * keyboard-controller. Longer than Android's IME animation (about 250-300ms), so
 * a transition keyboard-controller does see has finished first.
 */
export const PLATFORM_KEYBOARD_SETTLE_MS = 500;

/** Heights are fractional dp; a sub-dp difference is the same keyboard. */
const HEIGHT_TOLERANCE = 1;

export const CHAT_KEYBOARD_HIDDEN: ChatKeyboardState = { visible: false, height: 0, progressTrusted: true };

export function reduceChatKeyboard(platform: "android" | "ios", state: ChatKeyboardState, signal: ChatKeyboardSignal): ChatKeyboardState {
	switch (signal.kind) {
		case "controller-show":
			return { visible: true, height: Math.max(0, signal.height), progressTrusted: true };
		// No case for keyboard-controller's `keyboardWillHide`: the occupancy holds
		// until the keyboard has gone, and a pinned dock stays pinned. When the open
		// was one keyboard-controller missed, its progress is still 0, and handing
		// the dock back to it mid-close dropped the composer by a resting inset.
		case "controller-hidden":
			return CHAT_KEYBOARD_HIDDEN;
		case "platform-shown": {
			const height = rootKeyboardPad(platform, signal.height, signal.insetsBottom);
			if (state.visible && Math.abs(state.height - height) <= HEIGHT_TOLERANCE) return state;
			return { visible: height > 0, height, progressTrusted: false };
		}
		case "platform-hidden":
			return state.visible ? { visible: false, height: 0, progressTrusted: false } : state;
	}
}

/**
 * Where the composer dock should sit in its keyboard gap, 0 (resting) to 1
 * (against the keyboard), when the keyboard's own progress cannot be trusted.
 * `undefined` means ride the progress: that is the normal case, and the only
 * one that animates with the keyboard.
 */
export function chatDockRiseOverride(state: ChatKeyboardState): number | undefined {
	if (state.progressTrusted) return undefined;
	return state.visible ? 1 : 0;
}
