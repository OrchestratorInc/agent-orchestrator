import { SquareArrowOutUpRight } from "lucide-react";
import { useTranslation } from "react-i18next";
import { isWebLink, openLinkInSystemBrowser } from "../lib/external-link-policy";
import { TopbarButton } from "./TopbarButton";
import { Button } from "./ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

/**
 * Opens a bring-your-own Coder session's workspace page in the system browser.
 * Renders nothing without an http(s) URL (managed Coder, other providers, or a
 * control plane that predates the field).
 */
export function CoderWorkspaceLinkButton({ url }: { url?: string }) {
	const { t } = useTranslation();
	if (!url || !isWebLink(url)) return null;
	const label = t("terminal.sessionLoader.openCoderWorkspace");
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<Button
					type="button"
					variant="ghost"
					size="icon-sm"
					aria-label={label}
					className="text-muted-foreground hover:text-foreground"
					data-testid="coder-workspace-link"
					onClick={() => void openLinkInSystemBrowser(url)}
				>
					<SquareArrowOutUpRight aria-hidden="true" className="size-icon-sm" />
				</Button>
			</TooltipTrigger>
			<TooltipContent side="bottom">{label}</TooltipContent>
		</Tooltip>
	);
}

/**
 * The Coder workspace name in a session's workspace page URL
 * (<base>/@<owner>/<workspace name>), or "" when the URL has no such segment.
 */
export function coderWorkspaceNameFromUrl(url: string): string {
	try {
		const segments = new URL(url).pathname.split("/").filter(Boolean);
		if (segments.length < 2 || !segments[segments.length - 2]?.startsWith("@")) return "";
		return decodeURIComponent(segments[segments.length - 1] ?? "");
	} catch {
		return "";
	}
}

/**
 * The topbar control naming a bring-your-own Coder session's workspace and
 * opening it in the system browser, for the whole life of the session (the
 * startup screens show only the icon button above). Renders nothing without a
 * workspace page URL.
 */
export function CoderWorkspaceTopbarButton({ url, style }: { url?: string; style?: React.CSSProperties }) {
	const { t } = useTranslation();
	if (!url || !isWebLink(url)) return null;
	const name = coderWorkspaceNameFromUrl(url);
	const label = name ? t("shell.openCoderWorkspaceNamed", { name }) : t("terminal.sessionLoader.openCoderWorkspace");
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span className="inline-flex" style={style}>
					<TopbarButton
						aria-label={label}
						className="topbar-control--labeled"
						data-priority="secondary"
						data-testid="coder-workspace-topbar-link"
						variant="accent"
						onClick={() => void openLinkInSystemBrowser(url)}
					>
						<SquareArrowOutUpRight className="size-icon-md" aria-hidden="true" />
						{name ? <span data-compact-label className="max-w-48 truncate font-mono text-xs">{name}</span> : null}
					</TopbarButton>
				</span>
			</TooltipTrigger>
			<TooltipContent side="bottom">{label}</TooltipContent>
		</Tooltip>
	);
}
