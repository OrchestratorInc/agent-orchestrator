import { type QueryClient, useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronRight, Info } from "lucide-react";
import { RadioGroup } from "radix-ui";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { useCloudCp } from "../hooks/useCloudCp";
import { useCloudSession } from "../lib/cloud-session";
import { useCloudOrg } from "../hooks/useCloudOrg";
import { useRestoreFocusOnClose } from "../hooks/useRestoreFocusOnClose";
import {
	invalidateTeamsAndShares,
	looksLikeEmail,
	retryCloudRead,
	shareAccessQueryKey,
	teamShareAccessQueryKey,
	teamsUnavailable,
} from "../hooks/useTeams";
import type {
	CloudCpShareAccess,
	CloudCpShareEntry,
	CloudCpShareRole,
	CloudCpTeamMemberAccess,
} from "../lib/cloud-cp";
import { AccessMenu, CapMenu } from "./share/AccessMenu";
import { defaultCap, effectiveAccessText, listNames, roleLabel, type ShareModeCap, type ShareScope } from "./share/accessLabels";
import { ShareLinksSection, useActiveShareLinks } from "./share/ShareLinksSection";
import { UndoNotice, useUndoNotice } from "./share/UndoNotice";
import { NewTeamDialog } from "./teams/NewTeamDialog";
import { PeopleField, recipientKey, type Recipient } from "./teams/PeopleField";
import { PersonAvatar, TeamTile } from "./teams/TeamVisuals";
import { Button } from "./ui/button";
import { CloseButton } from "./ui/close-button";
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
import { Skeleton } from "./ui/skeleton";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { Pressable } from "./ui/pressable";

export type { ShareScope, ShareModeCap } from "./share/accessLabels";
export type { ShareAccess, ShareExpiry } from "./share/ShareLinksSection";
export { createdShareLinkUrl, shareLinkLabel, shareLinkRequest, shareLinksQueryKey } from "./share/ShareLinksSection";

const ROLE_RANK: Record<string, number> = { viewer: 1, editor: 2, member: 3 };

/**
 * Every change to who has access shares this key, so a refetch waits for the
 * last one in flight: refetching when the first of two quick stops settles
 * would bring back the row the second just removed.
 */
const ACCESS_CHANGE = ["share-access-change"] as const;

/** A row shared before the control plane answered, until the grant replaces it. */
const PENDING = "pending:";

type AccessKey = ReturnType<typeof shareAccessQueryKey>;
/** null: the control plane predates teams (its routes answer 404), so the dialog offers links only. */
type AccessData = CloudCpShareAccess | null;
type ShareRequest = {
	key: AccessKey;
	scope: ShareScope;
	sessionId: string | undefined;
	recipients: Recipient[];
	role: CloudCpShareRole;
	modeCap: ShareModeCap;
};
type Subject = { teamId: string } | { email: string };

/** The name to show for a grant's subject. */
const CAP_RANK: Record<string, number> = { "read-only": 1, standard: 2, trusted: 3 };

/**
 * Gives a team's people, in every cached list of them under prefix, the
 * access the team now gives: those who had their access through this team,
 * and those it now gives more. Returns what it replaced, to put back.
 */
export function withTeamAccess(
	queryClient: QueryClient,
	prefix: readonly unknown[],
	teamId: string,
	role: CloudCpShareRole,
	modeCap: ShareModeCap,
): [readonly unknown[], CloudCpTeamMemberAccess[] | undefined][] {
	const replaced: [readonly unknown[], CloudCpTeamMemberAccess[] | undefined][] = [];
	for (const [key, data] of queryClient.getQueriesData<CloudCpTeamMemberAccess[]>({ queryKey: prefix })) {
		if (key[key.length - 1] !== teamId || !data) continue;
		replaced.push([key, data]);
		queryClient.setQueryData<CloudCpTeamMemberAccess[]>(key, data.map((member) => {
			const access = member.access;
			const viaTeam = access.source === "team" && access.teamId === teamId;
			const rank = (ROLE_RANK[access.role ?? ""] ?? 0) * 10 + (CAP_RANK[access.modeCap ?? ""] ?? 0);
			const gains = (ROLE_RANK[role] ?? 0) * 10 + (CAP_RANK[modeCap] ?? 0) > rank;
			if (access.source === "workspace" || (!viaTeam && !gains)) return member;
			return { ...member, access: { ...access, role, modeCap, source: "team", teamId } };
		}));
	}
	return replaced;
}

