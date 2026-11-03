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
vi.mock("./stackScreenOptions", () => ({ stackScreenOptions: () => ({}), largeTitleScreenOptions: () => ({}) }));

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

// Every tab stays mounted; the ones not showing stop re-rendering on each poll.
// A page pushed over the board leaves the selected tab live, so its board is
// current as a back swipe reveals it; focus turns over a commit before selection.
it.each([
	["follows the store while its tab is on screen", true, "workers", false],
	["freezes a tab that is not selected", false, "projects", true],
	["keeps the selected tab live under a page pushed over the board", false, "workers", false],
	["thaws a tab as soon as it is focused", true, "projects", false],
])("%s", (_name, focused, selectedKey, expected) => {
	fixtures.focused = focused;
	fixtures.selectedKey = selectedKey;
	expect(frozen()).toBe(expected);
});
