import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { describe, expect, it, vi } from "vitest";
import { repairBrowserProfileCookies, repairCookies, type RepairCookie } from "./browser-cookie-repair";

const cookie = (domain: string, name: string, value: string, extra: Partial<RepairCookie> = {}): RepairCookie => ({
	domain,
	name,
	value,
	path: "/",
	secure: true,
	httpOnly: true,
	hostOnly: !domain.startsWith("."),
	sameSite: "unspecified",
	expirationDate: 1893456000,
	...extra,
});

// An expired set deletes exactly that name, domain and path, as Chromium does.
function fakeSession(initial: RepairCookie[]) {
	let jar = [...initial];
	return {
		jar: () => jar,
		cookies: {
			get: vi.fn(async () => [...jar]),
			set: vi.fn(async (details: { url: string; name: string; value: string; domain?: string; path?: string; expirationDate?: number }) => {
				const domain = details.domain ?? new URL(details.url).hostname;
				const others = jar.filter((item) => !(item.name === details.name && item.domain === domain && item.path === (details.path ?? "/")));
				jar = (details.expirationDate ?? 0) < 1000 ? others : [...others, cookie(domain, details.name, details.value, { path: details.path ?? "/" })];
			}),
		},
	};
}

const names = (jar: RepairCookie[]) => jar.map((item) => `${item.domain}|${item.name}=${item.value}`).sort();

describe("repairCookies", () => {
	it("removes an import-widened duplicate and keeps the site's real cookie", async () => {
		const session = fakeSession([cookie(".accounts.google.com", "LSID", "imported"), cookie("accounts.google.com", "LSID", "real")]);
		expect(await repairCookies(session)).toMatchObject({ duplicates: 1, antiBot: 0, failed: 0 });
		expect(names(session.jar())).toEqual(["accounts.google.com|LSID=real"]);
	});

	it("leaves a parent-domain cookie of the same name alone", async () => {
		const session = fakeSession([
			cookie(".accounts.google.com", "NID", "imported"),
			cookie("accounts.google.com", "NID", "real"),
			cookie(".google.com", "NID", "parent"),
		]);
		await repairCookies(session);
		expect(names(session.jar())).toEqual([".google.com|NID=parent", "accounts.google.com|NID=real"]);
	});

	it("never writes to a cookie it keeps", async () => {
		const session = fakeSession([
			cookie(".accounts.google.com", "LSID", "imported"),
			cookie("accounts.google.com", "LSID", "real"),
			cookie(".google.com", "LSID", "parent"),
		]);
		await repairCookies(session);
		expect(session.cookies.set).toHaveBeenCalledOnce();
		expect(session.cookies.set).toHaveBeenCalledWith(expect.objectContaining({ domain: ".accounts.google.com", expirationDate: 1 }));
	});

	it("keeps every valid cookie when deleting fails, and repairs on the next run", async () => {
		const session = fakeSession([cookie(".example.com", "sid", "imported"), cookie("example.com", "sid", "real")]);
		session.cookies.set.mockRejectedValueOnce(new Error("nope"));
		expect(await repairCookies(session)).toMatchObject({ duplicates: 0, failed: 1 });
		expect(names(session.jar())).toEqual([".example.com|sid=imported", "example.com|sid=real"]);
		expect(await repairCookies(session)).toMatchObject({ duplicates: 1, failed: 0 });
		expect(names(session.jar())).toEqual(["example.com|sid=real"]);
	});

	it("does not count a cookie that is still there after deleting it", async () => {
		const session = fakeSession([cookie(".example.com", "cf_clearance", "stale")]);
		session.cookies.set.mockResolvedValueOnce(undefined);
		expect(await repairCookies(session)).toMatchObject({ antiBot: 0, failed: 1 });
	});

	it("removes anti-bot cookies wherever they are", async () => {
		const session = fakeSession([
			cookie(".dash.cloudflare.com", "cf_clearance", "stale"),
			cookie(".example.com", "datadome", "stale"),
			cookie(".example.com", "session", "keep"),
		]);
		expect(await repairCookies(session)).toMatchObject({ antiBot: 2, duplicates: 0 });
		expect(names(session.jar())).toEqual([".example.com|session=keep"]);
	});

	it("leaves a domain cookie alone when no host-only copy exists", async () => {
		const session = fakeSession([cookie(".example.com", "session", "keep"), cookie("other.example.com", "session", "other")]);
		expect(await repairCookies(session)).toMatchObject({ duplicates: 0 });
		expect(session.cookies.set).not.toHaveBeenCalled();
	});

	it("does not treat different paths as duplicates", async () => {
		const session = fakeSession([cookie(".example.com", "a", "1", { path: "/x" }), cookie("example.com", "a", "2", { path: "/y" })]);
		expect(await repairCookies(session)).toMatchObject({ duplicates: 0 });
		expect(session.cookies.set).not.toHaveBeenCalled();
	});

	it("does not restore one duplicate while repairing another on an overlapping domain", async () => {
		const session = fakeSession([
			cookie(".example.com", "sid", "imported-parent"),
			cookie("example.com", "sid", "real-parent"),
			cookie(".sub.example.com", "sid", "imported-child"),
			cookie("sub.example.com", "sid", "real-child"),
		]);
		expect(await repairCookies(session)).toMatchObject({ duplicates: 2, failed: 0 });
		expect(names(session.jar())).toEqual(["example.com|sid=real-parent", "sub.example.com|sid=real-child"]);
	});

	it("gives the same result whichever overlapping duplicate comes first", async () => {
		const session = fakeSession([
			cookie("sub.example.com", "sid", "real-child"),
			cookie(".sub.example.com", "sid", "imported-child"),
			cookie("example.com", "sid", "real-parent"),
			cookie(".example.com", "sid", "imported-parent"),
		]);
		await repairCookies(session);
		expect(names(session.jar())).toEqual(["example.com|sid=real-parent", "sub.example.com|sid=real-child"]);
	});

	it("repairs each path and keeps other paths of the same name", async () => {
		const session = fakeSession([
			cookie(".example.com", "a", "imported-x", { path: "/x" }),
			cookie("example.com", "a", "real-x", { path: "/x" }),
			cookie(".example.com", "a", "imported-y", { path: "/y" }),
			cookie("example.com", "a", "real-y", { path: "/y" }),
			cookie(".example.com", "a", "only-z", { path: "/z" }),
		]);
		expect(await repairCookies(session)).toMatchObject({ duplicates: 2, failed: 0 });
		expect(session.jar().map((item) => `${item.domain}${item.path}=${item.value}`).sort()).toEqual([
			".example.com/z=only-z",
			"example.com/x=real-x",
			"example.com/y=real-y",
		]);
	});

	it("counts failures and keeps going", async () => {
		const session = fakeSession([cookie(".a.com", "cf_clearance", "x"), cookie(".b.com", "cf_clearance", "y")]);
		session.cookies.set.mockRejectedValueOnce(new Error("nope"));
		expect(await repairCookies(session)).toMatchObject({ antiBot: 1, failed: 1 });
	});
});

