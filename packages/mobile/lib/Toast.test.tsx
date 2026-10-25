import { useImperativeHandle, type Ref } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const fixtures = vi.hoisted(() => ({
	announce: vi.fn(),
	reduceMotion: false,
	showSnackbar: vi.fn(),
}));
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		AccessibilityInfo: { announceForAccessibility: fixtures.announce },
		Platform: { OS: "ios" },
		StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 0.33 },
		Text: host("Text"),
		View: host("View"),
	};
});
vi.mock("react-native-reanimated", async () => {
	const React = await import("react");
	const fade = (name: string) => ({ duration: (ms: number) => ({ name, ms }) });
	return {
		default: { View: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("AnimatedView", props, children) },
		FadeIn: fade("FadeIn"),
		FadeOut: fade("FadeOut"),
	};
});
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, bottom: 34, left: 0, right: 0 }) }));
vi.mock("@expo/ui/jetpack-compose", async () => {
	const React = await import("react");
	return {
		Host: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("ComposeHost", props, children),
		SnackbarHost: ({ ref }: { ref?: Ref<unknown> }) => {
			useImperativeHandle(ref, () => ({ showSnackbar: fixtures.showSnackbar }));
			return React.createElement("SnackbarHost");
		},
	};
});
vi.mock("./useReducedMotion", () => ({ useReducedMotion: () => fixtures.reduceMotion }));
vi.mock("./ThemeProvider", async () => {
	const { darkTheme } = await import("./theme");
	return {
		useTheme: () => darkTheme,
		useThemedStyles: (factory: (theme: unknown) => unknown) => factory(darkTheme),
		useThemeState: () => ({ scheme: "dark" }),
	};
});

import { ToastHost } from "./Toast";
import { ToastHost as AndroidToastHost } from "./Toast.android";
import type { ToastController } from "./Toast.types";
import { BANNER_MS } from "./motion";
import { darkTheme } from "./theme";
import { space } from "./tokens";
import { useToast } from "./useToast";

let renderer: ReactTestRenderer | undefined;
let toast!: ToastController;

function Screen({ android = false, bottomOffset }: { android?: boolean; bottomOffset?: number }) {
	toast = useToast();
	return android ? <AndroidToastHost toast={toast} bottomOffset={bottomOffset} /> : <ToastHost toast={toast} bottomOffset={bottomOffset} />;
}

function render(element: React.ReactElement): ReactTestRenderer {
	act(() => {
		renderer = create(element);
	});
	return renderer!;
}

function hud() {
	return renderer!.root.findAllByType("AnimatedView" as never)[0];
}

beforeEach(() => {
	vi.useFakeTimers();
	fixtures.announce.mockClear();
	fixtures.reduceMotion = false;
	fixtures.showSnackbar.mockReset();
	fixtures.showSnackbar.mockResolvedValue("dismissed");
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.useRealTimers();
});

describe("useToast", () => {
	it("hides a message only while it is still the one showing", () => {
		render(<Screen />);
		act(() => toast.show("Path copied."));
		const first = toast.current!;
		act(() => toast.show("Path copied."));
		expect(toast.current!.id).not.toBe(first.id);
		act(() => toast.hide(first.id));
		expect(toast.current?.text).toBe("Path copied.");
		act(() => toast.hide());
		expect(toast.current).toBeUndefined();
	});

	it("defaults to a short, neutral message", () => {
		render(<Screen />);
		act(() => toast.show("Sent."));
		expect(toast.current).toMatchObject({ text: "Sent.", duration: "short", tone: "neutral" });
	});
});

describe("ToastHost on iOS", () => {
	it("fades the HUD in and out, announces it, and takes it down after it has been read", () => {
		render(<Screen />);
		expect(hud()).toBeUndefined();
		act(() => toast.show("Comment added to the review."));
		expect(hud().props.entering).toEqual({ name: "FadeIn", ms: BANNER_MS });
		expect(hud().props.exiting).toEqual({ name: "FadeOut", ms: BANNER_MS });
		expect(fixtures.announce).toHaveBeenCalledExactlyOnceWith("Comment added to the review.");
		act(() => { vi.advanceTimersByTime(2_999); });
		expect(toast.current).toBeDefined();
		act(() => { vi.advanceTimersByTime(1); });
		expect(toast.current).toBeUndefined();
		expect(hud()).toBeUndefined();
	});

	it("keeps a long message up for longer", () => {
		render(<Screen />);
		act(() => toast.show("Studio Mac is offline, so its notifications were not cleared.", { duration: "long" }));
		act(() => { vi.advanceTimersByTime(3_000); });
		expect(toast.current).toBeDefined();
		act(() => { vi.advanceTimersByTime(3_000); });
		expect(toast.current).toBeUndefined();
	});

	it("holds still under Reduce Motion", () => {
		fixtures.reduceMotion = true;
		render(<Screen />);
		act(() => toast.show("Path copied."));
		expect(hud().props.entering).toBeUndefined();
		expect(hud().props.exiting).toBeUndefined();
	});

	it("never takes a touch, and sits above the home indicator unless told otherwise", () => {
		render(<Screen />);
		const anchor = renderer!.root.findAllByType("View" as never)[0];
		expect(anchor.props.pointerEvents).toBe("none");
		expect(anchor.props.style[1]).toEqual({ bottom: 34 + space.xl });
		act(() => renderer!.update(<Screen bottomOffset={96} />));
		expect(renderer!.root.findAllByType("View" as never)[0].props.style[1]).toEqual({ bottom: 96 });
	});

	it("leads a warning with its glyph in the warning colour", () => {
		render(<Screen />);
		act(() => toast.show("Couldn't clear notifications.", { tone: "warning" }));
		const glyph = renderer!.root.findByType("SymbolView" as never);
		expect(glyph.props.name).toEqual({ ios: "exclamationmark.triangle", android: "warning" });
		expect(glyph.props.tintColor).toBe(darkTheme.amber);
	});
});

describe("ToastHost on Android", () => {
	it("shows each message as a Material snackbar and clears it once Compose dismisses it", async () => {
		render(<Screen android />);
		const host = renderer!.root.findByType("ComposeHost" as never);
		expect(host.props).toMatchObject({ matchContents: { vertical: true }, pointerEvents: "none", colorScheme: "dark" });
		expect(host.props).not.toHaveProperty("seedColor");
		await act(async () => toast.show("Path copied."));
		expect(fixtures.showSnackbar).toHaveBeenCalledExactlyOnceWith({ message: "Path copied.", duration: "short" });
		expect(toast.current).toBeUndefined();
		expect(fixtures.announce).not.toHaveBeenCalled();
	});

	it("leaves a newer message alone when an older snackbar finishes", async () => {
		let finishFirst!: (result: string) => void;
		fixtures.showSnackbar.mockReturnValueOnce(new Promise((resolve) => { finishFirst = resolve; })).mockReturnValueOnce(new Promise(() => {}));
		render(<Screen android />);
		act(() => toast.show("First"));
		act(() => toast.show("Second"));
		await act(async () => finishFirst("dismissed"));
		expect(toast.current?.text).toBe("Second");
	});
});
