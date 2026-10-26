import { readFileSync } from "node:fs";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

vi.mock("react-native", () => ({
	Pressable: "Pressable",
	StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
	View: "View",
}));
vi.mock("./icons", () => ({ Feather: "Feather" }));
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

describe("WorkerRowActions", () => {
	it("presents session removal as a neutral archive action", () => {
		let renderer!: ReactTestRenderer;
		act(() => {
			renderer = create(
				<WorkerRowActions title="Polish workers" pinned={false} onSetPinned={vi.fn()} onDelete={vi.fn()} />,
			);
		});

		const archive = renderer.root.findByProps({ accessibilityLabel: "Archive Polish workers" });
		expect(archive).toBeTruthy();
		expect(glyph(renderer, "archive").props.tintColor).toBe("primary");
	});

	it("draws archive with the same control and ink as pin", () => {
		let renderer!: ReactTestRenderer;
		act(() => {
			renderer = create(
				<WorkerRowActions title="Polish workers" pinned={false} onSetPinned={vi.fn()} onDelete={vi.fn()} />,
			);
		});

		const style = (label: string) =>
			renderer.root.find((node) => (node.type as unknown) === "Pressable" && node.props.accessibilityLabel === label).props.style({ pressed: false });
		expect(style("Archive Polish workers")).toEqual(style("Pin Polish workers"));
		const pinIcon = glyph(renderer, "keep").props;
		const archiveIcon = glyph(renderer, "archive").props;
		expect({ size: archiveIcon.size, tintColor: archiveIcon.tintColor }).toEqual({ size: pinIcon.size, tintColor: pinIcon.tintColor });
	});

	// Both rails draw the pin the way the system symbol stands, never tilted.
	it("keeps swipe-action pins upright", () => {
		const source = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");
		expect(source("./worker-row-actions.tsx")).not.toContain("rotate");
		expect(source("./worker-row-actions.ios.tsx")).not.toContain("rotationEffect");
	});
});
