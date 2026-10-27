import { useState } from "react";
import { useTranslation } from "react-i18next";
import { History, LoaderCircle, User } from "lucide-react";
import { useArtifactVersion, useArtifactVersions, useSyncArtifactVersions, type ArtifactVersionSummary } from "../hooks/useArtifactVersions";
import { GithubAvatar } from "@aoagents/product-ui";
import { useQuery } from "@tanstack/react-query";
import { AgentAvatar } from "./AgentAvatar";
import { getGitHubStatus } from "../lib/github-daemon";
import { cn } from "../lib/utils";
import { MarkdownFileView } from "./markdown/MarkdownFileView";
import { Button } from "./ui/button";
import { DropdownMenuItem, DropdownMenuSub, DropdownMenuSubContent, DropdownMenuSubTrigger } from "./ui/dropdown-menu";
import { PanelMessage } from "./WorkspaceDiffView";

// Version rows shown before "Show more".
const VERSIONS_SHOWN = 8;

/** "Today at 8:31 PM", "Yesterday at …", else "Oct 11 at …". */
export function formatVersionTime(isoDate: string, now: Date = new Date()): string {
	const date = new Date(isoDate);
	if (Number.isNaN(date.getTime())) return "";
	const time = date.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
	const dayStart = (value: Date) => new Date(value.getFullYear(), value.getMonth(), value.getDate()).getTime();
	const days = Math.round((dayStart(now) - dayStart(date)) / 86_400_000);
	const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
	const day =
		days <= 1 && days >= 0
			? rtf.format(-days, "day")
			: date.toLocaleDateString(undefined, { day: "numeric", month: "short", ...(date.getFullYear() === now.getFullYear() ? {} : { year: "numeric" }) });
	return `${day.charAt(0).toLocaleUpperCase()}${day.slice(1)}, ${time}`;
}

function VersionItem({
	current,
	fallbackHarness,
	login,
	onSelect,
	restoredNote,
	selected,
	version,
}: {
	current: boolean;
	/** The session's agent, for a version recorded before its own was kept. */
	fallbackHarness: string;
	/** The user's GitHub username, once known; a version they saved shows their avatar. */
	login?: string;
	onSelect: () => void;
	/** "Restored from …", for a version that came from a restore. */
	restoredNote?: string;
	selected: boolean;
	version: ArtifactVersionSummary;
}) {
	const { t } = useTranslation();
	const harness = version.harness || fallbackHarness;
	return (
		<DropdownMenuItem aria-current={selected ? "true" : undefined} className={cn(selected && "bg-interactive-hover text-foreground")} onSelect={onSelect}>
			<span className="flex min-w-0 flex-col">
				<span className="flex items-baseline gap-2">
					<span className="min-w-0 truncate tabular-nums">{formatVersionTime(version.createdAt)}</span>
					{current ? <span className="shrink-0 text-caption text-passive">{t("artifacts.history.current")}</span> : null}
				</span>
				{restoredNote ? <span className="truncate text-caption text-passive">{restoredNote}</span> : null}
			</span>
			{/* Who wrote it: the agent's logo, or the user's avatar for a save made in the app. One fixed box keeps both the same size and centre. */}
			<span className="ml-auto flex size-icon-lg shrink-0 items-center justify-center" title={version.source === "user" ? login || t("artifacts.history.you") : undefined}>
				{version.source === "user" ? (
					login ? (
						<GithubAvatar className="size-full" login={login} />
					) : (
						<span aria-hidden="true" className="flex size-full items-center justify-center rounded-full bg-muted text-muted-foreground">
							<User className="size-3" />
						</span>
					)
				) : (
					<AgentAvatar className="size-full" provider={harness} />
				)}
			</span>
		</DropdownMenuItem>
	);
}

/**
 * The History submenu in the viewer's title menu: every kept version in one list,
 * newest first, the first marked current. Choosing one previews it in the
 * viewer; the current version returns to the live file. The list and the
 * user's avatar load when the title menu opens, so the submenu is ready to read, and
 * opening it records any edit the observer has not picked up yet.
 */
