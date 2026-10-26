import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@expo/ui/swift-ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { Circle: host("Circle"), HStack: host("HStack") };
});
vi.mock("@expo/ui/swift-ui/modifiers", () => ({
	accessibilityHidden: (hidden: boolean) => ({ accessibilityHidden: hidden }),
	foregroundStyle: (color: string) => ({ foregroundStyle: color }),
	frame: (size: object) => ({ frame: size }),
}));
vi.mock("@expo/ui/jetpack-compose", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { Box: host("Box"), Row: host("Row") };
});
vi.mock("@expo/ui/jetpack-compose/modifiers", () => ({
	background: (color: string) => ({ background: color }),
	clip: (shape: string) => ({ clip: shape }),
	size: (width: number, height: number) => ({ size: [width, height] }),
	Shapes: { Circle: "circle" },
}));

import { ThemePaletteSwatch } from "./ThemePaletteSwatch";
import { ThemePaletteSwatch as AndroidThemePaletteSwatch } from "./ThemePaletteSwatch.android";

const theme = { highlight: "#highlight", orange: "#orange", green: "#green", purple: "#purple" } as never;

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

describe("ThemePaletteSwatch", () => {
	it("draws the theme's highlight, working, ready and merged hues as SwiftUI dots VoiceOver skips", () => {
		act(() => { renderer = create(<ThemePaletteSwatch theme={theme} />); });
		const row = renderer!.root.findByType("HStack" as never);
		expect(row.props.modifiers).toEqual([{ accessibilityHidden: true }]);
		expect(row.findAllByType("Circle" as never).map((dot) => dot.props.modifiers)).toEqual(
			["#highlight", "#orange", "#green", "#purple"].map((color) => [{ foregroundStyle: color }, { frame: { width: 8, height: 8 } }]),
		);
	});

	it("draws the same dots in Compose on Android", () => {
		act(() => { renderer = create(<AndroidThemePaletteSwatch theme={theme} />); });
		const row = renderer!.root.findByType("Row" as never);
		expect(row.findAllByType("Box" as never).map((dot) => dot.props.modifiers)).toEqual(
			["#highlight", "#orange", "#green", "#purple"].map((color) => [{ size: [8, 8] }, { clip: "circle" }, { background: color }]),
		);
	});
});
