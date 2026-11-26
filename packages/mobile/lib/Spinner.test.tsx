import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

vi.mock("react-native", () => ({ ActivityIndicator: "ActivityIndicator", Platform: { OS: "ios" }, View: "View" }));
vi.mock("./ThemeProvider", async () => {
	const { darkTheme } = await import("./theme");
	return { useTheme: () => darkTheme };
});

import { Spinner } from "./Spinner";

let renderer: ReactTestRenderer | undefined;

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
