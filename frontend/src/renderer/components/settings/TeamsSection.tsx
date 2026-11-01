import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, Folder, LogOut, MessageSquare, MoreHorizontal, Pencil, Plus, Trash2, UserPlus, Users } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { cn } from "../../lib/utils";
import { useCloudCp } from "../../hooks/useCloudCp";
import { useCloudOrg } from "../../hooks/useCloudOrg";
import { invalidateTeamsAndShares, useMyTeams, useTeam, useTeams } from "../../hooks/useTeams";
import type { CloudCpShareEntry, CloudCpTeam, CloudCpTeamDetail, CloudCpTeamImpact, CloudCpTeamMember } from "../../lib/cloud-cp";
import { useUiStore } from "../../stores/ui-store";
import { ConfirmDialog } from "../ConfirmDialog";
import { AccessMenu } from "../share/AccessMenu";
import { defaultCap, listNames, roleLabel } from "../share/accessLabels";
import { NewTeamDialog, teamErrorMessage } from "../teams/NewTeamDialog";
import { PeopleField, type Recipient } from "../teams/PeopleField";
import { PersonAvatar, TeamTile } from "../teams/TeamVisuals";
import { Button } from "../ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { Input } from "../ui/input";
import { SettingsOptionMenu } from "./SettingsOptionMenu";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

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
 */
export function TeamsSection({ titleHidden }: { titleHidden: boolean }) {
	const { org } = useCloudOrg();
	const [openTeam, setOpenTeam] = useState<OpenTeam | null>(null);
	if (!org) return <TeamsSkeleton />;
	if (openTeam) {
		return <TeamPage key={openTeam.teamId} team={openTeam} onBack={() => setOpenTeam(null)} />;
	}
	return <TeamsList orgId={org.id} titleHidden={titleHidden} onOpen={setOpenTeam} />;
}

