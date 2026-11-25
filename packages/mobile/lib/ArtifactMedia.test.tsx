import { createElement } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const player = vi.hoisted(() => ({ status: "loading" as string, sources: [] as unknown[] }));
const platform = vi.hoisted(() => ({ OS: "ios" as string }));

vi.mock("react-native", () => ({
	AccessibilityInfo: { isScreenReaderEnabled: () => Promise.resolve(false), addEventListener: () => ({ remove: () => {} }) },
	ActivityIndicator: "ActivityIndicator",
	Platform: platform,
	Pressable: "Pressable",
	StyleSheet: { create: (styles: unknown) => styles, absoluteFill: {} },
	Text: "Text",
	View: "View",
}));
vi.mock("react-native-safe-area-context", () => ({ SafeAreaView: "SafeAreaView" }));
vi.mock("expo", () => ({
	useEvent: (_player: unknown, event: string, initial: object) => (event === "statusChange" ? { status: player.status } : initial),
}));
vi.mock("expo-video", () => ({
	useVideoPlayer: (source: unknown) => {
		player.sources.push(source);
		return { pause: vi.fn(), play: vi.fn(), seekBy: vi.fn(), playing: false, currentTime: 0, duration: 0, status: player.status };
	},
	VideoView: "VideoView",
}));
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn() } }));
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => ({}), useThemedStyles: () => ({}) }));
vi.mock("./tokens", () => ({ fontScaleCap: {}, iconSize: {}, space: {}, type: {} }));
vi.mock("./ui", () => ({ EmptyState: "EmptyState" }));
vi.mock("./useHeaderOverlap", () => ({ useGlassBarInset: () => 0 }));

import { ArtifactMedia } from "./ArtifactMedia";

let renderer: ReactTestRenderer | undefined;

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	player.status = "loading";
	player.sources.length = 0;
	platform.OS = "ios";
});

function element(props: { active: boolean; uri?: string; renew?: () => void }) {
	return createElement(ArtifactMedia, { source: { uri: props.uri ?? "https://signed/old/clip.mp4" }, kind: "video", active: props.active, name: "clip.mp4", label: "MP4 file", renew: props.renew });
}

function render(props: Parameters<typeof element>[0]) {
	act(() => {
		if (renderer) renderer.update(element(props));
		else renderer = create(element(props));
	});
	return renderer!;
}

function titles(root: ReactTestRenderer): string[] {
	return root.root.findAll((node) => (node.type as unknown) === "EmptyState").map((node) => node.props.title as string);
}

describe("ArtifactMedia", () => {
	it("loads nothing for a page beside the one on screen, and its file once shown", () => {
		render({ active: false });
		expect(player.sources.every((source) => source === null)).toBe(true);
		render({ active: true });
		expect(player.sources.at(-1)).toEqual({ uri: "https://signed/old/clip.mp4" });
	});

	it("asks for fresh access once when a Cloud address fails, and waits for it instead of failing", () => {
		const renew = vi.fn();
		player.status = "error";
		const root = render({ active: true, renew });
		expect(renew).toHaveBeenCalledTimes(1);
		expect(titles(root)).toEqual([]);
		expect(root.root.findAll((node) => (node.type as unknown) === "ActivityIndicator")).toHaveLength(1);
	});

	it("says it can't play when the renewed address fails too", () => {
		const renew = vi.fn();
		player.status = "error";
		render({ active: true, renew });
		const root = render({ active: true, renew, uri: "https://signed/new/clip.mp4" });
		expect(renew).toHaveBeenCalledTimes(1);
		expect(titles(root)).toEqual(["This video can't play on the phone"]);
	});

	it("says it can't play at once where nothing can be renewed (a machine's file)", () => {
		player.status = "error";
		const root = render({ active: true });
		expect(titles(root)).toEqual(["This video can't play on the phone"]);
	});

	it("draws its own controls on iOS, where the platform player's never came up", () => {
		player.status = "readyToPlay";
		const root = render({ active: true });
		const video = root.root.find((node) => (node.type as unknown) === "VideoView");
		expect(video.props.nativeControls).toBe(false);
		const labels = root.root.findAll((node) => (node.type as unknown) === "Pressable" && typeof node.props.accessibilityLabel === "string").map((node) => node.props.accessibilityLabel as string);
		expect(labels).toEqual(expect.arrayContaining(["Play", "Position", "Full screen"]));
	});

	it("keeps the controls up when the picture of a paused video is tapped", () => {
		player.status = "readyToPlay";
		const root = render({ active: true });
		const picture = root.root.find((node) => (node.type as unknown) === "Pressable" && node.props.accessible === false);
		act(() => picture.props.onPress());
		expect(root.root.findAll((node) => node.props.accessibilityLabel === "Play")).toHaveLength(1);
		expect(root.root.findAll((node) => node.props.accessibilityLabel === "Full screen")).toHaveLength(1);
	});

	it("keeps the platform's controls on Android", () => {
		platform.OS = "android";
		player.status = "readyToPlay";
		const root = render({ active: true });
		expect(root.root.find((node) => (node.type as unknown) === "VideoView").props.nativeControls).toBe(true);
		expect(root.root.findAll((node) => node.props.accessibilityLabel === "Play")).toHaveLength(0);
	});
});
