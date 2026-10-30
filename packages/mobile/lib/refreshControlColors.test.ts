import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { refreshControlColors } from "./refreshControlColors";
import type { Theme } from "./theme";

const source = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");

describe("refreshControlColors", () => {
	// Android draws Material's spinner from `colors` and its disc from
	// `progressBackgroundColor`, and ignores `tintColor`, which only iOS reads.
	it("colours the spinner on both platforms from the theme", () => {
		const theme = { accent: "#accent", bgElevated: "#elevated" } as Theme;
		expect(refreshControlColors(theme)).toEqual({ tintColor: "#accent", colors: ["#accent"], progressBackgroundColor: "#elevated" });
	});

	it("is what every board list's pull-to-refresh draws with", () => {
		for (const path of ["./worker-board-list.tsx", "../app/(tabs)/(projects)/projects.tsx", "../app/prs/index.tsx"]) {
			const text = source(path);
			expect(text).toMatch(/<RefreshControl refreshing=\{refreshing\} onRefresh=\{\w+\} \{\.\.\.refreshControlColors\(t\)\} \/>/);
			expect(text).not.toMatch(/<RefreshControl[^>]*tintColor=/);
		}
	});
});
