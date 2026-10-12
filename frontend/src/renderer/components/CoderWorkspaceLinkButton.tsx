import { SquareArrowOutUpRight } from "lucide-react";
import { useTranslation } from "react-i18next";
import { isWebLink, openLinkInSystemBrowser } from "../lib/external-link-policy";
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
