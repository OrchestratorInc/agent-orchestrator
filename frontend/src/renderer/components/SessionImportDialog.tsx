import { useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, Loader2 } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import { useAgentReadinessQuery } from "../hooks/useAgentReadinessQuery";
import { workspaceQueryOptions } from "../hooks/useWorkspaceQuery";
import { agentLabel } from "../lib/agent-options";
import { formatTimeCompact } from "../lib/format-time";
import {
	agentBlock,
	countBy,
	folderName,
	importChats,
	projectChats,
	sessionImportPreviewQuery,
	type AgentBlock,
	type ImportCandidate,
	type ImportResult,
	type ImportSource,
} from "../lib/session-import";
import { cn } from "../lib/utils";
import { useSessionImportStore } from "../stores/session-import-store";
import { useUiStore } from "../stores/ui-store";
import { AgentAvatar } from "./AgentAvatar";
import { Button } from "./ui/button";
import { Checkbox } from "./ui/checkbox";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "./ui/dialog";
import { Pressable } from "./ui/pressable";

type Folder = {
	key: string;
	name: string;
	path: string;
	folder?: string;
	chats: ImportCandidate[];
	agents: string[];
	lastActivity: string;
	suggested: boolean;
};

const SUGGESTED_FOLDER_CHATS = 3;
const MAX_FOLDER_LOGOS = 3;
const MAX_LISTED_FAILURES = 5;

export function SessionImportDialog() {
	const dialog = useSessionImportStore((state) => state.dialog);
	const closeDialog = useSessionImportStore((state) => state.closeDialog);
	const [importing, setImporting] = useState(false);
	if (!dialog) return null;
	return (
		<Dialog open onOpenChange={(open) => !open && !importing && closeDialog()}>
			<DialogContent
				className={settingsDialogContentClass}
				data-testid="session-import-dialog"
				showCloseButton={!importing}
				{...(dialog.kind === "project" ? { "aria-describedby": undefined } : {})}
			>
				{dialog.kind === "project" ? (
					<ProjectChats projectId={dialog.projectId} onClose={closeDialog} onImporting={setImporting} />
				) : (
					<Folders onClose={closeDialog} onImporting={setImporting} />
				)}
			</DialogContent>
		</Dialog>
	);
}

export function useImportRun(source: ImportSource, onDone: (results: ImportResult[]) => void, onImporting?: (importing: boolean) => void) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [importing, setImporting] = useState(false);
	const [error, setError] = useState<string>();
	const [failures, setFailures] = useState<{ id: string; title: string; error?: string }[]>([]);
	const busy = (value: boolean) => {
		setImporting(value);
		onImporting?.(value);
	};
	const run = async (ids: string[], folders?: string[]) => {
		busy(true);
		setError(undefined);
		setFailures([]);
		try {
			const results = await importChats(queryClient, source, ids, folders);
			const failed = results.filter((result) => result.status === "failed");
			const titles = new Map((queryClient.getQueryData(sessionImportPreviewQuery.queryKey)?.candidates ?? []).map((chat) => [chat.id, chat.title]));
			setFailures(failed.map((result) => ({ id: result.id, title: titles.get(result.id) ?? result.id, error: result.error })));
			if (failed.length) setError(t("sessionImport.resultFailed", { count: failed.length }));
			onDone(results);
			if (failed.length) void queryClient.invalidateQueries({ queryKey: sessionImportPreviewQuery.queryKey });
		} catch (err) {
			setError(err instanceof Error ? err.message : t("sessionImport.failed"));
		} finally {
			busy(false);
		}
	};
	return { importing, error, failures, run };
}

function useImportedToast() {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	return (results: ImportResult[]): ImportCandidate[] => {
		const created = new Set(results.flatMap((result) => (result.status === "created" ? [result.id] : [])));
		if (!created.size) return [];
		showGlobalToast(t("sessionImport.foldersImported", { count: created.size }), undefined, "success");
		return (queryClient.getQueryData(sessionImportPreviewQuery.queryKey)?.candidates ?? []).filter((chat) => chat.sessionId && created.has(chat.id));
	};
}

function useAgentBlocks() {
	const readiness = useAgentReadinessQuery();
	return (harness: string) => agentBlock(readiness.data?.agents?.find((agent) => agent.id === harness));
}

