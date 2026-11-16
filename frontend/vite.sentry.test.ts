import { describe, expect, it } from "vitest";
import { sentrySourceMaps } from "./vite.sentry";

describe("release source maps", () => {
	it("emits nothing for a build that is not published", () => {
		expect(sentrySourceMaps({})).toEqual({ sourcemap: false, plugins: [] });
	});

	it("emits hidden maps with the Sentry debug ID plugin for a published build", () => {
		const config = sentrySourceMaps({ AO_PUBLISHED_BUILD: "1" });
		expect(config.sourcemap).toBe("hidden");
		expect(config.plugins.length).toBeGreaterThan(0);
	});
});
