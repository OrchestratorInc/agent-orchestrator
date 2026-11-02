/**
 * Text an agent or a reviewer wrote goes through the real ChatMarkdown, so these
 * check the links it actually draws, not only what a tap would do.
 */
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	theme: new Proxy({}, { get: (_target, key) => key === "fontMono" ? "Mono" : "#000000" }),
	app: { sessions: [{ id: "w-7", projectId: "p", harness: "codex", kind: "worker", displayName: "Tile" }], config: { host: "127.0.0.1", hostId: "h1" } },
}));
vi.mock("react-native", () => ({
	NativeModules: {},
	findNodeHandle: (view: unknown) => view,
	Image: "Image",
	Platform: { OS: "ios" },
	Pressable: "Pressable",
	ScrollView: "ScrollView",
	StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
	Text: "Text",
	View: "View",
}));
vi.mock("@bsky.app/react-native-uitextview", () => ({ UITextView: "UITextView" }));
vi.mock("expo-clipboard", () => ({ setStringAsync: vi.fn() }));
vi.mock("expo-router", () => ({ router: { push: vi.fn() } }));
vi.mock("../AgentLogo", () => ({ AgentLogo: "AgentLogo" }));
vi.mock("../api", () => ({ getSession: vi.fn() }));
vi.mock("../store", () => ({ useApp: () => fixtures.app }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), select: vi.fn(), success: vi.fn() } }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../openGitHub", () => ({ openGitHub: vi.fn() }));
vi.mock("../files/navigation", () => ({ useFilesNavigation: () => null }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemedStyles: (factory: (theme: object) => unknown) => factory(fixtures.theme) }));

import { OriginMessageView, WorkerSessionsProvider } from "./AutomationMessage";
import type { ConversationMessage } from "./types";

const UNSAFE = [
	"Merged: see [Fix PR #12](https://github.com/attacker/repo/pull/1)",
	"and [github.com/o/r/pull/9](https://evil.example/x).",
	"Mirror https://evil.example/y for logs.",
	"![status](https://evil.example/pixel.png)",
].join("\n");

const PR = 'PR #7323 "feat(mobile): clearer pairing" (a → main)';
const reviewComment = [
	`The following 1 unresolved review comment(s) are on ${PR} as of just now.`,
	"",
	"1. src/a.ts:3 (@reviewer):",
	UNSAFE,
	"",
	"Address each comment and push fixes.",
	"PR: https://github.com/o/r/pull/7323",
].join("\n");

const message = (text: string, extra: Partial<ConversationMessage> = {}): ConversationMessage =>
	({ kind: "message", id: "m1", sequence: 1, revision: 1, role: "user", origin: "automation", text, streaming: false, createdAt: "2026-10-27T10:00:00Z", ...extra });

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
});

const textOf = (node: { children: unknown[] }): string => node.children.map((child) => typeof child === "string" ? child : textOf(child as { children: unknown[] })).join("");

function draw(item: ConversationMessage) {
	act(() => { renderer = create(<WorkerSessionsProvider><OriginMessageView message={item} fallback={null} /></WorkerSessionsProvider>); });
	return {
		// ChatMarkdown draws through UITextView as well as Text; a link is whatever claims the role.
		links: renderer!.root.findAll((node) => typeof node.type === "string" && node.props.accessibilityRole === "link").map(textOf),
		images: renderer!.root.findAll((node) => (node.type as unknown) === "Image"),
		text: textOf(renderer!.root),
	};
}

it.each([
	["a message from another agent", message(UNSAFE, { senderSessionId: "w-7", senderProjectId: "p" })],
	["a review comment from GitHub", message(reviewComment)],
])("draws only safe links in %s, naming the host behind a forge label", (_kind, item) => {
	const { links, images, text } = draw(item);
	// The forge link stays a link, with the host it really goes to beside its label.
	expect(links.filter((label) => label === "Fix PR #12")).toHaveLength(1);
	expect(text).toContain("Fix PR #12 (github.com)");
	// Nothing else is drawn as a link, whatever its label claims.
	expect(links.filter((label) => label !== "Fix PR #12" && label !== "#7323")).toEqual([]);
	expect(text).toContain("github.com/o/r/pull/9");
	expect(text).toContain("https://evil.example/y");
	// An image loads nothing from where the text points; its alt text stays.
	expect(images).toEqual([]);
	expect(text).toContain("status");
});
