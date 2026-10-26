import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DashboardPR, DashboardSession, SessionPRSummary } from "./api";

const fixture = vi.hoisted(() => ({
	platform: { OS: "ios" as "ios" | "android" },
	openPage: vi.fn(),
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
vi.mock("./ThemeProvider", () => {
	const theme = new Proxy({}, { get: (_, key) => `#${String(key)}` });
	return { useTheme: () => theme, useThemeState: () => ({ scheme: "dark" }), useThemedStyles: (factory: (value: unknown) => unknown) => factory(theme) };
});

import { PRCard } from "./PRCard";
import { symbols } from "./symbols";

const pr: DashboardPR = { number: 42, url: "https://github.com/ao/app/pull/42", state: "open" };
const session = {
	id: "worker-1", hostId: "mac", projectId: "app", displayName: "Fix the build", status: "idle", mode: "chat",
	branch: "fix-build", createdAt: "2026-10-11T00:00:00Z", lastActivityAt: "2026-10-11T00:00:00Z", pr,
} as unknown as DashboardSession;

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixture.platform.OS = "ios";
	for (const fn of [fixture.openPage, fixture.openGitHub, fixture.copy, fixture.show, fixture.haptics.tap, fixture.haptics.success]) fn.mockClear();
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

	// The GitHub link used to be a control nested inside the row, which screen
	// readers could not reach. It is one row now, with the rest in its menu.
	it("is one control, with no button nested inside it", () => {
		render();
		expect(renderer!.root.findAllByType("Pressable" as never)).toHaveLength(1);
	});

	it("offers Open in GitHub and Copy link from a long-press menu", () => {
		render();
		const menu = renderer!.root.findByType("MenuView" as never);
		expect(menu.props.shouldOpenOnLongPress).toBe(true);
		expect(menu.props.actions.map((action: { id: string; title: string }) => [action.id, action.title])).toEqual([
			["openInGitHub", "Open in GitHub"],
			["copyLink", "Copy link"],
		]);
		expect(menu.props.actions.map((action: { image: unknown }) => action.image)).toEqual(["arrow.up.right.square", "doc.on.doc"]);
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

	// VoiceOver's actions rotor and TalkBack's actions menu reach the same choices.
	it("names the menu's choices as accessibility actions", () => {
		const onLinkCopied = vi.fn();
		render({ onLinkCopied });
		expect(row().props.accessibilityActions).toEqual([
			{ name: "openInGitHub", label: "Open in GitHub" },
			{ name: "copyLink", label: "Copy link" },
		]);
		act(() => row().props.onAccessibilityAction({ nativeEvent: { actionName: "openInGitHub" } }));
		act(() => row().props.onAccessibilityAction({ nativeEvent: { actionName: "copyLink" } }));
		expect(fixture.openGitHub).toHaveBeenCalledWith(pr.url);
		expect(onLinkCopied).toHaveBeenCalledTimes(1);
	});

	// Compose draws a menu item's glyph only from a bundled image source, which
	// the shared registry provides under the same Material Symbol iOS's entry names.
	it("draws each Android menu glyph from the shared drawable registry", () => {
		const android = readFileSync(fileURLToPath(new URL("./pr-card-menu-icons.android.ts", import.meta.url)), "utf8");
		expect(android).toContain(`openInGitHub: materialDrawable("${symbols.openExternal.android}")`);
		expect(android).toContain(`copyLink: materialDrawable("${symbols.copy.android}")`);
		expect(android).not.toContain("require(");
	});

	// The eyebrow, details and blockers are quiet through colour and weight,
	// never smaller than footnote.
	it("sets no text below 13pt", () => {
		const source = readFileSync(fileURLToPath(new URL("./PRCard.tsx", import.meta.url)), "utf8");
		expect(source).not.toMatch(/type\.caption[12]/);
		expect(source).toMatch(/blockers:\s*\{[^}]*fontSize: type\.footnote\.fontSize/);
	});

	// iOS's context menu takes the long press natively. On Android the row's own
	// press handler holds the touch, so the row opens the dropdown itself.
	it("opens the menu from the row's long press on Android only", () => {
		render();
		expect(row().props.onLongPress).toBeUndefined();
		act(() => renderer!.unmount());
		fixture.platform.OS = "android";
		render();
		act(() => row().props.onLongPress());
		expect(fixture.show).toHaveBeenCalledTimes(1);
	});
});
