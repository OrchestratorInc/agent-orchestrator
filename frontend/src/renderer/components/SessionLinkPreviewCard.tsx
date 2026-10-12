import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import Markdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { FileText, Globe, LoaderCircle, Play, Sheet } from "lucide-react";
import { useRestoreSession } from "../hooks/useRestoreSession";
import { sessionArtifactFileQueryOptions } from "../hooks/useSessionWorkspaceFiles";
import { csvPreviewRows, tabularDelimiter } from "../lib/csv-preview";
import { clampPreviewText, parseMarkdownFrontmatter } from "../lib/markdown-frontmatter";
import { cn } from "../lib/utils";
import { parseArtifactLink, parseSessionLink } from "../lib/session-links";
import { getSessionStatusDotView, getSessionStatusView } from "../lib/session-presentation";
import { prCanMerge, prCardPresentation, sessionPRDisplaySummaries, type PRDisplayTone } from "../lib/pr-display";
import { useSessionLinkSource } from "../lib/use-session-link-navigation";
import { isInlineArtifactUrl } from "../../shared/agent-page-url";
import { LOCAL_HOST } from "../lib/hosts";
import type { SessionArtifact, WorkspaceSession } from "../types/workspace";
import { AgentAvatar } from "./AgentAvatar";
import { Button } from "./ui/button";
import { HoverCardContent } from "./ui/hover-card";
import { Skeleton } from "./ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui/table";

const prToneClasses = {
	neutral: { dot: "bg-status-idle", text: "text-muted-foreground" },
	passive: { dot: "bg-status-idle", text: "text-muted-foreground" },
	success: { dot: "bg-status-ready", text: "text-status-ready" },
	review: { dot: "bg-status-in-review", text: "text-status-in-review" },
	warning: { dot: "bg-status-needs-you", text: "text-status-needs-you" },
	error: { dot: "bg-destructive", text: "text-destructive" },
} as const;

function relativeTime(timestamp: string | undefined, format: (key: "time.justNow" | "shell.updatedAt", values?: Record<string, unknown>) => string): string {
	if (!timestamp) return "";
	const elapsedMinutes = Math.floor((Date.now() - Date.parse(timestamp)) / 60_000);
	if (!Number.isFinite(elapsedMinutes) || elapsedMinutes < 1) return format("shell.updatedAt", { time: format("time.justNow") });
	const unit: Intl.RelativeTimeFormatUnit = elapsedMinutes < 60 ? "minute" : elapsedMinutes < 1_440 ? "hour" : "day";
	const amount = unit === "minute" ? elapsedMinutes : unit === "hour" ? Math.floor(elapsedMinutes / 60) : Math.floor(elapsedMinutes / 1_440);
	return format("shell.updatedAt", { time: new Intl.RelativeTimeFormat(undefined, { numeric: "always" }).format(-amount, unit) });
}

function LoadingCard() {
	const { t } = useTranslation();
	return (
		<HoverCardContent collisionPadding={8} sideOffset={6} className="max-h-48 overflow-y-auto p-3">
			<div role="status" aria-label={t("session.statusChecking")} className="space-y-3">
				<div className="flex items-center gap-2.5">
					<Skeleton className="size-7 shrink-0 rounded-md" />
					<div className="min-w-0 flex-1 space-y-1.5"><Skeleton className="h-3 w-32" /><Skeleton className="h-2.5 w-24" /></div>
				</div>
				<Skeleton className="h-3 w-44" />
				<Skeleton className="h-2.5 w-36" />
			</div>
		</HoverCardContent>
	);
}

function UnavailableCard() {
	const { t } = useTranslation();
	return (
		<HoverCardContent collisionPadding={8} sideOffset={6} className="max-h-48 overflow-y-auto p-3">
			<p role="status" className="text-sm font-medium text-foreground">{t("session.statusUnavailable")}</p>
		</HoverCardContent>
	);
}

function ErrorCard() {
	const { t } = useTranslation();
	return (
		<HoverCardContent collisionPadding={8} sideOffset={6} className="max-h-48 overflow-y-auto p-3">
			<div role="alert" className="space-y-1">
				<p className="text-sm font-medium text-foreground">{t("session.statusUnavailable")}</p>
				<p className="text-xs leading-4 text-muted-foreground">{t("shell.couldNotLoadSessions")}</p>
			</div>
		</HoverCardContent>
	);
}

