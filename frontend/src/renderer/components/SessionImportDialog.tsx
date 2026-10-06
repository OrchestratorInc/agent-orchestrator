import { useEffect, useMemo, useState } from "react";
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
				err instanceof Error ? err.message : "Import failed. Try again.",
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
					<span>Import sessions</span>
				</button>
			</DialogTrigger>
			<DialogContent className="flex max-h-[85vh] flex-col sm:max-w-3xl">
				<DialogHeader>
					<DialogTitle>Bring your conversations into AO</DialogTitle>
					<DialogDescription>
						Import readable Claude Code and Codex histories from this computer.
						Only folders already registered as AO projects and standalone
						conversations appear.
					</DialogDescription>
				</DialogHeader>
				<div className="flex flex-wrap gap-3">
					<label className="flex flex-col gap-1 text-xs">
						Project
						<select
							aria-label="Project"
							value={project}
							onChange={(e) => setProject(e.target.value)}
							disabled={importing}
							className="h-9 rounded-md border border-input bg-background px-2 text-sm"
						>
							<option value="all">All projects and Standalone</option>
							<option value="standalone">Standalone</option>
							{projects.map((p) => (
								<option key={p.id} value={p.id}>
									{p.name}
								</option>
							))}
						</select>
					</label>
					<label className="flex min-w-48 flex-1 flex-col gap-1 text-xs">
						Find a conversation
						<Input
							value={search}
							onChange={(e) => setSearch(e.target.value)}
							placeholder="Search titles or providers"
							disabled={importing}
						/>
					</label>
				</div>
				<p className="text-xs text-muted-foreground">
					Conversations active in the last 30 days are preselected. You can also
					select older conversations. Import leaves every session idle.
				</p>
				{preview.isFetching ? (
					<p role="status" className="flex items-center gap-2 text-sm">
						<Loader2 className="size-4 animate-spin" aria-hidden="true" />
						Reading local histories…
					</p>
				) : null}
				{preview.error || error ? (
					<p role="alert" className="text-sm text-destructive">
						{error ?? preview.error?.message}
					</p>
				) : null}
				{preview.data?.truncated ? (
					<p role="status" className="text-xs text-warning">
						This scan reached its read limit. Some conversations were not
						scanned.
					</p>
				) : null}
				<div className="min-h-0 overflow-auto rounded-md border border-border">
					<label className="sticky top-0 flex items-center gap-3 border-b border-border bg-surface px-3 py-2 text-xs">
						<input
							type="checkbox"
							aria-label="Select all shown conversations"
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
						Select all shown · {visible.length} conversations
					</label>
					{visible.map((c) => (
						<ImportRow
							key={c.id}
							candidate={c}
							projectName={
								c.projectId
									? (names.get(c.projectId) ?? c.projectId)
									: "Standalone"
							}
							selected={selected.has(c.id)}
							disabled={importing}
							result={results.find((r) => r.id === c.id)}
							onSelect={(checked) => toggle([c.id], checked)}
						/>
					))}
					{!preview.isFetching && !visible.length ? (
						<p className="p-6 text-sm text-muted-foreground">
							No readable conversations match this selection.
						</p>
					) : null}
				</div>
				{results.length ? (
					<p role="status" className="text-sm">
						{successful} {successful === 1 ? "conversation" : "conversations"} imported or already in AO.{" "}
						{results.length - successful} could not be imported.
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
						Scan again
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
							? "Importing…"
							: `Import ${selectedVisible.length} ${selectedVisible.length === 1 ? "conversation" : "conversations"}`}
					</Button>
				</div>
				{selectedVisible.length > 500 ? (
					<p role="alert" className="text-xs text-destructive">
						Select up to 500 conversations at a time.
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
	const imported = Boolean(
		c.sessionId || (result && result.status !== "failed"),
	);
	return (
		<label className="flex cursor-pointer items-start gap-3 border-b border-border px-3 py-3 last:border-0">
			<input
				type="checkbox"
				aria-label={`Import ${c.title}`}
				className="mt-1"
				checked={selected && !imported}
				disabled={disabled || imported}
				onChange={(e) => onSelect(e.target.checked)}
			/>
			<span className="min-w-0 flex-1">
				<span className="block truncate text-sm font-medium">{c.title}</span>
				<span className="text-xs text-muted-foreground">
					{projectName} · {c.harness} · {c.messageCount} messages ·{" "}
					{new Date(c.lastActivity).toLocaleDateString()}
				</span>
				{imported ? (
					<span className="block text-xs text-muted-foreground">
						Already in AO
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
