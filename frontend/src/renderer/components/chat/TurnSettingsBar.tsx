/**
 * What the next turn will be sent with: model, reasoning effort, approval mode.
 *
 * All three are per-turn on the provider's side, so choosing one changes the next
 * message and never restarts the agent — the running turn keeps what it was
 * dispatched with. That is why this sits in the composer rather than in settings.
 *
 * The catalog comes from the provider, not from a list in AO. Models are added,
 * renamed, hidden per account and gated by entitlement AO cannot see, so a
 * hardcoded list would be wrong within a week. An agent whose provider cannot
 * enumerate models reports none and the model control hides itself.
 *
 * ACP agents advertise those same dimensions as live session options. They share
 * this chrome rather than each growing a row of pickers: model and thought level
 * club into the left-hand control, while a provider-owned mode (such as planning)
 * stays separate from AO's approval policy. The lists inside are still the
 * provider's; only the grouping of the triggers is AO's.
 */

import { Fragment, useMemo, useState, type FocusEvent, type ReactNode } from "react";
import { Check, ChevronDown, Hand, ListTodo, PencilLine, ShieldAlert, ShieldCheck, Shuffle, SlidersHorizontal, TriangleAlert, Zap, type LucideIcon } from "lucide-react";
import { useTranslation } from "react-i18next";
import {
	OptionMenu,
	OptionMenuContent,
	OptionMenuItem,
	OptionMenuLabel,
	OptionMenuSub,
	OptionMenuSubContent,
	OptionMenuSubTrigger,
	OptionMenuTrigger,
} from "../ui/option-menu";
import { fallbackEffort, useApplyEffortDefault } from "../../lib/effort";
import { cn } from "../../lib/utils";
import { effortDisplayLabel, EffortMenuItems, EffortPicker, formatEffortLabel } from "../settings/EffortPicker";
import { agentModelDisplayLabel, claudeChoiceModel, isDefaultPlaceholderLabel, splitClaudeModels } from "../../lib/agent-model-choices";
import { Switch } from "../ui/switch";
import { agentLabel } from "../../lib/agent-options";
import { AgentModelPicker, AutoCompactRow, compactsEarly, ToggleRow, type AgentSwitchProps, type PickerSource } from "./AgentSwitchPicker";
import { ModelMenuChoices } from "./ModelMenuChoices";
import { providerQualifiedChoice } from "../../lib/model-variants";
import type {
	ApprovalMode,
	ChatConfigOption,
	ChatConfigOptionValue,
	ChatModel,
	ModelReroute,
	TurnSettings,
} from "../../types/conversation";

/** AO's generic approval modes, used by harnesses without a native vocabulary. */
const APPROVAL_COPY: Record<ApprovalMode, { label: string; description: string }> = {
	default: { label: "Use agent permissions", description: "Follows the agent's own permission settings" },
	"accept-edits": { label: "Accept edits", description: "File edits apply without asking" },
	auto: { label: "Auto-approve", description: "Approves routine actions and asks about risky ones" },
	"bypass-permissions": { label: "Bypass permissions", description: "Never asks before acting" },
};

const APPROVAL_ORDER: ApprovalMode[] = [
	"default",
	"accept-edits",
	"auto",
	"bypass-permissions",
];

const AGY_APPROVAL_COPY: Record<ApprovalMode, { label: string; description?: string }> = {
	...APPROVAL_COPY,
	default: { label: "Ask for approval" },
	"bypass-permissions": { label: "Auto-approve" },
};

// Codex has three distinct permission profiles. Its default is already full
// access in AO's isolated worktree posture, so expose it as that rather than a
// fourth, ambiguous "default" option.
const CODEX_APPROVAL_COPY: Record<ApprovalMode, { label: string; description: string }> = {
	default: { label: "Full access", description: "Edits files and runs commands without asking" },
	"accept-edits": { label: "Ask for approval", description: "Asks before edits and commands that need it" },
	auto: { label: "Approve for me", description: "Only asks about actions detected as potentially unsafe" },
	"bypass-permissions": { label: "Bypass permissions", description: "Never asks before acting" },
};

const CODEX_APPROVAL_ORDER: ApprovalMode[] = [
	"default",
	"accept-edits",
	"auto",
	"bypass-permissions",
];

const TRIGGER_CLASS =
	"h-7 gap-1 bg-transparent rounded-lg px-3 text-xs! leading-none text-muted-foreground hover:bg-interactive-active hover:text-foreground data-[state=open]:bg-interactive-active data-[state=open]:text-foreground";
const CHAT_MENU_CLASS = "chat-settings-menu text-xs!";