function statusDot(session: WorkspaceSession) {
	const dot = getSessionStatusDotView(session);
	return cn("size-1.5 shrink-0 rounded-full", dot.className, dot.breathe && "animate-status-pulse");
}

function compactPRStatus(
	pr: ReturnType<typeof sessionPRDisplaySummaries>[number],
	t: TFunction,
): { label: string; tone: PRDisplayTone } {
	const primary = prCardPresentation(pr).primary;
	if (pr.state === "merged" || pr.state === "closed" || pr.state === "draft") return primary;
	if (prCanMerge(pr)) return { label: t("pr.card.readyToMerge"), tone: "success" };
	const blocked = pr.ci.state === "failing"
		|| pr.mergeability.state === "conflicting"
		|| pr.mergeability.state === "blocked"
		|| pr.mergeability.state === "unstable"
		|| pr.review.decision === "changes_requested"
		|| pr.review.decision === "review_required"
		|| pr.review.hasUnresolvedHumanComments;
	return blocked
		? { label: t("pr.merge.blocked"), tone: primary.tone }
		: { label: t("pr.card.open"), tone: "neutral" };
}

function SessionCardBody({ session }: { session: WorkspaceSession }) {
	const { t } = useTranslation();
	const prs = useMemo(() => sessionPRDisplaySummaries(session), [session]);
	const status = session.statusReadiness === "unavailable"
		? t("session.statusUnavailable")
		: session.displayStatus || getSessionStatusView(session.status, t).label;
	return (
		<div className="space-y-3" role="status" aria-label={`${session.title}, ${status}`}>
			<div className="flex min-w-0 items-start gap-2.5">
				<AgentAvatar provider={session.provider} className="size-7 shrink-0" />
				<div className="min-w-0 flex-1">
					<div className="flex min-w-0 items-baseline gap-1.5">
						<p className="min-w-0 truncate text-sm font-semibold text-foreground">{session.title}</p>
						<span className="shrink-0 font-mono text-xs text-muted-foreground">#{session.id.slice(-3)}</span>
					</div>
					<p className="mt-0.5 truncate text-xs text-muted-foreground">{session.workspaceName}</p>
				</div>
			</div>
			<div className="flex items-center gap-1.5 text-xs">
				<span aria-hidden="true" className={statusDot(session)} />
				<span className="min-w-0 truncate font-medium text-popover-foreground">{status}</span>
				<span className="ml-auto shrink-0 text-xs text-muted-foreground">{relativeTime(session.updatedAt, t)}</span>
			</div>
			{prs.length > 0 && (
				<div className="border-t border-border pt-2">
					<p className="text-xs font-semibold text-muted-foreground">{t("pr.count", { count: prs.length })}</p>
					<div className="mt-1.5 space-y-1.5">
						{prs.map((pr) => {
							const status = compactPRStatus(pr, t);
							const tone = prToneClasses[status.tone];
							return (
								<div key={`${pr.url}-${pr.number}`} className="flex min-h-5 items-center gap-1.5 text-xs">
									<span className="font-mono font-semibold text-popover-foreground">{t("pr.short")} #{pr.number}</span>
									<span aria-hidden="true" className={cn("size-1.5 shrink-0 rounded-full", tone.dot)} />
									<span className={cn("truncate", tone.text)}>{status.label}</span>
								</div>
							);
						})}
					</div>
				</div>
			)}
		</div>
	);
}

function TerminatedCard({ session, hostId }: { session: WorkspaceSession; hostId?: string }) {
	const { t } = useTranslation();
	const restoreSession = useRestoreSession();
	const [restoring, setRestoring] = useState(false);
	const [error, setError] = useState<string>();
	const resumeAgent = async () => {
		if (restoring) return;
		setRestoring(true);
		setError(undefined);
		try {
			const result = await restoreSession(session.id, hostId);
			if (result.status !== "success") setError(result.message);
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : t("terminal.unableRestore"));
		} finally {
			setRestoring(false);
		}
	};
	return (
		<div className="space-y-3" role="status" aria-label={t("session.agentTerminated")}>
			<p className="text-sm font-semibold text-popover-foreground">{t("session.agentTerminated")}</p>
			<Button
				className="w-full"
				disabled={restoring}
				onClick={(event) => {
					event.preventDefault();
					event.stopPropagation();
					void resumeAgent();
				}}
				size="sm"
				type="button"
			>
				{restoring
					? <LoaderCircle aria-hidden="true" className="size-icon-sm animate-spin" />
					: <Play aria-hidden="true" className="size-icon-sm" />}
				{t(restoring ? "inspector.resumingAgent" : "inspector.resumeAgent")}
			</Button>
			{error ? <p className="text-xs leading-4 text-destructive" role="alert">{error}</p> : null}
		</div>
	);
}

