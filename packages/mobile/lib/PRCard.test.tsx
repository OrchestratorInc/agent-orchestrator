import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { DashboardPR, DashboardSession, SessionPRSummary } from "./api";

const fixtures = vi.hoisted(() => ({
	push: vi.fn(),
	openPage: vi.fn(),
	openGitHub: vi.fn(async (_url: string) => {}),
	tap: vi.fn(),
	menu: undefined as undefined | { actions: { id: string; title: string }[]; onAction(id: string): void },
	theme: new Proxy({}, { get: () => "#000000" }),
}));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { Pressable: host("Pressable"), Text: host("Text"), View: host("View"), StyleSheet: { create: (styles: object) => styles, hairlineWidth: 1 } };
});
vi.mock("expo-router", () => ({ useRouter: () => ({ push: fixtures.push }) }));
vi.mock("./api", () => ({
	sessionTitle: (session: DashboardSession) => session.displayName ?? session.id,
	shortLabel: (value: string) => value,
}));
// Captures the menu the card builds, so each action can be chosen directly.
vi.mock("./long-press-menu", async () => {
	const React = await import("react");
	return {
		LongPressMenu: ({ children, actions, onAction }: { children?: React.ReactNode; actions: { id: string; title: string }[]; onAction(id: string): void }) => {
			fixtures.menu = { actions, onAction };
			return React.createElement("LongPressMenu", null, children);
		},
	};
});
vi.mock("./pageNavigation", () => ({ useOpenPage: () => fixtures.openPage }));
vi.mock("./openGitHub", () => ({ openGitHub: fixtures.openGitHub }));
vi.mock("./haptics", () => ({ haptics: { tap: fixtures.tap } }));
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemedStyles: (factory: (theme: object) => object) => factory(fixtures.theme) }));

import { PRCard } from "./PRCard";

const pr = { number: 12, url: "https://api.github.com/repos/acme/repo/pulls/12", state: "open", title: "Improve mobile review" } as DashboardPR;
const session = { id: "worker-1", projectId: "repo", status: "idle", displayName: "Fix the PR page", branch: "ao/fix" } as DashboardSession;
const summary = (over: Partial<SessionPRSummary>) => ({ number: 12, title: "Improve mobile review", state: "open", changedFiles: 0, additions: 0, deletions: 0, ...over }) as SessionPRSummary;

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	for (const spy of [fixtures.push, fixtures.openPage, fixtures.openGitHub, fixtures.tap]) spy.mockClear();
	fixtures.menu = undefined;
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
	vi.unstubAllGlobals();
});

function render(cardSummary?: SessionPRSummary) {
	act(() => renderer?.unmount());
	act(() => { renderer = create(<PRCard pr={pr} session={session} hostId="host-b" summary={cardSummary} />); });
	return fixtures.menu!;
}

it("offers the worker, the pull request and GitHub on long-press", () => {
	expect(render().actions.map((action) => action.title)).toEqual(["Open worker", "Open pull request", "Open on GitHub"]);
});

it("opens the worker's chat on the PR's own machine", () => {
	const menu = render();
	act(() => menu.onAction("worker"));
	expect(fixtures.push).toHaveBeenCalledExactlyOnceWith({ pathname: "/session/[id]", params: { id: "worker-1", projectId: "repo", hostId: "host-b" } });
	// The menu gives the haptic for its own items.
	expect(fixtures.tap).not.toHaveBeenCalled();
});

it("opens the pull request as a page, never inside a sheet", () => {
	const menu = render();
	act(() => menu.onAction("pr"));
	expect(fixtures.openPage).toHaveBeenCalledExactlyOnceWith({
		pathname: "/review/[sessionId]",
		params: { sessionId: "worker-1", prNumber: "12", prUrl: pr.url, hostId: "host-b" },
	});
	expect(fixtures.push).not.toHaveBeenCalled();
});

it("opens GitHub at the summary's html URL, then its URL, then the PR's", () => {
	for (const cardSummary of [summary({ htmlUrl: "https://github.com/acme/repo/pull/12", url: "https://api.example/12" }), summary({ url: "https://github.com/acme/repo/pull/12/files" }), undefined]) {
		const menu = render(cardSummary);
		act(() => menu.onAction("github"));
	}
	expect(fixtures.openGitHub.mock.calls.map(([url]) => url)).toEqual([
		"https://github.com/acme/repo/pull/12",
		"https://github.com/acme/repo/pull/12/files",
		pr.url,
	]);
});
