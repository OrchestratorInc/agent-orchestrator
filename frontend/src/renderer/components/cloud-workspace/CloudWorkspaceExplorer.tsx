import { useQuery } from "@tanstack/react-query";
import { Columns2, Maximize2, Minimize2, Rows3, Search } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../../hooks/useCloudCp";
import { cloudWorkspaceReviewSummaryQueryOptions, useCloudWorkspaceReviewEvents } from "../../hooks/useCloudWorkspaceReview";
import { joinWorkspaceRepositoryPath, splitWorkspaceRepositoryPath, withWorkspaceRepository, type CloudCpWorkspaceReviewFileSummary, type CloudCpWorkspaceReviewScope } from "../../lib/cloud-cp";
import type { WorkspaceSession } from "../../types/workspace";
import { PanelMessage, RetryButton, type FileAnnotationModel } from "../WorkspaceDiffView";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { CloudFileTree } from "./CloudFileTree";
import { CloudWorkspaceReviewPane } from "./CloudWorkspaceReviewPane";
import { repositoryAnnotationModel } from "./repositoryAnnotation";

export type CloudFileOpenOptions = {
	scope?: CloudCpWorkspaceReviewScope;
	commitSha?: string;
	mode?: "diff" | "file" | "rendered";
	editing?: boolean;
};

type CloudWorkspaceExplorerProps = {
	annotation?: FileAnnotationModel;
	session: WorkspaceSession;
	isMaximized?: boolean;
	onOpenFile?: (path: string, options?: CloudFileOpenOptions) => void;
	onSplitChange?: (split: boolean) => void;
	onToggleMaximized?: (next: boolean) => void;
	split?: boolean;
};

const noAnnotation: FileAnnotationModel = { targets: [], status: "idle", error: "", begin: () => undefined, draftFor: () => "", statusFor: () => "idle", setDraft: () => undefined, cancel: () => undefined, submit: async () => undefined };

