import { Share2 } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudSessionLive } from "../hooks/useCloudSessionLive";
import { orderSessionPeople, personInitials, SESSION_PEOPLE_SHOWN, sessionPeopleFromLive, type SessionPerson } from "../lib/session-people";
import { cn } from "../lib/utils";
import { ShareSessionDialog } from "./ShareSessionDialog";
import { TopbarButton } from "./TopbarButton";
import { HoverCard, HoverCardContent, HoverCardTrigger } from "./ui/hover-card";

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
					<button
						type="button"
						aria-label={label}
						className="inline-flex items-center rounded-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
						onClick={(event) => {
							event.stopPropagation();
							onActivate();
						}}
						data-session-people
					>
						{circles}
					</button>
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
 * is there.
 */
export function SessionPeopleControl({ orgId, projectId, sessionId, canShare }: {
	orgId: string;
	projectId: string;
	sessionId: string;
	canShare: boolean;
}) {
	const { t } = useTranslation();
	const { userId } = useCloudCp();
	const [open, setOpen] = useState(false);
	const live = useCloudSessionLive(orgId, sessionId, true);
	const me = live?.you || userId;
	const present = live ? sessionPeopleFromLive(live) : [];
	const others = present.filter((person) => person.userId !== me);
	if (others.length === 0 && !canShare) return null;
	return (
		<>
			{others.length > 0 ? (
				<span className="inline-flex px-1.5">
					<SessionPeopleStack people={others} list={present} me={me} onActivate={canShare ? () => setOpen(true) : undefined} ringClassName="ring-background" />
				</span>
			) : (
				<TopbarButton variant="accent" className="topbar-control--labeled" onClick={() => setOpen(true)} data-share-session-button>
					<Share2 className="size-icon-md" aria-hidden="true" />
					<span data-compact-label>{t("share.button")}</span>
				</TopbarButton>
			)}
			{canShare ? <ShareSessionDialog open={open} onOpenChange={setOpen} orgId={orgId} projectId={projectId} sessionId={sessionId} /> : null}
		</>
	);
}
