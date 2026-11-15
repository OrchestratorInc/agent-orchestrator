import { Share2 } from "@/components/app-icons";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudSessionLive } from "../hooks/useCloudSessionLive";
import { useTeamsEnabled } from "../hooks/useTeamsEnabled";
import { orderSessionPeople, personInitials, SESSION_PEOPLE_SHOWN, sessionPeopleFromLive, type SessionPerson } from "../lib/session-people";
import { cn } from "../lib/utils";
import { ShareSessionDialog } from "./ShareSessionDialog";
import { Button } from "./ui/button";
import { HoverCard, HoverCardContent, HoverCardTrigger } from "./ui/hover-card";
import { Pressable } from "./ui/pressable";

const circleClassName =
	"inline-flex size-6 shrink-0 items-center justify-center rounded-full bg-muted text-micro font-semibold leading-none text-muted-foreground ring-2";

export function SessionPeopleStack({ people, list = people, me, onActivate, ringClassName = "ring-surface" }: {
	people: readonly SessionPerson[];
	list?: readonly SessionPerson[];
	me?: string;
	onActivate?: () => void;
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

export function SessionPeopleControl({ orgId, projectId, sessionId, canShare, sessionName, projectName, standaloneAgent, shareProject = false }: {
	orgId: string;
	projectId: string;
	sessionId: string;
	canShare: boolean;
	sessionName?: string;
	projectName?: string;
	standaloneAgent?: boolean;
	shareProject?: boolean;
}) {
	const { t } = useTranslation();
	const { userId } = useCloudCp();
	const teams = useTeamsEnabled();
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
				<Button type="button" size="sm" variant="secondary" onClick={() => setOpen(true)} data-share-session-button>
					<Share2 className="size-icon-md" aria-hidden="true" />
					<span data-compact-label>{t("share.button")}</span>
				</Button>
			)}
			{canShare ? (
				<ShareSessionDialog
					open={open}
					onOpenChange={setOpen}
					orgId={orgId}
					projectId={projectId}
					sessionId={shareProject ? undefined : sessionId}
					sessionName={shareProject ? undefined : sessionName}
					projectName={projectName}
					people={teams}
					standaloneAgent={standaloneAgent}
				/>
			) : null}
		</>
	);
}
