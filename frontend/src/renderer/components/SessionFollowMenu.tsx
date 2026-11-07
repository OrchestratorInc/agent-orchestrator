import { Bell, BellOff, ChevronDown } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useSessionFollow } from "../hooks/useSessionFollow";
import type { CloudCpFollowLevel, CloudCpFollowReason, CloudCpSessionFollower } from "../lib/cloud-cp/types";
import { TopbarButton } from "./TopbarButton";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuLabel,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";

const LEVELS = [
	{ level: "needs_you", label: "follow.level.needsYou", description: "follow.level.needsYouDescription" },
	{ level: "participating", label: "follow.level.participating", description: "follow.level.participatingDescription" },
	{ level: "all", label: "follow.level.all", description: "follow.level.allDescription" },
	{ level: "muted", label: "follow.level.muted", description: "follow.level.mutedDescription" },
] as const satisfies ReadonlyArray<{ level: CloudCpFollowLevel; label: string; description: string }>;

const REASON_KEYS = {
	creator: "follow.reason.creator",
	prompted: "follow.reason.prompted",
	asided: "follow.reason.asided",
	mentioned: "follow.reason.mentioned",
	handoff: "follow.reason.handoff",
	reviewer: "follow.reason.reviewer",
	manual: "follow.reason.manual",
} as const satisfies Record<CloudCpFollowReason, string>;

function isFollowLevel(value: string): value is CloudCpFollowLevel {
	return LEVELS.some((item) => item.level === value);
}

/**
 * The session header's Following menu for a cloud session: how closely the
 * signed-in person hears about it, and who else does and why. Shown only on a
 * control plane with team notifications, once the person's level is known.
 */
export function SessionFollowMenu({ orgId, sessionId }: { orgId: string; sessionId: string }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const follow = useSessionFollow(orgId, sessionId, { followers: open });
	if (!follow.available || !follow.follow) return null;
	const level = follow.follow.level;
	const label = level === "" ? t("follow.follow") : level === "muted" ? t("follow.muted") : t("follow.following");
	const Icon = level === "muted" ? BellOff : Bell;
	const nameOf = (person: CloudCpSessionFollower) =>
		person.userId === follow.me ? t("follow.you") : person.name || t("chat.shared.someone");
	const reasonsOf = (person: CloudCpSessionFollower) =>
		person.reasons.map((reason) => t(REASON_KEYS[reason])).join(", ");
	return (
		<DropdownMenu open={open} onOpenChange={setOpen}>
			<DropdownMenuTrigger asChild>
				<TopbarButton
					aria-label={t("follow.menuLabel", { level: label })}
					className="topbar-control--labeled"
					data-session-follow={level || "none"}
					variant="action"
				>
					<Icon className="size-icon-md" aria-hidden="true" />
					<span data-compact-label>{label}</span>
					<ChevronDown className="size-icon-sm" aria-hidden="true" />
				</TopbarButton>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="w-80">
				<DropdownMenuLabel>{t("follow.menuTitle")}</DropdownMenuLabel>
				<DropdownMenuRadioGroup
					value={level}
					onValueChange={(next) => {
						if (!isFollowLevel(next) || next === level) return;
						void follow.setLevel(next).catch(() => undefined);
					}}
				>
					{LEVELS.map((item) => (
						<DropdownMenuRadioItem disabled={follow.saving} key={item.level} value={item.level}>
							<span className="flex min-w-0 flex-col">
								<span>{t(item.label)}</span>
								<span className="text-xs text-muted-foreground">{t(item.description)}</span>
							</span>
						</DropdownMenuRadioItem>
					))}
				</DropdownMenuRadioGroup>
				{follow.saveError ? <p className="px-2 py-1 text-caption text-error" role="alert">{t("follow.saveFailed")}</p> : null}
				<DropdownMenuSeparator />
				<DropdownMenuLabel>
					{follow.followers ? t("follow.whoCount", { count: follow.followers.length }) : t("follow.who")}
				</DropdownMenuLabel>
				{follow.followersError ? (
					<p className="px-2 py-1 text-caption text-error">{t("follow.loadFailed")}</p>
				) : follow.followersLoading ? (
					<p className="px-2 py-1 text-caption text-muted-foreground">{t("follow.loading")}</p>
				) : (
					<ul className="flex flex-col" data-session-followers>
						{(follow.followers ?? []).map((person) => (
							<li className="flex min-w-0 items-center justify-between gap-3 px-2 py-1.5 text-sm" key={person.userId}>
								<span className="min-w-0 truncate text-muted-foreground">{nameOf(person)}</span>
								<span className="shrink-0 text-xs text-muted-foreground">{reasonsOf(person)}</span>
							</li>
						))}
					</ul>
				)}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
