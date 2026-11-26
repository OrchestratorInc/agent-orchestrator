import { createElement } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const mocks = vi.hoisted(() => ({
	items: new Map<string, string>(),
	nav: { pathname: "/", params: {} as Record<string, string>, segments: [] as string[] },
	push: vi.fn(),
	openedBy: vi.fn(),
	initialURL: null as string | null,
	launch: { resolve: (_: unknown) => {} },
	failRead: false,
	appState: { listener: null as null | ((state: string) => void) },
}));

vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => {
		if (mocks.failRead) throw new Error("storage unavailable");
		return mocks.items.get(key) ?? null;
	},
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
vi.mock("react-native", () => ({ AppState: { addEventListener: (_: string, listener: (state: string) => void) => {
	mocks.appState.listener = listener;
	return { remove: () => { mocks.appState.listener = null; } };
} } }));
vi.mock("./telemetry/uiUsageRegistry", () => ({ reportOpenedBy: mocks.openedBy }));
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
	mocks.failRead = false;
	mocks.push.mockClear();
	mocks.openedBy.mockClear();
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
		// The reopened session counts as restored, not opened from the board.
		expect(mocks.openedBy).toHaveBeenCalledWith("restore");
	});

	it("marks no restore for a screen that is not a session", async () => {
		mocks.items.set(LAST_ROUTE_KEY, JSON.stringify({ href: "/files/web-1", leftAt: Date.now() - 60_000 }));
		await launch(null);
		expect(mocks.push).toHaveBeenCalledWith("/files/web-1");
		expect(mocks.openedBy).not.toHaveBeenCalled();
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
		expect(mocks.openedBy).not.toHaveBeenCalled();
	});

	// Left during the launch's reads and then closed by the phone: the screen
	// must still say when it was left, or the next launch will not reopen it.
	it("keeps the time the app was left while the launch was still reading", async () => {
		await launch(null, () => { mocks.appState.listener!("background"); });
		expect(mocks.push).toHaveBeenCalledWith("/session/web-1?hostId=a");
		const saved = JSON.parse(mocks.items.get(LAST_ROUTE_KEY)!) as { href: string; leftAt: number | null };
		expect(saved.href).toBe("/session/web-1?hostId=a");
		expect(typeof saved.leftAt).toBe("number");
	});

	// The reopened screen opens after the launch decided: that route change
	// must not wipe the departure time, or a phone that closes the app before
	// it comes back to the front leaves nothing to reopen.
	it("keeps the departure time once the reopened screen has opened", async () => {
		mocks.push.mockImplementationOnce(() => {
			mocks.nav = { pathname: "/session/web-1", params: { id: "web-1", hostId: "a" }, segments: ["session", "[id]"] };
		});
		await launch(null, () => { mocks.appState.listener!("background"); });
		await act(async () => { renderer!.update(createElement(RouteRestorer)); });
		const saved = JSON.parse(mocks.items.get(LAST_ROUTE_KEY)!) as { href: string; leftAt: number | null };
		expect(saved.href).toBe("/session/web-1?hostId=a");
		expect(typeof saved.leftAt).toBe("number");
		// Back in front, it is cleared, and a screen opened then is not marked as left.
		mocks.appState.listener!("active");
		mocks.nav = { pathname: "/session/web-4", params: { id: "web-4" }, segments: ["session", "[id]"] };
		await act(async () => { renderer!.update(createElement(RouteRestorer)); });
		expect(JSON.parse(mocks.items.get(LAST_ROUTE_KEY)!)).toEqual({ href: "/session/web-4", leftAt: null });
	});

	it("forgets that departure when the app came back before the reads landed", async () => {
		await launch(null, () => {
			mocks.appState.listener!("background");
			mocks.appState.listener!("active");
		});
		expect(JSON.parse(mocks.items.get(LAST_ROUTE_KEY)!).leftAt).toBeNull();
	});

	// A failed read must not leave the screen unremembered for the whole launch.
	it("goes on from the screen that is open when the saved one cannot be read", async () => {
		mocks.failRead = true;
		mocks.nav = { pathname: "/session/web-3", params: {}, segments: ["session", "[id]"] };
		await launch(null);
		expect(mocks.push).not.toHaveBeenCalled();
		expect(JSON.parse(mocks.items.get(LAST_ROUTE_KEY)!)).toEqual({ href: "/session/web-3", leftAt: null });
		mocks.appState.listener!("background");
		await act(async () => {});
		expect(JSON.parse(mocks.items.get(LAST_ROUTE_KEY)!).href).toBe("/session/web-3");
		expect(typeof JSON.parse(mocks.items.get(LAST_ROUTE_KEY)!).leftAt).toBe("number");
	});
});
