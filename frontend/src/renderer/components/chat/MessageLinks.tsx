/**
 * The web links a reply points at, gathered under its action row.
 *
 * A reply that cites several pages scatters them through its prose. The "N links"
 * button beside copy and roll back stacks their site icons and opens a list: each
 * page's title, its site, and an arrow to open it, the way the inline link would.
 * Only web links count (files and sessions open elsewhere), and only from the
 * reply's own text: a URL inside a code block is code, not a citation.
 */

import { useEffect, useMemo } from "react";
import { ArrowUpRight, ChevronDown } from "@/components/app-icons";
import { cn } from "../../lib/utils";
import { isWebLink } from "../../lib/external-link-policy";
import { isHostLocalWebLink } from "../../lib/loopback";
import { canLoadLinkFavicon, preloadLinkFavicon } from "../../lib/link-preload";
import { useCachedLinkFavicons, useLinkPreview } from "../../hooks/useLinkPreview";
import { usePullRequestPreview } from "../../hooks/usePullRequestPreview";
import { AppLink } from "../AppLink";
import { LinkFavicon } from "../LinkPreviewCard";
import { GitHubMark, parsePullRequestLink } from "./PullRequestLink";

export type MessageLink = { url: string; text?: string };

/** The button shows only for this many links or more; one link reads fine where it is. */
export const MESSAGE_LINKS_MIN = 2;

