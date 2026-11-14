import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { agentModelsQueryOptions, type AgentModelCatalog } from "../hooks/useAgentModelsQuery";
import { useAgentManagementMenu } from "../hooks/useAgentManagementMenu";
import { useOfferedModels } from "../hooks/useOfferedModels";
import { agentModelDisplayLabel, isConcreteModelID, modelChoiceLabel } from "../lib/agent-model-choices";
import { agentLabel } from "../lib/agent-options";
import { buildRankedAgentOptions, isLaunchableAgent, isReadyAgent, unknownAgentReadiness, type AgentInfo } from "../lib/agent-select-options";
import { KNOWN_REVIEWER_HARNESS_IDS } from "../lib/reviewer-harnesses";
import { cn } from "../lib/utils";
import { CHAT_PICKER_DIVIDER, CHAT_PICKER_SUB_CLASS, CHAT_PICKER_TRIGGER_CLASS } from "./chat/AgentSwitchPicker";
import { ModelMenuChoices } from "./chat/ModelMenuChoices";
import { ProviderMark } from "./chat/ProviderMark";
import { REVIEWER_AGENT_PRIORITY_RANK } from "./ReviewerSelect";
import { effortDisplayLabel, EffortMenuItems } from "./settings/EffortPicker";
import {
	OptionMenu,
	OptionMenuContent,
	OptionMenuItem,
	OptionMenuSub,
	OptionMenuSubContent,
	OptionMenuSubTrigger,
	OptionMenuTrigger,
} from "./ui/option-menu";

export type ReviewerModelConfig = { model?: string; mode?: string; effort?: string };

/**
 * The session's reviewer, picked the way the chat box picks a session's agent:
 * one pill (the agent's mark, model, and effort) opening one menu with model
 * search and Agent, Model, and Effort rows that open to the side. Unlike the
 * chat menu, a pick applies at once (there is no switch to confirm), the agents
 * are the reviewer agents, and chat-only settings are left out.
 */
