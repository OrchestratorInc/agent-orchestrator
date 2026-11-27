import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Columns2, Maximize2, Minimize2, Rows3, Search } from "@/components/app-icons";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../../hooks/useCloudCp";
import { useCloudSessionStartRequestInFlight, useCloudWorkspaceReady } from "../../hooks/useCloudWorkspaceReady";
import { cloudFilesManifest, cloudFilesReviewDataSource, cloudWorkspaceReviewSummaryQueryOptions, useCloudWorkspaceReviewEvents } from "../../hooks/useCloudWorkspaceReview";
import { useFileAnnotation } from "../../hooks/useFileAnnotation";
import { useFilesReviewSelection, type FilesSelectionStore } from "../../hooks/useFilesReviewSelection";
import type { FilesSource } from "../../hooks/useSessionWorkspaceFiles";
import { refreshCloudSessionLifecycle } from "../../hooks/useWorkspaceQuery";
import { annotationSource } from "../../lib/files-review-selection";
import { joinWorkspaceRepositoryPath, splitWorkspaceRepositoryPath, withWorkspaceRepository, type CloudCpWorkspaceReviewFileSummary, type CloudCpWorkspaceReviewScope } from "../../lib/cloud-cp";
import { cloudStartupProblem, resumeCloudSession } from "../../lib/cloud-lifecycle";
import type { WorkspaceSession } from "../../types/workspace";
import { useUiStore } from "../../stores/ui-store";
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
	/** Another branch's or a PR's commits, which the file is read from instead of the worktree. */
	source?: FilesSource;
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
	// Only a connected worker answers for its files: a waking session's worker
	// refuses every request, so the summary is not polled until it is back.
	// What was read before stays on screen meanwhile.
	const workerConnected = session.runtimeConnected === true;
	// A worker that has just connected can still be preparing the workspace (a
	// slow checkout takes minutes): the summary waits until it answers, and
	// the panel keeps loading meanwhile.
	const workspaceReady = useCloudWorkspaceReady(session);
	// A worker that will not come back (the session was deleted, or AO gave up
	// starting it) never answers. With nothing read before, the panel says so,
	// with Retry to start the session again, instead of loading forever.
	const startupProblem = cloudStartupProblem(session);
	const workerGone = !workerConnected &&
		(session.isTerminated === true || startupProblem?.phase === "failed" || startupProblem?.phase === "unavailable");
	const queryClient = useQueryClient();
	const [restartError, setRestartError] = useState<string>();
	// One start at a time: Retry waits while this session's resume is in flight.
	const restarting = useCloudSessionStartRequestInFlight(orgId, session.id);
	const restart = () => {
		if (!orgId || restarting) return;
		setRestartError(undefined);
		void resumeCloudSession(client, orgId, session.id)
			.then(() => refreshCloudSessionLifecycle(queryClient))
			.catch((error: unknown) => setRestartError(error instanceof Error ? error.message : String(error)));
	};
	const [internalSplit, setInternalSplit] = useState(() => window.localStorage.getItem("ao.cloud.files.diffStyle") === "split");
	const split = controlledSplit ?? internalSplit;
	const repository = useUiStore((state) => state.inspectorSessions[session.id]?.cloudReviewRepository ?? "");
	const setRepository = useUiStore((state) => state.setCloudReviewRepository);
	const mainSummaryQuery = useQuery({
		...cloudWorkspaceReviewSummaryQueryOptions({ client, baseUrl, orgId: orgId ?? "", sessionId: session.id, enabled: enabled && workspaceReady, visible: true }),
	});
	const repositories = mainSummaryQuery.data?.repositories ?? [];
	const switchable = repositories.length > 1;
	const activeRepository = switchable && repositories.some((entry) => !entry.main && entry.present && entry.name === repository) ? repository : "";
	const reviewUi = useUiStore((state) => state.inspectorSessions[session.id]?.cloudReviewUi?.[activeRepository]);
	const setReviewUi = useUiStore((state) => state.setCloudReviewUi);
	const storedShowChanges = reviewUi?.showChanges ?? true;
	const filter = reviewUi?.filter ?? "";
	const selectedPath = reviewUi?.selectedPath ?? null;
	const setShowChanges = (value: boolean) => setReviewUi(session.id, activeRepository, { showChanges: value });
	const setFilter = (value: string) => setReviewUi(session.id, activeRepository, { filter: value });
	const scopedClient = withWorkspaceRepository(client, activeRepository || undefined);
	const siblingSummaryQuery = useQuery({
		...cloudWorkspaceReviewSummaryQueryOptions({ client: scopedClient, baseUrl, orgId: orgId ?? "", sessionId: session.id, enabled: enabled && workspaceReady && activeRepository !== "", visible: true }),
	});
	const summaryQuery = activeRepository !== "" ? siblingSummaryQuery : mainSummaryQuery;
	// The branch or PR under review, picked from the same menus and rules as a
	// local review. A worker without views lists no branches, so the menu stays
	// hidden and the review shows the worktree as before.
	const summary = summaryQuery.data;
	const manifest = useMemo(() => (summary ? cloudFilesManifest(summary, session.id) : undefined), [session.id, summary]);
	const reviewDataSource = useMemo(() => cloudFilesReviewDataSource({ client: scopedClient, baseUrl, orgId: orgId ?? "", sessionId: session.id }), [baseUrl, orgId, scopedClient, session.id]);
	const selectionStore: FilesSelectionStore = {
		key: `cloud:${session.id}:${activeRepository}`,
		stored: reviewUi?.filesSelection,
		set: (next) => setReviewUi(session.id, activeRepository, { filesSelection: next }),
		reset: () => setReviewUi(session.id, activeRepository, { filesSelection: undefined }),
	};
	const checkedOutBranch = activeRepository ? repositories.find((entry) => entry.name === activeRepository)?.branch : session.branch;
	const review = useFilesReviewSelection({ sessionId: session.id, manifest, projectKind: "single_repo", checkedOutBranch, dataSource: reviewDataSource, selectionStore });
	const reviewBranch = review.target.branch;
	const viewing = reviewBranch.kind !== "checked_out" && review.branchMenu !== null;
	// Comments on a branch or PR tell the agent which one they are about.
	const sendViewAnnotation = useCallback(async (message: string) => { await client.sendSessionMessage(orgId ?? "", session.id, { text: message }); }, [client, orgId, session.id]);
	const viewAnnotation = useFileAnnotation(session.id, { source: annotationSource(reviewBranch), sendMessage: sendViewAnnotation });
	const reviewAnnotation = viewing ? viewAnnotation : annotation;
	const scopedAnnotation = useMemo(() => repositoryAnnotationModel(reviewAnnotation, activeRepository || undefined), [reviewAnnotation, activeRepository]);
	useCloudWorkspaceReviewEvents({ baseUrl, orgId, sessionId: session.id, enabled });
	useEffect(() => { window.localStorage.setItem("ao.cloud.files.diffStyle", split ? "split" : "unified"); }, [split]);
	useEffect(() => {
		if (mainSummaryQuery.data && repository !== activeRepository) setRepository(session.id, activeRepository);
	}, [mainSummaryQuery.data, repository, activeRepository, session.id, setRepository]);
	// The tree shows the worktree, so another branch or a PR is only reviewed.
	const showChanges = storedShowChanges || viewing;
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
		setReviewUi(session.id, activeRepository, { selectedPath: fullPath });
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
				{changedFiles.length > 0 && !viewing ? <div aria-label={t("files.viewMode")} className="flex items-center rounded-md border border-border bg-muted/30 p-0.5" role="tablist">
					<Button aria-selected={showChanges} data-active={showChanges} role="tab" className="text-2xs" onClick={() => setShowChanges(true)} size="xs" variant="ghost">{t("files.reviewChanges")}</Button>
					<Button aria-selected={!showChanges} data-active={!showChanges} role="tab" className="text-2xs" onClick={() => setShowChanges(false)} size="xs" variant="ghost">{t("files.allFiles")}</Button>
				</div> : null}
				{showChanges ? <Tooltip><TooltipTrigger asChild><Button aria-label={split ? t("files.unifiedDiff") : t("files.splitDiff")} aria-pressed={split} onClick={() => { const next = !split; if (controlledSplit === undefined) setInternalSplit(next); onSplitChange?.(next); }} size="icon-sm" variant="ghost">{split ? <Columns2 className="size-icon-sm" /> : <Rows3 className="size-icon-sm" />}</Button></TooltipTrigger><TooltipContent>{split ? t("files.unifiedDiff") : t("files.splitDiff")}</TooltipContent></Tooltip> : null}
				{onToggleMaximized ? <Tooltip><TooltipTrigger asChild><Button aria-label={isMaximized ? t("common.returnToPanel") : t("common.maximize")} onClick={() => onToggleMaximized(!isMaximized)} size="icon-sm" variant="ghost">{isMaximized ? <Minimize2 className="size-icon-sm" /> : <Maximize2 className="size-icon-sm" />}</Button></TooltipTrigger><TooltipContent>{isMaximized ? t("common.returnToPanel") : t("common.maximize")}</TooltipContent></Tooltip> : null}
			</header>
			{switchable ? <div aria-label={t("files.repositories", { defaultValue: "Repositories" })} className="flex shrink-0 items-center gap-1 overflow-x-auto border-b border-border bg-surface px-2 py-1" role="tablist">
				{[...repositories].sort((a, b) => Number(Boolean(b.main)) - Number(Boolean(a.main))).map((entry) => {
					const value = entry.main ? "" : entry.name;
					// The main checkout's folder is the provider's fixed name; label it with the repository's own.
					const label = entry.label || entry.name;
					const selected = value === activeRepository;
					return <Button aria-selected={selected} data-active={selected} role="tab" className="font-mono text-2xs" disabled={!entry.present} key={entry.name} onClick={() => { setRepository(session.id, value); setReviewUi(session.id, value, { filter: "" }); }} size="xs" title={entry.present ? entry.repository ?? label : t("files.repositoryMissing", { defaultValue: "{{name}} is not checked out in this session", name: label })} variant="ghost">{label}</Button>;
				})}
			</div> : null}
			{!enabled || (summaryQuery.isPending && !workerGone) ? <PanelMessage>{t("files.loading")}</PanelMessage> : summaryQuery.isPending ? (
				<PanelMessage action={session.isTerminated ? undefined : <RetryButton disabled={restarting} onClick={restart} />}>{restartError ?? startupProblem?.message ?? t("inspector.filesUnavailable")}</PanelMessage>
			) : summaryQuery.isError ? <PanelMessage action={<RetryButton onClick={() => void summaryQuery.refetch()} />}>{summaryQuery.error.message}</PanelMessage> : showChanges ? (
				<CloudWorkspaceReviewPane annotation={scopedAnnotation} baseUrl={baseUrl} client={scopedClient} data={summaryQuery.data} filter={filter} key={activeRepository} onBrowseAll={() => setShowChanges(false)} onOpenFile={open} orgId={orgId!} ready={workspaceReady} repository={activeRepository || undefined} review={review} sessionId={session.id} split={split} />
			) : <CloudFileTree baseUrl={baseUrl} client={scopedClient} filterText={filter} key={activeRepository} onOpenFile={(path) => open(path, { mode: "file" })} orgId={orgId!} ready={workspaceReady} selectedPath={selectedInRepository} sessionId={session.id} />}
		</section>
	);
}
