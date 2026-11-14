import { createElement } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const mocks = vi.hoisted(() => ({
	items: new Map<string, string>(),
	nav: { pathname: "/", params: {} as Record<string, string>, segments: [] as string[] },
	push: vi.fn(),
	initialURL: null as string | null,
	launch: { resolve: (_: unknown) => {} },
}));

vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => mocks.items.get(key) ?? null,
	setItem: async (key: string, value: string) => { mocks.items.set(key, value); },
} }));
vi.mock("expo-linking", () => ({ getInitialURL: async () => mocks.initialURL }));
vi.mock("expo-router", () => ({
	usePathname: () => mocks.nav.pathname,
	useGlobalSearchParams: () => mocks.nav.params,
	useSegments: () => mocks.nav.segments,
	useRootNavigationState: () => ({ key: "root" }),
	useRouter: () => ({ push: mocks.push }),
}));
vi.mock("react-native", () => ({ AppState: { addEventListener: () => ({ remove: () => {} }) } }));
vi.mock("./launchNotification", () => ({
	launchNotificationResponse: () => new Promise((resolve) => { mocks.launch.resolve = resolve; }),
}));

import { LAST_ROUTE_KEY } from "./lastRoute";
import { RouteRestorer } from "./RouteRestorer";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	mocks.items.clear();
	mocks.items.set(LAST_ROUTE_KEY, JSON.stringify({ href: "/session/web-1?hostId=a", leftAt: Date.now() - 60_000 }));
	mocks.nav = { pathname: "/", params: {}, segments: [] };
	mocks.initialURL = null;
	mocks.push.mockClear();
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
});

async function launch(response: unknown, beforeAnswer?: () => void) {
	await act(async () => { renderer = create(createElement(RouteRestorer)); });
	if (beforeAnswer) {
		beforeAnswer();
		await act(async () => { renderer!.update(createElement(RouteRestorer)); });
	}
	await act(async () => { mocks.launch.resolve(response); });
	await act(async () => {});
}

describe("RouteRestorer", () => {
	it("reopens the screen the phone closed the app on", async () => {
		await launch(null);
		expect(mocks.push).toHaveBeenCalledWith("/session/web-1?hostId=a");
	});

	// PushManager routes the tap that launched the app; the saved screen must
	// not be pushed as well.
	it("does not reopen over the notification tap that launched the app", async () => {
		await launch({ notification: { request: { identifier: "n1" }, date: 0 }, actionIdentifier: "default" });
		expect(mocks.push).not.toHaveBeenCalled();
	});

	it("does not replace a screen opened while the launch was still reading", async () => {
		await launch(null, () => {
			mocks.nav = { pathname: "/files/web-2/file", params: { sessionId: "web-2", path: "a.ts" }, segments: ["files", "[sessionId]", "file"] };
		});
		expect(mocks.push).not.toHaveBeenCalled();
		// That screen is what is remembered now.
		expect(JSON.parse(mocks.items.get(LAST_ROUTE_KEY)!)).toEqual({ href: "/files/web-2/file?path=a.ts", leftAt: null });
	});

	it("does not reopen when a link launched the app", async () => {
		mocks.initialURL = "ao-app://session/web-9";
		await launch(null);
		expect(mocks.push).not.toHaveBeenCalled();
	});
});
