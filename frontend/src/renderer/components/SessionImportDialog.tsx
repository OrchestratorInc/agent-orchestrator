import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, Loader2 } from "lucide-react";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogHeader,
	DialogTitle,
	DialogTrigger,
} from "./ui/dialog";
import type { components } from "../../api/schema";

type Candidate = components["schemas"]["SessionImportCandidate"];
type ImportResult = components["schemas"]["SessionImportResult"];

/** Both project selection and individual search use the daemon's validated preview. */
export function SessionImportDialog({
	projects,
	className,
	tabIndex,
}: {
	projects: { id: string; name: string }[];
	className?: string;
	tabIndex?: number;
}) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const [project, setProject] = useState("all");
	const [search, setSearch] = useState("");
	const [selected, setSelected] = useState(new Set<string>());
	const [results, setResults] = useState<ImportResult[]>([]);
	const [importing, setImporting] = useState(false);
	const [error, setError] = useState<string>();
	const queryClient = useQueryClient();
	const preview = useQuery({
		queryKey: ["session-import-preview"],
		enabled: open,
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
		if (!preview.data) return;
		setSelected(
			new Set(
				preview.data.candidates
					.filter((c) => c.suggested && !c.sessionId)
					.map((c) => c.id),
			),
		);
		setResults([]);
	}, [preview.data]);
	const candidates = preview.data?.candidates ?? [];
	const visible = useMemo(
		() =>
			candidates.filter(
				(c) =>
					(project === "all" ||
						(project === "standalone"
							? !c.projectId
							: c.projectId === project)) &&
					`${c.title} ${c.harness}`
						.toLowerCase()
						.includes(search.toLowerCase()),
			),
		[candidates, project, search],
	);
	const selectedVisible = visible.filter(
		(c) =>
			selected.has(c.id) &&
			!c.sessionId &&
			!results.some((r) => r.id === c.id && r.status !== "failed"),
	);
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
		setError(undefined);
		try {
			const response = await apiClient.POST("/api/v1/session-import", {
				body: { ids: selectedVisible.map((c) => c.id) },
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
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		} catch (err) {
			setError(
				err instanceof Error ? err.message : t("sessionImport.failed"),
			);
		} finally {
			setImporting(false);
		}
	};
	const successful = results.filter((r) => r.status !== "failed").length;
	return (
		<Dialog
			open={open}
			onOpenChange={(next) => {
				if (!importing) setOpen(next);
			}}
		>
			<DialogTrigger asChild>
				<button type="button" className={className} tabIndex={tabIndex}>
					<Download aria-hidden="true" className="size-4" />
					<span>{t("sessionImport.trigger")}</span>
				</button>
			</DialogTrigger>
			<DialogContent className="flex max-h-[85vh] flex-col sm:max-w-3xl">
				<DialogHeader>
					<DialogTitle>{t("sessionImport.title")}</DialogTitle>
					<DialogDescription>
						{t("sessionImport.description")}
					</DialogDescription>
				</DialogHeader>
				<div className="flex flex-wrap gap-3">
					<label className="flex flex-col gap-1 text-xs">
						{t("newTask.project")}
						<select
							aria-label={t("newTask.project")}
							value={project}
							onChange={(e) => setProject(e.target.value)}
							disabled={importing}
							className="h-9 rounded-md border border-input bg-background px-2 text-sm"
						>
							<option value="all">{t("sessionImport.allProjects")}</option>
							<option value="standalone">{t("remote.standalone")}</option>
							{projects.map((p) => (
								<option key={p.id} value={p.id}>
									{p.name}
								</option>
							))}
						</select>
					</label>
					<label className="flex min-w-48 flex-1 flex-col gap-1 text-xs">
						{t("sessionImport.searchLabel")}
						<Input
							value={search}
							onChange={(e) => setSearch(e.target.value)}
							placeholder={t("sessionImport.searchPlaceholder")}
							disabled={importing}
						/>
					</label>
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
				{preview.data?.truncated ? (
					<p role="status" className="text-xs text-warning">
						{t("sessionImport.truncated")}
					</p>
				) : null}
				<div className="min-h-0 overflow-auto rounded-md border border-border">
					<label className="sticky top-0 flex items-center gap-3 border-b border-border bg-surface px-3 py-2 text-xs">
						<input
							type="checkbox"
							aria-label={t("sessionImport.selectAllAria")}
							checked={
								eligible.length > 0 && eligible.every((c) => selected.has(c.id))
							}
							onChange={(e) =>
								toggle(
									eligible.map((c) => c.id),
									e.target.checked,
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
				{results.length ? (
					<p role="status" className="text-sm">
						{t("sessionImport.result", { count: successful })}{" "}
						{t("sessionImport.resultFailed", { count: results.length - successful })}
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
			</DialogContent>
		</Dialog>
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
			<input
				type="checkbox"
				aria-label={t("sessionImport.selectConversation", { title: c.title })}
				className="mt-1"
				checked={selected && !imported}
				disabled={disabled || imported}
				onChange={(e) => onSelect(e.target.checked)}
			/>
			<span className="min-w-0 flex-1">
				<span className="block truncate text-sm font-medium">{c.title}</span>
				<span className="text-xs text-muted-foreground">
					{projectName} · {c.harness} · {t("sessionImport.messages", { count: c.messageCount })} ·{" "}
					{new Date(c.lastActivity).toLocaleDateString(i18n.resolvedLanguage)}
				</span>
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
