import { readFileSync } from "node:fs";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		ActivityIndicator: host("ActivityIndicator"),
		Platform: { OS: "ios" },
		StyleSheet: { create: (styles: unknown) => styles },
		View: host("View"),
	};
});
vi.mock("../ThemeProvider", async () => {
	const { darkTheme } = await import("../theme");
	return { useTheme: () => darkTheme, useThemedStyles: (factory: (theme: unknown) => unknown) => factory(darkTheme) };
});

import { RenderSkeleton } from "./RenderSkeleton";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => vi.useFakeTimers());

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.useRealTimers();
});

function render(element: React.ReactElement) {
	act(() => {
		renderer = create(element);
	});
	return renderer!;
}

describe("RenderSkeleton", () => {
	it("holds still blocks with the system indicator in the page's place", () => {
		const root = render(<RenderSkeleton height={300} label="Swipe states" />).root;
		const skeleton = root.findAll((node) => node.type === ("View" as never) && node.props.accessibilityRole === "progressbar")[0];
		expect(skeleton.props).toMatchObject({ accessible: true, accessibilityLabel: "Loading Swipe states", accessibilityState: { busy: true } });
		expect(root.findAllByType("ActivityIndicator" as never)).toHaveLength(1);
		// The content block and two caption lines.
		expect(skeleton.findAll((node) => node.type === ("View" as never) && node !== skeleton && node.parent?.parent === skeleton)).toHaveLength(3);
	});

	it("draws only the content block in a short frame", () => {
		const root = render(<RenderSkeleton height={120} label="Chart" />).root;
		const skeleton = root.findAll((node) => node.type === ("View" as never) && node.props.accessibilityRole === "progressbar")[0];
		expect(skeleton.findAll((node) => node.type === ("View" as never) && node !== skeleton && node.parent?.parent === skeleton)).toHaveLength(1);
	});

	it("waits out its delay before appearing", () => {
		const tree = render(<RenderSkeleton height={300} label="Swipe states" delayMs={250} />);
		expect(tree.toJSON()).toBeNull();
		act(() => { vi.advanceTimersByTime(250); });
		expect(tree.root.findAllByType("ActivityIndicator" as never)).toHaveLength(1);
	});

	// The old shimmer ran an RN Animated loop under an SVG gradient.
	it("runs no animation of its own", () => {
		const text = readFileSync(new URL("./RenderSkeleton.tsx", import.meta.url), "utf8");
		expect(text).not.toMatch(/Animated|react-native-svg|LinearGradient|SHIMMER/);
	});
});