export function TurnSettingsBar({
	models,
	settings,
	harness,
	showApprovalMode = true,
	approvalModes,
	reroute,
	onChange,
	onRememberPermissions,
	rememberPermissionsPending,
	rememberPermissionsError,
	rememberedPermissionMode,
	configOptions,
	onChangeConfigOption,
	configPending,
	error,
	disabled,
	autoSelectEffortOnOpen = false,
	agentSwitch,
	beforePicker,
	modelsLoading,
	lastModel,
	children,
}: {
	models: ChatModel[];
	settings: TurnSettings;
	/** The active provider selects its own supported permission vocabulary. */
	harness?: string;
	/** Cloud permissions are fixed for the session, not a per-turn setting. */
	showApprovalMode?: boolean;
	approvalModes?: ApprovalMode[];
	/**
	 * The provider answered with a different model than the one chosen. Separate from
	 * `settings` all the way down: settings are what the user asked for, this is what
	 * replied, and folding them together is how the control ends up advertising a
	 * model that is not the one producing the answers.
	 */
	reroute?: ModelReroute;
	/** `picked` is set when the user chose that setting from its own menu. */
	onChange?: (next: TurnSettings, picked?: keyof TurnSettings) => void;
	/** `null` forgets the project's remembered mode. */
	onRememberPermissions?: (mode: ApprovalMode | null) => Promise<unknown> | void;
	rememberPermissionsPending?: boolean;
	rememberPermissionsError?: string;
	rememberedPermissionMode?: ApprovalMode;
	/** Controls advertised by an ACP agent for this exact live session. */
	configOptions?: ChatConfigOption[];
	onChangeConfigOption?: (
		optionId: string,
		value: ChatConfigOptionValue,
	) => Promise<unknown> | void;
	/** Prevent overlapping writes because provider responses replace the catalog. */
	configPending?: boolean;
	/**
	 * Pick an effort as soon as this opens when the model reports levels but no
	 * default. Only for a conversation with no turns yet, so reopening an existing
	 * one never changes the effort it was running with. Choosing another model
	 * always selects one regardless.
	 */
	autoSelectEffortOnOpen?: boolean;
	error?: string;
	disabled?: boolean;
	/** Turns the model control into one picker for agent, model and effort, laid out before the permissions. */
	agentSwitch?: AgentSwitchProps;
	/** Drawn just before that picker, such as the context meter. */
	beforePicker?: ReactNode;
	/** The agent is starting again; the picker names no model until it reports its own. */
	modelsLoading?: boolean;
	/** The model the chat's agent last reported running, when none is saved. */
	lastModel?: string;
	/** Inline controls on the right model row, before the mode/approval picker — queue vs steer. */
	children?: ReactNode;
}) {
	const { t } = useTranslation();
	const displayModels = useMemo(
		() => models.map((model) => ({
			...model,
			displayName: agentModelDisplayLabel(harness, model.displayName),
		})),
		[harness, models],
	);
	const claudeLabels = useMemo(() => (harness === "claude-code" ? claudeChoiceLabels(displayModels) : undefined), [harness, displayModels]);
	const displayConfigOptions = useMemo(
		() => (harness === "agy" ? agyComposerOptions(configOptions ?? []) : configOptions ?? []).map((option) => isModelOption(option) ? {
			...option,
			// Claude Code can list one model twice (alias and start id); it shows once.
			choices: dedupeChoicesByName(option.choices.map((choice) => providerQualifiedChoice(choice, harness ?? "")).map((choice) => ({
				...choice,
				name: claudeLabels?.(choice.value, choice.name) ?? agentModelDisplayLabel(harness, choice.name),
				description: choice.description
					? agentModelDisplayLabel(harness, choice.description)
					: choice.description,
			})), option.currentValue),
		} : option),
		[claudeLabels, configOptions, harness],
	);
	const selected = displayModels.find((model) => model.id === settings.model);
	const fallback = settings.model ? undefined : displayModels.find((model) => model.default);
	// A catalog miss must not relabel an explicit choice or borrow another model's
	// effort settings. Custom or newly available models may not be listed yet.
	const chosenLabel =
		selected?.displayName ?? settings.model ?? fallback?.displayName ?? "Choose a model";
	const rerouted = reroute
		? displayModels.find((model) => model.id === reroute.toModel)?.displayName ?? reroute.toModel
		: undefined;
	const modelLabel = rerouted ?? chosenLabel;
	const availableEfforts = (selected ?? fallback)?.efforts ?? [];
	const efforts = availableEfforts.filter((effort) => effort.toLowerCase() !== "default");
	const selectedEffort =
		settings.reasoningEffort && settings.reasoningEffort !== "default"
			? settings.reasoningEffort
			: (selected ?? fallback)?.defaultEffort;
	const effortLabel = selectedEffort === "default" ? undefined : selectedEffort;
	const approvalCopy = harness === "codex" ? CODEX_APPROVAL_COPY : harness === "agy" ? AGY_APPROVAL_COPY : APPROVAL_COPY;
	const approvalOrder = harness === "codex" ? CODEX_APPROVAL_ORDER : harness === "agy" ? APPROVAL_ORDER.filter((mode) => mode !== "bypass-permissions") : APPROVAL_ORDER;
	const approvalLabel = approvalCopy[settings.approvalMode ?? "default"].label;
	const modelGroupLabel = effortLabel
		? `${modelLabel} ${formatEffortLabel(effortLabel, t)}`
		: modelLabel;
	const grouped = partitionConfigOptions(displayConfigOptions, harness !== "agy");
	const optionDisabled = Boolean(disabled || configPending || rememberPermissionsPending);
	const applyOption = (optionId: string, value: ChatConfigOptionValue) => {
		if (!onChangeConfigOption) return;
		const providerId = harness === "agy" && optionId === AGY_MODEL_EFFORT
			? configOptions?.find(isModelOption)?.id ?? "model"
			: optionId;
		void Promise.resolve(onChangeConfigOption(providerId, value)).catch(() => {});
	};
	const nativeModelMenu = Boolean(onChange && displayModels.length > 0 && grouped.model.length === 0);
	// A model with effort levels but no provider default would otherwise show no
	// selected effort. AO picks a level and sets it, so what the picker shows is
	// what the next turn uses.
	const acpEffortOption = grouped.effort.at(0);
	useApplyEffortDefault(
		`${harness}:${grouped.model.map((option) => option.currentValue).join(":")}`,
		acpEffortOption ? acpFallbackEffort(acpEffortOption) : undefined,
		(value) => {
			if (acpEffortOption) applyOption(acpEffortOption.id, { value });
		},
		{ disabled: optionDisabled || !onChangeConfigOption, applyOnMount: autoSelectEffortOnOpen },
	);
	const nativeEffortUnset = !settings.reasoningEffort || settings.reasoningEffort === "default";
	useApplyEffortDefault(
		`${harness}:${settings.model ?? fallback?.id}`,
		// Only for a model this agent lists: just after a switch the settings can still be
		// the previous agent's, and saving an effort would carry its model onto the new one.
		nativeModelMenu && nativeEffortUnset && (selected ?? fallback) ? fallbackEffort(efforts, (selected ?? fallback)?.defaultEffort) : undefined,
		(value) => onChange?.({ ...settings, reasoningEffort: value }),
		{ disabled: optionDisabled || !onChange, applyOnMount: autoSelectEffortOnOpen },
	);
	const modeOption = grouped.mode;
	const inlineExecutionMode =
		grouped.executionMode && isPlanBinary(grouped.executionMode) ? grouped.executionMode : undefined;
	const standaloneExecutionMode =
		grouped.executionMode && !isPlanBinary(grouped.executionMode) ? grouped.executionMode : undefined;
	const planning = isPlanMode(grouped.executionMode);
	// Leaving Plan returns to the approval mode the session was on, not to the
	// provider's bare agent mode, which would silently drop the policy while the
	// picker still displayed it.
	const planReturn = modeOption?.choices.find(
		(choice) => choice.permissionMode === (settings.approvalMode ?? "default"),
	)?.value;
	const clubbedLeft =
		grouped.model.length > 0 ||
		grouped.effort.length > 0 ||
		Boolean(inlineExecutionMode) ||
		grouped.toggles.length > 0 ||
		grouped.extra.length > 0;
	const rememberMode = modeOption
		? modeOption.choices.find((choice) => choice.value === modeOption.currentValue)?.permissionMode
		: settings.approvalMode ?? "default";
	// Codex's default is Full access, which the project stores as bypass.
	const remembered = rememberMode !== undefined && rememberedPermissionMode !== undefined && (
		rememberedPermissionMode === rememberMode ||
		(harness === "codex" && rememberMode === "default" && rememberedPermissionMode === "bypass-permissions")
	);
	const rememberAction = onRememberPermissions && rememberMode && !planning ? (
		<ToggleRow
			label="Remember for this project"
			checked={remembered}
			onChange={optionDisabled ? undefined : (on) => {
				void Promise.resolve(onRememberPermissions(on ? rememberMode : null)).catch(() => {});
			}}
			className="mt-1 border-t border-border pt-2"
		/>
	) : null;
	const unified = Boolean(agentSwitch && harness);
	// The unified bar keeps the permission picker beside the Plan chip.
	const showPermissions = unified || !planning;
	const showRightDropdown = Boolean(children || (showPermissions && ((showApprovalMode && onChange) || modeOption)));
	const planOn = planning || Boolean(settings.planMode);
	const planLeaveValue = planReturn ?? grouped.executionMode?.choices.find((choice) => !isPlanChoice(choice))?.value;
	const leavePlan = () => {
		if (settings.planMode) onChange?.({ ...settings, planMode: undefined });
		else if (grouped.executionMode && planLeaveValue) applyOption(grouped.executionMode.id, { value: planLeaveValue });
	};
	// While planning, a provider whose one mode setting holds plan shows the mode it returns to.
	const shownModeOption = modeOption && planning && planLeaveValue
		? { ...modeOption, currentValue: planLeaveValue }
		: modeOption;
	// With another agent picked, the permission control is the mode it will start in,
	// which it may not share with the chat (Kimi runs only in its default, Pi only bypassed).
	const pending = agentSwitch?.pending;
	const pendingModes = pending ? agentSwitch?.pendingPermissionModes : undefined;
	const pendingAgent = pending ? agentLabel(pending.harness) : "";
	const pendingRuns = (mode: ApprovalMode) => !pendingModes || pendingModes.includes(mode);
	const chatMode = shownModeOption?.choices.find((choice) => choice.value === shownModeOption.currentValue)?.permissionMode
		?? settings.approvalMode ?? "default";
	const pendingWanted = pending?.approvalMode ?? agentSwitch?.chatPermissionMode ?? chatMode;
	const pendingMode = pendingRuns(pendingWanted) ? pendingWanted : pendingModes?.[0] ?? pendingWanted;
	const [pendingCopy, pendingOrder] = pending?.harness === "codex" ? [CODEX_APPROVAL_COPY, CODEX_APPROVAL_ORDER] as const : [APPROVAL_COPY, APPROVAL_ORDER] as const;
	const startsInNote = pendingMode !== pendingWanted
		? t("agentSwitch.startsInMode", { agent: pendingAgent, mode: pendingCopy[pendingWanted].label, target: pendingCopy[pendingMode].label })
		: undefined;
	const primaryModelOption = grouped.model.at(0);
	const fastOption = grouped.toggles.find(isFastOption);
	const thinkingOption = grouped.toggles.find(isThinkingToggle);
	const fastControls = fastOption && onChangeConfigOption
		? { fast: optionIsEnabled(fastOption), onToggleFast: (on: boolean) => setConfigToggle(fastOption, on, applyOption) }
		: {};
	const nativeFastControls = (selected ?? fallback)?.supportsFast && onChange
		? { fast: Boolean(settings.fastMode), onToggleFast: (on: boolean) => onChange({ ...settings, fastMode: on || undefined }) }
		: fastControls;
	const { ultracode, autoCompact } = agentSwitch ?? {};
	// Ultracode runs at Extra High, so it is only for models that have that level.
	const offersUltracode = Boolean(
		ultracode && harness === "claude-code" && onChangeConfigOption &&
		acpEffortOption?.choices.some((choice) => choice.value === "xhigh"),
	);
	const offersAutoCompact = Boolean(autoCompact) && compactsEarly(harness, primaryModelOption?.currentValue);
	const current: PickerSource = nativeModelMenu && onChange
		? {
			models: (harness === "claude-code" ? displayModels.filter((model) => model.id !== "default") : displayModels)
				.map((model) => ({ id: model.id, label: model.displayName })),
			modelId: settings.model ?? fallback?.id,
			modelLabel,
			// Fast mode is kept only where the newly picked model offers it.
			onPickModel: (id) => onChange({
				...settings,
				model: id,
				reasoningEffort: undefined,
				fastMode: displayModels.find((model) => model.id === id)?.supportsFast ? settings.fastMode : undefined,
			}),
			efforts: efforts.map((value) => ({ value, label: formatEffortLabel(value, t) })),
			effort: effortLabel,
			onPickEffort: (value) => onChange({ ...settings, reasoningEffort: value }),
			onPickSetup: (setup) => onChange({
				...settings,
				model: setup.model,
				reasoningEffort: setup.effort,
				fastMode: (setup.fast && displayModels.find((model) => model.id === setup.model)?.supportsFast) || undefined,
			}),
			...nativeFastControls,
		}
		: {
			models: (primaryModelOption?.choices ?? [])
				.map((choice) => ({ id: choice.value, label: choice.name, group: choice.groupName || choice.group, aliases: (choice as ChatConfigOption["choices"][number] & { aliases?: string[] }).aliases })),
			modelId: primaryModelOption?.currentValue,
			modelLabel: primaryModelOption ? optionCurrentLabel(primaryModelOption) : agentLabel(harness ?? ""),
			onPickModel: primaryModelOption && onChangeConfigOption ? (id) => applyOption(primaryModelOption.id, { value: id }) : undefined,
			// The effort, Thinking and Fast are set after the model, because the agent offers them
			// per model: each is read from the options the previous write returned, not the outgoing
			// model's. Fast is set either way, so a saved setup without it turns a paid Fast off.
			onPickSetup: primaryModelOption && onChangeConfigOption
				? (setup) => {
					ultracode?.onChange(false);
					let catalog = configOptions ?? [];
					const write = async (optionId: string, value: ChatConfigOptionValue) => {
						const next = await onChangeConfigOption(optionId, value);
						if (Array.isArray(next)) catalog = next as ChatConfigOption[];
					};
					const current = () => partitionConfigOptions(harness === "agy" ? agyComposerOptions(catalog) : catalog, harness !== "agy");
					void (async () => {
						await write(primaryModelOption.id, { value: setup.model });
						const effortOption = current().effort.at(0);
						if (setup.effort && effortOption?.choices.some((choice) => choice.value === setup.effort)) {
							await write(effortOption.id, { value: setup.effort });
						}
						const thinking = current().toggles.find(isThinkingToggle);
						if (thinking && setup.thinking !== undefined) await toggleConfigOption(thinking, setup.thinking, write);
						const fast = current().toggles.find(isFastOption);
						if (fast) await toggleConfigOption(fast, Boolean(setup.fast), write);
					})().catch(() => {});
				}
				: undefined,
			efforts: [
				...(acpEffortOption?.choices ?? [])
					.filter((choice) => choice.value && choice.value !== "default")
					.map((choice) => ({ value: choice.value, label: choice.name })),
				...(offersUltracode ? [{ value: ULTRACODE, label: "Ultracode" }] : []),
			],
			effort: offersUltracode && ultracode?.on
				? ULTRACODE
				: acpEffortOption?.currentValue && acpEffortOption.currentValue !== "default"
					? acpEffortOption.currentValue
					: undefined,
			onPickEffort: acpEffortOption && onChangeConfigOption
				? (value) => {
					ultracode?.onChange(value === ULTRACODE);
					applyOption(acpEffortOption.id, { value: value === ULTRACODE ? "xhigh" : value });
				}
				: undefined,
			...fastControls,
			...(thinkingOption && onChangeConfigOption
				? { thinking: optionIsEnabled(thinkingOption), onToggleThinking: (on: boolean) => setConfigToggle(thinkingOption, on, applyOption) }
				: {}),
		};
	// Plan and on/off settings live in the add menu; here is anything past the first model and effort.
	const hasPickerExtras = unified && Boolean(onChangeConfigOption) && (offersAutoCompact || grouped.extra.length > 0 ||
		(!nativeModelMenu && (grouped.model.length > 1 || grouped.effort.length > 1)));
	const pickerExtras = hasPickerExtras ? (
		<>
			{offersAutoCompact && autoCompact ? (
				<AutoCompactRow
					{...autoCompact}
					title="When Claude Code compacts the conversation on its own. Changing it restarts the agent; the conversation is kept."
				/>
			) : null}
			{nativeModelMenu ? null : grouped.model.slice(1).map((option) => (
				<OptionSubmenu key={option.id} option={option} onChange={applyOption} scrollable />
			))}
			{nativeModelMenu ? null : grouped.effort.slice(1).map((option) => (
				<EffortOptionSubmenu key={option.id} option={option} onChange={applyOption} />
			))}
			{grouped.extra.length > 0 ? <MoreOptionsSubmenu options={grouped.extra} onChange={applyOption} /> : null}
		</>
	) : null;

	return (
		<div role="group" aria-label="Turn settings" className="flex min-w-0 flex-1 flex-col gap-0.5">
			<div className="flex h-7 min-w-0 flex-1 items-center justify-between gap-2">
				<div className="flex h-7 min-w-0 flex-wrap items-center gap-0.5">
					{unified && agentSwitch && harness ? (
						<>
							{beforePicker}
							<AgentModelPicker
								currentHarness={harness}
								current={current}
								agentSwitch={agentSwitch}
								disabled={optionDisabled}
								// Only an agent that sends back its own settings has any to load; AO's would just flicker.
								loading={configPending && !nativeModelMenu}
								loadingModels={modelsLoading}
								savedModel={settings.model || lastModel}
								title={reroute ? `The provider answered with ${rerouted} instead of ${reroute.fromModel ?? chosenLabel}` : undefined}
								mark={reroute ? <Shuffle className="size-3 shrink-0 text-warning" aria-hidden="true" /> : undefined}
								extras={pickerExtras}
								onCloseAutoFocus={restoreComposerFocus}
							/>
						</>
					) : null}
					{!unified && nativeModelMenu && onChange ? (
						<ModelEffortPicker
							models={harness === "claude-code" ? displayModels.filter((model) => model.id !== "default") : displayModels}
							settings={settings}
							onChange={onChange}
							disabled={optionDisabled}
							modelLabel={modelLabel}
							groupLabel={modelGroupLabel}
							efforts={efforts}
							defaultEffort={(selected ?? fallback)?.defaultEffort}
							availability={(selected ?? fallback)?.efforts === undefined ? "unknown" : efforts.length ? "supported" : "unsupported"}
							reroute={reroute}
							rerouted={rerouted}
							chosenLabel={chosenLabel}
							executionMode={inlineExecutionMode}
							planReturn={planReturn}
							toggles={grouped.toggles}
							extraOptions={grouped.extra}
							onChangeConfigOption={onChangeConfigOption ? applyOption : undefined}
						/>
					) : null}

					{!unified && onChangeConfigOption && clubbedLeft && !nativeModelMenu ? (
						<ClubbedConfigPicker
							groupClaude={harness === "claude-code"}
							modelOptions={grouped.model}
							effortOptions={grouped.effort}
							executionMode={inlineExecutionMode}
							planReturn={planReturn}
							toggles={grouped.toggles}
							extraOptions={grouped.extra}
							disabled={optionDisabled}
							onChange={applyOption}
						/>
					) : null}

					{standaloneExecutionMode && onChangeConfigOption ? (
						<ExecutionModePicker
							option={standaloneExecutionMode}
							planReturn={planReturn}
							disabled={optionDisabled}
							onChange={applyOption}
						/>
					) : null}
				</div>

				{showRightDropdown || children || (unified && planOn) ? (
					<div className="flex h-7 shrink-0 items-center gap-1">
						{children}
						{showPermissions && pending && agentSwitch ? (
							<Picker
								label={pendingCopy[pendingMode].label}
								title={startsInNote ?? `Approval policy for ${pendingAgent}`}
								disabled={optionDisabled}
								leadIcon={permissionIcon(permissionLook(pending.harness, pendingMode))}
								alert={Boolean(startsInNote)}
							>
								{startsInNote ? (
									<p role="note" className="flex items-start gap-2 px-3 py-2 text-[11px] leading-snug text-destructive">
										<TriangleAlert aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" />
										<span>{startsInNote}</span>
									</p>
								) : null}
								{pendingOrder.map((mode) => (
									<PermissionRow
										key={mode}
										mode={permissionLook(pending.harness, mode)}
										title={pendingCopy[mode].label}
										description={pendingRuns(mode) ? pendingCopy[mode].description : t("agentSwitch.modeUnsupported", { agent: pendingAgent })}
										active={mode === pendingMode}
										disabled={!pendingRuns(mode)}
										onSelect={() => agentSwitch.onPendingChange({ ...pending, approvalMode: mode })}
									/>
								))}
							</Picker>
						) : showPermissions && shownModeOption && onChangeConfigOption ? (
							<ConfigOptionPicker
								option={shownModeOption}
								title={harness === "agy" ? "Approval policy for the next turn" : undefined}
								disabled={optionDisabled}
								onChange={(value) => applyOption(shownModeOption.id, value)}
								footer={rememberAction}
								leadIcon={unified ? permissionIcon(shownModeOption.choices.find((choice) => choice.value === shownModeOption.currentValue)?.permissionMode) : undefined}
							/>
						) : showApprovalMode && onChange ? (
							<Picker
								label={approvalLabel}
													title="Approval policy for the next turn"
													disabled={optionDisabled}
								leadIcon={unified ? permissionIcon(permissionLook(harness, settings.approvalMode ?? "default")) : undefined}
							>
								{approvalOrder.filter((mode) => !approvalModes || approvalModes.includes(mode)).map((mode) => (
									unified ? (
										<PermissionRow
											key={mode}
											mode={permissionLook(harness, mode)}
											title={approvalCopy[mode].label}
											description={approvalCopy[mode].description}
											active={mode === (settings.approvalMode ?? "default")}
											onSelect={() => onChange({ ...settings, approvalMode: mode })}
										/>
									) : (
									<OptionMenuItem
										key={mode}
										active={mode === (settings.approvalMode ?? "default")}
										radio
										onSelect={() => onChange({ ...settings, approvalMode: mode }, "approvalMode")}
										className={cn("text-xs")}
									>
										<span
											className={cn(
														"text-xs",
												mode === (settings.approvalMode ?? "default")
													? "text-foreground"
													: "text-muted-foreground",
											)}
										>
											{approvalCopy[mode].label}
										</span>
									</OptionMenuItem>
									)
								))}
								{rememberAction}
							</Picker>
						) : null}
						{unified && planOn ? (
							<button
								type="button"
								disabled={optionDisabled}
								onClick={leavePlan}
								aria-label="Plan mode is on. Turn it off"
								title="Plan mode is on. Click to turn it off"
								className={cn(TRIGGER_CLASS, "inline-flex items-center px-2 text-foreground")}
							>
								<ListTodo aria-hidden="true" className="size-3.5 shrink-0" />
								<span>Plan</span>
							</button>
						) : null}
					</div>
				) : null}
			</div>
			{rememberPermissionsError ? (
				<p role="alert" className="px-1 text-xs text-destructive">{rememberPermissionsError}</p>
			) : null}
			{error ? (
				<p role="alert" className="px-1 text-xs leading-snug text-destructive">
					{error}
				</p>
			) : null}
		</div>
	);
}