export function CloudWorkspaceExplorer({ annotation = noAnnotation, session, isMaximized = false, onOpenFile, onSplitChange, onToggleMaximized, split: controlledSplit }: CloudWorkspaceExplorerProps) {
	const { t } = useTranslation();
	const { client, ready, baseUrl } = useCloudCp();
	const orgId = session.cloud?.orgId;
	const enabled = ready && orgId !== undefined;
	const [showChanges, setShowChanges] = useState(true);
	const [filter, setFilter] = useState("");
	const [internalSplit, setInternalSplit] = useState(() => window.localStorage.getItem("ao.cloud.files.diffStyle") === "split");
	const split = controlledSplit ?? internalSplit;
	const [selectedPath, setSelectedPath] = useState<string | null>(null);
	// A multi-repository session reviews and browses one repository at a time:
	// the main one ("") or a sibling, by folder name.
	const [repository, setRepository] = useState("");
	const mainSummaryQuery = useQuery({
		...cloudWorkspaceReviewSummaryQueryOptions({ client, baseUrl, orgId: orgId ?? "", sessionId: session.id, enabled, visible: true }),
	});
	const repositories = mainSummaryQuery.data?.repositories ?? [];
	const switchable = repositories.length > 1;
	const activeRepository = switchable && repositories.some((entry) => !entry.main && entry.name === repository) ? repository : "";
	const scopedClient = withWorkspaceRepository(client, activeRepository || undefined);
	const siblingSummaryQuery = useQuery({
		...cloudWorkspaceReviewSummaryQueryOptions({ client: scopedClient, baseUrl, orgId: orgId ?? "", sessionId: session.id, enabled: enabled && activeRepository !== "", visible: true }),
	});
	const summaryQuery = activeRepository !== "" ? siblingSummaryQuery : mainSummaryQuery;
	const scopedAnnotation = useMemo(() => repositoryAnnotationModel(annotation, activeRepository || undefined), [annotation, activeRepository]);
	useCloudWorkspaceReviewEvents({ baseUrl, orgId, sessionId: session.id, enabled });
	useEffect(() => { window.localStorage.setItem("ao.cloud.files.diffStyle", split ? "split" : "unified"); }, [split]);
	useEffect(() => { setFilter(""); setSelectedPath(null); setShowChanges(true); setRepository(""); }, [session.id]);
	const changedFiles = useMemo(() => {
		const data = summaryQuery.data;
		if (!data) return [];
		const seen = new Set<string>();
		const result: Array<{ file: CloudCpWorkspaceReviewFileSummary; scope: CloudCpWorkspaceReviewScope }> = [];
		for (const [scope, files] of [["unstaged", data.sections.unstaged], ["staged", data.sections.staged], ["untracked", data.sections.untracked], ["committed", data.sections.committed]] as const) {
			for (const file of files) if (!seen.has(file.path)) { seen.add(file.path); result.push({ file, scope }); }
		}
		return result;
	}, [summaryQuery.data]);
	// A sibling's file opens as ../<repository>/<path>, so its tab and calls stay with that repository.
	const open = (path: string, options?: CloudFileOpenOptions) => {
		const fullPath = joinWorkspaceRepositoryPath(activeRepository || undefined, path);
		setSelectedPath(fullPath);
		onOpenFile?.(fullPath, options);
	};
	const selectedInRepository = selectedPath === null ? null : (() => {
		const split = splitWorkspaceRepositoryPath(selectedPath);
		return (split.repository ?? "") === activeRepository ? split.path : null;
	})();

	return (
		<section aria-label={t("files.sessionFiles")} className="flex h-full min-h-0 flex-col bg-background text-foreground">
			<header className="flex h-10 shrink-0 items-center gap-0.5 border-b border-border bg-surface px-2">
				<label className="relative mr-1 min-w-0 flex-1"><Search className="pointer-events-none absolute left-2.5 top-1/2 size-icon-sm -translate-y-1/2 text-passive" /><Input aria-label={t("files.explorer.filter")} className="h-8 pl-8 font-mono text-xs" onChange={(event) => setFilter(event.target.value)} placeholder={t("files.explorer.filterPlaceholder")} value={filter} /></label>
				{changedFiles.length > 0 ? <div aria-label={t("files.viewMode")} className="flex items-center rounded-md border border-border bg-muted/30 p-0.5" role="tablist">
					<Button aria-selected={showChanges} className="h-6 px-2 text-2xs" onClick={() => setShowChanges(true)} role="tab" size="sm" variant={showChanges ? "secondary" : "ghost"}>{t("files.reviewChanges")}</Button>
					<Button aria-selected={!showChanges} className="h-6 px-2 text-2xs" onClick={() => setShowChanges(false)} role="tab" size="sm" variant={!showChanges ? "secondary" : "ghost"}>{t("files.allFiles")}</Button>
				</div> : null}
				{showChanges ? <Tooltip><TooltipTrigger asChild><Button aria-label={split ? t("files.unifiedDiff") : t("files.splitDiff")} aria-pressed={split} onClick={() => { const next = !split; if (controlledSplit === undefined) setInternalSplit(next); onSplitChange?.(next); }} size="icon-sm" variant="ghost">{split ? <Columns2 className="size-icon-sm" /> : <Rows3 className="size-icon-sm" />}</Button></TooltipTrigger><TooltipContent>{split ? t("files.unifiedDiff") : t("files.splitDiff")}</TooltipContent></Tooltip> : null}
				{onToggleMaximized ? <Tooltip><TooltipTrigger asChild><Button aria-label={isMaximized ? t("files.minimize") : t("files.maximize")} onClick={() => onToggleMaximized(!isMaximized)} size="icon-sm" variant="ghost">{isMaximized ? <Minimize2 className="size-icon-sm" /> : <Maximize2 className="size-icon-sm" />}</Button></TooltipTrigger><TooltipContent>{isMaximized ? t("files.minimize") : t("files.maximize")}</TooltipContent></Tooltip> : null}
			</header>
			{switchable ? <div aria-label={t("files.repositories", { defaultValue: "Repositories" })} className="flex shrink-0 items-center gap-1 overflow-x-auto border-b border-border bg-surface px-2 py-1" role="tablist">
				{[...repositories].sort((a, b) => Number(Boolean(b.main)) - Number(Boolean(a.main))).map((entry) => {
					const value = entry.main ? "" : entry.name;
					// The main checkout's folder is the provider's fixed name; label it with the repository's own.
					const label = entry.label || entry.name;
					const selected = value === activeRepository;
					return <Button aria-selected={selected} className="h-6 shrink-0 px-2 font-mono text-2xs" disabled={!entry.present} key={entry.name} onClick={() => { setRepository(value); setFilter(""); }} role="tab" size="sm" title={entry.present ? entry.repository ?? label : t("files.repositoryMissing", { defaultValue: "{{name}} is not checked out in this session", name: label })} variant={selected ? "secondary" : "ghost"}>{label}</Button>;
				})}
			</div> : null}
			{!enabled || summaryQuery.isPending ? <PanelMessage>{t("files.loading")}</PanelMessage> : summaryQuery.isError ? <PanelMessage action={<RetryButton onClick={() => void summaryQuery.refetch()} />}>{summaryQuery.error.message}</PanelMessage> : showChanges ? (
				<CloudWorkspaceReviewPane annotation={scopedAnnotation} baseUrl={baseUrl} client={scopedClient} data={summaryQuery.data} filter={filter} key={activeRepository} onBrowseAll={() => setShowChanges(false)} onOpenFile={open} orgId={orgId!} repository={activeRepository || undefined} sessionId={session.id} split={split} />
			) : <CloudFileTree baseUrl={baseUrl} client={scopedClient} filterText={filter} key={activeRepository} onOpenFile={(path) => open(path, { mode: "file" })} orgId={orgId!} selectedPath={selectedInRepository} sessionId={session.id} />}
		</section>
	);
}
