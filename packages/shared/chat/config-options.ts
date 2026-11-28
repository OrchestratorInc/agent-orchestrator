import { agentModelDisplayLabel, claudeChoiceModel, isDefaultPlaceholderLabel } from "../models/agent-model-choices";
import { providerQualifiedChoice } from "../models/model-variants";

/*
 * How AO reads the settings an agent advertises for a live chat (ACP session
 * config options): which is the model, the effort, Fast mode, a plan mode, the
 * permission modes, and what is left over. One copy, shared by the desktop's
 * turn settings bar and the mobile composer, so the two never read the same
 * catalog two ways.
 */

/** One value offered by a select option. */
export interface ConfigChoice {
	value: string;
	name: string;
	description?: string;
	/** Provider grouping is presentation data, not part of the value sent back. */
	group?: string;
	groupName?: string;
	/** The AO approval mode the daemon maps this choice to, when it maps one. */
	permissionMode?: string;
	/** Why the choice cannot be picked now (AO adds it from the agent's catalog). */
	unavailableReason?: string;
	/** A quiet note AO shows beside the name (AO adds it; the provider never sends it). */
	hint?: string;
}

/** One provider-owned setting advertised for a live chat session. */
export interface ConfigOption {
	id: string;
	name: string;
	description?: string;
	/** ACP's semantic category, such as model, thought_level, or mode. */
	category?: string;
	type: "select" | "boolean";
	currentValue?: string;
	currentBoolean?: boolean;
	choices: ConfigChoice[];
}

/** The two value shapes ACP session config supports. */
export type ConfigOptionValue = { value: string } | { enabled: boolean };

type ChoiceOf<O extends ConfigOption> = O["choices"][number];

export function isModelOption(option: ConfigOption): boolean {
	return option.category === "model" || option.id === "model";
}

/**
 * ACP may advertise an `agent` option for its own multi-agent internals. It is
 * not AO's harness switcher and is not a model choice, so exposing it in the
 * composer promises a meaning AO cannot guarantee. Harness changes remain in
 * the dedicated session switcher.
 */
export function isAgentOption(option: ConfigOption): boolean {
	return option.id === "agent" || option.category === "agent";
}

export function isEffortOption(option: ConfigOption): boolean {
	return option.category === "thought_level" || option.id === "effort";
}

export function isModeOption(option: ConfigOption): boolean {
	return option.category === "mode" || option.id === "mode";
}

export function isFastOption(option: ConfigOption): boolean {
	return /(?:^|[\s_-])fast(?:[\s_-]|$)/i.test(`${option.id} ${option.name}`);
}

/** Cursor reports thinking as an on/off choice among its effort settings. */
export function isThinkingToggle(option: ConfigOption): boolean {
	return option.id === "thinking" && option.choices.length === 2 && option.choices.some(choiceIsEnabled);
}

/**
 * A select that reads as an on/off: some choice is on and some is off
 * (Claude Code's Fast mode as On/Off, Cursor's as true/false).
 */
export function isOnOffSelect(option: ConfigOption): boolean {
	return option.type === "select" && option.choices.some(choiceIsEnabled) && option.choices.some((choice) => !choiceIsEnabled(choice));
}

/**
 * An on/off setting: a boolean, Cursor's thinking, or Fast mode. A select that
 * merely mentions "fast" (a "Fast model" listing models) is no on/off: turning
 * it "on" would pick whichever choice came first.
 */
export function isInlineToggleOption(option: ConfigOption): boolean {
	return option.type === "boolean" || isThinkingToggle(option) || (isFastOption(option) && isOnOffSelect(option));
}

export function optionIsEnabled(option: ConfigOption): boolean {
	if (option.type === "boolean") return Boolean(option.currentBoolean);
	return choiceIsEnabled(option.choices.find((choice) => choice.value === option.currentValue));
}

export function choiceIsEnabled(choice: ConfigChoice | undefined): boolean {
	return Boolean(choice && /(?:^|[\s_-])(on|enabled|true)(?:[\s_-]|$)/i.test(`${choice.name} ${choice.value}`));
}

/**
 * What turning an on/off setting on or off sends: a boolean option's state, or
 * the choice of a select that reads as on or off (Fast mode as On/Off). None
 * when the select has no such choice.
 */
export function toggleValue(option: ConfigOption, enabled: boolean): ConfigOptionValue | undefined {
	if (option.type === "boolean") return { enabled };
	const next = option.choices.find((choice) => choiceIsEnabled(choice) === enabled);
	return next ? { value: next.value } : undefined;
}

/**
 * Whether a provider catalog replaces AO's own approval control. A `mode` option
 * that offers only execution modes (OpenCode's build/plan) is not one: taking it
 * for an approval catalog leaves the session with no permission control at all.
 */
