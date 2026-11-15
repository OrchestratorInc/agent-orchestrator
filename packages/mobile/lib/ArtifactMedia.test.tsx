import { createElement } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const player = vi.hoisted(() => ({ status: "loading" as string, sources: [] as unknown[] }));

vi.mock("react-native", () => ({
	ActivityIndicator: "ActivityIndicator",
	Pressable: "Pressable",
	StyleSheet: { create: (styles: unknown) => styles },
	Text: "Text",
	View: "View",
}));
vi.mock("expo", () => ({
	useEvent: (_player: unknown, event: string, initial: object) => (event === "statusChange" ? { status: player.status } : initial),
}));
vi.mock("expo-video", () => ({
	useVideoPlayer: (source: unknown) => {
		player.sources.push(source);
		return { pause: vi.fn(), play: vi.fn(), playing: false, currentTime: 0, duration: 0, status: player.status };
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
});
