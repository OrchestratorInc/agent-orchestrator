import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	platform: { OS: "ios", Version: "26.0" },
	pan: {} as Record<string, (...args: unknown[]) => void>,
	armed: vi.fn(),
}));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Platform: fixtures.platform,
		StyleSheet: { create: (styles: object) => styles },
		View: host("View"),
		Text: host("Text"),
		Pressable: host("Pressable"),
		FlatList: ({ data, renderItem }: { data: readonly unknown[]; renderItem: (info: { item: unknown; index: number }) => React.ReactNode }) =>
			React.createElement("FlatList", null, data.map((item, index) => React.createElement(React.Fragment, { key: index }, renderItem({ item, index })))),
	};
});
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, right: 0, bottom: 34, left: 0 }) }));
vi.mock("react-native-gesture-handler", async () => {
	const React = await import("react");
	const chain: Record<string, unknown> = new Proxy({}, {
		get: (_target, key: string) => (callback: unknown) => {
			if (typeof callback === "function") fixtures.pan[key] = callback as (...args: unknown[]) => void;
			return chain;
		},
	});
	return { Gesture: { Pan: () => chain }, GestureDetector: ({ children }: { children?: React.ReactNode }) => children };
});
vi.mock("react-native-reanimated", () => ({
	default: { View: "AnimatedView" },
	Easing: { out: () => undefined, cubic: undefined },
	useSharedValue: (value: unknown) => ({ value }),
	useAnimatedStyle: (style: () => object) => style(),
	withSpring: (value: number) => value,
	withTiming: (value: number, _config: object, done?: (finished: boolean) => void) => { done?.(true); return value; },
}));
vi.mock("react-native-worklets", () => ({ scheduleOnRN: (fn: () => void) => fn() }));
vi.mock("@expo/ui", async () => {
	const React = await import("react");
	return { Host: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("SwiftHost", props, children) };
});
vi.mock("@expo/ui/swift-ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { Button: host("SwiftButton"), HStack: host("HStack"), VStack: host("VStack"), Image: host("SwiftImage"), Text: host("SwiftText"), List: Object.assign(host("List"), { ForEach: host("ListForEach") }) };
});
vi.mock("@expo/ui/swift-ui/modifiers", () => {
	const modifier = (name: string) => (value?: unknown) => ({ [name]: value });
	return Object.fromEntries([
		"accessibilityElement", "accessibilityHint", "accessibilityLabel", "buttonBorderShape", "buttonStyle", "controlSize", "deleteDisabled", "font",
		"foregroundStyle", "lineLimit", "listRowBackground", "listStyle", "scrollContentBackground", "tint", "truncationMode",
	].map((name) => [name, modifier(name)]));
});
vi.mock("@expo/ui/jetpack-compose", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Host: host("ComposeHost"),
		ExtendedFloatingActionButton: Object.assign(host("ExtendedFloatingActionButton"), { Icon: host("FabIcon"), Text: host("FabText") }),
		Icon: host("Icon"),
		Text: host("ComposeText"),
	};
});
vi.mock("@expo/ui/jetpack-compose/modifiers", () => ({ paddingAll: (all: number) => ({ paddingAll: all }) }));
vi.mock("../ThemeProvider", () => ({
	useTheme: () => ({ accent: "#accent", onAccent: "#onAccent", red: "#red", tintRed: "#tintRed", bgSurface: "#bgSurface", textPrimary: "#textPrimary", textTertiary: "#textTertiary", borderSubtle: "#borderSubtle", fontMono: "mono" }),
	useThemeState: () => ({ scheme: "dark" }),
	useThemedStyles: () => ({}),
}));
vi.mock("../pressFeedback", () => ({ usePressFeedback: () => ({ android_ripple: { color: "#ripple" }, pressed: undefined }) }));
vi.mock("../haptics", () => ({ haptics: { armed: fixtures.armed } }));
vi.mock("../useReducedMotion", () => ({ useReducedMotion: () => false }));
vi.mock("./fileIcons.android", () => ({ fileDrawable: (name: string) => `drawable:${name}` }));

