import { afterEach, describe, expect, it } from "vitest";
import { cachedDiagram, clearDiagramCache, diagramCacheKey, storeDiagram } from "./mermaidSvgCache";

afterEach(clearDiagramCache);

describe("mermaidSvgCache", () => {
	it("keys a diagram by its source and theme", () => {
		const key = diagramCacheKey("graph TD; A-->B", "dark");
		expect(diagramCacheKey("graph TD; A-->B", "dark")).toBe(key);
		expect(diagramCacheKey("graph TD; A-->B", "light")).not.toBe(key);
		expect(diagramCacheKey("graph TD; A-->C", "dark")).not.toBe(key);
	});

	it("never gives two different sources one entry", () => {
		const a = "graph TD; A-->B";
		const b = "graph TD; A-->C";
		expect(a.length).toBe(b.length);
		storeDiagram(diagramCacheKey(a, "dark"), "<svg id=a/>");
		expect(cachedDiagram(diagramCacheKey(b, "dark"))).toBeUndefined();
		expect(cachedDiagram(diagramCacheKey(a, "dark"))).toBe("<svg id=a/>");
	});

	it("returns a stored diagram", () => {
		storeDiagram("k", "<svg/>");
		expect(cachedDiagram("k")).toBe("<svg/>");
		expect(cachedDiagram("other")).toBeUndefined();
	});

	it("lets the oldest diagrams go past its bound", () => {
		for (let index = 0; index < 50; index += 1) storeDiagram(`k${index}`, "<svg/>");
		expect(cachedDiagram("k0")).toBeUndefined();
		expect(cachedDiagram("k49")).toBe("<svg/>");
	});

	it("does not keep one diagram larger than the whole cache", () => {
		storeDiagram("huge", "x".repeat(2_000_001));
		expect(cachedDiagram("huge")).toBeUndefined();
	});
});