function TeamsSkeleton() {
	return (
		<div className="settings-grouped-rows flex w-full flex-col" aria-busy="true">
			{[0, 1, 2].map((row) => (
				<div key={row} className="settings-row-bar flex items-center gap-3">
					<span className="size-7 rounded-[7px] bg-muted" />
					<span className="h-3 w-48 rounded bg-muted" />
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

function TeamsList({ orgId, titleHidden, onOpen }: { orgId: string; titleHidden: boolean; onOpen: (team: OpenTeam) => void }) {
	const { t, i18n } = useTranslation();
	const teams = useTeams(orgId);
	const mine = useMyTeams();
	const [creating, setCreating] = useState(false);
	const elsewhere = (mine.data ?? []).filter((team) => team.orgId !== orgId);
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
	if (teams.isLoading) return <TeamsSkeleton />;
	if (teams.error) {
		return (
			<SettingsSection title={t("teams.yourTeams")} titleHidden={titleHidden}>
				<p role="alert" className="px-(--settings-row-inset) text-control text-error">{teamErrorMessage(teams.error, t("teams.loadFailed"))}</p>
			</SettingsSection>
		);
	}
	const own = teams.data ?? [];
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
			{elsewhere.length > 0 ? (
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
			) : null}
			{dialog}
		</>
	);
}

function TeamListRow({ team, subtitle, onOpen }: { team: CloudCpTeam; subtitle: string; onOpen: () => void }) {
	const { t } = useTranslation();
	return (
		<button
			type="button"
			className="settings-row-bar flex w-full items-center gap-3 text-left transition-colors duration-(--duration-fast) hover:bg-(--color-bg-settings-row-hover) focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring"
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
		</button>
	);
}

type Confirming =
	| { kind: "remove"; member: CloudCpTeamMember }
	| { kind: "delete" }
	| { kind: "leave" };

function TeamPage({ team, onBack }: { team: OpenTeam; onBack: () => void }) {
	const { t, i18n } = useTranslation();
	const { client, ready } = useCloudCp();
	const queryClient = useQueryClient();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const detail = useTeam(team.orgId, team.teamId, team.external);
	const [adding, setAdding] = useState<Recipient[]>([]);
	const [renaming, setRenaming] = useState<string | null>(null);
	const [confirming, setConfirming] = useState<Confirming | null>(null);
	const [showAll, setShowAll] = useState(false);
	const invalidate = () => invalidateTeamsAndShares(queryClient);

	const add = useMutation({
		mutationFn: () => client.addTeamMembers(team.orgId, team.teamId, adding.filter((recipient): recipient is Extract<Recipient, { kind: "person" }> => recipient.kind === "person").map((recipient) => ({ email: recipient.email }))),
		onSuccess: () => {
			setAdding([]);
			invalidate();
		},
	});
	const rename = useMutation({
		mutationFn: (name: string) => client.renameTeam(team.orgId, team.teamId, name),
		onSuccess: () => {
			setRenaming(null);
			invalidate();
		},
	});
	const changeRole = useMutation({
		mutationFn: ({ member, role }: { member: CloudCpTeamMember; role: "owner" | "member" }) => client.updateTeamMember(team.orgId, team.teamId, member.id, role),
		onSettled: invalidate,
	});
	const changeShare = useMutation({
		mutationFn: ({ entry, role, modeCap }: { entry: CloudCpShareEntry; role: "viewer" | "editor"; modeCap: "read-only" | "standard" | "trusted" }) =>
			client.updateShareEntry(team.orgId, entry.projectId, entry.grantId, { role, modeCap }),
		onSettled: invalidate,
	});
	const stopShare = useMutation({
		mutationFn: (entry: CloudCpShareEntry) => client.revokeShareGrant(team.orgId, entry.projectId, entry.grantId),
		onSuccess: (_result, entry) => {
			invalidate();
			showGlobalToast(t("share.stoppedWithTeam", { name: entry.sessionName || entry.projectName, team: detail.data?.team.name ?? "" }), undefined, {
				durationMs: 8_000,
				action: {
					label: t("share.undo"),
					run: () => {
						void client
							.createShareGrants(team.orgId, entry.projectId, {
								...(entry.sessionId ? { sessionId: entry.sessionId } : {}),
								subjects: [{ teamId: team.teamId }],
								role: entry.role,
								modeCap: entry.modeCap ?? defaultCap(entry.role),
							})
							.finally(invalidate);
					},
				},
			});
		},
	});
	const impact = useQuery({
		queryKey: ["cloud-team-impact", team.teamId, confirming?.kind ?? "", confirming?.kind === "remove" ? confirming.member.id : ""],
		enabled: ready && confirming !== null,
		staleTime: 0,
		queryFn: () => {
			if (!confirming) throw new Error("no change");
			if (confirming.kind === "remove") return client.teamMemberImpact(team.orgId, team.teamId, confirming.member.id);
			if (confirming.kind === "leave") return client.leaveTeamImpact(team.teamId);
			return client.teamImpact(team.orgId, team.teamId);
		},
	});
	const confirm = useMutation({
		mutationFn: async () => {
			if (!confirming) return;
			if (confirming.kind === "remove") await client.removeTeamMember(team.orgId, team.teamId, confirming.member.id);
			else if (confirming.kind === "leave") await client.leaveTeam(team.teamId);
			else await client.deleteTeam(team.orgId, team.teamId);
		},
		onSuccess: () => {
			const kind = confirming?.kind;
			setConfirming(null);
			invalidate();
			if (kind === "leave" || kind === "delete") onBack();
		},
	});

	const back = (
		<button type="button" className="-ml-1 inline-flex items-center gap-1 self-start rounded-md px-1 py-0.5 text-control text-muted-foreground hover:text-foreground" onClick={onBack} data-team-back="">
			<ChevronLeft className="size-3.5" aria-hidden="true" />
			{t("teams.title")}
		</button>
	);
	if (detail.isLoading) {
		return <div className="flex flex-col gap-4 px-(--settings-row-inset)">{back}<TeamsSkeleton /></div>;
	}
	if (detail.error || !detail.data) {
		return (
			<div className="flex flex-col gap-3 px-(--settings-row-inset)">
				{back}
				<p role="alert" className="text-control text-error">{teamErrorMessage(detail.error, t("teams.loadFailed"))}</p>
			</div>
		);
	}
	const data = detail.data;
	const manage = data.team.canManage;
	const owners = data.members.filter((member) => member.role === "owner").length;
	const members = showAll ? data.members : data.members.slice(0, MEMBERS_SHOWN);
	const hidden = data.members.length - members.length;

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
								if (renaming.trim()) rename.mutate(renaming.trim());
							}}
						>
							<Input value={renaming} autoFocus maxLength={80} onChange={(event) => setRenaming(event.target.value)} aria-label={t("teams.name")} className="h-9 max-w-80 text-base" />
							<Button type="submit" variant="primary" size="sm" disabled={!renaming.trim() || rename.isPending}>{t("teams.save")}</Button>
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
										<DropdownMenuItem onSelect={() => setRenaming(data.team.name)}>
											<Pencil aria-hidden="true" />
											{t("teams.rename")}
										</DropdownMenuItem>
									) : null}
									{data.team.yourRole ? (
										<DropdownMenuItem onSelect={() => setConfirming({ kind: "leave" })}>
											<LogOut aria-hidden="true" />
											{t("teams.leave")}
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
								add.reset();
							}}
							allowTeams={false}
							unavailable={new Set(data.members.flatMap((member) => (member.email ? [`email:${member.email}`] : [])))}
							unavailableLabel={t("teams.alreadyIn")}
							placeholder={t("teams.addByNameOrEmail")}
							ariaLabel={t("teams.addPeople")}
							onSubmit={() => adding.length > 0 && add.mutate()}
							className="flex-1"
						/>
						<Button type="button" variant="secondary" className="gap-1.5" disabled={adding.length === 0 || add.isPending} onClick={() => add.mutate()}>
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
					<button type="button" className="settings-row-bar w-full text-left text-control text-(--color-text-markdown-link) hover:underline" onClick={() => setShowAll(true)}>
						{t("teams.showAll", { count: hidden })}
					</button>
				) : null}
			</SettingsSection>
			{changeRole.error ? <p role="alert" className="-mt-4 px-(--settings-row-inset) text-caption text-error">{teamErrorMessage(changeRole.error, t("teams.saveFailed"))}</p> : null}

			<SettingsSection title={t("teams.sharedWith", { name: data.team.name })} grouped>
				{data.shares.length === 0 ? (
					<div className="settings-content-row text-control text-muted-foreground">{manage ? t("teams.nothingSharedManage") : t("teams.nothingShared")}</div>
				) : data.shares.map((entry) => (
					<SettingsRow
						key={entry.grantId}
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
								onChange={({ role, modeCap }) => changeShare.mutate({ entry, role, modeCap })}
								onStop={() => stopShare.mutate(entry)}
								stopLabel={t("share.stopSharingWith", { name: data.team.name })}
							/>
						) : (
							<span className="text-control text-settings-muted">{roleLabel(t, entry.role, entry.scope)}</span>
						)}
					</SettingsRow>
				))}
			</SettingsSection>

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
					onConfirm={() => confirm.mutate()}
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
					/>
				) : (
					<span className="text-control text-settings-muted">{member.role === "owner" ? t("teams.roleOwner") : t("teams.roleMember")}</span>
				)}
				{manage && !member.you ? (
					<DropdownMenu modal={false}>
						<DropdownMenuTrigger asChild>
							<button
								type="button"
								className={cn("grid size-7 place-items-center rounded-md text-passive hover:bg-interactive-hover hover:text-foreground")}
								aria-label={t("teams.memberActions", { name })}
								data-member-actions=""
							>
								<MoreHorizontal className="size-4" aria-hidden="true" />
							</button>
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
