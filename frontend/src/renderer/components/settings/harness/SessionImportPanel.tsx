import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Loader2 } from "@/components/app-icons";
import { agentLabel, type AgentId } from "../../../lib/agent-options";
import { workspaceQueryOptions } from "../../../hooks/useWorkspaceQuery";
import { formatTimeCompact } from "../../../lib/format-time";
import { projectNavigateTarget, useNavigateToSession } from "../../../lib/navigate-to-session";
import { folderName, importableProject, importChats, sessionImportPreviewQuery, type ImportCandidate as Candidate, type ImportResult } from "../../../lib/session-import";
import { useUiStore } from "../../../stores/ui-store";
import { Button } from "../../ui/button";
import { Checkbox } from "../../ui/checkbox";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../../ui/select";
import { SettingsSearch } from "../SettingsSearch";

const folderFilter = "folder:";

/** One agent's chats on this computer, from the daemon's validated preview. A
 * chat from a Git folder that is not a project yet adds that folder as a
 * project on import. */
export function SessionImportPanel({ harness }: { harness: AgentId }) {
	const { t } = useTranslation();
	const [project, setProject] = useState("all");
	const [query, setQuery] = useState("");
	const [selected, setSelected] = useState(new Set<string>());
	const [results, setResults] = useState<ImportResult[]>([]);
	const [importing, setImporting] = useState(false);
	// The scan an import runs after adding folders must not reset its results.
	const importingRef = useRef(false);
	// Choices made on the last list (a selection or an import) survive the next scan.
	const keepChoicesRef = useRef(false);
	const [error, setError] = useState<string>();
	const queryClient = useQueryClient();
	const navigate = useNavigate();
	const navigateToSession = useNavigateToSession();
	const closeSettings = useUiStore((state) => state.closeSettings);
	const closeSessionImportOffer = useUiStore((state) => state.closeSessionImportOffer);
	const workspaces = useQuery(workspaceQueryOptions);
	const projects = useMemo(
		() =>
			(workspaces.data ?? [])
				.filter(importableProject)
				.map((w) => ({ id: w.id, name: w.name })),
		[workspaces.data],
	);
	const preview = useQuery(sessionImportPreviewQuery);
	useEffect(() => {
		if (!preview.data || importingRef.current) return;
		const list = preview.data.candidates;
		if (keepChoicesRef.current) {
			const open = new Set(list.filter((c) => !c.sessionId).map((c) => c.id));
			setSelected((current) => new Set([...current].filter((id) => open.has(id))));
			return;
		}
		setSelected(new Set(list.filter((c) => !c.sessionId).map((c) => c.id)));
		setResults([]);
	}, [preview.data]);
	const candidates = useMemo(
		() => (preview.data?.candidates ?? []).filter((c) => c.harness === harness),
		[preview.data, harness],
	);
	const folders = useMemo(
		() => [...new Set(candidates.flatMap((c) => (c.folder ? [c.folder] : [])))].sort(),
		[candidates],
	);
	const names = useMemo(() => new Map(projects.map((p) => [p.id, p.name])), [projects]);
	const projectName = (c: Candidate) =>
		c.projectId
			? (names.get(c.projectId) ?? c.projectId)
			: c.folder
				? t("sessionImport.newProject", { name: folderName(c.folder) })
				: t("remote.standalone");
	const needle = query.trim().toLocaleLowerCase();
	const visible = candidates.filter(
		(c) =>
			(project === "all" ||
				(project === "standalone"
					? !c.projectId && !c.folder
					: project.startsWith(folderFilter)
						? c.folder === project.slice(folderFilter.length)
						: c.projectId === project)) &&
			(!needle || [c.title, projectName(c), c.folder ?? ""].some((text) => text.toLocaleLowerCase().includes(needle))),
	);
	const importedIds = new Set(results.filter((r) => r.status !== "failed").map((r) => r.id));
	const eligible = visible.filter((c) => !c.sessionId && !importedIds.has(c.id));
	const selectedVisible = eligible.filter((c) => selected.has(c.id));
	const newFolders = [...new Set(selectedVisible.flatMap((c) => (c.folder ? [c.folder] : [])))];
	// Leaving Settings for an imported chat opens it; the sidebar reveals the open session.
	const openSession = (c: Candidate, sessionId: string) => {
		closeSettings();
		navigateToSession(c.projectId, sessionId);
	};
	const created = results.flatMap((r) => {
		const c = r.status === "created" && r.sessionId ? candidates.find((item) => item.id === r.id) : undefined;
		return c && r.sessionId ? [{ candidate: c, sessionId: r.sessionId }] : [];
	});
	const createdProjects = new Set(created.map((x) => x.candidate.projectId));
	const onlyProject = createdProjects.size === 1 ? [...createdProjects][0] : undefined;
	const toggle = (ids: string[], checked: boolean) => {
		keepChoicesRef.current = true;
		setSelected((current) => {
			const next = new Set(current);
			for (const id of ids) {
				if (checked) next.add(id);
				else next.delete(id);
			}
			return next;
		});
	};
	const importSelected = async () => {
		setImporting(true);
		importingRef.current = true;
		keepChoicesRef.current = true;
		setError(undefined);
		const ids = selectedVisible.map((c) => c.id);
		try {
			if (newFolders.length) setProject("all");
			const imported = await importChats(queryClient, "settings", ids, newFolders);
			setResults(imported);
			setSelected(new Set(imported.filter((r) => r.status === "failed").map((r) => r.id)));
			if (imported.some((r) => r.status === "created")) closeSessionImportOffer();
		} catch (err) {
			setError(
				err instanceof Error ? err.message : t("sessionImport.failed"),
			);
		} finally {
			importingRef.current = false;
			setImporting(false);
		}
	};
	const summarize = (items: ImportResult[]) =>
		([
			["created", "sessionImport.result"],
			["already_imported", "sessionImport.resultExisting"],
			["failed", "sessionImport.resultFailed"],
		] as const)
			.map(([status, key]) => [items.filter((r) => r.status === status).length, key] as const)
			.filter(([n]) => n > 0)
			.map(([n, key]) => t(key, { count: n }))
			.join(" ");
	return (
		// Only the list scrolls; the filter stays on top and the actions stay on the bottom.
		<div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3">
			<div className="flex shrink-0 flex-col gap-3">
				<p className="text-xs text-muted-foreground">
					{t("sessionImport.description", { agent: agentLabel(harness) })}
				</p>
				<SettingsSearch
					value={query}
					onChange={setQuery}
					placeholder={t("sessionImport.searchPlaceholder")}
					aria-label={t("sessionImport.searchPlaceholder")}
					trailing={
						<Select value={project} onValueChange={setProject} disabled={importing}>
							<SelectTrigger size="sm" className="w-56 text-control" aria-label={t("newTask.project")}>
								<SelectValue />
							</SelectTrigger>
							<SelectContent position="popper" side="bottom" align="start" sideOffset={4} className="max-h-64">
								<SelectItem value="all">{t("sessionImport.allProjects")}</SelectItem>
								<SelectItem value="standalone">{t("remote.standalone")}</SelectItem>
								{projects.map((p) => (
									<SelectItem key={p.id} value={p.id}>
										{p.name}
									</SelectItem>
								))}
								{folders.map((path) => (
									<SelectItem key={path} value={folderFilter + path}>
										{t("sessionImport.newProject", { name: folderName(path) })}
									</SelectItem>
								))}
							</SelectContent>
						</Select>
					}
				/>
				<p className="text-xs text-muted-foreground">
					{t("sessionImport.selectionHelp")}
				</p>
				{preview.isFetching ? (
					<p role="status" className="flex items-center gap-2 text-sm">
						<Loader2 className="size-4 animate-spin" aria-hidden="true" />
						{t("sessionImport.reading")}
					</p>
				) : null}
				{preview.error || error ? (
					<p role="alert" className="text-sm text-destructive">
						{error ?? preview.error?.message}
					</p>
				) : null}
			</div>
			{preview.isPending ? null : (
				<div className="flex min-h-0 w-full min-w-0 flex-col overflow-hidden rounded-md border border-border">
					{/* Outside the scroller: rows never pass under it, so no fill is needed. */}
					<label className="flex shrink-0 items-center gap-3 border-b border-border px-3 py-2 text-xs">
						<Checkbox
							aria-label={t("sessionImport.selectAllAria")}
							checked={
								eligible.length > 0 && eligible.every((c) => selected.has(c.id))
							}
							onCheckedChange={(checked) =>
								toggle(
									eligible.map((c) => c.id),
									checked === true,
								)
							}
							disabled={importing || !eligible.length}
						/>
						{t("sessionImport.selectAll", { count: eligible.length })}
					</label>
					<div className="settings-thin-scrollbar min-h-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain">
						{visible.map((c) => (
							<ImportRow
								key={c.id}
								candidate={c}
								projectName={projectName(c)}
								selected={selected.has(c.id)}
								disabled={importing}
								result={results.find((r) => r.id === c.id)}
								onSelect={(checked) => toggle([c.id], checked)}
								onOpen={(sessionId) => openSession(c, sessionId)}
							/>
						))}
						{!preview.isFetching && !visible.length ? (
							<p className="p-6 text-sm text-muted-foreground">
								{needle ? t("sessionImport.noMatches", { query: query.trim() }) : t("sessionImport.empty")}
							</p>
						) : null}
					</div>
				</div>
			)}
			{preview.isPending ? null : (
				<div className="flex shrink-0 flex-col gap-3">
					{newFolders.length ? (
						<p className="text-xs text-muted-foreground">
							{t("sessionImport.addsProjects", {
								count: newFolders.length,
								names: newFolders.map(folderName).join(", "),
							})}
						</p>
					) : null}
					{results.length ? (
						<div className="flex flex-wrap items-center gap-x-3 gap-y-2">
							<p role="status" className="text-sm">
								{summarize(results)}
							</p>
							{created.length === 1 ? (
								<Button size="sm" variant="secondary" onClick={() => openSession(created[0].candidate, created[0].sessionId)}>
									{t("sessionImport.openChat")}
								</Button>
							) : onlyProject ? (
								<Button
									size="sm"
									variant="secondary"
									onClick={() => {
										closeSettings();
										void navigate(projectNavigateTarget(onlyProject));
									}}
								>
									{t("sessionImport.showProject")}
								</Button>
							) : null}
						</div>
					) : null}
					<div className="flex items-center justify-between gap-3">
						<Button
							variant="ghost"
							onClick={() => {
								setError(undefined);
								keepChoicesRef.current = false;
								// A rescan with an unchanged list keeps preview.data's identity, so the
								// effect would not run: reset here.
								setResults([]);
								void preview.refetch().then(({ data }) => {
									if (data && !keepChoicesRef.current) {
										setSelected(new Set(data.candidates.filter((c) => !c.sessionId).map((c) => c.id)));
									}
								});
							}}
							disabled={importing || preview.isFetching}
						>
							{t("sessionImport.scanAgain")}
						</Button>
						<Button
							onClick={() => void importSelected()}
							disabled={!selectedVisible.length || importing}
						>
							{importing
								? t("sessionImport.importing")
								: t("sessionImport.import", { count: selectedVisible.length })}
						</Button>
					</div>
				</div>
			)}
		</div>
	);
}

