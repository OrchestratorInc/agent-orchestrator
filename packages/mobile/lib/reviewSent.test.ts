import { beforeEach, describe, expect, it, vi } from "vitest";

const store = vi.hoisted(() => ({ items: new Map<string, string>(), getDelay: null as null | Promise<void> }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => {
		if (store.getDelay) await store.getDelay;
		return store.items.get(key) ?? null;
	},
	setItem: async (key: string, value: string) => { store.items.set(key, value); },
	removeItem: async (key: string) => { store.items.delete(key); },
} }));

beforeEach(() => {
	store.items.clear();
	store.getDelay = null;
	vi.resetModules();
});

const saved = () => JSON.parse(store.items.get("ao.reviews.sent.v1") ?? "{}") as Record<string, string[]>;
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

describe("review comments sent by hand", () => {
	it("keeps each session's sends under its machine, newest session last", async () => {
		const { sentScope, withSent } = await import("./reviewSent");
		let state = withSent({}, sentScope("mac", "s1"), ["a"]);
		state = withSent(state, sentScope("mac", "s2"), ["b"]);
		state = withSent(state, sentScope("mac", "s1"), ["a", "c"]);
		expect(Object.entries(state)).toEqual([["mac\ns2", ["b"]], ["mac\ns1", ["a", "c"]]]);
	});

	it("is bounded like the desktop: 200 sessions, 500 sends each", async () => {
		const { withSent } = await import("./reviewSent");
		let state = {};
		for (let index = 0; index < 205; index++) state = withSent(state, `mac\ns${index}`, ["k"]);
		expect(Object.keys(state)).toHaveLength(200);
		expect(Object.keys(state)[0]).toBe("mac\ns5");
		const many = withSent({}, "mac\ns", Array.from({ length: 520 }, (_, index) => `k${index}`));
		expect(many["mac\ns"]).toHaveLength(500);
		expect(many["mac\ns"]?.[0]).toBe("k20");
	});

	it("ignores saved data that is not a list of keys", async () => {
		const { parseSentReviews } = await import("./reviewSent");
		expect(parseSentReviews("not json")).toEqual({});
		expect(parseSentReviews("[1]")).toEqual({});
		expect(parseSentReviews(JSON.stringify({ ok: ["a"], bad: [1], worse: "a" }))).toEqual({ ok: ["a"] });
	});

	it("saves a send, and keeps one made before the saved sends were read", async () => {
		store.items.set("ao.reviews.sent.v1", JSON.stringify({ "mac\ns1": ["old"] }));
		let release!: () => void;
		store.getDelay = new Promise((resolve) => { release = resolve; });
		const { markReviewCommentsSent } = await import("./reviewSent");
		markReviewCommentsSent("mac\ns1", ["new"]);
		release();
		await settle();
		await settle();
		expect(saved()).toEqual({ "mac\ns1": ["old", "new"] });
	});

	it("forgets one machine's sends and leaves the others", async () => {
		store.items.set("ao.reviews.sent.v1", JSON.stringify({ "mac\ns1": ["a"], "cloud\ns2": ["b"], "mac-mini\ns3": ["c"] }));
		const { forgetSentReviewComments } = await import("./reviewSent");
		await forgetSentReviewComments("mac");
		expect(saved()).toEqual({ "cloud\ns2": ["b"], "mac-mini\ns3": ["c"] });
		await forgetSentReviewComments("cloud");
		await forgetSentReviewComments("mac-mini");
		expect(store.items.has("ao.reviews.sent.v1")).toBe(false);
	});
});
