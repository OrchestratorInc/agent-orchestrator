/**
 * One menu for who answers next: Agent/Model/Effort rows, then toggles.
 * The running agent's settings apply at once; another agent is only held as pending
 * until the next message (or Switch, in a terminal), so browsing never restarts anything.
 */

import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import { Brain, ChevronRight, DoubleZap, Loader2, Settings2, TriangleAlert, Zap, type AppIcon } from "@/components/app-icons";
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
import { rememberLastModel, useLastModels } from "../../lib/last-models";
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
import { uiUsage, useTelemetrySurface } from "../../lib/ui-usage-telemetry";
import { cn } from "../../lib/utils";
import { Button, GHOST_PILL_CLASS } from "../ui/button";
import type { ApprovalMode, TurnSettings } from "../../types/conversation";
import { formatEffortLabel } from "../settings/EffortPicker";
import {
	OptionMenu,
	OptionMenuContent,
	OptionMenuItem,
	OptionMenuLabel,
	OptionMenuTrigger,
} from "../ui/option-menu";
import { Switch } from "../ui/switch";
import { CloseButton } from "../ui/close-button";
import { Pressable } from "../ui/pressable";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "../ui/tooltip";
import { EffortSlider } from "./EffortSlider";
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
	/** Sent with the switch: Claude Code's compaction window, "200k" or "1m", or "off" for OpenCode's. */
	autoCompactWindow?: TurnSettings["autoCompactWindow"];
	/** The permission mode picked for the agent. Unset starts it in the chat's, or its strictest. */
	approvalMode?: ApprovalMode;
}

/** The effort a switch starts at: the one the picker shows. A Cursor variant id already carries it. */
export function switchEffort(pending: PendingAgentSwitch): string | undefined {
	return pending.effort ?? (listsModelVariants(pending.harness) ? undefined : pending.shownEffort);
}

/** How an agent's own compaction is set: Claude Code's window, or simply on or off. */
export type AutoCompactKind = "window" | "toggle";

/**
 * How AO can set an agent's own compaction, if it can: Claude Code's window on its larger-window
 * models, and OpenCode's on or off in a chat (a terminal's OpenCode reads only its own config).
 */
export function autoCompactKind(harness: string | undefined, model = "", terminal = false): AutoCompactKind | undefined {
	if (harness === "claude-code") return /haiku|opus-4-5/.test(model) ? undefined : "window";
	if (!terminal && (harness === "opencode" || harness === "opencode-v2")) return "toggle";
	return undefined;
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
	/** Claude Code's Ultracode: its own workflows at any effort level, asked for by a keyword in each message. */
	ultracode?: { on: boolean; onChange: (on: boolean) => void };
	/** When the running agent compacts on its own. Applies the next time it starts. */
	autoCompact?: AutoCompactControl;
	/**
	 * The agents to offer in place of the chat switch targets, for choosing one before a session exists.
	 * One with `unavailable` (why: not installed, not signed in) is left off the rail, unless it is the
	 * running or pending agent; then it shows dimmed with that reason and opens its setup.
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
	/** Other ids the agent lists this same model under; a model remembered under one is this row. */
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
	efforts: Array<{ value: string; label: string }>;
	effort?: string;
	onPickEffort?: (value: string) => void;
	/** Undefined when the agent has no speed setting. */
	fast?: boolean;
	onToggleFast?: (next: boolean) => void;
	/** Undefined when the model has no thinking choice. */
	thinking?: boolean;
	onToggleThinking?: (next: boolean) => void;
	/**
	 * Claude Code's Ultracode: a mode beside the effort level, not one of its levels, and it works at any
	 * of them. Undefined where it is not offered.
	 */
	ultracode?: boolean;
	onToggleUltracode?: (next: boolean) => void;
}

