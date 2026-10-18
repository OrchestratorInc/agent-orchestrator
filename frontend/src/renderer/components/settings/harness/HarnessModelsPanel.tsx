import { useQuery } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, Search } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { agentModelsQueryOptions } from "../../../hooks/useAgentModelsQuery";
import { useSettings, useUpdateHarnessDefault } from "../../../hooks/useSettings";
import { agentModelDisplayLabel, isConcreteModelID, splitClaudeModels } from "../../../lib/agent-model-choices";
import { agentLabel, type AgentId } from "../../../lib/agent-options";
import { LOCAL_HOST } from "../../../lib/hosts";
import { groupModelProviders } from "../../../lib/model-providers";
import { cn } from "../../../lib/utils";
import { Button } from "../../ui/button";
import { Switch } from "../../ui/switch";
import { SettingsOptionMenu } from "../SettingsOptionMenu";
import { HARNESS_CARD_CLASS, HARNESS_CARD_ROW_CLASS, HarnessStatusDot } from "./harness-ui";

const MODELS_PAGE_SIZE = 10;

// Multi-provider catalogs can be browsed before credentials are configured.
// Account-scoped harnesses retain their sign-in gate.
export const MULTI_PROVIDER_HARNESSES = new Set<string>([
	"opencode", "opencode-v2", "kilocode", "pi", "omp", "kimchi", "prime-agent",
	"crush", "mimo-code", "command-code", "aider", "kimi", "codewhale",
	"continue", "goose", "vibe", "cline", "autohand", "qwen", "fx",
]);

