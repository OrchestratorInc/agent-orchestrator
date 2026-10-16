import { describe, expect, it } from "vitest";
import { collapseModelVariants, findModelVariant, groupModelsByVendor, pickModelVariant, readableModelLabel } from "./model-variants";

// Shapes taken from a real Cursor catalog.
const catalog = [
	{ id: "auto", label: "Auto" },
	{ id: "claude-opus-4-7-xhigh", label: "Claude Opus 4.7 1M" },
	{ id: "claude-opus-4-7-xhigh-fast", label: "Claude Opus 4.7 1M Fast" },
	{ id: "claude-opus-4-7-high", label: "Claude Opus 4.7 1M High" },
	{ id: "claude-opus-4-7-thinking-high", label: "Claude Opus 4.7 1M High Thinking" },
	{ id: "claude-opus-4-7-thinking-high-fast", label: "Claude Opus 4.7 1M High Thinking Fast" },
	{ id: "claude-opus-4-7-low", label: "Claude Opus 4.7 1M Low" },
	{ id: "claude-4.6-opus-max-thinking", label: "Claude Opus 4.6 1M Max Thinking" },
	{ id: "claude-4.6-opus-high", label: "Claude Opus 4.6 1M" },
	{ id: "gpt-5.3-codex", label: "Codex 5.3" },
	{ id: "gpt-5.3-codex-high", label: "Codex 5.3 High" },
	{ id: "gpt-5.3-codex-fast", label: "Codex 5.3 Fast" },
	{ id: "gpt-5.5-extra-high", label: "GPT-5.5 1M Extra High" },
	{ id: "gpt-5.5-medium", label: "GPT-5.5 1M" },
	{ id: "gemini-3-flash", label: "Gemini 3 Flash" },
	{ id: "glm-5.2-high", label: "GLM 5.2" },
	{ id: "glm-5.2-max", label: "GLM 5.2 Max" },
	{ id: "grok-4.7-high-fast", label: "Grok 4.7  High Fast​​" },
];

describe("collapseModelVariants", () => {
	const families = collapseModelVariants(catalog);
	const family = (id: string) => families.find((entry) => entry.id === id);

	it("lists each model once, under its maker, without the setting words", () => {
		expect(families.map((entry) => `${entry.group}: ${entry.label}`)).toEqual([
			"Cursor: Auto",
			"Anthropic: Claude Opus 4.7",
			"Anthropic: Claude Opus 4.6",
			"OpenAI: Codex 5.3",
			"OpenAI: GPT-5.5",
			"Google: Gemini 3 Flash",
			"Z.ai: GLM 5.2",
			"xAI: Grok 4.7",
		]);
	});

	it("turns the settings in an id into effort, thinking and speed", () => {
		const opus = family("claude-opus-4-7");
		expect(opus?.efforts).toEqual(["low", "high", "xhigh"]);
		expect(opus?.hasFast).toBe(true);
		expect(opus?.hasThinkingChoice).toBe(true);
		expect(opus?.defaultVariant.id).toBe("claude-opus-4-7-xhigh");
		expect(family("gpt-5.5")?.efforts).toEqual(["medium", "xhigh"]);
		expect(family("gemini-3-flash")?.efforts).toEqual([]);
		// A bare id takes the level its siblings leave free.
		expect(family("gpt-5.3-codex")?.efforts).toEqual(["medium", "high"]);
		expect(family("gpt-5.3-codex")?.defaultVariant.id).toBe("gpt-5.3-codex");
	});

	it("finds a variant and the nearest one to a wanted setting", () => {
		const found = findModelVariant(families, "claude-opus-4-7-thinking-high-fast");
		expect(found?.family.id).toBe("claude-opus-4-7");
		expect(found?.variant).toMatchObject({ effort: "high", thinking: true, fast: true });
		const opus = family("claude-opus-4-7")!;
		expect(pickModelVariant(opus, { effort: "high", thinking: true, fast: false }).id).toBe("claude-opus-4-7-thinking-high");
		// No fast low variant exists, so effort wins and speed gives way.
		expect(pickModelVariant(opus, { effort: "low", thinking: false, fast: true }).id).toBe("claude-opus-4-7-low");
	});

	it("groups a plain model list by vendor", () => {
		const models = [
			{ id: "claude-opus-4-8", label: "Claude Opus 4.8" },
			{ id: "gpt-5.5", label: "GPT-5.5" },
			{ id: "claude-sonnet-5-5", label: "Claude Sonnet 5.5" },
		];
		expect(groupModelsByVendor(models).map((model) => [model.id, model.group])).toEqual([
			["claude-opus-4-8", "Anthropic"],
			["claude-sonnet-5-5", "Anthropic"],
			["gpt-5.5", "OpenAI"],
		]);
	});

	it("names a routed model by its last part", () => {
		expect(readableModelLabel("openrouter/anthropic/claude-haiku-4.5")).toBe("Claude Haiku 4.5");
	});
});
