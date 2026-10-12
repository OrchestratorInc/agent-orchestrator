import { describe, expect, it } from "vitest";
import { reconcileRows } from "./rowSharing";
import { createHostedRowsCache } from "./hostedRows";

describe("poll row sharing", () => {
	it("retains unchanged rows and the array across freshly parsed polls", () => {
		const rows = [{ id: "one", detail: { title: "First" } }, { id: "two", detail: { title: "Second" } }];
		expect(reconcileRows(rows, structuredClone(rows))).toBe(rows);
		const updated = reconcileRows(rows, [{ id: "two", detail: { title: "Changed" } }, structuredClone(rows[0])]);
		expect(updated.map((row) => row.detail.title)).toEqual(["Changed", "First"]);
		expect(updated[1]).toBe(rows[0]);
		expect(updated[0]).not.toBe(rows[1]);
		expect(reconcileRows(rows, [])).toEqual([]);
	});

	it("does not replace hosted workers for notification updates or another host's poll", () => {
		const combine = createHostedRowsCache<{ id: string; title: string }>();
		const hosts = [
			{ hostId: "mac", name: "Mac", rows: [{ id: "same", title: "First" }] },
			{ hostId: "dev", name: "Dev", rows: [{ id: "same", title: "Second" }] },
		];
		const initial = combine(hosts);
		expect(combine(hosts.map((host) => ({ ...host, unread: 3 })))).toBe(initial);
		const updated = combine([hosts[0], { ...hosts[1], rows: [{ id: "same", title: "Changed" }] }]);
		expect(updated[0]).toBe(initial[0]);
		expect(updated.map((row) => [row.hostName, row.title])).toEqual([["Mac", "First"], ["Dev", "Changed"]]);
		const renamed = combine([{ ...hosts[0], name: "Studio" }]);
		expect(renamed[0].hostName).toBe("Studio");
		expect(renamed).toHaveLength(1);
		expect(combine([])).toEqual([]);
	});
});