export function hasProviderPermissionMode(options: ConfigOption[]): boolean {
	return Boolean(partitionConfigOptions(options).mode);
}

export function partitionConfigOptions<O extends ConfigOption>(options: O[], includeExecutionModes = true): {
	model: O[];
	effort: O[];
	/** Provider plan/agent modes, placed with model selection rather than permissions. */
	executionMode: O | undefined;
	/** Compact boolean controls, such as Fast Mode, rendered beside Plan Mode. */
	toggles: O[];
	mode: O | undefined;
	extra: O[];
} {
	const primaryModel: O[] = [];
	const otherModel: O[] = [];
	const effort: O[] = [];
	const toggles: O[] = [];
	const extra: O[] = [];
	let executionMode: O | undefined;
	let mode: O | undefined;
	for (const rawOption of options) {
		const option = rawOption.type === "select"
			? isEffortOption(rawOption)
				? rawOption
				: resolveImplicitChoice(rawOption)
			: rawOption;
		if (option.type === "select" && option.choices.length === 0 && !isEffortOption(option)) continue;
		if (isAgentOption(option)) continue;
		if (isModelOption(option)) {
			if (option.category === "model" || option.id === "model") primaryModel.push(option);
			else otherModel.push(option);
			continue;
		}
		if (isEffortOption(option) && !isThinkingToggle(option)) {
			if (!option.choices.some((choice) => choice.value && choice.value !== "default")) continue;
			effort.push(option);
			continue;
		}
		if (isInlineToggleOption(option)) {
			toggles.push(option);
			continue;
		}
		if (isModeOption(option) && !mode) {
			// Classify before synthesizing Claude's Agent choice so it cannot promote approval choices.
			const executionValues = executionChoiceValues(option.choices);
			const permissionChoices = option.choices.filter((choice) => !executionValues.has(choice.value));
			const executionChoices = includeExecutionModes ? addAgentModeChoice(
				option.choices.filter((choice) => executionValues.has(choice.value)),
				permissionChoices,
			) : [];
			if (executionChoices.length > 0) {
				executionMode = withChoices(option, executionChoices);
				if (permissionChoices.length > 0) mode = withChoices(option, permissionChoices);
			} else {
				mode = option;
			}
			continue;
		}
		extra.push(option);
	}
	return { model: [...primaryModel, ...otherModel], effort, executionMode, toggles, mode, extra };
}

export function resolveImplicitChoice<O extends ConfigOption>(option: O): O {
	const mapped: O = isModeOption(option) ? {
		...option,
		choices: option.choices.map((choice) =>
			choice.permissionMode === "default" && isDefaultPlaceholderLabel(choice.name)
				? { ...choice, name: "Use agent permissions" }
				: choice,
		),
	} : option;
	// A mode named Default is the agent's own beside its other own modes (Kimi's Default, Plan,
	// Auto, YOLO); it means "the agent decides" only alone or beside modes matched to AO's.
	const placeholderMode = (choice: ChoiceOf<O>) =>
		choice.permissionMode !== "default" &&
		(isDefaultPlaceholderLabel(choice.name) || /^use agent permissions$/i.test(choice.name.trim())) &&
		(mapped.choices.length === 1 || mapped.choices.some((other) => other !== choice && other.permissionMode));
	const implicit = mapped.choices.find((choice) =>
		choice.value === "default" && (!isModeOption(mapped) || placeholderMode(choice)),
	);
	if (!implicit) return mapped;
	if (isModeOption(mapped)) return { ...mapped, choices: mapped.choices.filter((choice) => choice !== implicit) };
	const concrete = mapped.choices.find((choice) =>
		choice.value !== implicit.value && choice.name.toLowerCase() === implicit.description?.trim().toLowerCase(),
	);
	const followLabel = isModelOption(mapped) ? "Use agent model" : "Use agent setting";
	if (concrete && mapped.currentValue !== concrete.value) {
		const label = isDefaultPlaceholderLabel(concrete.name) ? concrete.value : concrete.name;
		// The row stands for the hidden model too, so a model remembered under its id still finds it.
		const aliases = [...((implicit as typeof implicit & { aliases?: string[] }).aliases ?? []), concrete.value];
		return {
			...mapped,
			choices: mapped.choices.filter((choice) => choice !== concrete).map((choice) =>
				choice === implicit ? { ...choice, name: label, aliases } : choice,
			),
		};
	}
	// A model picker lists models only; the agent's own choice is not a row of its own.
	if (isModelOption(mapped) && mapped.currentValue !== implicit.value) {
		return { ...mapped, choices: mapped.choices.filter((choice) => choice !== implicit) };
	}
	return {
		...mapped,
		choices: mapped.choices.map((choice) => choice === implicit && isDefaultPlaceholderLabel(choice.name)
			? { ...choice, name: concrete ? `${followLabel} (${concrete.name})` : followLabel }
			: choice),
	};
}

