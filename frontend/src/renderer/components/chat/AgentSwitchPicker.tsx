/**
 * One menu for who answers next: saved setups, Agent/Model/Effort rows, then toggles.
 * The running agent's settings apply at once; another agent is only held as pending
 * until the next message (or Switch, in a terminal), so browsing never restarts anything.
 */

import { useQuery } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import { Brain, ChevronRight, Loader2, Settings2, Star, TriangleAlert, Zap } from "@/components/app-icons";
import { Fragment, memo, useCallback, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { agentModelsQueryOptions, type AgentModelCatalog } from "../../hooks/useAgentModelsQuery";
import { useAgentManagementMenu } from "../../hooks/useAgentManagementMenu";
import { useAgentReadinessQuery } from "../../hooks/useAgentReadinessQuery";
import { useEdgeFade } from "../../hooks/useEdgeFade";
import { useSettings } from "../../hooks/useSettings";
import { agentModelDisplayLabel, mergeSameNamedModels, offeredCatalogModels, recordedModelName, splitClaudeModels } from "../../lib/agent-model-choices";
import { agentLabel } from "../../lib/agent-options";
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
import { Button, GHOST_PILL_CLASS } from "../ui/button";
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
import { CloseButton } from "../ui/close-button";
import { Pressable } from "../ui/pressable";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "../ui/tooltip";
import { ModelMenuChoices } from "./ModelMenuChoices";
import { ProviderMark } from "./ProviderMark";
import { useCloudAgentCatalogOptions } from "../../hooks/useCloudModelCatalog";

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
	/**
	 * A Cloud task's organization: the agents' models are Cloud's for the
	 * account (the ones its sessions run), not this computer's.
	 */
	cloudOrgId?: string;
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
	/**
	 * The agents to offer in place of the chat switch targets, for choosing one before a session exists.
	 * One with `unavailable` (why: not installed, not signed in) is shown dimmed and opens its setup.
	 */
	agents?: Array<{ id: string; label: string; unavailable?: string }>;
	/** Which agents settings "Manage agents" opens: a Cloud session's are the Cloud ones. */
	harnessView?: "local" | "cloud";
	account?: AccountChoice;
}

