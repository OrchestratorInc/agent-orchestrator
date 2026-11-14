import type { ApprovalMode, ChatConfigChoice, ChatConfigOption, ChatModel, ConversationSnapshot, TurnSettings } from "./types";
import { can } from "./types";

export type TurnSettingChoice = {
	value: string;
	label: string;
	description?: string;
	selected: boolean;
	/** The AO approval mode a permissions choice applies, when it maps to one. */
	permissionMode?: ApprovalMode;
};

export type TurnSettingRow = {
	id: string;
	label: string;
	description?: string;
	value: string;
	kind: "select" | "boolean";
	enabled?: boolean;
	/**
	 * How the provider categorised this control. Derived once, from the option
	 * itself — the summary used to re-guess it from the label and missed a Mode
	 * option called "Mode", so it showed AO's approvalMode while the sheet was
	 * editing the provider's.
	 */
	providerKind?: ProviderTurnControlKind;
	/**
	 * The choice the agent runs with when none is picked. Picking it clears the
	 * pick rather than saving it, so the conversation follows the agent's
	 * default again, even after that default changes.
	 */
	defaultValue?: string;
	choices: TurnSettingChoice[];
	target:
		| { kind: "settings"; key: keyof TurnSettings }
		| { kind: "option"; optionId: string };
};

/**
 * The value of a setting nobody has named: the provider did not report it and
 * the user has not picked one. Shown instead of guessing (#5834).
 */
export const NOT_REPORTED = "Not reported";

const USE_AGENT_PERMISSIONS = "Use agent permissions";

/** Labels for a provider's `default` choice when nothing says what it resolves to (desktop's wording, #5849). */
const FOLLOW_AGENT = {
	model: "Use agent model",
	effort: "Use agent effort",
	other: "Use agent setting",
} as const;

const APPROVALS: Array<{ value: ApprovalMode; label: string; description: string }> = [
	{ value: "default", label: USE_AGENT_PERMISSIONS, description: "The worktree is the safety boundary" },
	{ value: "accept-edits", label: "Ask outside worktree", description: "Edits here are allowed; anything else asks" },
	{ value: "auto", label: "Ask when unsure", description: "The agent decides when to check with you" },
	{ value: "bypass-permissions", label: "Never ask", description: "No approvals or sandbox prompts" },
];

/**
 * AO's own approval modes, by name. Codex's default is full access: AO runs it
 * with approval "never" and sandbox "danger-full-access" (codexappserver
 * `approvalSettings`), so it is not the agent's own permissions there. Same
 * split as desktop's CODEX_APPROVAL_COPY / APPROVAL_COPY.
 */
export function approvalLabel(mode: ApprovalMode, harness: string): string {
	if (mode === "default" && harness === "codex") return "Full access";
	return approvalModeLabel(mode);
}

function approvalModeLabel(mode: ApprovalMode): string {
	return APPROVALS.find((item) => item.value === mode)?.label ?? mode;
}

/** A native catalog's effort level by name; "default" names no level. */
export function effortChoiceLabel(effort: string): string {
	return isDefaultValue(effort) ? FOLLOW_AGENT.effort : capitalize(effort);
}

/** "Use agent model" and the like, including "Use agent model (Opus 5.5)": a choice that follows the agent. */
export function followsAgentLabel(label: string): boolean {
	return Object.values(FOLLOW_AGENT).some((follow) => label === follow || label.startsWith(`${follow} (`));
}

export type ProviderTurnControlKind = "fast" | "model" | "effort" | "permissions" | "other";

/**
 * Providers name their settings differently, so render the important four by
 * their semantic category first and fall back to a conservative name match.
 */
export function providerTurnControlKind(option: ChatConfigOption): ProviderTurnControlKind {
	const id = option.id.toLowerCase();
	const name = option.name.toLowerCase();
	if (id === "fast" || id.includes("fast") || name.includes("fast")) return "fast";
	if (option.category === "model" || id === "model" || id === "agent") return "model";
	if (option.category === "thought_level" || id === "effort" || id.includes("thought") || id.includes("reason")) return "effort";
	if (option.category === "mode" || id === "mode" || id.includes("permission") || id.includes("approval")) return "permissions";
	return "other";
}

