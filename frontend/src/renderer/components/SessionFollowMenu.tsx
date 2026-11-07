import { Bell, BellOff, ChevronDown } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useSessionFollow } from "../hooks/useSessionFollow";
import type { CloudCpFollowLevel, CloudCpSessionFollower } from "../lib/cloud-cp/types";
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

/**
 * Three choices: the default (what involves you), everything, or nothing but
 * what is addressed to you. The control plane's narrower "needs you" level
 * stays valid for anyone who already set it; it just isn't offered here.
 */
const LEVELS = [
	{ level: "participating", label: "follow.level.default" },
	{ level: "all", label: "follow.level.everything" },
	{ level: "muted", label: "follow.level.muted" },
] as const satisfies ReadonlyArray<{ level: CloudCpFollowLevel; label: string }>;

function isFollowLevel(value: string): value is CloudCpFollowLevel {
	return LEVELS.some((item) => item.level === value);
}

/**
 * The session header's Following menu for a cloud session: how closely the
 * signed-in person hears about it, and who else does. Shown only on a control
 * plane with team notifications, once the person's level is known.
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
			<DropdownMenuContent align="end" className="w-60">
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
							{t(item.label)}
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
							<li className="min-w-0 truncate px-2 py-1 text-sm text-muted-foreground" key={person.userId}>
								{nameOf(person)}
							</li>
						))}
					</ul>
				)}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
