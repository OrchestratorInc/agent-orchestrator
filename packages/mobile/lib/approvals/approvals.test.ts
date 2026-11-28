import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({ default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() } }));
vi.mock("expo-secure-store", () => ({ getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn() }));
vi.mock("expo/fetch", () => ({ fetch: vi.fn() }));

import type { ServerConfig } from "../config";
import { CloudError } from "../cloud/errors";
import { cloudApprovalCard, decideCloudApproval, listCloudApprovals } from "./cloudApprovals";
import { decideApproval, listApprovals, subscribeApprovals } from "./localApprovals";
import { approvalCardText, localApprovalCard } from "./types";

const host: ServerConfig = { host: "a.test", hostId: "h_a", httpPort: "3011", muxPort: "", password: "token-a" };
const pending = (id: string, sessionId = "s1") => ({
	id, kind: "browser-action", sessionId, site: "https://shop.example", action: `Click "Place order" on shop.example`,
	category: "payment", reason: "Pays money", createdAt: "2026-11-23T10:00:00Z", expiresAt: "2026-11-23T10:01:30Z",
});
const ok = (body: unknown) => new Response(JSON.stringify(body), { status: 200 });

describe("local approvals", () => {
	beforeEach(() => vi.stubGlobal("fetch", vi.fn()));
	afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); });

	it("lists one session's pending approvals as cards", async () => {
		vi.mocked(fetch).mockResolvedValue(ok({ pending: [pending("apr_1"), pending("apr_2", "s2")] }));
		const cards = await listApprovals(host, "s1");
		expect(vi.mocked(fetch).mock.calls[0][0]).toBe("http://a.test:3011/api/v1/approvals");
		expect(cards).toEqual([expect.objectContaining({ id: "apr_1", summary: `Click "Place order" on shop.example`, site: "shop.example", canDecide: true, canAllowSite: true })]);
	});

	it("sends the daemon's decision values", async () => {
		vi.mocked(fetch).mockResolvedValue(ok({ id: "apr_1", decision: "allow-site" }));
		expect(await decideApproval(host, "apr/1", "allow-site")).toBe("decided");
		expect(vi.mocked(fetch)).toHaveBeenCalledWith("http://a.test:3011/api/v1/approvals/apr%2F1/decision",
			expect.objectContaining({ method: "POST", body: JSON.stringify({ decision: "allow-site" }) }));
	});

	it("late decision returns already-decided", async () => {
		vi.mocked(fetch).mockResolvedValue(new Response(JSON.stringify({ code: "APPROVAL_NOT_PENDING", message: "This request already ended" }), { status: 409 }));
		expect(await decideApproval(host, "apr_1", "deny")).toBe("already-decided");
	});

	it("re-lists often enough that a card decided elsewhere goes within 2 s", async () => {
		vi.useFakeTimers();
		vi.mocked(fetch)
			.mockResolvedValueOnce(ok({ pending: [pending("apr_1")] }))
			.mockResolvedValue(ok({ pending: [] }));
		const seen: string[][] = [];
		const stop = subscribeApprovals(host, "s1", (cards) => seen.push(cards.map((c) => c.id)));
		await vi.advanceTimersByTimeAsync(0);
		await vi.advanceTimersByTimeAsync(2_000);
		stop();
		expect(seen[0]).toEqual(["apr_1"]);
		expect(seen.at(-1)).toEqual([]);
	});
});

describe("Always on <site>", () => {
	const local = { id: "a1", kind: "browser-action", sessionId: "s1", site: "https://shop.example", action: "Run script", category: "payment", reason: "", createdAt: "x", expiresAt: "y" };
	const cloud = { id: "a1", sessionId: "s1", action: "eval", category: "payment", reason: "", site: "shop.example", summary: "", createdAt: "x", expiresAt: "y" };

	it("no card offers it for the catch-all script and unknown categories", () => {
		for (const category of ["script", "unknown"]) {
			expect(localApprovalCard({ ...local, category }).canAllowSite).toBe(false);
			expect(cloudApprovalCard({ ...cloud, category }).canAllowSite).toBe(false);
		}
		expect(localApprovalCard(local).canAllowSite).toBe(true);
		expect(cloudApprovalCard(cloud).canAllowSite).toBe(true);
	});
});

describe("cloud approvals", () => {
	const row = { id: "a1", sessionId: "s1", action: "click", category: "pay", reason: "Pays", site: "shop.example", summary: "Click Buy on shop.example", createdAt: "x", expiresAt: "2026-11-23T10:01:30Z" };

	it("canDecide missing → decidable", () => {
		expect(cloudApprovalCard(row)).toEqual(expect.objectContaining({ canDecide: true, summary: "Click Buy on shop.example", site: "shop.example" }));
	});

	it("read-only card for non-deciders names who decides", () => {
		expect(cloudApprovalCard({ ...row, canDecide: false, decider: { name: "Ada" } })).toEqual(expect.objectContaining({ canDecide: false, deciderName: "Ada" }));
	});

	it("lists and decides through the control plane, and a 409 is already-decided", async () => {
		const client = { listBrowserApprovals: vi.fn(), decideBrowserApproval: vi.fn() };
		client.listBrowserApprovals.mockResolvedValueOnce({ pending: [row] });
		expect((await listCloudApprovals(client, "o1", "s1")).map((c) => c.id)).toEqual(["a1"]);
		expect(client.listBrowserApprovals).toHaveBeenCalledWith("o1", "s1");
		client.decideBrowserApproval.mockRejectedValueOnce(new CloudError("gone", { status: 409 }));
		expect(await decideCloudApproval(client, "o1", "s1", "a1", "deny")).toBe("already-decided");
		expect(client.decideBrowserApproval).toHaveBeenLastCalledWith("o1", "s1", "a1", "deny");
	});
});

describe("approval text", () => {
	it("is one plain line capped at 140 characters", () => {
		const card = localApprovalCard({ ...pending("apr_1"), action: `Click "${"x".repeat(300)}"\n‮` });
		const text = approvalCardText(card.summary);
		expect([...text].length).toBeLessThanOrEqual(140);
		expect(text).not.toMatch(/[\n‮]/);
	});
});
