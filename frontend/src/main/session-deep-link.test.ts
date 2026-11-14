import { beforeEach, describe, expect, it, vi } from "vitest";

const electron = vi.hoisted(() => ({ app: { isPackaged: false, setAsDefaultProtocolClient: vi.fn() } }));
vi.mock("electron", () => electron);

import {
	classifySessionDeepLink,
	createSessionDeepLinkRouter,
	findSessionDeepLinkArg,
	registerSessionProtocol,
	type SessionDeepLinkEvent,
} from "./session-deep-link";

describe("classifySessionDeepLink", () => {
	it("accepts a session route and returns the canonical URL", () => {
		expect(classifySessionDeepLink("ao://sessions/proj/s-1")).toEqual({ url: "ao://sessions/proj/s-1" });
		expect(classifySessionDeepLink("AO://Sessions/proj/s-1/")).toEqual({ url: "ao://sessions/proj/s-1" });
	});

	it("decodes and re-encodes ids so the URL is stable", () => {
		const link = "ao://sessions/my_project/s%C3%A9";
		expect(classifySessionDeepLink(link)).toEqual({ url: link });
		expect(classifySessionDeepLink("ao://sessions/a%2Dz/s%31")).toEqual({ url: "ao://sessions/a-z/s1" });
	});

	it.each([
		"",
		"ao://",
		"ao://sessions",
		"ao://sessions/proj",
		"ao://sessions//s-1",
		"ao://sessions/proj/s-1/extra",
		"ao://sessions/proj/s-1?x=1",
		"ao://sessions/proj/s-1#frag",
		"ao://user:pw@sessions/proj/s-1",
		"ao://user@sessions/proj/s-1",
		"ao://sessions:80/proj/s-1",
		"ao://sessions/%E0%A4%A/s-1",
		"ao://sessions/proj%2Fx/s-1",
		"ao://sessions/proj/s-1%00",
		"ao://sessions/proj/s%0A1",
		"ao://sessions/pro j/s-1",
		"ao://sessions/proj/s-1\n",
		"ao://sessions/%20/s-1",
		`ao://sessions/${"a".repeat(3000)}/s-1`,
		"https://sessions/proj/s-1",
		"ao-app://sessions/proj/s-1",
	])("rejects malformed link %j", (link) => {
		expect(classifySessionDeepLink(link)).toEqual({ error: "malformed" });
	});

	it("reports well-formed links to other routes as unsupported", () => {
		expect(classifySessionDeepLink("ao://settings")).toEqual({ error: "unsupported" });
		expect(classifySessionDeepLink("ao://kill/proj/s-1")).toEqual({ error: "unsupported" });
		expect(classifySessionDeepLink("ao://sessions/proj/s-1/artifacts/notes.md")).toEqual({ error: "unsupported" });
	});

	it("never carries actions: a route-looking verb is not a session link", () => {
		expect(classifySessionDeepLink("ao://sessions/proj/s-1/kill")).toEqual({ error: "malformed" });
	});
});

describe("findSessionDeepLinkArg", () => {
	it("finds the ao:// entry in argv and ignores ao-app://", () => {
		expect(findSessionDeepLinkArg(["app", "--flag", "ao://sessions/p/s"])).toBe("ao://sessions/p/s");
		expect(findSessionDeepLinkArg(["app", "ao-app://callback?token=1"])).toBeUndefined();
	});
});

describe("createSessionDeepLinkRouter", () => {
	function setup(ready: boolean) {
		const state = { ready };
		const delivered: SessionDeepLinkEvent[] = [];
		const focus = vi.fn();
		const router = createSessionDeepLinkRouter({
			focus,
			deliver: (event) => {
				if (!state.ready) return false;
				delivered.push(event);
				return true;
			},
		});
		return { router, state, delivered, focus };
	}

	it("delivers a warm link at once and focuses the window", () => {
		const { router, delivered, focus } = setup(true);
		router.handle("ao://sessions/p/s");
		expect(focus).toHaveBeenCalledOnce();
		expect(delivered).toEqual([{ url: "ao://sessions/p/s" }]);
		expect(router.take()).toBeNull();
	});

	it("queues a cold-launch link until the renderer takes it, once", () => {
		const { router, delivered } = setup(false);
		router.handle("ao://sessions/p/s");
		expect(delivered).toEqual([]);
		expect(router.take()).toEqual({ url: "ao://sessions/p/s" });
		expect(router.take()).toBeNull();
	});

	it("keeps only the newest queued link", () => {
		const { router } = setup(false);
		router.handle("ao://sessions/p/one");
		router.handle("ao://sessions/p/two");
		expect(router.take()).toEqual({ url: "ao://sessions/p/two" });
	});

	it("forwards a rejected link as an error rather than dropping it", () => {
		const { router, delivered } = setup(true);
		router.handle("ao://sessions/p/s?x=1");
		expect(delivered).toEqual([{ error: "malformed" }]);
	});
});

describe("registerSessionProtocol", () => {
	beforeEach(() => electron.app.setAsDefaultProtocolClient.mockClear());

	it("claims ao:// for a packaged app", () => {
		expect(registerSessionProtocol(true)).toBe(true);
		expect(electron.app.setAsDefaultProtocolClient).toHaveBeenCalledWith("ao");
	});

	it("does not take the scheme from the installed app in an unpackaged build", () => {
		expect(registerSessionProtocol(false)).toBe(false);
		expect(registerSessionProtocol()).toBe(false);
		expect(electron.app.setAsDefaultProtocolClient).not.toHaveBeenCalled();
	});
});
