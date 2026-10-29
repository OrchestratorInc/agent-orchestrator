import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	focused: true,
	selectedKey: "workers",
}));
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

vi.mock("expo-router", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Stack: Object.assign(host("Stack"), { Screen: host("StackScreen") }),
		useIsFocused: () => fixtures.focused,
		useRoute: () => ({ key: "workers", name: "(workers)" }),
	};
});
vi.mock("expo-router/react-navigation", () => ({
	useNavigationState: (select: (state: { index: number; routes: { key: string }[] }) => unknown) => {
		const routes = [{ key: "workers" }, { key: "projects" }];
		return select({ index: routes.findIndex((route) => route.key === fixtures.selectedKey), routes });
	},
}));
vi.mock("./store", async () => {
	const React = await import("react");
	return { FrozenAppScope: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("FrozenAppScope", props, children) };
});
vi.mock("./ThemeProvider", () => ({ useTheme: () => ({}) }));
vi.mock("./stackScreenOptions", () => ({ stackScreenOptions: () => ({}), untitledTabScreenOptions: () => ({}) }));

import { TabStack } from "./TabStack";

let renderer: ReactTestRenderer | undefined;
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	fixtures.focused = true;
	fixtures.selectedKey = "workers";
});

function frozen(): boolean {
	act(() => { renderer = create(<TabStack screen="index" title="Workers" />); });
	return renderer!.root.findByType("FrozenAppScope" as never).props.frozen;
}

it("follows the store while its tab is on screen", () => {
	expect(frozen()).toBe(false);
});

// Every tab stays mounted; the ones not showing stop re-rendering on each poll.
it("freezes a tab that is not selected", () => {
	fixtures.focused = false;
	fixtures.selectedKey = "projects";
	expect(frozen()).toBe(true);
});

// A session pushed over the board leaves the selected tab live, so its board is
// current as a back swipe reveals it.
it("keeps the selected tab live under a page pushed over the board", () => {
	fixtures.focused = false;
	expect(frozen()).toBe(false);
});

// Focus turns over with the tab itself, a commit before the selection does.
it("thaws a tab as soon as it is focused", () => {
	fixtures.selectedKey = "projects";
	expect(frozen()).toBe(false);
});

it("wraps the tab's whole stack", () => {
	act(() => { renderer = create(<TabStack screen="index" title="Workers" />); });
	const scope = renderer!.root.findByType("FrozenAppScope" as never);
	expect(scope.findByType("Stack" as never).findByType("StackScreen" as never).props).toMatchObject({ name: "index", options: { title: "Workers" } });
});