export function ArtifactHistoryMenu({
	fallbackHarness,
	hostId,
	onSelect,
	path,
	selectedVersion,
	sessionId,
}: {
	/** The session's agent, shown for versions recorded before their own agent was kept. */
	fallbackHarness: string;
	hostId?: string;
	onSelect: (version: number | null) => void;
	path: string;
	/** The previewed version; null is the live file. */
	selectedVersion: number | null;
	sessionId: string;
}) {
	const { t } = useTranslation();
	const [showAll, setShowAll] = useState(false);
	const versions = useArtifactVersions({ enabled: true, hostId, path, sessionId });
	const syncVersions = useSyncArtifactVersions({ hostId, path, sessionId });
	// Whose avatar marks a version saved in the app: the GitHub account the daemon is signed in as.
	const { data: github } = useQuery({ queryKey: ["github-status"], queryFn: getGitHubStatus, staleTime: 5 * 60_000 });
	const login = github?.login;
	const list = versions.data ?? [];
	const shown = showAll ? list : list.slice(0, VERSIONS_SHOWN);
	const currentVersion = list[0]?.version;
	// A restore says which version it came from; the source may have been pruned since.
	const restoredNoteOf = (version: ArtifactVersionSummary) => {
		if (!version.restoredFrom) return undefined;
		const source = list.find((candidate) => candidate.version === version.restoredFrom);
		return source ? t("artifacts.history.restoredFrom", { time: formatVersionTime(source.createdAt) }) : t("artifacts.history.restoredFromOlder");
	};
	return (
		<DropdownMenuSub onOpenChange={(open) => {
			if (open) syncVersions.mutate();
		}}>
			<DropdownMenuSubTrigger>
				<History aria-hidden="true" />
				{t("artifacts.history")}
			</DropdownMenuSubTrigger>
			<DropdownMenuSubContent className="max-h-[min(28rem,60vh)] w-72 overflow-y-auto" data-browser-native-overlay="true">
				{versions.isPending ? (
					<div className="flex justify-center py-2 text-muted-foreground">
						<LoaderCircle aria-hidden="true" className="size-icon-sm animate-spin" />
					</div>
				) : versions.isError && list.length === 0 ? (
					<div className="px-2 py-1.5 text-control text-muted-foreground">{t("artifacts.history.error")}</div>
				) : versions.isSuccess && list.length === 0 ? (
					<div className="px-2 py-1.5 text-control text-muted-foreground">{t("artifacts.history.empty")}</div>
				) : (
					shown.map((version) => {
						const isCurrent = version.version === currentVersion;
						return (
							<VersionItem
								current={isCurrent}
								fallbackHarness={fallbackHarness}
								key={version.version}
								login={login}
								onSelect={() => onSelect(isCurrent ? null : version.version)}
								restoredNote={restoredNoteOf(version)}
								selected={isCurrent ? selectedVersion === null : selectedVersion === version.version}
								version={version}
							/>
						);
					})
				)}
				{!showAll && list.length > VERSIONS_SHOWN ? (
					<DropdownMenuItem
						className="text-caption text-passive"
						// Showing the rest keeps the menu open.
						onSelect={(event) => {
							event.preventDefault();
							setShowAll(true);
						}}
					>
						{t("artifacts.history.showMore")}
					</DropdownMenuItem>
				) : null}
			</DropdownMenuSubContent>
		</DropdownMenuSub>
	);
}

/**
 * A saved version shown read-only in place of the live file, with the actions
 * that leave it: restore it, or go back to the current file. Markdown renders;
 * anything else, HTML included for now, shows as source. A long version shows
 * only its start, and a restore still writes all of it.
 */
export function ArtifactVersionPreview({
	artifactOrigin,
	hostId,
	onBack,
	onRestore,
	path,
	restoreError,
	restoring,
	sessionId,
	version,
}: {
	artifactOrigin?: string;
	hostId?: string;
	onBack: () => void;
	onRestore: () => void;
	path: string;
	restoreError?: string;
	restoring: boolean;
	sessionId: string;
	version: number;
}) {
	const { t } = useTranslation();
	const query = useArtifactVersion({ hostId, path, sessionId, version });
	const markdown = /\.(md|markdown)$/i.test(path);
	return (
		<div className="flex h-full min-h-0 flex-col bg-background">
			<div className="flex min-h-9 shrink-0 items-center gap-2 border-b border-border px-3 text-sm">
				<span className="min-w-0 flex-1 truncate text-muted-foreground">
					{query.data ? t("artifacts.history.viewing", { time: formatVersionTime(query.data.createdAt) }) : ""}
				</span>
				{restoreError ? <span className="truncate text-caption text-destructive">{restoreError}</span> : null}
				<Button onClick={onBack} size="sm" type="button" variant="ghost">
					{t("artifacts.history.backToCurrent")}
				</Button>
				<Button disabled={!query.data || restoring} onClick={onRestore} size="sm" type="button" variant="primary">
					{restoring ? <LoaderCircle aria-hidden="true" className="size-icon-sm animate-spin" /> : null}
					{t(restoring ? "artifacts.history.restoring" : "artifacts.history.restore")}
				</Button>
			</div>
			<div className="board-scrollbar min-h-0 flex-1 select-text overflow-x-hidden overflow-y-auto overscroll-contain">
				{query.isPending ? (
					<PanelMessage>
						<LoaderCircle aria-hidden="true" className="size-icon-sm animate-spin" />
					</PanelMessage>
				) : query.isError ? (
					<PanelMessage>{query.error.message}</PanelMessage>
				) : (
					<>
						{query.data.truncated ? (
							<div className="border-b border-border bg-warning/10 px-3 py-1.5 text-xs text-warning">{t("artifacts.history.truncated")}</div>
						) : null}
						{markdown ? (
							<MarkdownFileView artifactOrigin={artifactOrigin} content={query.data.content} filePath={path} hostId={hostId} sessionId={sessionId} truncated={false} version={query.dataUpdatedAt} />
						) : (
							<pre className="whitespace-pre-wrap break-words p-4 font-mono text-sm text-foreground">{query.data.content}</pre>
						)}
					</>
				)}
			</div>
		</div>
	);
}
