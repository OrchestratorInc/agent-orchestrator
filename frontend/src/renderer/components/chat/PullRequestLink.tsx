import { useTranslation } from "react-i18next";
import { Check, CircleDashed, X } from "@/components/app-icons";
import { usePullRequestPreview, type PullRequestPreview } from "../../hooks/usePullRequestPreview";
import { openLinkInSystemBrowser } from "../../lib/external-link-policy";
import { isPullRequestLink } from "../../lib/link-preload";
import { formatRelativeTime } from "../../lib/usage-format";
import { cn } from "../../lib/utils";
import { LinkPreviewCardLoading, LinkPreviewFallback } from "../LinkPreviewCard";
import { prStateIcon } from "../PRPills";
import { HoverCardContent } from "../ui/hover-card";

/** owner/name and number of a plain github.com pull request URL. */
export function parsePullRequestLink(href: string): { repo: string; number: number } | undefined {
	if (!isPullRequestLink(href)) return undefined;
	const [owner, name, , number] = new URL(href).pathname.split("/").filter(Boolean);
	return { repo: `${owner}/${name}`, number: Number(number) };
}

/**
 * What a pull request link's chip says, when its text only names the PR: the
 * URL itself, "#12", "PR #12" or "owner/name#12" for that same PR. A link the
 * writer put their own words on stays a text link. The repository shows only
 * when the text named it.
 */
export function pullRequestChipLabel(href: string, text: string): { repo?: string; number: number } | undefined {
	const link = parsePullRequestLink(href);
	if (!link) return undefined;
	const label = text.trim();
	if (label.replace(/\/$/, "") === href.replace(/\/$/, "")) return { number: link.number };
	const ref = /^(?:PR\s*)?#(\d+)$/i.exec(label);
	if (ref) return Number(ref[1]) === link.number ? { number: link.number } : undefined;
	const repoRef = /^([\w.-]+\/[\w.-]+)#(\d+)$/.exec(label);
	if (repoRef && repoRef[1]!.toLowerCase() === link.repo.toLowerCase() && Number(repoRef[2]) === link.number) {
		return { repo: repoRef[1], number: link.number };
	}
	return undefined;
}

/** GitHub's mark, in the text colour: the site's own icon for a PR whose state is not known yet. */
function GitHubMark({ className }: { className?: string }) {
	return (
		<svg viewBox="0 0 16 16" aria-hidden="true" className={className}>
			<path fill="currentColor" d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z" />
		</svg>
	);
}

/**
 * A PR state's icon and colour in a chat. A draft uses the muted text colour
 * rather than the passive one the PR panel uses: at chip size on the chip's
 * fill, passive grey all but disappears.
 */
function chatPrState(state: PullRequestPreview["state"]) {
	const base = prStateIcon[state];
	return state === "draft" ? { ...base, className: "text-muted-foreground" } : base;
}

/** The chip's look; the anchor itself is the chat's link. */
export const PULL_REQUEST_CHIP_CLASS = "inline-flex h-5 items-center gap-1 rounded-sm bg-interactive-active px-1.5 align-[-0.3em] text-control font-medium leading-5 whitespace-nowrap text-foreground tabular-nums no-underline transition-colors hover:bg-[color-mix(in_oklch,var(--color-text-primary)_11%,transparent)] focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring";

/**
 * A pull request link's chip: its state icon and number, or GitHub's mark
 * until the state is known (or when it cannot be read). Reads only what the
 * on-screen preload or a hover card put in the cache.
 */
export function PullRequestChipContent({ href, repo, number }: { href: string; repo?: string; number: number }) {
	const { t } = useTranslation();
	const { data } = usePullRequestPreview(href, false);
	const state = data ? chatPrState(data.state) : undefined;
	const Icon = state?.icon;
	return (
		<>
			{Icon ? <Icon aria-hidden="true" className={cn("size-3.5 shrink-0", state.className)} /> : <GitHubMark className="size-3.5 shrink-0 text-foreground" />}
			{repo ? <span className="font-normal text-muted-foreground">{repo}</span> : null}
			<span>#{number}</span>
			{data ? <span className="sr-only">, {t(`pr.state.${data.state}`)}</span> : null}
		</>
	);
}

