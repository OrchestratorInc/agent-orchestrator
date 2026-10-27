import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const { platform, theme } = vi.hoisted(() => ({
	platform: { OS: "ios" },
	theme: { accent: "#f4f5f7", accentBorder: "rgba(244,245,247,0.28)", highlight: "#79b0dc", selection: "rgba(121,176,220,0.28)" },
}));

vi.mock("react-native", () => ({
	NativeModules: {},
	findNodeHandle: (view: unknown) => view,
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

import { ChatLinkProvider, ChatMarkdown, SelectableResponseText } from "./ChatMarkdown";
import { parseBlocks } from "./markdownBlocks";
import * as androidSelection from "./useAndroidResponseSelection";

function textOf(node: { children: Array<string | { children: unknown[] }> }): string {
	return node.children.map((child) => typeof child === "string" ? child : textOf(child as typeof node)).join("");
}

it("keeps Add to chat disabled when the surface has no registered composer", () => {
	const hook = vi.spyOn(androidSelection, "useAndroidResponseSelection").mockReturnValue(vi.fn());
	let renderer!: ReactTestRenderer;
	try {
		act(() => { renderer = create(<ChatMarkdown continuousSelection selectionSource={{ conversationId: "conversation", messageId: "response", revision: 1 }} text="Response" />); });
		expect(hook).toHaveBeenCalledWith(null);
		expect(renderer.root.find((node) => node.props.selectable === true).props.addToChatEnabled).toBe(false);
	} finally {
		act(() => renderer.unmount());
		hook.mockRestore();
	}
});

it.each(["ios", "android"])("selects a heading and adjacent paragraphs as one range on %s", (os) => {
	platform.OS = os;
	let renderer!: ReactTestRenderer;
	try {
		act(() => { renderer = create(<ChatMarkdown text={"# Getting this to production\n\nFirst paragraph.\n\nSecond paragraph."} />); });
		const selectable = renderer.root.findAll((node) => node.props.selectable === true);
		expect(selectable).toHaveLength(1);
		expect(selectable[0].props.uiTextView).toBe(true);
		expect(textOf(selectable[0])).toBe("Getting this to production\n\nFirst paragraph.\n\nSecond paragraph.");
		act(() => renderer.unmount());
	} finally {
		platform.OS = "ios";
	}
});

it.each(["ios", "android"])("offers one continuous selection across all response text on %s", (os) => {
	platform.OS = os;
	let renderer!: ReactTestRenderer;
	try {
		const blocks = parseBlocks([
			"# Title", "", "Body [link](https://example.com).", "", "- List item", "", "> Quoted text", "",
			"```text", "Code text", "```", "",
			"![Caption](https://example.com/image.png)",
		].join("\n"));
		act(() => { renderer = create(<SelectableResponseText blocks={blocks.filter((block) => block.kind !== "table")} />); });
		const selectable = renderer.root.findAll((node) => node.props.selectable === true);
		expect(selectable).toHaveLength(1);
		expect(selectable[0].props.uiTextView).toBe(true);
		const selectedText = textOf(selectable[0]);
		for (const part of ["Title", "Body link.", "List item", "Quoted text", "Code text", "Caption"]) expect(selectedText).toContain(part);
		expect(renderer.root.findAll((node) => node.props.accessibilityRole === "link")).toHaveLength(1);
		act(() => renderer.unmount());
	} finally {
		platform.OS = "ios";
	}
});

it.each(["ios", "android"])("renders the agent reply as one in-place selection across headings, bullets, and code on %s", (os) => {
	platform.OS = os;
	let renderer!: ReactTestRenderer;
	try {
		act(() => { renderer = create(<ChatMarkdown continuousSelection text={[
			"# Release steps", "", "Build the app.", "", "- Check iOS", "- Check Android", "",
			"```sh", "npm test", "```",
		].join("\n")} />); });
		const selectable = renderer.root.findAll((node) => node.props.selectable === true);
		expect(selectable).toHaveLength(1);
		expect(selectable[0].props.uiTextView).toBe(true);
		expect(textOf(selectable[0])).toBe("Release steps\n\nBuild the app.\n\n• Check iOS\n• Check Android\n\nsh\nnpm test");
		act(() => renderer.unmount());
	} finally {
		platform.OS = "ios";
	}
});

it("draws a table in an agent reply as a grid between the selectable text around it", () => {
	let renderer!: ReactTestRenderer;
	act(() => { renderer = create(<ChatMarkdown continuousSelection text={[
		"Before the table.", "",
		"Job | Superset | AO",
		"--- | --- | ---",
		"Publish | `pages publish` | **Share links**",
		"Identity | workspace | session",
		"", "After the table.",
	].join("\n")} />); });
	const texts = renderer.root.findAll((node) => node.props.selectable === true).map(textOf);
	expect(texts).toEqual(["Before the table.", "Job", "Superset", "AO", "Publish", "pages publish", "Share links", "Identity", "workspace", "session", "After the table."]);
	expect(texts.join("")).not.toContain("|");
	// Every row gives a column the same width (not flexBasis, which Yoga ignores
	// in a horizontal ScrollView), so the columns line up.
	const rows = renderer.root.findAll((node) => (node.type as string) === "View" && [node.props.style].flat().some((style) => style?.flexDirection === "row"));
	expect(rows).toHaveLength(3);
	const widths = rows.map((row) => row.children.map((cell) => typeof cell === "string" ? 0 : (cell.props.style as Array<{ width?: number } | false>).find((style) => style && style.width !== undefined)));
	expect(widths[0].every((style) => typeof style === "object" && style.width! > 0)).toBe(true);
	expect(widths[1]).toEqual(widths[0]);
	expect(widths[2]).toEqual(widths[0]);
	act(() => renderer.unmount());
});

it("keeps inline links operable in the continuously selectable response", () => {
	const onLinkOpen = vi.fn();
	let renderer!: ReactTestRenderer;
	act(() => { renderer = create(<ChatLinkProvider onLinkOpen={onLinkOpen}><ChatMarkdown continuousSelection text="Read [the docs](https://example.com/docs)" /></ChatLinkProvider>); });
	const link = renderer.root.find((node) => node.props.accessibilityRole === "link");
	act(() => link.props.onPress());
	expect(onLinkOpen).toHaveBeenCalledWith("https://example.com/docs");
	act(() => renderer.unmount());
});

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
		expect(node.props.selectionColor).toBe(theme.highlight);
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
		for (const node of selectable) expect(node.props.selectionColor).toBe(theme.selection);
		act(() => renderer.unmount());
	} finally {
		platform.OS = "ios";
	}
});

