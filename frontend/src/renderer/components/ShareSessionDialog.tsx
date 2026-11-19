import { type QueryClient, useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, ChevronRight, Info, Settings2 } from "@/components/app-icons";
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
	useWorkspaceMemberCount,
} from "../hooks/useTeams";
import type {
	CloudCpShareAccess,
	CloudCpShareEntry,
	CloudCpShareLink,
	CloudCpShareRole,
	CloudCpTeamMemberAccess,
} from "../lib/cloud-cp";
import { ConfirmDialog } from "./ConfirmDialog";
import { AccessMenu, CapMenu } from "./share/AccessMenu";
import { defaultCap, effectiveAccessText, listNames, roleLabel, type ShareModeCap, type ShareScope } from "./share/accessLabels";
import { canCopy, CopyLinkButton } from "./share/CopyLinkButton";
import { GeneralAccessCard } from "./share/GeneralAccessCard";
import { LinkSettings } from "./share/LinkSettings";
import {
	actualLinkCap,
	anyoneSentence,
	DEFAULT_EXPIRY,
	isPersonalWorkspace,
	type LinkAccess,
	LinkRevokeError,
	linkErrorMessage,
	restrictedSentence,
	type ShareExpiry,
	useLinkAccess,
	workspaceDisplayName,
} from "./share/linkAccess";
import { UndoNotice, useUndoNotice } from "./share/UndoNotice";
import { SettingsSection } from "./settings/SettingsSection";
import { NewTeamDialog } from "./teams/NewTeamDialog";
import { PeopleField, recipientKey, type Recipient } from "./teams/PeopleField";
import { PersonAvatar, TeamTile } from "./teams/TeamVisuals";
import { Button } from "./ui/button";
import { Checkbox } from "./ui/checkbox";
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
export type { ShareExpiry } from "./share/linkAccess";
export { shareLinkRequest, shareLinksQueryKey } from "./share/linkAccess";

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

/** Confirmation the link card asks for before a change it can't undo. */
type LinkConfirm =
	| { kind: "off" }
	/** copy: Copy link asked, for a Trusted link made elsewhere, which only a new link can stand in for. */
	| { kind: "change"; role: CloudCpShareRole; modeCap: ShareModeCap; copy?: boolean }
	/** Copy new link for a link made elsewhere that can write: another link that can write. */
	| { kind: "copy-new" }
	| { kind: "revoke"; link: CloudCpShareLink };

/** Where Link settings was opened from, to go back to when it closes. */
const SETTINGS_OPENERS = { gear: "[data-share-settings]", card: "[data-share-link-settings]" } as const;
type SettingsOpener = keyof typeof SETTINGS_OPENERS;

/** The link a confirmation is about, as the links last read have it. */
function confirmLink(confirm: Extract<LinkConfirm, { kind: "revoke" }>, links: LinkAccess): CloudCpShareLink {
	return links.links.find((link) => link.id === confirm.link.id) ?? confirm.link;
}

/** How many people joined by the links a confirmation would turn off. */
function confirmJoined(confirm: LinkConfirm, links: LinkAccess): number {
	return confirm.kind === "revoke" ? confirmLink(confirm, links).joinedCount ?? 0 : links.joined;
}

/** Whether a confirmation offers to remove the people who joined: a link stops, the control plane can, and someone did. */
function offersRemoveJoined(confirm: LinkConfirm, links: LinkAccess): boolean {
	return confirm.kind !== "copy-new" && links.canRemoveJoined && confirmJoined(confirm, links) > 0;
}

/**
 * Whether a change lets anyone with the link write where they couldn't: to a
 * Can write link from a watch-only one, or from Read only agent permissions
 * to Standard. It asks first, naming the sandbox and its secrets.
 */
function widensToWrite(link: Pick<CloudCpShareLink, "role" | "modeCap"> | undefined, next: { role: CloudCpShareRole; modeCap: ShareModeCap }): boolean {
	if (next.role !== "editor") return false;
	if (link?.role !== "editor") return true;
	return actualLinkCap(link.role, link.modeCap) === "read-only" && actualLinkCap(next.role, next.modeCap) !== "read-only";
}

/**
 * Everything sharing holds for one session or its whole project, the way
 * Google Docs does it: people and teams on top (named, with what each can
 * do), then General access, one sentence that says who else the link lets
 * in, then Copy link. Picking someone turns it into a short confirm step that
 * names what will happen to whom. Against a control plane from before teams,
 * or in a build without Teams, it is General access and Copy link only.
 * Without a session it shares the whole project only. SharePanel draws it in
 * the share dialog and in Project settings › Sharing.
 */
