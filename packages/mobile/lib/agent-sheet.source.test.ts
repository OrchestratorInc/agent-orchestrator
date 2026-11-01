import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const route = readFileSync(new URL("../app/sheets/agent.tsx", import.meta.url), "utf8");

describe("agent sheet", () => {
	// The refresh control already answers the pull, so a refresh that works
	// adds nothing; one that fails still buzzes with its error.
	it("buzzes on a failed refresh but not on a successful one", () => {
		const refresh = route.slice(route.indexOf("async function onRefresh()"), route.indexOf("const allowedSet"));
		expect(refresh).toContain("haptics.error();");
		expect(refresh).not.toContain("haptics.success();");
		expect(refresh).not.toContain("haptics.tap();");
	});
});
