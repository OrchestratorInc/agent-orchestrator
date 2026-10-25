/**
 * One menu for who answers next: saved setups, Agent/Model/Effort rows, then toggles.
 * The running agent's settings apply at once; another agent is only held as pending
 * until the next message (or Switch, in a terminal), so browsing never restarts anything.
 */

import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import { Brain, ChevronRight, Loader2, Star, TriangleAlert, X, Zap } from "lucide-react";
import { Fragment, memo, useCallback, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { agentModelsQueryOptions, type AgentModelCatalog } from "../../hooks/useAgentModelsQuery";
import { useAgentManagementMenu } from "../../hooks/useAgentManagementMenu";
import { useAgentReadinessQuery } from "../../hooks/useAgentReadinessQuery";
import { useSettings } from "../../hooks/useSettings";
import { agentModelDisplayLabel, offeredCatalogModels, recordedModelName, splitClaudeModels } from "../../lib/agent-model-choices";
import { agentLabel } from "../../lib/agent-options";
import { isLaunchableAgent } from "../../lib/agent-select-options";
import { CHAT_SWITCH_TARGETS, TERMINAL_ONE_SHOT_TARGETS, TERMINAL_SWITCH_TARGETS } from "../../lib/agent-switch-targets";
import { clientForSessionHost } from "../../lib/host-clients";
import { toggleModelFavorite, useModelFavorites, type ModelFavorite } from "../../lib/model-favorites";
import {
	collapseModelVariants,
	groupModelsByVendor,
	findModelVariant,
	listsModelVariants,
	modelGroupLabel,
	pickModelVariant,
	readableModelLabel,
	type ModelVariant,
} from "../../lib/model-variants";
import { cn } from "../../lib/utils";
import type { ApprovalMode, TurnSettings } from "../../types/conversation";
import { formatEffortLabel } from "../settings/EffortPicker";
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
import { Switch } from "../ui/switch";
import { ModelMenuChoices } from "./ModelMenuChoices";
import { ProviderMark } from "./ProviderMark";

export interface PendingAgentSwitch {
	harness: string;
	/** Empty starts the agent on its own default model. */
	model?: string;
	modelLabel?: string;
	/** Sent with the switch. Unset when the model id already carries the effort. */
	effort?: string;
	/** Sent with the switch: start the agent on its faster tier. */
	fastMode?: boolean;
	/** What the chip shows; for agents whose model id carries these. */
	shownEffort?: string;
	fast?: boolean;
	thinking?: boolean;
	/** Run Claude Code with Ultracode: set at launch in a terminal, asked per message in chat. */
	ultracode?: boolean;
	/** Terminal only: where Claude Code compacts on its own, "200k" or "1m". */
	autoCompactWindow?: TurnSettings["autoCompactWindow"];
	/** The permission mode picked for the agent. Unset starts it in the chat's, or its strictest. */
	approvalMode?: ApprovalMode;
}

/** The effort a switch starts at: the one the picker shows. A Cursor variant id already carries it. */
export function switchEffort(pending: PendingAgentSwitch): string | undefined {
	return pending.effort ?? (listsModelVariants(pending.harness) ? undefined : pending.shownEffort);
}

/** Claude Code only compacts early on its larger-window models. */
export function compactsEarly(harness: string | undefined, model = ""): boolean {
	return harness === "claude-code" && !/haiku|opus-4-5/.test(model);
}

/** What the chat surface passes down to turn the model control into this one. */
export interface AgentSwitchProps {
	projectId: string;
	hostId?: string;
	pending?: PendingAgentSwitch;
	/** The permission modes the pending agent runs in, strictest first, once known. */
	pendingPermissionModes?: ApprovalMode[];
	/** The chat's own permission mode while its agent runs in another it needed, as Pi needs Bypass. */
	chatPermissionMode?: ApprovalMode;
	onPendingChange: (next: PendingAgentSwitch | undefined) => void;
	/** Switching is refused while the agent is working. */
	disabled?: boolean;
	/** Claude Code's Ultracode: Extra High effort plus its workflows, asked for by a keyword in each message. */
	ultracode?: { on: boolean; onChange: (on: boolean) => void };
	/** When Claude Code compacts on its own. Applies the next time it starts. */
	autoCompact?: AutoCompactControl;
}

export interface PickerModel {
	id: string;
	label: string;
	/** Models from several makers are listed under their maker. */
	group?: string;
	/** A quiet note beside the name, such as "default". */
	hint?: string;
	/** The id to select for this row, where it differs from the row's own id. */
	sendId?: string;
	/** Other ids the agent lists this same model under, whose favourites are this row's. */
	aliases?: string[];
}

/** One agent's models and settings, with what each control does when used. */
export interface PickerSource {
	models: PickerModel[];
	modelId?: string;
	/** The id of the selection, where it differs from the chosen row's id. */
	sendId?: string;
	modelLabel: string;
	onPickModel?: (id: string) => void;
	/** Selects a model together with its settings in one step, for a favourite. */
	onPickSetup?: (setup: ModelFavorite) => void;
	efforts: Array<{ value: string; label: string }>;
	effort?: string;
	onPickEffort?: (value: string) => void;
	/** Undefined when the agent has no speed setting. */
	fast?: boolean;
	onToggleFast?: (next: boolean) => void;
	/** Undefined when the model has no thinking choice. */
	thinking?: boolean;
	onToggleThinking?: (next: boolean) => void;
}

const TRIGGER_CLASS =
	"h-7 gap-1.5 bg-transparent rounded-lg px-2 text-[12px]! leading-none text-foreground hover:bg-interactive-active data-[state=open]:bg-interactive-active";
const SUB_CLASS = "chat-settings-menu min-w-48 text-[12px]!";
const DIVIDER = "mt-1 border-t border-border pt-1";

/** A variant-per-setting catalog as one model per family, with effort, thinking and speed as controls. */
function variantSource(
	models: PickerModel[],
	selectedId: string | undefined,
	fallbackLabel: string,
	onPick: ((variant: ModelVariant, label: string) => void) | undefined,
	t: TFunction,
): PickerSource {
	const families = collapseModelVariants(models);
	const selected = findModelVariant(families, selectedId);
	const family = selected?.family;
	const variant = selected?.variant;
	const choose = onPick && family
		? (want: Partial<ModelVariant>) => onPick(pickModelVariant(family, { ...variant, ...want }), family.label)
		: undefined;
	return {
		models: families.map((entry) => ({ id: entry.id, label: entry.label, group: entry.group, sendId: entry.defaultVariant.id })),
		modelId: family?.id,
		sendId: variant?.id,
		onPickSetup: onPick && ((setup) => {
			const found = findModelVariant(families, setup.model);
			if (found) onPick(found.variant, found.family.label);
		}),
		modelLabel: family?.label ?? fallbackLabel,
		onPickModel: onPick && ((id) => {
			const next = families.find((entry) => entry.id === id);
			if (next) onPick(next.defaultVariant, next.label);
		}),
		efforts: (family?.efforts ?? []).map((value) => ({ value, label: formatEffortLabel(value, t) })),
		effort: variant?.effort,
		onPickEffort: choose ? (effort) => choose({ effort }) : undefined,
		fast: family?.hasFast ? Boolean(variant?.fast) : undefined,
		onToggleFast: choose ? (fast) => choose({ fast }) : undefined,
		thinking: family?.hasThinkingChoice ? Boolean(variant?.thinking) : undefined,
		onToggleThinking: choose ? (thinking) => choose({ thinking }) : undefined,
	};
}

export function AgentModelPicker({
	currentHarness,
	current,
	agentSwitch,
	disabled,
	loading,
	loadingModels,
	savedModel,
	title,
	mark,
	extras,
	terminal = false,
	pendingFooter,
	onCloseAutoFocus,
}: {
	/** Shown under the settings of the agent picked to switch to, such as its Switch and Cancel buttons. */
	pendingFooter?: ReactNode;
	currentHarness: string;
	current: PickerSource;
	agentSwitch: AgentSwitchProps;
	/** Over a terminal session, where AO cannot change the running agent's model: the control names it and only offers a switch. */
	terminal?: boolean;
	/** The current agent's own settings cannot be changed right now. */
	disabled?: boolean;
	/** The running agent is answering a settings change; its other settings are not known yet. */
	loading?: boolean;
	/** The agent is starting again, so its current models are not known yet. */
	loadingModels?: boolean;
	/** The model the session last ran, named while its current models load. */
	savedModel?: string;
	title?: string;
	/** A flag on the trigger, such as a substituted model. */
	mark?: ReactNode;
	/** The current agent's other settings, as menu rows under its effort. */
	extras?: ReactNode;
	onCloseAutoFocus?: (event: Event) => void;
}) {
	const { t } = useTranslation();
	const { pending, onPendingChange, projectId, hostId } = agentSwitch;
	const readiness = useAgentReadinessQuery(true, hostId).data?.agents;
	// An agent can be ready in a terminal and still miss the adapter its chat
	// needs (Pi's pi-acp). The daemon checks that without starting anything.
	const chatUnavailable = useQuery({
		queryKey: ["chat-unavailable-agents", hostId ?? ""],
		queryFn: async () => {
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/agents/chat/unavailable", {
				params: { query: { agents: CHAT_SWITCH_TARGETS.join(",") } },
			});
			if (error) throw error;
			return new Set(data.agents.map((entry) => entry.agent));
		},
		enabled: !terminal,
		staleTime: 60_000,
	}).data;
	// Only runnable agents are offered, as in New Task; unknown readiness stays selectable
	// since the daemon checks the target first. A terminal's own agent is left out (/model there).
	const agents = useMemo(() => [
		...(terminal ? [] : [{ id: currentHarness, label: agentLabel(currentHarness) }]),
		...(terminal ? TERMINAL_SWITCH_TARGETS : CHAT_SWITCH_TARGETS).flatMap((id) => {
			if (id === currentHarness) return [];
			const agent = readiness?.find((candidate) => candidate.id === id);
			const label = agentLabel(id) !== id ? agentLabel(id) : agent?.label ?? id;
			if (!terminal && chatUnavailable?.has(id)) return [];
			return agent && !isLaunchableAgent(agent) ? [] : [{ id: id as string, label }];
		}),
	], [chatUnavailable, currentHarness, readiness, terminal]);
	const management = useAgentManagementMenu(undefined, hostId);
	const [open, setOpen] = useState(false);
	const favorites = useModelFavorites();
	const keepCurrent = () => { if (pending) onPendingChange(undefined); };
	// Cursor's command-line ids fold settings into the model; a running Cursor chat reports its own, kept as is.
	const live = listsModelVariants(currentHarness) && current.efforts.length === 0 && current.thinking === undefined
		? variantSource(current.models, current.modelId, current.modelLabel, current.onPickModel && ((variant) => current.onPickModel?.(variant.id)), t)
		: listsModelVariants(currentHarness) ? { ...current, models: groupModelsByVendor(current.models) } : current;
	const { settings: hostSettings } = useSettings(hostId);
	// The agent's list is shared by every session, so it names the saved model at once.
	// No saved model means the agent's own default, which Claude Code leaves unset.
	const savedCatalog = useQuery({ ...agentModelsQueryOptions(currentHarness, projectId, hostId), enabled: Boolean(loadingModels) }).data;
	// Until the agent reports its own models, its list fills the menu; picks wait for the agent.
	const starting = loadingModels && savedCatalog && !current.models.some((model) => model.id)
		? otherAgentSource(
			currentHarness,
			offeredCatalogModels(savedCatalog, hostSettings?.harnessDefaults?.[currentHarness]?.disabledModels, [savedModel]),
			savedModel ? { harness: currentHarness, model: savedModel } : undefined,
			() => {},
			t,
		)
		: undefined;
	const currentSource: PickerSource = disabled || starting
		? { ...(starting ?? live), onPickModel: undefined, onPickSetup: undefined, onPickEffort: undefined, onToggleFast: undefined, onToggleThinking: undefined }
		: live;
	// The other agents' model lists load once the menu is open, so search reaches every model.
	const others = agents.filter((agent) => agent.id !== currentHarness);
	const queryClient = useQueryClient();
	// Warmed on hover or focus, so the first keystrokes already search every agent.
	const warmCatalogs = () => {
		for (const agent of others) void queryClient.prefetchQuery(agentModelsQueryOptions(agent.id, projectId, hostId));
	};
	const catalogs = useQueries({
		queries: others.map((agent) => ({ ...agentModelsQueryOptions(agent.id, projectId, hostId), enabled: open })),
	});
	// A pending agent offers the models its own session will, so the catalog is narrowed to match.
	const otherSource = (harness: string, held?: PendingAgentSwitch) => otherAgentSource(
		harness,
		offeredCatalogModels(
			catalogs[others.findIndex((agent) => agent.id === harness)]?.data,
			hostSettings?.harnessDefaults?.[harness]?.disabledModels,
			[held?.model],
		),
		held, onPendingChange, t,
	);
	// The rows below always describe who answers next: the pending agent, or the running one.
	const shownHarness = pending?.harness ?? currentHarness;
	const source = pending ? otherSource(pending.harness, pending) : currentSource;
	const settable = !terminal || Boolean(pending);
	// A catalog that could not be read says why, instead of an empty model list.
	const shownCatalog = useQuery({ ...agentModelsQueryOptions(shownHarness, projectId, hostId), enabled: open && settable }).data;
	const modelsProblem = !settable || source.models.some((model) => model.id)
		? undefined
		: catalogProblem(shownCatalog);
	const defaultModel = savedCatalog?.models?.find((model) => model.isDefault);
	const savedLabel = !loadingModels || !savedCatalog ? undefined
		: savedModel && savedModel !== "default" ? recordedModelName(currentHarness, savedModel, savedCatalog.models ?? [])
		: defaultModel ? agentModelDisplayLabel(currentHarness, defaultModel.label) : undefined;
	// A bare agent pick starts on that agent's default model and effort, so those are named.
	const pendingDefaultModel = source.models.find((model) => model.hint === "default")?.label;
	const shownModel = pending
		? pending.modelLabel ?? (source.modelId ? source.modelLabel : pendingDefaultModel) ?? agentLabel(pending.harness)
		: savedLabel ?? currentSource.modelLabel;
	const pendingEffort = pending?.ultracode ? "ultracode" : pending?.effort ?? pending?.shownEffort ?? (pending ? source.effort : undefined);
	const shownEffort = pending
		? pendingEffort ? source.efforts.find((effort) => effort.value === pendingEffort)?.label ?? formatEffortLabel(pendingEffort, t) : undefined
		: currentSource.efforts.find((effort) => effort.value === currentSource.effort)?.label;
	const shownUltra = isUltraEffort(pending ? pendingEffort : currentSource.effort);
	const shownFast = pending ? pending.fast || pending.fastMode || source.fast : currentSource.fast;
	const shownThinking = pending ? pending.thinking ?? source.thinking : currentSource.thinking;
	const pickModel = (harness: string, id: string) => {
		if (harness !== currentHarness) return otherSource(harness, pending?.harness === harness ? pending : undefined).onPickModel?.(id);
		keepCurrent();
		currentSource.onPickModel?.(id);
	};
	const pickSetup = (favorite: ModelFavorite) => {
		if (favorite.harness === currentHarness) {
			keepCurrent();
			if (currentSource.onPickSetup) currentSource.onPickSetup(favorite);
			else currentSource.onPickModel?.(favorite.model);
		} else {
			onPendingChange({
				harness: favorite.harness, model: favorite.model, modelLabel: favorite.label, shownEffort: favorite.effort, fast: favorite.fast,
				// A variant id already carries its effort, thinking and speed.
				...(listsModelVariants(favorite.harness) ? { thinking: favorite.thinking } : { effort: favorite.effort, fastMode: favorite.fast }),
			});
		}
	};
	// A terminal's running agent cannot be given another model from here.
	const oneShot = (harness: string) => terminal && TERMINAL_ONE_SHOT_TARGETS.has(harness);
	const pickable = (harness: string) => (harness === currentHarness
		? !terminal && Boolean(currentSource.onPickModel)
		: !agentSwitch.disabled && !oneShot(harness) && agents.some((agent) => agent.id === harness));
	// A favourite whose model the agent no longer lists cannot be sent. Until
	// the agent's list loads, it is taken as offered.
	const offered = (favorite: ModelFavorite) => {
		const models = (favorite.harness === currentHarness ? currentSource.models : otherSource(favorite.harness).models).filter((model) => model.id);
		const names = (model: (typeof models)[number]) => [model.id, ...(model.aliases ?? [])];
		return models.length === 0 || models.some((model) => names(model).includes(favorite.row) || names(model).includes(favorite.model));
	};
	// A terminal's Switch button is in this menu, so a pick there must not close it.
	const stayOpen = (event: Event) => { if (terminal) event.preventDefault(); };
	const setups = favorites.filter((entry) => !terminal || entry.harness !== currentHarness);
	const chosenModel = pending ? pending.model : currentSource.sendId ?? currentSource.modelId;
	const searchRows = (open ? agents : []).flatMap((agent) => {
		const models = agent.id === currentHarness ? (terminal ? [] : currentSource.models) : otherSource(agent.id).models;
		return models.filter((model) => model.id).map((model) => ({
			id: `${agent.id} ${model.id}`, label: `${model.label} ${agentLabel(agent.id)}`, harness: agent.id, model,
		}));
	});
	// Rows are memoized, so a keystroke only renders the ones that appear or change.
	const pickRef = useRef<(event: Event, harness: string, id: string) => void>(() => {});
	pickRef.current = (event, harness, id) => { stayOpen(event); pickModel(harness, id); };
	const pickFromSearch = useCallback((event: Event, harness: string, id: string) => pickRef.current(event, harness, id), []);
	const modelValue = pending ? shownModel : source.modelLabel;
	const offersAutoCompact = compactsEarly(pending?.harness, pending?.model);

	return (
		// Not modal, so one click on the composer both closes the menu and focuses it.
		<OptionMenu open={open} onOpenChange={setOpen} modal={false}>
			<OptionMenuTrigger
				ref={management.triggerRef}
				disabled={disabled && agentSwitch.disabled}
				onPointerEnter={warmCatalogs}
				onFocus={warmCatalogs}
				aria-label={terminal ? t("agentSwitch.terminalAgent") : t("agentSwitch.pickerLabel")}
				title={title ?? (terminal ? t("agentSwitch.switchAnother") : t("agentSwitch.pickerLabel"))}
				className={cn(TRIGGER_CLASS, terminal ? "w-full justify-start" : undefined)}
				showCaret={!terminal}
				data-shortcut-target={terminal ? undefined : "model-picker"}
			>
				<ProviderMark provider={terminal ? currentHarness : shownHarness} className="size-3.5" />
				{loadingModels && !savedLabel && !pending && !terminal ? (
					<span className="flex items-center gap-1.5 text-muted-foreground">
						<Loader2 aria-hidden="true" className="size-3 animate-spin" />
						{t("settings.models.loading")}
					</span>
				) : <>
				<span className={cn("min-w-0 max-w-[28ch] truncate", shownUltra && !terminal ? "ultra-text" : undefined)}>
					{terminal ? agentLabel(currentHarness) : shownModel}
				</span>
				{terminal ? <ChevronRight aria-hidden="true" className="ml-auto size-icon-sm shrink-0 text-muted-foreground" /> : (
					<>
						{shownFast ? <Zap aria-label={t("agentSwitch.fastMode")} className="size-3 shrink-0 fill-current" /> : null}
						{shownThinking ? <Brain aria-label={t("agentSwitch.thinking")} className="size-3 shrink-0" /> : null}
						{shownEffort ? <span className={cn("shrink-0", shownUltra ? "ultra-text" : "text-muted-foreground")}>{shownEffort}</span> : null}
						{mark}
					</>
				)}
				</>}
			</OptionMenuTrigger>
			<OptionMenuContent align={terminal ? "start" : "end"} side={terminal ? "right" : "top"} className="chat-settings-menu w-64 text-[12px]!"
				onCloseAutoFocus={(event) => {
					management.onCloseAutoFocus(event);
					if (!event.defaultPrevented) onCloseAutoFocus?.(event);
				}}
			>
				<ModelMenuChoices models={searchRows} searchable searchPlaceholder={t("agentSwitch.search")}>
					{(matches, searchActiveID, optionID, query) => query ? matches.map((row) => (
						<SearchRow
							key={row.id}
							row={row}
							optionId={optionID?.(row.id)}
							active={row.harness === shownHarness && row.model.id === source.modelId}
							searchActive={row.id === searchActiveID}
							disabled={!pickable(row.harness)}
							onPick={pickFromSearch}
						/>
					)) : (
						<>
							{setups.map((favorite) => (
								<OptionMenuItem
									key={`${favorite.harness}:${favorite.row}`}
									active={favorite.harness === shownHarness && favorite.model === chosenModel}
									radio
									disabled={!pickable(favorite.harness) || !offered(favorite)}
									onSelect={(event) => { stayOpen(event); pickSetup(favorite); }}
									className="group/model text-xs text-foreground"
								>
									<ProviderMark provider={favorite.harness} className="mr-2 size-3.5" />
									<span className="min-w-0 truncate">{favorite.label}</span>
									{favorite.fast ? <Zap aria-label={t("agentSwitch.fastMode")} className="ml-1.5 size-3 shrink-0 fill-current" /> : null}
									{favorite.thinking ? <Brain aria-label={t("agentSwitch.thinking")} className="ml-1.5 size-3 shrink-0" /> : null}
									{favorite.effortLabel ? <span className="ml-1.5 shrink-0 text-muted-foreground">{favorite.effortLabel}</span> : null}
									<FavoriteStar starred onToggle={() => toggleModelFavorite(favorite)} />
								</OptionMenuItem>
							))}
							<div className={setups.length > 0 ? DIVIDER : undefined}>
								<OptionMenuSub>
									<OptionMenuSubTrigger label={t("agentSwitch.agent")} value={agentLabel(shownHarness)} />
									<OptionMenuSubContent scrollable className={SUB_CLASS}>
										<div className="model-menu-scroll max-h-[calc(var(--size-select-menu-max)-var(--space-2)*2)] overflow-y-auto overscroll-contain">
											{agents.map((agent) => (
												<OptionMenuItem
													key={agent.id}
													active={agent.id === shownHarness}
													radio
													disabled={agent.id !== currentHarness && agentSwitch.disabled}
													// Greyed rather than disabled, so the reason still shows on hover.
													aria-disabled={oneShot(agent.id) || undefined}
													title={oneShot(agent.id) ? t("agentSwitch.terminalOneShot", { agent: agent.label }) : undefined}
													onSelect={(event) => {
														// The menu stays open so the model and effort can be set too.
														event.preventDefault();
														if (oneShot(agent.id)) return;
														if (agent.id === currentHarness) keepCurrent();
														else if (agent.id !== pending?.harness) onPendingChange({ harness: agent.id });
													}}
													className={cn("text-xs text-foreground", oneShot(agent.id) && "opacity-50")}
												>
													<ProviderMark provider={agent.id} className="mr-2 size-3.5" />
													<span className="min-w-0 truncate">{agent.label}</span>
												</OptionMenuItem>
											))}
										</div>
										<OptionMenuItem className={cn(DIVIDER, "text-xs text-foreground")} onSelect={management.requestManagement}>
											{t("agentSelector.manage")}
										</OptionMenuItem>
									</OptionMenuSubContent>
								</OptionMenuSub>
								{settable && source.models.length > 0 ? (
									<OptionMenuSub>
										<OptionMenuSubTrigger label={t("agentSwitch.model")} value={modelValue} />
										<OptionMenuSubContent scrollable className={SUB_CLASS}>
											<ModelRows source={shownHarness === "claude-code" ? { ...source, models: claudeOrder(source.models) } : source} harness={shownHarness} />
										</OptionMenuSubContent>
									</OptionMenuSub>
								) : null}
								{modelsProblem ? (
									<OptionMenuItem disabled className="items-start gap-2 text-xs text-muted-foreground">
										<TriangleAlert aria-hidden="true" className="mt-0.5 size-3 shrink-0 text-destructive" />
										<span className="min-w-0 max-w-72 whitespace-normal break-words">
											{t("agentSwitch.modelsUnavailable", { agent: agentLabel(shownHarness), error: modelsProblem })}
										</span>
									</OptionMenuItem>
								) : null}
								{loading && !pending ? (
									<OptionMenuItem disabled className="gap-2 text-xs text-muted-foreground">
										<Loader2 aria-hidden="true" className="size-3 animate-spin" />
										{t("agentSwitch.loadingSettings")}
									</OptionMenuItem>
								) : null}
								{settable && !(loading && !pending) && source.efforts.length > 0 ? (
									<OptionMenuSub>
										<OptionMenuSubTrigger label={t("agentSwitch.effort")} value={source.efforts.find((choice) => choice.value === source.effort)?.label ?? t("agentSwitch.defaultModel")} />
										<OptionMenuSubContent className={SUB_CLASS}>
											{source.efforts.map((choice) => (
												<OptionMenuItem
													key={choice.value}
													active={choice.value === source.effort}
													radio
													disabled={!source.onPickEffort}
													onSelect={(event) => {
														event.preventDefault();
														if (choice.value !== source.effort) source.onPickEffort?.(choice.value);
													}}
													className="text-xs text-foreground"
												>
													{/* On its own span: the row's highlight would otherwise be clipped to the text. */}
													<span className={isUltraEffort(choice.value) ? "ultra-text" : undefined}>{choice.label}</span>
												</OptionMenuItem>
											))}
										</OptionMenuSubContent>
									</OptionMenuSub>
								) : null}
							</div>
							{settable && !(loading && !pending) && (source.fast !== undefined || source.thinking !== undefined) ? (
								<div className={DIVIDER}>
									<ToggleRow label={t("agentSwitch.fastMode")} checked={source.fast} onChange={source.onToggleFast} />
									<ToggleRow label={t("agentSwitch.thinking")} checked={source.thinking} onChange={source.onToggleThinking} />
								</div>
							) : null}
							{extras && !pending ? <div className={DIVIDER}>{extras}</div> : null}
							{offersAutoCompact ? (
								<div className={DIVIDER}>
									<AutoCompactRow
										value={pending?.autoCompactWindow}
										onChange={(value) => pending && onPendingChange({ ...pending, autoCompactWindow: value })}
										title={t("agentSwitch.autoCompactHint")}
									/>
								</div>
							) : null}
						</>
					)}
				</ModelMenuChoices>
				{pending ? pendingFooter : null}
			</OptionMenuContent>
		</OptionMenu>
	);
}

