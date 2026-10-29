import { beforeEach, describe, expect, it, vi } from "vitest";

const capture = vi.hoisted(() => vi.fn(async () => true));
vi.mock("./telemetry", () => ({ captureRendererEvent: capture }));

import {
	PARALLEL_PROMPT_FLAG_KEY,
	dismissParallelPrompt,
	rankParallelSuggestions,
	reportParallelPromptShown,
	resetParallelPromptForTest,
	shouldOfferParallelPrompt,
	suggestionSetSource,
} from "./parallel-prompt";

describe("parallel prompt", () => {
	beforeEach(() => {
		window.localStorage.clear();
		capture.mockClear();
		resetParallelPromptForTest();
	});

	it("offers the prompt from the first worker until the third", () => {
		expect(shouldOfferParallelPrompt(0)).toBe(false);
		expect(shouldOfferParallelPrompt(1)).toBe(true);
		expect(shouldOfferParallelPrompt(2)).toBe(true);
		expect(shouldOfferParallelPrompt(3)).toBe(false);
		// Graduation sticks: archiving workers later does not bring it back.
		expect(shouldOfferParallelPrompt(1)).toBe(false);
	});

	it("never offers it again once dismissed", () => {
		dismissParallelPrompt();
		expect(shouldOfferParallelPrompt(1)).toBe(false);
	});

	it("is switched off by the single experiment flag", () => {
		window.localStorage.setItem(PARALLEL_PROMPT_FLAG_KEY, "off");
		expect(shouldOfferParallelPrompt(1)).toBe(false);
		window.localStorage.setItem(PARALLEL_PROMPT_FLAG_KEY, "on");
		expect(shouldOfferParallelPrompt(1)).toBe(true);
	});

	it("ranks issues, then notes, then starter tasks, three in all", () => {
		const ranked = rankParallelSuggestions([
			{ source: "todo", path: "src/a.go", line: 3, text: "handle retries" },
			{ source: "issue", issueNumber: 12, title: "Fix login", url: "https://github.com/acme/repo/issues/12" },
		]);
		expect(ranked.map((suggestion) => suggestion.id)).toEqual(["issue:12", "todo:src/a.go:3", "starter:fixBug"]);
		expect(suggestionSetSource(ranked)).toBe("mixed");
		const starters = rankParallelSuggestions([]);
		expect(starters.map((suggestion) => suggestion.id)).toEqual(["starter:fixBug", "starter:docs", "starter:tests"]);
		expect(suggestionSetSource(starters)).toBe("starter");
	});

	it("reports shown once per project per launch with enums and counts only", () => {
		const ranked = rankParallelSuggestions([{ source: "issue", issueNumber: 12, title: "Fix login" }]);
		reportParallelPromptShown("ao", ranked);
		reportParallelPromptShown("ao", ranked);
		expect(capture.mock.calls).toEqual([["ao.onboarding.parallel_prompt_shown", { suggestion_count: 3, source: "mixed" }]]);
	});
});
