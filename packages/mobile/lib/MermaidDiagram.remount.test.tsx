import { createElement } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

vi.mock("react-native", () => ({
	ActivityIndicator: "ActivityIndicator",
	Linking: { openURL: vi.fn() },
	StyleSheet: { create: (styles: unknown) => styles },
	View: "View",
}));
vi.mock("react-native-webview", () => ({ WebView: "WebView" }));
vi.mock("./mermaidRuntime", () => ({ loadMermaidRuntime: vi.fn() }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => ({}), useThemedStyles: () => ({}), useThemeState: () => ({ scheme: "dark" }) }));

import { useDiagramRemount } from "./MermaidDiagram";

describe("useDiagramRemount", () => {
	it("mounts the page afresh when its process dies, then gives up past the limit", () => {
		let page!: ReturnType<typeof useDiagramRemount>;
		function Probe() {
			page = useDiagramRemount();
			return null;
		}
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(createElement(Probe)); });
		const giveUp = vi.fn();
		expect(page.mount).toBe(0);
		act(() => page.gone(giveUp));
		act(() => page.gone(giveUp));
		expect(page.mount).toBe(2);
		expect(giveUp).not.toHaveBeenCalled();
		act(() => page.gone(giveUp));
		expect(page.mount).toBe(2);
		expect(giveUp).toHaveBeenCalledTimes(1);
		act(() => renderer.unmount());
	});

	it("starts the count over once a fresh mount draws", () => {
		let page!: ReturnType<typeof useDiagramRemount>;
		function Probe() {
			page = useDiagramRemount();
			return null;
		}
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(createElement(Probe)); });
		const giveUp = vi.fn();
		// Three background visits, each ending the process, each redrawn.
		for (let visit = 0; visit < 3; visit += 1) {
			act(() => page.gone(giveUp));
			act(() => page.rendered());
		}
		expect(giveUp).not.toHaveBeenCalled();
		expect(page.mount).toBe(3);
		act(() => renderer.unmount());
	});
});
