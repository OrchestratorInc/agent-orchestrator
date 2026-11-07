import { ChevronDown, Link2, Share2 } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudSessionLive } from "../hooks/useCloudSessionLive";
import { useTeamsEnabled } from "../hooks/useTeamsEnabled";
import { aoBridge } from "../lib/bridge";
import { orderSessionPeople, personInitials, SESSION_PEOPLE_SHOWN, sessionPeopleFromLive, type SessionPerson } from "../lib/session-people";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import { canCopy } from "./share/CopyLinkButton";
import { anyoneSentence, useLinkAccess } from "./share/linkAccess";
import { ShareSessionDialog } from "./ShareSessionDialog";
import { TopbarButton } from "./TopbarButton";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "./ui/dropdown-menu";
import { HoverCard, HoverCardContent, HoverCardTrigger } from "./ui/hover-card";
import { Pressable } from "./ui/pressable";

const circleClassName =
	"inline-flex size-6 shrink-0 items-center justify-center rounded-full bg-muted text-micro font-semibold leading-none text-muted-foreground ring-2";

/**
 * The people in a shared session as overlapping circles, at most two then
 * "+N", whoever is writing to the agent ringed in the working color. Hovering
 * lists everyone and what they are doing; with onActivate the circles are a
 * button.
 */
export function SessionPeopleStack({ people, list = people, me, onActivate, ringClassName = "ring-surface" }: {
	/** Who the circles show. */
	people: readonly SessionPerson[];
	/** Who the hover list names; the circles' people by default. */
	list?: readonly SessionPerson[];
	me?: string;
	onActivate?: () => void;
	/** The ring that separates overlapping circles, matched to the surface behind them. */
	ringClassName?: string;
}) {
	const { t } = useTranslation();
	const ordered = orderSessionPeople(people);
	const shown = ordered.slice(0, SESSION_PEOPLE_SHOWN);
	const more = ordered.length - shown.length;
	const listed = orderSessionPeople(list);
	const nameOf = (person: SessionPerson): string => {
		const name = person.name || t("chat.shared.someone");
		return person.userId === me ? t("share.people.you", { name }) : name;
	};
	const label = t("share.people.label", { names: listed.map(nameOf).join(", ") });
	const circles = (
		<>
			{shown.map((person, index) => (
				<span
					key={person.userId}
					className={cn(circleClassName, index > 0 && "-ml-1.5", person.state === "working" ? "ring-status-working" : ringClassName)}
					data-person-state={person.state}
				>
					<SessionPersonAvatar key={person.avatarUrl} person={person} />
				</span>
			))}
			{more > 0 ? (
				<span className={cn(circleClassName, "-ml-1.5 tabular-nums", ringClassName)}>{t("share.people.more", { count: more })}</span>
			) : null}
		</>
	);
	return (
		<HoverCard>
			<HoverCardTrigger asChild>
				{onActivate ? (
					<Pressable
						aria-label={label}
						className="inline-flex items-center rounded-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
						onClick={(event) => {
							event.stopPropagation();
							onActivate();
						}}
						data-session-people
					>
						{circles}
					</Pressable>
				) : (
					<span role="group" aria-label={label} tabIndex={0} className="inline-flex items-center rounded-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60" data-session-people>
						{circles}
					</span>
				)}
			</HoverCardTrigger>
			<HoverCardContent align="end" className="w-60 p-1" onClick={(event) => event.stopPropagation()}>
				<ul className="flex flex-col" data-session-people-list>
					{listed.map((person) => (
						<li key={person.userId} className="flex min-w-0 items-center justify-between gap-3 px-2 py-1.5 text-xs">
							<span className="min-w-0 truncate text-foreground">{nameOf(person)}</span>
							<span className={cn("shrink-0", person.state === "working" ? "text-status-working" : "text-muted-foreground")}>
								{t(`share.people.${person.state}`)}
							</span>
						</li>
					))}
				</ul>
			</HoverCardContent>
		</HoverCard>
	);
}

/** The sign-in photo, with initials when it is absent or cannot load. */
function SessionPersonAvatar({ person }: { person: SessionPerson }) {
	const [broken, setBroken] = useState(false);
	return person.avatarUrl && !broken ? (
		<img
			src={person.avatarUrl}
			alt=""
			className="size-full rounded-full object-cover"
			referrerPolicy="no-referrer"
			onError={() => setBroken(true)}
		/>
	) : personInitials(person.name);
}

/**
 * The session header's sharing control. While someone else has the session
 * open it shows who is there as circles; otherwise the owner sees a Share
 * button. Either opens the share dialog for the owner; a guest only sees who
 * is there. Beside it, the owner's menu copies the session's link without
 * opening the dialog.
 */
