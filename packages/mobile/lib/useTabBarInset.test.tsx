import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { expect, it, vi } from "vitest";

const platform = vi.hoisted(() => ({ OS: "ios" }));
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
vi.mock("react-native", () => ({ Platform: platform }));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 59, bottom: 83 }) }));

import { useTabBarInset } from "./useTabBarInset";

function read(): number {
	let inset = -1;
	function Probe() {
		inset = useTabBarInset();
		return null;
	}
	let renderer!: ReactTestRenderer;
	act(() => { renderer = create(<Probe />); });
	act(() => renderer.unmount());
	return inset;
}

// Inside a tab, iOS's bottom inset is the tab bar's: the page runs under it.
it("clears the tab bar the page runs under on iOS", () => {
	platform.OS = "ios";
	expect(read()).toBe(83);
});

// Android's page stops at the navigation bar, so the window's inset is not the tab's.
it("has nothing left to clear on Android", () => {
	platform.OS = "android";
	expect(read()).toBe(0);
});
