import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { isConcreteModelID, modelChoiceLabel } from "../lib/agent-model-choices";
import {
	agentModelsQueryKey,
	agentModelsQueryOptions,
	refreshAgentModels, expandAgentModels,
	agentModelsRevalidationQueryOptions,
	type AgentModelCatalog,
} from "../hooks/useAgentModelsQuery";
import { AgentModelCombobox } from "./settings/AgentModelCombobox";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";

type AgentModelPickerProps = {
	agentId: string;
	agentLabel: string;
	projectId: string;
	hostId?: string;
	value: string;
	mode: string;
	disabled?: boolean;
	onModelChange: (value: string) => void;
	onModeChange: (value: string) => void;
	onWarningChange: (warning: string | undefined) => void;
};

export function AgentModelPicker({
	agentId,
	agentLabel,
	projectId,
	hostId,
	value,
	mode,
	disabled = false,
	onModelChange,
	onModeChange,
	onWarningChange,
}: AgentModelPickerProps) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const query = useQuery(agentModelsQueryOptions(agentId, projectId, hostId));
	const catalog: AgentModelCatalog | undefined = query.data;
	const revalidationQuery = useQuery(agentModelsRevalidationQueryOptions(agentId, projectId, catalog, hostId));
	useEffect(() => {
		if (revalidationQuery.data) {
			queryClient.setQueryData(agentModelsQueryKey(agentId, projectId, hostId), revalidationQuery.data);
		}
	}, [agentId, hostId, projectId, queryClient, revalidationQuery.data]);
	const warning =
		(revalidationQuery.isError
			? revalidationQuery.error instanceof Error
				? revalidationQuery.error.message
				: t("settings.models.validateFailed")
			: undefined) ??
		catalog?.warning ??
		(query.isError ? (query.error instanceof Error ? query.error.message : t("settings.models.loadFailed")) : undefined);
	useEffect(() => {
		onWarningChange(warning);
	}, [onWarningChange, warning]);
	useEffect(() => () => onWarningChange(undefined), [onWarningChange]);

	const catalogLoading = agentId !== "" && query.isFetching && catalog === undefined;
	const refreshCatalog = async () => {
		const refreshed = await refreshAgentModels(agentId, projectId, hostId);
		queryClient.setQueryData(agentModelsQueryKey(agentId, projectId, hostId), refreshed);
	};

	if (catalog?.selectionMode === "mode") {
		const options = (catalog.models ?? []).filter((item) => isConcreteModelID(item.id)).map((item) => ({
			value: item.id,
			label: modelChoiceLabel(item),
		}));
		const explicitMode = isConcreteModelID(mode) ? mode : "";
		const defaultMode = catalog.models?.find((item) => item.isDefault && isConcreteModelID(item.id))?.id || "";
		const effectiveMode = explicitMode || defaultMode;
		const visibleModeLabel = options.find((option) => option.value === effectiveMode)?.label ?? (explicitMode || t("settings.models.modeNotReported"));
		return (
			<SettingsOptionMenu
				aria-label={t("newTask.model")}
				value={effectiveMode}
				options={options}
				action={explicitMode && !defaultMode ? { label: t("settings.models.useAgentMode"), onSelect: () => onModeChange("") } : undefined}
				disabled={disabled || agentId === "" || (options.length === 0 && !(explicitMode && !defaultMode))}
				triggerClassName="composer-chip composer-toolbar-option w-full justify-between"
				menuAlign="start"
				renderTrigger={() => (
					<span className="min-w-0 truncate text-control text-foreground" title={visibleModeLabel}>
						{visibleModeLabel}
					</span>
				)}
				onChange={(value) => onModeChange(value === defaultMode ? "" : value)}
			/>
		);
	}

	const customModelEntry = catalog?.customModelEntry ?? (catalog?.allowCustom ? "direct" : "none");
	const displayModels = (catalog?.models ?? []).map((item) =>
		item.id === "auto" ? { ...item, label: t("settings.models.autoRouteLabel") } : item,
	);
	const selectCatalogModel = (nextModel: string) => {
		onModelChange(nextModel);
	};
	const selectCustomModel = (nextModel: string) => {
		onModelChange(nextModel);
	};

	return (
		<AgentModelCombobox
			key={`${hostId ?? ""}:${agentId}:${projectId}`}
			aria-label={t("newTask.model")}
			value={value}
			models={displayModels}
			additionalModelsAvailable={catalog?.additionalModelsAvailable}
			additionalModelsLoaded={catalog?.additionalModelsLoaded}
			catalogIdentity={catalog?.inputFingerprint}
			onLoadAdditional={() => expandAgentModels(queryClient, agentId, projectId, hostId)}
			allowCustom={catalog?.allowCustom}
			customModelEntry={customModelEntry}
			selectionMode={catalog?.selectionMode}
			agentLabel={agentLabel}
			onRefresh={refreshCatalog}
			refreshing={catalogLoading || catalog?.refreshState === "queued" || catalog?.refreshState === "refreshing"}
			refreshError={catalog?.refreshError}
			retryAt={catalog?.retryAt}
			disabled={disabled || agentId === ""}
			onChange={selectCatalogModel}
			onCustom={selectCustomModel}
			compact
			recentScope={hostId ? `${hostId}:${agentId}` : agentId}
			triggerClassName="composer-chip composer-toolbar-option w-full justify-between"
			menuAlign="start"
			renderTrigger={(label) => <span className="min-w-0 truncate text-control text-foreground" title={label}>{label}</span>}
		/>
	);
}
