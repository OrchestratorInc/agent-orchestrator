import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, Info, X } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudSession } from "../lib/cloud-session";
import { useRestoreFocusOnClose } from "../hooks/useRestoreFocusOnClose";
import {
	invalidateTeamsAndShares,
	shareAccessQueryKey,
	teamShareAccessQueryKey,
} from "../hooks/useTeams";
import type {
	CloudCpShareAccess,
	CloudCpShareEntry,
	CloudCpShareRole,
	CloudCpTeamMemberAccess,
} from "../lib/cloud-cp";
import { useUiStore } from "../stores/ui-store";
import { AccessMenu, CapMenu } from "./share/AccessMenu";
import { defaultCap, effectiveAccessText, listNames, roleLabel, type ShareModeCap, type ShareScope } from "./share/accessLabels";
import { ShareLinksSection } from "./share/ShareLinksSection";
import { NewTeamDialog } from "./teams/NewTeamDialog";
import { PeopleField, recipientKey, type Recipient } from "./teams/PeopleField";
import { PersonAvatar, TeamTile } from "./teams/TeamVisuals";
import { Button } from "./ui/button";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogDescription,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogHeaderClass,
} from "./ui/dialog";

export type { ShareScope, ShareModeCap } from "./share/accessLabels";
export type { ShareAccess, ShareExpiry } from "./share/ShareLinksSection";
export { shareLinkLabel, shareLinkRequest, shareLinksQueryKey } from "./share/ShareLinksSection";

const ROLE_RANK: Record<string, number> = { viewer: 1, editor: 2, member: 3 };

/** The name to show for a grant's subject. */
function subjectName(entry: Pick<CloudCpShareEntry, "subject">): string {
	const subject = entry.subject;
	return subject.kind === "team" ? subject.teamName ?? "" : subject.name || subject.email || "";
}

/** How to share the same thing again, for Undo. */
function subjectInput(entry: CloudCpShareEntry): { teamId: string } | { email: string } | undefined {
	if (entry.subject.kind === "team" && entry.subject.teamId) return { teamId: entry.subject.teamId };
	if (entry.subject.email) return { email: entry.subject.email };
	return undefined;
}

/**
 * Shares this cloud session, or its whole project, with teams and people,
 * and lists everyone who has access before asking for anything. Picking
 * someone turns the dialog into a short confirm step that names what will
 * happen to whom. Links stay below, under Anyone with the link.
 */