const TRIGGER_CLASS = cn(
	"h-7 gap-1.5 bg-transparent rounded-lg px-2 text-[12px]! leading-none text-foreground",
	GHOST_PILL_CLASS,
);
const SUB_CLASS = "chat-settings-menu min-w-48 text-[12px]!";
// Model rows shown before the list scrolls, so a long catalog never stretches the menu.
const VISIBLE_MODEL_ROWS = 7;
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
	autoCompact,
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
	/** The current agent's other settings, as menu rows under its effort. Given the picker's pick, called when one is changed. */
	extras?: (markPick: () => void) => ReactNode;
	/** The running agent's auto-compact setting, shown beside Fast mode; without one it shows disabled. */
	autoCompact?: AutoCompactControl & { title: string };
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
	// Every agent with why it cannot be used, if it cannot, in one fixed order, so picking one never
	// moves it (on /new a pick changes the running agent at once). Unknown readiness stays usable,
	// since the daemon checks the target first. The running agent is never marked, and leads the
	// list only when the list does not name it.
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
	// Only agents that can be used are on the rail: one that is not installed, not signed in or has
	// no chat here is left out rather than dimmed. The running agent and a pending one stay, so the
	// rail never loses its selection: a pending agent that stops being usable (its readiness arrives
	// after the pick) shows dimmed with the reason, opens its setup, and offers no model to pick.
	const agents = useMemo(
		() => railAgents.filter((agent) => !agent.unavailable || agent.id === currentHarness || agent.id === pending?.harness),
		[railAgents, currentHarness, pending?.harness],
	);
	const management = useAgentManagementMenu(undefined, hostId, agentSwitch.harnessView);
	const [open, setOpen] = useState(false);
	// The composer's model menu; a terminal's agent menu is not counted as one.
	const markModelPick = useTelemetrySurface("model_picker", open && !terminal, { choice: true });
	const lastModels = useLastModels();
	const keepCurrent = () => { if (pending) onPendingChange(undefined); };
	// Cursor's command-line ids fold settings into the model; a running Cursor chat reports its own, kept as is.
	const live = listsModelVariants(currentHarness) && current.efforts.length === 0 && current.thinking === undefined
		? variantSource(current.models, current.modelId, current.modelLabel, current.onPickModel && ((variant) => current.onPickModel?.(variant.id)), t)
		: listsModelVariants(currentHarness) ? { ...current, models: groupModelsByVendor(current.models) }
		: nameSharedModels(current);
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
	// Ultracode stays live: it only adds a word to the chat's next message and never touches the agent.
	const currentSource: PickerSource = disabled || starting
		? {
			...(starting ?? live), onPickModel: undefined, onPickEffort: undefined, onToggleFast: undefined, onToggleThinking: undefined,
			ultracode: live.ultracode, onToggleUltracode: live.onToggleUltracode,
		}
		: live;
	// The other agents' model lists load once the menu is open, so search reaches every model.
	const others = agents.filter((agent) => agent.id !== currentHarness);
	const queryClient = useQueryClient();
	// Warmed on hover or focus, so the first keystrokes already search every agent.
	const warmCatalogs = () => {
		for (const agent of others) void queryClient.prefetchQuery(catalogOptions(agent.id));
	};
	const catalogs = useQueries({
		queries: others.map((agent) => ({ ...catalogOptions(agent.id), enabled: open })),
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
	const pendingEffort = pending?.effort ?? pending?.shownEffort ?? (pending ? source.effort : undefined);
	const shownEffort = pending
		? pendingEffort ? source.efforts.find((effort) => effort.value === pendingEffort)?.label ?? formatEffortLabel(pendingEffort, t) : undefined
		: currentSource.efforts.find((effort) => effort.value === currentSource.effort)?.label;
	const ultraEffort = isUltraEffort(pending ? pendingEffort : currentSource.effort);
	const shownUltracode = pending ? Boolean(pending.ultracode && source.ultracode !== undefined) : Boolean(currentSource.ultracode);
	const shownUltra = ultraEffort || shownUltracode;
	const shownFast = pending ? pending.fast || pending.fastMode || source.fast : currentSource.fast;
	const shownThinking = pending ? pending.thinking ?? source.thinking : currentSource.thinking;
	// A row is named by its id or the variant it sends; either names the current model.
	// A terminal's agent menu is not the composer's model picker, so its picks are not counted.
	const countModelPick = (harness: string, ids: Array<string | undefined>) => {
		markModelPick();
		const same = harness === currentHarness && ids.some((id) => id !== undefined && (id === currentSource.modelId || id === currentSource.sendId));
		if (!terminal && !same) uiUsage.modelChanged();
	};
	const pickModel = (harness: string, id: string) => {
		countModelPick(harness, [id]);
		// Remembered in every chat, so this agent's list starts with it next time.
		rememberLastModel(harness, id);
		if (harness !== currentHarness) return otherSource(harness, pending?.harness === harness ? pending : undefined).onPickModel?.(id);
		keepCurrent();
		currentSource.onPickModel?.(id);
	};
	// A terminal's running agent cannot be given another model from here.
	const oneShot = (harness: string) => terminal && TERMINAL_ONE_SHOT_TARGETS.has(harness);
	// On the rail and not marked: the running agent always is; a pending one may have stopped being.
	const usable = (harness: string) => agents.some((agent) => agent.id === harness && !agent.unavailable);
	const pickable = (harness: string) => (harness === currentHarness
		? !terminal && Boolean(currentSource.onPickModel)
		: !agentSwitch.disabled && !oneShot(harness) && usable(harness));
	// A terminal's Switch button is in this menu, so a pick there must not close it.
	const stayOpen = (event: Event) => { if (terminal) event.preventDefault(); };
	// Search reaches every agent on the rail, the chosen one first.
	const searchRows = (open ? [...agents].sort((a, b) => Number(b.id === shownHarness) - Number(a.id === shownHarness)) : []).flatMap((agent) => {
		const listed = agent.id === currentHarness ? (terminal ? [] : currentSource.models) : otherSource(agent.id).models;
		const chosen = agent.id === currentHarness ? currentSource.modelId : pending?.harness === agent.id ? pending.model : undefined;
		const models = agent.id === "claude-code" ? claudeOrder(mergeSameNamedModels(listed, chosen)) : listed;
		return models.filter((model) => model.id).map((model) => ({
			id: `${agent.id} ${model.id}`, label: `${model.label} ${agentLabel(agent.id)}`, harness: agent.id, model,
		}));
	});
	// A match its agent cannot take now, or the agent cannot run, is listed dimmed and passed over by Enter.
	const searchPickable = (row: { harness: string; model: PickerModel }) => pickable(row.harness) && !row.model.unavailableReason;
	const shownModels = shownHarness === "claude-code" ? claudeOrder(mergeSameNamedModels(source.models, source.modelId)) : source.models;
	// The list starts with the shown agent's model: the chat's own, the one picked for a switch, or
	// else the one last picked for that agent in any chat. It is fixed for each agent while the menu
	// stays open, so picking another model does not move the rows under the pointer.
	const leadNow = (pending ? (pending.model ? source.modelId : undefined) : source.modelId) || lastModels[shownHarness];
	const leads = useRef(new Map<string, string | undefined>());
	if (!open) leads.current.clear();
	else if (!leads.current.has(shownHarness)) leads.current.set(shownHarness, leadNow);
	const leadId = leads.current.get(shownHarness);
	const leadRow = leadId
		? shownModels.find((model) => model.id && (model.id === leadId || model.sendId === leadId || Boolean(model.aliases?.includes(leadId))))
		: undefined;
	const browseRows = open && settable
		? (leadRow ? [leadRow, ...shownModels.filter((model) => model !== leadRow)] : shownModels)
			.map((model) => ({ id: `${shownHarness} ${model.id}`, label: model.label, harness: shownHarness, model }))
		: [];
	// Rows are memoized, so a keystroke only renders the ones that appear or change.
	const pickRef = useRef<(event: Event, harness: string, id: string) => void>(() => {});
	pickRef.current = (event, harness, id) => { stayOpen(event); pickModel(harness, id); };
	const pickFromSearch = useCallback((event: Event, harness: string, id: string) => pickRef.current(event, harness, id), []);
	const pickAgent = (id: string) => {
		markModelPick();
		if (id === currentHarness) keepCurrent();
		else if (id !== pending?.harness) onPendingChange({ harness: id });
	};
	// Seen on the rail but not pickable from it now: a terminal's one-shot agent, or any other while switching is refused.
	const blockedReason = (id: string): string | undefined => {
		if (id === currentHarness) return undefined;
		if (oneShot(id)) return t("agentSwitch.terminalOneShot", { agent: agentLabel(id) });
		return agentSwitch.disabled ? t("agentSwitch.busy") : undefined;
	};
	const pendingCompactKind = autoCompactKind(pending?.harness, pending?.model, terminal);
	// Only while there is nothing to show yet: a reload after a pick keeps the rows in place.
	const waiting = loading && !pending && source.efforts.length === 0 && source.fast === undefined && source.thinking === undefined;
	// A setting changed in the menu is a choice too, so closing after it is not an abandon.
	// It is not a model change.
	const withPick = (toggle: ((next: boolean) => void) | undefined) => toggle && ((next: boolean) => {
		markModelPick();
		toggle(next);
	});

	// The chosen agent's settings, under its models and kept in view while they scroll.
	const settingsRows = [
		waiting ? (
			<OptionMenuItem key="waiting" disabled className="gap-2 text-xs text-muted-foreground">
				<Loader2 aria-hidden="true" className="size-3 animate-spin" />
				{t("agentSwitch.loadingSettings")}
			</OptionMenuItem>
		) : null,
		settable && !waiting && source.efforts.length > 0 ? (
			<EffortSlider
				key="effort"
				label={t("agentSwitch.effort")}
				valueLabel={(choice) => choice?.label ?? t("agentSwitch.defaultModel")}
				choices={source.efforts}
				value={source.effort}
				disabled={!source.onPickEffort || source.efforts.length < 2}
				ultraValue={isUltraEffort}
				onCommit={(effort) => {
					markModelPick();
					source.onPickEffort?.(effort);
				}}
			/>
		) : settable && !waiting && !modelsProblem ? (
			// The same slider, disabled, so the menu does not change shape between models. An agent
			// that names its levels only once it runs (OpenCode) shows this until then.
			<EffortSlider
				key="effort"
				label={t("agentSwitch.effort")}
				valueLabel={() => t("agentSwitch.defaultModel")}
				choices={[]}
				disabled
				onCommit={() => {}}
			/>
		) : null,
	].filter(Boolean);
	// The pending agent's auto-compact, else the running one's; a pending switch hides the running agent's extras.
	const shownCompact: (AutoCompactControl & { title?: string }) | undefined = pending
		? pendingCompactKind
			? {
				kind: pendingCompactKind,
				value: pending.autoCompactWindow,
				onChange: (value: TurnSettings["autoCompactWindow"]) => onPendingChange({ ...pending, autoCompactWindow: value }),
				title: t(pendingCompactKind === "toggle" ? "agentSwitch.autoCompactHintToggle" : "agentSwitch.autoCompactHint", { agent: agentLabel(pending.harness) }),
			}
			: undefined
		: autoCompact;
	// Changing it is a choice too, so closing after it is not an abandon.
	const compact = shownCompact && {
		...shownCompact,
		onChange: (value: TurnSettings["autoCompactWindow"]) => {
			markModelPick();
			shownCompact.onChange(value);
		},
	};
	// Fast mode and auto-compact are always here, in one row, so every agent's menu has the same shape;
	// one the agent does not have is shown off and disabled.
	const showToggles = settable && !waiting && (!modelsProblem || source.fast !== undefined || source.thinking !== undefined);
	const notOffered = t("agentSwitch.notOffered");
	const settingsGroups = [
		settingsRows.length > 0 ? settingsRows : null,
		showToggles ? (
			<>
				<div className="flex items-center gap-1">
					<IconToggle
						icon={Zap}
						label={t("agentSwitch.fastMode")}
						checked={source.fast ?? false}
						onChange={source.fast === undefined ? undefined : withPick(source.onToggleFast)}
						hint={t("agentSwitch.fastModeHint")}
						note={source.fast === undefined ? notOffered : undefined}
					/>
					{/* Claude Code's Ultracode is a mode beside Fast mode, not a level on the effort slider. */}
					{source.ultracode !== undefined ? (
						<IconToggle
							icon={DoubleZap}
							iconClassName="h-3.5 w-auto"
							label={t("agentSwitch.ultracode")}
							checked={source.ultracode}
							onChange={withPick(source.onToggleUltracode)}
							hint={t("agentSwitch.ultracodeHint")}
							onClassName="ultra-icon"
						/>
					) : null}
					<AutoCompactChoices {...(compact ?? { onChange: () => {}, disabled: true, unavailable: true, title: notOffered })} className="ml-auto mr-2" />
				</div>
				<ToggleRow label={t("agentSwitch.thinking")} checked={source.thinking} onChange={withPick(source.onToggleThinking)} />
			</>
		) : null,
		extras && !pending ? extras(markModelPick) : null,
	].filter(Boolean);
	const settingsFooter = settingsGroups.length > 0
		// Each group's line runs through the menu's padding to both edges.
		// Each line sits right under what is above it, with no gap over it.
		? settingsGroups.map((group, index) => <div key={index} className="-mx-(--space-1) border-t border-border px-(--space-1) pt-1">{group}</div>)
		: undefined;

	return (
		// Not modal, so one click on the composer both closes the menu and focuses it.
		<OptionMenu open={open} onOpenChange={setOpen} modal={false}>
			<OptionMenuTrigger variant="ghost"
				ref={management.triggerRef}
				disabled={disabled && agentSwitch.disabled}
				onPointerEnter={warmCatalogs}
				onFocus={warmCatalogs}
				aria-label={terminal ? t("agentSwitch.terminalAgent") : t("agentSwitch.pickerLabel")}
				title={title ?? (terminal ? t("agentSwitch.switchAnother") : t("agentSwitch.pickerLabel"))}
				className={cn(TRIGGER_CLASS, terminal ? "w-full justify-start" : undefined)}
				showCaret={false}
				data-shortcut-target={terminal ? undefined : "model-picker"}
				data-telemetry-id={terminal ? undefined : "composer.model_picker"}
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
						{shownUltracode ? <DoubleZap aria-label={t("agentSwitch.ultracode")} className="ultra-icon h-3 w-auto shrink-0 fill-current" /> : null}
						{shownThinking ? <Brain aria-label={t("agentSwitch.thinking")} className="size-3 shrink-0" /> : null}
						{shownEffort ? <span className={cn("shrink-0", ultraEffort ? "ultra-text" : "text-muted-foreground")}>{shownEffort}</span> : null}
						{mark}
					</>
				)}
				</>}
			</OptionMenuTrigger>
			{/* Pinned to the trigger's left edge, which stays put while its label changes width.
			    The menu sizes to its content: the model list is capped at a few rows, so a long
			    catalog scrolls inside the list instead of stretching the menu. Search, the rail and
			    the settings under the list outgrow the shared select-menu cap, which squeezed the
			    list to two rows, so the menu is capped by the room beside the trigger instead. A
			    window too short for it still shrinks the list, which then scrolls. */}
			<OptionMenuContent align="start" side={terminal ? "right" : "top"} steady
				className="chat-settings-menu w-64 max-h-(--radix-dropdown-menu-content-available-height)! text-[12px]!"
				onCloseAutoFocus={(event) => {
					management.onCloseAutoFocus(event);
					if (!event.defaultPrevented) onCloseAutoFocus?.(event);
				}}
			>
				<ModelMenuChoices
					models={searchRows}
					browse={browseRows}
					browseKey={shownHarness}
					selectedId={source.modelId === undefined ? undefined : `${shownHarness} ${source.modelId}`}
					searchable
					searchPlaceholder={t("agentSwitch.search")}
					rows={VISIBLE_MODEL_ROWS}
					className="max-h-none"
					header={(
						<AgentRail
							agents={agents}
							selected={shownHarness}
							blockedReason={blockedReason}
							onPick={pickAgent}
							onSetup={(id) => { management.requestAgentSetup(id); setOpen(false); }}
							onManage={() => { management.requestManagement(); setOpen(false); }}
						/>
					)}
					footer={settingsFooter}
					pickable={searchPickable}
				>
					{(matches, searchActiveID, optionID, query) => query ? matches.map((row, index) => (
						// Matches are grouped under their agent, the chosen one first. Typing never
						// changes the agent; picking a match picks its agent with it.
						<Fragment key={row.id}>
							{row.harness !== matches[index - 1]?.harness ? (
								<OptionMenuLabel className="flex items-center gap-1.5 text-muted-foreground">
									<ProviderMark provider={row.harness} className="size-3" />
									{agentLabel(row.harness)}
								</OptionMenuLabel>
							) : null}
							<SearchRow
								row={row}
								optionId={optionID?.(row.id)}
								active={row.harness === shownHarness && row.model.id === source.modelId}
								searchActive={row.id === searchActiveID}
								disabled={!searchPickable(row)}
								onPick={pickFromSearch}
							/>
						</Fragment>
					)) : (
						<>
							{matches.length > 0 ? (
								<div>
									{matches.map((row, index, listed) => {
										const model = row.model;
										const active = model.id === source.modelId;
										// The model listed first sits above the group headings; the rest keep theirs.
										const previous = listed[index - 1];
										const heading = model.group && model !== leadRow && (previous?.model === leadRow || model.group !== previous?.model.group);
										return (
											<Fragment key={row.id}>
												{heading ? (
													<OptionMenuLabel>{model.group}</OptionMenuLabel>
												) : null}
												<OptionMenuItem
													active={active}
													radio
													disabled={!source.onPickModel || !usable(shownHarness) || Boolean(model.unavailableReason)}
													// The menu stays open, with the model's effort right under it.
													onSelect={(event) => {
														event.preventDefault();
														if (!active) pickModel(shownHarness, model.id);
													}}
													className="text-xs text-foreground"
												>
													{model.unavailableReason ? (
														<span className="min-w-0">
															<span className="block truncate">{model.label}</span>
															<span className="block whitespace-normal text-[10px] text-muted-foreground">{model.unavailableReason}</span>
														</span>
													) : <span className="min-w-0 truncate">{model.label}</span>}
													{model.hint ? <span className="ml-2 shrink-0 text-[10px] text-muted-foreground/70">{model.hint === "default" ? t("agentSwitch.defaultModel") : model.hint}</span> : null}
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
	// A larger mark in a snug pill: the logo carries the row, not the highlight around it.
	"flex size-6.5 shrink-0 items-center justify-center rounded-md",
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
	/** Why an agent cannot be chosen right now, or undefined when it can. */
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
	// Home, End and the wrap stay among the agents; the gear after them is reached by Right from the
	// last agent, and left by Right (to the first agent) or Left (to the last).
	const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
		if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
		const items = Array.from(event.currentTarget.querySelectorAll<HTMLElement>("[data-agent]"));
		const gear = event.currentTarget.querySelector<HTMLElement>("[data-rail-gear]");
		const onGear = document.activeElement === gear;
		const index = items.indexOf(document.activeElement as HTMLElement);
		if ((index < 0 && !onGear) || items.length === 0) return;
		event.preventDefault();
		event.stopPropagation();
		const last = items.length - 1;
		const next = event.key === "Home" ? items[0]
			: event.key === "End" ? items[last]
			: onGear ? items[event.key === "ArrowRight" ? 0 : last]
			: event.key === "ArrowRight" ? (index === last ? gear ?? items[0] : items[index + 1])
			: items[index === 0 ? last : index - 1];
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
					// A radio group inside the menu, outside ARIA's menu content model like the search field
					// above it: one agent is chosen of several, and its own keys move along it.
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
										// The tooltip shows the same words as the name; described by it too, it would be read twice.
										aria-describedby={undefined}
										tabIndex={checked || (chosenIndex < 0 && index === 0) ? 0 : -1}
										data-agent={agent.id}
										onClick={() => {
											if (agent.unavailable) onSetup(agent.id);
											else if (blocked === undefined && !checked) onPick(agent.id);
										}}
										className={cn(RAIL_ITEM_CLASS, (agent.unavailable || blocked !== undefined) && "opacity-40")}
									>
										<ProviderMark provider={agent.id} className="size-5" />
									</Pressable>
								</TooltipTrigger>
								<TooltipContent side="bottom" sideOffset={4} hideArrow>{name}</TooltipContent>
							</Tooltip>
						);
					})}
				</div>
				<Tooltip>
					<TooltipTrigger asChild>
						<Button variant="quiet" size="icon-xs" tabIndex={-1} data-rail-gear aria-label={t("agentSelector.manage")} aria-describedby={undefined} onClick={onManage}>
							<Settings2 aria-hidden="true" className="size-icon-sm" />
						</Button>
					</TooltipTrigger>
					<TooltipContent side="bottom" sideOffset={4} hideArrow>{t("agentSelector.manage")}</TooltipContent>
				</Tooltip>
			</div>
		</TooltipProvider>
	);
}

/** An on/off setting the agent has, as a menu row. The row owns the switch, so a click or Enter fires once. */
export function ToggleRow({ label, checked, onChange, title, className }: { label: string; checked?: boolean; onChange?: (next: boolean) => void; title?: string; className?: string }) {
	if (checked === undefined) return null;
	return (
		<OptionMenuItem
			disabled={!onChange}
			title={title}
			onSelect={(event) => { event.preventDefault(); onChange?.(!checked); }}
			className={cn("justify-between gap-4 text-xs text-foreground", className)}
		>
			<span>{label}</span>
			<Switch aria-label={label} checked={checked} tabIndex={-1} />
		</OptionMenuItem>
	);
}

/**
 * A mode as an icon button (Fast mode's lightning, Ultracode's double bolt): filled and lit when on, an
 * outline when off. It is still a menu row, so the arrow keys reach it and Enter toggles it without
 * closing the menu.
 */
/**
 * A mode switched on and off by its icon alone. Hovering names it and says what it does,
 * since the icon by itself does not.
 */
function IconToggle({ icon: Icon, label, hint, note, checked, onChange, onClassName = "text-foreground", iconClassName = "size-3.5" }: {
	icon: AppIcon;
	/** The icon's size: square by default; a wider icon gives a height and lets its width follow. */
	iconClassName?: string;
	label: string;
	/** What the mode does, under its name in the tooltip. */
	hint: string;
	/** Why it cannot be switched here, if it cannot. */
	note?: string;
	checked: boolean;
	onChange?: (next: boolean) => void;
	/** The icon's colour when on. */
	onClassName?: string;
}) {
	return (
		<TooltipProvider delayDuration={300}>
			<Tooltip>
				<TooltipTrigger asChild>
					<OptionMenuItem
						role="menuitemcheckbox"
						aria-checked={checked}
						aria-label={label}
						aria-description={note ?? hint}
						disabled={!onChange}
						onSelect={(event) => { event.preventDefault(); onChange?.(!checked); }}
						className={cn(
							"size-7 shrink-0 justify-center p-0 data-disabled:opacity-50",
							checked ? cn(onClassName, "before:bg-interactive-active") : "text-muted-foreground",
						)}
					>
						<Icon aria-hidden="true" className={cn(iconClassName, checked && "fill-current")} />
					</OptionMenuItem>
				</TooltipTrigger>
				<TooltipContent side="top" sideOffset={4} hideArrow className="max-w-60">
					<span className="block font-medium text-foreground">{label}</span>
					<span className="block text-muted-foreground">{hint}</span>
					{note ? <span className="mt-1 block text-muted-foreground">{note}</span> : null}
				</TooltipContent>
			</Tooltip>
		</TooltipProvider>
	);
}

/** Why a catalog has no models, from its refresh error or warning: the first line, kept short. */
export function catalogProblem(catalog: Pick<AgentModelCatalog, "refreshError" | "warning"> | undefined): string | undefined {
	const text = (catalog?.refreshError || catalog?.warning)?.trim().split("\n")[0]?.trim();
	if (!text) return undefined;
	return text.length > 200 ? `${text.slice(0, 199)}…` : text;
}

/**
 * Two routes to one model read the same once shortened. Their router tells them
 * apart, and where the router repeats too, only the full ID does.
 */
function nameSharedLabels<T extends { id: string; label: string }>(models: T[]): T[] {
	const routed = (model: T) => {
		const slash = model.id.indexOf("/");
		return slash < 0 ? model.label : `${model.label} (${model.id.slice(0, slash)})`;
	};
	const uses = new Map<string, number>();
	const routes = new Map<string, number>();
	for (const model of models) {
		uses.set(model.label, (uses.get(model.label) ?? 0) + 1);
		routes.set(routed(model), (routes.get(routed(model)) ?? 0) + 1);
	}
	return models.map((model) => {
		if (!model.id.includes("/") || (uses.get(model.label) ?? 0) < 2) return model;
		return { ...model, label: (routes.get(routed(model)) ?? 0) > 1 ? model.id : routed(model) };
	});
}

/** The running agent's models told apart, the picker button naming the chosen one the same way. */
function nameSharedModels(source: PickerSource): PickerSource {
	const models = nameSharedLabels(source.models);
	const chosen = source.models.findIndex((model) => model.id === source.modelId);
	return { ...source, models, modelLabel: source.models[chosen]?.label === source.modelLabel ? models[chosen].label : source.modelLabel };
}

/** Another agent's models and settings. Nothing changes until the switch runs; each pick is held as pending. */
function otherAgentSource(
	harness: string,
	catalog: AgentModelCatalog["models"] | undefined,
	pending: PendingAgentSwitch | undefined,
	onPick: (next: PendingAgentSwitch) => void,
	t: TFunction,
): PickerSource {
	const listed = nameSharedLabels((catalog ?? [])
		.filter((model) => !/^\[|\s/.test(model.id))
		.map((model) => ({
			id: model.id,
			label: readableModelLabel(agentModelDisplayLabel(harness, model.label)),
			group: modelGroupLabel(model.provider, harness),
			hint: model.isDefault ? "default" : undefined,
			unavailableReason: model.unavailableReason,
		})));
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
	// Ultracode works at any effort level, so every Claude Code model offers it.
	const offersUltracode = harness === "claude-code";
	const ultracodeOn = offersUltracode && Boolean(pending?.ultracode);
	const keep = {
		harness, model: pending?.model, modelLabel: pending?.modelLabel, shownEffort: defaultEffort,
		fastMode: fastOn || undefined, ultracode: ultracodeOn || undefined, autoCompactWindow: pending?.autoCompactWindow,
		// The permission mode picked for the switch stays through every other pick.
		approvalMode: pending?.approvalMode,
	};
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
		efforts: levels.map((value) => ({ value, label: formatEffortLabel(value, t) })),
		effort: pending?.effort ?? defaultEffort,
		onPickEffort: (effort) => onPick({ ...keep, effort }),
		fast: offersFast ? fastOn : undefined,
		onToggleFast: (fast) => onPick({ ...keep, effort: pending?.effort, fastMode: fast || undefined }),
		ultracode: offersUltracode ? ultracodeOn : undefined,
		onToggleUltracode: (on) => onPick({ ...keep, effort: pending?.effort, ultracode: on || undefined }),
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

/** The top level some agents have past Max, such as Codex's and Hermes' "ultra". */
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
	/** Claude Code's window (the default), or OpenCode's on or off. */
	kind?: AutoCompactKind;
	/** Unset leaves it to the agent. */
	value?: TurnSettings["autoCompactWindow"];
	/** The agent is working or already restarting. */
	disabled?: boolean;
	onChange: (value: TurnSettings["autoCompactWindow"]) => void;
}

const AUTO_COMPACT_CHOICES: Record<AutoCompactKind, ReadonlyArray<{ value: TurnSettings["autoCompactWindow"]; label: string }>> = {
	window: [{ value: undefined, label: "Auto" }, { value: "200k", label: "200k" }, { value: "1m", label: "1M" }],
	toggle: [{ value: undefined, label: "Auto" }, { value: "off", label: "Off" }],
};

/**
 * The agent's choices (Auto, 200k and 1M for Claude Code; Auto and Off for OpenCode) as one segmented
 * control beside Fast mode, named for screen readers and its tooltip without a visible label, so the row fits. An agent AO cannot set it for shows
 * only Auto, disabled. A value the agent has no choice for (another agent's window) reads as Auto.
 */
function AutoCompactChoices({ kind = "window", value, disabled, unavailable, onChange, title, className }: AutoCompactControl & { unavailable?: boolean; title?: string; className?: string }) {
	const { t } = useTranslation();
	const choices = unavailable ? AUTO_COMPACT_CHOICES[kind].slice(0, 1) : AUTO_COMPACT_CHOICES[kind];
	const current = choices.some((choice) => choice.value === value) ? value : undefined;
	return (
		<span
			role="radiogroup"
			aria-label={t("agentSwitch.autoCompact")}
			aria-disabled={unavailable || undefined}
			title={title}
			className={cn("flex shrink-0 text-xs", className)}
		>
			<span className={cn("flex rounded-md bg-interactive-active p-0.5", unavailable && "opacity-50")}>
				{choices.map((choice) => (
					<Pressable
						key={choice.label}
						role="radio"
						aria-checked={current === choice.value}
						disabled={disabled || current === choice.value}
						onClick={() => onChange(choice.value)}
						className={cn(
							"rounded px-2 py-0.5 disabled:cursor-default",
							current === choice.value ? "bg-settings-menu text-foreground" : "text-muted-foreground hover:text-foreground",
						)}
					>
						{choice.value === undefined ? t("agentSwitch.autoCompactAuto") : choice.value === "off" ? t("agentSwitch.autoCompactOff") : choice.label}
					</Pressable>
				))}
			</span>
		</span>
	);
}
