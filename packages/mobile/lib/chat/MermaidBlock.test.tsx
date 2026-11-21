import { createElement } from "react";
import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const { loadMermaidRuntime } = vi.hoisted(() => ({ loadMermaidRuntime: vi.fn(async () => "/*ENGINE*/ var mermaid = {};") }));

vi.mock("react-native", () => ({
	ActivityIndicator: "ActivityIndicator",
	Platform: { OS: "ios", select: (options: Record<string, unknown>) => options.ios ?? options.default },
	Pressable: "Pressable",
	StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
	Text: "Text",
	View: "View",
}));
vi.mock("react-native-webview", () => ({ WebView: "WebView" }));
vi.mock("../haptics", () => ({ haptics: { select: vi.fn() } }));
vi.mock("../MermaidDiagram", () => ({ stayInDiagramPage: () => true, useDiagramRemount: () => ({ mount: 0, gone: vi.fn(), rendered: vi.fn() }) }));
vi.mock("../mermaidRuntime", () => ({ loadMermaidRuntime }));
vi.mock("../ThemeProvider", () => ({
	useTheme: () => new Proxy({}, { get: (_, key) => (key === "fontMono" ? "mono" : "#111111") }),
	useThemeState: () => ({ scheme: "dark" }),
	useThemedStyles: (factory: (theme: unknown) => unknown) => factory(new Proxy({}, { get: () => "#111111" })),
}));

import { clearDiagramCache } from "../mermaidSvgCache";
import { MermaidBlock } from "./MermaidBlock";

const CODE = "graph TD; A-->B";
let renderer: ReactTestRenderer | undefined;

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	clearDiagramCache();
	loadMermaidRuntime.mockClear();
});

async function mount(): Promise<ReactTestRenderer> {
	await act(async () => { renderer = create(createElement(MermaidBlock, { code: CODE, codeBlock: createElement("CodeBlock") })); });
	await act(async () => {});
	return renderer!;
}

function frame(root: ReactTestRenderer): ReactTestInstance {
	return root.root.find((node) => (node.type as unknown) === "WebView");
}

function frameHeight(root: ReactTestRenderer): number | undefined {
	return (frame(root).parent?.props.style as { height?: number } | undefined)?.height;
}

function post(root: ReactTestRenderer, message: object) {
	act(() => frame(root).props.onMessage({ nativeEvent: { data: JSON.stringify(message) } }));
}

describe("MermaidBlock", () => {
	it("lets the engine page go once the diagram is drawn, before the frame shows", async () => {
		const root = await mount();
		expect(frame(root).props.source.html).toContain("/*ENGINE*/");
		post(root, { type: "rendered", height: 80, svg: "<svg id=\"drawn\"></svg>" });
		// The frame now holds only the drawn SVG, still hidden at its placeholder height.
		expect(frame(root).props.source.html).not.toContain("/*ENGINE*/");
		expect(frame(root).props.source.html).toContain("<svg id=\"drawn\"></svg>");
		expect(frameHeight(root)).toBe(160);
		post(root, { type: "rendered", height: 92 });
		expect(frameHeight(root)).toBe(92);
	});

	it("draws a diagram seen before from its SVG, without loading the engine", async () => {
		const first = await mount();
		post(first, { type: "rendered", height: 80, svg: "<svg id=\"drawn\"></svg>" });
		act(() => first.unmount());
		renderer = undefined;
		loadMermaidRuntime.mockClear();
		const again = await mount();
		expect(loadMermaidRuntime).not.toHaveBeenCalled();
		expect(frame(again).props.source.html).toContain("<svg id=\"drawn\"></svg>");
		expect(frame(again).props.source.html).not.toContain("/*ENGINE*/");
	});

	it("shows the code block when the engine page cannot draw it", async () => {
		const root = await mount();
		post(root, { type: "failed", message: "Parse error" });
		expect(root.root.findAll((node) => (node.type as unknown) === "WebView")).toHaveLength(0);
		expect(root.root.findAll((node) => (node.type as unknown) === "CodeBlock")).toHaveLength(1);
	});
});
