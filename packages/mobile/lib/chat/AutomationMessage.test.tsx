import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	theme: new Proxy({}, { get: () => "#000000" }),
	app: { sessions: [] as Array<{ id: string; projectId: string; harness?: string }>, config: { host: "127.0.0.1", hostId: "h1" } as object | null },
	getSession: vi.fn(),
	push: vi.fn(),
	openGitHub: vi.fn(),
}));
vi.mock("react-native", () => ({
	View: "View", Text: "Text", Pressable: "Pressable", ScrollView: "ScrollView",
	StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 },
}));
vi.mock("expo-router", () => ({ router: { push: fixtures.push } }));
vi.mock("../AgentLogo", () => ({ AgentLogo: "AgentLogo" }));
vi.mock("../api", () => ({ getSession: fixtures.getSession }));
vi.mock("../store", () => ({ useApp: () => fixtures.app }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn() } }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../openGitHub", () => ({ openGitHub: fixtures.openGitHub }));
vi.mock("../files/navigation", () => ({ useFilesNavigation: () => null }));
vi.mock("./ChatMarkdown", () => ({ ChatMarkdown: "ChatMarkdown", ChatLinkProvider: ({ children }: { children: unknown }) => children }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemedStyles: (factory: (theme: object) => object) => factory(fixtures.theme) }));

import { useState } from "react";
import { ConversationHarness, WorkerSessionsProvider, WorkerUpdateRows } from "./AutomationMessage";
import type { WorkerUpdate } from "./automationMessages";

const update = (key: string, sessionId: string): WorkerUpdate => ({ key, state: "done", name: sessionId, sessionId, projectId: "p", body: "Done.", createdAt: "2026-10-27T10:00:00Z" });

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.getSession.mockReset();
	fixtures.push.mockReset();
	fixtures.openGitHub.mockReset();
	fixtures.app.sessions = [];
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

const logos = () => renderer!.root.findAll((node) => (node.type as unknown) === "AgentLogo").map((node) => node.props.harness);

async function draw(updates: WorkerUpdate[]) {
	await act(async () => {
		renderer = create(<WorkerSessionsProvider><ConversationHarness.Provider value="claude-code"><WorkerUpdateRows updates={updates} /></ConversationHarness.Provider></WorkerSessionsProvider>);
	});
}

it("draws a live worker's own harness without asking the daemon", async () => {
	fixtures.app.sessions = [{ id: "w-live", projectId: "p", harness: "codex" }];
	await draw([update("a", "w-live")]);
	expect(logos()).toEqual(["codex"]);
	expect(fixtures.getSession).not.toHaveBeenCalled();
});

it("reads a worker the board leaves out once, and draws its harness", async () => {
	fixtures.getSession.mockResolvedValue({ id: "w-archived", projectId: "p", harness: "opencode" });
	await draw([update("a", "w-archived"), update("b", "w-archived")]);
	expect(logos()).toEqual(["opencode", "opencode"]);
	expect(fixtures.getSession).toHaveBeenCalledTimes(1);
});

it("falls back to the chat's own agent, never a generic glyph, when the worker cannot be read", async () => {
	fixtures.getSession.mockRejectedValue(new Error("not found"));
	await draw([update("a", "w-gone")]);
	expect(logos()).toEqual(["claude-code"]);
	expect(renderer!.root.findAll((node) => (node.type as unknown) === "Feather" && node.props.name === "cpu")).toEqual([]);
});

const textOf = (node: { children: unknown[] }): string => node.children.map((child) => typeof child === "string" ? child : textOf(child as { children: unknown[] })).join("");
const pressables = () => renderer!.root.findAll((node) => (node.type as unknown) === "Pressable");

