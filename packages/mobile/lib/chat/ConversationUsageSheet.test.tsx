import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Platform: { OS: "ios" },
		StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 0.5 },
		Text: host("Text"),
		View: host("View"),
	};
});
vi.mock("../ThemeProvider", async () => {
	const { darkTheme } = await import("../theme");
	return { useTheme: () => darkTheme, useThemedStyles: (factory: (theme: unknown) => unknown) => factory(darkTheme) };
});
vi.mock("../ui", async () => {
	const React = await import("react");
	return { SheetScreen: ({ title, subtitle, children }: { title: string; subtitle: string; children: React.ReactNode }) => React.createElement("SheetScreen", { title, subtitle }, children) };
});

import type { ConversationSnapshot } from "./types";
import { ConversationUsageSheet } from "./ConversationUsageSheet";

let renderer: ReactTestRenderer | undefined;

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
});

function render(snapshot: Partial<ConversationSnapshot>) {
	act(() => {
		renderer = create(<ConversationUsageSheet sessionTitle="Fix login" snapshot={snapshot as ConversationSnapshot} />);
	});
	return renderer!.root;
}

const usage = { contextUsed: 150_000, contextWindow: 200_000, inputTokens: 12_300, outputTokens: 4_500, cachedTokens: 0, totalTokens: 16_800, cost: 0.1234, currency: "$" };

describe("ConversationUsageSheet", () => {
	it("titles the sheet and names the session", () => {
		const sheet = render({ usage }).findByType("SheetScreen" as never);
		expect(sheet.props).toMatchObject({ title: "Context & usage", subtitle: "Fix login" });
	});

	// A meter whose value only its fill shows is invisible to a screen reader.
	it("reports each meter's value to screen readers", () => {
		const root = render({ usage, rateLimits: { primaryUsedPercent: 82.4, primaryResetsInSeconds: 7200, planLabel: "Max plan" } as ConversationSnapshot["rateLimits"] });
		const meters = root.findAll((node) => typeof node.type === "string" && node.props.accessibilityRole === "progressbar");
		expect(meters.map((meter) => [meter.props.accessibilityLabel, meter.props.accessibilityValue])).toEqual([
			["Context window", { min: 0, max: 100, now: 75, text: "75% used" }],
			["Max plan", { min: 0, max: 100, now: 82, text: "82% used" }],
		]);
		const text = root.findAllByType("Text" as never).map((node) => node.props.children);
		expect(text).toContain("12.3K in · 4.5K out");
		expect(text).toContain("$0.1234");
		expect(text).toContain("Resets in 2h");
	});

	it("says so when the agent has reported nothing", () => {
		const text = render({}).findAllByType("Text" as never).map((node) => node.props.children);
		expect(text).toEqual(["The agent hasn't reported usage for this conversation yet."]);
	});
});
