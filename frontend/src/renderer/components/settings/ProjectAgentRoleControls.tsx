import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { Switch } from "../ui/switch";
import { useTranslation } from "react-i18next";
import { agentModelsQueryKey, agentModelsQueryOptions, refreshAgentModels, revalidateAgentModels, type AgentModelCatalog } from "../../hooks/useAgentModelsQuery";
import { useOfferedModels } from "../../hooks/useOfferedModels";
import { agentModelDisplayLabel, isConcreteModelID, modelChoiceLabel } from "../../lib/agent-model-choices";
import { LOCAL_HOST } from "../../lib/hosts";
import { TaskModelSelection } from "../TaskModelControls";
import { AgentModelCombobox } from "./AgentModelCombobox";
import { SettingsOptionMenu } from "./SettingsOptionMenu";
import { SettingsRow } from "./SettingsRow";
import { MENU_TRIGGER_CHROME } from "../ui/option-menu";
import { cn } from "../../lib/utils";

export function AgentModelField({
	role,
	variant = "settings",
	agentId,
	projectId,
	hostId,
	model,
	mode,
	effort,
	onModelChange,
	onModeChange,
	onEffortChange,
	onValidityChange,
	allowCustomFallback = false,
	supportedEfforts,
	followCatalogDefaults = true,
	emptyLabel,
	independentMode = false,
}: {
	role: "worker" | "orchestrator" | "reviewer";
	variant?: "settings" | "task";
	agentId: string;
	projectId: string;
	hostId?: string;
	model: string;
	mode: string;
	effort: string;
	onModelChange: (value: string) => void;
	onModeChange: (value: string) => void;
	onEffortChange: (value: string) => void;
	onValidityChange: (valid: boolean) => void;
	allowCustomFallback?: boolean;
	/** Cloud accepts custom model IDs and validates effort at the harness boundary. */
	supportedEfforts?: readonly string[];
	/** Local catalog defaults may differ from the Cloud worker runtime. */
	followCatalogDefaults?: boolean;
	emptyLabel?: string;
	/** Cloud Cursor can select both a model and a launch mode. */
	independentMode?: boolean;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const query = useQuery(agentModelsQueryOptions(agentId, projectId, hostId));
	const catalog: AgentModelCatalog | undefined = query.data;
	// Settings → Harness → Models picks which local models are offered; Cloud keeps its own catalog.
	const { models: offeredModels } = useOfferedModels(agentId, catalog?.models, { hostId, keep: [model, mode], skip: !followCatalogDefaults });
	const revalidationQuery = useQuery({
		queryKey: ["agent-model-revalidation", hostId ?? LOCAL_HOST, agentId, projectId, catalog?.validatedAt ?? ""],
		queryFn: () => revalidateAgentModels(agentId, projectId, hostId),
		enabled: agentId !== "" && catalog?.refreshRecommended === true,
		staleTime: Number.POSITIVE_INFINITY,
		retry: false,
	});
	useEffect(() => {
		if (revalidationQuery.data) {
			queryClient.setQueryData(agentModelsQueryKey(agentId, projectId, hostId), revalidationQuery.data);
		}
	}, [agentId, hostId, projectId, queryClient, revalidationQuery.data]);
	const isMode = !independentMode && catalog?.selectionMode === "mode";
	const label = t(`settings.models.${role}${isMode ? "Mode" : "Model"}`);
	const warning =
		(revalidationQuery.isError ? (revalidationQuery.error instanceof Error ? revalidationQuery.error.message : t("settings.models.validateFailed")) : undefined) ??
		catalog?.warning ??
		(query.isError ? (query.error instanceof Error ? query.error.message : t("settings.models.loadFailed")) : undefined);

	if (variant === "task") {
		return <>
			<TaskModelSelection id="automation-model" fetching={query.isFetching} agentId={agentId} agentLabel={agentId} projectId={projectId}
				catalog={catalog ? { ...catalog, models: offeredModels } : undefined}
				value={model} mode={mode} effort={effort}
				disabled={agentId === "" || (query.isFetching && !catalog)}
				onModelChange={onModelChange} onModeChange={onModeChange} onEffortChange={onEffortChange}
				onValidityChange={onValidityChange}
				onRefresh={async () => { const refreshed = await refreshAgentModels(agentId, projectId, hostId); queryClient.setQueryData(agentModelsQueryKey(agentId, projectId, hostId), refreshed); }}
			/>
			{warning ? <p className="text-xs text-warning">{warning}</p> : null}
		</>;
	}

	if (isMode) {
		const defaultMode = followCatalogDefaults ? offeredModels.find((item) => item.isDefault && isConcreteModelID(item.id))?.id : "agent";
		const selectedMode = isConcreteModelID(mode) ? mode : "";
		const options = offeredModels.filter((item) => isConcreteModelID(item.id)).map((item) => ({
			value: item.id,
			label: agentModelDisplayLabel(agentId, modelChoiceLabel(item)),
		}));
		return (
			<>
				<div className="min-w-0">
					<div className="flex min-w-0 items-center gap-2">
						<SettingsOptionMenu
							aria-label={label}
							value={selectedMode || defaultMode || ""}
							options={options}
							placeholder={t("settings.models.modeNotReported")}
							action={selectedMode && !defaultMode ? { label: t("settings.models.useAgentMode"), onSelect: () => onModeChange("") } : undefined}
							triggerClassName="w-fit"
							disabled={options.length === 0 && !(selectedMode && !defaultMode)}
							onChange={(value) => {
								onModeChange(value === defaultMode ? "" : value);
								onModelChange("");
							}}
						/>
					</div>
				</div>
				{warning && <p className="px-1 text-xs leading-row text-warning">{warning}</p>}
			</>
		);
	}

	const models = offeredModels.map((item) => ({
		...item,
		label: item.id === "auto" ? t("settings.models.autoRouteLabel") : agentModelDisplayLabel(agentId, item.label),
		...(supportedEfforts ? { efforts: item.efforts ? item.efforts.filter((value) => supportedEfforts.includes(value)) : [...supportedEfforts] } : {}),
		...(!followCatalogDefaults ? { isDefault: false, defaultEffort: undefined } : {}),
	}));
	if (supportedEfforts && isConcreteModelID(model) && !models.some((item) => item.id === model)) {
		models.push({ id: model, label: model, efforts: [...supportedEfforts] });
	}
	// Cloud saves only explicit picks, but an unset model still names the one the
	// agent uses by default rather than a generic placeholder.
	const catalogDefault = followCatalogDefaults ? undefined : catalog?.models?.find((item) => item.isDefault && isConcreteModelID(item.id));
	const unsetLabel = emptyLabel ?? (catalogDefault ? agentModelDisplayLabel(agentId, catalogDefault.label) : undefined);
	const customModelEntry = catalog?.customModelEntry ?? (catalog?.allowCustom || allowCustomFallback ? "direct" : "none");
	const refreshCatalog = async () => {
		const refreshed = await refreshAgentModels(agentId, projectId, hostId);
		queryClient.setQueryData(agentModelsQueryKey(agentId, projectId, hostId), refreshed);
	};
	const selectCatalogModel = (value: string) => {
		onModelChange(value);
		if (!independentMode) onModeChange("");
	};
	const selectCustomModel = (value: string) => {
		onModelChange(value);
		if (!independentMode) onModeChange("");
	};
	return (
		<>
			<div className="min-w-0">
				<div className="min-w-0">
					<AgentModelCombobox
						aria-label={label}
						value={model}
						models={models}
						emptyLabel={unsetLabel}
						allowCustom={catalog?.allowCustom}
						customModelEntry={customModelEntry}
						agentLabel={agentId}
						agentId={agentId}
						onRefresh={refreshCatalog}
						refreshing={catalog?.refreshState === "queued" || catalog?.refreshState === "refreshing"}
						refreshError={catalog?.refreshError}
						retryAt={catalog?.retryAt}
						disabled={(query.isFetching && !catalog) || agentId === ""}
						onChange={selectCatalogModel}
						onCustom={selectCustomModel}
						triggerClassName={cn(MENU_TRIGGER_CHROME, "w-fit")}
						compact
						recentScope={agentId}
						menuAlign="start"
						tuning={{
							effort,
							effortsWithoutModel: supportedEfforts,
							onEffortChange,
							onValidityChange,
							roleLabel: t(`settings.models.${role}Role`),
						}}
					/>
				</div>
			</div>
			{warning && <p className="px-1 text-xs leading-row text-warning">{warning}</p>}
		</>
	);
}

export function ProjectAutoReviewToggle({ checked, onCheckedChange, description }: { checked: boolean; onCheckedChange: (checked: boolean) => void; description?: string }) {
	const { t } = useTranslation();
	return <ProjectToggleRow id="project-auto-review" label={t("settings.project.autoReviewToggle")} description={description ?? t("settings.project.autoReviewDescription")} checked={checked} onCheckedChange={onCheckedChange} />;
}

export function ProjectWorkersRequestReviewToggle({ checked, onCheckedChange }: { checked: boolean; onCheckedChange: (checked: boolean) => void }) {
	const { t } = useTranslation();
	return <ProjectToggleRow id="project-workers-request-review" label={t("settings.project.workersRequestReviewToggle")} description={t("settings.project.workersRequestReviewDescription")} checked={checked} onCheckedChange={onCheckedChange} />;
}

function ProjectToggleRow({ id, label, description, checked, onCheckedChange }: { id: string; label: string; description: string; checked: boolean; onCheckedChange: (checked: boolean) => void }) {
	return (
		<SettingsRow label={label} description={description}>
			<Switch
				aria-label={label}
				checked={checked}
				id={id}
				onCheckedChange={onCheckedChange}
			/>
		</SettingsRow>
	);
}
