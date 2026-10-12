import { createElement, type ReactNode } from "react";
import { createRequire } from "node:module";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
vi.mock("expo-router", () => ({ Tabs: ({ children, ...props }: { children?: ReactNode }) => createElement("Tabs", props, children) }));
vi.mock("expo-router/stack", () => ({ Stack: (props: object) => createElement("Stack", props) }));
vi.mock("./sidebar-navigation-shell", () => ({ SidebarNavigationShell: ({ children }: { children: ReactNode }) => createElement("Sidebar", {}, children) }));
import SidebarLayout from "../app/(tabs)/_layout";
// Exercise the installed Expo router, not a fake navigation state machine.
const { TabRouter } = createRequire(import.meta.url)("expo-router/build/react-navigation/routers/TabRouter.js");

describe("sidebar list lifecycle", () => {
	it("keeps route and navigator keys when moving among the three existing pages", () => {
		const router = TabRouter({ initialRouteName: "index", backBehavior: "none" });
		const options = { routeNames: ["index", "projects", "prs"], routeParamList: {}, routeGetIdList: {} };
		let state = router.getInitialState(options);
		const key = state.key;
		const routes = state.routes.map((route: { key: string }) => route.key);
		for (const name of ["projects", "index", "prs", "index", "prs"]) {
			state = router.getStateForAction(state, { type: "NAVIGATE", payload: { name } }, options);
			expect(state.key).toBe(key);
			expect(state.routes.map((route: { key: string }) => route.key)).toEqual(routes);
			expect(state.routes[state.index].name).toBe(name);
		}
		expect(router.getStateForAction(state, { type: "GO_BACK" }, options)).toBeNull();
	});
	it("retains visited pages without mounting all pages upfront or adding a visible tab bar", () => {
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(<SidebarLayout />); });
		const tabs = renderer.root.findByType("Tabs" as never);
		expect(tabs.props.screenOptions).toMatchObject({ headerShown: false, animation: "none", lazy: true, freezeOnBlur: true });
		expect(tabs.props.detachInactiveScreens).toBe(true);
		expect(tabs.props.backBehavior).toBe("none");
		expect(tabs.props.tabBar()).toBeNull();
		act(() => renderer.unmount());
	});
});
