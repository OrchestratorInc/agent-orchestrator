import { describe, expect, it } from "vitest";

type AsyncValueCache<K, V> = {
	load(key: K, loader: () => Promise<V>, keep?: (value: V) => boolean): Promise<V>;
	set(key: K, value: V): void;
	delete(key: K): void;
};

async function cacheModule() {
	return import("./asyncValueCache").catch(() => ({})) as Promise<{
		createAsyncValueCache?: <K, V>(maxEntries: number, ttlMs: number, now?: () => number) => AsyncValueCache<K, V>;
	}>;
}

describe("bounded asynchronous value cache", () => {
	it("shares one in-flight load for the same key", async () => {
		const createCache = (await cacheModule()).createAsyncValueCache;
		const cache = createCache?.<string, string>(4, 30_000);
		let loads = 0;
		let resolve!: (value: string) => void;
		const loader = () => {
			loads += 1;
			return new Promise<string>((done) => { resolve = done; });
		};

		const first = cache?.load("skills", loader);
		const second = cache?.load("skills", loader);
		resolve?.("catalog");

		expect(await Promise.all([first, second])).toEqual(["catalog", "catalog"]);
		expect(loads).toBe(1);
	});

	it("reuses a warm value until its TTL expires", async () => {
		let now = 1_000;
		const createCache = (await cacheModule()).createAsyncValueCache;
		const cache = createCache?.<string, number>(4, 100, () => now);
		let loads = 0;
		const load = () => Promise.resolve(++loads);

		expect(await cache?.load("settings", load)).toBe(1);
		expect(await cache?.load("settings", load)).toBe(1);
		now = 1_101;
		expect(await cache?.load("settings", load)).toBe(2);
	});

	it("evicts the least recently used value at its entry bound", async () => {
		const createCache = (await cacheModule()).createAsyncValueCache;
		const cache = createCache?.<string, string>(2, 30_000);
		cache?.set("one", "1");
		cache?.set("two", "2");
		await cache?.load("one", () => Promise.resolve("new-1"));
		cache?.set("three", "3");

		expect(await cache?.load("one", () => Promise.resolve("new-1"))).toBe("1");
		expect(await cache?.load("two", () => Promise.resolve("new-2"))).toBe("new-2");
	});

	it("does not cache a value the caller declines to keep", async () => {
		const createCache = (await cacheModule()).createAsyncValueCache;
		const cache = createCache?.<string, string[]>(2, 30_000);
		const keep = (value: string[]) => value.length > 0;
		expect(await cache?.load("catalog", () => Promise.resolve([]), keep)).toEqual([]);
		expect(await cache?.load("catalog", () => Promise.resolve(["model"]), keep)).toEqual(["model"]);
		expect(await cache?.load("catalog", () => Promise.resolve([]), keep)).toEqual(["model"]);
	});

	it("leaves a newer value alone when a superseded load comes back declined", async () => {
		const createCache = (await cacheModule()).createAsyncValueCache;
		const cache = createCache?.<string, string[]>(2, 30_000);
		const keep = (value: string[]) => value.length > 0;
		let finishSlow!: (value: string[]) => void;
		const slow = cache?.load("catalog", () => new Promise<string[]>((done) => { finishSlow = done; }), keep);
		cache?.delete("catalog");
		expect(await cache?.load("catalog", () => Promise.resolve(["model"]), keep)).toEqual(["model"]);
		finishSlow([]);
		expect(await slow).toEqual([]);
		expect(await cache?.load("catalog", () => Promise.resolve(["other"]), keep)).toEqual(["model"]);
	});

	it("allows explicit invalidation", async () => {
		const createCache = (await cacheModule()).createAsyncValueCache;
		const cache = createCache?.<string, string>(2, 30_000);
		cache?.set("skills", "old");
		cache?.delete("skills");

		expect(await cache?.load("skills", () => Promise.resolve("new"))).toBe("new");
	});
});
