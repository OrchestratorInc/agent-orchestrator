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

const ACCESS_CHANGE = ["share-access-change"] as const;

const PENDING = "pending:";

type AccessKey = ReturnType<typeof shareAccessQueryKey>;
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

const CAP_RANK: Record<string, number> = { "read-only": 1, standard: 2, trusted: 3 };

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

function subjectInput(entry: CloudCpShareEntry): Subject | undefined {
	if (entry.subject.kind === "team" && entry.subject.teamId) return { teamId: entry.subject.teamId };
	if (entry.subject.email) return { email: entry.subject.email };
	return undefined;
}

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

type LinkConfirm =
	| { kind: "off" }
	| { kind: "change"; role: CloudCpShareRole; modeCap: ShareModeCap; copy?: boolean }
	| { kind: "copy-new" }
	| { kind: "revoke"; link: CloudCpShareLink };

const SETTINGS_OPENERS = { gear: "[data-share-settings]", card: "[data-share-link-settings]" } as const;
type SettingsOpener = keyof typeof SETTINGS_OPENERS;

function confirmLink(confirm: Extract<LinkConfirm, { kind: "revoke" }>, links: LinkAccess): CloudCpShareLink {
	return links.links.find((link) => link.id === confirm.link.id) ?? confirm.link;
}

function confirmJoined(confirm: LinkConfirm, links: LinkAccess): number {
	return confirm.kind === "revoke" ? confirmLink(confirm, links).joinedCount ?? 0 : links.joined;
}

function offersRemoveJoined(confirm: LinkConfirm, links: LinkAccess): boolean {
	return confirm.kind !== "copy-new" && links.canRemoveJoined && confirmJoined(confirm, links) > 0;
}

function widensToWrite(link: Pick<CloudCpShareLink, "role" | "modeCap"> | undefined, next: { role: CloudCpShareRole; modeCap: ShareModeCap }): boolean {
	if (next.role !== "editor") return false;
	if (link?.role !== "editor") return true;
	return actualLinkCap(link.role, link.modeCap) === "read-only" && actualLinkCap(next.role, next.modeCap) !== "read-only";
}

function useShareController({ active: open, orgId, projectId, sessionId, teams, standaloneAgent = false }: {
	active: boolean;
	orgId: string;
	projectId: string;
	sessionId?: string;
	teams: boolean;
	standaloneAgent?: boolean;
}) {
	const { t, i18n } = useTranslation();
	const { client, baseUrl, ready } = useCloudCp();
	const me = useCloudSession().session?.user;
	const myEmail = me?.email?.toLowerCase() ?? "";
	const { org } = useCloudOrg(open);
	const workspace = org?.id === orgId ? org : undefined;
	const workspaceName = workspaceDisplayName(workspace?.displayName);
	const personal = standaloneAgent || isPersonalWorkspace(workspace);
	const memberCount = useWorkspaceMemberCount(orgId, open && teams && Boolean(workspace) && !personal);
	const solo = personal || (memberCount.data !== undefined && memberCount.data <= 1);
	const queryClient = useQueryClient();
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
	const [announcement, setAnnouncement] = useState("");
	const fieldInputRef = useRef<HTMLInputElement>(null);
	const copyButtonRef = useRef<HTMLButtonElement>(null);
	const contentRef = useRef<HTMLDivElement>(null);
	const backButtonRef = useRef<HTMLButtonElement>(null);
	const settingsOpener = useRef<SettingsOpener>("gear");
	const revokeFocus = useRef<{ id: string; index: number } | null>(null);
	const [checkingRevoke, setCheckingRevoke] = useState<string | null>(null);
	const pendingCopy = useRef<((url: string | undefined) => void) | null>(null);
	const settlePendingCopy = (url: string | undefined) => {
		const resolve = pendingCopy.current;
		pendingCopy.current = null;
		resolve?.(url);
	};
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
			const members = entry.subject.kind === "team" && entry.subject.teamId
				? withTeamAccess(queryClient, ["cloud-team-share-access", baseUrl, orgId, projectId], entry.subject.teamId, role, modeCap)
				: [];
			return { key, previous, members };
		},
		onError: (_error, { entry }, context) => {
			for (const [membersKey, data] of context?.members ?? []) queryClient.setQueryData(membersKey, data);
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

	const restricted = restrictedSentence(t, { scope, workspaceKnown: Boolean(workspace), solo, people: peopleAvailable });
	const turnOnLinks = () => {
		undoNotice.clearFailure();
		links.turnOn.mutate({ role: "viewer", modeCap: "read-only", expiry });
	};
	const askTurnOff = () => {
		setRemoveJoined(false);
		setConfirm({ kind: "off" });
		void links.refresh();
	};
	const askChange = (next: { role: CloudCpShareRole; modeCap: ShareModeCap }, copy = false) => {
		setRemoveJoined(false);
		setConfirm({ kind: "change", ...next, ...(copy ? { copy } : {}) });
		void links.refresh();
	};
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
		if (!current) return;
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
		const remove = removeJoined && offersRemoveJoined(current, links);
		setConfirm(null);
		undoNotice.clearFailure();
		if (current.kind === "off") {
			const count = links.links.length;
			links.turnOff.mutate({ removeJoined: remove }, {
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
		setAnnouncement((before) => `${t("share.linkCopiedSentence", { sentence })}${before.endsWith("​") ? "" : "​"}`);
	};

	const staging = staged.length > 0;
	const linkError = links.turnOn.error ?? links.turnOff.error ?? links.replace.error ?? links.copyNew.error ?? links.revoke.error ?? links.error;
	const failure = share.error ?? update.error ?? stop.error ?? access.error;
	const error = failure
		? failure instanceof Error ? failure.message : String(failure)
		: linkError ? linkErrorMessage(t, linkError)
			: undoNotice.failed ? t("share.undoFailed") : null;

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

export function ShareSessionDialog({ open, onOpenChange, orgId, projectId, sessionId, sessionName, projectName, people: teams = true, standaloneAgent = false }: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	orgId: string;
	projectId: string;
	sessionId?: string;
	sessionName?: string;
	projectName?: string;
	people?: boolean;
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
				onInteractOutside={(event) => {
					if (event.target instanceof Element && event.target.closest("[data-global-toast]")) event.preventDefault();
				}}
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

export function ProjectSharePanel({ orgId, projectId, people: teams = true }: {
	orgId: string;
	projectId: string;
	people?: boolean;
}) {
	const share = useShareController({ active: true, orgId, projectId, teams });
	return <SharePanel share={share} layout="inline" />;
}

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
	entries: CloudCpShareEntry[];
	loading: boolean;
	me: string | undefined;
	myName?: string;
	workspaceName?: string;
	memberCount?: number;
	solo: boolean;
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
	const youRow = solo && !owner && scope === "project" && Boolean(me);
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

