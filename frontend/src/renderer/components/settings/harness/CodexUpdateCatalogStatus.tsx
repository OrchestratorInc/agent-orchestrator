import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { agentModelsQueryOptions, isRefreshingCatalog } from "../../../hooks/useAgentModelsQuery";

/** An updated executable and a refreshed account catalog are separate results. */
export function CodexUpdateCatalogStatus({ startedAt, hostId }: { startedAt?: string; hostId?: string }) {
	const { t } = useTranslation();
	const catalog = useQuery(agentModelsQueryOptions("codex", "", hostId, true));
	const data = catalog.data;
	if (!catalog.isError && isRefreshingCatalog(data)) return <p role="status" className="text-xs text-settings-muted">{t("settings.harness.codexCatalogRefreshing")}</p>;
	if (catalog.isError || data?.refreshError || data?.stale) return <p role="alert" className="text-xs text-error">{t("settings.harness.codexCatalogFailed")}</p>;
	const validated = data?.validatedAt ? Date.parse(data.validatedAt) : 0;
	const updateStarted = startedAt ? Date.parse(startedAt) : 0;
	if (!data || isRefreshingCatalog(data) || !validated || validated < updateStarted) return <p role="status" className="text-xs text-settings-muted">{t("settings.harness.codexCatalogRefreshing")}</p>;
	return <p role="status" className="text-xs text-settings-muted">{t("settings.harness.codexCatalogRefreshed")}</p>;
}
