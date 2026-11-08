import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, Folder, LogOut, MessageSquare, MoreHorizontal, Pencil, Plus, Trash2, UserPlus, Users } from "lucide-react";
import { type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { cn } from "../../lib/utils";
import { useCloudCp } from "../../hooks/useCloudCp";
import { useCloudOrg } from "../../hooks/useCloudOrg";
import {
	invalidateTeamsAndShares,
	myTeamsQueryKey,
	retryCloudRead,
	teamImpactQueryKey,
	teamQueryKey,
	teamsQueryKey,
	teamsUnavailable,
	useMyTeams,
	useTeam,
	useTeams,
} from "../../hooks/useTeams";
import type { CloudCpShareEntry, CloudCpTeam, CloudCpTeamDetail, CloudCpTeamImpact, CloudCpTeamMember } from "../../lib/cloud-cp";
import { ConfirmDialog } from "../ConfirmDialog";
import { AccessMenu } from "../share/AccessMenu";
import { defaultCap, listNames, roleLabel } from "../share/accessLabels";
import { UndoNotice, useUndoNotice } from "../share/UndoNotice";
import { NewTeamDialog, teamErrorMessage } from "../teams/NewTeamDialog";
import { PeopleField, recipientKey, type Recipient } from "../teams/PeopleField";
import { PersonAvatar, TeamTile } from "../teams/TeamVisuals";
import { Button } from "../ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { Input } from "../ui/input";
import { Skeleton } from "../ui/skeleton";
import { SettingsOptionMenu } from "./SettingsOptionMenu";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";
import { Pressable } from "../ui/pressable";

type OpenTeam = { orgId: string; teamId: string; external: boolean };

const MEMBERS_SHOWN = 50;

/** "4 sessions and 1 project", in the person's language. */
export function lostText(t: TFunction, impact: Pick<CloudCpTeamImpact, "sessions" | "projects">, locale: string): string {
	const parts: string[] = [];
	if (impact.sessions) parts.push(t("teams.count.sessions", { count: impact.sessions }));
	if (impact.projects) parts.push(t("teams.count.projects", { count: impact.projects }));
	return listNames(parts, locale, (count) => t("share.summary.more", { count }));
}

/**
 * Settings › Teams: the workspace's teams and the ones the person is in
 * elsewhere, and a page per team for its people and what's shared with it.
 * `manage` is off in a workspace that can't share: then only the teams the
 * person is in elsewhere show, to open or leave.
 */
export function TeamsSection({ titleHidden, manage = true }: { titleHidden: boolean; manage?: boolean }) {
	const { t } = useTranslation();
	const { ready } = useCloudCp();
	const { org, error, refetch } = useCloudOrg();
	const [openTeam, setOpenTeam] = useState<OpenTeam | null>(null);
	// The row a team page was opened from, focused again on the way back.
	const [returnTo, setReturnTo] = useState<string | null>(null);
	// Lives above the page and the list, so Back keeps the offer in Settings.
	const undoNotice = useUndoNotice(true);
	const notice = (
		<UndoNotice
			notice={undoNotice.shown}
			onUndo={undoNotice.undo}
			onDismiss={undoNotice.dismiss}
			className="sticky bottom-3 z-10 mx-(--settings-row-inset)"
		/>
	);
	if (!ready) return <TeamsNotice titleHidden={titleHidden} text={t("teams.signIn")} />;
	if (!org) {
		if (error) {
			return (
				<TeamsNotice titleHidden={titleHidden} text={teamErrorMessage(error, t("teams.loadFailed"))} tone="error">
					<Button variant="ghost" size="sm" className="text-settings-muted hover:text-settings-label" onClick={() => void refetch()}>{t("teams.retry")}</Button>
				</TeamsNotice>
			);
		}
		return <TeamsSkeleton />;
	}
	if (openTeam) {
		return (
			<>
				<TeamPage
					key={openTeam.teamId}
					team={openTeam}
					undoNotice={undoNotice}
					onBack={() => {
						setReturnTo(openTeam.teamId);
						setOpenTeam(null);
					}}
				/>
				{notice}
			</>
		);
	}
	return (
		<>
			<TeamsList orgId={org.id} manage={manage} titleHidden={titleHidden} focusTeamId={returnTo} onOpen={setOpenTeam} />
			{notice}
		</>
	);
}

function TeamsNotice({ titleHidden, text, tone, children }: { titleHidden: boolean; text: string; tone?: "error"; children?: ReactNode }) {
	const { t } = useTranslation();
	return (
		<SettingsSection title={t("teams.title")} sectionId="teams" titleHidden={titleHidden} grouped>
			<div className="settings-row-bar">
				<p role={tone === "error" ? "alert" : undefined} className={cn("text-sm", tone === "error" ? "text-xs text-error" : "text-settings-muted")} data-teams-notice="">{text}</p>
				{children}
			</div>
		</SettingsSection>
	);
}

function TeamsSkeleton() {
	return (
		<div className="settings-grouped-rows flex w-full flex-col" aria-busy="true">
			{[0, 1, 2].map((row) => (
				<div key={row} className="settings-row-bar flex items-center gap-3">
					<Skeleton className="size-7 rounded-[7px]" />
					<Skeleton className="h-3 w-48 rounded" />
				</div>
			))}
		</div>
	);
}

function teamSubtitle(t: TFunction, team: CloudCpTeam, locale: string): string {
	const people = t("teams.people", { count: team.memberCount });
	const shared = lostText(t, { sessions: team.sharedSessions, projects: team.sharedProjects }, locale);
	return shared ? t("teams.subtitleShared", { people, shared }) : people;
}

function TeamsList({ orgId, manage, titleHidden, focusTeamId, onOpen }: {
	orgId: string;
	manage: boolean;
	titleHidden: boolean;
	focusTeamId: string | null;
	onOpen: (team: OpenTeam) => void;
}) {
	const { t, i18n } = useTranslation();
	const teams = useTeams(orgId, manage);
	const mine = useMyTeams();
	const [creating, setCreating] = useState(false);
	const elsewhere = (mine.data ?? []).filter((team) => team.orgId !== orgId);
	// Both lists settle before anything shows, so "No teams yet" never flashes
	// and "Teams you're in" doesn't pop in under the workspace's own.
	const loading = (manage && teams.isPending) || mine.isPending;
	const unavailable = teamsUnavailable(teams.error) || teamsUnavailable(mine.error);
	useEffect(() => {
		if (loading || !focusTeamId) return;
		const rows = [...document.querySelectorAll<HTMLElement>("[data-team-row]")];
		(rows.find((row) => row.dataset.teamRow === focusTeamId) ?? document.querySelector<HTMLElement>("[data-teams-new]"))?.focus();
	}, [focusTeamId, loading]);
	const newTeamButton = (
		<Button type="button" variant="primary" size="sm" className="gap-1.5" onClick={() => setCreating(true)} data-teams-new="">
			<Plus className="size-3.5" aria-hidden="true" />
			{t("teams.newTeam")}
		</Button>
	);
	const dialog = creating ? (
		<NewTeamDialog
			orgId={orgId}
			open
			onOpenChange={setCreating}
			onCreated={(team) => {
				setCreating(false);
				onOpen({ orgId, teamId: team.team.id, external: false });
			}}
		/>
	) : null;
	// A control plane from before teams: nothing to show, and nothing wrong.
	if (unavailable) return <TeamsNotice titleHidden={titleHidden} text={t("teams.unavailable")} />;
	if (loading) return <TeamsSkeleton />;
	if (manage && teams.error) {
		return (
			<SettingsSection title={t("teams.yourTeams")} titleHidden={titleHidden}>
				<p role="alert" className="px-(--settings-row-inset) text-control text-error">{teamErrorMessage(teams.error, t("teams.loadFailed"))}</p>
			</SettingsSection>
		);
	}
	const own = teams.data ?? [];
	const elsewhereSection = elsewhere.length > 0 ? (
		<SettingsSection title={t("teams.teamsYoureIn")} grouped>
			{elsewhere.map((team) => (
				<TeamListRow
					key={team.id}
					team={team}
					subtitle={team.orgName ? `${team.orgName} · ${t("teams.people", { count: team.memberCount })}` : t("teams.people", { count: team.memberCount })}
					onOpen={() => onOpen({ orgId: team.orgId, teamId: team.id, external: true })}
				/>
			))}
		</SettingsSection>
	) : null;
	if (!manage) {
		return elsewhereSection ?? <TeamsNotice titleHidden={titleHidden} text={t("teams.unavailable")} />;
	}
	if (own.length === 0 && elsewhere.length === 0) {
		return (
			<>
				<div className="flex flex-col items-center gap-2 py-16 text-center" data-teams-empty="">
					<span className="grid size-10 place-items-center rounded-[9px] bg-foreground/[0.11] text-foreground/80">
						<Users className="size-4.5" aria-hidden="true" />
					</span>
					<h3 className="mt-2 text-heading-sm font-semibold text-foreground">{t("teams.emptyTitle")}</h3>
					<p className="mb-3 text-control text-muted-foreground">{t("teams.emptyBody")}</p>
					{newTeamButton}
				</div>
				{dialog}
			</>
		);
	}
	return (
		<>
			<SettingsSection title={t("teams.yourTeams")} sectionId="teams" grouped action={newTeamButton}>
				{own.length === 0 ? (
					<div className="settings-content-row text-control text-muted-foreground">{t("teams.noneHere")}</div>
				) : own.map((team) => (
					<TeamListRow key={team.id} team={team} subtitle={teamSubtitle(t, team, i18n.language)} onOpen={() => onOpen({ orgId, teamId: team.id, external: false })} />
				))}
			</SettingsSection>
			{elsewhereSection}
			{dialog}
		</>
	);
}

function TeamListRow({ team, subtitle, onOpen }: { team: CloudCpTeam; subtitle: string; onOpen: () => void }) {
	const { t } = useTranslation();
	return (
		<Pressable
			className="settings-row-bar flex w-full items-center gap-3 transition-colors duration-(--duration-fast) hover:bg-(--color-bg-settings-row-hover) focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring"
			onClick={onOpen}
			data-team-row={team.id}
		>
			<TeamTile name={team.name} size="lg" />
			<span className="min-w-0 flex-1">
				<span className="block truncate text-sm leading-5 text-settings-label">{team.name}</span>
				<span className="block truncate text-control leading-4 text-settings-muted">{subtitle}</span>
			</span>
			{team.yourRole ? <span className="shrink-0 text-control text-settings-muted">{team.yourRole === "owner" ? t("teams.roleOwner") : t("teams.roleMember")}</span> : null}
			<ChevronRight className="size-icon-md shrink-0 text-passive" aria-hidden="true" />
		</Pressable>
	);
}

type Confirming =
	| { kind: "remove"; member: CloudCpTeamMember }
	| { kind: "delete" }
	| { kind: "leave" };

type ShareChange = { entry: CloudCpShareEntry; role: "viewer" | "editor"; modeCap: "read-only" | "standard" | "trusted" };

/** The same share in a team's list: one per session, or the whole project. */
function sameShare(a: CloudCpShareEntry, b: CloudCpShareEntry): boolean {
	return a.projectId === b.projectId && a.scope === b.scope && (a.sessionId ?? "") === (b.sessionId ?? "");
}

function TeamPage({ team, onBack, undoNotice }: { team: OpenTeam; onBack: () => void; undoNotice: ReturnType<typeof useUndoNotice> }) {
	const { t, i18n } = useTranslation();
	const { client, baseUrl, ready } = useCloudCp();
	const queryClient = useQueryClient();
	const detail = useTeam(team.orgId, team.teamId, team.external);
	const [adding, setAdding] = useState<Recipient[]>([]);
	const [renaming, setRenaming] = useState<string | null>(null);
	const [confirming, setConfirming] = useState<Confirming | null>(null);
	const [showAll, setShowAll] = useState(false);
	const backRef = useRef<HTMLButtonElement>(null);
	const teamKey = teamQueryKey(baseUrl, team.orgId, team.teamId, team.external);
	const teamChange = ["team-change", team.teamId] as const;
	// Refetches once the last change in flight settles, so a later change isn't undone by an earlier one's read.
	const settle = () => {
		if (queryClient.isMutating({ mutationKey: teamChange }) <= 1) invalidateTeamsAndShares(queryClient);
	};
	const setDetail = (update: (data: CloudCpTeamDetail) => CloudCpTeamDetail) =>
		queryClient.setQueryData<CloudCpTeamDetail>(teamKey, (data) => (data ? update(data) : data));
	// The team as the lists show it: its name and size change there at once too.
	const setListed = (update: (listed: CloudCpTeam) => CloudCpTeam | null) => {
		for (const key of [teamsQueryKey(baseUrl, team.orgId), myTeamsQueryKey(baseUrl)]) {
			queryClient.setQueryData<CloudCpTeam[]>(key, (list) => list?.flatMap((listed) => {
				if (listed.id !== team.teamId) return [listed];
				const next = update(listed);
				return next ? [next] : [];
			}));
		}
	};
	const applyDetail = (next: CloudCpTeamDetail | undefined) => {
		if (!next?.team) return;
		queryClient.setQueryData<CloudCpTeamDetail>(teamKey, next);
		setListed((listed) => ({ ...listed, name: next.team.name, memberCount: next.team.memberCount }));
	};

	// Opening a page puts focus on its way back, so the keyboard starts here.
	useEffect(() => {
		backRef.current?.focus();
	}, []);

	const add = useMutation({
		mutationKey: teamChange,
		mutationFn: (people: Recipient[]) =>
			client.addTeamMembers(team.orgId, team.teamId, people.filter((recipient): recipient is Extract<Recipient, { kind: "person" }> => recipient.kind === "person").map((recipient) => ({ email: recipient.email }))),
		onSuccess: (next, people) => {
			applyDetail(next);
			// Only who was sent: anyone picked while it was on its way stays.
			const sent = new Set(people.map(recipientKey));
			setAdding((current) => current.filter((recipient) => !sent.has(recipientKey(recipient))));
		},
		onSettled: settle,
	});
	const rename = useMutation({
		mutationKey: teamChange,
		mutationFn: (name: string) => client.renameTeam(team.orgId, team.teamId, name),
		onMutate: async (name) => {
			await queryClient.cancelQueries({ queryKey: teamKey });
			const before = queryClient.getQueryData<CloudCpTeamDetail>(teamKey)?.team.name;
			setDetail((data) => ({ ...data, team: { ...data.team, name } }));
			setListed((listed) => ({ ...listed, name }));
			return { before };
		},
		onSuccess: applyDetail,
		onError: (_error, name, context) => {
			const before = context?.before;
			if (before !== undefined) {
				setDetail((data) => ({ ...data, team: { ...data.team, name: before } }));
				setListed((listed) => ({ ...listed, name: before }));
			}
			// Back to the field with what was typed, next to the error.
			setRenaming(name);
		},
		onSettled: settle,
	});
	const changeRole = useMutation({
		mutationKey: teamChange,
		mutationFn: ({ member, role }: { member: CloudCpTeamMember; role: "owner" | "member" }) => client.updateTeamMember(team.orgId, team.teamId, member.id, role),
		onMutate: async ({ member, role }) => {
			await queryClient.cancelQueries({ queryKey: teamKey });
			setDetail((data) => ({ ...data, members: data.members.map((other) => (other.id === member.id ? { ...other, role } : other)) }));
		},
		onSuccess: applyDetail,
		onError: (_error, { member }) => {
			setDetail((data) => ({ ...data, members: data.members.map((other) => (other.id === member.id ? { ...other, role: member.role } : other)) }));
		},
		onSettled: settle,
	});
	const changeShare = useMutation({
		mutationKey: teamChange,
		mutationFn: ({ entry, role, modeCap }: ShareChange) => client.updateShareEntry(team.orgId, entry.projectId, entry.grantId, { role, modeCap }),
		onMutate: async ({ entry, role, modeCap }) => {
			await queryClient.cancelQueries({ queryKey: teamKey });
			setDetail((data) => ({ ...data, shares: data.shares.map((other) => (other.grantId === entry.grantId ? { ...other, role, modeCap } : other)) }));
		},
		onError: (_error, { entry }) => {
			setDetail((data) => ({ ...data, shares: data.shares.map((other) => (other.grantId === entry.grantId ? entry : other)) }));
		},
		onSettled: settle,
	});
	const restoreShare = useMutation({
		mutationKey: teamChange,
		mutationFn: ({ entry }: { entry: CloudCpShareEntry; index: number }) =>
			client.createShareGrants(team.orgId, entry.projectId, {
				...(entry.sessionId ? { sessionId: entry.sessionId } : {}),
				subjects: [{ teamId: team.teamId }],
				role: entry.role,
				modeCap: entry.modeCap ?? defaultCap(entry.role),
				...(entry.deniedCommands?.length ? { deniedCommands: entry.deniedCommands } : {}),
			}),
		// Back in the list at once; the new grant takes its place when it exists.
		onMutate: async ({ entry, index }) => {
			await queryClient.cancelQueries({ queryKey: teamKey });
			setDetail((data) => {
				if (data.shares.some((other) => sameShare(other, entry))) return data;
				const shares = [...data.shares];
				shares.splice(Math.min(Math.max(index, 0), shares.length), 0, entry);
				return { ...data, shares };
			});
		},
		onSuccess: ({ entries }, { entry }) => {
			const made = entries.find((other) => sameShare(other, entry));
			if (made) setDetail((data) => ({ ...data, shares: data.shares.map((other) => (other.grantId === entry.grantId ? { ...entry, ...made } : other)) }));
		},
		onError: (_error, { entry }) => {
			setDetail((data) => ({ ...data, shares: data.shares.filter((other) => other.grantId !== entry.grantId) }));
		},
		onSettled: settle,
	});
	const stopShare = useMutation({
		mutationKey: teamChange,
		mutationFn: (entry: CloudCpShareEntry) => client.revokeShareGrant(team.orgId, entry.projectId, entry.grantId),
		onMutate: async (entry) => {
			await queryClient.cancelQueries({ queryKey: teamKey });
			const index = queryClient.getQueryData<CloudCpTeamDetail>(teamKey)?.shares.findIndex((other) => other.grantId === entry.grantId) ?? -1;
			setDetail((data) => ({ ...data, shares: data.shares.filter((other) => other.grantId !== entry.grantId) }));
			return { index };
		},
		onError: (_error, entry, context) => {
			setDetail((data) => {
				if (data.shares.some((other) => other.grantId === entry.grantId)) return data;
				const shares = [...data.shares];
				shares.splice(Math.min(Math.max(context?.index ?? 0, 0), shares.length), 0, entry);
				return { ...data, shares };
			});
		},
		onSuccess: (_result, entry, context) => {
			undoNotice.show({
				message: t("share.stoppedWithTeam", { name: entry.sessionName || entry.projectName, team: queryClient.getQueryData<CloudCpTeamDetail>(teamKey)?.team.name ?? "" }),
				undo: () => restoreShare.mutateAsync({ entry, index: context?.index ?? -1 }),
			});
		},
		onSettled: settle,
	});
	const impact = useQuery({
		queryKey: teamImpactQueryKey(baseUrl, team.orgId, team.teamId, confirming?.kind ?? "", confirming?.kind === "remove" ? confirming.member.id : ""),
		enabled: ready && confirming !== null,
		staleTime: 0,
		retry: retryCloudRead,
		queryFn: () => {
			if (!confirming) throw new Error("no change");
			if (confirming.kind === "remove") return client.teamMemberImpact(team.orgId, team.teamId, confirming.member.id);
			if (confirming.kind === "leave") return client.leaveTeamImpact(team.teamId);
			return client.teamImpact(team.orgId, team.teamId);
		},
	});
	const confirm = useMutation({
		mutationKey: teamChange,
		mutationFn: async (change: Confirming) => {
			if (change.kind === "remove") await client.removeTeamMember(team.orgId, team.teamId, change.member.id);
			else if (change.kind === "leave") await client.leaveTeam(team.teamId);
			else await client.deleteTeam(team.orgId, team.teamId);
		},
		onSuccess: (_result, change) => {
			setConfirming(null);
			if (change.kind === "remove") {
				setDetail((data) => ({ ...data, team: { ...data.team, memberCount: Math.max(0, data.team.memberCount - 1) }, members: data.members.filter((other) => other.id !== change.member.id) }));
				setListed((listed) => ({ ...listed, memberCount: Math.max(0, listed.memberCount - 1) }));
			} else {
				// Gone from the lists before going back to them, not after a round trip.
				setListed(() => null);
				onBack();
			}
		},
		onSettled: settle,
	});

	const back = (
		<Button
			ref={backRef}
			type="button"
			variant="quiet"
			size="none"
			className="-ml-1 h-6 gap-1 self-start px-1 text-control [&_svg]:size-3.5"
			onClick={onBack}
			data-team-back=""
		>
			<ChevronLeft aria-hidden="true" />
			{t("teams.title")}
		</Button>
	);
	const data = detail.data;
	if (!data) {
		return (
			<div className="flex flex-col gap-4" data-team-page={team.teamId}>
				<div className="flex flex-col gap-2 px-(--settings-row-inset)">
					{back}
					{detail.isLoading ? null : <p role="alert" className="text-control text-error">{teamErrorMessage(detail.error, t("teams.loadFailed"))}</p>}
				</div>
				{detail.isLoading ? <TeamsSkeleton /> : null}
			</div>
		);
	}
	const manage = data.team.canManage;
	const owners = data.members.filter((member) => member.role === "owner").length;
	const members = showAll ? data.members : data.members.slice(0, MEMBERS_SHOWN);
	const hidden = data.members.length - members.length;
	const inTeam = new Set(data.members.flatMap((member) => (member.email ? [`email:${member.email.toLowerCase()}`] : [])));
	const shareError = changeShare.error ?? stopShare.error;

	return (
		<div className="flex flex-col gap-(--size-settings-section-gap,24px)" data-team-page={data.team.id}>
			<div className="flex flex-col gap-2 px-(--settings-row-inset)">
				{back}
				<div className="flex items-center gap-3">
					<TeamTile name={data.team.name} size="xl" />
					{renaming !== null ? (
						<form
							className="flex min-w-0 flex-1 items-center gap-2"
							onSubmit={(event) => {
								event.preventDefault();
								const name = renaming.trim();
								if (!name) return;
								// The new name shows at once; a failure brings the field back.
								setRenaming(null);
								if (name !== data.team.name) rename.mutate(name);
							}}
						>
							<Input value={renaming} autoFocus maxLength={80} onChange={(event) => { setRenaming(event.target.value); rename.reset(); }} aria-label={t("teams.name")} className="h-9 max-w-80 text-base" />
							<Button type="submit" variant="primary" size="sm" disabled={!renaming.trim()}>{t("teams.save")}</Button>
							<Button type="button" variant="ghost" size="sm" onClick={() => { setRenaming(null); rename.reset(); }}>{t("confirm.cancel")}</Button>
						</form>
					) : (
						<div className="min-w-0 flex-1">
							<h2 className="truncate text-settings-heading font-semibold text-settings-title" data-team-name="">{data.team.name}</h2>
							{team.external && data.team.orgName ? <p className="text-control text-muted-foreground">{t("teams.workspaceOf", { name: data.team.orgName })}</p> : null}
						</div>
					)}
					{renaming === null ? (
						team.external && !manage ? (
							data.team.yourRole ? (
								<Button type="button" variant="secondary" size="sm" className="gap-1.5" onClick={() => setConfirming({ kind: "leave" })} data-team-leave="">
									<LogOut className="size-3.5" aria-hidden="true" />
									{t("teams.leave")}
								</Button>
							) : null
						) : (
							<DropdownMenu modal={false}>
								<DropdownMenuTrigger asChild>
									<Button type="button" variant="ghost" size="sm" aria-label={t("teams.moreActions")} data-team-actions="">
										<MoreHorizontal className="size-4" aria-hidden="true" />
									</Button>
								</DropdownMenuTrigger>
								<DropdownMenuContent align="end" className="w-48">
									{manage ? (
										<DropdownMenuItem onSelect={() => { rename.reset(); setRenaming(data.team.name); }}>
											<Pencil aria-hidden="true" />
											{t("teams.rename")}
										</DropdownMenuItem>
									) : null}
									{data.team.yourRole ? (
										// The last owner can't leave (a team needs an owner), so it says why instead of asking.
										<DropdownMenuItem
											disabled={data.team.yourRole === "owner" && owners <= 1}
											onSelect={() => setConfirming({ kind: "leave" })}
											data-team-leave=""
										>
											<LogOut aria-hidden="true" />
											<span className="flex min-w-0 flex-col">
												<span>{t("teams.leave")}</span>
												{data.team.yourRole === "owner" && owners <= 1 ? (
													<span className="text-xs text-muted-foreground" data-leave-reason="">{t("teams.leaveLastOwner")}</span>
												) : null}
											</span>
										</DropdownMenuItem>
									) : null}
									{manage ? (
										<>
											<DropdownMenuSeparator />
											<DropdownMenuItem onSelect={() => setConfirming({ kind: "delete" })} className="text-destructive focus:text-destructive [&_svg]:text-destructive!">
												<Trash2 aria-hidden="true" />
												{t("teams.delete")}
											</DropdownMenuItem>
										</>
									) : null}
								</DropdownMenuContent>
							</DropdownMenu>
						)
					) : null}
				</div>
				{rename.error ? <p role="alert" className="text-caption text-error">{teamErrorMessage(rename.error, t("teams.saveFailed"))}</p> : null}
			</div>

			<SettingsSection title={t("teams.peopleCount", { count: data.members.length })} grouped>
				{manage ? (
					<div className="settings-content-row flex items-start gap-2" data-team-add="">
						<PeopleField
							orgId={team.orgId}
							value={adding}
							onChange={(next) => {
								setAdding(next);
								if (!add.isPending) add.reset();
							}}
							allowTeams={false}
							unavailable={inTeam}
							unavailableLabel={t("teams.alreadyIn")}
							placeholder={t("teams.addByNameOrEmail")}
							ariaLabel={t("teams.addPeople")}
							onSubmit={() => adding.length > 0 && !add.isPending && add.mutate(adding)}
							className="flex-1"
						/>
						<Button type="button" variant="secondary" className="gap-1.5" disabled={adding.length === 0 || add.isPending} onClick={() => add.mutate(adding)}>
							<UserPlus className="size-3.5" aria-hidden="true" />
							{t("teams.add")}
						</Button>
					</div>
				) : null}
				{add.error ? <p role="alert" className="settings-content-row text-caption text-error">{teamErrorMessage(add.error, t("teams.addFailed"))}</p> : null}
				{members.map((member) => (
					<MemberRow
						key={member.id}
						member={member}
						manage={manage}
						lastOwner={member.role === "owner" && owners <= 1}
						onRole={(role) => changeRole.mutate({ member, role })}
						onRemove={() => setConfirming({ kind: "remove", member })}
					/>
				))}
				{hidden > 0 ? (
					<Pressable className="settings-row-bar w-full text-control text-(--color-text-markdown-link) hover:underline" onClick={() => setShowAll(true)}>
						{t("teams.showAll", { count: hidden })}
					</Pressable>
				) : null}
			</SettingsSection>
			{changeRole.error ? <p role="alert" className="-mt-4 px-(--settings-row-inset) text-caption text-error">{teamErrorMessage(changeRole.error, t("teams.saveFailed"))}</p> : null}

			<SettingsSection title={t("teams.sharedWith", { name: data.team.name })} grouped>
				{data.shares.length === 0 ? (
					<div className="settings-content-row text-control text-muted-foreground">{manage ? t("teams.nothingSharedManage") : t("teams.nothingShared")}</div>
				) : data.shares.map((entry) => (
					<SettingsRow
						key={`${entry.projectId}:${entry.scope}:${entry.sessionId ?? ""}`}
						leading={entry.scope === "session"
							? <MessageSquare className="size-4 text-muted-foreground" aria-hidden="true" />
							: <Folder className="size-4 text-muted-foreground" aria-hidden="true" />}
						label={entry.scope === "session" ? entry.sessionName || entry.projectName : entry.projectName}
						description={entry.scope === "session"
							? t("teams.shareOfSession", { project: entry.projectName, name: entry.sharedByName || t("teams.someone") })
							: t("teams.shareOfProject", { name: entry.sharedByName || t("teams.someone") })}
						data-team-share={entry.grantId}
					>
						{manage ? (
							<AccessMenu
								role={entry.role}
								modeCap={entry.modeCap ?? defaultCap(entry.role)}
								scope={entry.scope}
								// The latest change's error is the one shown.
								onChange={({ role, modeCap }) => {
									undoNotice.clearFailure();
									if (!stopShare.isPending) stopShare.reset();
									changeShare.mutate({ entry, role, modeCap });
								}}
								onStop={() => {
									undoNotice.clearFailure();
									if (!changeShare.isPending) changeShare.reset();
									stopShare.mutate(entry);
								}}
								stopLabel={t("share.stopSharingWith", { name: data.team.name })}
							/>
						) : (
							<span className="text-control text-settings-muted">{roleLabel(t, entry.role, entry.scope)}</span>
						)}
					</SettingsRow>
				))}
			</SettingsSection>
			{shareError || undoNotice.failed ? (
				<p role="alert" className="-mt-4 px-(--settings-row-inset) text-caption text-error">
					{shareError ? teamErrorMessage(shareError, t("teams.saveFailed")) : t("share.undoFailed")}
				</p>
			) : null}

			{confirming ? (
				<ConfirmDialog
					open
					destructive
					title={confirmTitle(t, confirming, data)}
					description={impact.isLoading ? t("teams.checkingImpact") : confirmBody(t, confirming, data, impact.data, i18n.language)}
					confirmLabel={confirming.kind === "remove" ? t("teams.remove") : confirming.kind === "leave" ? t("teams.leave") : t("teams.deleteTeam")}
					busy={confirm.isPending}
					confirmDisabled={impact.isLoading}
					error={confirm.error ? teamErrorMessage(confirm.error, t("teams.saveFailed")) : null}
					onConfirm={() => confirm.mutate(confirming)}
					onOpenChange={(next) => {
						if (!next) {
							setConfirming(null);
							confirm.reset();
						}
					}}
				/>
			) : null}
		</div>
	);
}

function confirmTitle(t: TFunction, confirming: Confirming, data: CloudCpTeamDetail): string {
	if (confirming.kind === "remove") return t("teams.removeTitle", { name: memberName(t, confirming.member), team: data.team.name });
	if (confirming.kind === "leave") return t("teams.leaveTitle", { team: data.team.name });
	return t("teams.deleteTitle", { team: data.team.name });
}

function confirmBody(t: TFunction, confirming: Confirming, data: CloudCpTeamDetail, impact: CloudCpTeamImpact | undefined, locale: string): string {
	if (!impact) return "";
	const lost = lostText(t, impact, locale);
	const team = data.team.name;
	if (confirming.kind === "remove") {
		const name = memberName(t, confirming.member);
		if (confirming.member.workspaceMember || impact.people === 0) return t("teams.removeWorkspaceMember", { name, team });
		const base = lost ? t("teams.removeLoses", { name, lost, team }) : t("teams.removeLosesNothing", { name, team });
		return impact.keeps.length > 0 ? `${base} ${t("teams.removeKeeps", { name, kept: listNames(impact.keeps, locale, (count) => t("share.summary.more", { count })) })}` : base;
	}
	if (confirming.kind === "leave") {
		return lost ? t("teams.leaveLoses", { lost, team }) : t("teams.leaveLosesNothing", { team });
	}
	if (!lost) return t("teams.deleteLosesNothing", { team });
	if (impact.people === 0) return t("teams.deleteWorkspaceOnly", { team });
	return t("teams.deleteLoses", { count: impact.people, lost, team });
}

function memberName(t: TFunction, member: CloudCpTeamMember): string {
	return member.name || member.email || t("teams.someone");
}

function MemberRow({ member, manage, lastOwner, onRole, onRemove }: {
	member: CloudCpTeamMember;
	manage: boolean;
	lastOwner: boolean;
	onRole: (role: "owner" | "member") => void;
	onRemove: () => void;
}) {
	const { t } = useTranslation();
	const roleOptions = useMemo(() => [
		{ value: "owner" as const, label: t("teams.roleOwner"), disabled: !member.workspaceMember && member.role !== "owner" },
		{ value: "member" as const, label: t("teams.roleMember"), disabled: lastOwner },
	], [lastOwner, member.role, member.workspaceMember, t]);
	// Why a role can't be chosen, under it in the menu.
	const roleReason = (role: "owner" | "member") =>
		role === "owner" ? t("teams.ownerNeedsWorkspace") : t("teams.lastOwnerStays");
	const name = member.name || member.email || t("teams.invitedPerson");
	const description = member.invited
		? t("teams.addedByEmail")
		: member.name && member.email ? member.email : undefined;
	return (
		<SettingsRow
			leading={<PersonAvatar name={member.name} email={member.email} invited={member.invited} />}
			label={<>{name}{member.you ? <span className="text-settings-muted"> {t("share.you")}</span> : null}</>}
			description={description}
			data-team-member={member.id}
		>
			<div className="flex items-center gap-1">
				{manage ? (
					<SettingsOptionMenu
						value={member.role}
						options={roleOptions}
						onChange={(role) => role !== member.role && onRole(role)}
						aria-label={t("teams.roleFor", { name })}
						renderMenuItem={(option) => (
							<span className="flex min-w-0 flex-col">
								<span>{option.label}</span>
								{option.disabled ? (
									<span className="text-xs text-muted-foreground" data-role-reason={option.value}>{roleReason(option.value)}</span>
								) : null}
							</span>
						)}
					/>
				) : (
					<span className="text-control text-settings-muted">{member.role === "owner" ? t("teams.roleOwner") : t("teams.roleMember")}</span>
				)}
				{manage && !member.you ? (
					<DropdownMenu modal={false}>
						<DropdownMenuTrigger asChild>
							<Button type="button" variant="quiet" size="icon-sm" aria-label={t("teams.memberActions", { name })} data-member-actions="">
								<MoreHorizontal className="size-4" aria-hidden="true" />
							</Button>
						</DropdownMenuTrigger>
						<DropdownMenuContent align="end" className="w-44">
							<DropdownMenuItem onSelect={onRemove} disabled={lastOwner} className="text-destructive focus:text-destructive [&_svg]:text-destructive!">
								<Trash2 aria-hidden="true" />
								{t("teams.removeFromTeam")}
							</DropdownMenuItem>
						</DropdownMenuContent>
					</DropdownMenu>
				) : null}
			</div>
		</SettingsRow>
	);
}