function dedupeChoicesByName<T extends { value: string; name: string; group?: string }>(choices: T[], current: string | undefined): (T & { aliases?: string[] })[] {
	return choices.flatMap((choice) => {
		// One model from two providers (Pi's openai and openai-codex) stays two rows.
		const twins = choices.filter((other) => other.name === choice.name && other.group === choice.group);
		const kept = twins.find((other) => other.value === current) ?? twins[0];
		if (choice !== kept) return [];
		// The values folded into this one, so a favourite starred on any of them still shows.
		const aliases = twins.filter((other) => other !== choice).map((other) => other.value);
		return [aliases.length ? { ...choice, aliases } : choice];
	});
}

const AGY_MODEL_EFFORT = "ao-agy-model-effort";

// Antigravity encodes reasoning levels in model IDs. Present the same model /
// effort menu as Codex while sending only the original, advertised model IDs.
function agyComposerOptions(options: ChatConfigOption[]): ChatConfigOption[] {
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
			name: choice.permissionMode ? AGY_APPROVAL_COPY[choice.permissionMode].label
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
		},
	];
}

/**
 * The provider's model option offers family aliases ("opus") and bare ids. The new-task
 * picker names them by version, so the chat does too.
 */
function claudeChoiceLabels(catalog: { id: string; displayName: string }[]) {
	const choiceModel = claudeChoiceModel(catalog.map((model) => ({ id: model.id, label: model.displayName })));
	return choiceModel && ((value: string, name?: string) => choiceModel(value, name)?.label);
}