/**
 * Providers offer a choice whose value is "default" and whose label names
 * nothing: Claude sends "Default (recommended)" for the model and "Default" for
 * effort (#5834). The wire value is kept, so picking it still follows the
 * provider when its default moves.
 *
 * Model and effort follow desktop's `resolveImplicitChoice` (TurnSettingsBar,
 * #5849): when the choice's description names another choice ("Opus 5.5"), it
 * takes that name and every copy of it goes, even while that model is the
 * explicit pick, so the menu never lists a model twice. Otherwise "Use agent
 * model" / "Use agent effort" rather than a level we would have to guess.
 *
 * Permission modes: see `namePermissionChoice`. A provider's own names stay,
 * as on desktop. Desktop names a placeholder mapped to AO's default mode "Use
 * agent permissions", drops one whose value is "default" otherwise, and shows
 * any other by its placeholder name. Here every placeholder stays, so each mode
 * the agent offers can still be picked, and is named by the mode it applies.
 */
export function resolveDefaultChoices(options: ChatConfigOption[]): ChatConfigOption[] {
	return options.map(resolveDefaultChoice);
}

function resolveDefaultChoice(option: ChatConfigOption): ChatConfigOption {
	if (option.type !== "select") return option;
	const kind = providerTurnControlKind(option);
	// Fast mode is a toggle, which reads a "default" choice as Off.
	if (kind === "fast") return option;
	if (kind === "permissions") {
		const choices = option.choices.map(namePermissionChoice);
		return choices.every((choice, index) => choice === option.choices[index]) ? option : { ...option, choices };
	}
	const resolved = resolveImplicitChoice(option);
	return isModelOption(resolved) ? withoutDuplicateNames(resolved) : resolved;
}

function resolveImplicitChoice(option: ChatConfigOption): ChatConfigOption {
	const implicit = option.choices.find((choice) => choice.value === "default");
	if (!implicit) return option;
	const named = implicit.description?.trim().toLowerCase();
	// The running model when it is one of the models so named: Claude lists a
	// model twice, by alias and by id, and the other copy goes as a duplicate.
	const namedChoices = named
		? option.choices.filter((choice) => choice !== implicit && choice.name.trim().toLowerCase() === named)
		: [];
	const concrete = namedChoices.find((choice) => choice.value === option.currentValue) ?? namedChoices[0];
	if (concrete) {
		// One row per model: the agent's own choice takes the name of every copy
		// it stands for, and is the one ticked while one of them runs. The row
		// that stays reads exactly like the ones it replaces.
		const name = isDefaultPlaceholderLabel(concrete.name) ? concrete.value : concrete.name;
		const running = namedChoices.some((choice) => choice.value === option.currentValue);
		return {
			...option,
			currentValue: running ? implicit.value : option.currentValue,
			choices: option.choices
				.filter((choice) => !namedChoices.includes(choice))
				.map((choice) => choice === implicit ? { ...choice, name, description: concrete.description } : choice),
		};
	}
	if (!isDefaultPlaceholderLabel(implicit.name)) return option;
	const follow = isModelOption(option) ? FOLLOW_AGENT.model : providerTurnControlKind(option) === "effort" ? FOLLOW_AGENT.effort : FOLLOW_AGENT.other;
	return {
		...option,
		choices: option.choices.map((choice) =>
			choice === implicit ? { ...choice, name: follow, description: undefined } : choice),
	};
}

/** A model picker. An `agent` option is classed with models elsewhere, but it picks an agent. */
function isModelOption(option: ChatConfigOption): boolean {
	return option.category === "model" || option.id.toLowerCase() === "model";
}

/**
 * One row per model name. AO hands Claude Code its catalog's full ids on top
 * of the aliases it lists by itself, so "opus" and "claude-opus-5-5" both
 * arrive as "Opus 5.5". The running one stays, else the first, which is the
 * alias: it follows the family's newest model.
 */
function withoutDuplicateNames(option: ChatConfigOption): ChatConfigOption {
	const kept = new Map<string, ChatConfigChoice>();
	for (const choice of option.choices) {
		const name = choice.name.trim().toLowerCase();
		const first = kept.get(name);
		if (!first || (choice.value === option.currentValue && first.value !== option.currentValue)) kept.set(name, choice);
	}
	if (kept.size === option.choices.length) return option;
	const keep = new Set(kept.values());
	return { ...option, choices: option.choices.filter((choice) => keep.has(choice)) };
}

/** "Default", "Default (recommended)": a label that names no model, level or mode. */
function isDefaultPlaceholderLabel(label: string): boolean {
	return /^default(?:\s*\([^)]*\))?$/i.test(label.trim());
}

