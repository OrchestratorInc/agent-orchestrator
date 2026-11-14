import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

type Route = { key: string; name: string; params?: { screen?: string }; state?: { index: number; routes: { name: string }[] } };

const fixtures = vi.hoisted(() => {
	const state = { routes: [] as Route[], dispatch: vi.fn() };
	return Object.assign(state, { navigation: { getState: () => ({ routes: state.routes }), dispatch: state.dispatch } });
});
vi.mock("expo-router", () => ({
	useNavigation: () => fixtures.navigation,
}));
vi.mock("expo-router/react-navigation", () => ({
	CommonActions: { setParams: (params: object) => ({ type: "SET_PARAMS", payload: { params } }) },
	getFocusedRouteNameFromRoute: (route: Route) => route.state?.routes[route.state.index]?.name ?? route.params?.screen,
}));

import { useBoardTabBeneath } from "./useBoardTabBeneath";

function Page() {
	useBoardTabBeneath("(projects)");
	return null;
}

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.dispatch.mockClear();
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.unstubAllGlobals();
});

const page = { key: "project-1", name: "project/[id]" };

const board = (index?: number): Route => ({ key: "board", name: "(tabs)", ...(index === undefined ? {} : { state: { index, routes: [{ name: "(workers)" }, { name: "(projects)" }] } }) });

// A cold link: the board has never changed tab, so it has no state yet.
it.each([
	["a board that has not picked a tab", [board(), page], 1],
	["the Workers tab", [board(0), page], 1],
	["a board already on Projects", [board(1), page], 0],
	["no board at all", [page], 0],
])("selects Projects beneath a page opened over %s only when needed", (_name, routes, dispatches) => {
	fixtures.routes = routes as Route[];
	act(() => { renderer = create(<Page />); });
	expect(fixtures.dispatch).toHaveBeenCalledTimes(dispatches as number);
	if (dispatches) expect(fixtures.dispatch).toHaveBeenCalledWith({ type: "SET_PARAMS", payload: { params: { screen: "(projects)" } }, source: "board" });
});