function ModelEffortPicker({
	models,
	settings,
	onChange,
	disabled,
	modelLabel,
	groupLabel,
	efforts,
	defaultEffort,
	availability,
	reroute,
	rerouted,
	chosenLabel,
	executionMode,
	planReturn,
	toggles = [],
	extraOptions = [],
	onChangeConfigOption,
}: {
	models: ChatModel[];
	settings: TurnSettings;
	onChange: (next: TurnSettings) => void;
	disabled?: boolean;
	modelLabel: string;
	groupLabel: string;
	efforts: string[];
	defaultEffort?: string;
	availability: "supported" | "unsupported" | "unknown";
	reroute?: ModelReroute;
	rerouted?: string;
	chosenLabel: string;
	executionMode?: ChatConfigOption;
	planReturn?: string;
	toggles?: ChatConfigOption[];
	extraOptions?: ChatConfigOption[];
	onChangeConfigOption?: (optionId: string, value: ChatConfigOptionValue) => void;
}) {
	const { t } = useTranslation();
	const catalog = useMemo(() => models.map((model) => ({ ...model, label: model.displayName })), [models]);
	const effortChoices = useMemo(() => efforts.map((value) => ({ value })), [efforts]);
	const savedEffort = settings.reasoningEffort === "default" ? "" : settings.reasoningEffort ?? "";

	return (
		<OptionMenu>
				<OptionMenuTrigger
					showCaret={false}
					disabled={disabled}
					aria-label="Model and reasoning effort for the next turn"
					data-shortcut-target="model-picker"
					title={
						reroute
							? `The provider answered with ${rerouted} instead of ${reroute.fromModel ?? chosenLabel}${
									reroute.reason ? `: ${reroute.reason}` : ""
								}`
							: "Model and reasoning effort for the next turn"
					}
					className={TRIGGER_CLASS}
				>
					<span className="min-w-0 max-w-[38ch] truncate">{groupLabel}</span>
					{reroute ? (
						// A mark, not a second name. Two truncated model names side by side is
						// less legible than one readable name plus a flag that says it is not
						// the one that was asked for; the tooltip and the menu spell out which.
						<Shuffle
							className="size-3 shrink-0 text-warning"
							aria-label={`Substituted for ${reroute.fromModel ?? chosenLabel}`}
						/>
					) : null}
				</OptionMenuTrigger>
			<OptionMenuContent align="start" className={CHAT_MENU_CLASS} onCloseAutoFocus={restoreComposerFocus}>
				<OptionMenuSub>
					<OptionMenuSubTrigger label="Model" value={modelLabel} />
					{/* Scroll on an inner strip: the surface utility caps height but wheel
					    events do not reliably reach an outer overflow on nested submenus. */}
					<OptionMenuSubContent scrollable className={CHAT_MENU_CLASS} onFocus={focusModelSearch}>
						<ModelMenuChoices models={catalog}>
							{(matches, searchActiveID, optionID) => matches.map((model) => (
								<OptionMenuItem
									key={model.id}
									id={optionID?.(model.id)}
									active={model.id === settings.model}
									searchActive={model.id === searchActiveID}
									radio
									onSelect={() => onChange({ ...settings, model: model.id, reasoningEffort: undefined })}
									className={cn("text-xs", model.id === settings.model ? "text-foreground" : "text-muted-foreground")}
								>
									{model.displayName}
								</OptionMenuItem>
							))}
						</ModelMenuChoices>
					</OptionMenuSubContent>
				</OptionMenuSub>

				{efforts.length > 0 || savedEffort ? (
					<OptionMenuSub>
						<OptionMenuSubTrigger
							label={t("settings.models.effort")}
							value={effortDisplayLabel({ value: savedEffort, choices: effortChoices, followLabel: t("settings.models.effort"), t, defaultEffort })}
						/>
						<OptionMenuSubContent className={CHAT_MENU_CLASS}>
							<EffortMenuItems
								value={savedEffort}
								choices={effortChoices}
								availability={availability}
								defaultEffort={defaultEffort}
								compact
								onChange={(value) => onChange({ ...settings, reasoningEffort: value || undefined })}
							/>
						</OptionMenuSubContent>
					</OptionMenuSub>
				) : null}
				{executionMode && onChangeConfigOption ? (
					<PlanModeToggle option={executionMode} planReturn={planReturn} onChange={onChangeConfigOption} />
				) : null}
				{toggles.map((option) => (
					<ConfigToggle key={option.id} option={option} onChange={onChangeConfigOption!} />
				))}
				{extraOptions.length > 0 && onChangeConfigOption ? (
					<MoreOptionsSubmenu options={extraOptions} onChange={onChangeConfigOption} />
				) : null}
			</OptionMenuContent>
		</OptionMenu>
	);
}

