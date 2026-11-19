import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DashboardPR, DashboardSession, SessionPRSummary } from "./api";

const fixture = vi.hoisted(() => ({
	platform: { OS: "ios" as "ios" | "android" },
	openPage: vi.fn(),
	push: vi.fn(),
	openGitHub: vi.fn(),
	copy: vi.fn(),
	haptics: { tap: vi.fn(), success: vi.fn() },
	show: vi.fn(),
}));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Platform: fixture.platform,
		Pressable: host("Pressable"),
		StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 0.5 },
		Text: host("Text"),
		View: host("View"),
	};
});
vi.mock("@expo/ui/community/menu", async () => {
	const React = await import("react");
	return {
		MenuView: ({ ref, children, ...props }: { ref?: React.Ref<{ show(): void }>; children?: React.ReactNode }) => {
			React.useImperativeHandle(ref, () => ({ show: fixture.show }));
			return React.createElement("MenuView", props, children);
		},
	};
});
vi.mock("expo-clipboard", () => ({ setStringAsync: fixture.copy }));
// ./api reaches storage and telemetry modules that need a device.
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {} }));
vi.mock("expo-secure-store", () => ({}));
vi.mock("./installId", () => ({}));
vi.mock("./sentry", () => ({}));
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("./haptics", () => ({ haptics: fixture.haptics }));
vi.mock("./openGitHub", () => ({ openGitHub: fixture.openGitHub }));
vi.mock("./pageNavigation", () => ({ useOpenPage: () => fixture.openPage }));
vi.mock("expo-router", () => ({ useRouter: () => ({ push: fixture.push }) }));
vi.mock("./ThemeProvider", () => {
	const theme = new Proxy({}, { get: (_, key) => `#${String(key)}` });
	return { useTheme: () => theme, useThemeState: () => ({ scheme: "dark" }), useThemedStyles: (factory: (value: unknown) => unknown) => factory(theme) };
});

import { PRCard } from "./PRCard";

const pr: DashboardPR = { number: 42, url: "https://github.com/ao/app/pull/42", state: "open" };
const session = {
	id: "worker-1", hostId: "mac", projectId: "app", displayName: "Fix the build", status: "idle", mode: "chat",
	branch: "fix-build", createdAt: "2026-10-11T00:00:00Z", lastActivityAt: "2026-10-11T00:00:00Z", pr,
} as unknown as DashboardSession;

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixture.platform.OS = "ios";
	for (const fn of [fixture.openPage, fixture.push, fixture.openGitHub, fixture.copy, fixture.show, fixture.haptics.tap, fixture.haptics.success]) fn.mockClear();
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.unstubAllGlobals();
});

function render(props: { summary?: SessionPRSummary; onLinkCopied?: () => void } = {}) {
	act(() => {
		renderer = create(<PRCard pr={pr} session={session} hostId="mac" {...props} />);
	});
	return renderer!;
}
function row(): ReactTestInstance {
	return renderer!.root.findByType("Pressable" as never);
}

