import { createElement } from "react";
import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
vi.mock("react-native", () => ({
	Platform: { OS: "ios" },
	Text: "Text",
	View: "View",
	StyleSheet: { create: <T,>(styles: T) => styles, hairlineWidth: 0.5 },
}));
vi.mock("../ui", () => ({
	Button: (props: { title: string }) => createElement("Button", props),
}));
vi.mock("../ThemeProvider", async () => {
	const { darkTheme } = await import("../theme");
	return { useTheme: () => darkTheme, useThemedStyles: (factory: (t: typeof darkTheme) => unknown) => factory(darkTheme) };
});

import { BrowserApprovalCard } from "./BrowserApprovalCard";
import type { ApprovalCard } from "./types";

let renderer: ReactTestRenderer | undefined;
afterEach(() => {
	if (renderer) act(() => renderer!.unmount());
	renderer = undefined;
	vi.useRealTimers();
});

const card = (over: Partial<ApprovalCard> = {}): ApprovalCard => ({
	id: "apr_1", sessionId: "s1", summary: `Click "Place order" on shop.example`, site: "shop.example",
	reason: "This pays money.", expiresAt: "2026-11-23T10:01:30Z", canDecide: true, canAllowSite: true, ...over,
});
const texts = () => renderer!.root.findAllByType("Text" as never).map((n: ReactTestInstance) => [n.props.children].flat().join(""));
const button = (title: string) => renderer!.root.findAll((n) => (n.type as unknown) === "Button" && n.props.title === title)[0];
const render = (props: Parameters<typeof BrowserApprovalCard>[0]) => act(() => { renderer = create(createElement(BrowserApprovalCard, props)); });

it("card shows action, site, countdown; Allow once / Always on site / Deny send the right decision", async () => {
	vi.useFakeTimers();
	vi.setSystemTime(new Date("2026-11-23T10:00:00Z"));
	const onDecide = vi.fn().mockResolvedValue("decided");
	render({ card: card(), onDecide });
	expect(texts()).toEqual(expect.arrayContaining([`Click "Place order" on shop.example`, "This pays money.", "shop.example · 1:30 left"]));
	for (const [title, decision] of [["Allow once", "allow-once"], ["Always on shop.example", "allow-site"], ["Deny", "deny"]] as const) {
		await act(async () => { button(title).props.onPress(); });
		expect(onDecide).toHaveBeenLastCalledWith(decision);
	}
	act(() => { vi.advanceTimersByTime(30_000); });
	expect(texts()).toContain("shop.example · 1:00 left");
});

it("late decision returns already-decided and says so, not an error", async () => {
	const onDecide = vi.fn().mockResolvedValue("already-decided");
	render({ card: card(), onDecide });
	await act(async () => { button("Deny").props.onPress(); });
	expect(texts()).toContain("Already decided");
	expect(button("Deny")).toBeUndefined();
});

it("a failed decision keeps the buttons and says it didn't send", async () => {
	render({ card: card(), onDecide: vi.fn().mockRejectedValue(new Error("offline")) });
	await act(async () => { button("Allow once").props.onPress(); });
	expect(texts()).toContain("Couldn't send your answer. Try again.");
	expect(button("Allow once")).toBeDefined();
});

it("read-only card for non-deciders", () => {
	render({ card: card({ canDecide: false, deciderName: "Ada" }), onDecide: vi.fn() });
	expect(texts()).toContain("Waiting for Ada to approve");
	expect(renderer!.root.findAll((n) => (n.type as unknown) === "Button")).toHaveLength(0);
});

it("no site means no Always button", () => {
	render({ card: card({ canAllowSite: false, site: "" }), onDecide: vi.fn() });
	expect(button("Always on ")).toBeUndefined();
	expect(renderer!.root.findAll((n) => (n.type as unknown) === "Button").map((n) => n.props.title)).toEqual(["Deny", "Allow once"]);
});
