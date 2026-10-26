import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vitest";
import type { TabPageBottomGap } from "./useTabPageBottomGap";

const platform = vi.hoisted(() => ({ OS: "android" }));
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
vi.mock("react-native", () => ({ Platform: platform, View: "View" }));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaFrame: () => ({ x: 0, y: 0, width: 412, height: 900 }) }));

import { View } from "react-native";
import { useTabPageBottomGap } from "./useTabPageBottomGap";

let renderer: ReactTestRenderer | undefined;
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
});

// Lays a page out ending `pageBottom` down a 900pt window and reads the gap below it.
function gapBelow(pageBottom: number): number {
	let result!: TabPageBottomGap;
	function Page() {
		result = useTabPageBottomGap();
		return <View ref={result.ref} onLayout={result.onLayout} />;
	}
	act(() => {
		renderer = create(<Page />, {
			createNodeMock: () => ({ measureInWindow: (done: (x: number, y: number, width: number, height: number) => void) => done(0, 100, 412, pageBottom - 100) }),
		});
	});
	act(() => result.onLayout());
	return result.gap;
}

// Android's page ends at the tab bar, and the window runs on below it.
it("measures the tab bar below an Android page", () => {
	platform.OS = "android";
	expect(gapBelow(764)).toBe(136);
});

// iOS's page runs to the bottom of the window under the bar.
it("leaves nothing below an iOS page", () => {
	platform.OS = "ios";
	expect(gapBelow(764)).toBe(0);
});