export function ShareSessionDialog({ open, onOpenChange, orgId, projectId, sessionId }: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	orgId: string;
	projectId: string;
	sessionId: string;
}) {
	const { t, i18n } = useTranslation();
	const { client, baseUrl, ready } = useCloudCp();
	const myEmail = useCloudSession().session?.user.email?.toLowerCase() ?? "";
	const queryClient = useQueryClient();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const [scope, setScope] = useState<ShareScope>("session");
	const [staged, setStaged] = useState<Recipient[]>([]);
	const [stagedRole, setStagedRole] = useState<CloudCpShareRole>("viewer");
	const [stagedCap, setStagedCap] = useState<ShareModeCap>("read-only");
	const [newTeamFor, setNewTeamFor] = useState<Recipient[] | null>(null);
	const scopeSession = scope === "session" ? sessionId : undefined;
	const accessKey = shareAccessQueryKey(baseUrl, orgId, projectId, scopeSession);
	const access = useQuery({
		queryKey: accessKey,
		enabled: open && ready,
		queryFn: () => client.getShareAccess(orgId, projectId, scopeSession),
	});
	const entries = access.data?.entries ?? [];
	const ownEntries = entries.filter((entry) => entry.scope === scope);
	const unavailable = useMemo(() => {
		const keys = new Set<string>();
		for (const entry of ownEntries) {
			if (entry.subject.kind === "team" && entry.subject.teamId) keys.add(`team:${entry.subject.teamId}`);
			else if (entry.subject.email) keys.add(`email:${entry.subject.email.toLowerCase()}`);
		}
		if (access.data?.owner?.email) keys.add(`email:${access.data.owner.email.toLowerCase()}`);
		return keys;
	}, [ownEntries, access.data?.owner?.email]);

	const invalidate = () => invalidateTeamsAndShares(queryClient);
	const share = useMutation({
		mutationFn: () =>
			client.createShareGrants(orgId, projectId, {
				...(scopeSession ? { sessionId: scopeSession } : {}),
				subjects: staged.map((recipient) => (recipient.kind === "team" ? { teamId: recipient.teamId } : { email: recipient.email })),
				role: stagedRole,
				modeCap: stagedCap,
			}),
		onSuccess: () => {
			setStaged([]);
			setStagedRole("viewer");
			setStagedCap("read-only");
			invalidate();
		},
	});
	const update = useMutation({
		mutationFn: ({ entry, role, modeCap }: { entry: CloudCpShareEntry; role: CloudCpShareRole; modeCap: ShareModeCap }) =>
			client.updateShareEntry(orgId, projectId, entry.grantId, { role, modeCap }),
		onMutate: async ({ entry, role, modeCap }) => {
			await queryClient.cancelQueries({ queryKey: accessKey });
			const previous = queryClient.getQueryData<CloudCpShareAccess>(accessKey);
			queryClient.setQueryData<CloudCpShareAccess>(accessKey, (data) =>
				data ? { ...data, entries: data.entries.map((other) => (other.grantId === entry.grantId ? { ...other, role, modeCap } : other)) } : data,
			);
			return { previous };
		},
		onError: (_error, _input, context) => {
			if (context?.previous) queryClient.setQueryData(accessKey, context.previous);
		},
		onSettled: invalidate,
	});
	const stop = useMutation({
		mutationFn: (entry: CloudCpShareEntry) => client.revokeShareGrant(orgId, projectId, entry.grantId),
		onMutate: async (entry) => {
			await queryClient.cancelQueries({ queryKey: accessKey });
			const previous = queryClient.getQueryData<CloudCpShareAccess>(accessKey);
			queryClient.setQueryData<CloudCpShareAccess>(accessKey, (data) =>
				data ? { ...data, entries: data.entries.filter((other) => other.grantId !== entry.grantId) } : data,
			);
			return { previous };
		},
		onError: (_error, _entry, context) => {
			if (context?.previous) queryClient.setQueryData(accessKey, context.previous);
		},
		onSuccess: (_result, entry) => {
			const subject = subjectInput(entry);
			showGlobalToast(t("share.stopped", { name: subjectName(entry) }), undefined, {
				durationMs: 8_000,
				dedupeKey: `share-stopped-${entry.grantId}`,
				...(subject
					? {
						action: {
							label: t("share.undo"),
							run: () => {
								void client
									.createShareGrants(orgId, projectId, {
										...(entry.sessionId ? { sessionId: entry.sessionId } : {}),
										subjects: [subject],
										role: entry.role,
										modeCap: entry.modeCap ?? defaultCap(entry.role),
									})
									.finally(invalidate);
							},
						},
					}
					: {}),
			});
		},
		onSettled: invalidate,
	});

	const close = (next: boolean) => {
		onOpenChange(next);
		if (!next) {
			setStaged([]);
			setScope("session");
			share.reset();
		}
	};
	const switchScope = (next: ShareScope) => {
		setScope(next);
		setStaged([]);
		share.reset();
	};
	const staging = staged.length > 0;
	const error = share.error ?? update.error ?? stop.error ?? access.error;

	return (
		<Dialog open={open} onOpenChange={close}>
			<DialogContent
				showCloseButton={false}
				className={cn(settingsDialogContentClass, "w-[min(540px,calc(100vw-24px))]")}
				onClick={(event) => event.stopPropagation()}
				onCloseAutoFocus={restoreFocusOnClose}
				data-share-session-dialog
			>
				<DialogClose asChild>
					<button type="button" className="settings-dialog-close-button settings-close-button" aria-label={t("confirm.close")}>
						<X className="size-4" aria-hidden="true" />
					</button>
				</DialogClose>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 p-5 pr-12 pb-0")}>
					<DialogTitle className="settings-dialog-title text-subtitle">{scope === "project" ? t("share.projectTitle") : t("share.title")}</DialogTitle>
					<DialogDescription className={cn("text-control leading-5 text-settings-muted", scope === "project" ? "" : "sr-only")}>
						{scope === "project" ? t("share.projectPeopleDescription") : t("share.peopleDescription")}
					</DialogDescription>
				</div>
				<div className={cn(settingsDialogBodyClass, "gap-3.5 px-5 pt-4 pb-5")}>
					<div role="radiogroup" aria-label={t("share.scope")} className="inline-flex gap-0.5 self-start rounded-md bg-(--color-bg-settings-input) p-0.5" data-share-scope="">
						{(["session", "project"] as const).map((value) => (
							<button
								key={value}
								type="button"
								role="radio"
								aria-checked={scope === value}
								onClick={() => switchScope(value)}
								className={cn(
									"h-6.5 rounded-sm px-2.5 text-[12.5px] transition-colors duration-(--duration-fast)",
									scope === value ? "bg-foreground/10 text-foreground" : "text-muted-foreground hover:text-foreground",
								)}
							>
								{value === "session" ? t("share.scopeSession") : t("share.scopeProject")}
							</button>
						))}
					</div>
					<PeopleField
						orgId={orgId}
						value={staged}
						onChange={(next) => {
							setStaged(next);
							share.reset();
						}}
						unavailable={unavailable}
						workspaceHasAccess
						placeholder={t("share.addPeopleOrTeams")}
						ariaLabel={t("share.addPeopleOrTeams")}
						onNewTeam={(people) => setNewTeamFor(people)}
						onSubmit={() => share.mutate()}
						trailing={staging ? (
							<AccessMenu
								role={stagedRole}
								modeCap={stagedCap}
								scope={scope}
								showCap={false}
								onChange={({ role, modeCap }) => {
									setStagedRole(role);
									setStagedCap(modeCap);
								}}
							/>
						) : undefined}
						invalid={Boolean(share.error)}
					/>
					{error ? <p role="alert" className="-mt-1.5 text-caption leading-4 text-error">{error instanceof Error ? error.message : String(error)}</p> : null}
					{staging ? (
						<StagedSummary
							orgId={orgId}
							projectId={projectId}
							sessionId={scopeSession}
							scope={scope}
							staged={staged}
							role={stagedRole}
							modeCap={stagedCap}
							onCapChange={setStagedCap}
							entries={ownEntries}
							me={myEmail}
							locale={i18n.language}
						/>
					) : (
						<>
							<AccessList
								orgId={orgId}
								projectId={projectId}
								sessionId={scopeSession}
								scope={scope}
								access={access.data}
								loading={access.isLoading}
								me={myEmail}
								onChange={(entry, next) => update.mutate({ entry, ...next })}
								onStop={(entry) => stop.mutate(entry)}
								onShowProject={() => switchScope("project")}
							/>
							<ShareLinksSection orgId={orgId} projectId={projectId} sessionId={sessionId} scope={scope} open={open} />
						</>
					)}
				</div>
				{staging ? (
					<div className="flex items-center justify-end gap-2 border-t border-border px-5 py-3.5" data-share-footer="">
						<Button type="button" variant="ghost" onClick={() => setStaged([])} disabled={share.isPending}>
							{t("confirm.cancel")}
						</Button>
						<Button type="button" variant="primary" onClick={() => share.mutate()} disabled={!ready || share.isPending} data-share-submit="">
							{share.isPending ? t("share.sharing") : t("share.shareAction")}
						</Button>
					</div>
				) : null}
				{newTeamFor ? (
					<NewTeamDialog
						orgId={orgId}
						open
						initialPeople={newTeamFor.filter((recipient) => recipient.kind === "person")}
						onOpenChange={(next) => {
							if (!next) setNewTeamFor(null);
						}}
						onCreated={(team) => {
							setNewTeamFor(null);
							setStaged((current) => [
								...current.filter((recipient) => recipient.kind === "team"),
								{ kind: "team", teamId: team.team.id, name: team.team.name, memberCount: team.team.memberCount },
							]);
						}}
					/>
				) : null}
			</DialogContent>
		</Dialog>
	);
}

