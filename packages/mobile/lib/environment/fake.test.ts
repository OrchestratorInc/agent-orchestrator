import { describe, expect, it } from "vitest";
import { createFakeSessionSource } from "./fake";

describe("createFakeSessionSource", () => {
	it("answers with empty collections by default", async () => {
		const source = createFakeSessionSource();
		expect(await source.listProjects()).toEqual([]);
		expect(await source.listSessions()).toEqual([]);
	});

	it("lets a test override one method and keep the rest", async () => {
		const source = createFakeSessionSource({
			listProjects: async () => [{ id: "p1", name: "web" }],
		});
		expect(await source.listProjects()).toEqual([{ id: "p1", name: "web" }]);
		expect(await source.listSessions()).toEqual([]);
	});

	it("reports its environment kind", () => {
		expect(createFakeSessionSource().kind).toBe("local");
		expect(createFakeSessionSource({ kind: "cloud" }).kind).toBe("cloud");
	});
});