it.each([false, true])("colors Markdown links with the theme highlight, not the hue-less accent (pressable: %s)", (pressable) => {
	let renderer!: ReactTestRenderer;
	const markdown = <ChatMarkdown text="Read the [docs](https://example.com/docs) first." />;
	act(() => { renderer = create(pressable ? <ChatLinkProvider onLinkOpen={vi.fn()}>{markdown}</ChatLinkProvider> : markdown); });
	const links = renderer.root.findAll((node) => typeof node.type === "string" && (node.props.style as { textDecorationLine?: string } | undefined)?.textDecorationLine === "underline");
	expect(links.map(textOf)).toEqual(["docs"]);
	expect(links[0].props.style.color).toBe(theme.highlight);
	expect(links[0].props.style.color).not.toBe(theme.accent);
	act(() => renderer.unmount());
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


it("adds the exact native selection without changing the response or link handlers", () => {
	const onAddToChat = vi.fn();
	const source = { conversationId: "conversation", messageId: "response", revision: 2 };
	let renderer!: ReactTestRenderer;
	act(() => { renderer = create(<ChatLinkProvider onLinkOpen={vi.fn()} onAddToChat={onAddToChat}><ChatMarkdown continuousSelection selectionSource={source} text={"# Heading\n\n- One\n- Two"} /></ChatLinkProvider>); });
	const root = renderer.root.find((node) => node.props.selectable === true);
	expect(root.props.addToChatEnabled).toBe(true);
	const selection = "ding\n\n• One\n• Tw";
	act(() => root.props.onAddToChat({ nativeEvent: { target: 1, text: selection } }));
	expect(onAddToChat).toHaveBeenCalledExactlyOnceWith(selection, source);
	expect(textOf(root)).toBe("Heading\n\n• One\n• Two");
	act(() => renderer.unmount());
});
