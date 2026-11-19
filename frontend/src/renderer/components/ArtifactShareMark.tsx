import { useTranslation } from "react-i18next";
import { Globe, Link } from "@/components/app-icons";
import type { CloudShareAccess } from "../../shared/cloud-share";
import { artifactSharing, type ArtifactRow } from "../lib/artifacts";
import { cn } from "../lib/utils";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

/**
 * Who a link opens for, as one icon and color everywhere it shows: a green
 * globe for a public link, anyone with it opens it, and an amber link for a
 * private one, open only to its owner and the people invited.
 */
export function shareAccessIcon(access: CloudShareAccess): { Icon: typeof Globe; className: string } {
	return access === "anyone" ? { Icon: Globe, className: "text-success" } : { Icon: Link, className: "text-status-in-review" };
}

/**
 * How a published artifact is shared, beside its time on a card or a row: as
 * shareAccessIcon marks its link, and a muted link while who it opens for is
 * not known yet (the link list has not been read, or could not be). Nothing
 * for a file that is not shared.
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
	const known = sharing === "shared" || sharing === "public" ? shareAccessIcon(shared ? "restricted" : "anyone") : undefined;
	const Icon = known?.Icon ?? Link;
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span
					aria-label={label}
					className={cn("inline-flex shrink-0", known?.className ?? "text-muted-foreground", className)}
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
