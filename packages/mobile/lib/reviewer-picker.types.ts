import type { AgentModelCatalog, ReviewerAgentConfig } from "./api";
import { effortLabel } from "./modelEffort";

export type ReviewerPickerOption = {
	id: string;
	label: string;
};

/**
 * A setting row under the reviewer (its model or mode, its effort): what it
 * reads, the choice checked, and the choices in order. A choice with an empty
 * id clears the session's own value so the reviewer inherits again.
 */
export type ReviewerChoiceRow = {
	title: string;
	/** The id of the checked choice. */
	value: string;
	/** What the row reads. */
	valueLabel: string;
	options: readonly ReviewerPickerOption[];
};

/**
 * The reviewer, model and effort rows of the review actions sheet. Both
 * variants draw native rows for a native list the caller hosts: SwiftUI
 * `Picker`s for a `Form` section on iOS, Material list items that open a
 * dropdown menu inside a Compose `Host` on Android.
 */
export type ReviewerPickerProps = {
	/** Only selectable reviewers; unavailable agents are never offered. */
	reviewers: readonly ReviewerPickerOption[];
	/** The session override, or "" for the project default. */
	selectedReviewer: string;
	/** The harness that actually runs reviews, shown next to "Project default". */
	effectiveReviewer: string;
	onSelectReviewer: (id: string) => void;
	/** Absent when the reviewer has no model/mode catalog. */
	model?: ReviewerChoiceRow;
	onSelectModel: (id: string) => void;
	/** Absent when the model the reviewer runs with takes no effort. */
	effort?: ReviewerChoiceRow;
	onSelectEffort: (level: string) => void;
	busy: boolean;
	/** False where the reviewer's model cannot be chosen at all (Orchestrator Cloud). */
	modelChoice?: boolean;
};

/** The reviewer section's footer, naming the model only where one can be picked. */
export function reviewerSectionFooter(reviewer: ReviewerPickerProps | undefined): string {
	return reviewer?.modelChoice === false
		? "Choose who runs the next Orchestrator review."
		: "Choose who runs the next Orchestrator review and which model it uses.";
}

export function reviewerLabel(reviewers: readonly ReviewerPickerOption[], id: string): string {
	return reviewers.find((item) => item.id === id)?.label || id;
}

/** What the reviewer row reads when the session follows the project's choice. */
export function projectDefaultLabel(reviewers: readonly ReviewerPickerOption[], effectiveReviewer: string): string {
	return effectiveReviewer ? `Project default · ${reviewerLabel(reviewers, effectiveReviewer)}` : "Project default";
}

/**
 * The options to offer: the catalog, plus the current value when the catalog no
 * longer lists it, so the row never reads blank.
 */
export function withCurrentOption(options: readonly ReviewerPickerOption[], current: string): readonly ReviewerPickerOption[] {
	return !current || options.some((option) => option.id === current) ? options : [...options, { id: current, label: current }];
}

/** The reviewer's settings, from which its model and effort rows are drawn. */
export type ReviewerModelState = {
	/** The reviewer agent's model catalog; absent while it loads or where there is none. */
	catalog: Pick<AgentModelCatalog, "selectionMode" | "models"> | undefined;
	/** The session's own reviewer settings; an empty field inherits. */
	saved: ReviewerAgentConfig;
	/** What an empty session field inherits from the project for this reviewer agent. */
	inherited: ReviewerAgentConfig;
};

export type ReviewerModelView = {
	model?: ReviewerChoiceRow;
	effort?: ReviewerChoiceRow;
};

/** A "default" entry, or a saved "default", names no model. */
function isConcreteModelID(id: string | undefined): id is string {
	return Boolean(id) && id!.toLowerCase() !== "default";
}

/** A catalog entry whose label is only "Default (…)" reads by its id instead. */
function modelChoiceLabel(model: { id: string; label: string }): string {
	return /^default(?:\s*\([^)]*\))?$/i.test(model.label.trim()) ? model.id : model.label || model.id;
}

function catalogDefaultModel(catalog: ReviewerModelState["catalog"]): string {
	return catalog?.models.find((model) => model.isDefault && isConcreteModelID(model.id))?.id ?? "";
}

function catalogModel(catalog: ReviewerModelState["catalog"], id: string) {
	return id ? catalog?.models.find((model) => model.id === id) : undefined;
}

/**
 * The model (or mode) the next review runs with, as the daemon resolves it:
 * the session's own choice, else the project's reviewer setting for this
 * agent, else the catalog's default. Empty when none of them names one.
 */
function runsWith({ catalog, saved, inherited }: ReviewerModelState): string {
	const isMode = catalog?.selectionMode === "mode";
	return resolveModel(catalog, isMode ? saved.mode : saved.model, isMode ? inherited.mode : inherited.model);
}

