import { createElement } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const nav = vi.hoisted(() => ({
	pathname: "/",
	segments: [] as string[],
	params: {} as Record<string, string>,
	key: "root" as string | undefined,
	push: vi.fn(),
	replace: vi.fn(),
	resolveSaved: (_href: string | null) => {},
	setCurrentPage: vi.fn(),
}));

vi.mock("expo-router", () => ({
	useRouter: () => ({ push: nav.push, replace: nav.replace }),
	usePathname: () => nav.pathname,
	useSegments: () => nav.segments,
	useGlobalSearchParams: () => nav.params,
	useRootNavigationState: () => (nav.key ? { key: nav.key } : undefined),
}));

vi.mock("./updateResumeStore", () => ({
	setCurrentPage: nav.setCurrentPage,
	// Storage that answers only when the test says so.
	takeResumeRoute: () =>
		new Promise<string | null>((resolve) => {
			nav.resolveSaved = resolve;
		}),
}));

import { UpdateRouteKeeper } from "./UpdateRouteKeeper";

let renderer: ReactTestRenderer;

function go(pathname: string, segments: string[] = pathname.split("/").filter(Boolean)) {
	nav.pathname = pathname;
	nav.segments = segments;
	act(() => renderer.update(createElement(UpdateRouteKeeper)));
}

async function storageAnswers(href: string | null) {
	await act(async () => nav.resolveSaved(href));
}

beforeEach(() => {
	nav.pathname = "/";
	nav.segments = [];
	nav.params = {};
	nav.key = "root";
	nav.push.mockClear();
	nav.replace.mockClear();
	act(() => {
		renderer = create(createElement(UpdateRouteKeeper));
	});
});

afterEach(() => act(() => renderer.unmount()));

describe("UpdateRouteKeeper restore", () => {
	it("reopens the saved page over the board when the user stayed put", async () => {
		await storageAnswers("/settings/theme");
		expect(nav.push).toHaveBeenCalledWith("/settings/theme");
	});

	it("does not restore after the user left the landing page, even if they came back before storage answered", async () => {
		go("/settings");
		go("/");
		await storageAnswers("/settings/theme");
		expect(nav.push).not.toHaveBeenCalled();
		expect(nav.replace).not.toHaveBeenCalled();
	});

	it("does not restore while the user is somewhere else", async () => {
		go("/settings");
		await storageAnswers("/settings/theme");
		expect(nav.push).not.toHaveBeenCalled();
		expect(nav.replace).not.toHaveBeenCalled();
	});

	it("does nothing when no page was saved", async () => {
		await storageAnswers(null);
		expect(nav.push).not.toHaveBeenCalled();
		expect(nav.replace).not.toHaveBeenCalled();
	});
});
