import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { Switch } from "../ui/switch";
import { useTranslation } from "react-i18next";
import { agentModelsQueryKey, agentModelsQueryOptions, refreshAgentModels, revalidateAgentModels, type AgentModelCatalog } from "../../hooks/useAgentModelsQuery";
import { useOfferedModels } from "../../hooks/useOfferedModels";
import { agentModelDisplayLabel, isConcreteModelID, modelChoiceLabel } from "../../lib/agent-model-choices";
import { agentLabel } from "../../lib/agent-options";
import { cloudModelVerdict, listsLocalModelsForCloud } from "../../lib/cloud-model-catalog";
import { cloudCatalogAsAgentCatalog, useCloudModelCatalog } from "../../hooks/useCloudModelCatalog";
import { LOCAL_HOST } from "../../lib/hosts";
import { TaskModelSelection } from "../TaskModelControls";
import { AgentModelCombobox } from "./AgentModelCombobox";
import { EffortPicker } from "./EffortPicker";
import { SettingsOptionMenu } from "./SettingsOptionMenu";
import { SettingsRow } from "./SettingsRow";
import { MENU_TRIGGER_CHROME, OptionMenuItem } from "../ui/option-menu";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";

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
	cloudOrgId,
	warningClassName,
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
	/** Where the warning sits in the caller's layout (a row gives it its own line). */
	warningClassName?: string;
	/**
	 * A Cloud project's organization: the field then lists Cloud's models for
	 * the account (the ones its sessions run), and this computer's only where
	 * Cloud has no list for the agent (an older control plane, opencode).
	 */
	cloudOrgId?: string;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const cloud = useCloudModelCatalog({ orgId: cloudOrgId, agent: agentId, enabled: Boolean(cloudOrgId) && agentId !== "" });
	const cloudListed = cloud.catalog;
	// This computer's list stands in only where Cloud has none for the agent (an
	// older control plane, opencode's credential-scoped list), never after a
	// failed read: its models may not run in Cloud.
	const localWanted = !cloudOrgId || listsLocalModelsForCloud(cloud.gap, agentId, projectId);
	const query = useQuery({ ...agentModelsQueryOptions(agentId, projectId, hostId), enabled: localWanted });
	const catalog: AgentModelCatalog | undefined = cloudListed
		? cloudCatalogAsAgentCatalog(agentId, cloudListed)
		: localWanted ? query.data : undefined;
	// Cloud is the field's only source (a Cloud project, and not an agent Cloud
	// lists nothing for that this computer's list stands in for).
	const cloudSourced = Boolean(cloudOrgId) && !localWanted;
	const catalogFetching = cloudSourced ? cloud.loading : query.isFetching && !catalog;
	// Without Cloud's list the field says so, offers to read it again, and keeps
	// manual entry; it never lists this computer's models in its place.
	const cloudGapWarning = cloudSourced && !cloudListed && cloud.gap
		? t(cloud.gap === "unavailable" ? "settings.models.cloudModelsUnavailable" : "settings.models.cloudModelsFailed", { agent: agentLabel(agentId) })
		: undefined;
	const readCloudAgain = async () => {
		await cloud.query.refetch();
	};
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
	// The account's own Cloud list says a saved model will not run: its tasks
	// start on the agent's default instead (the control plane sees to it).
	const cloudUnavailable = cloudListed !== undefined && cloudModelVerdict(cloudListed, model) === "unavailable";
	const warning =
		cloudGapWarning ??
		(cloudUnavailable ? t("settings.models.cloudModelUnavailable", { model, agent: agentLabel(agentId) }) : undefined) ??
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
				onRefresh={cloudSourced ? readCloudAgain : async () => { const refreshed = await refreshAgentModels(agentId, projectId, hostId); queryClient.setQueryData(agentModelsQueryKey(agentId, projectId, hostId), refreshed); }}
			/>
			{warning ? <FieldWarning className={cn("text-xs", warningClassName)} warning={warning} onRetry={cloudGapWarning ? readCloudAgain : undefined} /> : null}
		</>;
	}

	if (isMode) {
		const defaultMode = followCatalogDefaults ? offeredModels.find((item) => item.isDefault && isConcreteModelID(item.id))?.id : "agent";
		const selectedMode = isConcreteModelID(mode) ? mode : "";
		const options = offeredModels.filter((item) => isConcreteModelID(item.id)).map((item) => ({
			value: item.id,
			label: agentModelDisplayLabel(agentId, modelChoiceLabel(item)),
		}));
		// Cloud (and a catalog with no marked mode) has no listed default: unset
		// means the agent's own default, which the picker names and can return to.
		const defaultListed = options.some((option) => option.value === defaultMode);
		const shownMode = selectedMode || (defaultListed ? defaultMode : "") || "";
		const selectMode = (value: string) => {
			onModeChange(value === defaultMode ? "" : value);
			onModelChange("");
		};
		// The agent picks its own model for each mode, so its modes are the row's
		// effort levels and the model column reads "Auto".
		if (options.length > 0) {
			return (
				<>
					<div className="flex min-w-0 flex-wrap items-center gap-2">
						<span className="text-settings-muted">{t("settings.models.autoRouteLabel")}</span>
						<EffortPicker label={label} value={shownMode} choices={options} triggerClassName="w-fit" onChange={selectMode}
							followLabel={defaultListed ? undefined : t("settings.models.agentDefaultMode")}
							footer={!defaultListed && selectedMode ? <OptionMenuItem onSelect={() => { onModeChange(""); onModelChange(""); }}>{t("settings.models.useAgentMode")}</OptionMenuItem> : undefined} />
					</div>
					{warning && <FieldWarning className={cn("px-1 text-xs leading-row", warningClassName)} warning={warning} onRetry={cloudGapWarning ? readCloudAgain : undefined} />}
				</>
			);
		}
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
				{warning && <FieldWarning className={cn("px-1 text-xs leading-row", warningClassName)} warning={warning} onRetry={cloudGapWarning ? readCloudAgain : undefined} />}
			</>
		);
	}

	const models = offeredModels.map((item) => ({
		...item,
		label: item.id === "auto" ? t("settings.models.autoRouteLabel") : agentModelDisplayLabel(agentId, item.label),
		...(supportedEfforts ? { efforts: item.efforts ? item.efforts.filter((value) => supportedEfforts.includes(value)) : [...supportedEfforts] } : {}),
		// Cloud runs the worker's own CLI, so this computer's version limits do not apply.
		...(!followCatalogDefaults ? { isDefault: false, defaultEffort: undefined, requiresVersion: undefined, unavailableReason: undefined } : {}),
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
		if (cloudSourced) {
			await readCloudAgain();
			return;
		}
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
	// No wrapper: the pickers and the effort warning join the caller's row, so the
	// warning takes its own line there instead of widening the model's column.
	return (
		<>
			<AgentModelCombobox
				hostId={hostId}
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
				disabled={catalogFetching || agentId === ""}
				onChange={selectCatalogModel}
				onCustom={selectCustomModel}
				triggerClassName={cn(MENU_TRIGGER_CHROME, "w-fit")}
				compact
				recentScope={agentId}
				menuAlign="start"
				effortPlacement="chip"
				warningClassName={warningClassName}
				tuning={{
					effort,
					effortsWithoutModel: supportedEfforts,
					onEffortChange,
					onValidityChange,
					roleLabel: t(`settings.models.${role}Role`),
				}}
			/>
			{warning && <FieldWarning className={cn("px-1 text-xs leading-row", warningClassName)} warning={warning} onRetry={cloudGapWarning ? readCloudAgain : undefined} />}
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

/** A model field's warning, with a Retry when reading Cloud's list again can clear it. */
function FieldWarning({ warning, className, onRetry }: { warning: string; className?: string; onRetry?: () => Promise<void> }) {
	const { t } = useTranslation();
	if (!onRetry) return <p className={cn("text-warning", className)}>{warning}</p>;
	return (
		<div className={cn("flex items-center gap-2", className)}>
			<p className="min-w-0 text-warning">{warning}</p>
			<Button size="xs" variant="ghost" className="shrink-0" onClick={() => void onRetry().catch(() => undefined)}>
				{t("newTask.modelNotice.retry")}
			</Button>
		</div>
	);
}
