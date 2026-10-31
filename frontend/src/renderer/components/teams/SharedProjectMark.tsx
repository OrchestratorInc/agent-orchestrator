import type { TFunction } from "i18next";
import { Users } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { WorkspaceShare } from "../../types/workspace";
import { listNames } from "../share/accessLabels";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

/**
 * Who shared a project with this person and through which teams. A whole
 * project share speaks for itself; otherwise each session shared on its own
 * counts, so sessions that came through different teams name them all.
 */
export function sharedProjectLines(share: WorkspaceShare, t: TFunction, locale: string): string[] {
	const sources = share.wholeProject ? [share.wholeProject] : Object.values(share.sessions ?? {});
	if (sources.length === 0) sources.push(share);
	const unique = (values: Array<string | undefined>) => [...new Set(values.filter((value): value is string => Boolean(value)))];
	const more = (count: number) => t("share.summary.more", { count });
	const names = unique(sources.map((source) => source.byName));
	const teams = unique(sources.map((source) => source.viaTeamName));
	return [
		names.length > 0 ? t("teams.sharedBy", { name: listNames(names, locale, more) }) : t("teams.sharedWithYou"),
		teams.length > 0 ? t("teams.throughTeam", { team: listNames(teams, locale, more) }) : null,
	].filter((line): line is string => line !== null);
}

/**
 * Marks a project someone else shared with this person, without a pill in
 * the row: a quiet people mark whose tooltip says who shared it and through
 * which team.
 */
export function SharedProjectMark({ share }: { share: WorkspaceShare }) {
	const { t, i18n } = useTranslation();
	const lines = sharedProjectLines(share, t, i18n.language);
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