function ClubbedConfigPicker({
	modelOptions,
	effortOptions,
	executionMode,
	planReturn,
	toggles,
	extraOptions,
	disabled,
	onChange,
	groupClaude,
}: {
	groupClaude?: boolean;
	modelOptions: ChatConfigOption[];
	effortOptions: ChatConfigOption[];
	executionMode?: ChatConfigOption;
	planReturn?: string;
	toggles: ChatConfigOption[];
	extraOptions: ChatConfigOption[];
	disabled?: boolean;
	onChange: (optionId: string, value: ChatConfigOptionValue) => void;
}) {
	const { t } = useTranslation();
	const primaryModel = modelOptions.at(0);
	const primaryEffort = effortOptions.at(0);
	const modelLabel = primaryModel ? optionCurrentLabel(primaryModel) : undefined;
	const effortLabel = primaryEffort ? effortDisplayLabel({ ...acpEffortProps(primaryEffort), followLabel: "", t }) : undefined;
	const groupLabel = [modelLabel, effortLabel].filter(Boolean).join(" ") || "More";
	const leftCount =
		modelOptions.length + effortOptions.length + Number(Boolean(executionMode)) + toggles.length + extraOptions.length;
	if (leftCount === 0) return null;
	if (leftCount === 1) {
		if (primaryEffort) {
			return (
				<EffortPicker
					{...acpEffortMenuProps(primaryEffort)}
					disabled={disabled}
					triggerClassName={TRIGGER_CLASS}
					onChange={(value) => onChange(primaryEffort.id, { value })}
				/>
			);
		}
		if (executionMode)
			return (
				<ExecutionModePicker
					option={executionMode}
					planReturn={planReturn}
					disabled={disabled}
					onChange={onChange}
				/>
			);
		const option = primaryModel ?? toggles[0] ?? extraOptions[0];
		if (!option) return null;
		return (
			<ConfigOptionPicker
				option={option}
				disabled={disabled}
				onChange={(value) => onChange(option.id, value)}
				groupClaude={groupClaude}
			/>
		);
	}

	return (
		<OptionMenu>
				<OptionMenuTrigger
					showCaret={false}
					disabled={disabled}
					aria-label="Model and reasoning effort for the next turn"
					data-shortcut-target="model-picker"
					title="Model and reasoning effort for the next turn"
					className={TRIGGER_CLASS}
				>
					<span className="min-w-0 max-w-[38ch] truncate">{groupLabel}</span>
				</OptionMenuTrigger>
			<OptionMenuContent align="start" className={CHAT_MENU_CLASS} onCloseAutoFocus={restoreComposerFocus}>
				{modelOptions.map((option) => (
					<OptionSubmenu key={option.id} option={option} onChange={onChange} scrollable groupClaude={groupClaude} />
				))}
				{effortOptions.map((option) => (
					<EffortOptionSubmenu key={option.id} option={option} onChange={onChange} />
				))}
				{executionMode ? <PlanModeToggle option={executionMode} planReturn={planReturn} onChange={onChange} /> : null}
				{toggles.map((option) => (
					<ConfigToggle key={option.id} option={option} onChange={onChange} />
				))}
				{extraOptions.length > 0 ? (
					<MoreOptionsSubmenu options={extraOptions} onChange={onChange} />
				) : null}
			</OptionMenuContent>
		</OptionMenu>
	);
}

// ACP exposes the provider-owned reset as a "default" choice whose description
// names the concrete level it resolves to.
function acpEffortMenuProps(option: ChatConfigOption) {
	const defaultChoice = option.choices.find((choice) => choice.value === "default");
	return {
		value: option.currentValue ?? "",
		choices: option.choices.map((choice) => ({ value: choice.value, label: choice.name })),
		defaultValue: defaultChoice?.value ?? null,
		defaultEffort: defaultChoice?.description,
	};
}

function acpEffortProps(option: ChatConfigOption) {
	const { value, choices, defaultEffort } = acpEffortMenuProps(option);
	return { value: value === "default" ? "" : value, choices, defaultEffort };
}

// ACP exposes the provider-owned reset as a "default" choice. When it names no
// concrete level, nothing is selected, so AO supplies one.
function acpFallbackEffort(option: ChatConfigOption): string | undefined {
	if (option.currentValue && option.currentValue !== "default") return undefined;
	const concrete = option.choices.filter((choice) => choice.value && choice.value !== "default");
	const described = option.choices.find((choice) => choice.value === "default")?.description?.trim().toLowerCase();
	const reported = concrete.find(
		(choice) => described && (choice.value.toLowerCase() === described || choice.name.toLowerCase() === described),
	)?.value;
	return fallbackEffort(
		concrete.map((choice) => choice.value),
		reported,
	);
}

function EffortOptionSubmenu({
	option,
	onChange,
}: {
	option: ChatConfigOption;
	onChange: (id: string, value: ChatConfigOptionValue) => void;
}) {
	const { t } = useTranslation();
	const followLabel = t("settings.models.effort");
	return (
		<OptionMenuSub>
			<OptionMenuSubTrigger label={followLabel} value={effortDisplayLabel({ ...acpEffortProps(option), followLabel, t })} />
			<OptionMenuSubContent className={CHAT_MENU_CLASS}>
				<EffortMenuItems {...acpEffortMenuProps(option)} compact onChange={(value) => onChange(option.id, { value })} />
			</OptionMenuSubContent>
		</OptionMenuSub>
	);
}

function PlanModeToggle({
	option,
	planReturn,
	onChange,
}: {
	option: ChatConfigOption;
	/** Where turning Plan off goes, when the provider shares one slot for both. */
	planReturn?: string;
	onChange: (optionId: string, value: ChatConfigOptionValue) => void;
}) {
	const planning = isPlanMode(option);
	const next = planning
		? planReturn ?? option.choices.find((choice) => !isPlanChoice(choice))?.value
		: option.choices.find((choice) => isPlanChoice(choice))?.value;
	if (!next) return null;
	return (
		<MenuToggle
			label="Plan Mode"
			checked={planning}
			onCheckedChange={() => onChange(option.id, { value: next })}
		/>
	);
}

// ⌘⇧M opens the model menu from the composer, so closing it (Esc or a choice)
// hands focus back to the composer instead of leaving it on the trigger.
function restoreComposerFocus(event: Event) {
	const trigger = document.querySelector<HTMLElement>("[data-shortcut-target='model-picker'][data-shortcut-opened]");
	if (!trigger) return;
	delete trigger.dataset.shortcutOpened;
	const composer = document.querySelector<HTMLElement>('[aria-label="Message the agent"]');
	if (!composer || composer.getAttribute("aria-disabled") === "true") return;
	event.preventDefault();
	composer.focus();
}

