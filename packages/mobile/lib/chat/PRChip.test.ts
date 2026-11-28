import { describe, expect, it, vi } from "vitest";

vi.mock("react-native", () => ({ StyleSheet: { create: (styles: unknown) => styles }, Text: "Text", Platform: { OS: "ios", select: (o: { ios?: unknown }) => o.ios } }));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../AppSymbol", () => ({ AppSymbol: "AppSymbol" }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({}), useThemedStyles: () => ({}) }));

import { prChipAccessibilityLabel, prChipHeadline } from "./PRChip";

const ready = {
	number: 42, url: "https://github.test/acme/tracker/pull/42", state: "open",
	ciStatus: "passing", reviewDecision: "approved", mergeability: { mergeable: true, state: "mergeable" },
} as never;

describe("the composer's pull request chip", () => {
	it("says what to do next when no merge is running", () => {
		expect(prChipHeadline(ready).text).toBe("Ready to merge");
		expect(prChipAccessibilityLabel(ready)).toBe("Pull request 42, Ready to merge");
	});

	// Started from the PR page and left: the chat's chip no longer offers a
	// merge that is already running.
	it("says Merging… in the merged colour while a merge runs", () => {
		expect(prChipHeadline(ready, undefined, true)).toEqual({ text: "Merging…", tone: "merged" });
		expect(prChipAccessibilityLabel(ready, undefined, undefined, true)).toBe("Pull request 42, Merging…");
	});
});
