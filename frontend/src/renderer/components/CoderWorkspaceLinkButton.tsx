import { SquareArrowOutUpRight } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import { isWebLink, openLinkInSystemBrowser } from "../lib/external-link-policy";
import { Button } from "./ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

export function CoderWorkspaceLinkButton({ url }: { url?: string }) {
	const { t } = useTranslation();
	if (!url || !isWebLink(url)) return null;
	const label = t("terminal.sessionLoader.openCoderWorkspace");
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<Button
					type="button"
					variant="quiet"
					size="icon-sm"
					aria-label={label}
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

export function coderWorkspaceNameFromUrl(url: string): string {
	try {
		const segments = new URL(url).pathname.split("/").filter(Boolean);
		if (segments.length < 2 || !segments[segments.length - 2]?.startsWith("@")) return "";
		return decodeURIComponent(segments[segments.length - 1] ?? "");
	} catch {
		return "";
	}
}

export function CoderWorkspaceTopbarButton({ url, style }: { url?: string; style?: React.CSSProperties }) {
	const { t } = useTranslation();
	if (!url || !isWebLink(url)) return null;
	const name = coderWorkspaceNameFromUrl(url);
	const label = name ? t("shell.openCoderWorkspaceNamed", { name }) : t("terminal.sessionLoader.openCoderWorkspace");
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span className="inline-flex" style={style}>
					<Button type="button" size="sm"
						aria-label={label}
						data-priority="secondary"
						data-testid="coder-workspace-topbar-link"
						variant="quiet"
						onClick={() => void openLinkInSystemBrowser(url)}
					>
						<SquareArrowOutUpRight className="size-icon-md" aria-hidden="true" />
						{name ? <span data-compact-label className="max-w-48 truncate font-mono text-xs">{name}</span> : null}
					</Button>
				</span>
			</TooltipTrigger>
			<TooltipContent side="bottom">{label}</TooltipContent>
		</Tooltip>
	);
}
