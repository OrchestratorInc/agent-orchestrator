import { describe, expect, it, vi } from "vitest";

const { AgentLogo, Text, View } = vi.hoisted(() => ({
	AgentLogo: vi.fn(),
	Text: vi.fn(),
	View: vi.fn(),
}));

vi.mock("../AgentLogo", () => ({ AgentLogo }));
vi.mock("react-native", () => ({
	StyleSheet: { create: (styles: unknown) => styles },
	Text,
	useWindowDimensions: () => ({ width: 390, height: 844 }),
	View,
}));
vi.mock("../ThemeProvider", () => ({
	useTheme: () => ({ green: "green", orange: "orange", red: "red", amber: "amber" }),
	useThemedStyles: (factory: (theme: Record<string, string>) => unknown) => factory({
		textPrimary: "white",
		textSecondary: "silver",
		textTertiary: "gray",
	}),
}));

import { ConversationTitle, conversationStateLabel } from "./ConversationTitle";

type Element = { type: unknown; props: Record<string, unknown> & { children?: unknown } };
const childrenOf = (element: Element): Element[] => [element.props.children].flat(Infinity).filter(Boolean) as Element[];
const textsOf = (element: Element): string[] => element.type === Text
	? [String(element.props.children)]
	: childrenOf(element).flatMap((child) => typeof child === "object" ? textsOf(child) : []);

describe("ConversationTitle", () => {
	it("shows the active harness logo beside the worker title", () => {
		const element = ConversationTitle({ title: "Say hi", subtitle: "meetyou", harness: "claude-code", state: "ready" }) as unknown as Element;

		const children = childrenOf(element);
		expect(children[0].type).toBe(AgentLogo);
		expect(children[0].props).toMatchObject({ harness: "claude-code", size: 22 });
	});

	// The bar shows the state as a dot only; the word is for screen readers.
	it("shows the state as a dot, and names it only to screen readers", () => {
		const element = ConversationTitle({ title: "Say hi", subtitle: "meetyou", state: "busy" }) as unknown as Element;
		expect(textsOf(element)).toEqual(["Say hi", "meetyou"]);
		expect(element.props).toMatchObject({
			accessible: true,
			accessibilityRole: "header",
			accessibilityLabel: "Say hi, meetyou, Working",
		});
	});

	it("leaves the state out until the conversation reports one", () => {
		const element = ConversationTitle({ title: "Say hi", subtitle: "meetyou" }) as unknown as Element;
		expect(textsOf(element)).toEqual(["Say hi", "meetyou"]);
		expect(element.props.accessibilityLabel).toBe("Say hi, meetyou");
	});

	it("has a word for every controller state", () => {
		expect(["connecting", "ready", "busy", "recovering", "hibernated", "stopped"].map(conversationStateLabel))
			.toEqual(["Connecting", "Ready", "Working", "Reconnecting", "Hibernated", "Stopped"]);
		expect(conversationStateLabel("paused")).toBe("Paused");
	});
});