function ConfigToggle({
	option,
	onChange,
}: {
	option: ChatConfigOption;
	onChange: (optionId: string, value: ChatConfigOptionValue) => void;
}) {
	return (
		<MenuToggle
			label={option.name}
			checked={optionIsEnabled(option)}
			onCheckedChange={(enabled) => {
				if (option.type === "boolean") {
					onChange(option.id, { enabled });
					return;
				}
				const next = option.choices.find((choice) => choiceIsEnabled(choice) === enabled);
				if (next) onChange(option.id, { value: next.value });
			}}
		/>
	);
}

function MenuToggle({
	label,
	checked,
	onCheckedChange,
}: {
	label: string;
	checked: boolean;
	onCheckedChange: (checked: boolean) => void;
}) {
	return (
		<OptionMenuItem
			// Keep the menu open so several toggles can be flipped, and make Enter on
			// the focused row do what clicking its switch does.
			onSelect={(event) => {
				event.preventDefault();
				onCheckedChange(!checked);
			}}
			className="justify-between gap-4 px-3 py-2 text-xs"
		>
			<span>{label}</span>
			<Switch
				aria-label={label}
				checked={checked}
				// Presentational: the row owns the toggle so a click or Enter fires once.
				tabIndex={-1}
			/>
		</OptionMenuItem>
	);
}

function ExecutionModePicker({
	option,
	planReturn,
	disabled,
	onChange,
}: {
	option: ChatConfigOption;
	planReturn?: string;
	disabled?: boolean;
	onChange: (optionId: string, value: ChatConfigOptionValue) => void;
}) {
	return (
		<OptionMenu>
			<OptionMenuTrigger
				showCaret={false}
				disabled={disabled}
				aria-label="Model mode for the next turn"
				title="Model mode for the next turn"
				className={TRIGGER_CLASS}
			>
				<span className="min-w-0 max-w-[16ch] truncate">{executionModeLabel(option)}</span>
			</OptionMenuTrigger>
			<OptionMenuContent align="start" className={CHAT_MENU_CLASS}>
				{isPlanBinary(option) ? (
					<PlanModeToggle option={option} planReturn={planReturn} onChange={onChange} />
				) : (
					<ConfigOptionChoices
						option={option}
						onChange={(value) => onChange(option.id, value)}
					/>
				)}
			</OptionMenuContent>
		</OptionMenu>
	);
}

function MoreOptionsSubmenu({
	options,
	onChange,
}: {
	options: ChatConfigOption[];
	onChange: (optionId: string, value: ChatConfigOptionValue) => void;
}) {
	return (
		<OptionMenuSub>
			<OptionMenuSubTrigger label="More" />
			<OptionMenuSubContent className={CHAT_MENU_CLASS}>
				{options.map((option) => (
					<OptionSubmenu key={option.id} option={option} onChange={onChange} />
				))}
			</OptionMenuSubContent>
		</OptionMenuSub>
	);
}

function OptionSubmenu({
	option,
	label,
	onChange,
	scrollable,
	groupClaude,
}: {
	option: ChatConfigOption;
	/** A semantic label when one provider option is deliberately split in two. */
	label?: string;
	onChange: (optionId: string, value: ChatConfigOptionValue) => void;
	scrollable?: boolean;
	groupClaude?: boolean;
}) {
	const current = optionCurrentLabel(option);
	return (
		<OptionMenuSub>
			<OptionMenuSubTrigger label={label ?? option.name} value={current} />
			<OptionMenuSubContent
				scrollable={scrollable}
				className={CHAT_MENU_CLASS}
				onFocus={isModelOption(option) ? focusModelSearch : undefined}
			>
				{isModelOption(option) ? (
					<ConfigModelChoices option={option} onChange={(value) => onChange(option.id, value)} groupClaude={groupClaude} />
				) : (
					<ConfigOptionChoices
						option={option}
						onChange={(value) => onChange(option.id, value)}
					/>
				)}
			</OptionMenuSubContent>
		</OptionMenuSub>
	);
}

function ConfigOptionPicker({
	option,
	label,
	title,
	onChange,
	disabled,
	footer,
	leadIcon,
	groupClaude,
}: {
	option: ChatConfigOption;
	label?: string;
	title?: string;
	onChange: (value: ChatConfigOptionValue) => void;
	disabled?: boolean;
	footer?: ReactNode;
	leadIcon?: LucideIcon;
	groupClaude?: boolean;
}) {
	return (
		<Picker
			label={label ?? optionCurrentLabel(option)}
			title={title || option.description || option.name}
			disabled={disabled}
			leadIcon={leadIcon}
			onFocus={isModelOption(option) ? focusModelSearch : undefined}
		>
			{isModelOption(option) ? (
				<ConfigModelChoices option={option} onChange={onChange} groupClaude={groupClaude} />
			) : leadIcon && option.type === "select" ? (
				option.choices.map((choice) => (
					<PermissionRow
						key={choice.value}
						mode={choice.permissionMode}
						title={choice.name}
						description={choice.description}
						active={choice.value === option.currentValue}
						onSelect={() => onChange({ value: choice.value })}
					/>
				))
			) : (
				<ConfigOptionChoices option={option} onChange={onChange} />
			)}
			{footer}
		</Picker>
	);
}

function ConfigModelChoices({
	option,
	onChange,
	groupClaude,
}: {
	option: ChatConfigOption;
	onChange: (value: ChatConfigOptionValue) => void;
	/** Newest model per Claude family first, the rest behind a toggle, as in the new-task picker. */
	groupClaude?: boolean;
}) {
	const { t } = useTranslation();
	const models = useMemo(() => option.choices.map((choice) => ({
		...choice,
		id: choice.value,
		label: choice.name,
	})), [option.choices]);
	const claude = useMemo(() => (groupClaude ? splitClaudeModels(models) : undefined), [groupClaude, models]);
	const [otherOpen, setOtherOpen] = useState(false);
	const selectedIsOther = Boolean(claude?.other.some((model) => model.id === option.currentValue));
	const showOther = otherOpen || selectedIsOther;
	const menuModels = claude ? (showOther ? [...claude.current, ...claude.other] : claude.current) : models;
	return (
		<ModelMenuChoices models={menuModels}>
			{(matches, searchActiveID, optionID) => (
				<>
					<ConfigOptionChoices
						option={{ ...option, choices: matches }}
						onChange={onChange}
						searchActiveID={searchActiveID}
						optionID={optionID}
					/>
					{claude?.other.length && !selectedIsOther ? (
						<OptionMenuItem
							// The composer focuses its editor on any click that bubbles out of the portal,
							// which would pull focus out of the menu and close it.
							onClick={(event) => event.stopPropagation()}
							onSelect={(event) => {
								event.preventDefault();
								setOtherOpen((open) => !open);
							}}
							className="text-xs text-muted-foreground"
							aria-expanded={otherOpen}
						>
							{t("settings.models.otherModels")}
							<ChevronDown className={cn("ml-auto size-3 opacity-70 transition-transform", otherOpen && "rotate-180")} aria-hidden="true" />
						</OptionMenuItem>
					) : null}
				</>
			)}
		</ModelMenuChoices>
	);
}

function ConfigOptionChoices({
	option,
	onChange,
	searchActiveID,
	optionID,
}: {
	option: ChatConfigOption;
	onChange: (value: ChatConfigOptionValue) => void;
	searchActiveID?: string;
	optionID?: (id: string) => string;
}) {
	if (option.type === "boolean") {
		return (
			<>
				{[true, false].map((enabled) => (
					<OptionMenuItem
							key={String(enabled)}
							active={enabled === option.currentBoolean}
							radio
						onSelect={() => onChange({ enabled })}
						className={cn("text-xs")}
					>
						<span
							className={cn(
								enabled === option.currentBoolean
									? "text-foreground"
									: "text-muted-foreground",
							)}
						>
							{enabled ? "On" : "Off"}
						</span>
					</OptionMenuItem>
				))}
			</>
		);
	}

	return (
		<>
			{option.choices.map((choice, index) => {
				const previousGroup = index > 0 ? option.choices[index - 1]?.group : undefined;
				return (
					<Fragment key={choice.value}>
						{choice.group && choice.group !== previousGroup ? (
							<OptionMenuLabel className="px-3 pb-1 pt-2 text-micro text-muted-foreground">
								{choice.groupName || choice.group}
							</OptionMenuLabel>
						) : null}
						<OptionMenuItem
							id={optionID?.(choice.value)}
							active={choice.value === option.currentValue}
							searchActive={choice.value === searchActiveID}
							radio
							onSelect={() => onChange({ value: choice.value })}
							className={cn("text-xs")}
						>
							<span className="flex w-full items-baseline gap-2">
								<span
									className={cn(
										"text-xs",
										choice.value === option.currentValue
											? "text-foreground"
											: "text-muted-foreground",
									)}
								>
									{choice.name}
								</span>
							</span>
						</OptionMenuItem>
					</Fragment>
				);
			})}
		</>
	);
}