/**
 * A permission choice whose name says nothing is named by the mode the daemon
 * maps it to, because picking it applies that mode: a "Default" mapped to
 * bypass-permissions reads "Never ask", not "Default" (review on #6071). An
 * unmapped one reads "Use agent permissions". "Default approvals" is what
 * daemons before #5849 call OpenCode's default tier, and a phone can be paired
 * with one. The wire value is kept.
 */
function namePermissionChoice(choice: ChatConfigChoice): ChatConfigChoice {
	const placeholder = isDefaultPlaceholderLabel(choice.name) || /^(?:default approvals|use agent permissions)$/i.test(choice.name.trim());
	if (!placeholder) return choice;
	const name = approvalModeLabel(choice.permissionMode ?? "default");
	return name === choice.name ? choice : { ...choice, name };
}

/**
 * A provider option's current value, by the name of its choice. A value no
 * choice explains is shown as sent; "default" or no value at all reads "Not
 * reported" rather than "Default" (#5834).
 */
export function providerChoiceLabel(option: ChatConfigOption): string {
	if (option.type === "boolean") return option.currentBoolean ? "On" : "Off";
	const selected = option.choices.find((choice) => choice.value === option.currentValue);
	if (selected) return selected.name;
	return sentValue(option.currentValue) ?? NOT_REPORTED;
}

/**
 * The native model row's value: `model` by name (the catalog entry picked, else
 * the one the catalog marks as the provider's default). Without one, the
 * setting as sent, except "default", which reads "Not reported". Display only:
 * the setting itself is sent unchanged.
 */
export function nativeModelLabel(model: ChatModel | undefined, setting: string | undefined): string {
	const sent = sentValue(setting);
	return model?.displayName ?? (sent ? readableModelId(sent) : undefined) ?? NOT_REPORTED;
}

/**
 * A Claude model id as its name, for when the catalog that names it is not
 * there (a stopped session reports none): "claude-opus-5-5" reads "Opus 5.5",
 * and a dated snapshot drops its date. Any other id is shown as sent.
 */
export function readableModelId(id: string): string {
	const claude = /^claude-([a-z]+)-(\d+)(?:-(\d{1,2}))?(?:-\d{8})?$/.exec(id);
	if (!claude) return id;
	const [, family, major, minor] = claude;
	return `${capitalize(family)} ${minor ? `${major}.${minor}` : major}`;
}

/** A value as sent, or undefined when there is none or it is "default", which names nothing. */
function sentValue(value: string | undefined): string | undefined {
	return value && !isDefaultValue(value) ? value : undefined;
}

/**
 * A provider `mode` option replaces the Approvals row only when it offers
 * approval choices. OpenCode advertises build/plan there — execution modes —
 * and mistaking them for approvals leaves the session with no way to bypass.
 */
function isPermissionModeOption(option: ChatConfigOption): boolean {
	if (option.category !== "mode" && option.id !== "mode") return false;
	return option.choices.some((choice) =>
		![choice.value, choice.name].some((text) => /^(?:plan|agent|build)(?:[\s_-]mode)?$/i.test(text.trim())));
}

/**
 * Whether the provider's own controls stand in for AO's. A catalog it reported
 * counts as well as the capability: a hibernated chat's snapshot reports no
 * capabilities, and the catalog remembered from an earlier visit would otherwise
 * show beside AO's Approvals row until the wake lands.
 */
export function usesProviderControls(snapshot: ConversationSnapshot, options: readonly ChatConfigOption[]): boolean {
	return can(snapshot, "config_options") || options.length > 0;
}