function AccessList({ orgId, projectId, sessionId, scope, access, loading, me, onChange, onStop, onShowProject }: {
	orgId: string;
	projectId: string;
	sessionId: string | undefined;
	scope: ShareScope;
	access: CloudCpShareAccess | undefined;
	loading: boolean;
	me: string | undefined;
	onChange: (entry: CloudCpShareEntry, next: { role: CloudCpShareRole; modeCap: ShareModeCap }) => void;
	onStop: (entry: CloudCpShareEntry) => void;
	onShowProject: () => void;
}) {
	const { t } = useTranslation();
	const owner = access?.owner;
	const entries = access?.entries ?? [];
	if (loading) {
		return (
			<div className="flex flex-col gap-1.5" aria-busy="true">
				<p className="text-xs text-muted-foreground">{t("share.peopleWithAccess")}</p>
				<div className="flex flex-col gap-2 rounded-md bg-muted/40 p-3">
					{[0, 1].map((row) => (
						<div key={row} className="flex items-center gap-2.5">
							<span className="size-6 rounded-full bg-muted" />
							<span className="h-3 w-40 rounded bg-muted" />
						</div>
					))}
				</div>
			</div>
		);
	}
	if (!owner && entries.length === 0) {
		return (
			<div className="flex flex-col gap-1.5">
				<p className="text-xs text-muted-foreground">{t("share.peopleWithAccess")}</p>
				<p className="flex items-start gap-1.5 text-xs leading-5 text-muted-foreground" data-share-empty="">
					<Info className="mt-1 size-3 shrink-0 text-passive" aria-hidden="true" />
					{scope === "project" ? t("share.projectNotShared") : t("share.notShared")}
				</p>
			</div>
		);
	}
	return (
		<div className="flex flex-col gap-1.5">
			<p className="text-xs text-muted-foreground">{t("share.peopleWithAccess")}</p>
			<ul className="flex flex-col divide-y divide-border rounded-md bg-muted/40 py-0.5" data-share-access-list="">
				{owner ? (
					<li className="flex min-h-11 items-center gap-2.5 py-1.5 pr-2 pl-2.5">
						<PersonAvatar name={owner.name} email={owner.email} />
						<span className="min-w-0 flex-1 truncate text-control text-foreground">
							{owner.name || owner.email}
							{owner.email && owner.email.toLowerCase() === me ? <span className="text-muted-foreground"> {t("share.you")}</span> : null}
						</span>
						<span className="pr-1 text-control text-passive">{t("share.owner")}</span>
					</li>
				) : null}
				{entries.map((entry) => (
					<AccessRow
						key={entry.grantId}
						orgId={orgId}
						projectId={projectId}
						sessionId={sessionId}
						scope={scope}
						entry={entry}
						onChange={(next) => onChange(entry, next)}
						onStop={() => onStop(entry)}
						onShowProject={onShowProject}
					/>
				))}
			</ul>
		</div>
	);
}