function subjectName(entry: Pick<CloudCpShareEntry, "subject">): string {
	const subject = entry.subject;
	return subject.kind === "team" ? subject.teamName ?? "" : subject.name || subject.email || "";
}

/** How to share the same thing again, for Undo. */
function subjectInput(entry: CloudCpShareEntry): Subject | undefined {
	if (entry.subject.kind === "team" && entry.subject.teamId) return { teamId: entry.subject.teamId };
	if (entry.subject.email) return { email: entry.subject.email };
	return undefined;
}

/**
 * One row per team or person at a scope, so a row shared a moment ago and
 * the grant that replaces it are the same row (and keep focus).
 */
export function entryRowKey(entry: Pick<CloudCpShareEntry, "subject" | "scope" | "sessionId">): string {
	const subject = entry.subject;
	const who = subject.kind === "team" ? `team:${subject.teamId ?? ""}` : subject.email ? `email:${subject.email.toLowerCase()}` : `user:${subject.userId ?? ""}`;
	return `${entry.scope}:${entry.sessionId ?? ""}:${who}`;
}

function pendingEntry(recipient: Recipient, request: ShareRequest, projectId: string): CloudCpShareEntry {
	return {
		grantId: `${PENDING}${recipientKey(recipient)}`,
		subject: recipient.kind === "team"
			? { kind: "team", teamId: recipient.teamId, teamName: recipient.name, memberCount: recipient.memberCount }
			: { kind: recipient.name ? "user" : "email", email: recipient.email, ...(recipient.name ? { name: recipient.name } : {}) },
		scope: request.scope,
		...(request.sessionId ? { sessionId: request.sessionId } : {}),
		projectId,
		projectName: "",
		role: request.role,
		modeCap: request.modeCap,
		deniedCommands: [],
		source: "direct",
		createdAt: new Date().toISOString(),
	};
}

function pendingIds(request: ShareRequest): Set<string> {
	return new Set(request.recipients.map((recipient) => `${PENDING}${recipientKey(recipient)}`));
}

/**
 * The list with the grants the control plane made: each replaces its row in
 * place (a pending row, or the same subject's older grant), the rest go at
 * the end, and rows in `drop` that nothing replaced go away.
 */
export function withCreatedEntries(data: CloudCpShareAccess, created: CloudCpShareEntry[], drop: ReadonlySet<string>): CloudCpShareAccess {
	const byRow = new Map(created.map((entry) => [entryRowKey(entry), entry]));
	const placed = new Set<string>();
	const entries = data.entries.flatMap((entry) => {
		const next = byRow.get(entryRowKey(entry));
		if (next && !placed.has(next.grantId)) {
			placed.add(next.grantId);
			return [next];
		}
		return drop.has(entry.grantId) ? [] : [entry];
	});
	return { ...data, entries: [...entries, ...created.filter((entry) => !placed.has(entry.grantId))] };
}

function withoutGrants(data: AccessData | undefined, grantIds: ReadonlySet<string>): AccessData | undefined {
	return data ? { ...data, entries: data.entries.filter((entry) => !grantIds.has(entry.grantId)) } : data;
}