import { QueuedComments } from "./QueuedComments";
import { QueuedComments as AndroidQueuedComments } from "./QueuedComments.android";
import { ReviewButton } from "./ReviewButton";
import { ReviewButton as AndroidReviewButton } from "./ReviewButton.android";
import type { ReviewComment } from "./reviewStore";

const comments: ReviewComment[] = [
	{ id: "c1", label: "auth.ts · Line 4", feedback: "Rename this.", status: "queued", target: { path: "auth.ts" } as ReviewComment["target"] },
	{ id: "c2", label: "auth.ts · Lines 9–12", feedback: "Why the retry?", status: "queued", target: { path: "auth.ts" } as ReviewComment["target"] },
];

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
	fixtures.armed.mockClear();
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

describe("the review button", () => {
	it("is a prominent glass capsule on iOS that opens the review", async () => {
		const onPress = vi.fn();
		await act(async () => { renderer = create(<ReviewButton count={2} onPress={onPress} />); });
		const button = renderer!.root.findByType("SwiftButton" as never);
		expect(button.props.modifiers).toEqual(expect.arrayContaining([
			{ buttonStyle: "glassProminent" },
			{ tint: "#accent" },
			{ foregroundStyle: "#onAccent" },
			{ accessibilityLabel: "Review 2 comments before sending" },
		]));
		expect(renderer!.root.findByType("SwiftText" as never).props.children).toBe("Send 2 comments");
		act(() => button.props.onPress());
		expect(onPress).toHaveBeenCalledOnce();
	});

	it("is Material's extended FAB on Android", async () => {
		const onPress = vi.fn();
		await act(async () => { renderer = create(<AndroidReviewButton count={1} onPress={onPress} />); });
		const fab = renderer!.root.findByType("ExtendedFloatingActionButton" as never);
		expect(fab.props.containerColor).toBe("#accent");
		expect(renderer!.root.findByType("Icon" as never).props).toMatchObject({ source: "drawable:review", tint: "#onAccent", contentDescription: "Review 1 comment before sending" });
		expect(renderer!.root.findByType("ComposeText" as never).props.children).toBe("Send 1 comment");
		act(() => fab.props.onClick());
		expect(onPress).toHaveBeenCalledOnce();
	});

	it("draws nothing with nothing queued", async () => {
		await act(async () => { renderer = create(<ReviewButton count={0} onPress={vi.fn()} />); });
		expect(renderer!.toJSON()).toBeNull();
	});
});

describe("the queued comments", () => {
	it("are a SwiftUI list whose swipe-to-delete removes the swiped row", async () => {
		const onRemove = vi.fn();
		await act(async () => { renderer = create(<QueuedComments comments={comments} onRemove={onRemove} />); });
		act(() => renderer!.root.findByType("ListForEach" as never).props.onDelete([1]));
		expect(onRemove).toHaveBeenCalledWith("c2");
		const rows = renderer!.root.findAllByType("VStack" as never);
		expect(rows[0].props.modifiers).toContainEqual({ accessibilityLabel: "Comment on auth.ts · Line 4: Rename this." });
	});

	it("swipe away on Android, with the delete button and TalkBack's action as well", async () => {
		const onRemove = vi.fn();
		await act(async () => { renderer = create(<AndroidQueuedComments comments={comments} onRemove={onRemove} />); });
		const host = (node: { type: unknown }) => typeof node.type === "string";
		const described = renderer!.root.findAll((node) => host(node) && node.props.accessibilityActions?.[0]?.name === "delete");
		expect(described).toHaveLength(2);
		act(() => described[0].props.onAccessibilityAction({ nativeEvent: { actionName: "delete" } }));
		expect(onRemove).toHaveBeenLastCalledWith("c1");
		const removeButtons = renderer!.root.findAll((node) => host(node) && node.props.accessibilityLabel === "Remove comment on auth.ts · Lines 9–12");
		act(() => removeButtons[0].props.onPress());
		expect(onRemove).toHaveBeenLastCalledWith("c2");
	});
});
