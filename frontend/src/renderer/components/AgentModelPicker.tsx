import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import {
	agentModelsQueryKey,
	agentModelsQueryOptions,
	refreshAgentModels,
	revalidateAgentModels,
	type AgentModelCatalog,
} from "../hooks/useAgentModelsQuery";
import { useOfferedModels } from "../hooks/useOfferedModels";
import { AgentModelCombobox } from "./settings/AgentModelCombobox";
import { NativeModePicker } from "./NativeModePicker";

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
	const { models: offeredModels } = useOfferedModels(agentId, catalog?.models, { hostId, keep: [value, mode] });
	const revalidationQuery = useQuery({
		queryKey: hostId
			? ["agent-model-revalidation", hostId, agentId, projectId, catalog?.validatedAt ?? ""]
			: ["agent-model-revalidation", agentId, projectId, catalog?.validatedAt ?? ""],
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

	if (catalogLoading) {
		return (
			<span
				className="composer-chip composer-toolbar-option w-full cursor-not-allowed justify-start opacity-50"
				role="status"
				aria-label={t("settings.models.loading")}
				aria-busy="true"
			>
				<Loader2 className="size-icon-sm shrink-0 animate-spin text-settings-muted" aria-hidden="true" />
				<span className="truncate text-settings-muted">{t("settings.models.loading")}</span>
			</span>
		);
	}

	if (catalog?.selectionMode === "mode") {
		return (
			<NativeModePicker
				models={offeredModels}
				mode={mode}
				onModeChange={onModeChange}
				disabled={disabled || agentId === ""}
				triggerClassName="composer-chip composer-toolbar-option w-full justify-between"
			/>
		);
	}

	const customModelEntry = catalog?.customModelEntry ?? (catalog?.allowCustom ? "direct" : "none");
	const displayModels = offeredModels.map((item) =>
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
			key={hostId ? `${hostId}:${agentId}` : agentId}
			aria-label={t("newTask.model")}
			value={value}
			models={displayModels}
			allowCustom={catalog?.allowCustom}
			customModelEntry={customModelEntry}
			agentLabel={agentLabel}
			agentId={agentId}
			onRefresh={refreshCatalog}
			refreshing={catalog?.refreshState === "queued" || catalog?.refreshState === "refreshing"}
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
