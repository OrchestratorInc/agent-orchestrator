import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, it, expect, vi } from "vitest";
const fixture = vi.hoisted(() => ({ focused: false, scroll: vi.fn() }));
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
vi.mock("expo-router", () => ({ usePathname: () => "/" }));
vi.mock("expo-router/react-navigation", () => ({ useIsFocused: () => fixture.focused, useScrollToTop: () => {} }));
vi.mock("./sidebar-navigation-context", () => ({ useSidebarNavigation: () => ({ scrollRequest: { destination: "agents", sequence: 1 } }) }));
import { useTabScrollToTop } from "./useTabScrollToTop";
function Probe() {
	const ref = useTabScrollToTop<{ scrollTo: typeof fixture.scroll }>();
	ref.current = { scrollTo: fixture.scroll };
	return null;
}
afterEach(() => { fixture.focused = false; fixture.scroll.mockClear(); });
it("does not scroll a retained inactive tab when another tab is reselected", () => {
	let renderer!: ReactTestRenderer;
	act(() => { renderer = create(<Probe />); });
	expect(fixture.scroll).not.toHaveBeenCalled();
	act(() => renderer.unmount());
});
it("still scrolls the active list when its own destination is reselected", () => {
	fixture.focused = true;
	let renderer!: ReactTestRenderer;
	act(() => { renderer = create(<Probe />); });
	expect(fixture.scroll).toHaveBeenCalledExactlyOnceWith({ y: 0, animated: true });
	act(() => renderer.unmount());
});