/** The harness catalog, browsed by provider. */
export function HarnessModelsPanel({ agentId, hostId, installed, needsLogin, onOpenAccount }: {
	agentId: AgentId;
	hostId?: string;
	installed: boolean;
	needsLogin: boolean;
	onOpenAccount: () => void;
}) {
	const { i18n, t } = useTranslation();
	const options = agentModelsQueryOptions(agentId, "", hostId, true);
	const loginBlocksCatalog = needsLogin && !MULTI_PROVIDER_HARNESSES.has(agentId);
	const catalog = useQuery({ ...options, enabled: installed && !loginBlocksCatalog });
	const [page, setPage] = useState(0);
	const [modelSearch, setModelSearch] = useState("");
	const scope = `${hostId ?? LOCAL_HOST}:${agentId}`;
	const [providerChoice, setProviderChoice] = useState<{ scope: string; id: string }>();
	const { settings } = useSettings(hostId);
	const allModels = catalog.data?.models;
	const providers = useMemo(() => groupModelProviders(allModels ?? [], catalog.data?.configuredProviders), [allModels, catalog.data?.configuredProviders]);
	const showProviders = providers.some((provider) => provider.id !== "");
	// Switched-off models leave the project, New Task, and chat pickers; models the
	// catalog adds later are offered.
	const { update: updateHarnessDefault, error: disabledModelsError } = useUpdateHarnessDefault(hostId);
	// The list being saved shows at once; the saved settings replace it once no save is left waiting.
	const [disabledDraft, setDisabledDraft] = useState<{ scope: string; ids: string[] }>();
	const saveQueue = useRef<Promise<unknown>>(Promise.resolve());
	const queuedSaves = useRef(0);
	const savedDisabledIds = disabledDraft?.scope === scope ? disabledDraft.ids : settings?.harnessDefaults?.[agentId]?.disabledModels ?? [];
	const allModelIds = useMemo(() => (allModels ?? []).map((model) => model.id).filter(isConcreteModelID), [allModels]);
	// A list that switches off every catalog model filters nothing, like the pickers.
	const savedDisabledSet = new Set(savedDisabledIds);
	const allDisabled = allModelIds.length > 0 && allModelIds.every((id) => savedDisabledSet.has(id));
	const disabledIds = allDisabled ? [] : savedDisabledIds;
	const disabledSet = allDisabled ? new Set<string>() : savedDisabledSet;
	const isModelEnabled = (id: string) => !disabledSet.has(id);
	const enabledCount = allModelIds.filter(isModelEnabled).length;
	// `keepOn` stays switched on, so Disable all leaves the pickers one model.
	const setModelsEnabled = (ids: string[], on: boolean, keepOn?: string) => {
		// Ids the catalog no longer lists stay saved in case it lists them again.
		const next = new Set(disabledIds);
		for (const id of ids) {
			if (on) next.delete(id); else next.add(id);
		}
		if (keepOn) next.delete(keepOn);
		// Pickers need a model, so keep at least one on.
		if (allModelIds.every((id) => next.has(id))) return;
		const saved = [...next];
		setDisabledDraft({ scope, ids: saved });
		queuedSaves.current += 1;
		// One request at a time, in click order, so an older list never lands last.
		saveQueue.current = saveQueue.current
			.then(() => updateHarnessDefault({ agentId, disabledModels: saved }))
			.catch(() => undefined)
			.finally(() => {
				queuedSaves.current -= 1;
				if (queuedSaves.current === 0) setDisabledDraft(undefined);
			});
	};
	const initialModel = allModels?.find((model) => model.isDefault);
	const initialProviderID = initialModel ? initialModel.provider?.trim() ?? "" : undefined;
	const initialProvider = providers.find((provider) => provider.id === initialProviderID) ?? providers[0];
	const selectedProvider = (providerChoice?.scope === scope ? providers.find((provider) => provider.id === providerChoice.id) : undefined) ?? initialProvider;
	useEffect(() => { setPage(0); setModelSearch(""); }, [scope, selectedProvider?.id]);
	const agent = agentLabel(agentId);
	if (!installed || loginBlocksCatalog) {
		return <div className="space-y-3">
			<div className="flex flex-col items-center gap-3 px-3 py-8 text-center">
			<p className="text-sm text-settings-muted">{t(installed ? "settings.harness.models.needsLogin" : "settings.harness.models.notInstalled", { agent })}</p>
			<Button type="button" size="sm" variant="outline" onClick={onOpenAccount}>{t("settings.harness.tabAccount")}</Button>
		</div></div>;
	}
	const data = catalog.data;
	// Claude uses the New Task family order, with older models included in the same paginated list.
	const catalogModels = (showProviders ? selectedProvider?.models : allModels) ?? [];
	const claude = agentId === "claude-code" ? splitClaudeModels(catalogModels.filter((model) => isConcreteModelID(model.id))) : undefined;
	const providerModels = claude ? [...claude.current, ...claude.other] : catalogModels;
	const normalizedModelSearch = modelSearch.trim().toLocaleLowerCase();
	const models = normalizedModelSearch
		? providerModels.filter((model) => `${model.label} ${model.id}`.toLocaleLowerCase().includes(normalizedModelSearch))
		: providerModels;
	const switchableModels = models.filter((model) => isConcreteModelID(model.id));
	// Enable all and Disable all cover every provider's models, or only the search results.
	const bulkModelIds = normalizedModelSearch ? switchableModels.map((model) => model.id) : allModelIds;
	const bulkModelIdSet = new Set(bulkModelIds);
	// Pickers need at least one model, so Disable all keeps one on when nothing it leaves
	// alone is: the one already on in view, else the default.
	const keptOnDisable = allModelIds.some((id) => isModelEnabled(id) && !bulkModelIdSet.has(id))
		? undefined
		: (switchableModels.find((model) => model.isDefault && isModelEnabled(model.id))
			?? switchableModels.find((model) => isModelEnabled(model.id))
			?? switchableModels.find((model) => model.isDefault) ?? switchableModels[0])?.id;
	const pageCount = Math.max(1, Math.ceil(models.length / MODELS_PAGE_SIZE));
	const currentPage = Math.min(page, pageCount - 1);
	const pageStart = currentPage * MODELS_PAGE_SIZE;
	const visibleModels = models.slice(pageStart, pageStart + MODELS_PAGE_SIZE);
	const error = (catalog.error instanceof Error ? catalog.error.message : undefined) ?? data?.refreshError;
	const showConfiguredGroups = !data?.stale && providers.some((provider) => provider.configured);
	const renderProviderOption = (id: string, label: string, inMenu: boolean) => {
		const provider = providers.find((candidate) => candidate.id === id);
		const count = provider?.models.length ?? 0;
		const configured = provider?.configured && !data?.stale;
		const configuredLabel = t("settings.harness.models.configuredProvider");
		// Configured providers get only the status slot's dot and full-strength text so
		// the few with credentials stand out among hundreds of catalog providers.
		return <span className="flex min-w-0 flex-1 items-center gap-2">
			<span className="sr-only">{`${label} · ${count}${configured ? ` · ${configuredLabel}` : ""}`}</span>
			<span aria-hidden="true" className="flex size-1.5 shrink-0">{configured ? <HarnessStatusDot tone="ok" /> : null}</span>
			<span aria-hidden="true" className={cn("min-w-0 truncate", inMenu && !configured && "text-settings-muted")}>{label}</span>
			<span aria-hidden="true" className="ml-auto shrink-0 pl-2 text-xs tabular-nums text-settings-muted">{count}</span>
		</span>;
	};
	return <div className="flex min-h-0 flex-1 flex-col gap-3">
		{showProviders ? <div className="shrink-0 space-y-2">
			<h3 className="text-sm font-medium text-settings-label">{t("settings.harness.models.provider")}</h3>
			<SettingsOptionMenu
				aria-label={t("settings.harness.models.provider")} value={selectedProvider?.id ?? ""}
				options={providers.map((provider) => ({ value: provider.id, label: provider.id || t("settings.harness.models.unknownProvider"),
					// Configured providers sort first; head both runs only when there are some.
					group: showConfiguredGroups ? t(provider.configured ? "settings.harness.models.configuredProvider" : "settings.harness.models.otherProviders") : undefined }))}
				onChange={(id) => { setProviderChoice({ scope, id }); setPage(0); }}
				renderTrigger={(option) => option ? renderProviderOption(option.value, option.label, false) : null}
				renderMenuItem={(option) => renderProviderOption(option.value, option.label, true)}
				searchable={providers.length > 5} searchPlaceholder={t("settings.harness.models.searchProviders")} menuAlign="start"
				triggerClassName="w-full max-w-(--size-settings-menu-width) justify-between"
				menuClassName="w-(--radix-dropdown-menu-trigger-width)" menuItemClassName="w-full"
			/>
			{providers.some((provider) => provider.configured) && !data?.stale ? <p className="text-xs text-settings-muted">{t("settings.harness.models.configuredProviderHint")}</p> : null}
		</div> : null}
		<div className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1">
			<h3 className="text-xs font-medium leading-4 text-settings-muted">{t("settings.harness.models.title")}</h3>
			{allModelIds.length ? <span className="text-xs tabular-nums text-settings-muted">{t("settings.harness.models.enabledSummary", { enabled: enabledCount, total: allModelIds.length })}</span> : null}
			{bulkModelIds.length > 1 ? <span className="ml-auto flex items-center gap-1">
				<Button type="button" size="sm" variant="ghost" className="h-6 px-2 text-caption"
					disabled={bulkModelIds.every(isModelEnabled)}
					onClick={() => setModelsEnabled(bulkModelIds, true)}>{t("settings.harness.models.enableAll")}</Button>
				<Button type="button" size="sm" variant="ghost" className="h-6 px-2 text-caption"
					disabled={!bulkModelIds.some((id) => id !== keptOnDisable && isModelEnabled(id))}
					onClick={() => setModelsEnabled(bulkModelIds, false, keptOnDisable)}>{t("settings.harness.models.disableAll")}</Button>
			</span> : null}
		</div>
		{disabledModelsError ? <p role="alert" className="text-xs text-error">{disabledModelsError}</p> : null}
		{providerModels.length > MODELS_PAGE_SIZE ? <label className="flex h-8 shrink-0 items-center gap-2 rounded-md border border-(--color-border-settings-input) bg-(--color-bg-settings-input) px-2.5">
			<Search aria-hidden="true" className="size-3.5 shrink-0 text-settings-muted" />
			<input type="search" aria-label={t("settings.models.searchAria", { label: t("settings.harness.models.title").toLocaleLowerCase() })}
				className="min-w-0 flex-1 bg-transparent text-sm text-settings-label outline-none placeholder:text-settings-muted"
				placeholder={t("settings.models.searchPlaceholder")} value={modelSearch} onChange={(event) => { setModelSearch(event.target.value); setPage(0); }} />
		</label> : null}
		{error ? <p role="alert" className="text-xs text-error">{error}</p> : null}
		{catalog.isPending ? null
			: !models.length ? <p className="px-3 py-6 text-center text-sm text-settings-muted">{t(normalizedModelSearch ? "settings.options.noMatches" : "settings.harness.models.empty")}</p>
			: <ul className={cn(HARNESS_CARD_CLASS, "settings-thin-scrollbar min-h-0 flex-initial overflow-y-auto overscroll-contain")} tabIndex={0} aria-label={t("settings.harness.models.title")}>
				{visibleModels.map((model) => <li key={model.id} className={cn(HARNESS_CARD_ROW_CLASS, "shrink-0 gap-4")}>
					<span className="min-w-0 flex-1">
						<span className="flex flex-wrap items-center gap-2 text-sm leading-5 text-settings-label">
							{agentModelDisplayLabel(agentId, model.label)}
							{model.isDefault ? <span className="rounded-sm bg-(--color-bg-settings-input) px-1.5 py-0.5 text-caption text-settings-muted">{t("settings.harness.models.default")}</span> : null}
						</span>
						{/* Only facts the harness reports; nothing is shown in place of a missing value. */}
						{model.contextWindow ? <span className="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-xs text-settings-muted">
							{model.contextWindow ? <span>{t("settings.harness.models.context")}: {new Intl.NumberFormat(i18n.resolvedLanguage).format(model.contextWindow)}</span> : null}
						</span> : null}
					</span>
					{model.efforts?.length ? <span className="shrink-0 text-right text-xs text-settings-muted">
						{t("settings.harness.models.efforts", { levels: model.efforts.join(" · ") })}
					</span> : null}
					{isConcreteModelID(model.id) ? <Switch
						aria-label={t("settings.harness.models.toggleModel", { model: agentModelDisplayLabel(agentId, model.label) })}
						checked={isModelEnabled(model.id)}
						disabled={isModelEnabled(model.id) && enabledCount <= 1}
						onCheckedChange={(on) => setModelsEnabled([model.id], on)}
					/> : null}
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