function ProjectChats({ projectId, onClose, onImporting }: { projectId: string; onClose: () => void; onImporting: (importing: boolean) => void }) {
	const { t } = useTranslation();
	const workspaces = useQuery(workspaceQueryOptions);
	const name = workspaces.data?.find((workspace) => workspace.id === projectId)?.name ?? projectId;
	const preview = useQuery({ ...sessionImportPreviewQuery, staleTime: 60_000 });
	const blockOf = useAgentBlocks();
	const recordImport = useSessionImportStore((state) => state.recordImport);
	const showImported = useImportedToast();
	const [unchecked, setUnchecked] = useState(new Set<string>());
	const chats = projectChats(preview.data?.candidates, projectId);
	const selected = chats.filter((chat) => !unchecked.has(chat.id));
	const { importing, error, failures, run } = useImportRun("project_dialog", (results) => {
		const created = results.filter((result) => result.status === "created").length;
		if (created) recordImport(projectId, created);
		showImported(results);
		if (!results.some((result) => result.status === "failed")) onClose();
	}, onImporting);
	return (
		<>
			<div className={settingsDialogHeaderClass}>
				<DialogTitle>{t("sessionImport.projectDialogTitle", { project: name })}</DialogTitle>
			</div>
			<div className={settingsDialogBodyClass}>
				<PreviewState pending={preview.isPending} error={preview.error} empty={!chats.length} emptyText={t("sessionImport.projectEmpty")}>
					<div className="shrink-0 overflow-hidden rounded-md border border-border">
						{chats.map((chat) => (
							<ChatRow
								key={chat.id}
								chat={chat}
								block={blockOf(chat.harness)}
								checked={!unchecked.has(chat.id)}
								disabled={importing}
								onCheckedChange={(checked) =>
									setUnchecked((current) => {
										const next = new Set(current);
										if (checked) next.delete(chat.id);
										else next.add(chat.id);
										return next;
									})
								}
							/>
						))}
					</div>
				</PreviewState>
			</div>
			<ImportError error={error} failures={failures} />
			<div className={settingsDialogFooterClass}>
				<Button variant="secondary" disabled={importing} onClick={onClose}>
					{t("sessionImport.cancel")}
				</Button>
				<Button variant="primary" disabled={importing || !selected.length} onClick={() => void run(selected.map((chat) => chat.id))}>
					{importing ? t("sessionImport.importing") : t("sessionImport.importChats", { count: selected.length })}
				</Button>
			</div>
		</>
	);
}