export function SessionLinkPreviewCard({
	href,
	sourceHostId,
	sourceKind,
}: {
	href: string;
	sourceHostId?: string;
	sourceKind?: "cloud";
}) {
	const target = parseSessionLink(href);
	const source = useSessionLinkSource(sourceHostId, sourceKind);
	const workspace = target ? source.workspaces.find((candidate) => candidate.id === target.projectId) : undefined;
	const session = workspace?.sessions.find((candidate) => candidate.id === target?.sessionId);
	const restoreHostId = sourceHostId && sourceHostId !== LOCAL_HOST ? sourceHostId : undefined;
	if (session) return (
		<HoverCardContent collisionPadding={8} sideOffset={6} className="max-h-48 overflow-y-auto p-3">
			{session.isTerminated === true || session.status === "terminated"
				? <TerminatedCard session={session} hostId={restoreHostId} />
				: <SessionCardBody session={session} />}
		</HoverCardContent>
	);
	if (source.isLoading) return <LoadingCard />;
	if (source.isError) return <ErrorCard />;
	return <UnavailableCard />;
}

const ARTIFACT_EXCERPT_CHARS = 480;
const MARKDOWN_PREVIEW_CHARS = 1500;
const TABLE_PREVIEW_ROWS = 6;
const TABLE_PREVIEW_COLUMNS = 4;
// The page renders at PAGE_PREVIEW_WIDTH and is scaled to the card's 296px content width.
const PAGE_PREVIEW_WIDTH = 1000;
const PAGE_PREVIEW_HEIGHT = 625;
const PAGE_PREVIEW_SCALE = 296 / PAGE_PREVIEW_WIDTH;
const MARKDOWN_FRONTMATTER = /^(?:\uFEFF)?---[ \t]*\r?\n[\s\S]*?\r?\n---[ \t]*(?:\r?\n|$)/;

/** The leading readable text of an artifact, for a hover preview. */
export function artifactExcerpt(kind: SessionArtifact["kind"], content: string): string {
	let text = content;
	if (kind === "html") {
		const document = new DOMParser().parseFromString(content, "text/html");
		for (const node of document.querySelectorAll("script, style, noscript, template")) node.remove();
		const title = document.title.trim();
		const body = (document.body?.textContent ?? "").replace(/\s+/g, " ").trim();
		text = title && !body.startsWith(title) ? `${title}\n${body}` : body;
	} else if (kind === "markdown") {
		text = content.replace(MARKDOWN_FRONTMATTER, "");
	}
	text = text.trim();
	return text.length > ARTIFACT_EXCERPT_CHARS ? `${text.slice(0, ARTIFACT_EXCERPT_CHARS).trimEnd()}…` : text;
}

/** A Markdown artifact's body without frontmatter, cut at a line break near the preview limit. */
export function markdownPreviewSource(content: string): string {
	const body = content.replace(MARKDOWN_FRONTMATTER, "").trim();
	if (body.length <= MARKDOWN_PREVIEW_CHARS) return body;
	const cut = body.lastIndexOf("\n", MARKDOWN_PREVIEW_CHARS);
	return body.slice(0, cut > 0 ? cut : MARKDOWN_PREVIEW_CHARS).trimEnd();
}