/** Ask is an execution mode only when the same option advertises Agent; otherwise it is an approval policy. */
export function executionChoiceValues(choices: ConfigChoice[]): Set<string> {
	const values = new Set<string>();
	const hasAgent = choices.some((choice) => executionChoiceMatches(choice, "agent"));
	for (const choice of choices) {
		if (executionChoiceMatches(choice, "plan|agent|build")) {
			values.add(choice.value);
			continue;
		}
		if (hasAgent && executionChoiceMatches(choice, "ask")) values.add(choice.value);
	}
	return values;
}

// Match complete mode labels so custom agent names cannot masquerade as native modes.
function executionChoiceMatches(choice: ConfigChoice, word: string): boolean {
	return [choice.value, choice.name].some((value) =>
		new RegExp(`^(?:${word})(?:[\\s_-]mode)?$`, "i").test(value.trim()),
	);
}

function choiceMatches(choice: Pick<ConfigChoice, "name" | "value">, word: string): boolean {
	return new RegExp(`(?:^|[\\s_-])(?:${word})(?:[\\s_-]|$)`, "i").test(`${choice.name} ${choice.value}`);
}

/**
 * Claude currently sends Plan as an execution mode but describes its ordinary
 * interactive posture as Manual. Present the latter as Agent Mode next to Plan,
 * without inventing a value: choosing it still sends the provider's own Manual
 * value. Other harnesses that advertise Agent Mode explicitly stay untouched.
 */
function addAgentModeChoice<C extends ConfigChoice>(executionChoices: C[], permissionChoices: C[]): C[] {
	// OpenCode names its ordinary agent mode "build", so its catalog already has
	// one and must not gain a synthetic duplicate. With no execution mode to leave
	// for, an Agent choice alone would be a picker with nothing to pick.
	if (executionChoices.length === 0 || executionChoices.some((choice) => executionChoiceMatches(choice, "agent|build"))) {
		return executionChoices;
	}
	const standard = permissionChoices.find((choice) => choiceMatches(choice, "manual"))
		?? permissionChoices.find((choice) => choiceMatches(choice, "default|standard"));
	return standard
		? [{ ...standard, name: "Agent Mode", description: "Standard agent execution" }, ...executionChoices]
		: executionChoices;
}

export function isPlanBinary(option: ConfigOption): boolean {
	return option.choices.length === 2 && option.choices.some(isPlanChoice);
}

export function executionModeLabel(option: ConfigOption): string {
	if (isPlanBinary(option)) return isPlanMode(option) ? "Plan Mode" : "Agent Mode";
	return option.choices.find((choice) => choice.value === option.currentValue)?.name ?? option.name;
}

export function isPlanMode(option: ConfigOption | undefined): boolean {
	return Boolean(option?.currentValue && isPlanChoice({ value: option.currentValue, name: option.currentValue }));
}

export function isPlanChoice(choice: Pick<ConfigChoice, "name" | "value">): boolean {
	// Custom agent IDs can contain "plan" without enabling native planning.
	return [choice.value, choice.name].some((value) => /^plan(?:[\s_-]mode)?$/i.test(value.trim()));
}

function withChoices<O extends ConfigOption>(option: O, choices: ChoiceOf<O>[], name = option.name): O {
	const currentValue = choices.some((choice) => choice.value === option.currentValue)
		? option.currentValue
		: undefined;
	return { ...option, name, currentValue, choices };
}

export function optionCurrentLabel(option: ConfigOption): string {
	if (option.type === "boolean") return option.currentBoolean ? "On" : "Off";
	if (option.currentValue === "default" && !option.choices.some((choice) => choice.value === "default")) {
		if (isEffortOption(option)) return "Effort not reported";
		if (isModelOption(option)) return "Model not reported";
		return "Selection not reported";
	}
	return option.choices.find((choice) => choice.value === option.currentValue)?.name
		?? option.currentValue
		?? option.name;
}

export function dedupeChoicesByName<T extends { value: string; name: string; group?: string; unavailableReason?: string }>(choices: T[], current: string | undefined): (T & { aliases?: string[] })[] {
	return choices.flatMap((choice) => {
		// One model from two providers (Pi's openai and openai-codex) stays two rows.
		// So do an alias the CLI can run and a concrete id it is too old for: folding
		// them would leave only one row, enabled or disabled for both.
		const twins = choices.filter((other) => other.name === choice.name && other.group === choice.group
			&& Boolean(other.unavailableReason) === Boolean(choice.unavailableReason));
		const kept = twins.find((other) => other.value === current) ?? twins[0];
		if (choice !== kept) return [];
		// The values folded into this one, so a model remembered under any of them is still found.
		const aliases = twins.filter((other) => other !== choice).map((other) => other.value);
		return [aliases.length ? { ...choice, aliases } : choice];
	});
}

