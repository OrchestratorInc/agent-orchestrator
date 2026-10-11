import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const { platform, theme } = vi.hoisted(() => ({
	platform: { OS: "ios" },
	theme: { accent: "#f4f5f7", accentBorder: "rgba(244,245,247,0.28)" },
}));

vi.mock("react-native", () => ({
	Image: "Image",
	Platform: platform,
	Pressable: "Pressable",
	ScrollView: "ScrollView",
	StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
	Text: "Text",
	View: "View",
}));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("@bsky.app/react-native-uitextview", () => ({ UITextView: "UITextView" }));
vi.mock("expo-clipboard", () => ({ setStringAsync: vi.fn() }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), select: vi.fn(), success: vi.fn() } }));
vi.mock("../openGitHub", () => ({ openGitHub: vi.fn() }));
vi.mock("../ThemeProvider", () => ({
	useTheme: () => theme,
	useThemedStyles: (factory: (theme: Record<string, string>) => unknown) => factory(theme),
}));

import { ChatMarkdown } from "./ChatMarkdown";

function textOf(node: { children: Array<string | { children: unknown[] }> }): string {
	return node.children.map((child) => typeof child === "string" ? child : textOf(child as typeof node)).join("");
}

it("lets readers select response prose in every rendered Markdown block", () => {
	let renderer!: ReactTestRenderer;
	act(() => {
		renderer = create(<ChatMarkdown text={[
			"# Heading response",
			"",
			"- List response",
			"",
			"> Quoted response",
			"",
			"| Column response |",
			"| --- |",
			"| Cell response |",
			"",
			"![Image caption](https://example.com/image.png)",
			"",
			"```text",
			"Code response",
			"```",
			"",
			"Paragraph response",
		].join("\n")} />);
	});

	const selectableText = renderer.root.findAll((node) => node.props.selectable === true)
		.map(textOf);
	for (const response of ["Heading response", "List response", "Quoted response", "Column response", "Cell response", "Image caption", "Code response", "Paragraph response"]) {
		expect(selectableText, `${response} should support native text selection`).toContain(response);
	}
	const rangeSelectableText = renderer.root.findAll((node) => node.props.selectable === true && node.props.uiTextView === true)
		.map(textOf);
	for (const response of ["Heading response", "List response", "Quoted response", "Column response", "Cell response", "Image caption", "Code response", "Paragraph response"]) {
		expect(rangeSelectableText, `${response} should support native range selection on iOS`).toContain(response);
	}
	for (const node of renderer.root.findAll((node) => node.props.uiTextView === true)) {
		expect(node.props.selectionColor).toBe(theme.accent);
	}

	act(() => renderer.unmount());
});

it("uses the spawn prompt's translucent selection gray on Android", () => {
	platform.OS = "android";
	let renderer!: ReactTestRenderer;
	try {
		act(() => { renderer = create(<ChatMarkdown text={"Paragraph response\n\n```text\nCode response\n```"} />); });
		const selectable = renderer.root.findAll((node) => node.props.uiTextView === true);
		expect(selectable.map(textOf)).toEqual(["Paragraph response", "Code response"]);
		for (const node of selectable) expect(node.props.selectionColor).toBe(theme.accentBorder);
		act(() => renderer.unmount());
	} finally {
		platform.OS = "ios";
	}
});

it("keeps inline links accessible on iOS while plain prose remains range-selectable", () => {
	let renderer!: ReactTestRenderer;
	act(() => {
		renderer = create(<ChatMarkdown text={[
			"# Heading [docs](https://example.com/heading)",
			"",
			"> Quote https://example.com/quote",
			"",
			"- List [docs](https://example.com/list)",
			"",
			"Paragraph [docs](https://example.com/paragraph)",
			"",
			"Plain response",
		].join("\n")} />);
	});
	const prose = renderer.root.findAll((node) => node.props.selectable === true);
	for (const prefix of ["Heading", "Quote", "List", "Paragraph"]) {
		const block = prose.find((node) => textOf(node).startsWith(prefix));
		expect(block?.props.uiTextView, `${prefix} should preserve nested link accessibility`).toBe(false);
	}
	const plain = prose.find((node) => textOf(node) === "Plain response");
	expect(plain?.props.uiTextView).toBe(true);
	expect(renderer.root.findAll((node) => node.props.accessibilityRole === "link")).toHaveLength(4);
	act(() => renderer.unmount());
});

it("keeps Android link-containing prose range-selectable", () => {
	platform.OS = "android";
	let renderer!: ReactTestRenderer;
	try {
		act(() => { renderer = create(<ChatMarkdown text="Read [docs](https://example.com/docs)" />); });
		const prose = renderer.root.findAll((node) => node.props.selectable === true);
		expect(prose).toHaveLength(1);
		expect(prose[0].props.uiTextView).toBe(true);
		act(() => renderer.unmount());
	} finally {
		platform.OS = "ios";
	}
});