export interface AccountChoice {
	options: Array<{ id: string; label: string; disabled?: boolean }>;
	value?: string;
	onPick: (id: string) => void;
	disabled?: boolean;
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
	/** Why the row cannot be picked now (the installed agent CLI is too old); shown under its name. */
	unavailableReason?: string;
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

const TRIGGER_CLASS = cn(
	"h-7 gap-1.5 bg-transparent rounded-lg px-2 text-[12px]! leading-none text-foreground",
	GHOST_PILL_CLASS,
);
const SUB_CLASS = "chat-settings-menu min-w-48 text-[12px]!";
// Model rows shown before the list scrolls, so a long catalog never stretches the menu.
const VISIBLE_MODEL_ROWS = 3;
const DIVIDER = "mt-1 border-t border-border pt-1";
// Shared with the review controls' reviewer picker, which is laid out like this menu.
export { TRIGGER_CLASS as CHAT_PICKER_TRIGGER_CLASS, SUB_CLASS as CHAT_PICKER_SUB_CLASS, DIVIDER as CHAT_PICKER_DIVIDER };

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
	const cloudCatalogOptions = useCloudAgentCatalogOptions(agentSwitch.cloudOrgId);
	// Each agent's model list: Cloud's for a Cloud task, else this computer's.
	// Both resolve to the pickers' catalog shape (Cloud's through its select),
	// which is all the queries below read.
	const catalogOptions = (harness: string) =>
		(cloudCatalogOptions(harness) ?? agentModelsQueryOptions(harness, projectId, hostId)) as ReturnType<typeof agentModelsQueryOptions>;
	// The agents to offer before a session exists, in place of the chat switch targets.
	const choices = agentSwitch.agents;
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
	// Every agent is on the rail in one fixed order, so picking one never moves it (on /new a
	// pick changes the running agent at once). Runnable ones can be picked, as in New Task;
	// unknown readiness stays selectable since the daemon checks the target first. One that is
	// not installed or signed in, or has no chat here, is dimmed after them and opens its setup
	// in Settings, rather than missing with no word why. The running agent is never dimmed, and
	// leads the rail only when the list does not name it.
	const railAgents = useMemo<RailAgent[]>(() => {
		const listed: RailAgent[] = choices
			? choices.map((agent) => (agent.id === currentHarness ? { id: agent.id, label: agent.label } : agent))
			: (terminal ? TERMINAL_SWITCH_TARGETS : CHAT_SWITCH_TARGETS).map((id) => {
				const agent = readiness?.find((candidate) => candidate.id === id);
				const label = agentLabel(id) !== id ? agentLabel(id) : agent?.label ?? id;
				if (id === currentHarness) return { id: id as string, label };
				const unavailable = agent?.installation.state === "not_installed" ? t("agentSwitch.notInstalled")
					: agent?.authentication.state === "unauthorized" ? t("agentSwitch.notSignedIn")
					: !terminal && chatUnavailable?.has(id) ? t("agentSwitch.chatUnavailable")
					: undefined;
				return { id: id as string, label, unavailable };
			});
		const ordered = [...listed.filter((agent) => !agent.unavailable), ...listed.filter((agent) => agent.unavailable)];
		return ordered.some((agent) => agent.id === currentHarness)
			? ordered
			: [{ id: currentHarness, label: agentLabel(currentHarness) }, ...ordered];
	}, [chatUnavailable, choices, currentHarness, readiness, t, terminal]);
	// Only agents that can be used are on the rail: one that is not installed or not signed in is
	// left out rather than dimmed. The agent that is answering stays, so its own rail never loses
	// the selection, and it still says why it cannot be switched to.
	const agents = useMemo(
		() => railAgents.filter((agent) => !agent.unavailable || agent.id === currentHarness),
		[railAgents, currentHarness],
	);
	const management = useAgentManagementMenu(undefined, hostId, agentSwitch.harnessView);
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
	const savedCatalog = useQuery({ ...catalogOptions(currentHarness), enabled: Boolean(loadingModels) }).data;
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
	// A pending agent's models are read once, for the agent that is shown.
	const pendingCatalog = useQuery({ ...catalogOptions(pending?.harness ?? ""), enabled: open && Boolean(pending) });
	// A pending agent offers the models its own session will, so the catalog is narrowed to match.
	const otherSource = (harness: string, held?: PendingAgentSwitch) => otherAgentSource(
		harness,
		offeredCatalogModels(
			pendingCatalog.data,
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
	const shownCatalog = useQuery({ ...catalogOptions(shownHarness), enabled: open && settable }).data;
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
		// The id a favourite sends decides: a row it was folded into may be runnable while it is not.
		if (models.some((model) => model.unavailableReason && (model.sendId ?? model.id) === favorite.model)) return false;
		return models.length === 0 || models.some((model) => !model.unavailableReason && (names(model).includes(favorite.row) || names(model).includes(favorite.model)));
	};
	// A terminal's Switch button is in this menu, so a pick there must not close it.
	const stayOpen = (event: Event) => { if (terminal) event.preventDefault(); };
	// The list shows the chosen agent's favourites and models only; agents are picked on the rail.
	const setups = settable ? favorites.filter((entry) => entry.harness === shownHarness) : [];
	const chosenModel = pending ? pending.model : currentSource.sendId ?? currentSource.modelId;
	// Search narrows within the agent shown: the rail above is where an agent is chosen.
	const shownModels = shownHarness === "claude-code" ? claudeOrder(mergeSameNamedModels(source.models, source.modelId)) : source.models;
	const browseRows = open && settable
		? shownModels.map((model) => ({ id: `${shownHarness} ${model.id}`, label: model.label, harness: shownHarness, model }))
		: [];
	// Rows are memoized, so a keystroke only renders the ones that appear or change.
	const pickRef = useRef<(event: Event, harness: string, id: string) => void>(() => {});
	pickRef.current = (event, harness, id) => { stayOpen(event); pickModel(harness, id); };
	const pickFromSearch = useCallback((event: Event, harness: string, id: string) => pickRef.current(event, harness, id), []);
	const pickAgent = (id: string) => {
		if (id === currentHarness) keepCurrent();
		else if (id !== pending?.harness) onPendingChange({ harness: id });
	};
	// Seen on the rail but not pickable from it now: a terminal's one-shot agent, or any other while switching is refused.
	const blockedReason = (id: string): string | undefined => {
		if (id === currentHarness) return undefined;
		if (oneShot(id)) return t("agentSwitch.terminalOneShot", { agent: agentLabel(id) });
		return agentSwitch.disabled ? "" : undefined;
	};
	const offersAutoCompact = compactsEarly(pending?.harness, pending?.model);
	// Only while there is nothing to show yet: a reload after a pick keeps the rows in place.
	const waiting = loading && !pending && source.efforts.length === 0 && source.fast === undefined && source.thinking === undefined;
	const showAccount = Boolean(agentSwitch.account && !pending && agentSwitch.account.options.length > 1);

	// The chosen agent's settings, under its models and kept in view while they scroll.
	const settingsRows = [
		waiting ? (
			<OptionMenuItem key="waiting" disabled className="gap-2 text-xs text-muted-foreground">
				<Loader2 aria-hidden="true" className="size-3 animate-spin" />
				{t("agentSwitch.loadingSettings")}
			</OptionMenuItem>
		) : null,
		settable && !waiting && source.efforts.length > 0 ? (
			<OptionMenuSub key="effort" closeOnSelect>
				<OptionMenuSubTrigger label={t("agentSwitch.effort")} value={source.efforts.find((choice) => choice.value === source.effort)?.label ?? t("agentSwitch.defaultModel")} />
				<OptionMenuSubContent align="start" alignOffset={0} className={SUB_CLASS}>
					{source.efforts.map((choice) => (
						<OptionMenuItem
							key={choice.value}
							active={choice.value === source.effort}
							radio
							disabled={!source.onPickEffort}
							onSelect={() => {
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
		) : settable && !waiting && !modelsProblem ? (
			// The same row, disabled, so the menu does not change shape between models.
			<OptionMenuSub key="effort" closeOnSelect>
				<OptionMenuSubTrigger disabled className="opacity-50" label={t("agentSwitch.effort")} value={t("agentSwitch.defaultModel")} />
			</OptionMenuSub>
		) : null,
		// One account has nothing to switch to, so the row is only for two or more.
		showAccount && agentSwitch.account ? <AccountRows key="account" account={agentSwitch.account} /> : null,
	].filter(Boolean);
	const settingsGroups = [
		settingsRows.length > 0 ? settingsRows : null,
		settable && !waiting && (source.fast !== undefined || source.thinking !== undefined) ? (
			<>
				<ToggleRow label={t("agentSwitch.fastMode")} checked={source.fast} onChange={source.onToggleFast} />
				<ToggleRow label={t("agentSwitch.thinking")} checked={source.thinking} onChange={source.onToggleThinking} />
			</>
		) : null,
		extras && !pending ? extras : null,
		offersAutoCompact ? (
			<AutoCompactRow
				value={pending?.autoCompactWindow}
				onChange={(value) => pending && onPendingChange({ ...pending, autoCompactWindow: value })}
				title={t("agentSwitch.autoCompactHint")}
			/>
		) : null,
	].filter(Boolean);
	const settingsFooter = settingsGroups.length > 0
		? settingsGroups.map((group, index) => <div key={index} className={DIVIDER}>{group}</div>)
		: undefined;

	return (
		// Not modal, so one click on the composer both closes the menu and focuses it.
		<OptionMenu open={open} onOpenChange={setOpen} modal={false}>
			<OptionMenuTrigger variant="ghost"
				ref={management.triggerRef}
				disabled={disabled && agentSwitch.disabled}
				aria-label={terminal ? t("agentSwitch.terminalAgent") : t("agentSwitch.pickerLabel")}
				title={title ?? (terminal ? t("agentSwitch.switchAnother") : t("agentSwitch.pickerLabel"))}
				className={cn(TRIGGER_CLASS, terminal ? "w-full justify-start" : undefined)}
				showCaret={false}
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
			{/* Pinned to the trigger's left edge, which stays put while its label changes width.
			    The menu sizes to its content: the model list is capped at a few rows, so a long
			    catalog scrolls inside the list instead of stretching the menu. */}
			<OptionMenuContent align="start" side={terminal ? "right" : "top"} steady
				className="chat-settings-menu w-64 text-[12px]!"
				onCloseAutoFocus={(event) => {
					management.onCloseAutoFocus(event);
					if (!event.defaultPrevented) onCloseAutoFocus?.(event);
				}}
			>
				<ModelMenuChoices
					models={browseRows}
					browseKey={shownHarness}
					selectedId={source.modelId === undefined ? undefined : `${shownHarness} ${source.modelId}`}
					searchable
					searchPlaceholder={t("agentSwitch.search")}
					rows={VISIBLE_MODEL_ROWS + setups.length}
					header={({ clearSearch }) => (
						<AgentRail
							agents={agents}
							selected={shownHarness}
							blockedReason={blockedReason}
							onPick={(id) => { clearSearch(); pickAgent(id); }}
							onSetup={(id) => { management.requestAgentSetup(id); setOpen(false); }}
							onManage={() => { management.requestManagement(); setOpen(false); }}
						/>
					)}
					footer={settingsFooter}
				>
					{(matches, searchActiveID, optionID, query) => query ? matches.map((row) => (
						// Every match belongs to the agent shown, so a pick never changes the agent.
						<SearchRow
							key={row.id}
							row={row}
							optionId={optionID?.(row.id)}
							active={row.model.id === source.modelId}
							searchActive={row.id === searchActiveID}
							disabled={!pickable(row.harness) || Boolean(row.model.unavailableReason)}
							onPick={pickFromSearch}
						/>
					)) : (
						<>
							{setups.map((favorite) => (
								<OptionMenuItem
									key={`${favorite.harness}:${favorite.row}`}
									active={favorite.model === chosenModel}
									radio
									disabled={!pickable(favorite.harness) || !offered(favorite)}
									onSelect={(event) => { stayOpen(event); pickSetup(favorite); }}
									className="group/model text-xs text-foreground"
								>
									<span className="min-w-0 truncate">{favorite.label}</span>
									{favorite.fast ? <Zap aria-label={t("agentSwitch.fastMode")} className="ml-1.5 size-3 shrink-0 fill-current" /> : null}
									{favorite.thinking ? <Brain aria-label={t("agentSwitch.thinking")} className="ml-1.5 size-3 shrink-0" /> : null}
									{favorite.effortLabel ? <span className="ml-1.5 shrink-0 text-muted-foreground">{favorite.effortLabel}</span> : null}
									<FavoriteStar starred onToggle={() => toggleModelFavorite(favorite)} />
								</OptionMenuItem>
							))}
							{matches.length > 0 ? (
								<div className={setups.length > 0 ? DIVIDER : undefined}>
									{matches.map((row, index) => {
										const model = row.model;
										const active = model.id === source.modelId;
										const starredAs = model.id ? favorites.find((entry) => entry.harness === shownHarness && (entry.row === model.id || model.aliases?.includes(entry.row))) : undefined;
										return (
											<Fragment key={row.id}>
												{model.group && model.group !== matches[index - 1]?.model.group ? (
													<OptionMenuLabel>{model.group}</OptionMenuLabel>
												) : null}
												<OptionMenuItem
													active={active}
													radio
													disabled={!source.onPickModel || Boolean(model.unavailableReason)}
													// The menu stays open, with the model's effort right under it.
													onSelect={(event) => {
														event.preventDefault();
														if (!active) pickModel(shownHarness, model.id);
													}}
													className="group/model text-xs text-foreground"
												>
													{model.unavailableReason ? (
														<span className="min-w-0">
															<span className="block truncate">{model.label}</span>
															<span className="block whitespace-normal text-[10px] text-muted-foreground">{model.unavailableReason}</span>
														</span>
													) : <span className="min-w-0 truncate">{model.label}</span>}
													{model.hint ? <span className="ml-2 shrink-0 text-[10px] text-muted-foreground/70">{model.hint === "default" ? t("agentSwitch.defaultModel") : model.hint}</span> : null}
													{/* Unstarring removes the favourite as it was saved, under whichever id. */}
													{model.id ? <FavoriteStar starred={Boolean(starredAs)} onToggle={() => toggleModelFavorite(starredAs ?? favoriteFor(shownHarness, model, active ? source : undefined))} /> : null}
												</OptionMenuItem>
											</Fragment>
										);
									})}
								</div>
							) : null}
							{!settable ? (
								<p className="px-2 py-1.5 text-xs text-muted-foreground">{t("agentSwitch.switchAnother")}</p>
							) : null}
							{modelsProblem ? (
								<OptionMenuItem disabled className="items-start gap-2 text-xs text-muted-foreground">
									<TriangleAlert aria-hidden="true" className="mt-0.5 size-3 shrink-0 text-destructive" />
									<span className="min-w-0 max-w-72 whitespace-normal break-words">
										{t("agentSwitch.modelsUnavailable", { agent: agentLabel(shownHarness), error: modelsProblem })}
									</span>
								</OptionMenuItem>
							) : null}
						</>
					)}
				</ModelMenuChoices>
				{pending ? pendingFooter : null}
			</OptionMenuContent>
		</OptionMenu>
	);
}

/** An agent on the rail. `unavailable` says why it cannot be picked (not installed, not signed in); it opens its setup. */
interface RailAgent {
	id: string;
	label: string;
	unavailable?: string;
}

const RAIL_ITEM_CLASS = cn(
	"flex size-7 shrink-0 items-center justify-center rounded-md",
	GHOST_PILL_CLASS,
	// The chosen agent keeps the pressed pill: a quiet fill, no border.
	"aria-checked:before:inset-0 aria-checked:before:opacity-100 aria-checked:before:bg-interactive-active",
	// Inset, as the rail clips anything drawn outside its items.
	"focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring",
);

/**
 * The agents as a row of logos under the search: choosing one lists its models below.
 * Left and Right move along it (choosing as they go, as in any radio group), Home and End
 * jump to its ends. A rail longer than the menu scrolls sideways under an edge fade.
 */
function AgentRail({ agents, selected, blockedReason, onPick, onSetup, onManage }: {
	agents: RailAgent[];
	selected: string;
	/** Why an agent cannot be chosen right now; "" for no stated reason, undefined when it can. */
	blockedReason: (id: string) => string | undefined;
	onPick: (id: string) => void;
	onSetup: (id: string) => void;
	onManage: () => void;
}) {
	const { t } = useTranslation();
	const scrollRef = useRef<HTMLDivElement>(null);
	useEdgeFade(scrollRef, "x");
	const chosenIndex = agents.findIndex((agent) => agent.id === selected);
	// The chosen agent in view as the menu opens, however far along the rail it sits. The rail's
	// own scroll is set, not scrollIntoView, which would also scroll the menu and the page.
	useLayoutEffect(() => {
		const rail = scrollRef.current;
		const chosen = rail?.querySelector<HTMLElement>('[aria-checked="true"]');
		if (!rail || !chosen || chosenIndex <= 0) return;
		const end = chosen.offsetLeft + chosen.offsetWidth;
		if (end > rail.clientWidth) rail.scrollLeft = end - rail.clientWidth;
		// Only on open: a pick is already in view.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, []);
	const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
		if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
		const items = Array.from(event.currentTarget.querySelectorAll<HTMLElement>("[data-rail-item]"));
		const index = items.indexOf(document.activeElement as HTMLElement);
		if (index < 0) return;
		event.preventDefault();
		event.stopPropagation();
		const next = items[
			event.key === "Home" ? 0
			: event.key === "End" ? items.length - 1
			: (index + (event.key === "ArrowRight" ? 1 : -1) + items.length) % items.length
		];
		next.focus();
		// Choosing follows focus; an agent that cannot be chosen is only focused.
		const id = next.dataset.agent;
		if (id && next.getAttribute("aria-disabled") !== "true" && id !== selected) onPick(id);
	};
	return (
		<TooltipProvider delayDuration={400} skipDelayDuration={300}>
			<div className="flex items-center gap-1" onKeyDown={onKeyDown}>
				<div
					ref={scrollRef}
					role="radiogroup"
					aria-label={t("agentSwitch.agents")}
					className="edge-fade edge-fade-x relative flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto overscroll-x-contain [--edge-fade-size:16px] [scrollbar-width:none]"
					// A mouse wheel scrolls the rail along, as it has no vertical travel.
					onWheel={(event) => {
						if (Math.abs(event.deltaY) > Math.abs(event.deltaX)) event.currentTarget.scrollLeft += event.deltaY;
					}}
				>
					{agents.map((agent, index) => {
						const checked = agent.id === selected;
						const blocked = blockedReason(agent.id);
						const reason = agent.unavailable ?? blocked;
						const name = reason ? t("agentSwitch.agentWithReason", { agent: agent.label, reason }) : agent.label;
						return (
							<Tooltip key={agent.id}>
								<TooltipTrigger asChild>
									<Pressable
										role="radio"
										aria-checked={checked}
										// Dimmed rather than disabled, so the tooltip still says why.
										aria-disabled={agent.unavailable !== undefined || blocked !== undefined || undefined}
										aria-label={name}
										tabIndex={checked || (chosenIndex < 0 && index === 0) ? 0 : -1}
										data-rail-item
										data-agent={agent.id}
										onClick={() => {
											if (agent.unavailable) onSetup(agent.id);
											else if (blocked === undefined && !checked) onPick(agent.id);
										}}
										className={cn(RAIL_ITEM_CLASS, (agent.unavailable || blocked !== undefined) && "opacity-40")}
									>
										<ProviderMark provider={agent.id} className="size-4" />
									</Pressable>
								</TooltipTrigger>
								<TooltipContent side="bottom" sideOffset={4} hideArrow>{name}</TooltipContent>
							</Tooltip>
						);
					})}
				</div>
				<Tooltip>
					<TooltipTrigger asChild>
						<Button variant="quiet" size="icon-xs" tabIndex={-1} data-rail-item aria-label={t("agentSelector.manage")} onClick={onManage}>
							<Settings2 aria-hidden="true" className="size-icon-sm" />
						</Button>
					</TooltipTrigger>
					<TooltipContent side="bottom" sideOffset={4} hideArrow>{t("agentSelector.manage")}</TooltipContent>
				</Tooltip>
			</div>
		</TooltipProvider>
	);
}

function AccountRows({ account }: { account: AccountChoice }) {
	const { t } = useTranslation();
	const chosen = account.options.find((option) => option.id === account.value);
	return (
		<OptionMenuSub closeOnSelect>
			<OptionMenuSubTrigger disabled={account.disabled} className={account.disabled ? "opacity-50" : undefined} label={t("settings.agentAccounts.sessionAccount")} value={chosen?.label ?? ""} />
			<OptionMenuSubContent scrollable className={SUB_CLASS}>
				<div className="model-menu-scroll max-h-[calc(var(--size-select-menu-max)-var(--space-2)*2)] overflow-y-auto overscroll-contain">
					{account.options.map((option) => (
						<OptionMenuItem
							key={option.id}
							active={option.id === account.value}
							radio
							disabled={option.disabled}
							onSelect={() => { if (option.id !== account.value) account.onPick(option.id); }}
							className="text-xs text-foreground"
						>
							<span className="min-w-0 truncate">{option.label}</span>
						</OptionMenuItem>
					))}
				</div>
			</OptionMenuSubContent>
		</OptionMenuSub>
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
			unavailableReason: model.unavailableReason,
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

/**
 * Claude's models: the newest of each family and its context variants first, in Fable, Opus,
 * Sonnet, Haiku order, then the older releases. The older ones sit below the few rows the menu
 * shows, so they cost no height but a scroll or a search still reaches them.
 */
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
	const { t } = useTranslation();
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
			<span className="min-w-0 truncate" title={row.model.unavailableReason}>{row.model.label}</span>
			{/* Drawn as the group's heading; read with the row, so a match names its agent. */}
			<span className="sr-only">{t("agentSwitch.favoriteAgent", { agent: agentLabel(row.harness) })}</span>
		</OptionMenuItem>
	);
});

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
function FavoriteStar({ starred, onToggle, className }: { starred: boolean; onToggle: () => void; className?: string }) {
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
				className,
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
		<CloseButton size="icon-xs" aria-label={label} title={label} disabled={disabled} onClick={onClick} className={cn("-my-1", className)} />
	);
}

/** An error from a switch, shown as a strip on top of the chat box. */
export function ComposerErrorNotice(
	{ title, message, action, onDismiss, dismissLabel }: { title: string; message: ReactNode; action?: ReactNode; onDismiss: () => void; dismissLabel?: string },
) {
	const { t } = useTranslation();
	return (
		<div
			role="alert"
			data-composer-notice
			className="flex min-w-0 items-start gap-2 rounded-t-(--radius-chat-composer) border-b border-destructive/30 bg-destructive/10 px-3 py-2 text-xs"
		>
			<TriangleAlert aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-destructive" />
			<div className="min-w-0 flex-1">
				<p className="font-medium text-foreground">{title}</p>
				<p className="break-words text-muted-foreground">{message}</p>
				{action}
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
					<Pressable
						key={choice.label}
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
					</Pressable>
				))}
			</span>
		</div>
	);
}
