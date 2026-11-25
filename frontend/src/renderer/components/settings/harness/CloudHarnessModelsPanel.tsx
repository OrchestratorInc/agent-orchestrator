import { ChevronLeft, ChevronRight, Search } from "@/components/app-icons";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { cloudCatalogAsAgentCatalog, useCloudModelCatalog } from "../../../hooks/useCloudModelCatalog";
import { agentModelDisplayLabel } from "../../../lib/agent-model-choices";
import { agentLabel, type AgentId } from "../../../lib/agent-options";
import { cn } from "../../../lib/utils";
import { Button } from "../../ui/button";
import { HARNESS_CARD_CLASS, HARNESS_CARD_ROW_CLASS } from "./harness-ui";

const MODELS_PAGE_SIZE = 10;

/** The models Cloud runs for a harness, laid out like the local Models tab.
 * Cloud pickers list them all, so there is nothing to switch off here. */
export function CloudHarnessModelsPanel({ agentId, orgId, connected, onOpenAccount }: {
	agentId: AgentId;
	orgId: string;
	connected: boolean;
	onOpenAccount: () => void;
}) {
	const { t } = useTranslation();
	const { catalog, gap, loading } = useCloudModelCatalog({ orgId, agent: agentId, enabled: connected });
	const [page, setPage] = useState(0);
	const [modelSearch, setModelSearch] = useState("");
	useEffect(() => { setPage(0); setModelSearch(""); }, [agentId]);
	const agent = agentLabel(agentId);
	const allModels = catalog ? cloudCatalogAsAgentCatalog(agentId, catalog).models : [];
	// Cloud lists its release models for any account, so the list waits for a login.
	if (!connected) {
		return <div className="flex flex-col items-center gap-3 px-3 py-8 text-center">
			<p className="text-sm text-settings-muted">{t("settings.harness.models.needsLogin", { agent })}</p>
			<Button type="button" size="sm" variant="secondary" onClick={onOpenAccount}>{t("settings.harness.tabAccount")}</Button>
		</div>;
	}
	const normalizedModelSearch = modelSearch.trim().toLocaleLowerCase();
	const models = normalizedModelSearch
		? allModels.filter((model) => `${model.label} ${model.id}`.toLocaleLowerCase().includes(normalizedModelSearch))
		: allModels;
	const pageCount = Math.max(1, Math.ceil(models.length / MODELS_PAGE_SIZE));
	const currentPage = Math.min(page, pageCount - 1);
	const pageStart = currentPage * MODELS_PAGE_SIZE;
	const visibleModels = models.slice(pageStart, pageStart + MODELS_PAGE_SIZE);
	return <div className="flex min-h-0 flex-1 flex-col gap-3">
		<h3 className="shrink-0 text-xs font-medium leading-4 text-settings-muted">{t("settings.harness.models.title")}</h3>
		{allModels.length > MODELS_PAGE_SIZE ? <label className="flex h-8 shrink-0 items-center gap-2 rounded-md border border-(--color-border-settings-input) bg-(--color-bg-settings-input) px-2.5">
			<Search aria-hidden="true" className="size-3.5 shrink-0 text-settings-muted" />
			<input type="search" aria-label={t("settings.models.searchAria", { label: t("settings.harness.models.title").toLocaleLowerCase() })}
				className="min-w-0 flex-1 bg-transparent text-sm text-settings-label outline-none placeholder:text-settings-muted"
				placeholder={t("settings.models.searchPlaceholder")} value={modelSearch} onChange={(event) => { setModelSearch(event.target.value); setPage(0); }} />
		</label> : null}
		{gap ? <p role="alert" className="text-xs text-error">{t("settings.harness.models.loadFailed")}</p> : null}
		{loading || gap ? null
			: !models.length ? <p className="px-3 py-6 text-center text-sm text-settings-muted">{t(normalizedModelSearch ? "settings.options.noMatches" : "settings.harness.models.empty", { agent })}</p>
			: <ul className={cn(HARNESS_CARD_CLASS, "settings-thin-scrollbar min-h-0 flex-initial overflow-y-auto overscroll-contain")} tabIndex={0} aria-label={t("settings.harness.models.title")}>
				{visibleModels.map((model) => <li key={model.id} className={cn(HARNESS_CARD_ROW_CLASS, "shrink-0 gap-4")}>
					<span className="flex min-w-0 flex-1 flex-wrap items-center gap-2 text-sm leading-5 text-settings-label">
						{agentModelDisplayLabel(agentId, model.label)}
						{model.isDefault ? <span className="rounded-sm bg-(--color-bg-settings-input) px-1.5 py-0.5 text-caption text-settings-muted">{t("settings.harness.models.default")}</span> : null}
					</span>
					{model.efforts?.length ? <span className="shrink-0 text-right text-xs text-settings-muted">
						{t("settings.harness.models.efforts", { levels: model.efforts.join(" · ") })}
					</span> : null}
				</li>)}
			</ul>}
		{pageCount > 1 ? <nav className="flex shrink-0 items-center justify-end gap-2 text-xs text-settings-muted" aria-label={t("settings.harness.models.pagination")}>
			<span className="tabular-nums">{t("settings.harness.models.pageRange", { start: pageStart + 1, end: pageStart + visibleModels.length, total: models.length })}</span>
			<Button type="button" size="icon-sm" variant="ghost" aria-label={t("settings.harness.models.previousPage")} disabled={currentPage === 0} onClick={() => setPage(currentPage - 1)}>
				<ChevronLeft aria-hidden="true" />
			</Button>
			<Button type="button" size="icon-sm" variant="ghost" aria-label={t("settings.harness.models.nextPage")} disabled={currentPage >= pageCount - 1} onClick={() => setPage(currentPage + 1)}>
				<ChevronRight aria-hidden="true" />
			</Button>
		</nav> : null}
	</div>;
}