describe("PRCard", () => {
	// A tap goes to the review; going somewhere is not a moment worth a buzz.
	it("opens the pull request's review on a tap, without a haptic", () => {
		render();
		act(() => row().props.onPress());
		expect(fixture.openPage).toHaveBeenCalledWith({
			pathname: "/review/[sessionId]",
			params: { sessionId: "worker-1", prNumber: "42", prUrl: pr.url, hostId: "mac" },
		});
		expect(fixture.haptics.tap).not.toHaveBeenCalled();
	});

	it("offers Open worker, Open in GitHub and Copy link from a long-press menu, and opens the worker's chat", () => {
		render();
		const menu = renderer!.root.findByType("MenuView" as never);
		expect(menu.props.shouldOpenOnLongPress).toBe(true);
		expect(menu.props.actions.map((action: { title: string }) => action.title)).toEqual(["Open worker", "Open in GitHub", "Copy link"]);
		act(() => menu.props.onPressAction({ nativeEvent: { event: "openWorker" } }));
		expect(fixture.push).toHaveBeenCalledWith({ pathname: "/session/[id]", params: { id: "worker-1", projectId: "app", hostId: "mac" } });
	});

	it("opens the summary's GitHub page from the menu", () => {
		render({ summary: { htmlUrl: "https://github.com/ao/app/pull/42#files" } as SessionPRSummary });
		act(() => renderer!.root.findByType("MenuView" as never).props.onPressAction({ nativeEvent: { event: "openInGitHub" } }));
		expect(fixture.openGitHub).toHaveBeenCalledWith("https://github.com/ao/app/pull/42#files");
	});

	it("copies the link, confirms it, and tells the screen", () => {
		const onLinkCopied = vi.fn();
		render({ onLinkCopied });
		act(() => renderer!.root.findByType("MenuView" as never).props.onPressAction({ nativeEvent: { event: "copyLink" } }));
		expect(fixture.copy).toHaveBeenCalledWith(pr.url);
		expect(fixture.haptics.success).toHaveBeenCalledTimes(1);
		expect(onLinkCopied).toHaveBeenCalledTimes(1);
	});

	// The GitHub link used to be a control nested inside the row, which screen
	// readers could not reach. It is one row now, and VoiceOver's actions rotor
	// and TalkBack's actions menu reach the menu's choices.
	it("is one control that names the menu's choices as accessibility actions", () => {
		const onLinkCopied = vi.fn();
		render({ onLinkCopied });
		expect(renderer!.root.findAllByType("Pressable" as never)).toHaveLength(1);
		expect(row().props.accessibilityActions).toEqual([
			{ name: "openWorker", label: "Open worker" },
			{ name: "openInGitHub", label: "Open in GitHub" },
			{ name: "copyLink", label: "Copy link" },
		]);
		act(() => row().props.onAccessibilityAction({ nativeEvent: { actionName: "openInGitHub" } }));
		act(() => row().props.onAccessibilityAction({ nativeEvent: { actionName: "copyLink" } }));
		expect(fixture.openGitHub).toHaveBeenCalledWith(pr.url);
		expect(onLinkCopied).toHaveBeenCalledTimes(1);
	});

	// The row leads with what the pull request does, and says only what needs
	// you: waiting for review is not printed on every row.
	it("shows the title without its commit prefix, the failing check, and nothing for a plain review wait", () => {
		const summary = {
			title: "fix(mobile): build the menu only when it opens", state: "open", repo: "ao/app",
			ci: { state: "failing", failingChecks: [{ name: "android-build" }] },
			review: { decision: "review_required", hasUnresolvedHumanComments: false, unresolvedBy: [] },
			mergeability: { state: "blocked", reasons: ["review_required"] },
		} as unknown as SessionPRSummary;
		render({ summary });
		const text = renderer!.root.findAllByType("Text" as never).map((node) => node.children.join("")).join("|");
		expect(text).toContain("Build the menu only when it opens");
		expect(text).toContain("android-build failed");
		expect(text).not.toMatch(/review required/i);
		expect(row().props.accessibilityLabel).toBe("Build the menu only when it opens. Pull request 42. android-build failed.");
	});

	// A screen reader hears what the row shows: the scope that fills the line
	// when there is no status, and how long ago the pull request changed.
	it("speaks the row's scope and age", () => {
		const summary = {
			title: "fix(mobile): build the menu only when it opens", state: "open", repo: "ao/app", updatedAt: "2026-11-12T10:00:00Z",
			ci: { state: "passing", failingChecks: [] },
			review: { decision: "review_required", hasUnresolvedHumanComments: false, unresolvedBy: [] },
			mergeability: { state: "blocked", reasons: ["review_required"] },
		} as unknown as SessionPRSummary;
		act(() => {
			renderer = create(<PRCard pr={pr} session={session} hostId="mac" summary={summary} now={Date.parse("2026-11-12T15:00:00Z")} />);
		});
		expect(row().props.accessibilityLabel).toBe("Build the menu only when it opens. mobile. Pull request 42. open. Updated 5h ago.");
	});

	// iOS's context menu takes the long press natively. On Android the row's own
	// press handler holds the touch, so the row opens the dropdown itself.
	it("opens the menu from the row's long press on Android only", () => {
		render();
		expect(row().props.onLongPress).toBeUndefined();
		act(() => renderer!.unmount());
		fixture.platform.OS = "android";
		vi.stubGlobal("requestAnimationFrame", (callback: () => void) => { callback(); return 0; });
		render();
		act(() => row().props.onPressIn({ nativeEvent: { locationX: 120, locationY: 30 } }));
		act(() => row().props.onLongPress());
		expect(fixture.show).toHaveBeenCalledTimes(1);
		// Opened at the finger, from an anchor beside the row rather than around it.
		const menu = renderer!.root.findByType("MenuView" as never);
		expect(menu.props.style).toEqual(expect.arrayContaining([{ left: 120, top: 30 }]));
		expect(menu.findAllByType("Pressable" as never)).toHaveLength(0);
	});
});
