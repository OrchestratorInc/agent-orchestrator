import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { WHATS_NEW_FEED_URL, parseWhatsNewFeed } from "../shared/whats-new";
import { WHATS_NEW_FILE_NAME, WhatsNewService, eligibleWhatsNewItems, whatsNewFeedUrl } from "./whats-new";

const NOW = Date.parse("2026-10-29T12:00:00Z");
const DAY = 24 * 60 * 60 * 1000;

const announcement = {
	id: "2026-10-notifications",
	kind: "announcement",
	title: "Know when agents finish",
	body: "Orchestrator now notifies you when an agent finishes.",
	publishedAt: "2026-10-28",
	imageUrl: "https://docs.orchestrator.inc/whats-new/notifications.png",
	cta: { label: "Read more", url: "https://docs.orchestrator.inc/docs/" },
};
const nudge = {
	id: "update-0-15-2",
	kind: "nudge",
	title: "Update to fix terminal input",
	body: "Versions before 0.15.2 can drop keystrokes.",
	publishedAt: "2026-10-27",
	maxVersion: "0.15.2",
	cta: { label: "Update", action: "update" },
};

function feedResponse(body: unknown, init: { ok?: boolean } = {}) {
	return { ok: init.ok ?? true, text: async () => JSON.stringify(body) } as Response;
}

describe("parseWhatsNewFeed", () => {
	it("keeps valid items and drops malformed, duplicate and unsafe fields", () => {
		const feed = parseWhatsNewFeed({
			version: 1,
			items: [
				announcement,
				announcement,
				{ ...announcement, id: "Bad ID" },
				{ ...announcement, id: "no-title", title: "" },
				{
					...nudge,
					id: "third-party-media",
					imageUrl: "https://tracker.example/pixel.png",
					videoUrl: "http://docs.orchestrator.inc/a.mp4",
					cta: { label: "Go", url: "javascript:alert(1)" },
					minVersion: "latest",
				},
			],
		});
		expect(feed?.enabled).toBe(true);
		expect(feed?.items.map((item) => item.id)).toEqual(["2026-10-notifications", "third-party-media"]);
		const unsafe = feed?.items[1];
		expect(unsafe?.imageUrl).toBeUndefined();
		expect(unsafe?.videoUrl).toBeUndefined();
		expect(unsafe?.cta).toBeUndefined();
		expect(unsafe?.minVersion).toBeUndefined();
	});

	it("honors the remote kill switch and rejects unknown versions", () => {
		expect(parseWhatsNewFeed({ version: 1, enabled: false, items: [announcement] })?.enabled).toBe(false);
		expect(parseWhatsNewFeed({ version: 2, items: [announcement] })).toBeNull();
		expect(parseWhatsNewFeed("nope")).toBeNull();
	});
});

describe("eligibleWhatsNewItems", () => {
	const feed = parseWhatsNewFeed({
		version: 1,
		items: [
			nudge,
			announcement,
			{ ...announcement, id: "future", publishedAt: "2026-11-01" },
			{ ...announcement, id: "expired", publishedAt: "2026-09-01" },
			{ ...announcement, id: "explicit-expiry", publishedAt: "2026-10-20", expiresAt: "2026-10-29T11:00:00Z" },
			{ ...announcement, id: "new-builds", publishedAt: "2026-10-26", minVersion: "0.15.0" },
		],
	});

	it("shows live items for the running version, newest first", () => {
		expect(eligibleWhatsNewItems(feed, "0.15.1", NOW).map((item) => item.id)).toEqual([
			"2026-10-notifications",
			"update-0-15-2",
			"new-builds",
		]);
	});

	it("targets nudges by version range, inclusive min and exclusive max", () => {
		expect(eligibleWhatsNewItems(feed, "0.15.2", NOW).map((item) => item.id)).toEqual(["2026-10-notifications", "new-builds"]);
		expect(eligibleWhatsNewItems(feed, "0.14.9", NOW).map((item) => item.id)).toEqual(["2026-10-notifications", "update-0-15-2"]);
		expect(eligibleWhatsNewItems(feed, "0.15.0-nightly.20261020", NOW).map((item) => item.id)).toContain("new-builds");
	});

	it("shows nothing when the feed is disabled", () => {
		expect(eligibleWhatsNewItems(feed && { ...feed, enabled: false }, "0.15.1", NOW)).toEqual([]);
	});
});

describe("whatsNewFeedUrl", () => {
	it("allows only https or loopback overrides", () => {
		expect(whatsNewFeedUrl({})).toBe(WHATS_NEW_FEED_URL);
		expect(whatsNewFeedUrl({ AO_WHATS_NEW_URL: "http://127.0.0.1:4000/feed.json" })).toBe("http://127.0.0.1:4000/feed.json");
		expect(whatsNewFeedUrl({ AO_WHATS_NEW_URL: "http://example.com/feed.json" })).toBe(WHATS_NEW_FEED_URL);
	});
});