export function SessionPeopleControl({ orgId, projectId, sessionId, canShare, sessionName, projectName }: {
	orgId: string;
	projectId: string;
	sessionId: string;
	canShare: boolean;
	sessionName?: string;
	projectName?: string;
}) {
	const { t } = useTranslation();
	const { userId } = useCloudCp();
	const teams = useTeamsEnabled();
	const [open, setOpen] = useState(false);
	const [menuOpen, setMenuOpen] = useState(false);
	const live = useCloudSessionLive(orgId, sessionId, true);
	const me = live?.you || userId;
	const present = live ? sessionPeopleFromLive(live) : [];
	const others = present.filter((person) => person.userId !== me);
	if (others.length === 0 && !canShare) return null;
	const trigger = others.length > 0 ? (
		<span className="inline-flex px-1.5">
			<SessionPeopleStack people={others} list={present} me={me} onActivate={canShare ? () => setOpen(true) : undefined} ringClassName="ring-background" />
		</span>
	) : (
		<TopbarButton variant="accent" className="topbar-control--labeled rounded-r-none" onClick={() => setOpen(true)} data-share-session-button>
			<Share2 className="size-icon-md" aria-hidden="true" />
			<span data-compact-label>{t("share.button")}</span>
		</TopbarButton>
	);
	return (
		<>
			{canShare ? (
				<span className="inline-flex items-center" data-share-split="">
					{trigger}
					<ShareMenu
						orgId={orgId}
						projectId={projectId}
						sessionId={sessionId}
						open={menuOpen}
						onOpenChange={setMenuOpen}
						onManage={() => setOpen(true)}
						joined={others.length === 0}
					/>
				</span>
			) : trigger}
			{canShare ? (
				<ShareSessionDialog
					open={open}
					onOpenChange={setOpen}
					orgId={orgId}
					projectId={projectId}
					sessionId={sessionId}
					sessionName={sessionName}
					projectName={projectName}
					people={teams}
				/>
			) : null}
		</>
	);
}

/**
 * The chevron beside Share: Copy link and Share…, as Google Docs' Share
 * button has. Copy link copies the link this device made, once the links are
 * read again: the list it opened with may hold one revoked elsewhere since,
 * and then (or when the read fails) it opens the Share dialog instead. One
 * made on another device can't be shown again, and a new one is a new
 * credential, so that is made in the Share dialog, where its access and
 * expiry show: the item opens it. With link access off there is nothing to
 * copy, so it says so, or that only the whole project's link is on. The
 * toast names who the link lets in.
 */
function ShareMenu({ orgId, projectId, sessionId, open, onOpenChange, onManage, joined }: {
	orgId: string;
	projectId: string;
	sessionId: string;
	open: boolean;
	onOpenChange: (open: boolean) => void;
	onManage: () => void;
	/** Joined to the Share button's edge, rather than standing beside the people circles. */
	joined: boolean;
}) {
	const { t } = useTranslation();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const access = useLinkAccess({ orgId, projectId, sessionId, scope: "session", enabled: open });
	const { mode } = access.copy;
	const off = mode === "off";
	// This session's own link access is off, but a link to the whole project opens it.
	const viaProject = off && access.projectLinks.length > 0;
	// Made on another device: the dialog makes a new one, never this menu.
	const elsewhere = mode === "new" || mode === "replace";
	const copy = async () => {
		const fresh = access.url ? await access.freshUrl() : undefined;
		if (!fresh) {
			onManage();
			return;
		}
		const { url } = fresh;
		const sentence = anyoneSentence(t, "session", fresh.link.role);
		try {
			await aoBridge.clipboard.writeText(url);
		} catch {
			showGlobalToast(t("share.copyFailedTitle"), url, { tone: "error" });
			return;
		}
		showGlobalToast(t("share.linkCopied"), sentence, { action: { label: t("share.manage"), run: onManage } });
	};
	return (
		<DropdownMenu open={open} onOpenChange={onOpenChange} modal={false}>
			<DropdownMenuTrigger asChild>
				<TopbarButton
					variant={joined ? "accent" : "icon"}
					className={cn(joined ? "-ml-px rounded-l-none px-1.5" : "")}
					aria-label={t("share.menu")}
					data-share-menu=""
				>
					<ChevronDown className="size-icon-sm" aria-hidden="true" />
				</TopbarButton>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="w-64">
				<DropdownMenuItem
					disabled={!canCopy(access)}
					onSelect={() => void copy()}
					className="items-start py-1.5"
					data-share-menu-copy={mode}
				>
					<Link2 className="mt-0.5" aria-hidden="true" />
					<span className="flex min-w-0 flex-1 flex-col">
						<span>{elsewhere ? t("share.copyNewLinkEllipsis") : t("share.copyLink")}</span>
						{off ? <span className="text-xs text-muted-foreground">{viaProject ? t("share.menuProjectLinkOn") : t("share.menuLinkOff")}</span> : null}
						{elsewhere ? <span className="text-xs text-muted-foreground">{t("share.menuLinkElsewhere")}</span> : null}
					</span>
				</DropdownMenuItem>
				<DropdownMenuItem onSelect={onManage} data-share-menu-manage="">
					<Share2 aria-hidden="true" />
					<span>{t("share.shareEllipsis")}</span>
				</DropdownMenuItem>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