/** An on/off setting the agent has, as a menu row. The row owns the switch, so a click or Enter fires once. */
export function ToggleRow({ label, checked, onChange, className }: { label: string; checked?: boolean; onChange?: (next: boolean) => void; className?: string }) {
	if (checked === undefined) return null;
	return (
		<OptionMenuItem
			disabled={!onChange}
			onSelect={(event) => { event.preventDefault(); onChange?.(!checked); }}
			className={cn("justify-between gap-4 text-xs text-foreground", className)}
		>
			<span>{label}</span>
			<Switch aria-label={label} checked={checked} tabIndex={-1} />
		</OptionMenuItem>
	);
}

/** Why a catalog has no models, from its refresh error or warning: the first line, kept short. */
export function catalogProblem(catalog: Pick<AgentModelCatalog, "refreshError" | "warning"> | undefined): string | undefined {
	const text = (catalog?.refreshError || catalog?.warning)?.trim().split("\n")[0]?.trim();
	if (!text) return undefined;
	return text.length > 200 ? `${text.slice(0, 199)}…` : text;
}

/** Another agent's models and settings. Nothing changes until the switch runs; each pick is held as pending. */
function otherAgentSource(
	harness: string,
	catalog: AgentModelCatalog["models"] | undefined,
	pending: PendingAgentSwitch | undefined,
	onPick: (next: PendingAgentSwitch) => void,
	t: TFunction,
): PickerSource {
	const listed = (catalog ?? [])
		.filter((model) => !/^\[|\s/.test(model.id))
		.map((model) => ({
			id: model.id,
			label: readableModelLabel(agentModelDisplayLabel(harness, model.label)),
			group: modelGroupLabel(model.provider, harness),
			hint: model.isDefault ? "default" : undefined,
		}));
	// Two routes to one model read the same once shortened; their router tells them apart.
	const shared = new Set(listed.filter((entry, index) => listed.findIndex((other) => other.label === entry.label) !== index).map((entry) => entry.label));
	for (const entry of listed) {
		if (entry.id.includes("/") && shared.has(entry.label)) entry.label = `${entry.label} (${entry.id.slice(0, entry.id.indexOf("/"))})`;
	}
	if (listsModelVariants(harness)) {
		// With no model picked, the agent's default is shown as chosen; it is what starts.
		return variantSource(listed, pending?.model ?? listed.find((model) => model.hint === "default")?.id, agentLabel(harness), (variant, modelLabel) => onPick({
			harness, model: variant.id, modelLabel, shownEffort: variant.effort, fast: variant.fast, thinking: variant.thinking,
		}), t);
	}
	const chosen = pending ? pending.model ?? "" : undefined;
	// Effort levels belong to a model. With none picked they are the default's.
	const effortSource = catalog?.find((model) => (chosen ? model.id === chosen : model.isDefault)) ?? catalog?.[0];
	const defaultEffort = effortSource?.defaultEffort;
	const offersFast = Boolean(effortSource?.supportsFast);
	const fastOn = offersFast && Boolean(pending?.fastMode);
	const levels = (effortSource?.efforts ?? []).filter((value) => value && value.toLowerCase() !== "default");
	// Ultracode runs at Extra High, so it is only for models that have that level.
	const offersUltracode = harness === "claude-code" && levels.includes("xhigh");
	const keep = { harness, model: pending?.model, modelLabel: pending?.modelLabel, shownEffort: defaultEffort, fastMode: fastOn || undefined, autoCompactWindow: pending?.autoCompactWindow };
	return {
		// The plain row is only for an agent that lists no models.
		models: listed.length > 0 ? listed : [{ id: "", label: t("agentSwitch.defaultModel") }],
		modelId: chosen,
		modelLabel: pending?.modelLabel ?? agentLabel(harness),
		onPickModel: (id) => {
			// Fast mode carries over only to a model that also offers it.
			const next = catalog?.find((model) => (id ? model.id === id : model.isDefault));
			onPick({
				...keep,
				model: id || undefined,
				modelLabel: listed.find((model) => model.id === id)?.label,
				shownEffort: next?.defaultEffort,
				fastMode: Boolean(next?.supportsFast && pending?.fastMode) || undefined,
			});
		},
		efforts: [
			...levels.map((value) => ({ value, label: formatEffortLabel(value, t) })),
			...(offersUltracode ? [{ value: "ultracode", label: "Ultracode" }] : []),
		],
		effort: offersUltracode && pending?.ultracode ? "ultracode" : pending?.effort ?? defaultEffort,
		onPickEffort: (effort) => onPick(effort === "ultracode" ? { ...keep, effort: "xhigh", ultracode: true } : { ...keep, effort }),
		fast: offersFast ? fastOn : undefined,
		onToggleFast: (fast) => onPick({ ...keep, effort: pending?.effort, ultracode: pending?.ultracode, fastMode: fast || undefined }),
	};
}

/** Claude's models as New Task lists them: the newest of each family first, then the older ones. */
function claudeOrder<T extends { id: string; label: string }>(models: T[]): T[] {
	const { current, other } = splitClaudeModels(models);
	return [...current, ...other];
}

const SearchRow = memo(function SearchRow({ row, optionId, active, searchActive, disabled, onPick }: {
	row: { id: string; harness: string; model: PickerModel };
	optionId?: string;
	active: boolean;
	searchActive: boolean;
	disabled: boolean;
	onPick: (event: Event, harness: string, id: string) => void;
}) {
	return (
		<OptionMenuItem
			id={optionId}
			active={active}
			searchActive={searchActive}
			radio
			disabled={disabled}
			onSelect={(event) => onPick(event, row.harness, row.model.id)}
			className="text-xs text-foreground"
		>
			<ProviderMark provider={row.harness} className="mr-2 size-3.5" />
			<span className="min-w-0 truncate">{row.model.label}</span>
			<span className="ml-auto shrink-0 pl-3 text-muted-foreground">{agentLabel(row.harness)}</span>
		</OptionMenuItem>
	);
});

/** One agent's model list, with search on a long one and a star on each row. */
function ModelRows({ source, harness }: { source: PickerSource; harness: string }) {
	const { t } = useTranslation();
	const favorites = useModelFavorites();
	return (
		<ModelMenuChoices models={source.models} searchable={source.models.filter((model) => model.id).length > 10}>
			{(matches, searchActiveID, optionID) => matches.map((model, index) => {
				const active = model.id === source.modelId;
				const key = model.id || "default";
				return (
					<Fragment key={key}>
						{model.group && model.group !== matches[index - 1]?.group ? (
							<OptionMenuLabel>{model.group}</OptionMenuLabel>
						) : null}
						<OptionMenuItem
							id={optionID?.(key)}
							active={active}
							searchActive={model.id === searchActiveID}
							radio
							disabled={!source.onPickModel}
							onSelect={(event) => {
								// The menu stays open so effort and speed can be set too.
								event.preventDefault();
								if (!active) source.onPickModel?.(model.id);
							}}
							className="group/model text-xs text-foreground"
						>
							<span className="min-w-0 truncate">{model.label}</span>
							{model.hint ? <span className="ml-2 shrink-0 text-[10px] text-muted-foreground/70">{model.hint === "default" ? t("agentSwitch.defaultModel") : model.hint}</span> : null}
							{model.id ? (() => {
								const starredAs = favorites.find((entry) => entry.harness === harness && (entry.row === model.id || model.aliases?.includes(entry.row)));
								// Unstarring removes the favourite as it was saved, under whichever id.
								return <FavoriteStar starred={Boolean(starredAs)} onToggle={() => toggleModelFavorite(starredAs ?? favoriteFor(harness, model, active ? source : undefined))} />;
							})() : null}
						</OptionMenuItem>
					</Fragment>
				);
			})}
		</ModelMenuChoices>
	);
}

/** What starring a row saves: the chosen row with the settings it has now, any other row as the model on its own. */
function favoriteFor(harness: string, model: PickerModel, chosen: PickerSource | undefined): ModelFavorite {
	const plain = { harness, row: model.id, model: model.sendId ?? model.id, label: model.label };
	// Ultracode is asked for per message or at launch; it is not a level to restore.
	if (!chosen || isUltraEffort(chosen.effort) && harness === "claude-code") return plain;
	return {
		...plain,
		model: chosen.sendId ?? model.id,
		effort: chosen.effort,
		effortLabel: chosen.efforts.find((entry) => entry.value === chosen.effort)?.label,
		fast: chosen.fast || undefined,
		thinking: chosen.thinking || undefined,
	};
}

/** The star at the end of a model row. It shows on hover, and stays once starred. */
function FavoriteStar({ starred, onToggle }: { starred: boolean; onToggle: () => void }) {
	const { t } = useTranslation();
	return (
		<span
			role="button"
			tabIndex={-1}
			aria-label={starred ? t("agentSwitch.removeFavourite") : t("agentSwitch.addFavourite")}
			title={starred ? t("agentSwitch.removeFavourite") : t("agentSwitch.addFavourite")}
			// Starring must not also pick the model.
			onPointerDown={(event) => event.stopPropagation()}
			onPointerUp={(event) => event.stopPropagation()}
			onClick={(event) => { event.preventDefault(); event.stopPropagation(); onToggle(); }}
			className={cn(
				"ml-auto flex size-5 shrink-0 items-center justify-center rounded hover:bg-interactive-active",
				starred ? "text-warning" : "text-muted-foreground opacity-0 group-hover/model:opacity-100 group-data-highlighted/model:opacity-100",
			)}
		>
			<Star aria-hidden="true" className={cn("size-3.5", starred ? "fill-current" : undefined)} />
		</span>
	);
}

/** The top level some agents have past Max: Codex's "ultra", Claude Code's "ultracode". */
function isUltraEffort(value: string | undefined): boolean {
	return Boolean(value?.toLowerCase().startsWith("ultra"));
}

export function DismissButton({ label, onClick, disabled, className }: { label: string; onClick: () => void; disabled?: boolean; className?: string }) {
	return (
		<button type="button" aria-label={label} title={label} disabled={disabled} onClick={onClick}
			className={cn("shrink-0 rounded p-0.5 text-muted-foreground hover:bg-interactive-active hover:text-foreground", className)}>
			<X aria-hidden="true" className="size-3.5" />
		</button>
	);
}

/** An error from a switch, shown as a strip on top of the chat box. */
export function ComposerErrorNotice(
	{ title, message, onDismiss, dismissLabel }: { title: string; message: string; onDismiss: () => void; dismissLabel?: string },
) {
	const { t } = useTranslation();
	return (
		<div
			role="alert"
			className="-mb-2 flex min-w-0 items-start gap-2 rounded-t-(--radius-chat-composer) border-b border-destructive/30 bg-destructive/10 px-3 py-2 text-xs"
		>
			<TriangleAlert aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-destructive" />
			<div className="min-w-0 flex-1">
				<p className="font-medium text-foreground">{title}</p>
				<p className="break-words text-muted-foreground">{message}</p>
			</div>
			<DismissButton label={dismissLabel ?? t("agentSwitch.dismiss")} onClick={onDismiss} />
		</div>
	);
}

export interface AutoCompactControl {
	/** Unset leaves it to Claude Code. */
	value?: TurnSettings["autoCompactWindow"];
	/** The agent is working or already restarting. */
	disabled?: boolean;
	onChange: (value: TurnSettings["autoCompactWindow"]) => void;
}

const AUTO_COMPACT_CHOICES = [{ value: undefined, label: "Auto" }, { value: "200k", label: "200k" }, { value: "1m", label: "1M" }] as const;

/** A menu row: where Claude Code compacts on its own. */
export function AutoCompactRow({ value, disabled, onChange, title }: AutoCompactControl & { title: string }) {
	const { t } = useTranslation();
	return (
		<div className="flex h-9 items-center gap-2 px-3 text-xs" title={title}>
			<span className="text-muted-foreground">{t("agentSwitch.autoCompact")}</span>
			<span role="radiogroup" aria-label={t("agentSwitch.autoCompact")} className="ml-auto flex rounded-md bg-interactive-active p-0.5">
				{AUTO_COMPACT_CHOICES.map((choice) => (
					<button
						key={choice.label}
						type="button"
						role="radio"
						aria-checked={value === choice.value}
						disabled={disabled || value === choice.value}
						onClick={() => onChange(choice.value)}
						className={cn(
							"rounded px-2 py-0.5 disabled:cursor-default",
							value === choice.value ? "bg-settings-menu text-foreground" : "text-muted-foreground hover:text-foreground",
						)}
					>
						{choice.value ? choice.label : t("agentSwitch.autoCompactAuto")}
					</button>
				))}
			</span>
		</div>
	);
}