export function turnSettingsRows(snapshot: ConversationSnapshot, models: ChatModel[], options: ChatConfigOption[]): TurnSettingRow[] {
	const selectedModel = models.find((model) => model.id === snapshot.settings.model) ?? models.find((model) => model.default);
	const providerModel = options.some((option) => option.category === "model" || option.id === "model" || option.id === "agent");
	const providerMode = options.some(isPermissionModeOption);
	const providerControls = usesProviderControls(snapshot, options);
	const rows: TurnSettingRow[] = [];

	if ((!providerControls || !providerModel) && models.length) {
		rows.push({
			id: "model",
			label: "Model",
			value: nativeModelLabel(selectedModel, snapshot.settings.model),
			kind: "select",
			// No "Provider default" hint: with nothing picked, the provider's model
			// is the selected row already (#5834).
			choices: models.map((model) => ({
				value: model.id,
				label: model.displayName,
				description: model.description || undefined,
				selected: model.id === selectedModel?.id,
			})),
			target: { kind: "settings", key: "model" },
		});
		if (selectedModel?.efforts?.length) {
			const effort = snapshot.settings.reasoningEffort ?? selectedModel.defaultEffort;
			rows.push({
				id: "effort",
				label: "Effort",
				value: effort ? effortChoiceLabel(effort) : NOT_REPORTED,
				kind: "select",
				defaultValue: selectedModel.defaultEffort,
				choices: selectedModel.efforts.map((value) => ({ value, label: effortChoiceLabel(value), selected: value === effort })),
				target: { kind: "settings", key: "reasoningEffort" },
			});
		}
	}

	if (!providerControls || !providerMode) {
		const approval = snapshot.settings.approvalMode ?? "default";
		rows.push({
			id: "approvals",
			label: "Approvals",
			value: approvalLabel(approval, snapshot.harness),
			kind: "select",
			choices: APPROVALS.map((item) => ({
				value: item.value,
				label: approvalLabel(item.value, snapshot.harness),
				description: item.description,
				selected: item.value === approval,
				permissionMode: item.value,
			})),
			target: { kind: "settings", key: "approvalMode" },
		});
	}

	// Nothing picked: the provider's model is the agent's own, whatever it
	// reports running, so the choice that follows the agent is the one ticked.
	const followsAgentModel = !sentValue(snapshot.settings.model);
	rows.push(...options.map((option) => providerRow(option, followsAgentModel)));
	return rows.sort((a, b) => controlPriority(a) - controlPriority(b));
}

/**
 * The summary's parts, for a control that shows the approval mode apart from
 * the model. Effort is the setting people change most after the model, and it
 * was the one this line never mentioned. A value that names nothing is "":
 * saying it spends the row's width on nothing (#5834).
 */
export function turnSettingsParts(snapshot: ConversationSnapshot, models: ChatModel[], options: ChatConfigOption[]): { model: string; effort: string; approval: string } {
	const rows = turnSettingsRows(snapshot, models, options);
	const effort = rows.find(isEffortRow);
	const permissions = rows.find(isPermissionRow);
	return {
		model: modelLabel(rows, snapshot, models),
		effort: named(effort ? effort.value : capitalize(snapshot.settings.reasoningEffort ?? "")),
		approval: named(permissions?.value ?? approvalLabel(snapshot.settings.approvalMode ?? "default", snapshot.harness)),
	};
}

function modelLabel(rows: TurnSettingRow[], snapshot: ConversationSnapshot, models: ChatModel[]): string {
	// Names the model that answered when the provider swapped in another, as the
	// desktop's picker does. A reroute recorded against an earlier pick no longer applies.
	const reroute = snapshot.modelReroute;
	if (reroute?.toModel && (!reroute.fromModel || reroute.fromModel === snapshot.settings.model)) {
		return models.find((model) => model.id === reroute.toModel)?.displayName ?? readableModelId(reroute.toModel);
	}
	const model = rows.find(isModelRow);
	return named(model ? model.value : readableModelId(snapshot.settings.model ?? ""));
}

/** The value, or "" when it names nothing: unreported, a bare "default", or "Use agent …". */
function named(value: string): string {
	return value === NOT_REPORTED || isDefaultValue(value) || followsAgentLabel(value) ? "" : value;
}

function isDefaultValue(value: string): boolean {
	return value.trim().toLowerCase() === "default";
}

function isModelRow(row: TurnSettingRow): boolean {
	if (row.target.kind === "settings") return row.target.key === "model";
	return row.providerKind === "model";
}

function isEffortRow(row: TurnSettingRow): boolean {
	if (row.target.kind === "settings") return row.target.key === "reasoningEffort";
	return row.providerKind === "effort";
}

function isPermissionRow(row: TurnSettingRow): boolean {
	if (row.target.kind === "settings") return row.target.key === "approvalMode";
	return row.providerKind === "permissions";
}

/**
 * `followsAgentModel`: no model was picked in AO (the daemon saves every pick),
 * so a model row ticks its "Use agent model" choice even while the provider
 * reports the model that resolves to. Claude Code reports "opus" for a session
 * nobody pointed at Opus when its own settings name it.
 */
function providerRow(option: ChatConfigOption, followsAgentModel: boolean): TurnSettingRow {
	const model = isModelOption(option);
	const follows = followsAgentModel && model && option.choices.some((choice) => choice.value === "default");
	return {
		id: `option:${option.id}`,
		label: option.name,
		description: option.description,
		value: providerChoiceLabel(option),
		kind: option.type,
		enabled: Boolean(option.currentBoolean),
		choices: option.choices.map((choice) => ({
			value: choice.value,
			label: model ? modelChoiceLabel(choice) : choice.name,
			description: choice.description,
			selected: follows ? choice.value === "default" : choice.value === option.currentValue,
			permissionMode: choice.permissionMode,
		})),
		target: { kind: "option", optionId: option.id },
		providerKind: providerTurnControlKind(option),
	};
}