const MARKDOWN_PREVIEW_ELEMENTS = ["h1", "h2", "h3", "h4", "h5", "h6", "p", "ul", "ol", "li", "blockquote", "pre", "code", "strong", "em", "del", "a", "img", "br", "hr"];
const MARKDOWN_PREVIEW_HEADING = "mt-1.5 text-xs font-semibold text-foreground first:mt-0";
const MARKDOWN_PREVIEW_COMPONENTS: Components = {
	h1: ({ children }) => <p className={cn(MARKDOWN_PREVIEW_HEADING, "text-sm")}>{children}</p>,
	h2: ({ children }) => <p className={MARKDOWN_PREVIEW_HEADING}>{children}</p>,
	h3: ({ children }) => <p className={MARKDOWN_PREVIEW_HEADING}>{children}</p>,
	h4: ({ children }) => <p className={MARKDOWN_PREVIEW_HEADING}>{children}</p>,
	h5: ({ children }) => <p className={MARKDOWN_PREVIEW_HEADING}>{children}</p>,
	h6: ({ children }) => <p className={MARKDOWN_PREVIEW_HEADING}>{children}</p>,
	p: ({ children }) => <p className="mt-1 break-words first:mt-0">{children}</p>,
	ul: ({ children }) => <ul className="mt-1 list-disc space-y-0.5 pl-4 first:mt-0">{children}</ul>,
	ol: ({ children }) => <ol className="mt-1 list-decimal space-y-0.5 pl-4 first:mt-0">{children}</ol>,
	blockquote: ({ children }) => <blockquote className="mt-1 border-l-2 border-border pl-2 text-muted-foreground first:mt-0">{children}</blockquote>,
	pre: ({ children }) => <pre className="mt-1 overflow-hidden rounded-sm bg-surface px-1.5 py-1 font-mono text-[11px] first:mt-0">{children}</pre>,
	code: ({ children }) => <code className="font-mono text-[0.95em] text-markdown-code">{children}</code>,
	a: ({ children }) => <span className="text-markdown-link">{children}</span>,
	img: ({ alt }) => <span className="text-muted-foreground">{alt}</span>,
	hr: () => <hr className="my-1.5 border-border" />,
};

/** A Markdown artifact's body, formatted compactly and with links and images inert. */
function ArtifactMarkdownPreview({ source, clamp }: { source: string; clamp: "short" | "tall" }) {
	return (
		<div className={cn("overflow-hidden text-xs leading-4 text-popover-foreground [mask-image:linear-gradient(to_bottom,black_70%,transparent)]", clamp === "short" ? "max-h-20" : "max-h-40")}>
			<Markdown allowedElements={MARKDOWN_PREVIEW_ELEMENTS} unwrapDisallowed remarkPlugins={[remarkGfm]} components={MARKDOWN_PREVIEW_COMPONENTS}>
				{source}
			</Markdown>
		</div>
	);
}

/** A scaled, non-interactive render of an HTML artifact on its own inline origin. */
function ArtifactPagePreview({ url, title }: { url: string; title: string }) {
	return (
		<div
			className="relative overflow-hidden rounded-sm border border-border bg-background"
			style={{ height: PAGE_PREVIEW_HEIGHT * PAGE_PREVIEW_SCALE }}
		>
			<iframe
				src={url}
				title={title}
				sandbox="allow-scripts allow-same-origin"
				loading="lazy"
				tabIndex={-1}
				aria-hidden="true"
				className="pointer-events-none absolute left-0 top-0 origin-top-left border-0"
				style={{ width: PAGE_PREVIEW_WIDTH, height: PAGE_PREVIEW_HEIGHT, transform: `scale(${PAGE_PREVIEW_SCALE})` }}
			/>
		</div>
	);
}

function ArtifactTablePreview({ rows }: { rows: string[][] }) {
	const [header = [], ...body] = rows;
	return (
		<Table className="table-fixed text-xs">
			<TableHeader>
				<TableRow className="hover:bg-transparent">
					{header.map((cell, column) => <TableHead key={column} className="h-6 truncate px-1.5">{cell}</TableHead>)}
				</TableRow>
			</TableHeader>
			<TableBody>
				{body.map((row, index) => (
					<TableRow key={index} className="hover:bg-transparent">
						{header.map((_, column) => <TableCell key={column} className="truncate px-1.5 py-1 text-popover-foreground">{row[column] ?? ""}</TableCell>)}
					</TableRow>
				))}
			</TableBody>
		</Table>
	);
}

