import { describe, expect, it } from "vitest";
import type { AgentModelCatalog } from "./api";
import { reviewerEffortPick, reviewerModelPick, reviewerModelView, type ReviewerModelState } from "./reviewer-picker.types";

// The shape of Claude Code's catalog: a "default" placeholder, then models
// with their effort levels, one marked as the default.
const claude: Pick<AgentModelCatalog, "selectionMode" | "models"> = {
	selectionMode: "catalog",
	models: [
		{ id: "default", label: "Default (recommended)" },
		{ id: "opus[1m]", label: "Opus (1M context)", isDefault: true, efforts: ["low", "medium", "high", "xhigh", "max"], defaultEffort: "xhigh" },
		{ id: "sonnet", label: "Sonnet", efforts: ["low", "medium", "high"], defaultEffort: "high" },
		{ id: "haiku", label: "Haiku" },
	],
};
// Cursor's names no default model.
const cursor: Pick<AgentModelCatalog, "selectionMode" | "models"> = {
	selectionMode: "catalog",
	models: [{ id: "auto", label: "Auto" }, { id: "gpt-5", label: "GPT-5" }],
};
const amp: Pick<AgentModelCatalog, "selectionMode" | "models"> = {
	selectionMode: "mode",
	models: [{ id: "smart", label: "Smart", isDefault: true }, { id: "rush", label: "Rush" }],
};

const state = (overrides: Partial<ReviewerModelState> = {}): ReviewerModelState => ({ catalog: claude, saved: {}, inherited: {}, ...overrides });
const ids = (row: { options: readonly { id: string }[] } | undefined) => row?.options.map((option) => option.id);

describe("reviewerModelView", () => {
	it("reads the catalog's default model and its default effort when nothing is set", () => {
		const view = reviewerModelView(state());
		expect(view.model).toMatchObject({ title: "Model", value: "opus[1m]", valueLabel: "Opus (1M context)" });
		// No "Provider default", no placeholder, no way back to where it already is.
		expect(ids(view.model)).toEqual(["opus[1m]", "sonnet", "haiku"]);
		expect(view.effort).toMatchObject({ title: "Effort", value: "xhigh", valueLabel: "Xhigh" });
		expect(ids(view.effort)).toEqual(["low", "medium", "high", "xhigh", "max"]);
	});

	it("reads the project's reviewer model and effort the session inherits", () => {
		const view = reviewerModelView(state({ inherited: { model: "sonnet", effort: "medium" } }));
		expect(view.model).toMatchObject({ value: "sonnet", valueLabel: "Sonnet" });
		expect(ids(view.model)).toEqual(["opus[1m]", "sonnet", "haiku"]);
		expect(view.effort).toMatchObject({ value: "medium", valueLabel: "Medium" });
		expect(ids(view.effort)).toEqual(["low", "medium", "high"]);
	});

	it("reads the session's own choice over the project's, offering the way back", () => {
		const view = reviewerModelView(state({ saved: { model: "sonnet", effort: "low" }, inherited: { model: "opus[1m]", effort: "max" } }));
		expect(view.model).toMatchObject({ value: "sonnet", valueLabel: "Sonnet" });
		expect(view.model?.options[0]).toEqual({ id: "", label: "Use agent model" });
		expect(view.effort).toMatchObject({ value: "low", valueLabel: "Low" });
		expect(view.effort?.options[0]).toEqual({ id: "", label: "Clear effort" });
	});

	it("checks \"Use agent model\" when no one names a model", () => {
		const view = reviewerModelView(state({ catalog: cursor }));
		expect(view.model).toMatchObject({ value: "", valueLabel: "Use agent model" });
		expect(ids(view.model)).toEqual(["", "auto", "gpt-5"]);
		expect(view.effort).toBeUndefined();
	});

	it("reads a saved \"default\" as the catalog default, offering the way back", () => {
		expect(reviewerModelView(state({ saved: { model: "default" } })).model).toMatchObject({ value: "opus[1m]" });
		// The daemon takes any saved model over the project's, "default" included,
		// so the review runs the agent's default, not the project's model.
		const view = reviewerModelView(state({ saved: { model: "default" }, inherited: { model: "sonnet" } }));
		expect(view.model).toMatchObject({ value: "opus[1m]", valueLabel: "Opus (1M context)" });
		expect(view.model?.options[0]).toEqual({ id: "", label: "Use agent model" });
	});

	it("reads a project's \"default\" as the catalog default", () => {
		expect(reviewerModelView(state({ inherited: { model: "default" } })).model).toMatchObject({ value: "opus[1m]" });
	});

	it("keeps a model or effort the catalog no longer lists", () => {
		const view = reviewerModelView(state({ saved: { model: "opus-4", effort: "ultra" } }));
		expect(view.model).toMatchObject({ value: "opus-4", valueLabel: "opus-4" });
		expect(ids(view.model)).toContain("opus-4");
		// The model is unknown, so it lists no levels: the saved one can only be cleared.
		expect(view.effort).toMatchObject({ value: "ultra", valueLabel: "Ultra" });
		expect(ids(view.effort)).toEqual(["", "ultra"]);
	});

	it("shows no effort for a model that takes none", () => {
		expect(reviewerModelView(state({ saved: { model: "haiku" } })).effort).toBeUndefined();
	});

	it("checks \"Default\" for a model that names no default level", () => {
		const catalog = { ...claude, models: claude.models.map((model) => ({ ...model, defaultEffort: undefined })) };
		const view = reviewerModelView(state({ catalog }));
		expect(view.effort).toMatchObject({ value: "", valueLabel: "Default" });
		expect(view.effort?.options[0]).toEqual({ id: "", label: "Default" });
	});

	it("names modes for a mode catalog, reading the project's mode", () => {
		const view = reviewerModelView(state({ catalog: amp, inherited: { mode: "rush" } }));
		expect(view.model).toMatchObject({ title: "Mode", value: "rush", valueLabel: "Rush" });
		expect(reviewerModelView(state({ catalog: amp, saved: { mode: "rush" } })).model?.options[0]).toEqual({ id: "", label: "Use agent mode" });
	});

	it("draws nothing without a catalog (still loading, or Orchestrator Cloud)", () => {
		expect(reviewerModelView(state({ catalog: undefined }))).toEqual({});
	});
});