/**
 * A model menu's row. The agent's own choice stands in for the model it names
 * (see `resolveDefaultChoices`) and is tagged as the default, so following the
 * agent reads apart from a pinned model; the capsule shows the name alone.
 */
function modelChoiceLabel(choice: ChatConfigChoice): string {
	return choice.value === "default" && !followsAgentLabel(choice.name) ? `${choice.name} · Default` : choice.name;
}

function controlPriority(row: TurnSettingRow): number {
	if (row.target.kind === "option") {
		return { fast: 0, model: 1, effort: 2, permissions: 3, other: 10 }[row.providerKind ?? "other"];
	}
	const key = row.id;
	if (key === "model") return 1;
	if (key === "effort") return 2;
	if (key === "approvals") return 4;
	return 10;
}

/** What a turn-settings row adjusts, for the glyph the menus draw beside it. */
export type TurnSettingTopic = "model" | "effort" | "fast" | "permissions" | "setting";

/**
 * The row's topic, or none for an agent picker: providers class an `agent`
 * option with models, but no glyph says "agent" without saying "model".
 */
export function turnSettingTopic(row: TurnSettingRow): TurnSettingTopic | undefined {
	const id = row.id.toLowerCase();
	if (id.includes("agent")) return undefined;
	if (row.providerKind === "model" || id.includes("model")) return "model";
	if (row.providerKind === "permissions" || id.includes("mode") || id.includes("approval")) return "permissions";
	if (id.includes("effort") || id.includes("thought") || id.includes("reason")) return "effort";
	if (id.includes("fast")) return "fast";
	return "setting";
}

/** What picking in a row saves: a provider option by its id, or AO's own settings. */
export type TurnSettingWrite =
	| { kind: "option"; id: string; value: { value: string } | { enabled: boolean } }
	| { kind: "settings"; settings: TurnSettings };

/** The write for a row's choice: a select's value, or a boolean row's new state. */
export function turnSettingWrite(settings: TurnSettings, row: TurnSettingRow, choice: string | boolean): TurnSettingWrite {
	if (row.target.kind === "option") {
		return { kind: "option", id: row.target.optionId, value: typeof choice === "boolean" ? { enabled: choice } : { value: choice } };
	}
	return { kind: "settings", settings: applyTurnSettingChoice(settings, row, String(choice)) };
}

function applyTurnSettingChoice(settings: TurnSettings, row: TurnSettingRow, value: string): TurnSettings {
	if (row.target.kind !== "settings") return settings;
	if (row.target.key === "model") return { ...settings, model: value, reasoningEffort: undefined };
	if (row.defaultValue !== undefined && value === row.defaultValue) return { ...settings, [row.target.key]: undefined };
	return { ...settings, [row.target.key]: value } as TurnSettings;
}

/** A model menu shorter than this is listed whole: there is nothing to tuck away. */
const SPLIT_MODEL_MENU_OVER = 6;

/**
 * A model menu's choices in two parts: the newest of each family first, in the
 * provider's order, and the rest, older versions, to open one level down.
 * Providers list their newest model of a family before its older ones (Claude:
 * "Opus 5.5" before "Opus 4.8"), so the first of each family by the name's
 * first word is its newest. The running model stays on top wherever it is
 * listed. A short menu, or one with no older versions, is left whole.
 */
export function splitModelChoices(choices: readonly TurnSettingChoice[]): { latest: TurnSettingChoice[]; older: TurnSettingChoice[] } {
	if (choices.length <= SPLIT_MODEL_MENU_OVER) return { latest: [...choices], older: [] };
	const families = new Set<string>();
	const latest: TurnSettingChoice[] = [];
	const older: TurnSettingChoice[] = [];
	for (const choice of choices) {
		const family = choice.label.trim().split(/\s+/)[0].toLowerCase();
		if (!families.has(family) || choice.selected) latest.push(choice);
		else older.push(choice);
		families.add(family);
	}
	return older.length ? { latest, older } : { latest: [...choices], older: [] };
}

function capitalize(value: string): string {
	return value ? value[0].toUpperCase() + value.slice(1) : value;
}