describe("WhatsNewService", () => {
	let stateDir: string;
	let now: number;

	beforeEach(async () => {
		stateDir = await mkdtemp(path.join(tmpdir(), "ao-whats-new-"));
		now = NOW;
	});
	afterEach(async () => {
		await rm(stateDir, { recursive: true, force: true });
	});

	function service(fetchImpl: typeof fetch, env: NodeJS.ProcessEnv = {}) {
		return new WhatsNewService({ stateDir, appVersion: "0.15.1", fetch: fetchImpl, now: () => now, env });
	}

	it("fetches anonymously, caches, and tracks dismissals", async () => {
		const fetchImpl = vi.fn(async () => feedResponse({ version: 1, items: [announcement, nudge] }));
		const whatsNew = service(fetchImpl as unknown as typeof fetch);
		await expect(whatsNew.refresh()).resolves.toBe(true);
		expect(fetchImpl).toHaveBeenCalledOnce();
		const [url, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit];
		expect(url).toBe(WHATS_NEW_FEED_URL);
		expect(url).not.toContain("?");
		expect(init.credentials).toBe("omit");
		expect(Object.keys(init.headers as Record<string, string>)).toEqual(["Accept"]);

		expect((await whatsNew.list()).map((item) => [item.id, item.seen])).toEqual([
			["2026-10-notifications", false],
			["update-0-15-2", false],
		]);
		await whatsNew.dismiss(["2026-10-notifications", "Not An Id"]);
		expect((await whatsNew.list()).map((item) => item.seen)).toEqual([true, false]);
		const stored = JSON.parse(await readFile(path.join(stateDir, WHATS_NEW_FILE_NAME), "utf8"));
		expect(stored.dismissed).toEqual(["2026-10-notifications"]);
	});

	it("fetches at most once per launch and once per day", async () => {
		const fetchImpl = vi.fn(async () => feedResponse({ version: 1, items: [announcement] }));
		const first = service(fetchImpl as unknown as typeof fetch);
		await first.refresh();
		await first.refresh();
		now += DAY / 2;
		await service(fetchImpl as unknown as typeof fetch).refresh();
		expect(fetchImpl).toHaveBeenCalledOnce();
		now += DAY;
		await service(fetchImpl as unknown as typeof fetch).refresh();
		expect(fetchImpl).toHaveBeenCalledTimes(2);
	});

	it("fails silently offline and keeps the cached feed", async () => {
		await writeFile(
			path.join(stateDir, WHATS_NEW_FILE_NAME),
			JSON.stringify({ fetchedAt: NOW - 2 * DAY, feed: { enabled: true, items: [announcement] }, dismissed: [] }),
		);
		const offline = vi.fn(async () => {
			throw new TypeError("fetch failed");
		});
		const whatsNew = service(offline as unknown as typeof fetch);
		await expect(whatsNew.refresh()).resolves.toBe(false);
		expect((await whatsNew.list()).map((item) => item.id)).toEqual(["2026-10-notifications"]);

		const httpError = service(vi.fn(async () => feedResponse({}, { ok: false })) as unknown as typeof fetch);
		await expect(httpError.refresh()).resolves.toBe(false);
	});

	it("re-evaluates the cached feed against the clock on every list without fetching", async () => {
		await writeFile(
			path.join(stateDir, WHATS_NEW_FILE_NAME),
			JSON.stringify({
				fetchedAt: NOW,
				feed: { enabled: true, items: [{ ...announcement, publishedAt: "2026-10-30T00:00:00Z", expiresAt: "2026-11-02T00:00:00Z" }] },
				dismissed: [],
			}),
		);
		const fetchImpl = vi.fn();
		const whatsNew = service(fetchImpl as unknown as typeof fetch);
		await expect(whatsNew.list()).resolves.toEqual([]);
		now = Date.parse("2026-10-30T00:00:01Z");
		expect((await whatsNew.list()).map((item) => item.id)).toEqual(["2026-10-notifications"]);
		now = Date.parse("2026-11-02T00:00:00Z");
		await expect(whatsNew.list()).resolves.toEqual([]);
		expect(fetchImpl).not.toHaveBeenCalled();
	});

	it("does nothing when turned off locally", async () => {
		const fetchImpl = vi.fn();
		const whatsNew = service(fetchImpl as unknown as typeof fetch, { AO_WHATS_NEW: "0" });
		await expect(whatsNew.refresh()).resolves.toBe(false);
		await expect(whatsNew.list()).resolves.toEqual([]);
		expect(fetchImpl).not.toHaveBeenCalled();
	});
});
