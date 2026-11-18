import { createElement } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const { theme } = vi.hoisted(() => ({
	theme: { accent: "#f4f5f7", accentBorder: "rgba(244,245,247,0.28)", accentTint: "rgba(244,245,247,0.14)", highlight: "#79b0dc", selection: "rgba(121,176,220,0.28)" },
}));

vi.mock("react-native", () => ({
	NativeModules: {},
	findNodeHandle: (view: unknown) => view,
	Image: "Image",
	Platform: { OS: "ios" },
	Pressable: "Pressable",
	ScrollView: "ScrollView",
	StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
	Text: "Text",
	View: "View",
}));
vi.mock("@bsky.app/react-native-uitextview", () => ({ UITextView: "UITextView" }));
vi.mock("expo-clipboard", () => ({ setStringAsync: vi.fn() }));
vi.mock("../haptics", () => ({ haptics: { tap: vi.fn(), select: vi.fn(), success: vi.fn() } }));
vi.mock("../openGitHub", () => ({ openGitHub: vi.fn() }));
vi.mock("../ThemeProvider", () => ({
	useTheme: () => theme,
	useThemedStyles: (factory: (theme: Record<string, string>) => unknown) => factory(theme),
}));
// The diagram itself draws in a WebView; here only where a fence goes matters.
vi.mock("./MermaidBlock", () => ({
	MermaidBlock: (props: Record<string, unknown>) => createElement("MermaidBlock", props),
}));

import { ChatMarkdown } from "./ChatMarkdown";
import { isMermaidFence } from "./mermaidFence";

const DOCUMENT = ["Before the diagram.", "", "```mermaid", "graph TD; A-->B", "```", "", "After the diagram."].join("\n");

let renderer: ReactTestRenderer | undefined;
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
});

async function render(element: ReturnType<typeof createElement>): Promise<ReactTestRenderer> {
	await act(async () => { renderer = create(element); });
	// The renderer loads lazily: let it resolve.
	await act(async () => {});
	return renderer!;
}

function diagrams(root: ReactTestRenderer) {
	return root.root.findAll((node) => (node.type as unknown) === "MermaidBlock");
}

describe("Mermaid fences in markdown", () => {
	it("draws a fence as its diagram, with the code block as its Code view", async () => {
		const root = await render(createElement(ChatMarkdown, { text: DOCUMENT }));
		const [diagram] = diagrams(root);
		expect(diagram.props.code).toBe("graph TD; A-->B");
		expect(diagram.props.codeBlock).toBeTruthy();
	});

	it("breaks a reply's selectable text around the diagram, as a table does", async () => {
		const root = await render(createElement(ChatMarkdown, { text: DOCUMENT, continuousSelection: true }));
		expect(diagrams(root)).toHaveLength(1);
		const selectable = root.root.findAll((node) => node.props.selectable === true && node.props.uiTextView === true);
		expect(selectable).toHaveLength(2);
	});

	it("keeps a fence that is still streaming as text in the reply", async () => {
		const root = await render(createElement(ChatMarkdown, { text: DOCUMENT, continuousSelection: true, streaming: true }));
		expect(diagrams(root)).toHaveLength(0);
	});

	it("leaves a fence as code where diagrams are off (a gallery card)", async () => {
		const root = await render(createElement(ChatMarkdown, { text: DOCUMENT, diagrams: false }));
		expect(diagrams(root)).toHaveLength(0);
	});

	it("leaves other fences as code", async () => {
		const root = await render(createElement(ChatMarkdown, { text: "```ts\nconst a = 1;\n```" }));
		expect(diagrams(root)).toHaveLength(0);
	});
});

describe("isMermaidFence", () => {
	it("matches the language however it is written", () => {
		expect(isMermaidFence("mermaid")).toBe(true);
		expect(isMermaidFence(" Mermaid ")).toBe(true);
		expect(isMermaidFence("mmd")).toBe(false);
		expect(isMermaidFence(undefined)).toBe(false);
	});
});
