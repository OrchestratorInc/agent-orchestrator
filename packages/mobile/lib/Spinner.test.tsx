import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const fixtures = vi.hoisted(() => ({ platform: { OS: "ios" } }));
vi.mock("react-native", () => ({ ActivityIndicator: "ActivityIndicator", Platform: fixtures.platform, View: "View" }));
vi.mock("./ThemeProvider", async () => {
	const { darkTheme } = await import("./theme");
	return { useTheme: () => darkTheme };
});

import { Spinner } from "./Spinner";
import { darkTheme } from "./theme";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	fixtures.platform.OS = "ios";
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
});

function render(element: React.ReactElement): ReactTestRenderer {
	act(() => {
		renderer = create(element);
	});
	return renderer!;
}

describe("Spinner", () => {
	it("is the system indicator in the theme's secondary colour", () => {
		const indicator = render(<Spinner />).root.findByType("ActivityIndicator" as never);
		expect(indicator.props.size).toBe("small");
		expect(indicator.props.color).toBe(darkTheme.textSecondary);
	});

	it("passes the system's large size through", () => {
		expect(render(<Spinner size="large" color={darkTheme.accent} />).root.findByType("ActivityIndicator" as never).props).toMatchObject({ size: "large", color: darkTheme.accent });
	});

	it("scales iOS's fixed small indicator into a glyph-sized slot", () => {
		const root = render(<Spinner size={12} />).root;
		const slot = root.findByType("View" as never);
		expect(slot.props.style[0]).toMatchObject({ width: 12, height: 12 });
		const indicator = root.findByType("ActivityIndicator" as never);
		expect(indicator.props.size).toBe("small");
		expect(indicator.props.style).toEqual({ transform: [{ scale: 12 / 20 }] });
	});

	it("sizes Android's native indicator directly", () => {
		fixtures.platform.OS = "android";
		const root = render(<Spinner size={12} />).root;
		expect(root.findAllByType("View" as never)).toHaveLength(0);
		expect(root.findByType("ActivityIndicator" as never).props.size).toBe(12);
	});

	it("stays out of the accessibility tree unless it stands alone", () => {
		const hidden = render(<Spinner />).root.findByType("ActivityIndicator" as never);
		expect(hidden.props).toMatchObject({ accessible: false, accessibilityElementsHidden: true, importantForAccessibility: "no-hide-descendants" });
		act(() => renderer!.update(<Spinner accessibilityLabel="Loading conversation" />));
		expect(renderer!.root.findByType("ActivityIndicator" as never).props).toMatchObject({
			accessible: true,
			accessibilityRole: "progressbar",
			accessibilityLabel: "Loading conversation",
			accessibilityState: { busy: true },
		});
	});
});
