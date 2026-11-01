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

// A cold link: the board has never changed tab, so it has no state yet.
it("selects Projects under a page opened over a board that has not picked a tab", () => {
	fixtures.routes = [{ key: "board", name: "(tabs)" }, page];
	act(() => { renderer = create(<Page />); });
	expect(fixtures.dispatch).toHaveBeenCalledExactlyOnceWith({ type: "SET_PARAMS", payload: { params: { screen: "(projects)" } }, source: "board" });
});

it("selects Projects under a page opened over the Workers tab", () => {
	fixtures.routes = [{ key: "board", name: "(tabs)", state: { index: 0, routes: [{ name: "(workers)" }, { name: "(projects)" }] } }, page];
	act(() => { renderer = create(<Page />); });
	expect(fixtures.dispatch).toHaveBeenCalledTimes(1);
});

it("leaves a board already on Projects alone", () => {
	fixtures.routes = [{ key: "board", name: "(tabs)", state: { index: 1, routes: [{ name: "(workers)" }, { name: "(projects)" }] } }, page];
	act(() => { renderer = create(<Page />); });
	expect(fixtures.dispatch).not.toHaveBeenCalled();
});

it("does nothing without a board beneath", () => {
	fixtures.routes = [page];
	act(() => { renderer = create(<Page />); });
	expect(fixtures.dispatch).not.toHaveBeenCalled();
});
