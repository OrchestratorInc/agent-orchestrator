import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

vi.mock("react-native", () => ({
	Image: "Image",
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
	useTheme: () => ({}),
	useThemedStyles: (factory: (theme: Record<string, string>) => unknown) => factory({}),
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

	act(() => renderer.unmount());
});