function isFastOption(option: ChatConfigOption): boolean {
	return /(?:^|[\s_-])fast(?:[\s_-]|$)/i.test(`${option.id} ${option.name}`);
}

function setConfigToggle(
	option: ChatConfigOption,
	enabled: boolean,
	apply: (optionId: string, value: ChatConfigOptionValue) => void,
) {
	if (option.type === "boolean") {
		apply(option.id, { enabled });
		return;
	}
	const next = option.choices.find((choice) => choiceIsEnabled(choice) === enabled);
	if (next) apply(option.id, { value: next.value });
}

/** setConfigToggle for a write the caller waits on; a toggle already set is not written. */
async function toggleConfigOption(
	option: ChatConfigOption,
	enabled: boolean,
	write: (optionId: string, value: ChatConfigOptionValue) => Promise<void>,
) {
	if (optionIsEnabled(option) === enabled) return;
	let pending: Promise<void> | undefined;
	setConfigToggle(option, enabled, (optionId, value) => { pending = write(optionId, value); });
	await pending;
}

/** Codex names its modes differently: its default has a free hand and its accept-edits asks first. */
function permissionLook(harness: string | undefined, mode: ApprovalMode): ApprovalMode {
	if (harness !== "codex") return mode;
	return mode === "default" ? "bypass-permissions" : mode === "accept-edits" ? "default" : mode;
}

function permissionIcon(mode: ApprovalMode | undefined): LucideIcon {
	if (mode === "bypass-permissions") return ShieldAlert;
	if (mode === "accept-edits") return PencilLine;
	if (mode === "auto") return ShieldCheck;
	return Hand;
}