function useShareController({ active: open, orgId, projectId, sessionId, teams, standaloneAgent = false }: {
	/** Whether the surface is showing; closing it forgets everything but the shares themselves. */
	active: boolean;
	orgId: string;
	projectId: string;
	/** The session to share; without one only the whole project. */
	sessionId?: string;
	/** Sharing with people and teams (Teams builds); off, links only. */
	teams: boolean;
	/** A Cloud standalone agent: its owner's alone, so the workspace's people reach it only when added. */
	standaloneAgent?: boolean;
}) {
	const { t, i18n } = useTranslation();
	const { client, baseUrl, ready } = useCloudCp();
	const me = useCloudSession().session?.user;
	const myEmail = me?.email?.toLowerCase() ?? "";
	// A project belongs to its workspace: the access list names it on top.
	// Undefined while it loads or when it couldn't be read.
	const { org } = useCloudOrg(open);
	const workspace = org?.id === orgId ? org : undefined;
	// Its name only when it reads as one: never an email.
	const workspaceName = workspaceDisplayName(workspace?.displayName);
	// Nobody else in a personal workspace, and nobody else in the workspace can
	// open a standalone agent: only its owner and the people added.
	const personal = standaloneAgent || isPersonalWorkspace(workspace);
	// How many are in a team's workspace. With nobody but you in it, it grants
	// nobody anything, so it reads as a personal one. Unknown (loading, or
	// unreadable) is never alone: that would understate who can open it.
	const memberCount = useWorkspaceMemberCount(orgId, open && teams && Boolean(workspace) && !personal);
	const solo = personal || (memberCount.data !== undefined && memberCount.data <= 1);
	const queryClient = useQueryClient();
	// Opened for a project rather than a session: there is no session to go back to.
	// The same dialog can be handed a session again (the header follows the
	// session on screen), so the scope follows it rather than its first one.
	const projectOnly = !sessionId;
	const [chosenScope, setScope] = useState<ShareScope>("session");
	const scope: ShareScope = projectOnly ? "project" : chosenScope;
	const [view, setView] = useState<"main" | "settings">("main");
	const [staged, setStaged] = useState<Recipient[]>([]);
	const [stagedRole, setStagedRole] = useState<CloudCpShareRole>("viewer");
	const [stagedCap, setStagedCap] = useState<ShareModeCap>("read-only");
	const [newTeamFor, setNewTeamFor] = useState<{ people: Recipient[]; name: string } | null>(null);
	const [expiry, setExpiry] = useState<ShareExpiry>(DEFAULT_EXPIRY);
	const [confirm, setConfirm] = useState<LinkConfirm | null>(null);
	const [removeJoined, setRemoveJoined] = useState(false);
	// What the polite live region last said: a copy, with who the link lets in.
	const [announcement, setAnnouncement] = useState("");
	const fieldInputRef = useRef<HTMLInputElement>(null);
	const copyButtonRef = useRef<HTMLButtonElement>(null);
	const contentRef = useRef<HTMLDivElement>(null);
	const backButtonRef = useRef<HTMLButtonElement>(null);
	// The control that opened Link settings, which gets focus back when it closes.
	const settingsOpener = useRef<SettingsOpener>("gear");
	// After a link is revoked in Link settings, focus goes to the next row's Revoke (its place), or Back.
	const revokeFocus = useRef<{ id: string; index: number } | null>(null);
	// A link whose revoke is reading the links first, to count who joined by it.
	const [checkingRevoke, setCheckingRevoke] = useState<string | null>(null);
	// Copy link waiting on a confirmation: one that replaces a Trusted link, or makes another that can write.
	const pendingCopy = useRef<((url: string | undefined) => void) | null>(null);
	const settlePendingCopy = (url: string | undefined) => {
		const resolve = pendingCopy.current;
		pendingCopy.current = null;
		resolve?.(url);
	};
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
		enabled: open && ready && teams,
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
	const peopleAvailable = teams && (access.data === undefined ? !knownUnavailable : access.data !== null);
	const links = useLinkAccess({ orgId, projectId, sessionId: sessionId ?? "", scope, enabled: open });
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
				message: t("share.removed", { name: subjectName(entry) }),
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
	// opens fresh: no old errors, no picks, the default access of Can watch.
	useEffect(() => {
		if (open) return;
		setStaged([]);
		setStagedRole("viewer");
		setStagedCap("read-only");
		setScope("session");
		setView("main");
		setConfirm(null);
		setAnnouncement("");
		setNewTeamFor(null);
		share.reset();
		update.reset();
		stop.reset();
		restore.reset();
		for (const mutation of [links.turnOn, links.turnOff, links.replace, links.copyNew, links.revoke]) mutation.reset();
		revokeFocus.current = null;
		setCheckingRevoke(null);
		settlePendingCopy(undefined);
		undoNotice.clearFailure();
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [open]);
	const switchScope = (next: ShareScope) => {
		if (next === scope) return;
		setScope(next);
		setView("main");
		resetStaging();
	};
	const openSettings = (from: SettingsOpener) => {
		settingsOpener.current = from;
		setView("settings");
	};
	const closeSettings = () => setView("main");
	// Focus follows the view: into Link settings on Back, and out of it to
	// what opened it, so it never drops to the page behind the dialog.
	const shownView = useRef(view);
	useLayoutEffect(() => {
		const was = shownView.current;
		shownView.current = view;
		if (!open || was === view) return;
		if (view === "settings") {
			backButtonRef.current?.focus();
			return;
		}
		const content = contentRef.current;
		const opener = content?.querySelector<HTMLElement>(SETTINGS_OPENERS[settingsOpener.current]) ?? content?.querySelector<HTMLElement>(SETTINGS_OPENERS.gear);
		opener?.focus();
	}, [open, view]);
	// Once a revoked link's row has gone, focus the Revoke now in its place, else the one above, else Back.
	const linkIds = links.links.map((link) => link.id).join(" ");
	useLayoutEffect(() => {
		const pending = revokeFocus.current;
		if (!pending || view !== "settings" || links.links.some((link) => link.id === pending.id)) return;
		revokeFocus.current = null;
		const revokes = [...(contentRef.current?.querySelectorAll<HTMLElement>("[data-share-revoke]") ?? [])];
		(revokes[Math.min(pending.index, revokes.length - 1)] ?? backButtonRef.current)?.focus();
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [linkIds, view]);
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
	const ordered = useMemo(() => orderEntries(entries, scope), [entries, scope]);
	const stopEntry = (entry: CloudCpShareEntry) => {
		const rows = ordered.filter((other) => !(scope === "session" && other.scope === "project"));
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

	// General access. Turning link access on makes a Can watch link; anything
	// that widens it, turns it off, or replaces a link asks first.
	const restricted = restrictedSentence(t, { scope, workspaceKnown: Boolean(workspace), solo, people: peopleAvailable });
	const turnOnLinks = () => {
		undoNotice.clearFailure();
		links.turnOn.mutate({ role: "viewer", modeCap: "read-only", expiry });
	};
	// Each confirmation that counts who joined by a link reads the links again
	// first: someone may have joined since the dialog opened. It can't be
	// confirmed until the list is back (links.fetching).
	const askTurnOff = () => {
		setRemoveJoined(false);
		setConfirm({ kind: "off" });
		void links.refresh();
	};
	const askChange = (next: { role: CloudCpShareRole; modeCap: ShareModeCap }, copy = false) => {
		// Removing the people who joined is only ever chosen, never ticked for
		// them: someone who joined by link alone would lose access outright.
		setRemoveJoined(false);
		setConfirm({ kind: "change", ...next, ...(copy ? { copy } : {}) });
		void links.refresh();
	};
	/**
	 * Copy link that has to ask first. A Trusted link made elsewhere: a new
	 * link can't be Trusted, so it replaces it with a Can write one. Any other
	 * that can write: Copy new link makes another link that can write, which
	 * asks as turning writing on does. Resolves to the URL to copy, or undefined.
	 */
	const askCopy = () => new Promise<string | undefined>((resolve) => {
		settlePendingCopy(undefined);
		pendingCopy.current = resolve;
		if (links.copy.mode === "replace") {
			askChange({ role: "editor", modeCap: "standard" }, true);
		} else {
			setRemoveJoined(false);
			setConfirm({ kind: "copy-new" });
		}
	});
	const askRevoke = async (link: CloudCpShareLink, index: number) => {
		revokeFocus.current = { id: link.id, index };
		setCheckingRevoke(link.id);
		let fresh: CloudCpShareLink[];
		try {
			fresh = await links.refresh();
		} finally {
			setCheckingRevoke(null);
		}
		const current = fresh.find((other) => other.id === link.id);
		// Gone already: the list now says so.
		if (!current) return;
		// A link nobody joined by stops at once; otherwise the owner chooses about them.
		if (typeof current.joinedCount !== "number" || current.joinedCount === 0) {
			links.revoke.mutate({ link: current, removeJoined: false }, { onError: () => { revokeFocus.current = null; } });
			return;
		}
		setRemoveJoined(false);
		setConfirm({ kind: "revoke", link: current });
	};
	const cancelConfirm = () => {
		if (confirm?.kind === "revoke") revokeFocus.current = null;
		if ((confirm?.kind === "change" && confirm.copy) || confirm?.kind === "copy-new") settlePendingCopy(undefined);
		setConfirm(null);
	};
	const runConfirm = () => {
		const current = confirm;
		if (!current) return;
		// Only what was offered: a control plane that can't remove them is never asked to.
		const remove = removeJoined && offersRemoveJoined(current, links);
		setConfirm(null);
		undoNotice.clearFailure();
		if (current.kind === "off") {
			const count = links.links.length;
			links.turnOff.mutate({ removeJoined: remove }, {
				// Who lost access as the control plane counted it, not as the dialog last read it.
				onSuccess: (removed) => undoNotice.show({
					message: remove && removed !== undefined && removed > 0
						? t("share.general.turnedOffRemoved", { count: removed })
						: t("share.general.turnedOff", { count }),
				}),
			});
		} else if (current.kind === "copy-new") {
			links.copyNew.mutate(undefined, {
				onSuccess: (url) => settlePendingCopy(url),
				onError: () => settlePendingCopy(undefined),
			});
		} else if (current.kind === "change") {
			links.replace.mutate({ role: current.role, modeCap: current.modeCap, removeJoined: remove }, {
				onSuccess: (url) => {
					if (current.copy) settlePendingCopy(url);
				},
				// The new link exists even when an old one couldn't be turned off; it can still be copied.
				onError: (failure) => {
					if (current.copy) settlePendingCopy(failure instanceof LinkRevokeError ? failure.url : undefined);
				},
			});
		} else {
			links.revoke.mutate({ link: confirmLink(current, links), removeJoined: remove }, { onError: () => { revokeFocus.current = null; } });
		}
	};
	const announceCopy = () => {
		const link = links.broadest;
		const sentence = link ? anyoneSentence(t, scope, link.role) : "";
		// Changing the text each time makes the region speak again for a second copy.
		setAnnouncement((before) => `${t("share.linkCopiedSentence", { sentence })}${before.endsWith("​") ? "" : "​"}`);
	};

	const staging = staged.length > 0;
	const linkError = links.turnOn.error ?? links.turnOff.error ?? links.replace.error ?? links.copyNew.error ?? links.revoke.error ?? links.error;
	const failure = share.error ?? update.error ?? stop.error ?? access.error;
	const error = failure
		? failure instanceof Error ? failure.message : String(failure)
		: linkError ? linkErrorMessage(t, linkError)
			: undoNotice.failed ? t("share.undoFailed") : null;

	// What Esc steps back out of before the surface closes: Link settings, then the confirm step.
	const canStepBack = view === "settings" || staging;
	const stepBack = (): boolean => {
		if (view === "settings") {
			closeSettings();
			return true;
		}
		if (staging) {
			cancelStaging();
			return true;
		}
		return false;
	};
	// Where focus starts: ready to type a name. Links only: on Copy link when it
	// can copy, else on General access; while that loads, on the first control.
	const initialFocus = (): HTMLElement | null => (
		peopleAvailable && fieldInputRef.current
			? fieldInputRef.current
			: canCopy(links) && copyButtonRef.current
				? copyButtonRef.current
				: contentRef.current?.querySelector<HTMLElement>("[data-general-access-menu]") ?? null
	);

	return {
		orgId, projectId, projectOnly, standaloneAgent, scope, scopeSession, switchScope, view, openSettings, closeSettings, staging, canStepBack, stepBack, initialFocus,
		contentRef, fieldInputRef, copyButtonRef, backButtonRef, ready, peopleAvailable, error, locale: i18n.language,
		staged, setStaged, stagedRole, setStagedRole, stagedCap, setStagedCap, share, unavailable, submitShare, cancelStaging, ownEntries, myEmail,
		access, ordered, workspaceName, memberCount, solo, me, update, accessKey, stopEntry, undoNotice, links, restricted, turnOnLinks, askTurnOff, askChange,
		expiry, setExpiry, askRevoke, checkingRevoke, announcement, announceCopy, askCopy, newTeamFor, setNewTeamFor,
		confirm, removeJoined, setRemoveJoined, runConfirm, cancelConfirm,
	};
}

type ShareController = ReturnType<typeof useShareController>;

/**
 * Shares this cloud session, or its whole project, in a dialog: the title,
 * where it is and the switch between the session and the whole project, Link
 * settings and Close on top, the SharePanel, then Copy link beside Done.
 * Without a session it shares the whole project only.
 */
export function ShareSessionDialog({ open, onOpenChange, orgId, projectId, sessionId, sessionName, projectName, people: teams = true, standaloneAgent = false }: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	orgId: string;
	projectId: string;
	/** The session to share; without one the dialog shares the whole project only. */
	sessionId?: string;
	/** The session's title, for "Share \"<title>\"". */
	sessionName?: string;
	/** The project's name, for "Session in <project>". */
	projectName?: string;
	/** Sharing with people and teams (Teams builds); off, the dialog is links only. */
	people?: boolean;
	/** A Cloud standalone agent's session: its project is the agent, so there is no whole project to share instead. */
	standaloneAgent?: boolean;
}) {
	const { t } = useTranslation();
	const share = useShareController({ active: open, orgId, projectId, sessionId, teams, standaloneAgent });
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const { scope, view, staging, projectOnly } = share;
	const title = scope === "project"
		? projectName ? t("share.projectTitleNamed", { name: projectName }) : t("share.projectTitle")
		: sessionName ? t("share.titleNamed", { name: sessionName }) : t("share.title");

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent
				ref={share.contentRef}
				showCloseButton={false}
				className={cn(settingsDialogContentClass, "w-[min(540px,calc(100vw-24px))]")}
				onClick={(event) => event.stopPropagation()}
				// An Undo handed to the global toast keeps the dialog open when used.
				onInteractOutside={(event) => {
					if (event.target instanceof Element && event.target.closest("[data-global-toast]")) event.preventDefault();
				}}
				// Focus must never stay on the page behind, where keys would reach the terminal.
				onOpenAutoFocus={(event) => {
					const target = share.initialFocus();
					if (!target) return;
					event.preventDefault();
					target.focus();
				}}
				onCloseAutoFocus={restoreFocusOnClose}
				onEscapeKeyDown={(event) => {
					if (share.stepBack()) event.preventDefault();
				}}
				data-share-session-dialog
			>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 px-5 pt-4 pb-0")}>
					<div className="flex items-center gap-1">
						{view === "settings" ? (
							<Button ref={share.backButtonRef} type="button" variant="quiet" size="icon-sm" className="-ml-1.5" aria-label={t("share.back")} onClick={share.closeSettings} data-share-back="">
								<ArrowLeft className="size-3.5" aria-hidden="true" />
							</Button>
						) : null}
						<DialogTitle className="settings-dialog-title min-w-0 flex-1 truncate text-subtitle" title={view === "settings" ? undefined : title}>
							{view === "settings" ? t("share.general.linkSettings") : title}
						</DialogTitle>
						{view === "main" && !staging ? <LinkSettingsButton onClick={() => share.openSettings("gear")} /> : null}
						<DialogClose asChild>
							<CloseButton size="icon-sm" aria-label={t("confirm.close")} />
						</DialogClose>
					</div>
					<DialogDescription asChild>
						<p className="flex min-w-0 flex-wrap items-baseline gap-x-1 text-xs leading-5 text-muted-foreground">
							{view === "settings" ? (
								<span>{scope === "project" ? t("share.settings.descriptionProject") : t("share.settings.description")}</span>
							) : (
								<>
									<span className="min-w-0 truncate">
										{scope === "project"
											? t("share.projectIncludes")
											: standaloneAgent
												? t("share.standaloneAgent")
												: projectName ? t("share.sessionIn", { project: projectName }) : t("share.sessionInProject")}
									</span>
									{projectOnly || standaloneAgent ? null : (
										<>
											<span aria-hidden="true">·</span>
											{/* A dotted underline, as Add Project's "Start without a repo", so it
											    reads as an action. Keyboard focus brightens it as hover does: the
											    app draws no focus rings (styles.css). */}
											<Button
												type="button"
												variant="link"
												className="text-xs font-medium text-muted-foreground decoration-dotted decoration-muted-foreground/60 underline-offset-4 hover:text-foreground hover:decoration-foreground focus-visible:text-foreground focus-visible:decoration-foreground"
												onClick={() => share.switchScope(scope === "project" ? "session" : "project")}
												data-share-switch-scope={scope === "project" ? "session" : "project"}
											>
												{scope === "project" ? t("share.backToSession") : t("share.shareProjectInstead")}
											</Button>
										</>
									)}
								</>
							)}
						</p>
					</DialogDescription>
				</div>
				<SharePanel share={share} layout="dialog" />
			</DialogContent>
		</Dialog>
	);
}

/**
 * The whole project's sharing as a page, for Project settings › Sharing:
 * the same SharePanel the share dialog shows for the project, with nothing to
 * close. Changes apply at once, as they do in the dialog.
 */
export function ProjectSharePanel({ orgId, projectId, people: teams = true }: {
	orgId: string;
	projectId: string;
	/** Sharing with people and teams (Teams builds); off, links only. */
	people?: boolean;
}) {
	const share = useShareController({ active: true, orgId, projectId, teams });
	return <SharePanel share={share} layout="inline" />;
}

/** The gear that opens Link settings. */
function LinkSettingsButton({ onClick }: { onClick: () => void }) {
	const { t } = useTranslation();
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<Button type="button" variant="quiet" size="icon-sm" aria-label={t("share.general.linkSettings")} onClick={onClick} data-share-settings="">
					<Settings2 className="size-3.5" aria-hidden="true" />
				</Button>
			</TooltipTrigger>
			<TooltipContent>{t("share.general.linkSettings")}</TooltipContent>
		</Tooltip>
	);
}

