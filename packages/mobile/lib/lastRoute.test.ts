import { describe, expect, it } from "vitest";
import { parseLastRoute, RESTORE_WITHIN_MS, restorableHref, routeHref, routeToRestore } from "./lastRoute";

describe("the last screen", () => {
	it("is remembered as its path with the parameters the path does not carry", () => {
		expect(routeHref("/session/web-1", { id: "web-1", hostId: "demo-machine", projectId: "web-app" })).toBe("/session/web-1?hostId=demo-machine&projectId=web-app");
		expect(routeHref("/spawn", {})).toBe("/spawn");
	});

	it("is a reading screen, never a sheet, pairing or onboarding", () => {
		for (const href of ["/session/web-1?hostId=a", "/files/web-1/file?path=a.ts", "/artifact/web-1?path=r.md", "/preview/web-1", "/shell/h1", "/spawn", "/spawn?hostId=a"]) {
			expect(restorableHref(href)).toBe(true);
		}
		for (const href of ["/", "/pair", "/onboarding", "/sheets/model", "/settings", "/spawner", null]) {
			expect(restorableHref(href)).toBe(false);
		}
	});

	it("is reopened soon after the app was left, and only then", () => {
		const saved = { href: "/session/web-1?hostId=a", leftAt: 1_000 };
		expect(routeToRestore(saved, 1_000 + 60_000)).toBe("/session/web-1?hostId=a");
		expect(routeToRestore(saved, 1_000 + RESTORE_WITHIN_MS + 1)).toBeNull();
		// Still open (never left), or on the board when left: nothing to reopen.
		expect(routeToRestore({ href: saved.href, leftAt: null }, 2_000)).toBeNull();
		expect(routeToRestore({ href: null, leftAt: 1_000 }, 2_000)).toBeNull();
	});

	it("gives way to a notification tap that launched the app", () => {
		const saved = { href: "/session/web-1", leftAt: 1_000 };
		expect(routeToRestore(saved, 5_000, { notificationAt: 4_000 })).toBeNull();
		// An old notification from before the app was left does not count.
		expect(routeToRestore(saved, 5_000, { notificationAt: 500 })).toBe("/session/web-1");
	});

	it("reads back what was saved, and nothing from garbage", () => {
		expect(parseLastRoute(JSON.stringify({ href: "/spawn", leftAt: 3 }))).toEqual({ href: "/spawn", leftAt: 3 });
		expect(parseLastRoute("{")).toBeNull();
		expect(parseLastRoute(null)).toBeNull();
	});
});