/** One permission mode: what it is called, what it lets the agent do, and whether it is on. */
function PermissionRow({
	mode,
	title,
	description,
	active,
	disabled,
	onSelect,
}: {
	mode?: ApprovalMode;
	title: string;
	description?: string;
	active: boolean;
	/** The agent picked for the next message cannot run in this mode. */
	disabled?: boolean;
	onSelect: () => void;
}) {
	const Icon = permissionIcon(mode);
	const unrestricted = mode === "bypass-permissions";
	return (
		<OptionMenuItem active={active} radio disabled={disabled} onSelect={onSelect} className="items-start gap-3 whitespace-normal px-3 py-2">
			<Icon aria-hidden="true" className={cn("mt-0.5 size-4 shrink-0", unrestricted ? "text-warning" : "text-muted-foreground")} />
			<span className="flex min-w-0 flex-1 flex-col gap-0.5">
				<span className={cn("text-xs", unrestricted ? "text-warning" : "text-foreground")}>{title}</span>
				{description ? (
					<span className={cn("truncate text-[11px] leading-snug", unrestricted ? "text-warning/80" : "text-muted-foreground")}>
						{description}
					</span>
				) : null}
			</span>
			{active ? <Check aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-foreground" /> : null}
		</OptionMenuItem>
	);
}

/** A row of the chat box's add menu: an icon, what it is, and what choosing it does. */
export function ComposerMenuRow({
	icon: Icon,
	title,
	description,
	checked,
	disabled,
	onSelect,
}: {
	icon: LucideIcon;
	title: string;
	description?: string;
	checked?: boolean;
	disabled?: boolean;
	onSelect: () => void;
}) {
	return (
		<OptionMenuItem disabled={disabled} onSelect={onSelect} className="gap-2 px-3 py-1.5 text-xs">
			<Icon aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
			<span className="shrink-0 text-foreground">{title}</span>
			{description ? <span className="min-w-0 truncate text-muted-foreground">{description}</span> : null}
			{checked ? <Check aria-hidden="true" className="ml-auto size-3.5 shrink-0 text-foreground" /> : null}
		</OptionMenuItem>
	);
}

const ULTRACODE = "ultracode";

/** Claude Code turns Ultracode on for a turn when the user's message names it. */
export function withUltracode(text: string): string {
	return /\bultracode\b/i.test(text) ? text : `Ultracode:\n${text}`;
}

/** The agent's on/off settings as add-menu rows: plan mode, fast mode and any other switch it advertises. */
export function ComposerModeItems({
	configOptions,
	settings,
	models,
	nativePlan = false,
	onChange,
	onChangeConfigOption,
	disabled,
}: {
	configOptions: ChatConfigOption[];
	settings: TurnSettings;
	/** The provider's own model list, for settings AO sends per turn. */
	models?: ChatModel[];
	/** The provider takes plan mode as a per-turn setting from AO. */
	nativePlan?: boolean;
	onChange?: (next: TurnSettings) => void;
	onChangeConfigOption?: (optionId: string, value: ChatConfigOptionValue) => Promise<unknown> | void;
	disabled?: boolean;
}) {
	const grouped = partitionConfigOptions(configOptions);
	const apply = (optionId: string, value: ChatConfigOptionValue) => {
		if (onChangeConfigOption) void Promise.resolve(onChangeConfigOption(optionId, value)).catch(() => {});
	};
	const executionMode = onChangeConfigOption && grouped.executionMode && isPlanBinary(grouped.executionMode)
		? grouped.executionMode
		: undefined;
	const planning = isPlanMode(executionMode);
	const planReturn = grouped.mode?.choices.find((choice) => choice.permissionMode === (settings.approvalMode ?? "default"))?.value;
	const planTarget = planning
		? planReturn ?? executionMode?.choices.find((choice) => !isPlanChoice(choice))?.value
		: executionMode?.choices.find(isPlanChoice)?.value;
	const planOn = executionMode ? planning : Boolean(settings.planMode);
	const togglePlan = executionMode
		? planTarget ? () => apply(executionMode.id, { value: planTarget }) : undefined
		: nativePlan && onChange ? () => onChange({ ...settings, planMode: settings.planMode ? undefined : true }) : undefined;
	const model = models?.find((entry) => entry.id === settings.model) ?? models?.find((entry) => entry.default);
	const rows = [
		...(togglePlan ? [{ icon: ListTodo, title: "Plan mode", on: planOn, toggle: togglePlan }] : []),
		...(model?.supportsFast && onChange ? [{
			icon: Zap,
			title: "Fast mode",
			on: Boolean(settings.fastMode),
			toggle: () => onChange({ ...settings, fastMode: settings.fastMode ? undefined : true }),
		}] : []),
		...(onChangeConfigOption ? grouped.toggles : []).map((option) => ({
			icon: isFastOption(option) ? Zap : SlidersHorizontal,
			title: option.name,
			on: optionIsEnabled(option),
			toggle: () => setConfigToggle(option, !optionIsEnabled(option), apply),
		})),
	];
	return rows.map((row, index) => (
		<ComposerMenuRow
			key={index}
			icon={row.icon}
			title={row.title}
			description={`Turn ${row.title.toLowerCase()} ${row.on ? "off" : "on"}`}
			checked={row.on}
			disabled={disabled}
			onSelect={row.toggle}
		/>
	));
}

/**
 * One dropdown, wearing the chrome Settings uses. These controls are the same
 * kind of thing as a settings row's — pick one of a list — so they are drawn the
 * same way, and the panel sizes itself from the shared surface rather than each
 * picker naming a width of its own.
 */
function Picker({
	label,
	title,
	disabled,
	badge,
	children,
	onFocus,
	leadIcon: LeadIcon,
	alert,
}: {
	label: string;
	title: string;
	disabled?: boolean;
	/** The current choice will not hold for the next message: marked red. */
	alert?: boolean;
	/** Set on the permission control when it leads the row: marked with this, with a caret, opening rightwards. */
	leadIcon?: LucideIcon;
	/** A note that belongs on the trigger, e.g. the model that was overridden. */
	badge?: React.ReactNode;
	children: React.ReactNode;
	onFocus?: (event: FocusEvent<HTMLDivElement>) => void;
}) {
	return (
		<OptionMenu>
				<OptionMenuTrigger
					showCaret={Boolean(LeadIcon)}
					aria-label={title}
					title={title}
					disabled={disabled}
					className={cn(TRIGGER_CLASS, LeadIcon ? "px-2" : undefined, alert ? "text-destructive" : undefined)}
				>
					{alert ? <TriangleAlert aria-hidden="true" className="size-3.5 shrink-0 text-destructive" />
						: LeadIcon ? <LeadIcon aria-hidden="true" className="size-3.5 shrink-0" /> : null}
					<span className="min-w-0 max-w-[16ch] truncate">{label}</span>
					{badge}
				</OptionMenuTrigger>
			<OptionMenuContent
				align={LeadIcon ? "start" : "end"}
				// The permission menu fits every mode on one line each, plus the remember switch, without scrolling.
				className={cn(CHAT_MENU_CLASS, LeadIcon ? "max-h-(--radix-dropdown-menu-content-available-height)! max-w-[calc(100vw-2rem)]!" : undefined)}
				onFocus={onFocus}
			>
				{children}
			</OptionMenuContent>
		</OptionMenu>
	);
}

function focusModelSearch(event: FocusEvent<HTMLDivElement>) {
	if (event.target !== event.currentTarget) return;
	const search = event.currentTarget.querySelector<HTMLInputElement>('input[type="search"]');
	if (search) {
		// Focus search before the menu's roving focus chooses a model.
		event.preventDefault();
		search.focus();
	}
}

function isModelOption(option: ChatConfigOption): boolean {
	return option.category === "model" || option.id === "model";
}

/**
 * ACP may advertise an `agent` option for its own multi-agent internals. It is
 * not AO's harness switcher and is not a model choice, so exposing it in the
 * composer promises a meaning AO cannot guarantee. Harness changes remain in
 * the dedicated session switcher.
 */
function isAgentOption(option: ChatConfigOption): boolean {
	return option.id === "agent" || option.category === "agent";
}

function isEffortOption(option: ChatConfigOption): boolean {
	return option.category === "thought_level" || option.id === "effort";
}

function isModeOption(option: ChatConfigOption): boolean {
	return option.category === "mode" || option.id === "mode";
}

function isInlineToggleOption(option: ChatConfigOption): boolean {
	return option.type === "boolean" || isThinkingToggle(option) || isFastOption(option);
}

/** Cursor reports thinking as an on/off choice among its effort settings. */
function isThinkingToggle(option: ChatConfigOption): boolean {
	return option.id === "thinking" && option.choices.length === 2 && option.choices.some(choiceIsEnabled);
}

function optionIsEnabled(option: ChatConfigOption): boolean {
	if (option.type === "boolean") return Boolean(option.currentBoolean);
	return choiceIsEnabled(option.choices.find((choice) => choice.value === option.currentValue));
}

function choiceIsEnabled(choice: ChatConfigOption["choices"][number] | undefined): boolean {
	return Boolean(choice && /(?:^|[\s_-])(on|enabled|true)(?:[\s_-]|$)/i.test(`${choice.name} ${choice.value}`));
}

/**
 * Whether a provider catalog replaces AO's own approval control. A `mode` option
 * that offers only execution modes (OpenCode's build/plan) is not one: taking it
 * for an approval catalog leaves the session with no permission control at all.
 */
export function hasProviderPermissionMode(options: ChatConfigOption[]): boolean {
	return Boolean(partitionConfigOptions(options).mode);
}

function partitionConfigOptions(options: ChatConfigOption[], includeExecutionModes = true): {
	model: ChatConfigOption[];
	effort: ChatConfigOption[];
	/** Provider plan/agent modes, placed with model selection rather than permissions. */
	executionMode: ChatConfigOption | undefined;
	/** Compact boolean controls, such as Fast Mode, rendered beside Plan Mode. */
	toggles: ChatConfigOption[];
	mode: ChatConfigOption | undefined;
	extra: ChatConfigOption[];
} {
	const primaryModel: ChatConfigOption[] = [];
	const otherModel: ChatConfigOption[] = [];
	const effort: ChatConfigOption[] = [];
	const toggles: ChatConfigOption[] = [];
	const extra: ChatConfigOption[] = [];
	let executionMode: ChatConfigOption | undefined;
	let mode: ChatConfigOption | undefined;
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

function resolveImplicitChoice(option: ChatConfigOption): ChatConfigOption {
	const mapped = isModeOption(option) ? {
		...option,
		choices: option.choices.map((choice) =>
			choice.permissionMode === "default" && isDefaultPlaceholderLabel(choice.name)
				? { ...choice, name: "Use agent permissions" }
				: choice,
		),
	} : option;
	// A mode named Default is the agent's own beside its other own modes (Kimi's Default, Plan,
	// Auto, YOLO); it means "the agent decides" only alone or beside modes matched to AO's.
	const placeholderMode = (choice: ChatConfigOption["choices"][number]) =>
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
		// The row stands for the hidden model too, so a favourite saved on it still shows as offered.
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
function executionChoiceValues(choices: ChatConfigOption["choices"]): Set<string> {
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
function executionChoiceMatches(choice: ChatConfigOption["choices"][number], word: string): boolean {
	return [choice.value, choice.name].some((value) =>
		new RegExp(`^(?:${word})(?:[\\s_-]mode)?$`, "i").test(value.trim()),
	);
}

function choiceMatches(
	choice: Pick<ChatConfigOption["choices"][number], "name" | "value">,
	word: string,
): boolean {
	return new RegExp(`(?:^|[\\s_-])(?:${word})(?:[\\s_-]|$)`, "i").test(`${choice.name} ${choice.value}`);
}

/**
 * Claude currently sends Plan as an execution mode but describes its ordinary
 * interactive posture as Manual. Present the latter as Agent Mode next to Plan,
 * without inventing a value: choosing it still sends the provider's own Manual
 * value. Other harnesses that advertise Agent Mode explicitly stay untouched.
 */
function addAgentModeChoice(
	executionChoices: ChatConfigOption["choices"],
	permissionChoices: ChatConfigOption["choices"],
): ChatConfigOption["choices"] {
	// OpenCode names its ordinary agent mode "build", so its catalog already has
	// one and must not gain a synthetic duplicate.
	if (executionChoices.some((choice) => executionChoiceMatches(choice, "agent|build"))) {
		return executionChoices;
	}
	const standard = permissionChoices.find((choice) => choiceMatches(choice, "manual"))
		?? permissionChoices.find((choice) => choiceMatches(choice, "default|standard"));
	return standard
		? [{ ...standard, name: "Agent Mode", description: "Standard agent execution" }, ...executionChoices]
		: executionChoices;
}

function isPlanBinary(option: ChatConfigOption): boolean {
	return option.choices.length === 2 && option.choices.some(isPlanChoice);
}

function executionModeLabel(option: ChatConfigOption): string {
	if (isPlanBinary(option)) return isPlanMode(option) ? "Plan Mode" : "Agent Mode";
	return option.choices.find((choice) => choice.value === option.currentValue)?.name ?? option.name;
}

function isPlanMode(option: ChatConfigOption | undefined): boolean {
	return Boolean(option?.currentValue && isPlanChoice({ value: option.currentValue, name: option.currentValue }));
}

function isPlanChoice(choice: Pick<ChatConfigOption["choices"][number], "name" | "value">): boolean {
	// Custom agent IDs can contain "plan" without enabling native planning.
	return [choice.value, choice.name].some((value) => /^plan(?:[\s_-]mode)?$/i.test(value.trim()));
}

function withChoices(
	option: ChatConfigOption,
	choices: ChatConfigOption["choices"],
	name = option.name,
): ChatConfigOption {
	const currentValue = choices.some((choice) => choice.value === option.currentValue)
		? option.currentValue
		: undefined;
	return { ...option, name, currentValue, choices };
}

function optionCurrentLabel(option: ChatConfigOption): string {
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