/**
 * Sharing's controls, from useShareController. In the dialog, its body and
 * footer: people, General access, then Copy link beside Done. Inline (Project
 * settings › Sharing), the same controls as settings sections: the people
 * field, People with access, then General access with Link settings and Copy
 * link beside its heading, and no Done. The confirm step and Link settings
 * take the panel's place in both, and Esc steps back out of them.
 */
function SharePanel({ share: controller, layout }: { share: ShareController; layout: "dialog" | "inline" }) {
	const { t } = useTranslation();
	const {
		orgId, projectId, scope, scopeSession, switchScope, view, openSettings, closeSettings, staging, canStepBack, stepBack,
		contentRef, fieldInputRef, copyButtonRef, backButtonRef, ready, peopleAvailable, error, locale,
		staged, setStaged, stagedRole, setStagedRole, stagedCap, setStagedCap, share, unavailable, submitShare, cancelStaging, ownEntries, myEmail,
		access, ordered, workspaceName, memberCount, solo, me, update, accessKey, stopEntry, undoNotice, links, restricted, turnOnLinks, askTurnOff, askChange,
		expiry, setExpiry, askRevoke, checkingRevoke, announcement, announceCopy, askCopy, newTeamFor, setNewTeamFor,
		confirm, removeJoined, setRemoveJoined, runConfirm, cancelConfirm,
	} = controller;
	// Inline, a link the clipboard refused shows under General access rather than beside Copy link in its heading.
	const [copyFallbackHost, setCopyFallbackHost] = useState<HTMLDivElement | null>(null);
	const inline = layout === "inline";

	const peopleField = (
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
			workspaceHasAccess={!controller.standaloneAgent}
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
	);
	const stagedSummary = (
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
			locale={locale}
			membersHaveAccess={!controller.standaloneAgent}
		/>
	);
	const accessList = (
		<AccessList
			orgId={orgId}
			projectId={projectId}
			sessionId={scopeSession}
			scope={scope}
			access={access.data ?? undefined}
			entries={ordered}
			// Counted with the list, so a workspace with only you in it never shows first.
			loading={access.isLoading || memberCount.isLoading}
			me={myEmail}
			myName={me?.displayName}
			workspaceName={workspaceName}
			memberCount={memberCount.data}
			solo={solo}
			label={!inline}
			onChange={(entry, next) => update.mutate({ key: accessKey, entry, ...next })}
			onStop={stopEntry}
			onShowProject={() => switchScope("project")}
		/>
	);
	const generalAccess = (
		<GeneralAccessCard
			scope={scope}
			access={links}
			restricted={restricted}
			locale={locale}
			label={!inline}
			onTurnOn={turnOnLinks}
			onTurnOff={askTurnOff}
			onChangeRole={askChange}
			onOpenSettings={() => openSettings("card")}
		/>
	);
	const copyLink = (
		<CopyLinkButton
			access={links}
			people={peopleAvailable}
			onCopied={announceCopy}
			onAsk={askCopy}
			buttonRef={copyButtonRef}
			{...(inline ? { size: "sm" as const, fallbackHost: copyFallbackHost } : {})}
		/>
	);
	const linkSettings = (
		<LinkSettings
			scope={scope}
			access={links}
			expiry={expiry}
			onExpiryChange={setExpiry}
			onRevoke={(link, index) => void askRevoke(link, index)}
			busy={links.revoke.isPending || checkingRevoke !== null}
			locale={locale}
		/>
	);
	const errorLine = (className?: string) => (error ? <p role="alert" className={cn("text-caption leading-4 text-error", className)}>{error}</p> : null);
	// Where "Notify people" goes once sharing sends email; for now they find it in the app.
	const inAppNotice = <p className="self-center text-xs text-muted-foreground" data-share-in-app="">{t("share.inAppNotice")}</p>;
	const stagingActions = (
		<div className="flex shrink-0 gap-2">
			<Button type="button" variant="secondary" size={inline ? "sm" : "default"} onClick={cancelStaging}>
				{t("confirm.cancel")}
			</Button>
			<Button type="button" variant="primary" size={inline ? "sm" : "default"} onClick={submitShare} disabled={!ready || share.isPending} data-share-submit="">
				{share.isPending ? t("share.sharing") : t("share.shareAction")}
			</Button>
		</div>
	);
	const notices = (
		<>
			<UndoNotice
				notice={undoNotice.shown}
				onUndo={() => {
					undoNotice.undo();
					fieldInputRef.current?.focus();
				}}
				onDismiss={undoNotice.dismiss}
			/>
			<p className="sr-only" aria-live="polite" data-share-announcement="">{announcement}</p>
		</>
	);
	const dialogs = (
		<>
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
			<LinkConfirmDialog
				confirm={confirm}
				scope={scope}
				links={links}
				removeJoined={removeJoined}
				onRemoveJoinedChange={setRemoveJoined}
				onConfirm={runConfirm}
				onCancel={cancelConfirm}
			/>
		</>
	);

	if (inline) {
		return (
			<div
				ref={contentRef}
				className="flex flex-col gap-(--size-settings-section-gap)"
				data-share-panel={scope}
				data-share-layout="inline"
				// Esc steps back out of Link settings or the confirm step before it closes Settings.
				data-settings-inline-edit={canStepBack ? "" : undefined}
				onKeyDown={(event) => {
					if (event.key !== "Escape" || event.nativeEvent.isComposing || !stepBack()) return;
					event.preventDefault();
					event.stopPropagation();
				}}
			>
				{view === "settings" ? (
					<section className="flex flex-col gap-(--size-settings-section-inner-gap)" aria-label={t("share.general.linkSettings")}>
						<div className="flex items-start gap-1 px-(--settings-row-inset)">
							<Button ref={backButtonRef} type="button" variant="quiet" size="icon-sm" className="-mt-1 -ml-2" aria-label={t("share.back")} onClick={closeSettings} data-share-back="">
								<ArrowLeft className="size-3.5" aria-hidden="true" />
							</Button>
							<div className="min-w-0 flex-1">
								<h2 className="text-sm font-medium leading-5 text-foreground">{t("share.general.linkSettings")}</h2>
								<p className="text-xs leading-5 text-settings-muted">{scope === "project" ? t("share.settings.descriptionProject") : t("share.settings.description")}</p>
							</div>
						</div>
						{linkSettings}
						{errorLine()}
					</section>
				) : (
					<>
						<div className="flex flex-col gap-(--size-settings-section-inner-gap)">
							<p className="px-(--settings-row-inset) text-sm leading-5 text-settings-muted">{scope === "project" ? t("share.projectIncludes") : t("share.sessionInProject")}</p>
							{peopleAvailable ? peopleField : null}
							{errorLine("px-(--settings-row-inset)")}
						</div>
						{peopleAvailable ? (
							staging ? (
								<div className="flex flex-col gap-(--size-settings-section-inner-gap)">
									{stagedSummary}
									<div className="flex items-center justify-between gap-2 pl-(--settings-row-inset)" data-share-footer="">
										{inAppNotice}
										{stagingActions}
									</div>
								</div>
							) : (
								<SettingsSection title={t("share.peopleWithAccess")} sectionId="share-people">{accessList}</SettingsSection>
							)
						) : null}
						{staging ? null : (
							<SettingsSection
								title={t("share.generalAccess")}
								sectionId="share-general-access"
								titleAction={<LinkSettingsButton onClick={() => openSettings("gear")} />}
								action={copyLink}
							>
								{generalAccess}
								<div ref={setCopyFallbackHost} className="contents" />
							</SettingsSection>
						)}
					</>
				)}
				{notices}
				{dialogs}
			</div>
		);
	}

	return (
		<>
			<div className={cn(settingsDialogBodyClass, "gap-3.5 px-5 pt-4 pb-5")} data-share-panel={scope} data-share-layout="dialog">
				{view === "settings" ? (
					<>
						{linkSettings}
						{errorLine()}
					</>
				) : (
					<>
						{peopleAvailable ? (
							<>
								{peopleField}
								{errorLine("-mt-1.5")}
								{staging ? stagedSummary : accessList}
							</>
						) : errorLine()}
						{staging ? null : generalAccess}
					</>
				)}
				{notices}
			</div>
			<div className="flex items-start justify-between gap-2 border-t border-border px-5 py-3.5" data-share-footer="">
				{staging ? (
					<>
						{inAppNotice}
						{stagingActions}
					</>
				) : (
					<>
						{view === "main" ? copyLink : <span />}
						{view === "settings" ? (
							<Button type="button" variant="primary" onClick={closeSettings} data-share-done="">
								{t("share.done")}
							</Button>
						) : (
							<DialogClose asChild>
								<Button type="button" variant="primary" data-share-done="">
									{t("share.done")}
								</Button>
							</DialogClose>
						)}
					</>
				)}
			</div>
			{dialogs}
		</>
	);
}