/**
 * The session's model, else the project's, else the catalog default. Like the
 * daemon (mergeReviewerAgentConfig), any saved value wins, "default" included:
 * that runs the agent's default, so it reads as the catalog default rather
 * than falling through to the project's model.
 */
function resolveModel(catalog: ReviewerModelState["catalog"], own: string | undefined, project: string | undefined): string {
	const chosen = own || project;
	return isConcreteModelID(chosen) ? chosen : catalogDefaultModel(catalog);
}

/**
 * The model and effort rows, resolved the way desktop's Reviews tab resolves
 * them (frontend ReviewerPicker): each reads what the next review will
 * actually run with, never "Provider default".
 */
export function reviewerModelView(state: ReviewerModelState): ReviewerModelView {
	const { catalog, saved, inherited } = state;
	if (!catalog) return {};
	const isMode = catalog.selectionMode === "mode";
	const models = catalog.models.filter((model) => isConcreteModelID(model.id)).map((model) => ({ id: model.id, label: modelChoiceLabel(model) }));
	const current = runsWith(state);
	const own = isMode ? saved.mode : saved.model;

	const view: ReviewerModelView = {};
	if (models.length) {
		// "Use agent model" goes back to inheriting. It is offered once the
		// session saves a model of its own (a saved "default" too, so it can be
		// cleared), and stands in as the checked row when nothing names one (a
		// catalog with no default, such as Cursor's).
		const follow = own || !current ? [{ id: "", label: isMode ? "Use agent mode" : "Use agent model" }] : [];
		const options = withCurrentOption(models, current);
		view.model = {
			title: isMode ? "Mode" : "Model",
			value: current,
			valueLabel: current ? reviewerLabel(options, current) : follow[0].label,
			options: [...follow, ...options],
		};
	}

	// Effort belongs to the model the reviewer runs with.
	const effortModel = catalogModel(catalog, current);
	const levels = (effortModel?.efforts ?? []).filter((level) => level && level.toLowerCase() !== "default");
	const savedEffort = saved.effort ?? "";
	const inheritedEffort = inherited.effort ?? "";
	const shown = savedEffort || inheritedEffort;
	if (!levels.length && !shown) return view;
	const reported = effortModel?.defaultEffort?.trim().toLowerCase();
	const modelDefault = levels.find((level) => level.toLowerCase() === reported) ?? "";
	const value = shown || modelDefault;
	// Clearing the session's effort goes back to the project's, or to the
	// model's own default; a saved level the model does not list can only be cleared.
	const clear = Boolean(savedEffort) && (Boolean(inheritedEffort) || !modelDefault || !levels.includes(savedEffort));
	const blank = clear || !value ? [{ id: "", label: clear ? "Clear effort" : "Default" }] : [];
	view.effort = {
		title: "Effort",
		value,
		valueLabel: value ? effortLabel(value) : "Default",
		options: [...blank, ...[...levels, ...(value && !levels.includes(value) ? [value] : [])].map((level) => ({ id: level, label: effortLabel(level) }))],
	};
	return view;
}

/**
 * The session's reviewer settings after picking a model or mode ("" goes back
 * to inheriting). A picked model is saved by name, even the catalog default,
 * so it holds over the project's choice. The session's effort is kept only
 * where the model the run will use offers it; if the inherited effort is not
 * offered either, the model's own default is named so the run gets a level it
 * can use.
 */
export function reviewerModelPick({ catalog, saved, inherited }: ReviewerModelState, id: string): ReviewerAgentConfig {
	const isMode = catalog?.selectionMode === "mode";
	const target = catalogModel(catalog, resolveModel(catalog, id, isMode ? inherited.mode : inherited.model));
	const offers = (level: string | undefined) => Boolean(level && target?.efforts?.includes(level));
	let effort = offers(saved.effort) ? saved.effort! : "";
	if (!effort && inherited.effort && target?.efforts && !offers(inherited.effort) && offers(target.defaultEffort)) effort = target.defaultEffort!;
	return { ...saved, model: isMode ? "" : id, mode: isMode ? id : "", effort };
}

/**
 * The session's reviewer settings after picking an effort ("" clears it). With
 * nothing inherited, the model's own default is saved as no override, so the
 * reviewer keeps following the model.
 */
export function reviewerEffortPick(state: ReviewerModelState, level: string): ReviewerAgentConfig {
	const effortModel = catalogModel(state.catalog, runsWith(state));
	const reported = effortModel?.defaultEffort?.trim().toLowerCase();
	const followsDefault = !state.inherited.effort && Boolean(level) && level.toLowerCase() === reported;
	return { ...state.saved, effort: followsDefault ? "" : level };
}
