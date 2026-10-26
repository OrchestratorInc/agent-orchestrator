import { readFileSync } from "node:fs";
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
const restingStyle = (renderer: ReactTestRenderer, label: string) =>
	Object.assign({}, ...control(renderer, label).props.style({ pressed: false }).filter(Boolean));
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
		const renderer = mount();
		expect(restingStyle(renderer, "Archive Polish workers")).toMatchObject({ backgroundColor: "red-tint" });
		expect(control(renderer, "Archive Polish workers").props.android_ripple).toMatchObject({ color: "red-tint" });
		expect(glyph(renderer, "archive").props.tintColor).toBe("red");
	});

	// Pinning asks for nothing, so it stays in foreground ink rather than a hue.
	it("keeps Pin neutral, and names the state it will change to", () => {
		const renderer = mount();
		expect(restingStyle(renderer, "Pin Polish workers")).toMatchObject({ backgroundColor: "accent-tint" });
		expect(glyph(renderer, "keep").props.tintColor).toBe("primary");
		expect(control(mount(true), "Unpin Polish workers")).toBeTruthy();
	});

	it("gives each control the platform's touch target", () => {
		const renderer = mount();
		for (const label of ["Pin Polish workers", "Archive Polish workers"]) {
			expect(restingStyle(renderer, label)).toMatchObject({ width: 44, height: 44 });
		}
	});

	// Both rails draw the pin the way the system symbol stands, never tilted.
	it("keeps swipe-action pins upright", () => {
		const source = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");
		expect(source("./worker-row-actions.tsx")).not.toContain("rotate");
		expect(source("./worker-row-actions.ios.tsx")).not.toContain("rotationEffect");
	});
});