/**
 * The questions General access asks before a change that can't be taken back:
 * turning link access off, letting anyone with the link write (a wider link,
 * or another one that can write), and making a new link, which every change
 * to a link's access does. When links stop, each offers to remove the people
 * who joined by them, when the control plane can, never ticked: removing is
 * the owner's choice. It counts them from the links just read again, so it
 * waits for that read.
 */
function LinkConfirmDialog({ confirm, scope, links, removeJoined, onRemoveJoinedChange, onConfirm, onCancel }: {
	confirm: LinkConfirm | null;
	scope: ShareScope;
	links: LinkAccess;
	removeJoined: boolean;
	onRemoveJoinedChange: (value: boolean) => void;
	onConfirm: () => void;
	onCancel: () => void;
}) {
	const { t } = useTranslation();
	if (!confirm) return null;
	const count = links.links.length;
	const joined = confirmJoined(confirm, links);
	const offerRemove = offersRemoveJoined(confirm, links);
	const copy = confirm.kind === "change" && confirm.copy === true;
	const copyNew = confirm.kind === "copy-new";
	const widening = copyNew || (confirm.kind === "change" && widensToWrite(links.broadest, confirm));
	const title = confirm.kind === "off"
		? t("share.confirm.offTitle")
		: confirm.kind === "revoke"
			? t("share.confirm.revokeTitle")
			: copy ? t("share.confirm.trustedTitle") : widening ? t("share.confirm.writeTitle") : t("share.confirm.changeTitle");
	const stops = confirm.kind === "off"
		? t("share.confirm.offLinks", { count })
		: confirm.kind === "revoke"
			? t("share.confirm.revokeLink")
			: t("share.confirm.changeLinks", { count });
	// Who keeps access: everyone who joined, unless the owner ticks removing
	// them. A control plane that counts them and counts nobody has nobody to keep it.
	const keeps = (offerRemove && removeJoined) || (links.canRemoveJoined && joined === 0)
		? undefined
		: confirm.kind !== "revoke" && count > 1 ? t("share.confirm.keepJoinedSeveral") : t("share.confirm.keepJoined");
	const description = (
		<div className="flex flex-col gap-2">
			{widening || copy ? <p>{scope === "project" ? t("share.confirm.writeProject") : t("share.confirm.writeSession")}</p> : null}
			{copy ? <p>{t("share.confirm.trustedCopy")}</p> : null}
			{copyNew
				? <p>{t("share.copyNewLinkHint")}</p>
				: <p>{keeps ? t("share.twoSentences", { first: stops, second: keeps }) : stops}</p>}
		</div>
	);
	return (
		<ConfirmDialog
			open
			title={title}
			description={description}
			confirmLabel={confirm.kind === "off" ? t("share.confirm.offAction") : confirm.kind === "revoke" ? t("share.revoke") : widening ? t("share.confirm.writeAction") : t("share.confirm.changeAction")}
			destructive={confirm.kind === "off" || confirm.kind === "revoke"}
			// The count of who joined is only as good as the list it came from.
			confirmDisabled={links.fetching}
			onConfirm={onConfirm}
			onOpenChange={(next) => {
				if (!next) onCancel();
			}}
		>
			{offerRemove ? (
				<label className="flex items-start gap-2 text-control leading-5 text-foreground" data-share-remove-joined="">
					<Checkbox className="mt-0.5" checked={removeJoined} onCheckedChange={(value) => onRemoveJoinedChange(value === true)} />
					<span>
						{confirm.kind === "revoke"
							? t("share.confirm.removeJoined", { count: joined })
							: t("share.confirm.removeJoinedViaLink", { count: joined })}
					</span>
				</label>
			) : null}
		</ConfirmDialog>
	);
}

