import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { agentLabel, type AgentId } from "../../../lib/agent-options";
import { apiClient, apiErrorCode, apiErrorMessage } from "../../../lib/api-client";
import { workspaceQueryKey, workspaceQueryOptions } from "../../../hooks/useWorkspaceQuery";
import { CLOUD_PROJECT_KIND, STANDALONE_WORKSPACE_ID } from "../../../types/workspace";
import { Button } from "../../ui/button";
import { Checkbox } from "../../ui/checkbox";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../../ui/select";
import type { components } from "../../../../api/schema";

type Candidate = components["schemas"]["SessionImportCandidate"];
type ImportResult = components["schemas"]["SessionImportResult"];

/** Agents whose local chats AO can import. */
export const IMPORTABLE_AGENTS: ReadonlySet<AgentId> = new Set<AgentId>(["claude-code", "codex"]);

const offerClosedKey = "ao.sessionImport.offerClosed";
const offerListeners = new Set<() => void>();
const folderFilter = "folder:";

/** Home offers the import once: until the user imports or dismisses it. */
export function sessionImportOfferClosed(): boolean {
	try {
		return window.localStorage.getItem(offerClosedKey) === "true";
	} catch {
		return false;
	}
}

export function subscribeSessionImportOffer(listener: () => void): () => void {
	offerListeners.add(listener);
	return () => offerListeners.delete(listener);
}

export function closeSessionImportOffer(): void {
	try {
		window.localStorage.setItem(offerClosedKey, "true");
	} catch {
		// The offer comes back next launch; the agent page still has the import.
	}
	for (const listener of offerListeners) listener();
}

function folderName(path: string): string {
	return path.split(/[\\/]/).filter(Boolean).at(-1) ?? path;
}

/** One agent's chats on this computer, from the daemon's validated preview. A
 * chat from a Git folder that is not a project yet adds that folder as a
 * project on import. */
