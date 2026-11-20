import * as Haptics from "expo-haptics";
import { Platform } from "react-native";

// Thin, best-effort wrapper over expo-haptics. Every call is fire-and-forget: a
// device without a haptic engine (or the web target, where expo-haptics is a
// no-op) must never throw into a press handler. The named API mirrors intent —
// callers ask for `tap`/`select`/`success`, not raw enum members — so the choice
// of impact vs. selection vs. notification lives in exactly one place.
//
// Android goes through `performAndroidHapticsAsync`, the view's own haptic
// feedback, rather than expo-haptics' impact/selection/notification calls.
// Those drive the Vibrator with fixed waveforms, which ignores the user's touch
// feedback setting and feels nothing like the system's own clicks. Android has
// fewer constants than iOS has patterns, so a few intents share one.

function run(fire: () => Promise<void>): void {
	if (Platform.OS === "web") return;
	// Swallow rejections — haptics are a nicety, never a failure path.
	fire().catch(() => {});
}

// HapticFeedbackConstants gained CONFIRM and REJECT in API 30 and SEGMENT_TICK
// in API 34. expo-haptics rejects a constant the device lacks, so older devices
// get the closest constant every level has instead of nothing.
const CONFIRM_API = 30;
const SEGMENT_TICK_API = 34;

function since(apiLevel: number, preferred: Haptics.AndroidHaptics, fallback: Haptics.AndroidHaptics): Haptics.AndroidHaptics {
	const version = typeof Platform.Version === "number" ? Platform.Version : 0;
	return version >= apiLevel ? preferred : fallback;
}

function feedback(ios: () => Promise<void>, android: () => Haptics.AndroidHaptics): void {
	if (Platform.OS === "android") run(() => Haptics.performAndroidHapticsAsync(android()));
	else run(ios);
}

export const haptics = {
	// Generic light press — buttons, cards, pull-to-refresh.
	tap() {
		feedback(() => Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light), () => Haptics.AndroidHaptics.Virtual_Key);
	},
	// A selection change — tabs, filter pills, list-item selection.
	select() {
		feedback(() => Haptics.selectionAsync(), () => since(SEGMENT_TICK_API, Haptics.AndroidHaptics.Segment_Tick, Haptics.AndroidHaptics.Clock_Tick));
	},
	// A detent passing under the finger — a slider's knob stepping along its track.
	detent() {
		feedback(() => Haptics.selectionAsync(), () => since(SEGMENT_TICK_API, Haptics.AndroidHaptics.Segment_Tick, Haptics.AndroidHaptics.Clock_Tick));
	},
	// A control reached the point where letting go acts — swipe-to-merge armed.
	// Android has no threshold constant; the heavy click is the firm bump.
	armed() {
		feedback(() => Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Rigid), () => Haptics.AndroidHaptics.Long_Press);
	},
	// A deliberate press lands — a slider released into its action, a long
	// press opening a menu.
	press() {
		feedback(() => Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Medium), () => Haptics.AndroidHaptics.Long_Press);
	},
	// An action succeeded — spawn started, session killed, connection made.
	success() {
		feedback(
			() => Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success),
			() => since(CONFIRM_API, Haptics.AndroidHaptics.Confirm, Haptics.AndroidHaptics.Virtual_Key),
		);
	},
	// A cautionary moment — a destructive confirmation is being raised.
	// Android has no warning constant: a heavy click is firmer than a tap
	// without reading as the double buzz of a rejection.
	warning() {
		feedback(() => Haptics.notificationAsync(Haptics.NotificationFeedbackType.Warning), () => Haptics.AndroidHaptics.Long_Press);
	},
	// An action failed.
	error() {
		feedback(
			() => Haptics.notificationAsync(Haptics.NotificationFeedbackType.Error),
			() => since(CONFIRM_API, Haptics.AndroidHaptics.Reject, Haptics.AndroidHaptics.Long_Press),
		);
	},
};