const CHECKS = {
	passing: { icon: Check, className: "text-success", labelKey: "pr.card.checksPassing" },
	failing: { icon: X, className: "text-error", labelKey: "pr.card.checksFailing" },
	pending: { icon: CircleDashed, className: "text-muted-foreground", labelKey: "pr.card.checksPending" },
} as const;

function PullRequestCard({ url, preview, onBrowserOpen }: { url: string; preview: PullRequestPreview; onBrowserOpen?: (url: string) => void }) {
	const { t, i18n } = useTranslation();
	const state = chatPrState(preview.state);
	const StateIcon = state.icon;
	const checks = preview.checks ? CHECKS[preview.checks] : undefined;
	const ChecksIcon = checks?.icon;
	const when = preview.stateChangedAt ? formatRelativeTime(preview.stateChangedAt, new Date(), i18n.language) : "";
	const open = (event: React.MouseEvent<HTMLAnchorElement>) => {
		event.preventDefault();
		if (onBrowserOpen && !event.ctrlKey && !event.metaKey && !event.altKey) onBrowserOpen(url);
		else void openLinkInSystemBrowser(url);
	};
	const showDiff = preview.additions > 0 || preview.deletions > 0 || preview.changedFiles > 0;
	return (
		<div data-testid="pr-preview-card" className="flex flex-col gap-1.5 px-3 py-2.5">
			<div className="flex items-center gap-1.5 text-caption leading-4 text-muted-foreground">
				<GitHubMark className="size-3.5 shrink-0 text-foreground" />
				<a href={url} onClick={open} className="min-w-0 truncate hover:underline focus-visible:underline">{preview.repo}</a>
			</div>
			{preview.title ? (
				<a href={url} onClick={open} className="line-clamp-2 text-sm font-medium leading-5 text-popover-foreground hover:underline focus-visible:underline">
					{preview.title}
				</a>
			) : null}
			<div className="flex min-w-0 items-center gap-1.5 text-caption leading-4 text-muted-foreground tabular-nums">
				<StateIcon aria-hidden="true" className={cn("size-3.5 shrink-0", state.className)} />
				<span className={state.className}>{t(`prLink.state.${preview.state}`)}</span>
				<span aria-hidden="true">·</span>
				<span>#{preview.number}</span>
				{when ? (
					<>
						<span aria-hidden="true">·</span>
						<span className="truncate">{when}</span>
					</>
				) : null}
			</div>
			{preview.sourceBranch && preview.targetBranch ? (
				<div className="truncate font-mono text-caption leading-4 text-muted-foreground">
					{preview.sourceBranch} → {preview.targetBranch}
				</div>
			) : null}
			{checks || showDiff ? (
				<div className="mt-1 flex items-center justify-between gap-3 border-t border-border pt-2 text-caption leading-4 text-muted-foreground tabular-nums">
					{checks && ChecksIcon ? (
						<span className="flex min-w-0 items-center gap-1.5">
							<ChecksIcon aria-hidden="true" className={cn("size-3.5 shrink-0", checks.className)} />
							<span className="truncate">{t(checks.labelKey)}</span>
						</span>
					) : <span />}
					{showDiff ? (
						<span className="shrink-0">
							<span className="text-success">+{preview.additions}</span>{" "}
							<span className="text-error">−{preview.deletions}</span>
							{" · "}
							{t("prLink.files", { count: preview.changedFiles })}
						</span>
					) : null}
				</div>
			) : null}
		</div>
	);
}

/**
 * The hover card for any GitHub pull request link in a chat. Falls back to
 * the plain link card when the daemon cannot read the PR (no GitHub account,
 * no access, or it does not exist).
 */
export function PullRequestHoverCard({ url, onBrowserOpen }: { url: string; onBrowserOpen?: (url: string) => void }) {
	const query = usePullRequestPreview(url, true);
	return (
		<HoverCardContent collisionPadding={8} className="w-80">
			{query.data ? (
				<PullRequestCard url={url} preview={query.data} onBrowserOpen={onBrowserOpen} />
			) : query.isPending ? (
				<LinkPreviewCardLoading url={url} />
			) : (
				<LinkPreviewFallback url={url} onBrowserOpen={onBrowserOpen} />
			)}
		</HoverCardContent>
	);
}