/** The option Antigravity's synthesized effort control is sent as: its model option. */
export const AGY_MODEL_EFFORT = "ao-agy-model-effort";

/**
 * Antigravity encodes reasoning levels in model IDs. Present the same model /
 * effort menu as Codex while sending only the original, advertised model IDs.
 * `approvalLabel` names a mode the daemon maps a choice to.
 */
export function agyComposerOptions<O extends ConfigOption>(options: O[], approvalLabel: (mode: string) => string): O[] {
	const model = options.find(isModelOption);
	const filtered = options.filter((option) =>
		!isAgentOption(option) && (option.id === "mode" || (
			!isModeOption(option) &&
			!/^session[\s_-]*mode$/i.test(option.id) &&
			!/^session mode$/i.test(option.name) &&
			!/^agent(?:[\s_-]*mode)?$/i.test(option.name)
		)),
	).map((option) => option.id === "mode" ? {
		...option,
		choices: option.choices.filter((choice) => !executionChoiceValues(option.choices).has(choice.value)).map((choice) => ({
			...choice,
			name: choice.permissionMode ? approvalLabel(choice.permissionMode)
				: choice.value === "default" ? "Ask for approval" : choice.name,
		})),
	} : option);
	if (!model || options.some(isEffortOption)) return filtered;
	const variants = model.choices.map((choice) => {
		const match = /^(.*)-(minimal|low|medium|high|xhigh)$/i.exec(choice.value);
		return { choice, base: match?.[1] ?? choice.value, effort: match?.[2]?.toLowerCase() };
	});
	const selected = variants.find(({ choice }) => choice.value === model.currentValue);
	if (!selected?.effort) return filtered;
	const families = [...new Set(variants.map(({ base }) => base))];
	const models = families.map((base) => {
		const family = variants.filter((variant) => variant.base === base);
		const representative = family.find((variant) => variant.effort === selected.effort) ?? family[0];
		return {
			...representative.choice,
			name: representative.effort
				? representative.choice.name.replace(/\s*\((?:minimal|low|medium|high|xhigh)\)\s*$/i, "")
				: representative.choice.name,
		};
	});
	return [
		...filtered.map((option) => option === model ? { ...option, choices: models } : option),
		{
			id: AGY_MODEL_EFFORT,
			name: "Reasoning effort",
			category: "thought_level",
			type: "select",
			currentValue: model.currentValue,
			choices: variants.filter(({ base }) => base === selected.base).map(({ choice, effort }) => ({
				value: choice.value,
				name: effort ? effort[0].toUpperCase() + effort.slice(1) : choice.name,
			})),
		} as unknown as O,
	];
}

/**
 * The provider's model option offers family aliases ("opus") and bare ids. The new-task
 * picker names them by version, so the chat does too.
 */
export function claudeChoiceLabels(catalog: { id: string; displayName: string }[]) {
	const choiceModel = claudeChoiceModel(catalog.map((model) => ({ id: model.id, label: model.displayName })));
	return choiceModel && ((value: string, name?: string) => choiceModel(value, name)?.label);
}

/**
 * The options as a composer shows them: Antigravity's effort drawn out of its
 * model ids, and every model choice named as the agent's catalog names it
 * ("Opus 5.5", not "Claude Opus 5.5" or "opus"), under its provider, listed
 * once. `catalog` is the agent's model catalog, already named for display.
 */
export function displayConfigOptions<O extends ConfigOption>(
	options: O[],
	harness: string | undefined,
	catalog: { id: string; displayName: string }[],
	approvalLabel: (mode: string) => string,
): O[] {
	const claudeLabels = harness === "claude-code" ? claudeChoiceLabels(catalog) : undefined;
	return (harness === "agy" ? agyComposerOptions(options, approvalLabel) : options).map((option) => isModelOption(option) ? {
		...option,
		// Claude Code can list one model twice (alias and start id); it shows once.
		choices: dedupeChoicesByName(option.choices.map((choice) => providerQualifiedChoice(choice, harness ?? "")).map((choice) => ({
			...choice,
			name: claudeLabels?.(choice.value, choice.name) ?? agentModelDisplayLabel(harness, choice.name),
			description: choice.description
				? agentModelDisplayLabel(harness, choice.description)
				: choice.description,
		})), option.currentValue),
	} : option);
}
