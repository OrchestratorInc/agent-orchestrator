import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
vi.mock("react-native", () => ({
	Text: "Text",
	View: "View",
	StyleSheet: { create: <T,>(styles: T) => styles, hairlineWidth: 0.5 },
}));
vi.mock("react-native-reanimated", () => ({
	default: { View: "AnimatedView" },
	useAnimatedStyle: (factory: () => object) => factory(),
	useSharedValue: (value: unknown) => ({ value }),
	withTiming: (value: unknown) => value,
}));
vi.mock("../useReducedMotion", () => ({ useReducedMotion: () => true }));
vi.mock("../ThemeProvider", async () => {
	const { lightTheme } = await import("../theme");
	return { useTheme: () => lightTheme, useThemedStyles: (factory: (t: typeof lightTheme) => unknown) => factory(lightTheme) };
});

import { lightTheme } from "../theme";
import { DictationField, WAVEFORM_BARS } from "./DictationField";

let renderer: ReactTestRenderer | undefined;
afterEach(() => { if (renderer) act(() => renderer!.unmount()); renderer = undefined; });

function render(props: Partial<Parameters<typeof DictationField>[0]> = {}) {
	const listeners: ((level: number) => void)[] = [];
	const subscribeLevel = vi.fn((listener: (level: number) => void) => {
		listeners.push(listener);
		return () => { listeners.splice(listeners.indexOf(listener), 1); };
	});
	act(() => { renderer = create(<DictationField state="recording" partial="" subscribeLevel={subscribeLevel} {...props} />); });
	return { listeners, subscribeLevel };
}

const label = () => renderer!.root.findByType("Text" as never);
const bars = () => renderer!.root.findAllByType("AnimatedView" as never);

// One label for the whole recording: the banner it replaced read "Keep
// holding…" for the warm-up and changed a moment later.
it("keeps one label through the warm-up and the recording", () => {
	render({ state: "starting" });
	expect(label().props.children).toBe("Listening…");
	expect(bars()).toHaveLength(WAVEFORM_BARS);
	expect(bars().every((bar) => bar.props.style[1].backgroundColor === lightTheme.textFaint)).toBe(true);
	act(() => renderer!.unmount());
	renderer = undefined;
	render({ state: "recording" });
	expect(label().props.children).toBe("Listening…");
	expect(bars()[0].props.style[1].backgroundColor).toBe(lightTheme.red);
});

it("shows the words heard so far, giving up the start of a long transcript", () => {
	render({ partial: "rename the helper and rerun the suite" });
	expect(label().props).toMatchObject({ children: "rename the helper and rerun the suite", numberOfLines: 1, ellipsizeMode: "head" });
});

it("listens to the level only while the mic captures, and lets go after", () => {
	const warming = render({ state: "starting" });
	expect(warming.subscribeLevel).not.toHaveBeenCalled();
	act(() => renderer!.unmount());
	renderer = undefined;
	const recording = render({ state: "recording" });
	expect(recording.listeners).toHaveLength(1);
	act(() => recording.listeners[0](0.5));
	act(() => renderer!.unmount());
	renderer = undefined;
	expect(recording.listeners).toHaveLength(0);
});
