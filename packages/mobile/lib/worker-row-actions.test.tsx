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
		expect(renderer.root.findByProps({ name: "archive" }).props.color).toBe("secondary");
	});
});