it("opens a report anywhere on its row, with its links live and a way into the worker's chat", async () => {
	fixtures.app.sessions = [{ id: "w-1", projectId: "p", harness: "codex" }];
	const report = { ...update("a", "w-1"), body: "PR #7291 open\nAll CI checks pass: https://github.com/o/r/pull/7291\nSee [github.com/o/r/pull/9](https://evil.example/x) and [the PR](https://github.com/o/r/pull/7291)." };
	await draw([report]);
	expect(renderer!.root.findAll((node) => (node.type as unknown) === "Text" && node.props.accessibilityRole === "link")).toEqual([]);
	await act(async () => pressables()[0]!.props.onPress());
	// Only forge pages are live; a label is shown with the host it really goes to.
	const links = renderer!.root.findAll((node) => (node.type as unknown) === "Text" && node.props.accessibilityRole === "link");
	expect(links.map(textOf)).toEqual(["https://github.com/o/r/pull/7291", "the PR"]);
	expect(textOf(renderer!.root)).toContain("the PR (github.com)");
	expect(textOf(renderer!.root)).toContain("See github.com/o/r/pull/9 and");
	const link = links[0]!;
	await act(async () => link.props.onPress());
	expect(fixtures.openGitHub).toHaveBeenCalledWith("https://github.com/o/r/pull/7291");
	const openChat = pressables().find((node) => textOf(node).includes("Open chat"))!;
	await act(async () => openChat.props.onPress());
	expect(fixtures.push).toHaveBeenCalledWith({ pathname: "/session/[id]", params: { id: "w-1", projectId: "p", hostId: "h1" } });
});

it("opens an artifact from the worker's artifact folder in the viewer", async () => {
	fixtures.app.sessions = [{ id: "w-1", projectId: "p" }];
	await draw([{ ...update("a", "w-1"), body: "Wrote the audit.\nartifact: /Users/x/.ao/data/artifacts/w-1/audit.md\nartifact: /tmp/elsewhere.md" }]);
	const links = renderer!.root.findAll((node) => (node.type as unknown) === "Pressable" && node.props.accessibilityRole === "link");
	expect(links.map((node) => node.props.accessibilityLabel)).toEqual(["Open audit.md"]);
	await act(async () => links[0]!.props.onPress());
	expect(fixtures.push).toHaveBeenCalledWith({ pathname: "/artifact/[sessionId]", params: { sessionId: "w-1", entry: "audit.md", hostId: "h1" } });
});

it("shows a report headed by its first sentence once when opened, not twice", async () => {
	fixtures.app.sessions = [{ id: "w-1", projectId: "p" }];
	await draw([{ ...update("a", "w-1"), body: "Fixed the flaky test. All 29 checks pass now." }]);
	await act(async () => pressables()[0]!.props.onPress());
	expect(textOf(renderer!.root).split("Fixed the flaky test").length - 1).toBe(1);
	expect(textOf(renderer!.root)).toContain("All 29 checks pass now.");
});

it("asks again for a worker whose read failed, instead of caching the failure", async () => {
	fixtures.getSession.mockRejectedValueOnce(new Error("offline")).mockResolvedValue({ id: "w-retry", projectId: "p", harness: "goose" });
	await draw([update("a", "w-retry")]);
	expect(logos()).toEqual(["claude-code"]);
	act(() => renderer!.unmount());
	await draw([update("b", "w-retry")]);
	expect(logos()).toEqual(["goose"]);
	expect(fixtures.getSession).toHaveBeenCalledTimes(2);
});

it("leaves settled rows alone when the store changes nothing they show", async () => {
	fixtures.app.sessions = [{ id: "w-1", projectId: "p", harness: "codex" }];
	let storeTick!: () => void;
	// Stands in for the store: re-renders the provider, as every poll does, while the rows' element stays the same.
	const rows = <ConversationHarness.Provider value="claude-code"><WorkerUpdateRows updates={[update("a", "w-1"), update("b", "w-1")]} /></ConversationHarness.Provider>;
	function Store() {
		const [, setTick] = useState(0);
		storeTick = () => setTick((tick) => tick + 1);
		return <WorkerSessionsProvider>{rows}</WorkerSessionsProvider>;
	}
	await act(async () => { renderer = create(<Store />); });
	const drawn = () => renderer!.root.findAll((node) => (node.type as unknown) === "AgentLogo");
	const first = drawn().map((node) => node.props);

	// A poll: a new array holding the same sessions.
	fixtures.app.sessions = [{ id: "w-1", projectId: "p", harness: "codex" }];
	await act(async () => storeTick());
	expect(drawn().map((node) => node.props)).toEqual(first);
	expect(drawn().every((node, index) => node.props === first[index])).toBe(true);

	// Something a row shows changed: the rows draw it.
	fixtures.app.sessions = [{ id: "w-1", projectId: "p", harness: "opencode" }];
	await act(async () => storeTick());
	expect(logos()).toEqual(["opencode", "opencode"]);
});