function AccessRow({ orgId, projectId, sessionId, scope, entry, onChange, onStop, onShowProject }: {
	orgId: string;
	projectId: string;
	sessionId: string | undefined;
	scope: ShareScope;
	entry: CloudCpShareEntry;
	onChange: (next: { role: CloudCpShareRole; modeCap: ShareModeCap }) => void;
	onStop: () => void;
	onShowProject: () => void;
}) {
	const { t } = useTranslation();
	const [expanded, setExpanded] = useState(false);
	const fromProject = scope === "session" && entry.scope === "project";
	const name = subjectName(entry);
	const team = entry.subject.kind === "team";
	const sub = team
		? t("teams.people", { count: entry.subject.memberCount ?? 0 })
		: entry.subject.name
			? entry.subject.email
			: t("teams.addedByEmail");
	const modeCap = entry.modeCap ?? defaultCap(entry.role);
	return (
		<li className="flex flex-col" data-share-entry={entry.subject.kind} data-share-entry-scope={entry.scope}>
			<div className="flex min-h-11 items-center gap-2.5 py-1.5 pr-1.5 pl-2.5">
				{team ? <TeamTile name={name} /> : <PersonAvatar name={entry.subject.name} email={entry.subject.email} invited={!entry.subject.name} />}
				<div className="min-w-0 flex-1">
					<p className="truncate text-control text-foreground">{name}</p>
					<p className="truncate text-xs text-muted-foreground">
						{sub}
						{fromProject ? (
							<>
								{" · "}
								<button type="button" className="underline-offset-2 hover:text-foreground hover:underline" onClick={onShowProject}>
									{t("share.fromProject")}
								</button>
							</>
						) : entry.source === "link" ? ` · ${t("share.fromLink")}` : null}
					</p>
				</div>
				{fromProject ? (
					<span className="px-2 text-control whitespace-nowrap text-muted-foreground" title={t("share.changeInProject")}>
						{roleLabel(t, entry.role, "project")}
					</span>
				) : (
					<AccessMenu
						role={entry.role}
						modeCap={modeCap}
						scope={scope}
						onChange={onChange}
						onStop={onStop}
						stopLabel={t("share.stopSharingWith", { name })}
					/>
				)}
				{team ? (
					<button
						type="button"
						className="grid size-6 shrink-0 place-items-center rounded-sm text-passive hover:bg-interactive-hover hover:text-foreground"
						aria-expanded={expanded}
						aria-label={expanded ? t("share.hideMembers", { name }) : t("share.showMembers", { name })}
						onClick={() => setExpanded((value) => !value)}
						data-share-expand=""
					>
						<ChevronRight className={cn("size-3.5 transition-transform duration-(--duration-normal)", expanded && "rotate-90")} aria-hidden="true" />
					</button>
				) : (
					<span className="w-6 shrink-0" />
				)}
			</div>
			{team && expanded && entry.subject.teamId ? (
				<TeamMembersAccess orgId={orgId} projectId={projectId} sessionId={sessionId} scope={scope} teamId={entry.subject.teamId} />
			) : null}
		</li>
	);
}