function ImportRow({
	candidate: c,
	projectName,
	selected,
	disabled,
	result,
	onSelect,
	onOpen,
}: {
	candidate: Candidate;
	projectName: string;
	selected: boolean;
	disabled: boolean;
	result?: ImportResult;
	onSelect: (checked: boolean) => void;
	onOpen: (sessionId: string) => void;
}) {
	const { t, i18n } = useTranslation();
	const imported = Boolean(
		c.sessionId || (result && result.status !== "failed"),
	);
	const sessionId = c.sessionId || (result?.status !== "failed" ? result?.sessionId : undefined);
	return (
		<label className="flex cursor-pointer items-start gap-3 border-b border-border px-3 py-3 last:border-0">
			<Checkbox
				aria-label={t("sessionImport.selectConversation", { title: c.title })}
				className="mt-0.5"
				checked={selected && !imported}
				disabled={disabled || imported}
				onCheckedChange={(checked) => onSelect(checked === true)}
			/>
			<span className="min-w-0 flex-1">
				<span className="block truncate text-sm font-medium">{c.title}</span>
				<span className="block truncate text-xs text-muted-foreground">
					<span title={c.folder}>{projectName}</span> · {t("sessionImport.messages", { count: c.messageCount })} ·{" "}
					<time dateTime={c.lastActivity} title={new Date(c.lastActivity).toLocaleString(i18n.resolvedLanguage)}>
						{formatTimeCompact(c.lastActivity, { longUnits: true })}
					</time>
				</span>
				{c.shortened ? (
					<span className="block text-xs text-warning">
						{t("sessionImport.shortened")}
					</span>
				) : null}
				{imported ? (
					<span className="flex items-center gap-2 text-xs text-muted-foreground">
						{t("sessionImport.alreadyImported")}
						{sessionId ? (
							<Button
								size="xs"
								variant="ghost"
								onClick={(event) => {
									event.preventDefault();
									onOpen(sessionId);
								}}
							>
								{t("sessionImport.open")}
							</Button>
						) : null}
					</span>
				) : null}
				{result?.error ? (
					<span role="alert" className="block text-xs text-destructive">
						{result.error}
					</span>
				) : null}
			</span>
		</label>
	);
}
