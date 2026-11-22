import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const source = (path: string) => readFileSync(fileURLToPath(new URL(path, import.meta.url)), "utf8");

// The Artifacts grid mounted every card at once (FlatList's default window is
// 21 screens) and asked for a preview as soon as 1% of a card showed.
describe("artifact grids load lazily", () => {
	it("mounts a few screens of cards and previews only cards a quarter on screen", () => {
		const card = source("./ArtifactCard.tsx");
		expect(card).toContain("windowSize: 5");
		expect(card).toContain("itemVisiblePercentThreshold: 25");
		for (const page of ["../app/artifacts/index.tsx", "../app/artifacts/[sessionId].tsx"]) {
			const text = source(page);
			expect(text).toContain("{...ARTIFACT_GRID_WINDOW}");
			expect(text).toContain("useRef(ARTIFACT_VIEWABILITY)");
			// Previews change only once the grid settles, not mid-scroll.
			expect(text).toContain("useSettledVisibility<");
		}
	});

	it("redraws only the card whose visibility changed", () => {
		expect(source("../app/artifacts/index.tsx")).toContain("const GridCell = memo(function GridCell(");
	});
});