function TeamMembersAccess({ orgId, projectId, sessionId, scope, teamId }: {
	orgId: string;
	projectId: string;
	sessionId: string | undefined;
	scope: ShareScope;
	teamId: string;
}) {
	const { t } = useTranslation();
	const { client, baseUrl, ready } = useCloudCp();
	const members = useQuery({
		queryKey: teamShareAccessQueryKey(baseUrl, orgId, projectId, sessionId, teamId),
		enabled: ready,
		queryFn: async () => (await client.getTeamShareAccess(orgId, projectId, sessionId, teamId)).members,
	});
	if (members.isLoading) {
		return <p className="pb-2 pl-11 text-xs text-muted-foreground">{t("share.loadingMembers")}</p>;
	}
	const list = (members.data ?? []).filter((member) => !member.member.you);
	return (
		<div className="flex flex-col pr-2 pb-1.5 pl-11" data-share-team-members="">
			{list.map((member) => (
				<TeamMemberLine key={member.member.id} member={member} teamId={teamId} scope={scope} />
			))}
			<p className="flex items-start gap-1.5 pt-1.5 pb-0.5 text-xs leading-5 text-muted-foreground">
				<Info className="mt-1 size-3 shrink-0 text-passive" aria-hidden="true" />
				{t("share.highestWins")}
			</p>
		</div>
	);
}

function TeamMemberLine({ member, teamId, scope }: { member: CloudCpTeamMemberAccess; teamId: string; scope: ShareScope }) {
	const { t } = useTranslation();
	const name = member.member.name || member.member.email || t("teams.someone");
	const text = member.member.invited && !member.member.workspaceMember
		? { level: member.access.role ? roleLabel(t, member.access.role === "member" ? "editor" : member.access.role, scope) : t("teams.access.none"), source: t("teams.access.onSignIn") }
		: effectiveAccessText(t, member.access, teamId, scope);
	return (
		<div className="flex min-h-8.5 items-center gap-2.5 py-1" data-share-member="">
			<PersonAvatar name={member.member.name} email={member.member.email} invited={member.member.invited} size="sm" />
			<span className="min-w-0 flex-1 truncate text-[12.5px] text-foreground">{name}</span>
			<span className="shrink-0 text-xs whitespace-nowrap text-muted-foreground">
				{text.source ? <><span className="text-foreground">{text.level}</span> · {text.source}</> : text.level}
			</span>
		</div>
	);
}