/**
 * Broadest to most specific, as Figma lists access: what comes from the whole
 * project, then teams, then people, then emails still waiting for sign-in.
 */
export function orderEntries(entries: readonly CloudCpShareEntry[], scope: ShareScope): CloudCpShareEntry[] {
	const rank = (entry: CloudCpShareEntry) =>
		scope === "session" && entry.scope === "project" ? 0
			: entry.subject.kind === "team" ? 1
				: entry.subject.name ? 2
					: 3;
	return entries.map((entry, index) => ({ entry, index }))
		.sort((a, b) => rank(a.entry) - rank(b.entry) || a.index - b.index)
		.map(({ entry }) => entry);
}

function AccessList({ orgId, projectId, sessionId, scope, access, entries, loading, me, myName, workspaceName, memberCount, solo, label = true, onChange, onStop, onShowProject }: {
	orgId: string;
	projectId: string;
	sessionId: string | undefined;
	scope: ShareScope;
	access: CloudCpShareAccess | undefined;
	/** The access's entries, broadest first. */
	entries: CloudCpShareEntry[];
	loading: boolean;
	me: string | undefined;
	/** The signed-in person's name, for a project's list with only them on it. */
	myName?: string;
	/** The name of the workspace that owns the project, when it reads as one (never an email); its people have full access. */
	workspaceName?: string;
	/** How many people are in the workspace, you included; undefined while it loads or when it couldn't be read. */
	memberCount?: number;
	/** Nobody but you in the workspace (or a standalone agent): the workspace grants nobody anything, so it gets no row. */
	solo: boolean;
	/** "People with access" over the list; off where a section heading says it. */
	label?: boolean;
	onChange: (entry: CloudCpShareEntry, next: { role: CloudCpShareRole; modeCap: ShareModeCap }) => void;
	onStop: (entry: CloudCpShareEntry) => void;
	onShowProject: () => void;
}) {
	const { t } = useTranslation();
	const owner = access?.owner;
	if (loading) {
		return (
			<div className="flex flex-col gap-1.5" aria-busy="true">
				{label ? <p className="text-xs text-muted-foreground">{t("share.peopleWithAccess")}</p> : null}
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
	// A project has no owner of its own: it is the workspace's. With nobody
	// else in the workspace, the one with access is you.
	const youRow = solo && !owner && scope === "project" && Boolean(me);
	// The CP names someone without a profile name by their email.
	const myShownName = myName && myName.toLowerCase() !== me ? myName : undefined;
	const workspaceDetail = [workspaceName, memberCount === undefined ? undefined : t("share.workspaceMemberCount", { count: memberCount })]
		.filter(Boolean)
		.join(" \u00b7 ");
	return (
		<div className="flex flex-col gap-1.5">
			{label ? <p className="text-xs text-muted-foreground">{t("share.peopleWithAccess")}</p> : null}
			<ul className="flex flex-col divide-y divide-border rounded-md bg-muted/40 py-0.5" data-share-access-list="">
				{owner ? (
					<li className="flex min-h-11 items-center gap-2.5 py-1.5 pr-2 pl-2.5" data-share-owner-row="">
						<PersonAvatar name={owner.name} email={owner.email} />
						<div className="min-w-0 flex-1">
							<p className="truncate text-control text-foreground">
								{owner.name || owner.email}
								{owner.email && owner.email.toLowerCase() === me ? <span className="text-muted-foreground"> {t("share.you")}</span> : null}
							</p>
							{owner.name && owner.email ? <p className="truncate text-xs text-muted-foreground">{owner.email}</p> : null}
						</div>
						<span className="pr-1 text-control text-passive">{t("share.owner")}</span>
					</li>
				) : null}
				{youRow ? (
					<li className="flex min-h-11 items-center gap-2.5 py-1.5 pr-2 pl-2.5" data-share-you-row="">
						<PersonAvatar name={myShownName} email={me} />
						<div className="min-w-0 flex-1">
							<p className="truncate text-control text-foreground">
								{myShownName || me}
								<span className="text-muted-foreground"> {t("share.you")}</span>
							</p>
							{myShownName && me ? <p className="truncate text-xs text-muted-foreground">{me}</p> : null}
						</div>
						<span className="pr-1 text-control text-passive">{t("teams.access.full")}</span>
					</li>
				) : null}
				{solo ? null : (
					<li className="flex min-h-11 items-center gap-2.5 py-1.5 pr-2 pl-2.5" data-share-workspace-row="">
						<TeamTile name={workspaceName || "W"} />
						<div className="min-w-0 flex-1">
							<p className="truncate text-control text-foreground">{t("share.workspaceEveryone")}</p>
							{workspaceDetail ? <p className="truncate text-xs text-muted-foreground" data-share-workspace-detail="">{workspaceDetail}</p> : null}
						</div>
						<Tooltip>
							<TooltipTrigger asChild>
								<span className="pr-1 text-control text-passive" tabIndex={0} data-share-workspace-access="">{t("teams.access.full")}</span>
							</TooltipTrigger>
							<TooltipContent className="max-w-72 leading-normal text-pretty">{t("share.workspaceAccessHint")}</TooltipContent>
						</Tooltip>
					</li>
				)}
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
							<span className="px-2 text-control whitespace-nowrap text-muted-foreground" tabIndex={0} aria-disabled="true" data-share-locked="">
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
						stopLabel={t("share.removeAccess")}
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
function StagedSummary({ orgId, projectId, sessionId, scope, staged, role, modeCap, onCapChange, entries, me, locale, membersHaveAccess = true }: {
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
	/** False for a Cloud standalone agent: the workspace's people get only what a share gives them. */
	membersHaveAccess?: boolean;
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
				if (membersHaveAccess && member.member.workspaceMember) add(workspace, key, label);
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
	}, [entries, locale, me, membersHaveAccess, role, scope, staged, t, teamAccess]);

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