describe("repairBrowserProfileCookies", () => {
	async function fixture() {
		const stateDir = await mkdtemp(path.join(os.tmpdir(), "ao-cookie-repair-"));
		return { stateDir, cleanup: () => rm(stateDir, { recursive: true, force: true }) };
	}

	it("repairs each profile once and remembers it across runs", async () => {
		const { stateDir, cleanup } = await fixture();
		try {
			const session = fakeSession([cookie(".x.com", "cf_clearance", "stale")]);
			const fromProfile = vi.fn(() => session);
			await repairBrowserProfileCookies({ stateDir, profileIds: ["p1"], fromProfile });
			await repairBrowserProfileCookies({ stateDir, profileIds: ["p1"], fromProfile });
			expect(fromProfile).toHaveBeenCalledOnce();
			await repairBrowserProfileCookies({ stateDir, profileIds: ["p1", "p2"], fromProfile });
			expect(fromProfile).toHaveBeenCalledTimes(2);
		} finally {
			await cleanup();
		}
	});

	it("retries a profile whose repair failed, and still repairs the others", async () => {
		const { stateDir, cleanup } = await fixture();
		try {
			const good = fakeSession([cookie(".x.com", "cf_clearance", "stale")]);
			const fromProfile = vi.fn((id: string) => {
				if (id === "bad") throw new Error("locked");
				return good;
			});
			await repairBrowserProfileCookies({ stateDir, profileIds: ["bad", "good"], fromProfile, log: () => undefined });
			expect(good.jar()).toEqual([]);
			const state = JSON.parse(await readFile(path.join(stateDir, "browser-cookie-repair.json"), "utf8"));
			expect(state.repaired).toEqual(["good"]);
		} finally {
			await cleanup();
		}
	});

	it("survives an unreadable state file", async () => {
		const { stateDir, cleanup } = await fixture();
		try {
			await writeFile(path.join(stateDir, "browser-cookie-repair.json"), "not json");
			const session = fakeSession([]);
			await expect(repairBrowserProfileCookies({ stateDir, profileIds: ["p1"], fromProfile: () => session })).resolves.toBeUndefined();
			expect(session.cookies.get).toHaveBeenCalled();
		} finally {
			await cleanup();
		}
	});
});