function ArtifactContentPreview({ artifact, session, remote }: { artifact: SessionArtifact; session: WorkspaceSession; remote: boolean }) {
	const { t } = useTranslation();
	// The frame allows same-origin, which is only safe on the dedicated inline artifact
	// origin. A remote host's page URL carries a proxy capability token the page could
	// read, so a remote or Cloud session shows the text excerpt instead.
	const page = artifact.kind === "html" && !remote && artifact.inlineUrl && isInlineArtifactUrl(artifact.inlineUrl) ? artifact.inlineUrl : undefined;
	const file = useQuery({ ...sessionArtifactFileQueryOptions(session.id, artifact.path, artifact.rawUrl), enabled: Boolean(artifact.rawUrl) && !page, staleTime: 30_000 });
	if (page) return <ArtifactPagePreview url={page} title={artifact.name} />;
	if (file.isLoading) return <div className="space-y-1.5"><Skeleton className="h-2.5 w-full" /><Skeleton className="h-2.5 w-5/6" /><Skeleton className="h-2.5 w-2/3" /></div>;
	const content = file.data && !file.data.binary ? file.data.content : "";
	const delimiter = tabularDelimiter(artifact.path);
	const rows = content && delimiter ? csvPreviewRows(content, delimiter, TABLE_PREVIEW_ROWS, TABLE_PREVIEW_COLUMNS) : [];
	if (rows.length > 0) return <ArtifactTablePreview rows={rows} />;
	const frontmatter = artifact.kind === "markdown" && content ? parseMarkdownFrontmatter(content) : undefined;
	const title = frontmatter?.name ? clampPreviewText(frontmatter.name) : undefined;
	const description = frontmatter?.description ? clampPreviewText(frontmatter.description) : undefined;
	const markdown = artifact.kind === "markdown" && content ? markdownPreviewSource(content) : "";
	const excerpt = content && !markdown ? artifactExcerpt(artifact.kind, content) : "";
	if (!title && !description && !markdown && !excerpt) return <p className="text-xs text-muted-foreground">{t("artifactLink.noPreview")}</p>;
	return (
		<div className="space-y-1.5">
			{title || description ? (
				<div className="space-y-0.5">
					{title ? <p className="truncate text-xs font-semibold text-foreground">{title}</p> : null}
					{description ? <p className="line-clamp-4 break-words text-xs leading-4 text-muted-foreground">{description}</p> : null}
				</div>
			) : null}
			{markdown ? <ArtifactMarkdownPreview source={markdown} clamp={title || description ? "short" : "tall"} /> : null}
			{excerpt ? (
				<p className={cn("whitespace-pre-wrap break-words text-xs leading-4 text-popover-foreground", title || description ? "line-clamp-3" : "line-clamp-6", artifact.kind === "file" && "font-mono")}>{excerpt}</p>
			) : null}
		</div>
	);
}

function ArtifactCardBody({ artifact, session, remote }: { artifact: SessionArtifact; session: WorkspaceSession; remote: boolean }) {
	const { t } = useTranslation();
	const Icon = artifact.kind === "html" ? Globe : tabularDelimiter(artifact.path) ? Sheet : FileText;
	const author = session.kind === "orchestrator" ? t("shell.orchestrator") : session.title;
	return (
		<div className="space-y-2" role="status" aria-label={artifact.name}>
			<div className="flex min-w-0 items-start gap-2">
				<Icon aria-hidden="true" className="mt-0.5 size-icon-sm shrink-0 text-muted-foreground" />
				<div className="min-w-0 flex-1">
					<p className="truncate text-sm font-semibold text-foreground">{artifact.name}</p>
					<p className="mt-0.5 truncate text-xs text-muted-foreground">
						{author}
						{author !== session.id ? <span className="font-mono"> · {session.id}</span> : null}
					</p>
				</div>
				<span className="shrink-0 text-xs text-muted-foreground">{relativeTime(artifact.updatedAt, t)}</span>
			</div>
			<div className="border-t border-border pt-2">
				<ArtifactContentPreview artifact={artifact} session={session} remote={remote} />
			</div>
		</div>
	);
}

export function ArtifactLinkPreviewCard({
	href,
	sourceHostId,
	sourceKind,
}: {
	href: string;
	sourceHostId?: string;
	sourceKind?: "cloud";
}) {
	const { t } = useTranslation();
	const target = parseArtifactLink(href);
	const source = useSessionLinkSource(sourceHostId, sourceKind);
	const workspace = target ? source.workspaces.find((candidate) => candidate.id === target.projectId) : undefined;
	const session = workspace?.sessions.find((candidate) => candidate.id === target?.sessionId);
	const artifact = session?.artifactFiles?.find((candidate) => candidate.path === target?.path);
	if (session && artifact) return (
		<HoverCardContent collisionPadding={8} sideOffset={6} className="w-80 p-3">
			<ArtifactCardBody artifact={artifact} session={session} remote={sourceKind === "cloud" || Boolean(sourceHostId && sourceHostId !== LOCAL_HOST)} />
		</HoverCardContent>
	);
	if (session) return (
		<HoverCardContent collisionPadding={8} sideOffset={6} className="p-3">
			<p role="status" className="text-sm font-medium text-foreground">{t("artifactLink.unavailable")}</p>
		</HoverCardContent>
	);
	if (source.isLoading) return <LoadingCard />;
	if (source.isError) return <ErrorCard />;
	return <UnavailableCard />;
}