export function ReviewerPicker({
	agents,
	defaultHarness,
	value,
	model,
	mode,
	effort,
	inherited = {},
	projectId,
	hostId,
	disabled = false,
	ariaLabel,
	onAgentChange,
	onConfigChange,
}: {
	agents?: components["schemas"]["AgentReadinessSnapshot"][];
	/** The agent an empty `value` follows (the project default). */
	defaultHarness: string;
	/** The session's saved reviewer agent; empty follows the project default. */
	value: string;
	/** The session's own reviewer settings; empty means "inherit". */
	model: string;
	mode: string;
	effort: string;
	/** What an empty session field inherits from the project's reviewer for this agent. */
	inherited?: ReviewerModelConfig;
	projectId: string;
	hostId?: string;
	disabled?: boolean;
	ariaLabel: string;
	/** Saves a new reviewer agent, with its model and effort reset. */
	onAgentChange: (harness: string) => void;
	/** Saves the session's model, mode, and effort for the current agent. */
	onConfigChange: (config: ReviewerModelConfig) => void;
}) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const harness = value || defaultHarness;

	// The reviewer agents, ranked as in Project Settings' reviewer picker.
	const fallbackAgents: AgentInfo[] = [...KNOWN_REVIEWER_HARNESS_IDS].map((id) => unknownAgentReadiness(id, agentLabel(id)));
	const supported = (agents ?? fallbackAgents).filter((agent) => KNOWN_REVIEWER_HARNESS_IDS.has(agent.id));
	const ranked = buildRankedAgentOptions({
		agents: supported.length > 0 ? supported : fallbackAgents,
		priorityRank: REVIEWER_AGENT_PRIORITY_RANK,
		fallbackAgents,
	});
	const selectable = hostId ? isLaunchableAgent : isReadyAgent;
	const otherAgents = ranked.filter((agent) => agent.id !== defaultHarness && (agents === undefined || selectable(agent)));
	const needsSetup = agents !== undefined && !ranked.some((agent) => agent.id === harness && selectable(agent));
	const management = useAgentManagementMenu(needsSetup ? harness : undefined, hostId);

	const catalogQuery = useQuery(agentModelsQueryOptions(harness, projectId, hostId));
	const catalog = catalogQuery.data;
	const { models: offered } = useOfferedModels(harness, catalog?.models, { hostId, keep: [model, mode] });
	const isMode = catalog?.selectionMode === "mode";
	const rows = offered
		.filter((item) => isConcreteModelID(item.id))
		// The catalog's reason a model cannot run here (such as an old agent version) keeps it from being picked.
		.map((item) => ({ id: item.id, label: agentModelDisplayLabel(harness, modelChoiceLabel(item)), unavailableReason: item.unavailableReason }));
	const defaultID = defaultModelID(catalog);

	// The daemon fills an empty session field from the project's reviewer for this
	// agent, so show what the reviewer will actually run with.
	const shownModel = model || (mode ? "" : inherited.model ?? "");
	const shownMode = mode || (model ? "" : inherited.mode ?? "");
	const inheritedEffort = inherited.effort ?? "";
	const shownEffort = effort || inheritedEffort;
	const runsWith = shownMode || shownModel || defaultID;
	const runsWithLabel = rows.find((row) => row.id === runsWith)?.label || runsWith;

	// Effort belongs to the model the reviewer runs with.
	const effortModel = catalogModel(catalog, runsWith);
	const choices = (effortModel?.efforts ?? []).filter((level) => level && level.toLowerCase() !== "default").map((level) => ({ value: level }));
	const reportedDefault = effortModel?.defaultEffort?.trim().toLowerCase();
	// Saving an empty effort means "inherit". With nothing inherited that is the
	// model's default, so picking the reported default clears the override. When
	// the project sets an effort, every level is saved as picked, or the model's
	// default could never be chosen; Clear effort then goes back to the project's.
	const needsClear = effort !== "" && (inheritedEffort !== ""
		|| (choices.some((choice) => choice.value === effort) && !choices.some((choice) => choice.value.toLowerCase() === reportedDefault)));
	const effortLabel = effortDisplayLabel({ value: shownEffort, choices, followLabel: t("agentSwitch.defaultModel"), t, defaultEffort: effortModel?.defaultEffort });

	const save = (next: { model: string; mode: string; effort: string }) => onConfigChange({
		...(next.model ? { model: next.model } : {}),
		...(next.mode ? { mode: next.mode } : {}),
		...(next.effort ? { effort: next.effort } : {}),
	});
	// A picked model or mode is saved by name, even the catalog default, so it holds
	// over a project-level choice; "Use agent model" is the way back. The effort is
	// kept only when the new pick offers it (an empty id is the catalog default).
	const pickModel = (id: string) => {
		const kept = effort && catalogModel(catalog, id || defaultID)?.efforts?.includes(effort) ? effort : "";
		save({ model: isMode ? "" : id, mode: isMode ? id : "", effort: kept });
	};
	const pickEffort = (level: string) => save({ model, mode, effort: level });
	// Picking the reviewer already saved changes nothing: saving it again would
	// drop its model, effort, and hidden settings.
	const pickAgent = (next: string) => {
		if (next !== value) onAgentChange(next);
	};

	const defaultHarnessLabel = ranked.find((agent) => agent.id === defaultHarness)?.label || agentLabel(defaultHarness);
	const modelRow = (row: { id: string; label: string; unavailableReason?: string }, optionId?: string, searchActive?: boolean) => (
		<OptionMenuItem
			key={row.id}
			id={optionId}
			active={row.id === runsWith}
			searchActive={searchActive}
			radio
			disabled={Boolean(row.unavailableReason)}
			onSelect={() => pickModel(row.id)}
			className="text-xs text-foreground"
		>
			{/* As in the chat model menu: the reason sits under the name. */}
			{row.unavailableReason ? (
				<span className="min-w-0">
					<span className="block truncate">{row.label}</span>
					<span className="block whitespace-normal text-[10px] text-muted-foreground">{row.unavailableReason}</span>
				</span>
			) : <span className="min-w-0 truncate">{row.label}</span>}
			{row.id === defaultID ? <span className="ml-2 shrink-0 text-[10px] text-muted-foreground/70">{t("agentSwitch.defaultModel")}</span> : null}
		</OptionMenuItem>
	);

	return (
		<OptionMenu open={open} onOpenChange={setOpen} modal={false}>
			<OptionMenuTrigger
				variant="ghost"
				ref={management.triggerRef}
				disabled={disabled}
				aria-label={ariaLabel}
				title={ariaLabel}
				className={cn(CHAT_PICKER_TRIGGER_CLASS, "ml-auto min-w-0 max-w-full")}
				showCaret={false}
			>
				<ProviderMark provider={harness} className="size-3.5" />
				{/* The agent's name is read out and searchable; the mark shows it. */}
				<span className="sr-only">{agentLabel(harness)}</span>
				<span className="min-w-0 max-w-[28ch] truncate">{runsWithLabel || agentLabel(harness)}</span>
				{shownEffort ? <span className="shrink-0 text-muted-foreground">{effortLabel}</span> : null}
				{needsSetup ? <span className="shrink-0 text-muted-foreground">{t("agentSelector.needsSetup")}</span> : null}
			</OptionMenuTrigger>
			<OptionMenuContent align="end" steady className="chat-settings-menu w-64 text-[12px]!" onCloseAutoFocus={management.onCloseAutoFocus}>
				<ModelMenuChoices models={rows} selectedId={runsWith} searchable searchPlaceholder={t("settings.models.searchPlaceholder")}>
					{(matches, searchActiveID, optionID, query) => query ? matches.map((row) => modelRow(row, optionID?.(row.id), row.id === searchActiveID)) : (
						<>
							<OptionMenuSub closeOnSelect>
								<OptionMenuSubTrigger label={t("agentSwitch.agent")} value={agentLabel(harness)} />
								<OptionMenuSubContent scrollable className={CHAT_PICKER_SUB_CLASS}>
									<div className="model-menu-scroll max-h-[calc(var(--size-select-menu-max)-var(--space-2)*2)] overflow-y-auto overscroll-contain">
										{/* The project default comes first: picking it follows the project. */}
										<OptionMenuItem active={value === ""} radio onSelect={() => pickAgent("")} className="text-xs text-foreground">
											<ProviderMark provider={defaultHarness} className="mr-2 size-3.5" />
											<span className="min-w-0 truncate">{defaultHarnessLabel}</span>
											<span className="ml-auto shrink-0 pl-3 text-muted-foreground">{t("agentSwitch.defaultModel")}</span>
										</OptionMenuItem>
										{otherAgents.map((agent) => (
											<OptionMenuItem
												key={agent.id}
												active={agent.id === value}
												radio
												disabled={agent.disabled}
												onSelect={() => pickAgent(agent.id)}
												className="text-xs text-foreground"
											>
												<ProviderMark provider={agent.id} className="mr-2 size-3.5" />
												<span className="min-w-0 truncate">{agent.label}</span>
												{agent.status ? <span className="ml-auto shrink-0 pl-3 text-muted-foreground">{agent.status}</span> : null}
											</OptionMenuItem>
										))}
									</div>
									{/* Settings open once the whole menu has closed, and a pick here only closes this side list. */}
								<OptionMenuItem
									className={cn(CHAT_PICKER_DIVIDER, "text-xs text-foreground")}
									onSelect={() => {
										management.requestManagement();
										setOpen(false);
									}}
								>
										{t("agentSelector.manage")}
									</OptionMenuItem>
								</OptionMenuSubContent>
							</OptionMenuSub>
							{rows.length > 0 ? (
								<OptionMenuSub closeOnSelect>
									<OptionMenuSubTrigger label={t(isMode ? "settings.cloudProject.mode" : "agentSwitch.model")} value={runsWithLabel} />
									<OptionMenuSubContent scrollable className={CHAT_PICKER_SUB_CLASS}>
										<div className="model-menu-scroll max-h-[calc(var(--size-select-menu-max)-var(--space-2)*2)] overflow-y-auto overscroll-contain">
											{model || mode ? (
												<OptionMenuItem onSelect={() => pickModel("")} className="text-xs text-foreground">
													{t(isMode ? "settings.models.useAgentMode" : "settings.models.useAgentModel")}
												</OptionMenuItem>
											) : null}
											{rows.map((row) => modelRow(row))}
										</div>
									</OptionMenuSubContent>
								</OptionMenuSub>
							) : catalogQuery.isFetching ? (
								<OptionMenuItem disabled className="text-xs text-muted-foreground">{t("settings.models.loading")}</OptionMenuItem>
							) : null}
							{choices.length > 0 || shownEffort ? (
								<OptionMenuSub closeOnSelect>
									<OptionMenuSubTrigger label={t("agentSwitch.effort")} value={effortLabel} />
									<OptionMenuSubContent className={CHAT_PICKER_SUB_CLASS}>
										<EffortMenuItems
											value={shownEffort}
											defaultValue={inheritedEffort ? null : ""}
											choices={choices}
											defaultEffort={effortModel?.defaultEffort}
											availability={effortModel?.efforts === undefined ? "unknown" : choices.length > 0 ? "supported" : "unsupported"}
											compact
											onChange={pickEffort}
										/>
										{needsClear ? (
											<OptionMenuItem className={cn(CHAT_PICKER_DIVIDER, "text-xs text-foreground")} onSelect={() => pickEffort("")}>
												{t("settings.models.clearEffort")}
											</OptionMenuItem>
										) : null}
									</OptionMenuSubContent>
								</OptionMenuSub>
							) : null}
						</>
					)}
				</ModelMenuChoices>
			</OptionMenuContent>
		</OptionMenu>
	);
}

function defaultModelID(catalog: AgentModelCatalog | undefined): string {
	return catalog?.models?.find((item) => item.isDefault && isConcreteModelID(item.id))?.id ?? "";
}

function catalogModel(catalog: AgentModelCatalog | undefined, id: string) {
	return id ? catalog?.models?.find((item) => item.id === id) : undefined;
}