describe("reviewerModelPick", () => {
	it("saves a picked model by name, even the catalog default, keeping other settings", () => {
		expect(reviewerModelPick(state({ saved: { permissions: "plan" } }), "opus[1m]")).toEqual({ permissions: "plan", model: "opus[1m]", mode: "", effort: "" });
	});

	it("keeps the session's effort only where the new model offers it", () => {
		expect(reviewerModelPick(state({ saved: { effort: "high" } }), "sonnet").effort).toBe("high");
		expect(reviewerModelPick(state({ saved: { effort: "max" } }), "sonnet").effort).toBe("");
	});

	it("names the model's default when the inherited effort does not fit it", () => {
		expect(reviewerModelPick(state({ inherited: { effort: "max" } }), "sonnet").effort).toBe("high");
		expect(reviewerModelPick(state({ inherited: { effort: "medium" } }), "sonnet").effort).toBe("");
	});

	it("checks the effort against the inherited model when going back to it", () => {
		expect(reviewerModelPick(state({ saved: { model: "sonnet", effort: "max" }, inherited: { model: "opus[1m]" } }), "")).toMatchObject({ model: "", effort: "max" });
	});

	it("checks the effort against the catalog default when the project names only \"default\"", () => {
		expect(reviewerModelPick(state({ saved: { model: "sonnet", effort: "high" }, inherited: { model: "default" } }), "")).toMatchObject({ model: "", effort: "high" });
	});

	it("saves a mode as a mode", () => {
		expect(reviewerModelPick(state({ catalog: amp }), "rush")).toMatchObject({ model: "", mode: "rush" });
	});
});

describe("reviewerEffortPick", () => {
	it("checks the effort against the model a saved \"default\" runs", () => {
		// Opus is the catalog default; with the project's Sonnet it would read "high".
		expect(reviewerModelView(state({ saved: { model: "default" }, inherited: { model: "sonnet" } })).effort).toMatchObject({ value: "xhigh" });
	});

	it("saves the model's own default as no override when nothing is inherited", () => {
		expect(reviewerEffortPick(state({ saved: { effort: "low" } }), "xhigh").effort).toBe("");
		expect(reviewerEffortPick(state(), "low").effort).toBe("low");
	});

	it("saves every level by name when the project sets one", () => {
		expect(reviewerEffortPick(state({ inherited: { effort: "low" } }), "xhigh").effort).toBe("xhigh");
	});

	it("clears the session's effort", () => {
		expect(reviewerEffortPick(state({ saved: { model: "sonnet", effort: "low" } }), "")).toEqual({ model: "sonnet", effort: "" });
	});
});