/**
 * The confirm step: who will get what. Each picked team's members are
 * weighed against what they already have, so the line names who gains, who
 * keeps more, and who is already a workspace member.
 */
function StagedSummary({ orgId, projectId, sessionId, scope, staged, role, modeCap, onCapChange, entries, me, locale }: {
	orgId: string;
	projectId: string;
	sessionId: string | undefined;
	scope: ShareScope;
	staged: Recipient[];
	role: CloudCpShareRole;
	modeCap: ShareModeCap;
	onCapChange: (cap: ShareModeCap) => void;
	entries: CloudCpShareEntry[];
	me: string | undefined;
	locale: string;
}) {
	const { t } = useTranslation();
	const { client, baseUrl, ready } = useCloudCp();
	const teams = staged.filter((recipient): recipient is Extract<Recipient, { kind: "team" }> => recipient.kind === "team");
	const teamAccess = useQueries({
		queries: teams.map((team) => ({
			queryKey: teamShareAccessQueryKey(baseUrl, orgId, projectId, sessionId, team.teamId),
			enabled: ready,
			queryFn: async () => (await client.getTeamShareAccess(orgId, projectId, sessionId, team.teamId)).members,
		})),
	});
	const loading = teamAccess.some((query) => query.isLoading);
	const summary = useMemo(() => {
		const gets: string[] = [];
		const keeps: string[] = [];
		const workspace: string[] = [];
		const onSignIn: string[] = [];
		const seen = new Set<string>();
		const add = (bucket: string[], key: string, label: string) => {
			if (seen.has(key)) return;
			seen.add(key);
			bucket.push(label);
		};
		teamAccess.forEach((query) => {
			for (const member of query.data ?? []) {
				if (member.member.you || (member.member.email && member.member.email.toLowerCase() === me)) continue;
				const key = member.member.email || member.member.id;
				const label = member.member.name || member.member.email || t("teams.someone");
				if (member.member.workspaceMember) add(workspace, key, label);
				else if (member.member.invited && !member.member.name) add(onSignIn, key, label);
				else if ((ROLE_RANK[member.access.role ?? ""] ?? 0) >= ROLE_RANK[role]!) add(keeps, key, label);
				else add(gets, key, label);
			}
		});
		for (const recipient of staged) {
			if (recipient.kind !== "person") continue;
			const existing = entries.find((entry) => entry.subject.email?.toLowerCase() === recipient.email);
			const label = recipient.name || recipient.email;
			if (existing && ROLE_RANK[existing.role]! >= ROLE_RANK[role]!) add(keeps, recipient.email, label);
			else if (!recipient.name) add(onSignIn, recipient.email, label);
			else add(gets, recipient.email, label);
		}
		const names = (list: string[]) => listNames(list, locale, (count) => t("share.summary.more", { count }));
		const parts: string[] = [];
		const level = roleLabel(t, role, scope);
		if (gets.length) parts.push(t("share.summary.gets", { count: gets.length, names: names(gets), access: level }));
		if (onSignIn.length) parts.push(t("share.summary.onSignIn", { count: onSignIn.length, names: names(onSignIn), access: level }));
		if (keeps.length) parts.push(t("share.summary.keeps", { count: keeps.length, names: names(keeps) }));
		if (workspace.length) parts.push(t("share.summary.workspace", { count: workspace.length, names: names(workspace) }));
		if (parts.length === 0) parts.push(t("share.summary.nobody"));
		return parts.join(" ");
	}, [entries, locale, me, role, scope, staged, t, teamAccess]);

	return (
		<div className="flex flex-col gap-2 rounded-md bg-muted/40 px-3 py-2.5" data-share-summary="">
			{role === "editor" ? (
				<div className="flex items-center gap-2">
					<span className="text-control text-muted-foreground">{t("share.modeCap")}</span>
					<span className="flex-1" />
					<CapMenu value={modeCap} onChange={onCapChange} />
				</div>
			) : null}
			<p className="flex items-start gap-1.5 text-xs leading-5 text-muted-foreground" aria-live="polite">
				<Info className="mt-1 size-3 shrink-0 text-passive" aria-hidden="true" />
				<span>{loading ? t("share.summary.checking") : summary}</span>
			</p>
		</div>
	);
}

export { recipientKey };