export function SessionImportPanel({ harness }: { harness: AgentId }) {
	const { t } = useTranslation();
	const [project, setProject] = useState("all");
	const [selected, setSelected] = useState(new Set<string>());
	const [results, setResults] = useState<ImportResult[]>([]);
	const [importing, setImporting] = useState(false);
	// The scan an import runs after adding folders must not reset its results.
	const importingRef = useRef(false);
	const [error, setError] = useState<string>();
	const queryClient = useQueryClient();
	const workspaces = useQuery(workspaceQueryOptions);
	const projects = useMemo(
		() =>
			(workspaces.data ?? [])
				.filter((w) => !w.hostId && w.kind !== CLOUD_PROJECT_KIND && w.id !== STANDALONE_WORKSPACE_ID)
				.map((w) => ({ id: w.id, name: w.name })),
		[workspaces.data],
	);
	const preview = useQuery({
		queryKey: ["session-import-preview"],
		retry: false,
		staleTime: 0,
		refetchOnWindowFocus: false,
		refetchOnReconnect: false,
		queryFn: async ({ signal }) => {
			const response = await apiClient.GET("/api/v1/session-import", {
				signal,
			});
			if (response.error) throw new Error(apiErrorMessage(response.error));
			return response.data;
		},
	});
	useEffect(() => {
		if (!preview.data || importingRef.current) return;
		setSelected(
			new Set(
				preview.data.candidates
					.filter((c) => c.suggested && !c.sessionId)
					.map((c) => c.id),
			),
		);
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
	const visible = useMemo(
		() =>
			candidates.filter(
				(c) =>
					project === "all" ||
					(project === "standalone"
						? !c.projectId && !c.folder
						: project.startsWith(folderFilter)
							? c.folder === project.slice(folderFilter.length)
							: c.projectId === project),
			),
		[candidates, project],
	);
	const selectedVisible = visible.filter(
		(c) =>
			selected.has(c.id) &&
			!c.sessionId &&
			!results.some((r) => r.id === c.id && r.status !== "failed"),
	);
	const newFolders = [...new Set(selectedVisible.flatMap((c) => (c.folder ? [c.folder] : [])))];
	const names = new Map(projects.map((p) => [p.id, p.name]));
	const toggle = (ids: string[], checked: boolean) =>
		setSelected((current) => {
			const next = new Set(current);
			for (const id of ids) {
				if (checked) next.add(id);
				else next.delete(id);
			}
			return next;
		});
	const eligible = visible.filter(
		(c) =>
			!c.sessionId &&
			!results.some((r) => r.id === c.id && r.status !== "failed"),
	);
	const importSelected = async () => {
		setImporting(true);
		importingRef.current = true;
		setError(undefined);
		const ids = selectedVisible.map((c) => c.id);
		try {
			// A chat imports into its folder's project: add new folders, then scan again.
			for (const path of newFolders) {
				const added = await apiClient.POST("/api/v1/projects", { body: { path } });
				if (added.error && apiErrorCode(added.error) !== "PATH_ALREADY_REGISTERED") {
					throw new Error(apiErrorMessage(added.error));
				}
			}
			if (newFolders.length) {
				setProject("all");
				const rescan = await preview.refetch();
				if (rescan.error) throw rescan.error;
			}
			const response = await apiClient.POST("/api/v1/session-import", {
				body: { ids },
			});
			if (response.error) throw new Error(apiErrorMessage(response.error));
			setResults(response.data.results);
			setSelected(
				new Set(
					response.data.results
						.filter((r) => r.status === "failed")
						.map((r) => r.id),
				),
			);
			if (response.data.results.some((r) => r.status === "created")) closeSessionImportOffer();
		} catch (err) {
			setError(
				err instanceof Error ? err.message : t("sessionImport.failed"),
			);
		} finally {
			// A request that fails partway may still have created sessions.
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
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
		<div className="flex flex-col gap-3">
			<p className="text-xs text-muted-foreground">
				{t("sessionImport.description", { agent: agentLabel(harness) })}
			</p>
			<div className="flex flex-wrap gap-3">
				<div className="flex flex-col gap-1 text-xs">
					<span aria-hidden="true">{t("newTask.project")}</span>
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
				</div>
			</div>
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
			<div className="rounded-md border border-border">
				<label className="sticky top-0 flex items-center gap-3 border-b border-border bg-surface px-3 py-2 text-xs">
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
					{t("sessionImport.selectAll", { count: visible.length })}
				</label>
				{visible.map((c) => (
					<ImportRow
						key={c.id}
						candidate={c}
						projectName={
							c.projectId
								? (names.get(c.projectId) ?? c.projectId)
								: c.folder
									? t("sessionImport.newProject", { name: folderName(c.folder) })
									: t("remote.standalone")
						}
						selected={selected.has(c.id)}
						disabled={importing}
						result={results.find((r) => r.id === c.id)}
						onSelect={(checked) => toggle([c.id], checked)}
					/>
				))}
				{!preview.isFetching && !visible.length ? (
					<p className="p-6 text-sm text-muted-foreground">
						{t("sessionImport.empty")}
					</p>
				) : null}
			</div>
			{newFolders.length ? (
				<p className="text-xs text-muted-foreground">
					{t("sessionImport.addsProjects", {
						count: newFolders.length,
						names: newFolders.map(folderName).join(", "),
					})}
				</p>
			) : null}
			{results.length ? (
				<p role="status" className="text-sm">
					{summarize(results)}
				</p>
			) : null}
			<div className="flex items-center justify-between gap-3">
				<Button
					variant="ghost"
					onClick={() => {
						setError(undefined);
						void preview.refetch();
					}}
					disabled={importing || preview.isFetching}
				>
					{t("sessionImport.scanAgain")}
				</Button>
				<Button
					onClick={() => void importSelected()}
					disabled={
						!selectedVisible.length ||
						selectedVisible.length > 500 ||
						importing ||
						preview.isFetching
					}
				>
					{importing
						? t("sessionImport.importing")
						: t("sessionImport.import", { count: selectedVisible.length })}
				</Button>
			</div>
			{selectedVisible.length > 500 ? (
				<p role="alert" className="text-xs text-destructive">
					{t("sessionImport.limit")}
				</p>
			) : null}
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
}: {
	candidate: Candidate;
	projectName: string;
	selected: boolean;
	disabled: boolean;
	result?: ImportResult;
	onSelect: (checked: boolean) => void;
}) {
	const { t, i18n } = useTranslation();
	const imported = Boolean(
		c.sessionId || (result && result.status !== "failed"),
	);
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
				<span className="text-xs text-muted-foreground">
					{projectName} · {agentLabel(c.harness)} · {t("sessionImport.messages", { count: c.messageCount })} ·{" "}
					{new Date(c.lastActivity).toLocaleDateString(i18n.resolvedLanguage)}
				</span>
				{c.shortened ? (
					<span className="block text-xs text-warning">
						{t("sessionImport.shortened")}
					</span>
				) : null}
				{imported ? (
					<span className="block text-xs text-muted-foreground">
						{t("sessionImport.alreadyImported")}
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
