import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

vi.mock("react-native", () => ({
	Platform: { OS: "android" },
	Pressable: "Pressable",
	StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
	View: "View",
}));
vi.mock("./ThemeProvider", () => {
	const theme = {
		accent: "accent",
		accentTint: "accent-tint",
		borderSubtle: "border",
		bgElevated: "elevated",
		red: "red",
		textPrimary: "primary",
		textSecondary: "secondary",
		tintRed: "red-tint",
	};
	return { useTheme: () => theme, useThemedStyles: (factory: (value: typeof theme) => unknown) => factory(theme) };
});

import { WorkerRowActions } from "./worker-row-actions";

// The rail draws system symbols; find one by its Material name.
const glyph = (renderer: ReactTestRenderer, android: string) =>
	renderer.root.find((node) => (node.type as unknown) === "SymbolView" && node.props.name?.android === android);
const control = (renderer: ReactTestRenderer, label: string) =>
	renderer.root.find((node) => (node.type as unknown) === "Pressable" && node.props.accessibilityLabel === label);
function mount(pinned = false) {
	let renderer!: ReactTestRenderer;
	act(() => {
		renderer = create(<WorkerRowActions title="Polish workers" pinned={pinned} onSetPinned={vi.fn()} />);
	});
	return renderer;
}

describe("WorkerRowActions", () => {
	// Archiving is the full swipe past the pin (and the long-press menu), not a
	// second button in the rail.
	it("holds only the pin, with no Archive button", () => {
		const renderer = mount();
		const controls = renderer.root.findAll((node) => (node.type as unknown) === "Pressable");
		expect(controls.map((node) => node.props.accessibilityLabel)).toEqual(["Pin Polish workers"]);
	});

	it("names the pin state it will change to", () => {
		expect(control(mount(), "Pin Polish workers")).toBeTruthy();
		expect(control(mount(true), "Unpin Polish workers")).toBeTruthy();
	});
});