/**
 * Shares this cloud session, or its whole project, with teams and people,
 * and lists everyone who has access before asking for anything. Picking
 * someone turns the dialog into a short confirm step that names what will
 * happen to whom. Links stay below, under Anyone with the link. Against a
 * control plane from before teams the dialog is links only, as it was then.
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
	// A project belongs to its workspace: the access list names it on top.
	const { org } = useCloudOrg(open);
	const workspaceName = org?.id === orgId ? org.displayName : undefined;
	const queryClient = useQueryClient();
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const [scope, setScope] = useState<ShareScope>("session");
	const [staged, setStaged] = useState<Recipient[]>([]);
	const [stagedRole, setStagedRole] = useState<CloudCpShareRole>("viewer");
	const [stagedCap, setStagedCap] = useState<ShareModeCap>("read-only");
	const [newTeamFor, setNewTeamFor] = useState<{ people: Recipient[]; name: string } | null>(null);
	const fieldInputRef = useRef<HTMLInputElement>(null);
	const contentRef = useRef<HTMLDivElement>(null);
	// After a row is stopped, focus goes to the next row's menu (its row key), or the field.
	const focusAfter = useRef<{ stopped: string; target: string } | null>(null);
	const openRef = useRef(open);
	openRef.current = open;
	const scopeRef = useRef(scope);
	scopeRef.current = scope;
	const undoNotice = useUndoNotice(open);

	const scopeSession = scope === "session" ? sessionId : undefined;
	const accessKey = shareAccessQueryKey(baseUrl, orgId, projectId, scopeSession);
	const access = useQuery({
		queryKey: accessKey,
		enabled: open && ready,
		retry: retryCloudRead,
		queryFn: async (): Promise<AccessData> => {
			try {
				return await client.getShareAccess(orgId, projectId, scopeSession);
			} catch (error) {
				if (teamsUnavailable(error)) return null;
				throw error;
			}
		},
	});
	// Another session's dialog already found this control plane without teams:
	// open links only at once instead of showing the people field first.
	const knownUnavailable = useMemo(
		() => open && queryClient.getQueryCache().findAll({ queryKey: ["cloud-share-access", baseUrl] }).some((query) => query.state.data === null),
		[baseUrl, open, queryClient],
	);
	const peopleAvailable = access.data === undefined ? !knownUnavailable : access.data !== null;
	const links = useActiveShareLinks({ orgId, projectId, sessionId, scope, enabled: open });
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

	const settle = () => {
		if (queryClient.isMutating({ mutationKey: ACCESS_CHANGE }) <= 1) invalidateTeamsAndShares(queryClient);
	};
	const share = useMutation({
		mutationKey: ACCESS_CHANGE,
		mutationFn: (request: ShareRequest) =>
			client.createShareGrants(orgId, projectId, {
				...(request.sessionId ? { sessionId: request.sessionId } : {}),
				subjects: request.recipients.map((recipient) => (recipient.kind === "team" ? { teamId: recipient.teamId } : { email: recipient.email })),
				role: request.role,
				modeCap: request.modeCap,
			}),
		// The rows show at once; each becomes its grant when the control plane answers.
		onMutate: async (request) => {
			await queryClient.cancelQueries({ queryKey: request.key });
			queryClient.setQueryData<AccessData>(request.key, (data) =>
				data ? { ...data, entries: [...data.entries, ...request.recipients.map((recipient) => pendingEntry(recipient, request, projectId))] } : data,
			);
			return { key: request.key };
		},
		onSuccess: ({ entries: created }, request) => {
			queryClient.setQueryData<AccessData>(request.key, (data) => (data ? withCreatedEntries(data, created, pendingIds(request)) : data));
		},
		onError: (_error, request) => {
			queryClient.setQueryData<AccessData>(request.key, (data) => withoutGrants(data, pendingIds(request)));
			// The picks come back with the access they were given, under the error.
			if (openRef.current && scopeRef.current === request.scope) {
				setStaged((current) => [...request.recipients.filter((recipient) => !current.some((other) => recipientKey(other) === recipientKey(recipient))), ...current]);
				setStagedRole(request.role);
				setStagedCap(request.modeCap);
			}
		},
		onSettled: settle,
	});
	const update = useMutation({
		mutationKey: ACCESS_CHANGE,
		mutationFn: ({ entry, role, modeCap }: { key: AccessKey; entry: CloudCpShareEntry; role: CloudCpShareRole; modeCap: ShareModeCap }) =>
			client.updateShareEntry(orgId, projectId, entry.grantId, { role, modeCap }),
		onMutate: async ({ key, entry, role, modeCap }) => {
			await queryClient.cancelQueries({ queryKey: key });
			const previous = queryClient.getQueryData<AccessData>(key);
			queryClient.setQueryData<AccessData>(key, (data) =>
				data ? { ...data, entries: data.entries.map((other) => (other.grantId === entry.grantId ? { ...other, role, modeCap } : other)) } : data,
			);
			// An expanded team's people show the change at once too; the refetch
			// when it is saved settles anyone with access another way.
			const members = entry.subject.kind === "team" && entry.subject.teamId
				? withTeamAccess(queryClient, ["cloud-team-share-access", baseUrl, orgId, projectId], entry.subject.teamId, role, modeCap)
				: [];
			return { key, previous, members };
		},
		onError: (_error, { entry }, context) => {
			for (const [membersKey, data] of context?.members ?? []) queryClient.setQueryData(membersKey, data);
			// Put back only what this change replaced, in the list it changed.
			const before = context?.previous?.entries.find((other) => other.grantId === entry.grantId);
			if (!context || !before) return;
			queryClient.setQueryData<AccessData>(context.key, (data) =>
				data ? { ...data, entries: data.entries.map((other) => (other.grantId === entry.grantId ? before : other)) } : data,
			);
		},
		onSettled: settle,
	});
	const restore = useMutation({
		mutationKey: ACCESS_CHANGE,
		mutationFn: ({ entry, subject }: { key: AccessKey; entry: CloudCpShareEntry; subject: Subject; index: number }) =>
			client.createShareGrants(orgId, projectId, {
				...(entry.sessionId ? { sessionId: entry.sessionId } : {}),
				subjects: [subject],
				role: entry.role,
				modeCap: entry.modeCap ?? defaultCap(entry.role),
				...(entry.deniedCommands?.length ? { deniedCommands: entry.deniedCommands } : {}),
			}),
		// Back where it was at once; the new grant replaces it when the control plane answers.
		onMutate: async ({ key, entry, index }) => {
			await queryClient.cancelQueries({ queryKey: key });
			queryClient.setQueryData<AccessData>(key, (data) => {
				if (!data || data.entries.some((other) => entryRowKey(other) === entryRowKey(entry))) return data;
				const entries = [...data.entries];
				entries.splice(Math.min(Math.max(index, 0), entries.length), 0, entry);
				return { ...data, entries };
			});
		},
		onSuccess: ({ entries: created }, { key, entry }) => {
			queryClient.setQueryData<AccessData>(key, (data) => (data ? withCreatedEntries(data, created, new Set([entry.grantId])) : data));
		},
		onError: (_error, { key, entry }) => {
			queryClient.setQueryData<AccessData>(key, (data) => withoutGrants(data, new Set([entry.grantId])));
		},
		onSettled: settle,
	});
	const stop = useMutation({
		mutationKey: ACCESS_CHANGE,
		mutationFn: ({ entry }: { key: AccessKey; entry: CloudCpShareEntry }) => client.revokeShareGrant(orgId, projectId, entry.grantId),
		onMutate: async ({ key, entry }) => {
			await queryClient.cancelQueries({ queryKey: key });
			const previous = queryClient.getQueryData<AccessData>(key);
			queryClient.setQueryData<AccessData>(key, (data) => withoutGrants(data, new Set([entry.grantId])));
			return { key, previous, index: previous?.entries.findIndex((other) => other.grantId === entry.grantId) ?? -1 };
		},
		onError: (_error, { entry }, context) => {
			if (!context || context.index < 0) return;
			queryClient.setQueryData<AccessData>(context.key, (data) => {
				if (!data || data.entries.some((other) => other.grantId === entry.grantId)) return data;
				const entries = [...data.entries];
				entries.splice(Math.min(context.index, entries.length), 0, entry);
				return { ...data, entries };
			});
		},
		// Undo is offered once the share is gone: offered sooner, it could race the stop.
		onSuccess: (_result, { key, entry }, context) => {
			const subject = subjectInput(entry);
			undoNotice.show({
				message: t("share.stopped", { name: subjectName(entry) }),
				...(subject ? { undo: () => restore.mutateAsync({ key, entry, subject, index: context?.index ?? -1 }) } : {}),
			});
		},
		onSettled: settle,
	});

	const resetStaging = () => {
		setStaged([]);
		setStagedRole("viewer");
		setStagedCap("read-only");
		share.reset();
	};
	// Closing forgets everything but the shares themselves, so the dialog
	// opens fresh: no old errors, no picks, the default access of Watch.
	useEffect(() => {
		if (open) return;
		setStaged([]);
		setStagedRole("viewer");
		setStagedCap("read-only");
		setScope("session");
		setNewTeamFor(null);
		share.reset();
		update.reset();
		stop.reset();
		restore.reset();
		undoNotice.clearFailure();
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [open]);
	const switchScope = (next: ShareScope) => {
		if (next === scope) return;
		setScope(next);
		resetStaging();
	};
	const submitShare = () => {
		if (!ready || staged.length === 0 || share.isPending) return;
		const request: ShareRequest = { key: accessKey, scope, sessionId: scopeSession, recipients: staged, role: stagedRole, modeCap: stagedCap };
		// Cleared before the request, so a second Enter has nothing to send.
		resetStaging();
		undoNotice.clearFailure();
		share.mutate(request);
		fieldInputRef.current?.focus();
	};
	const cancelStaging = () => {
		resetStaging();
		fieldInputRef.current?.focus();
	};
	const stopEntry = (entry: CloudCpShareEntry) => {
		const rows = entries.filter((other) => !(scope === "session" && other.scope === "project"));
		const at = rows.findIndex((other) => other.grantId === entry.grantId);
		const next = rows[at + 1] ?? rows[at - 1];
		focusAfter.current = { stopped: entry.grantId, target: next ? entryRowKey(next) : "field" };
		undoNotice.clearFailure();
		stop.mutate({ key: accessKey, entry });
	};
	// Moves focus once the stopped row has left the list.
	useLayoutEffect(() => {
		const pending = focusAfter.current;
		if (!pending || entries.some((entry) => entry.grantId === pending.stopped)) return;
		focusAfter.current = null;
		const row = [...(contentRef.current?.querySelectorAll<HTMLElement>("[data-share-row]") ?? [])].find((element) => element.dataset.shareRow === pending.target);
		const menu = row?.querySelector<HTMLButtonElement>("[data-access-menu]");
		if (menu && !menu.disabled) menu.focus();
		else fieldInputRef.current?.focus();
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [access.data]);

	const staging = staged.length > 0;
	const peopleError = share.error ?? update.error ?? stop.error ?? access.error;
	const error = peopleError ? (peopleError instanceof Error ? peopleError.message : String(peopleError)) : undoNotice.failed ? t("share.undoFailed") : null;

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent
				ref={contentRef}
				showCloseButton={false}
				className={cn(settingsDialogContentClass, "w-[min(540px,calc(100vw-24px))]")}
				onClick={(event) => event.stopPropagation()}
				// An Undo handed to the global toast keeps the dialog open when used.
				onInteractOutside={(event) => {
					if (event.target instanceof Element && event.target.closest("[data-global-toast]")) event.preventDefault();
				}}
				// Opens ready to type a name; links only, the first control, as before teams.
				onOpenAutoFocus={(event) => {
					if (!peopleAvailable) return;
					event.preventDefault();
					fieldInputRef.current?.focus();
				}}
				onCloseAutoFocus={restoreFocusOnClose}
				data-share-session-dialog
			>
				<DialogClose asChild>
					<CloseButton floating aria-label={t("confirm.close")} />
				</DialogClose>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 p-5 pr-12 pb-0")}>
					<DialogTitle className="settings-dialog-title text-subtitle">{scope === "project" ? t("share.projectTitle") : t("share.title")}</DialogTitle>
					{peopleAvailable ? (
						<DialogDescription className={cn("text-control leading-5 text-settings-muted", scope === "project" ? "" : "sr-only")}>
							{scope === "project" ? t("share.projectPeopleDescription") : t("share.peopleDescription")}
						</DialogDescription>
					) : (
						<DialogDescription className="text-control leading-5 text-settings-muted">
							{scope === "project" ? t("share.projectDescription") : t("share.description")}
						</DialogDescription>
					)}
				</div>
				<div className={cn(settingsDialogBodyClass, "gap-3.5 px-5 pt-4 pb-5")}>
					<RadioGroup.Root
						value={scope}
						onValueChange={(value) => switchScope(value as ShareScope)}
						aria-label={t("share.scope")}
						className="inline-flex gap-0.5 self-start rounded-md bg-(--color-bg-settings-input) p-0.5"
						data-share-scope=""
					>
						{(["session", "project"] as const).map((value) => (
							<RadioGroup.Item
								key={value}
								value={value}
								className="h-6.5 rounded-sm px-2.5 text-[12.5px] text-muted-foreground outline-none transition-colors duration-(--duration-fast) hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring data-[state=checked]:bg-foreground/10 data-[state=checked]:text-foreground"
							>
								{value === "session" ? t("share.scopeSession") : t("share.scopeProject")}
							</RadioGroup.Item>
						))}
					</RadioGroup.Root>
					{peopleAvailable ? (
						<>
							<PeopleField
								orgId={orgId}
								value={staged}
								inputRef={fieldInputRef}
								onChange={(next) => {
									setStaged(next);
									undoNotice.clearFailure();
									if (!share.isPending) share.reset();
								}}
								unavailable={unavailable}
								workspaceHasAccess
								placeholder={t("share.addPeopleOrTeams")}
								ariaLabel={t("share.addPeopleOrTeams")}
								onNewTeam={(people, typed) => {
									// A typed email joins the new team; other text names it.
									const email = looksLikeEmail(typed) ? typed.toLowerCase() : "";
									const picked = people.filter((recipient) => recipient.kind === "person");
									setNewTeamFor({
										people: email && !picked.some((recipient) => recipient.kind === "person" && recipient.email === email) ? [...picked, { kind: "person", email }] : picked,
										name: email ? "" : typed,
									});
								}}
								onSubmit={submitShare}
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
							{error ? <p role="alert" className="-mt-1.5 text-caption leading-4 text-error">{error}</p> : null}
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
								<AccessList
									orgId={orgId}
									projectId={projectId}
									sessionId={scopeSession}
									scope={scope}
									access={access.data ?? undefined}
									loading={access.isLoading}
									hasLink={(links.data?.length ?? 0) > 0}
									me={myEmail}
									workspaceName={workspaceName}
									onChange={(entry, next) => update.mutate({ key: accessKey, entry, ...next })}
									onStop={stopEntry}
									onShowProject={() => switchScope("project")}
								/>
							)}
						</>
					) : null}
					{/* Stays mounted through the confirm step so a link made a moment ago,
					    shown only once, is still there after; a new scope starts afresh. */}
					<div hidden={staging} className="flex flex-col">
						<ShareLinksSection key={scope} orgId={orgId} projectId={projectId} sessionId={sessionId} scope={scope} open={open} />
					</div>
					<UndoNotice
						notice={undoNotice.shown}
						onUndo={() => {
							undoNotice.undo();
							fieldInputRef.current?.focus();
						}}
						onDismiss={undoNotice.dismiss}
					/>
				</div>
				{staging ? (
					<div className="flex items-center justify-end gap-2 border-t border-border px-5 py-3.5" data-share-footer="">
						<Button type="button" variant="ghost" onClick={cancelStaging}>
							{t("confirm.cancel")}
						</Button>
						<Button type="button" variant="primary" onClick={submitShare} disabled={!ready || share.isPending} data-share-submit="">
							{share.isPending ? t("share.sharing") : t("share.shareAction")}
						</Button>
					</div>
				) : null}
				{newTeamFor ? (
					<NewTeamDialog
						orgId={orgId}
						open
						initialPeople={newTeamFor.people}
						initialName={newTeamFor.name}
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

function AccessList({ orgId, projectId, sessionId, scope, access, loading, hasLink, me, workspaceName, onChange, onStop, onShowProject }: {
	orgId: string;
	projectId: string;
	sessionId: string | undefined;
	scope: ShareScope;
	access: CloudCpShareAccess | undefined;
	loading: boolean;
	/** A live link opens it too, so "only people in this workspace" would be wrong. */
	hasLink: boolean;
	me: string | undefined;
	/** The workspace that owns the project, shown on top of a project's list. */
	workspaceName?: string;
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
							<Skeleton className="size-6 rounded-full" />
							<Skeleton className="h-3 w-40 rounded" />
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
					{scope === "project"
						? hasLink ? t("share.projectNotSharedLink") : t("share.projectNotShared")
						: hasLink ? t("share.notSharedLink") : t("share.notShared")}
				</p>
			</div>
		);
	}
	return (
		<div className="flex flex-col gap-1.5">
			<p className="text-xs text-muted-foreground">{t("share.peopleWithAccess")}</p>
			<ul className="flex flex-col divide-y divide-border rounded-md bg-muted/40 py-0.5" data-share-access-list="">
				{/* A session's top row is its owner; a project has none of its own, it is the workspace's. */}
				{!owner && scope === "project" ? (
					<li className="flex min-h-11 items-center gap-2.5 py-1.5 pr-2 pl-2.5" data-share-workspace-row="">
						<TeamTile name={workspaceName || "W"} />
						<span className="min-w-0 flex-1 truncate text-control text-foreground">
							{workspaceName ? t("share.workspacePeople", { name: workspaceName }) : t("share.workspacePeopleUnnamed")}
						</span>
						<span className="pr-1 text-control text-passive">{t("share.owner")}</span>
					</li>
				) : null}
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
						key={entryRowKey(entry)}
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
	// Shared a moment ago: shown at once, changed once the grant exists.
	const pending = entry.grantId.startsWith(PENDING);
	return (
		<li
			className={cn("flex flex-col transition-opacity duration-(--duration-fast)", pending && "opacity-70")}
			aria-busy={pending || undefined}
			data-share-entry={entry.subject.kind}
			data-share-entry-scope={entry.scope}
			data-share-row={entryRowKey(entry)}
		>
			<div className="flex min-h-11 items-center gap-2.5 py-1.5 pr-1.5 pl-2.5">
				{team ? <TeamTile name={name} /> : <PersonAvatar name={entry.subject.name} email={entry.subject.email} invited={!entry.subject.name} />}
				<div className="min-w-0 flex-1">
					<p className="truncate text-control text-foreground">{name}</p>
					{/* Only the email shortens: "from the whole project" stays whole, so it can be clicked. */}
					<p className="flex min-w-0 items-baseline text-xs whitespace-nowrap text-muted-foreground">
						<span className="min-w-0 truncate">{sub}</span>
						{fromProject ? (
							<>
								<span className="shrink-0">{"\u00a0·\u00a0"}</span>
								<Button type="button" variant="link" className="shrink-0 no-underline hover:underline" onClick={onShowProject} data-share-from-project="">
									{t("share.fromProject")}
								</Button>
							</>
						) : entry.source === "link" ? <span className="shrink-0">{`\u00a0·\u00a0${t("share.fromLink")}`}</span> : null}
					</p>
				</div>
				{fromProject ? (
					<Tooltip>
						<TooltipTrigger asChild>
							<span className="px-2 text-control whitespace-nowrap text-muted-foreground" tabIndex={0}>
								{roleLabel(t, entry.role, "project")}
							</span>
						</TooltipTrigger>
						<TooltipContent>{t("share.changeInProject")}</TooltipContent>
					</Tooltip>
				) : (
					<AccessMenu
						role={entry.role}
						modeCap={modeCap}
						scope={scope}
						onChange={onChange}
						onStop={onStop}
						stopLabel={t("share.stopSharingWith", { name })}
						disabled={pending}
					/>
				)}
				{team ? (
					<Pressable
						className="grid size-6 shrink-0 place-items-center rounded-sm text-passive hover:bg-interactive-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
						aria-expanded={expanded}
						aria-label={expanded ? t("share.hideMembers", { name }) : t("share.showMembers", { name })}
						onClick={() => setExpanded((value) => !value)}
						data-share-expand=""
					>
						<ChevronRight className={cn("size-3.5 transition-transform duration-(--duration-normal)", expanded && "rotate-90")} aria-hidden="true" />
					</Pressable>
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
		retry: retryCloudRead,
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
			retry: retryCloudRead,
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

