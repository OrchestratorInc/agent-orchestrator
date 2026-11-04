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

// Removes the way Electron does: every cookie the URL can see with that name.
function fakeSession(initial: RepairCookie[]) {
	let jar = [...initial];
	return {
		jar: () => jar,
		cookies: {
			get: vi.fn(async () => [...jar]),
			remove: vi.fn(async (url: string, name: string) => {
				const host = new URL(url).hostname;
				jar = jar.filter(
					(item) => item.name !== name || !(item.domain?.startsWith(".") ? `.${host}`.endsWith(item.domain) : item.domain === host),
				);
			}),
			set: vi.fn(async (details: { url: string; name: string; value: string; domain?: string; path?: string }) => {
				const domain = details.domain ?? new URL(details.url).hostname;
				jar = jar.filter((item) => !(item.name === details.name && item.domain === domain && item.path === (details.path ?? "/")));
				jar.push(cookie(domain, details.name, details.value, { path: details.path ?? "/" }));
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

	it("restores a parent-domain cookie of the same name that removal also hits", async () => {
		const session = fakeSession([
			cookie(".accounts.google.com", "NID", "imported"),
			cookie("accounts.google.com", "NID", "real"),
			cookie(".google.com", "NID", "parent"),
		]);
		await repairCookies(session);
		expect(names(session.jar())).toEqual([".google.com|NID=parent", "accounts.google.com|NID=real"]);
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
		expect(session.cookies.remove).not.toHaveBeenCalled();
	});

	it("does not treat different paths as duplicates", async () => {
		const session = fakeSession([cookie(".example.com", "a", "1", { path: "/x" }), cookie("example.com", "a", "2", { path: "/y" })]);
		expect(await repairCookies(session)).toMatchObject({ duplicates: 0 });
		expect(session.cookies.remove).not.toHaveBeenCalled();
	});

	it("counts failures and keeps going", async () => {
		const session = fakeSession([cookie(".a.com", "cf_clearance", "x"), cookie(".b.com", "cf_clearance", "y")]);
		session.cookies.remove.mockRejectedValueOnce(new Error("nope"));
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
			expect(session.cookies.get).toHaveBeenCalledOnce();
		} finally {
			await cleanup();
		}
	});
});
