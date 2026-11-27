import { createElement } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const env = vi.hoisted(() => ({
	listeners: new Set<(state: string) => void>(),
	pathname: "/",
	updates: {
		isUpdatePending: false,
		downloadedUpdate: undefined as { updateId: string } | undefined,
		lastCheckForUpdateTimeSinceRestart: undefined as Date | undefined,
	},
	check: vi.fn(async () => ({ kind: "up-to-date" as const })),
	show: vi.fn(),
}));

vi.mock("react-native", () => ({
	AppState: {
		addEventListener: (_: string, listener: (state: string) => void) => {
			env.listeners.add(listener);
			return { remove: () => env.listeners.delete(listener) };
		},
	},
}));

vi.mock("expo-router", () => ({ usePathname: () => env.pathname }));

vi.mock("expo-updates", () => ({
	isEnabled: true,
	useUpdates: () => env.updates,
}));

vi.mock("./updates", async (importOriginal) => ({
	...(await importOriginal<typeof import("./updates")>()),
	checkAndDownload: env.check,
}));

vi.mock("./updateScreenState", () => ({ showUpdateReady: env.show }));

import { UpdatesManager } from "./UpdatesManager";
import { CHECK_STALE_MS, REOFFER_MS } from "./updates";

const T0 = new Date("2026-11-27T10:00:00Z").getTime();
let renderer: ReactTestRenderer;

function rerender() {
	act(() => renderer.update(createElement(UpdatesManager)));
}

/** A trip out of the app and back, `ms` after the current time. */
function returnAfter(ms: number) {
	vi.setSystemTime(Date.now() + ms);
	act(() => {
		for (const listener of env.listeners) listener("background");
		for (const listener of env.listeners) listener("active");
	});
}

beforeEach(() => {
	vi.useFakeTimers({ toFake: ["Date"] });
	vi.setSystemTime(T0);
	env.listeners.clear();
	env.pathname = "/";
	env.updates = { isUpdatePending: false, downloadedUpdate: undefined, lastCheckForUpdateTimeSinceRestart: undefined };
	env.check.mockClear();
	env.show.mockClear();
	act(() => {
		renderer = create(createElement(UpdatesManager));
	});
});

afterEach(() => {
	act(() => renderer.unmount());
	vi.useRealTimers();
});

describe("UpdatesManager resume check", () => {
	it("leaves the first minutes to the native launch check", () => {
		returnAfter(CHECK_STALE_MS - 1);
		expect(env.check).not.toHaveBeenCalled();
	});

	it("checks on a short return once the last check is stale", () => {
		returnAfter(CHECK_STALE_MS);
		expect(env.check).toHaveBeenCalledTimes(1);
		// Our own check counts: an immediate second return does not check again.
		returnAfter(1_000);
		expect(env.check).toHaveBeenCalledTimes(1);
	});

	it("counts a later native check (from Settings) as fresh", () => {
		vi.setSystemTime(T0 + CHECK_STALE_MS);
		env.updates = { ...env.updates, lastCheckForUpdateTimeSinceRestart: new Date(Date.now()) };
		rerender();
		returnAfter(CHECK_STALE_MS - 1);
		expect(env.check).not.toHaveBeenCalled();
	});
});

describe("UpdatesManager offers", () => {
	function download(updateId: string) {
		env.updates = { ...env.updates, isUpdatePending: true, downloadedUpdate: { updateId } };
		rerender();
	}

	it("offers a download once, then again only a day after Not now", () => {
		download("u1");
		expect(env.show).toHaveBeenCalledTimes(1);
		returnAfter(CHECK_STALE_MS);
		returnAfter(REOFFER_MS - CHECK_STALE_MS - 1);
		expect(env.show).toHaveBeenCalledTimes(1);
		returnAfter(1);
		expect(env.show).toHaveBeenCalledTimes(2);
	});

	it("offers a newer download straight away", () => {
		download("u1");
		returnAfter(CHECK_STALE_MS);
		expect(env.check).toHaveBeenCalledTimes(1);
		download("u2");
		expect(env.show).toHaveBeenCalledTimes(2);
	});

	it("never re-offers over onboarding or pairing", () => {
		download("u1");
		env.pathname = "/pair";
		rerender();
		returnAfter(REOFFER_MS);
		expect(env.show).toHaveBeenCalledTimes(1);
	});
});
