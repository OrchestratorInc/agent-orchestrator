import { describe, expect, it } from "vitest";
import { statusMarkColor } from "./statusMark";
import type { Theme } from "./theme";

const t = { amber: "#875900", green: "#007a55", red: "#c10007", orange: "#b23a0a", purple: "#7f22fe", textSecondary: "#555" } as unknown as Theme;

describe("statusMarkColor", () => {
	// A light theme's amber is deepened for text (#875900) and read muddy as a fill.
	it("draws a light theme's status hues bright, as fills", () => {
		expect(statusMarkColor(t, t.amber, "light")).toBe("#f5a524");
		expect(statusMarkColor(t, t.green, "light")).toBe("#12b76a");
		expect(statusMarkColor(t, t.red, "light")).toBe("#f04438");
		expect(statusMarkColor(t, t.orange, "light")).toBe("#f97316");
		expect(statusMarkColor(t, t.purple, "light")).toBe("#8b5cf6");
	});

	it("leaves a dark theme's hues, and any colour that is not a status hue, as they are", () => {
		expect(statusMarkColor(t, t.amber, "dark")).toBe("#875900");
		expect(statusMarkColor(t, t.textSecondary, "light")).toBe("#555");
	});
});
