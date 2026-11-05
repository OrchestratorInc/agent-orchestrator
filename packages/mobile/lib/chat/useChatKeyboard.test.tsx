import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

type Listener = (event?: { height?: number; endCoordinates?: { height: number } }) => void;
const fixture = vi.hoisted(() => ({
	controller: new Map<string, Listener>(),
	platform: new Map<string, Listener>(),
	visible: false,
}));
vi.mock("react-native", () => ({
	Platform: { OS: "android" },
	Keyboard: {
		isVisible: () => fixture.visible,
		metrics: () => undefined,
		addListener: (name: string, listener: Listener) => { fixture.platform.set(name, listener); return { remove: () => fixture.platform.delete(name) }; },
	},
}));
vi.mock("react-native-keyboard-controller", () => ({
	KeyboardEvents: { addListener: (name: string, listener: Listener) => { fixture.controller.set(name, listener); return { remove: () => fixture.controller.delete(name) }; } },
}));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ bottom: 24 }) }));
vi.mock("expo-router/build/react-navigation/elements", () => ({ useHeaderHeight: () => 64 }));
import { PLATFORM_KEYBOARD_SETTLE_MS } from "../session/keyboardInset";
import { useChatKeyboard } from "./useChatKeyboard";

let renderer: ReactTestRenderer | undefined;
let latest: ReturnType<typeof useChatKeyboard> | undefined;
function Harness() { latest = useChatKeyboard(); return null; }
async function mount() { await act(async () => { renderer = create(<Harness />); }); }
async function fire(source: "controller" | "platform", name: string, event?: Parameters<Listener>[0]) {
	await act(async () => { (source === "controller" ? fixture.controller : fixture.platform).get(name)?.(event); });
}
async function settle() { await act(async () => { await vi.advanceTimersByTimeAsync(PLATFORM_KEYBOARD_SETTLE_MS); }); }

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.useFakeTimers();
	fixture.controller.clear(); fixture.platform.clear(); fixture.visible = false;
});
afterEach(async () => { await act(async () => renderer?.unmount()); vi.useRealTimers(); vi.unstubAllGlobals(); });

// The composer rides the keyboard down frame by frame; a conversation still
// padded for the open keyboard left an empty band above it until the close ended.
it("releases the conversation's pad as the keyboard starts to close, not once it has gone", async () => {
	await mount();
	await fire("controller", "keyboardWillShow", { height: 336 });
	expect(latest?.rootStyle).toEqual({ paddingBottom: 336 });
	await fire("controller", "keyboardWillHide");
	expect(latest?.rootStyle).toEqual({ paddingBottom: 0 });
	// The composer's own handling still waits for the close.
	expect(latest?.keyboardVisible).toBe(true);
	expect(latest?.composerLift).toBeUndefined();
	// Opened again mid-close: padded again.
	await fire("controller", "keyboardWillShow", { height: 336 });
	expect(latest?.rootStyle).toEqual({ paddingBottom: 336 });
	await fire("controller", "keyboardWillHide");
	await fire("controller", "keyboardDidHide");
	expect(latest?.rootStyle).toEqual({ paddingBottom: 0 });
	expect(latest?.composerLift).toBe(0);
});

it("keeps the keyboard pad through a close React Native reports before keyboard-controller", async () => {
	await mount();
	await fire("controller", "keyboardWillShow", { height: 336 });
	expect(latest?.rootStyle).toEqual({ paddingBottom: 336 });
	// Android: the root insets hold the end state as the close starts.
	fixture.visible = false;
	await fire("platform", "keyboardDidHide");
	expect(latest?.rootStyle).toEqual({ paddingBottom: 336 });
	expect(latest?.dockRiseOverride).toBeUndefined();
	await fire("controller", "keyboardDidHide");
	await settle();
	expect(latest?.rootStyle).toEqual({ paddingBottom: 0 });
	expect(latest?.dockRiseOverride).toBeUndefined();
});

it("drops the pad once the keyboard has settled when keyboard-controller missed the close", async () => {
	await mount();
	await fire("controller", "keyboardWillShow", { height: 336 });
	fixture.visible = false;
	await fire("platform", "keyboardDidHide");
	await settle();
	expect(latest?.rootStyle).toEqual({ paddingBottom: 0 });
	expect(latest?.dockRiseOverride).toBe(0);
});

it("ignores a report React Native has already contradicted", async () => {
	await mount();
	fixture.visible = true;
	await fire("platform", "keyboardDidShow", { endCoordinates: { height: 312 } });
	fixture.visible = false;
	await settle();
	expect(latest?.keyboardVisible).toBe(false);
	expect(latest?.rootStyle).toEqual({ paddingBottom: 0 });
});

// The composer rides keyboard-controller's own progress on the UI thread, and
// stands at the reconciled height only when that progress missed the open.
// Backgrounding the app with the keyboard up hides it without running the
// animation the composer rides, which then held the composer at the open height.
it("rests the composer at the bottom whenever the keyboard is closed", async () => {
	await mount();
	expect(latest?.composerLift).toBe(0);
	await fire("controller", "keyboardWillShow", { height: 336 });
	expect(latest?.composerLift).toBeUndefined();
	await fire("controller", "keyboardDidHide");
	expect(latest?.composerLift).toBe(0);
});

it("lets the composer ride the keyboard, and lifts it by hand only for an open keyboard-controller missed", async () => {
	await mount();
	await fire("controller", "keyboardWillShow", { height: 336 });
	expect(latest?.composerLift).toBeUndefined();
	await fire("controller", "keyboardDidHide");
	fixture.visible = true;
	await fire("platform", "keyboardDidShow", { endCoordinates: { height: 312 } });
	await settle();
	expect(latest?.composerLift).toBe(latest?.rootStyle?.paddingBottom);
	expect(latest?.composerLift).toBeGreaterThan(0);
});
