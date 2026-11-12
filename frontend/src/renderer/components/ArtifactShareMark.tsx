import { useTranslation } from "react-i18next";
import { Globe, Link, Users } from "@/components/app-icons";
import { artifactSharing, type ArtifactRow } from "../lib/artifacts";
import { cn } from "../lib/utils";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

/**
 * How a published artifact is shared, beside its time on a card or a row: a
 * green globe for a public link, as the publish panel marks one, and amber
 * people for a private link, open only to its owner and the people invited,
 * and a muted link while who it opens for is not known yet (the link list has
 * not been read, or could not be). Nothing for a file that is not shared.
 */
export function ArtifactShareMark({ row, className, iconClassName }: {
	row: Pick<ArtifactRow, "shareUrl" | "shareAccess" | "cloud">;
	className?: string;
	iconClassName?: string;
}) {
	const { t } = useTranslation();
	if (!row.shareUrl) return null;
	const sharing = artifactSharing(row) ?? "published";
	const shared = sharing === "shared";
	const label = shared ? t("artifacts.sharedPrivately") : t("artifacts.published");
	const Icon = shared ? Users : sharing === "public" ? Globe : Link;
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span
					aria-label={label}
					className={cn("inline-flex shrink-0", shared ? "text-status-in-review" : sharing === "public" ? "text-success" : "text-muted-foreground", className)}
					data-artifact-share-mark={sharing}
					role="img"
				>
					<Icon aria-hidden="true" className={iconClassName} />
				</span>
			</TooltipTrigger>
			<TooltipContent side="bottom">{label}</TooltipContent>
		</Tooltip>
	);
}