const MARKDOWN_LINK = /\[([^\]\n]*)\]\(\s*<?(https?:\/\/[^\s)>]+)>?(?:\s+"[^"]*")?\s*\)/g;
const BARE_URL = /<?(https?:\/\/[^\s<>"'`]+)/g;

/** A bare URL's end, without the sentence's punctuation or an unmatched closing bracket. */
function trimUrl(url: string): string {
	let end = url.replace(/[.,;:!?*_~]+$/, "");
	while (end.endsWith(")") && (end.match(/\(/g)?.length ?? 0) < (end.match(/\)/g)?.length ?? 0)) end = end.slice(0, -1);
	while (end.endsWith("]") && (end.match(/\[/g)?.length ?? 0) < (end.match(/\]/g)?.length ?? 0)) end = end.slice(0, -1);
	return end.replace(/>$/, "");
}

/** The markdown with fenced blocks and inline code blanked, so their URLs are not links. */
function withoutCode(markdown: string): string {
	let fence: string | undefined;
	const lines = markdown.split("\n").map((line) => {
		const marker = /^\s*(`{3,}|~{3,})/.exec(line)?.[1];
		if (fence) {
			if (marker && marker[0] === fence[0] && marker.length >= fence.length) fence = undefined;
			return "";
		}
		if (marker) {
			fence = marker;
			return "";
		}
		return line;
	});
	return lines.join("\n").replace(/`[^`\n]*`/g, " ");
}

function sameLink(a: string, b: string): boolean {
	return a.replace(/\/$/, "") === b.replace(/\/$/, "");
}

/** The web links in a reply's markdown, in order, once each, skipping code. */
export function messageWebLinks(markdown: string): MessageLink[] {
	const prose = withoutCode(markdown);
	const found: { at: number; link: MessageLink }[] = [];
	const covered: Array<[number, number]> = [];
	for (const match of prose.matchAll(MARKDOWN_LINK)) {
		const url = trimUrl(match[2]!);
		if (!isWebLink(url)) continue;
		const text = match[1]!.trim();
		found.push({ at: match.index!, link: { url, text: text && !sameLink(text, url) ? text : undefined } });
		covered.push([match.index!, match.index! + match[0].length]);
	}
	for (const match of prose.matchAll(BARE_URL)) {
		const at = match.index!;
		if (covered.some(([start, end]) => at >= start && at < end)) continue;
		const url = trimUrl(match[1]!);
		if (isWebLink(url)) found.push({ at, link: { url } });
	}
	found.sort((a, b) => a.at - b.at);
	const links: MessageLink[] = [];
	for (const { link } of found) {
		const same = links.find((each) => sameLink(each.url, link.url));
		if (!same) links.push(link);
		else if (!same.text && link.text) same.text = link.text;
	}
	return links;
}

/** Where a link points, as a chip or a list row names it: `anthropic.com`, `localhost:3000`. */
export function linkSiteLabel(url: string): string {
	try {
		const parsed = new URL(url);
		if (isHostLocalWebLink(url)) return parsed.host;
		return parsed.hostname.replace(/^www\./, "");
	} catch {
		return url;
	}
}

/**
 * A plain GitHub issue URL as GitHub writes it, `electron#41322`: the repository's
 * name and the number. Pull requests have their own chip, with their state.
 */
export function githubIssueLabel(url: string): string | undefined {
	try {
		const parsed = new URL(url);
		if (parsed.protocol !== "https:" || parsed.host !== "github.com" || parsed.search || parsed.username) return undefined;
		const match = /^\/[\w.-]+\/([\w.-]+)\/issues\/(\d+)\/?$/.exec(parsed.pathname);
		return match ? `${match[1]}#${match[2]}` : undefined;
	} catch {
		return undefined;
	}
}

/** What a bare link's chip shows: GitHub's mark and `repo#N` for an issue, else the site's icon and name. */
export function LinkSiteChipContent({ url }: { url: string }) {
	const issue = githubIssueLabel(url);
	return (
		<>
			{issue ? <GitHubMark className="size-3.5 shrink-0 text-foreground" /> : <LinkSiteIcon url={url} />}
			<span data-link-site="">{issue ?? linkSiteLabel(url)}</span>
		</>
	);
}

/** A link's site icon: asked for once it is on screen, a globe for a local page or none. */
export function LinkSiteIcon({ url, className }: { url: string; className?: string }) {
	const cachedState = useCachedLinkFavicons(url);
	const favicons = cachedState === "failed" ? undefined : cachedState;
	const loadable = useMemo(() => canLoadLinkFavicon(url), [url]);
	useEffect(() => {
		if (!favicons && loadable) preloadLinkFavicon(url);
	}, [favicons, loadable, url]);
	return (
		<LinkFavicon
			faviconUrl={favicons?.faviconUrl}
			faviconDarkUrl={favicons?.faviconDarkUrl}
			pending={cachedState === undefined && loadable}
			className={cn("size-3.5 shrink-0", className)}
		/>
	);
}

/** The "N links" button: up to three site icons, stacked, then the count. */
export function MessageLinksButton({ links, open, onToggle }: { links: MessageLink[]; open: boolean; onToggle: () => void }) {
	const shown = links.slice(0, 3);
	return (
		<button
			type="button"
			onClick={onToggle}
			aria-expanded={open}
			aria-label={`${links.length} links`}
			data-message-links=""
			className="ms-1 inline-flex h-7 shrink-0 items-center gap-1.5 rounded-md bg-surface pr-2 pl-1.5 text-control text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring"
		>
			<span aria-hidden="true" className="flex items-center">
				{shown.map((link, index) => (
					<span
						key={link.url}
						className={cn(
							"flex size-[18px] items-center justify-center rounded-full bg-background ring-2 ring-[var(--color-bg-secondary)]",
							index > 0 && "-ms-1.5",
						)}
					>
						<LinkSiteIcon url={link.url} className="size-3" />
					</span>
				))}
			</span>
			<span>{links.length} links</span>
			<ChevronDown aria-hidden="true" className={cn("size-3 transition-transform", open && "rotate-180")} />
		</button>
	);
}

/** The opened list: one row per link, opening it the way the inline link does. */
export function MessageLinksList({ links }: { links: MessageLink[] }) {
	return (
		<ul data-message-links-list="" className="m-0 flex list-none flex-col rounded-lg border border-border p-1">
			{links.map((link) => <MessageLinkRow key={link.url} link={link} />)}
		</ul>
	);
}

function MessageLinkRow({ link }: { link: MessageLink }) {
	const local = isHostLocalWebLink(link.url);
	const pullRequest = parsePullRequestLink(link.url);
	const { data: pr } = usePullRequestPreview(link.url, Boolean(pullRequest));
	const { data: page } = useLinkPreview(link.url, !local && !pullRequest);
	const title = pullRequest
		? pr?.title ? `${pr.title} · #${pullRequest.number}` : link.text ?? `${pullRequest.repo}#${pullRequest.number}`
		: page?.title?.trim() || link.text || shortUrl(link.url);
	return (
		<li>
			<AppLink
				href={link.url}
				noHoverPreview
				target="_blank"
				rel="noreferrer noopener"
				title={link.url}
				className="flex h-9 min-w-0 items-center gap-3 rounded-md px-2.5 text-sm text-foreground no-underline transition-colors hover:bg-interactive-hover focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-ring"
			>
				<LinkSiteIcon url={link.url} className="size-4" />
				<span className="min-w-0 flex-1 truncate">{title}</span>
				<span className="shrink-0 text-control text-muted-foreground">{local ? "Browser" : linkSiteLabel(link.url)}</span>
				<ArrowUpRight aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
			</AppLink>
		</li>
	);
}

/** A link without its scheme or trailing slash: `localhost:3000/settings`. */
function shortUrl(url: string): string {
	return url.replace(/^https?:\/\//, "").replace(/\/$/, "");
}
