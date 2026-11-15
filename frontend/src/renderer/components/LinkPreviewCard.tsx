import { BrowserIcon } from "./icons";
import { useEffect, useState, type ComponentProps } from "react";
import { useTranslation } from "react-i18next";
import { measureFaviconTone, type FaviconTone } from "../lib/favicon-tone";
import { cn } from "../lib/utils";
import { useResolvedTheme } from "../stores/ui-store";
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
	dark: "dark:invert",
	light: "invert dark:invert-0",
	fixed: "",
	unknown: "dark:bg-white/90",
};

const FAVICON_TIMEOUT_MS = 4_000;

function GlobeMark({ className, loading }: { className: string; loading?: boolean }) {
	return <BrowserIcon aria-hidden="true" className={cn(className, loading && "link-favicon-shimmer")} />;
}

const settledFavicons = new Map<string, FaviconTone | "failed">();
const MAX_SETTLED_FAVICONS = 500;

export function forgetSettledFavicons() {
	settledFavicons.clear();
}

function settleFavicon(src: string, result: FaviconTone | "failed") {
	settledFavicons.delete(src);
	settledFavicons.set(src, result);
	if (settledFavicons.size > MAX_SETTLED_FAVICONS) settledFavicons.delete(settledFavicons.keys().next().value as string);
}

function PreviewFavicon({ src, className }: { src: string; className: string }) {
	const settled = settledFavicons.get(src);
	const [corsFailed, setCorsFailed] = useState(settled === "unknown");
	const [failed, setFailed] = useState(settled === "failed");
	const [tone, setTone] = useState<FaviconTone | undefined>(settled === "failed" ? undefined : settled);
	const [expired, setExpired] = useState(false);
	useEffect(() => {
		if (tone || failed) return;
		const timer = window.setTimeout(() => setExpired(true), FAVICON_TIMEOUT_MS);
		return () => window.clearTimeout(timer);
	}, [failed, tone]);
	if (failed || (expired && !tone)) return <GlobeMark className={className} />;
	return (
		<>
			{tone ? null : <GlobeMark className={className} loading />}
			<img
				key={corsFailed ? "plain" : "cors"}
				src={src}
				alt=""
				aria-hidden="true"
				referrerPolicy="no-referrer"
				crossOrigin={corsFailed ? undefined : "anonymous"}
				onLoad={(event) => {
					const measured = corsFailed ? "unknown" : measureFaviconTone(event.currentTarget);
					settleFavicon(src, measured);
					setTone(measured);
				}}
				onError={() => {
					if (!corsFailed) setCorsFailed(true);
					else {
						settleFavicon(src, "failed");
						setFailed(true);
					}
				}}
				className={cn(className, "rounded-xs", tone ? FAVICON_TONE_CLASS[tone] : "hidden")}
			/>
		</>
	);
}

export function LinkFavicon({ faviconUrl, faviconDarkUrl, pending = false, className = "size-3.5 shrink-0" }: {
	faviconUrl?: string;
	faviconDarkUrl?: string;
	pending?: boolean;
	className?: string;
}) {
	const src = (useResolvedTheme() === "dark" && faviconDarkUrl) || faviconUrl;
	const hasSrc = Boolean(src);
	const [lookupExpired, setLookupExpired] = useState(false);
	useEffect(() => {
		if (hasSrc) return;
		const timer = window.setTimeout(() => setLookupExpired(true), FAVICON_TIMEOUT_MS);
		return () => window.clearTimeout(timer);
	}, [hasSrc]);
	return src && !lookupExpired
		? <PreviewFavicon key={src} src={src} className={className} />
		: <GlobeMark className={className} loading={!src && pending && !lookupExpired} />;
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

function LinkPreviewDomain({ domain, faviconUrl, faviconDarkUrl, pending, linkProps, status }: {
	domain: string;
	faviconUrl?: string;
	faviconDarkUrl?: string;
	pending?: boolean;
	linkProps?: OpenLinkProps;
	status?: string;
}) {
	return (
		<div className="flex items-center gap-1.5 text-caption leading-4 text-muted-foreground">
			<LinkFavicon faviconUrl={faviconUrl} faviconDarkUrl={faviconDarkUrl} pending={pending} />
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
			<LinkPreviewDomain domain={linkDomain(url)} pending />
			<Skeleton className="h-3 w-4/5" />
		</div>
	);
}

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
			<LinkPreviewDomain domain={domain} faviconUrl={preview?.faviconUrl} faviconDarkUrl={preview?.faviconDarkUrl} linkProps={linkProps} status={t("artifactLink.noPreview")} />
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
				<LinkPreviewDomain domain={linkDomain(preview.url || url)} faviconUrl={preview.faviconUrl} faviconDarkUrl={preview.faviconDarkUrl} linkProps={linkProps} />
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
