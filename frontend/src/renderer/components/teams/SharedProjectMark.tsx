import { Users } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { WorkspaceShare } from "../../types/workspace";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

/**
 * Marks a project someone else shared with this person, without a pill in
 * the row: a quiet people mark whose tooltip says who shared it and through
 * which team.
 */
export function SharedProjectMark({ share }: { share: WorkspaceShare }) {
	const { t } = useTranslation();
	const source = share.wholeProject ?? share;
	const lines = [
		source.byName ? t("teams.sharedBy", { name: source.byName }) : t("teams.sharedWithYou"),
		source.viaTeamName ? t("teams.throughTeam", { team: source.viaTeamName }) : null,
	].filter(Boolean) as string[];
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span
					className="sidebar-expanded-chrome relative z-[1] inline-grid size-4 shrink-0 place-items-center text-passive group-data-[collapsible=icon]:hidden"
					aria-label={lines.join(". ")}
					data-shared-project-mark=""
				>
					<Users className="size-3" aria-hidden="true" />
				</span>
			</TooltipTrigger>
			<TooltipContent side="right" className="flex flex-col gap-0.5">
				{lines.map((line) => <span key={line}>{line}</span>)}
			</TooltipContent>
		</Tooltip>
	);
}
