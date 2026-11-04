import { beforeEach, describe, expect, it, vi } from "vitest";

const sent = vi.hoisted(() => ({ bodies: [] as Array<{ url: string; body: { message: string; userAuthored?: boolean } }>, failAt: -1 }));

vi.mock("../api", () => ({
	apiRequest: vi.fn(async (_cfg: unknown, url: string, init?: { body?: string }) => {
		if (sent.bodies.length === sent.failAt) throw new Error("offline");
		sent.bodies.push({ url, body: JSON.parse(init?.body ?? "{}") });
		return new Response("{}");
	}),
}));

vi.mock("../config", () => ({
	httpBase: (c: { host: string; httpPort: number }) => `http://${c.host}:${c.httpPort}`,
	authHeaders: (c: { password?: string; hostId?: string }) => ({
		...(c.password ? { Authorization: `Bearer ${c.password}` } : {}),
		...(c.hostId ? { "X-AO-Expected-Host-ID": c.hostId } : {}),
	}),
}));

import { sendFileFeedback, workspaceImageSource } from "./api";
import { changedPathCount } from "./navigation";

const cfg = { host: "mac.local", httpPort: 3001, password: "pw", hostId: "h1" } as never;

beforeEach(() => {
	sent.bodies = [];
	sent.failAt = -1;
});

describe("sending review feedback", () => {
	it("relays comments as the user's own words, batched like desktop", async () => {
		const count = await sendFileFeedback(cfg, "s 1", [
			{ target: { path: "a.ts", side: "new", line: 3 }, feedback: "rename this" },
			{ target: { path: "b.ts", side: "old", line: 9 }, feedback: "why was this removed?" },
		]);
		expect(count).toBe(2);
		expect(sent.bodies).toHaveLength(1);
		expect(sent.bodies[0].url).toBe("/api/v1/sessions/s%201/send");
		expect(sent.bodies[0].body.userAuthored).toBe(true);
		expect(sent.bodies[0].body.message).toContain("The user left 2 inline feedback comments");
		expect(sent.bodies[0].body.message).toContain("- Path: b.ts");
	});

	it("reports each delivered message so a failure keeps only what was not sent", async () => {
		const long = "x".repeat(1700);
		const comments = Array.from({ length: 4 }, (_, index) => ({ target: { path: `f${index}.ts`, side: "new" as const, line: 1 }, feedback: long }));
		const delivered: number[] = [];
		sent.failAt = 1;
		await expect(sendFileFeedback(cfg, "s", comments, (count) => delivered.push(count))).rejects.toThrow("offline");
		expect(delivered).toHaveLength(1);
		expect(delivered[0]).toBeGreaterThan(0);
		expect(delivered[0]).toBeLessThan(4);
	});
});

describe("image sources", () => {
	it("points at the blob route with auth headers and a cache-busting version", () => {
		expect(workspaceImageSource(cfg, "s", "img/a b.png", "before", "v7")).toEqual({
			uri: "http://mac.local:3001/api/v1/sessions/s/workspace/file/blob?path=img%2Fa+b.png&side=before&v=v7",
			headers: { Authorization: "Bearer pw", "X-AO-Expected-Host-ID": "h1" },
		});
	});
});

describe("changedPathCount", () => {
	it("counts each path once across turns", () => {
		expect(changedPathCount([{ diff: { files: [{ path: "a" }, { path: "b" }] } }, { diff: { files: [{ path: "a" }] } }, {}])).toBe(2);
		expect(changedPathCount(undefined)).toBe(0);
	});
});
