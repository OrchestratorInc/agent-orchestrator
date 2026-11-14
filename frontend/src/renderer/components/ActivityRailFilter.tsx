import { ListFilter } from "lucide-react";
import { useTranslation } from "react-i18next";
import { NO_RAIL_FILTER, RAIL_GROUPS, railFilterActive, type RailFilter, type RailGroup } from "../lib/activity-rail";
import { cn } from "../lib/utils";
import { DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "./ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

const groupLabelKey = {
	talk: "inspector.rail.filterGroup.talk",
	commits: "inspector.rail.filterGroup.commits",
	pr: "inspector.rail.filterGroup.pr",
	session: "inspector.rail.filterGroup.session",
} as const satisfies Record<RailGroup, string>;

/**
 * The activity rail's filter: a menu that ticks which kinds of rows show, and
 * whether bots do. Each kind says how many rows it has; one with none is left
 * out. A dot on the button says something is hidden.
 */
export function ActivityRailFilter({ counts, filter, onChange }: {
	counts: Record<RailGroup, number>;
	filter: RailFilter;
	onChange: (filter: RailFilter) => void;
}) {
	const { t } = useTranslation();
	const active = railFilterActive(filter);
	const toggle = (group: RailGroup, shown: boolean) =>
		onChange({ ...filter, hidden: shown ? filter.hidden.filter((key) => key !== group) : [...filter.hidden, group] });
	return (
		<DropdownMenu>
			<Tooltip>
				<TooltipTrigger asChild>
					<DropdownMenuTrigger asChild>
						<button
							aria-label={t("inspector.rail.filter")}
							className={cn(
								"relative flex size-6 shrink-0 items-center justify-center rounded-md text-passive transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60 data-[state=open]:bg-muted data-[state=open]:text-foreground",
								active && "text-foreground",
							)}
							data-filtered={active ? "true" : undefined}
							data-testid="activity-rail-filter"
							type="button"
						>
							<ListFilter aria-hidden="true" className="size-icon-sm" />
							{active ? <span aria-hidden="true" className="absolute top-0.5 right-0.5 size-1.5 rounded-full bg-status-in-review" /> : null}
						</button>
					</DropdownMenuTrigger>
				</TooltipTrigger>
				<TooltipContent>{t("inspector.rail.filter")}</TooltipContent>
			</Tooltip>
			<DropdownMenuContent align="end" className="w-60 max-w-[calc(100vw-2rem)]">
				<DropdownMenuLabel className="text-micro font-medium text-muted-foreground">{t("inspector.rail.filterTitle")}</DropdownMenuLabel>
				{RAIL_GROUPS.filter((group) => counts[group] > 0).map((group) => (
					<DropdownMenuCheckboxItem
						checked={!filter.hidden.includes(group)}
						key={group}
						onCheckedChange={(checked) => toggle(group, checked === true)}
						// Several kinds are ticked in one visit, so the menu stays open.
						onSelect={(event) => event.preventDefault()}
					>
						<span className="min-w-0 flex-1 truncate">{t(groupLabelKey[group])}</span>
						<span className="text-passive tabular-nums">{counts[group]}</span>
					</DropdownMenuCheckboxItem>
				))}
				<DropdownMenuSeparator />
				<DropdownMenuCheckboxItem checked={filter.hideBots} onCheckedChange={(checked) => onChange({ ...filter, hideBots: checked === true })} onSelect={(event) => event.preventDefault()}>
					{t("inspector.rail.filterHideBots")}
				</DropdownMenuCheckboxItem>
				<DropdownMenuSeparator />
				<DropdownMenuItem disabled={!active} onSelect={() => onChange(NO_RAIL_FILTER)}>{t("inspector.rail.filterShowAll")}</DropdownMenuItem>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