function Folders({ onClose, onImporting }: { onClose: () => void; onImporting: (importing: boolean) => void }) {
	const { t } = useTranslation();
	const workspaces = useQuery(workspaceQueryOptions);
	const preview = useQuery({ ...sessionImportPreviewQuery, staleTime: 60_000 });
	const blockOf = useAgentBlocks();
	const closeSessionImportOffer = useUiStore((state) => state.closeSessionImportOffer);
	const recordImport = useSessionImportStore((state) => state.recordImport);
	const showImported = useImportedToast();
	const [picked, setPicked] = useState<Set<string>>();
	const [showOther, setShowOther] = useState(false);
	const byKey = new Map<string, Folder>();
	for (const chat of preview.data?.candidates ?? []) {
		const key = chat.projectId ?? chat.folder;
		if (!key || chat.sessionId) continue;
		const project = chat.projectId ? workspaces.data?.find((workspace) => workspace.id === chat.projectId) : undefined;
		const folder = byKey.get(key) ?? {
			key,
			name: project?.name ?? folderName(chat.folder ?? key),
			path: project?.path ?? chat.folder ?? key,
			folder: chat.projectId ? undefined : chat.folder,
			chats: [],
			agents: [],
			lastActivity: chat.lastActivity,
			suggested: false,
		};
		folder.chats.push(chat);
		if (!folder.agents.includes(chat.harness)) folder.agents.push(chat.harness);
		if (chat.lastActivity > folder.lastActivity) folder.lastActivity = chat.lastActivity;
		byKey.set(key, folder);
	}
	const folders = [...byKey.values()].sort((a, b) => b.lastActivity.localeCompare(a.lastActivity));
	for (const folder of folders) {
		folder.suggested = folder.chats.filter((chat) => !blockOf(chat.harness)).length >= SUGGESTED_FOLDER_CHATS;
	}
	const suggested = folders.filter((folder) => folder.suggested);
	const other = folders.filter((folder) => !folder.suggested);
	const selected = picked ?? new Set(suggested.flatMap((folder) => folder.chats.map((chat) => chat.id)));
	const isPicked = (folder: Folder) => folder.chats.some((chat) => selected.has(chat.id));
	const chosen = folders.filter(isPicked);
	const toggle = (folder: Folder, checked: boolean) =>
		setPicked((current) => {
			const next = new Set(current ?? selected);
			for (const chat of folder.chats) {
				if (checked) next.add(chat.id);
				else next.delete(chat.id);
			}
			return next;
		});
	const { importing, error, failures, run } = useImportRun("home_folders", (results) => {
		const imported = showImported(results);
		if (results.some((result) => result.status === "created")) closeSessionImportOffer();
		for (const [projectId, count] of countBy(imported.flatMap((chat) => (chat.projectId ? [chat.projectId] : [])))) recordImport(projectId, count);
		if (!results.some((result) => result.status === "failed")) onClose();
	}, onImporting);
	const otherOpen = showOther || !suggested.length;
	const row = (folder: Folder) => (
		<FolderRow key={folder.key} folder={folder} checked={isPicked(folder)} disabled={importing} onCheckedChange={(checked) => toggle(folder, checked)} />
	);
	return (
		<>
			<div className={settingsDialogHeaderClass}>
				<DialogTitle>{t("sessionImport.foldersTitle")}</DialogTitle>
				<DialogDescription>{t("sessionImport.foldersDescription")}</DialogDescription>
			</div>
			<div className={settingsDialogBodyClass}>
				<PreviewState pending={preview.isPending} error={preview.error} empty={!folders.length} emptyText={t("sessionImport.foldersEmpty")}>
					<div className="flex flex-col gap-3">
						{suggested.length ? <div className="overflow-hidden rounded-md border border-border">{suggested.map(row)}</div> : null}
						{other.length ? (
							<div className="flex flex-col gap-2">
								{suggested.length ? (
									<Pressable
										aria-expanded={otherOpen}
										className="flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
										onClick={() => setShowOther(!showOther)}
									>
										<ChevronRight aria-hidden="true" className={cn("size-3.5 transition-transform", otherOpen && "rotate-90")} />
										{t("sessionImport.otherFolders", { count: other.length })}
									</Pressable>
								) : null}
								{otherOpen ? <div className="overflow-hidden rounded-md border border-border">{other.map(row)}</div> : null}
							</div>
						) : null}
					</div>
				</PreviewState>
			</div>
			<ImportError error={error} failures={failures} />
			<div className={settingsDialogFooterClass}>
				<Button variant="secondary" disabled={importing} onClick={onClose}>
					{t("sessionImport.cancel")}
				</Button>
				<Button
					variant="primary"
					disabled={importing || !chosen.length}
					onClick={() => {
						setPicked(selected);
						void run(
							chosen.flatMap((folder) => folder.chats.map((chat) => chat.id)),
							chosen.flatMap((folder) => (folder.folder ? [folder.folder] : [])),
						);
					}}
				>
					{importing ? t("sessionImport.importing") : t("sessionImport.importFolders", { count: chosen.length })}
				</Button>
			</div>
		</>
	);
}

function ImportError({ error, failures }: { error?: string; failures: { id: string; title: string; error?: string }[] }) {
	const { t } = useTranslation();
	if (!error) return null;
	return (
		<div role="alert" className="max-h-[30svh] shrink-0 space-y-1 overflow-y-auto px-(--size-modal-padding) pb-(--size-modal-padding) text-sm text-destructive settings-thin-scrollbar">
			<p>{error}</p>
			{failures.slice(0, MAX_LISTED_FAILURES).map((failure) => (
				<p key={failure.id} className="truncate text-xs">
					{failure.title}
					{failure.error ? `: ${failure.error}` : null}
				</p>
			))}
			{failures.length > MAX_LISTED_FAILURES ? <p className="text-xs">{t("sessionImport.offerMore", { count: failures.length - MAX_LISTED_FAILURES })}</p> : null}
		</div>
	);
}

