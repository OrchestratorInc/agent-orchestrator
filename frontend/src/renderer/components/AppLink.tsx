import { BrowserIcon } from "./icons";
import { createContext, useContext, useEffect, useImperativeHandle, useRef, useState, type ComponentProps, type ReactNode } from "react";
import { Copy, ExternalLink, FileText, } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../lib/bridge";
import { isSystemBrowserLink, isWebLink, openLinkInSystemBrowser } from "../lib/external-link-policy";
import { useLinkPreview } from "../hooks/useLinkPreview";
import { observeLinkForPreload } from "../lib/link-preload";
import { LinkPreviewHoverCard } from "./LinkPreviewCard";
import {
	ContextMenu,
	ContextMenuTrigger,
	ContextMenuContent,
	ContextMenuItem,
	ContextMenuSeparator,
} from "./ui/context-menu";
import { ChatPathMenuContext, ChatPathMenuItems } from "./ChatPathMenu";
import { HoverCard, HoverCardTrigger } from "./ui/hover-card";

export const AppBrowserLinkContext = createContext<((url: string) => void) | undefined>(undefined);

export function AppLink({ href, onClick, onAuxClick, onBrowserOpen, inAppLink, systemBrowser, filePath, menuPath, onFileOpen, hoverPreview, noHoverPreview, ref, ...props }: ComponentProps<"a"> & {
	onBrowserOpen?: (url: string) => void;
	inAppLink?: (url: string) => boolean;
	systemBrowser?: boolean;
	filePath?: string;
	menuPath?: string;
	onFileOpen?: (path: string) => void;
	hoverPreview?: (open: boolean) => ReactNode;
	noHoverPreview?: boolean;
}) {
	const { t } = useTranslation();
	const sessionBrowserOpen = useContext(AppBrowserLinkContext);
	const pathMenuContext = useContext(ChatPathMenuContext);
	const contextPath = menuPath ?? filePath;
	const openBrowser = systemBrowser ? undefined : onBrowserOpen ?? sessionBrowserOpen;
	const webLink = !!href && isWebLink(href);
	const browserLink = !systemBrowser && !!href && (inAppLink?.(href) ?? webLink);
	const showBrowserAction = browserLink && !(contextPath && pathMenuContext);
	const hasHoverPreview = !noHoverPreview && (webLink || Boolean(hoverPreview));
	const [previewOpen, setPreviewOpen] = useState(false);
	const previewQuery = useLinkPreview(href ?? "", previewOpen && webLink);
	const anchorRef = useRef<HTMLAnchorElement>(null);
	useImperativeHandle(ref, () => anchorRef.current as HTMLAnchorElement);
	useEffect(() => (hasHoverPreview ? observeLinkForPreload(anchorRef.current, href) : undefined), [hasHoverPreview, href]);
	const anchor = (
		<a
			{...props}
			ref={anchorRef}
			href={href}
			onClick={(event) => {
				onClick?.(event);
				if (event.defaultPrevented || !href) return;
				if (systemBrowser) {
					event.preventDefault();
					if (isSystemBrowserLink(href)) void openLinkInSystemBrowser(href);
					return;
				}
				if (!browserLink) return;
				event.preventDefault();
				if (openBrowser && (!webLink || (!event.ctrlKey && !event.metaKey && !event.altKey))) openBrowser(href);
				else void openLinkInSystemBrowser(href);
			}}
			onAuxClick={(event) => {
				onAuxClick?.(event);
				if (!systemBrowser || event.button !== 1 || event.defaultPrevented || !href) return;
				event.preventDefault();
				if (isSystemBrowserLink(href)) void openLinkInSystemBrowser(href);
			}}
		/>
	);
	if (!href || href.startsWith("#") || (href.startsWith("/") && !browserLink && !filePath)) return anchor;
	const trigger = hasHoverPreview ? (
		<ContextMenuTrigger asChild>
			<HoverCardTrigger
				asChild
				onFocus={(event) => {
					if (!event.currentTarget.matches(":focus-visible")) event.preventDefault();
				}}
			>
				{anchor}
			</HoverCardTrigger>
		</ContextMenuTrigger>
	) : (
		<ContextMenuTrigger asChild>{anchor}</ContextMenuTrigger>
	);
	const menu = (
		<ContextMenu>
			{trigger}
			<ContextMenuContent className="min-w-52">
				{showBrowserAction && (
					<ContextMenuItem disabled={!openBrowser} onSelect={() => openBrowser?.(href)}>
						<BrowserIcon aria-hidden="true" />
						{t("link.openInAOBrowser")}
					</ContextMenuItem>
				)}
				{filePath && onFileOpen && (
					<ContextMenuItem onSelect={() => onFileOpen(filePath)}>
						<FileText aria-hidden="true" />
						{t("link.openInAO")}
					</ContextMenuItem>
				)}
				{webLink && (
					<ContextMenuItem onSelect={() => void openLinkInSystemBrowser(href)}>
						<ExternalLink aria-hidden="true" />
						{t("link.openInExternalBrowser")}
					</ContextMenuItem>
				)}
				{(showBrowserAction || (filePath && onFileOpen)) && <ContextMenuSeparator />}
				{contextPath && pathMenuContext ? <ChatPathMenuItems path={contextPath} /> : (
					<ContextMenuItem onSelect={() => void aoBridge.clipboard.writeText(href)}>
						<Copy aria-hidden="true" />
						{t("link.copy")}
					</ContextMenuItem>
				)}
			</ContextMenuContent>
		</ContextMenu>
	);
	if (!hasHoverPreview) return menu;
	return (
		<HoverCard onOpenChange={setPreviewOpen}>
			{menu}
			{previewOpen && webLink && <LinkPreviewHoverCard url={href} query={previewQuery} onBrowserOpen={openBrowser} />}
			{previewOpen && !webLink && hoverPreview?.(previewOpen)}
		</HoverCard>
	);
}
