import { readFileSync } from "node:fs";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const router = vi.hoisted(() => ({ dismissTo: vi.fn(), back: vi.fn(), replace: vi.fn(), canGoBack: vi.fn(() => false) }));
const reportErrorShown = vi.hoisted(() => vi.fn());

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { StyleSheet: { create: (styles: unknown) => styles }, View: host("View") };
});
vi.mock("expo-router", () => ({ useRouter: () => router }));
vi.mock("./telemetry/uiUsageRegistry", () => ({ reportErrorShown }));
vi.mock("./ui", async () => {
	const React = await import("react");
	return {
		EmptyState: ({ action, ...props }: { action?: React.ReactNode }) => React.createElement("EmptyState", props, action),
		Button: (props: object) => React.createElement("Button", props),
	};
});

import { NotFoundScreen } from "./NotFoundScreen";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.clearAllMocks();
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
});

function render(): ReactTestRenderer {
	act(() => {
		renderer = create(<NotFoundScreen />);
	});
	return renderer!;
}

describe("NotFoundScreen", () => {
	it("says the page doesn't exist, with one way out: the board", () => {
		const tree = render();
		expect(tree.root.findByType("EmptyState" as never).props.title).toBe("This page doesn't exist");
		const buttons = tree.root.findAllByType("Button" as never);
		expect(buttons.map((button) => button.props.title)).toEqual(["Open board"]);
	});

	it("goes back down to the board rather than stacking another on top", () => {
		const tree = render();
		act(() => tree.root.findByType("Button" as never).props.onPress());
		expect(router.dismissTo).toHaveBeenCalledWith("/");
		expect(router.replace).not.toHaveBeenCalled();
	});

	it("reports the unmatched route by kind once, without the path", () => {
		const tree = render();
		act(() => tree.update(<NotFoundScreen />));
		expect(reportErrorShown).toHaveBeenCalledTimes(1);
		expect(reportErrorShown).toHaveBeenCalledWith("screen", "route_not_found");
	});
});

describe("not-found routes", () => {
	// Route files are React Native modules, so their source is read, not imported.
	const route = (file: string) => readFileSync(new URL(`../app/${file}`, import.meta.url), "utf8");

	// `_sitemap` replaces expo-router's own sitemap, which crashes on native.
	it.each(["+not-found.tsx", "_sitemap.tsx"])("%s draws the in-app not-found screen", (file) => {
		expect(route(file)).toMatch(/^export \{ NotFoundScreen as default \} from "\.\.\/lib\/NotFoundScreen";$/m);
	});

	it("registers both on the root stack with a title", () => {
		const layout = route("_layout.tsx");
		expect(layout).toContain('<Stack.Screen name="+not-found" options={{ title: "Not found" }} />');
		expect(layout).toContain('<Stack.Screen name="_sitemap" options={{ title: "Not found" }} />');
	});
});