function PreviewState({ pending, error, empty, emptyText, children }: { pending: boolean; error: Error | null; empty: boolean; emptyText: string; children: ReactNode }) {
	const { t } = useTranslation();
	if (pending) {
		return (
			<p role="status" className="flex items-center gap-2 text-sm text-muted-foreground">
				<Loader2 className="size-4 animate-spin" aria-hidden="true" />
				{t("sessionImport.reading")}
			</p>
		);
	}
	if (error) {
		return (
			<p role="alert" className="text-sm text-destructive">
				{error.message}
			</p>
		);
	}
	if (empty) return <p className="text-sm text-muted-foreground">{emptyText}</p>;
	return children;
}

function When({ at }: { at: string }) {
	const { i18n } = useTranslation();
	return (
		<time dateTime={at} title={new Date(at).toLocaleString(i18n.resolvedLanguage)}>
			{formatTimeCompact(at, { longUnits: true })}
		</time>
	);
}

function ChatRow({
	chat,
	block,
	checked,
	disabled,
	onCheckedChange,
}: {
	chat: ImportCandidate;
	block?: AgentBlock;
	checked: boolean;
	disabled: boolean;
	onCheckedChange: (checked: boolean) => void;
}) {
	const { t } = useTranslation();
	return (
		<label className="flex cursor-pointer items-center gap-3 border-b border-border px-3 py-2.5 last:border-0">
			<Checkbox
				aria-label={t("sessionImport.selectConversation", { title: chat.title })}
				checked={checked}
				disabled={disabled}
				onCheckedChange={(next) => onCheckedChange(next === true)}
			/>
			<AgentAvatar className="size-4 shrink-0" decorative provider={chat.harness} />
			<span className="min-w-0 flex-1">
				<span className="block truncate text-sm">{chat.title}</span>
				<span className="block truncate text-xs text-muted-foreground">
					{agentLabel(chat.harness)} · {t("sessionImport.messages", { count: chat.messageCount })} · <When at={chat.lastActivity} />
					{block ? <span className="text-warning"> · {t(block === "signed_out" ? "sessionImport.agentSignedOut" : "sessionImport.agentMissing")}</span> : null}
				</span>
				{chat.shortened ? <span className="block text-xs text-warning">{t("sessionImport.shortened")}</span> : null}
			</span>
		</label>
	);
}

function FolderRow({
	folder,
	checked,
	disabled,
	onCheckedChange,
}: {
	folder: Folder;
	checked: boolean;
	disabled: boolean;
	onCheckedChange: (checked: boolean) => void;
}) {
	const { t } = useTranslation();
	return (
		<label className="flex cursor-pointer items-center gap-3 border-b border-border px-3 py-2.5 last:border-0">
			<Checkbox
				aria-label={t("sessionImport.selectFolder", { name: folder.name })}
				checked={checked}
				disabled={disabled}
				onCheckedChange={(next) => onCheckedChange(next === true)}
			/>
			<span className="min-w-0 flex-1">
				<span className="block truncate text-sm font-medium">{folder.name}</span>
				<span className="block truncate text-xs text-muted-foreground" title={folder.path}>
					{folder.path.replace(/^\/(?:Users|home)\/[^/]+/, "~")}
				</span>
			</span>
			<span className="flex shrink-0 items-center gap-2 text-xs text-muted-foreground">
				<span className="flex items-center gap-0.5" role="img" aria-label={folder.agents.map(agentLabel).join(", ")}>
					{folder.agents.slice(0, MAX_FOLDER_LOGOS).map((agent) => (
						<AgentAvatar key={agent} className="size-3.5" decorative provider={agent} />
					))}
					{folder.agents.length > MAX_FOLDER_LOGOS ? (
						<span className="pl-0.5 tabular-nums">{t("sessionImport.offerMore", { count: folder.agents.length - MAX_FOLDER_LOGOS })}</span>
					) : null}
				</span>
				<span className="tabular-nums">{t("sessionImport.chats", { count: folder.chats.length })}</span>
				<When at={folder.lastActivity} />
			</span>
		</label>
	);
}
