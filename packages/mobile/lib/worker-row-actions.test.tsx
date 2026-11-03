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
		renderer = create(<WorkerRowActions title="Polish workers" pinned={pinned} onSetPinned={vi.fn()} onDelete={vi.fn()} />);
	});
	return renderer;
}

describe("WorkerRowActions", () => {
	// Archive stops the agent and deletes a clean worktree: the one destructive
	// control of the pair, so it carries the red on both platforms.
	it("marks Archive as the destructive action", () => {
		expect(glyph(mount(), "archive").props.tintColor).toBe("red");
	});

	it("names the pin state it will change to", () => {
		expect(control(mount(), "Pin Polish workers")).toBeTruthy();
		expect(control(mount(true), "Unpin Polish workers")).toBeTruthy();
	});
});
