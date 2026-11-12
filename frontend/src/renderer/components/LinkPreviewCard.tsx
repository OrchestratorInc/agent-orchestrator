import { BrowserIcon } from "./icons";
import { useState, type ComponentProps } from "react";
import { useTranslation } from "react-i18next";
import { measureFaviconTone, type FaviconTone } from "../lib/favicon-tone";
import { cn } from "../lib/utils";
import { openLinkInSystemBrowser } from "../lib/external-link-policy";
import type { LinkPreview } from "../hooks/useLinkPreview";
import { HoverCardContent } from "./ui/hover-card";
import { Skeleton } from "./ui/skeleton";

const BODY_CLASS = "flex items-start gap-2 px-2.5 py-2";

function parseLink(url: string): { domain: string; path: string } {
	try {
		const parsed = new URL(url);
		const path = `${parsed.pathname}${parsed.search}`;
		return { domain: parsed.hostname.replace(/^www\./, ""), path: path === "/" ? "" : path };
	} catch {
		return { domain: url, path: "" };
	}
}

function linkDomain(url: string): string {
	return parseLink(url).domain;
}

function PreviewImage({ src, alt, className }: { src: string; alt: string; className?: string }) {
	const [failed, setFailed] = useState(false);
	if (failed) return null;
	return (
		<img
			src={src}
			alt={alt}
			referrerPolicy="no-referrer"
			onError={() => setFailed(true)}
			className={className}
		/>
	);
}

const FAVICON_TONE_CLASS: Record<FaviconTone, string> = {
	// A one-colour glyph drawn for the opposite theme is inverted to match this one.
	dark: "dark:invert",
	light: "invert dark:invert-0",
	fixed: "",
	// Unreadable pixels: most favicons are drawn for a light tab, so give them one in dark themes.
	unknown: "dark:bg-white/90",
};

function PreviewFavicon({ src }: { src: string }) {
	// Pixels are only readable from a CORS load, which hosts without CORS headers
	// reject, so the second attempt loads the icon plainly and leaves it unmeasured.
	const [corsFailed, setCorsFailed] = useState(false);
	const [failed, setFailed] = useState(false);
	const [tone, setTone] = useState<FaviconTone>("fixed");
	if (failed) return <BrowserIcon aria-hidden="true" className="size-3.5 shrink-0" />;
	return (
		<img
			key={corsFailed ? "plain" : "cors"}
			src={src}
			alt=""
			referrerPolicy="no-referrer"
			crossOrigin={corsFailed ? undefined : "anonymous"}
			onLoad={(event) => setTone(corsFailed ? "unknown" : measureFaviconTone(event.currentTarget))}
			onError={() => {
				if (corsFailed) setFailed(true);
				else setCorsFailed(true);
			}}
			className={cn("size-3.5 shrink-0 rounded-xs", FAVICON_TONE_CLASS[tone])}
		/>
	);
}

type OpenLinkProps = {
	href: string;
	onClick: (event: React.MouseEvent<HTMLAnchorElement>) => void;
};

function openLinkProps(url: string, onBrowserOpen?: (url: string) => void): OpenLinkProps {
	return {
		href: url,
		onClick: (event) => {
			event.preventDefault();
			if (onBrowserOpen && !event.ctrlKey && !event.metaKey && !event.altKey) onBrowserOpen(url);
			else void openLinkInSystemBrowser(url);
		},
	};
}

function LinkPreviewDomain({ domain, faviconUrl, linkProps, status }: {
	domain: string;
	faviconUrl?: string;
	linkProps?: OpenLinkProps;
	status?: string;
}) {
	return (
		<div className="flex items-center gap-1.5 text-caption leading-4 text-muted-foreground">
			{faviconUrl ? <PreviewFavicon key={faviconUrl} src={faviconUrl} /> : <BrowserIcon aria-hidden="true" className="size-3.5 shrink-0" />}
			{linkProps ? (
				<a {...linkProps} className="min-w-0 truncate hover:underline focus-visible:underline">{domain}</a>
			) : (
				<span className="min-w-0 truncate">{domain}</span>
			)}
			{status ? <span role="status" className="ml-auto shrink-0 text-passive">{status}</span> : null}
		</div>
	);
}

export function LinkPreviewCardLoading({ url }: { url: string }) {
	const { t } = useTranslation();
	return (
		<div data-testid="link-preview-loading" role="status" aria-label={t("session.statusChecking")} className={cn(BODY_CLASS, "flex-col gap-1")}>
			<LinkPreviewDomain domain={linkDomain(url)} />
			<Skeleton className="h-3 w-4/5" />
		</div>
	);
}

/** Where the link goes, for when the page offers nothing to preview. */
export function LinkPreviewFallback({ url, preview, onBrowserOpen }: {
	url: string;
	preview?: LinkPreview;
	onBrowserOpen?: (url: string) => void;
}) {
	const { t } = useTranslation();
	const { domain, path } = parseLink(preview?.url || url);
	const linkProps = openLinkProps(url, onBrowserOpen);
	return (
		<div data-testid="link-preview-fallback" className={cn(BODY_CLASS, "flex-col gap-0.5")}>
			<LinkPreviewDomain domain={domain} faviconUrl={preview?.faviconUrl} linkProps={linkProps} status={t("artifactLink.noPreview")} />
			{path ? <div className="w-full truncate font-mono text-caption leading-4 text-passive">{path}</div> : null}
		</div>
	);
}

export function LinkPreviewCard({ url, preview, onBrowserOpen }: {
	url: string;
	preview: LinkPreview;
	onBrowserOpen?: (url: string) => void;
}) {
	const linkProps = openLinkProps(url, onBrowserOpen);
	if (!preview.title && !preview.description) return <LinkPreviewFallback url={url} preview={preview} onBrowserOpen={onBrowserOpen} />;
	return (
		<div data-testid="link-preview-card" className={BODY_CLASS}>
			<div className="min-w-0 flex-1 space-y-0.5">
				<LinkPreviewDomain domain={linkDomain(preview.url || url)} faviconUrl={preview.faviconUrl} linkProps={linkProps} />
				{preview.title && (
					<a {...linkProps} className="line-clamp-2 text-xs font-medium leading-4 text-popover-foreground hover:underline focus-visible:underline">
						{preview.title}
					</a>
				)}
				{preview.description && (
					<div className="line-clamp-1 text-caption leading-4 text-muted-foreground">{preview.description}</div>
				)}
			</div>
			{preview.imageUrl && (
				<PreviewImage key={preview.imageUrl} src={preview.imageUrl} alt="" className="size-10 shrink-0 rounded-sm object-cover" />
			)}
		</div>
	);
}

/** The hover overlay for a web link: settles into content or the compact fallback. */
export function LinkPreviewHoverCard({ url, query, onBrowserOpen, ...contentProps }: Omit<ComponentProps<typeof HoverCardContent>, "children"> & {
	url: string;
	query: { data?: LinkPreview; isPending: boolean };
	onBrowserOpen?: (url: string) => void;
}) {
	return (
		<HoverCardContent collisionPadding={8} {...contentProps} className="w-64">
			{query.data ? (
				<LinkPreviewCard url={url} preview={query.data} onBrowserOpen={onBrowserOpen} />
			) : query.isPending ? (
				<LinkPreviewCardLoading url={url} />
			) : (
				<LinkPreviewFallback url={url} onBrowserOpen={onBrowserOpen} />
			)}
		</HoverCardContent>
	);
}
