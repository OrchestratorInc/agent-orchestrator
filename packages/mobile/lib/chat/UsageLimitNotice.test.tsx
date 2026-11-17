import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ appState: [] as Array<(state: string) => void> }));

vi.mock("react-native", () => ({
	AppState: {
		addEventListener: (_: string, listener: (state: string) => void) => {
			fixtures.appState.push(listener);
			return { remove: () => { fixtures.appState = fixtures.appState.filter((entry) => entry !== listener); } };
		},
	},
	Platform: { OS: "ios" },
	StyleSheet: { create: (styles: object) => styles, hairlineWidth: 0.5 },
	Text: "Text",
	View: "View",
}));
vi.mock("../ThemeProvider", () => ({
	useTheme: () => ({ amber: "#amber", red: "#red", textTertiary: "#tertiary" }),
	useThemedStyles: (factory: (theme: object) => object) => factory({}),
}));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../ui", async () => {
	const React = await import("react");
	return { Button: (props: { title: string }) => React.createElement("Button", props) };
});

import type { ConversationTurn } from "./types";
import { AccountHandoffRow, TurnRecoveryContext, UsageLimitNotice, type TurnRecovery } from "./UsageLimitNotice";

type LimitedTurn = ConversationTurn & { usageLimit: NonNullable<ConversationTurn["usageLimit"]> };

const NOW = Date.parse("2026-11-17T12:00:00Z");
const RESET = "2026-11-17T13:00:00Z";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.useFakeTimers({ now: NOW });
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.useRealTimers();
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

const turn = (usageLimit: LimitedTurn["usageLimit"], patch: Partial<ConversationTurn> = {}): LimitedTurn => ({
	id: "t1", state: "failed", requestedAt: "x", providerTurnId: "p1", hasHumanPrompt: true,
	errorMessage: "You've hit your usage limit.", ...patch, usageLimit,
});

function recovery(patch: Partial<TurnRecovery> = {}): TurnRecovery {
	return {
		limitedTurnId: "t1",
		resume: vi.fn(async () => {}),
		setAutoResume: vi.fn(async () => {}),
		accountLabel: (id) => (id === "a2" ? "work@example.com" : undefined),
		...patch,
	};
}

function render(element: React.ReactElement, value: TurnRecovery | null = recovery()) {
	act(() => { renderer = create(<TurnRecoveryContext.Provider value={value}>{element}</TurnRecoveryContext.Provider>); });
	return renderer!.root;
}

const buttons = (root: ReactTestRenderer["root"]) => root.findAllByType("Button" as never).map((button) => button.props.title as string);
const texts = (root: ReactTestRenderer["root"]) => root.findAllByType("Text" as never).map((text) => [text.props.children].flat().join(""));

describe("UsageLimitNotice", () => {
	it("offers auto-resume before the reset and arms it for this turn", async () => {
		const value = recovery();
		const root = render(<UsageLimitNotice turn={turn({ autoResume: false, resetsAt: RESET })} />, value);
		expect(buttons(root)).toEqual(["Auto-resume at reset"]);
		expect(texts(root)).toContain("You've hit your usage limit.");
		expect(texts(root).some((text) => text.startsWith("Resets "))).toBe(true);
		await act(async () => root.findByProps({ title: "Auto-resume at reset" }).props.onPress());
		expect(value.setAutoResume).toHaveBeenCalledWith("t1", true);
	});

	it("offers cancelling while armed", async () => {
		const value = recovery();
		const root = render(<UsageLimitNotice turn={turn({ autoResume: true, resetsAt: RESET })} />, value);
		expect(buttons(root)).toEqual(["Cancel auto-resume"]);
		await act(async () => root.findByProps({ title: "Cancel auto-resume" }).props.onPress());
		expect(value.setAutoResume).toHaveBeenCalledWith("t1", false);
	});

	it("turns into Resume when the reset passes", async () => {
		const value = recovery();
		const root = render(<UsageLimitNotice turn={turn({ autoResume: false, resetsAt: RESET })} />, value);
		act(() => { vi.advanceTimersByTime(60 * 60_000); });
		expect(buttons(root)).toEqual(["Resume"]);
		await act(async () => root.findByProps({ title: "Resume" }).props.onPress());
		expect(value.resume).toHaveBeenCalledWith("t1");
	});

	it("rechecks the reset when the app comes back to the foreground", () => {
		const root = render(<UsageLimitNotice turn={turn({ autoResume: false, resetsAt: RESET })} />);
		// A suspended app's timer never fired; the clock moved on regardless.
		vi.setSystemTime(Date.parse(RESET) + 1);
		act(() => fixtures.appState.forEach((listener) => listener("active")));
		expect(buttons(root)).toEqual(["Resume"]);
	});

	it("shows a failed request in place", async () => {
		const value = recovery({ resume: vi.fn(async () => { throw new Error("Reconnecting to this machine."); }) });
		const root = render(<UsageLimitNotice turn={turn({ autoResume: false, resetsAt: null })} />, value);
		await act(async () => root.findByProps({ title: "Resume" }).props.onPress());
		expect(texts(root)).toContain("Reconnecting to this machine.");
	});

	it("shows the daemon's last auto-resume failure", () => {
		const root = render(<UsageLimitNotice turn={turn({ autoResume: true, resetsAt: "2026-11-17T11:00:00Z", resumeError: "The agent needs input." })} />);
		expect(texts(root)).toEqual(expect.arrayContaining(["Auto-resume is waiting to retry.", "The agent needs input."]));
	});

	it("only marks older or unrecoverable limited turns", () => {
		const root = render(<UsageLimitNotice turn={turn({ autoResume: false, resetsAt: RESET }, { id: "t0" })} />);
		expect(buttons(root)).toEqual([]);
		expect(texts(root)).toEqual(["Usage limit reached", "You've hit your usage limit."]);
	});
});

describe("AccountHandoffRow", () => {
	it("names the account the session moved to and whether the prompt went again", () => {
		const root = render(<AccountHandoffRow note={{ accountId: "a2", retry: "sent" }} />);
		expect(texts(root)).toEqual(["Usage limit reached. Moved to work@example.com", "Prompt sent again."]);
	});

	it("falls back when the account is not known here", () => {
		const root = render(<AccountHandoffRow note={{ accountId: "gone" }} />, null);
		expect(texts(root)).toEqual(["Usage limit reached. Moved to another account"]);
	});
});
