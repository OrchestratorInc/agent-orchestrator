import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactElement, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { Check, Copy, Loader2, Share } from "@/components/app-icons";
import {
	type CloudShareAccess,
	type CloudShareFile,
	type CloudShareGrant,
	type CloudShareInspection,
	type CloudShareLink,
	type CloudShareLinks,
	type CloudSharePublishResult,
	artifactShareUrl,
	isPrivateShare,
	sameShareFingerprint,
} from "../../shared/cloud-share";
import { useCloudMeUserId } from "../hooks/useCloudMeUserId";
import { workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { useSettings } from "../hooks/useSettings";
import { aoBridge } from "../lib/bridge";
import type { ArtifactRow } from "../lib/artifacts";
import { useCloudSession } from "../lib/cloud-session";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import { STANDALONE_WORKSPACE_ID } from "../types/workspace";
import { shareAccessIcon } from "./ArtifactShareMark";
import { ArtifactGeneralAccess, accessName, accessSentence } from "./share/artifact/ArtifactGeneralAccess";
import { type ArtifactPerson, ArtifactPeopleList } from "./share/artifact/ArtifactPeopleList";
import { InviteField } from "./share/artifact/InviteField";
import { Button } from "./ui/button";
import { Popover, PopoverAnchor, PopoverContent, PopoverTrigger } from "./ui/popover";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

type Phase =
	| { kind: "loading" }
	| { kind: "signin" }
	| { kind: "unsupported" }
	| { kind: "notOwner" }
	| { kind: "limit"; limit: number }
	/** Before the first publish of a file on this computer: who it is for is set here, and Publish uploads it. */
	| { kind: "setup"; inspection: CloudShareInspection; links: CloudShareLinks; changed?: boolean; unpublished?: boolean }
	| { kind: "uploading"; access: CloudShareAccess; invited: number }
	| {
		kind: "published";
		share: CloudShareLink;
		/** Who the link is shared with, once read. */
		grants?: CloudShareGrant[];
		/** Reading who the link is shared with failed: say so and offer to try again. */
		grantsError?: boolean;
		/** The file changed while it was being republished: say so, and offer its newest version. */
		changed?: boolean;
		unpublishing?: "confirm" | "busy";
		error?: string;
		/** The link was just made private: say what that does to people without an invite. */
		narrowed?: boolean;
		/**
		 * The file changed since it was published to this link: Republish puts
		 * the newer version on it.
		 */
		republish?: { inspection: CloudShareInspection; links: CloudShareLinks };
	}
	| { kind: "unpublished" }
	| { kind: "error"; message: string };

/** Videos are not shared yet: there is no partial-file serving for them. */
export const VIDEO_FILE = /\.(mp4|m4v|mov|webm|ogv|avi|mkv)$/i;
const COPIED_MS = 2_000;

/**
 * A publish still running. It outlives the popover: closing the popover
 * keeps it going, and reopening Publish on the file shows it again.
 */
interface PendingShare {
	result: Promise<CloudSharePublishResult>;
	links: CloudShareLinks;
	/** The active link this publish updates, for a republish. */
	share?: CloudShareLink;
	access: CloudShareAccess;
	invited: number;
}

const pendingShares = new Map<string, PendingShare>();
const pendingListeners = new Set<() => void>();

function setPending(key: string, pending: PendingShare | undefined): void {
	if (pending) pendingShares.set(key, pending);
	else pendingShares.delete(key);
	for (const listener of pendingListeners) listener();
}

function subscribePending(listener: () => void): () => void {
	pendingListeners.add(listener);
	return () => pendingListeners.delete(listener);
}

/** Whether a publish of the file is running, wherever it was started. */
function usePublishing(key: string): boolean {
	return useSyncExternalStore(subscribePending, () => pendingShares.has(key));
}

function pendingKey(baseUrl: string, file: Pick<CloudShareFile, "sessionId" | "path">): string {
	return JSON.stringify([baseUrl, file.sessionId, file.path]);
}

/** The panel names a file without its extension: `report.html` is `report`. A dotfile keeps its name. */
export function publishTitle(name: string): string {
	return name.replace(/(?<=[^.])\.[^.]+$/, "");
}

/** An error's message, without the wrapper Electron puts around one thrown across IPC. */
function errorMessage(error: unknown): string {
	const message = error instanceof Error ? error.message : String(error);
	return message.replace(/^Error invoking remote method '[^']*': (?:\w*Error: )?/, "");
}

/** Whether each control plane serves share links, as far as this run has found out. */
const shareAvailability = new Map<string, boolean>();
const shareAvailabilityChecks = new Map<string, Promise<boolean | null>>();

/**
 * Whether the control plane serves share links. Its share routes exist only
 * while its share flag is on; until the main process has checked, and when
 * they are off, Publish is not offered. The answer is kept for this run.
 */
export function useShareAvailable(): boolean {
	const { baseUrl } = useShareAccess();
	const [available, setAvailable] = useState(() => shareAvailability.get(baseUrl) ?? false);
	useEffect(() => {
		if (!baseUrl) return setAvailable(false);
		const known = shareAvailability.get(baseUrl);
		if (known !== undefined) return setAvailable(known);
		let cancelled = false;
		let check = shareAvailabilityChecks.get(baseUrl);
		if (!check) {
			check = aoBridge.cloudShare.available(baseUrl).catch(() => null);
			shareAvailabilityChecks.set(baseUrl, check);
		}
		void check.then((result) => {
			// An unreachable control plane is asked again next time.
			if (result === null) shareAvailabilityChecks.delete(baseUrl);
			else shareAvailability.set(baseUrl, result);
			if (!cancelled) setAvailable(result === true);
		});
		return () => {
			cancelled = true;
		};
	}, [baseUrl]);
	return available;
}

/**
 * Bumped when a panel publishes, unpublishes or changes who a link opens for,
 * so useReconciledShareLinks reads the link list again and a cached list read
 * before the change is no longer used.
 */
let shareLinksRevision = 0;
const shareLinksListeners = new Set<() => void>();
function shareLinksChanged(): void {
	shareLinksRevision += 1;
	for (const listener of shareLinksListeners) listener();
}
function subscribeShareLinks(listener: () => void): () => void {
	shareLinksListeners.add(listener);
	return () => shareLinksListeners.delete(listener);
}

/**
 * Whose link list: the control plane and the signed-in account. A list holds
 * one account's links and plan, so it is never read for another.
 */
export interface ShareScope {
	baseUrl: string;
	/** The signed-in user's ID; empty while signed out. */
	account: string;
}

function scopeKey(scope: ShareScope): string {
	return JSON.stringify([scope.baseUrl, scope.account]);
}

/** The last link list read for each scope, the revision it was read at, and when. */
interface CachedShareLinks {
	links: CloudShareLinks;
	revision: number;
	at: number;
}
const shareLinksCache = new Map<string, CachedShareLinks>();
/** Reads under way, one per scope and revision, shared by every caller. */
const shareLinksReads = new Map<string, Promise<CloudShareLinks>>();
/** How long a list read lets the panel open without waiting for another. */
const SHARE_LINKS_FRESH_MS = 5 * 60_000;

/**
 * Reads the person's link list. Callers asking at the same time share one
 * request, and the answer is kept for the panel to open on at once. The main
 * process also brings this computer's publish records up to date with it.
 */
function readShareLinks(scope: ShareScope): Promise<CloudShareLinks> {
	const revision = shareLinksRevision;
	const cacheKey = scopeKey(scope);
	const key = JSON.stringify([cacheKey, revision]);
	let read = shareLinksReads.get(key);
	if (!read) {
		read = aoBridge.cloudShare.list(scope.baseUrl)
			.then((links) => {
				// A read started before a change never replaces one started after
				// it, and one started for an account no longer signed in is not kept.
				const cached = shareLinksCache.get(cacheKey);
				if (shareLinksReads.get(key) === read && (!cached || cached.revision <= revision)) {
					shareLinksCache.set(cacheKey, { links, revision, at: Date.now() });
				}
				for (const listener of shareLinksListeners) listener();
				return links;
			})
			.finally(() => {
				if (shareLinksReads.get(key) === read) shareLinksReads.delete(key);
			});
		shareLinksReads.set(key, read);
	}
	return read;
}

/** The list last read for this scope, while no change was made since and it is recent; else undefined. */
function freshShareLinks(scope: ShareScope): CloudShareLinks | undefined {
	const cached = shareLinksCache.get(scopeKey(scope));
	if (!cached || cached.revision !== shareLinksRevision || Date.now() - cached.at > SHARE_LINKS_FRESH_MS) return undefined;
	return cached.links;
}

/** The scope the caches were last used for. */
let lastShareScope: string | undefined;

/**
 * Forgets every cached and in-flight link list when the signed-in account or
 * the control plane changes (sign-out included), and has every surface read
 * the list again for the new one.
 */
function noteShareScope(scope: ShareScope): void {
	const key = scopeKey(scope);
	if (lastShareScope === key) return;
	const switched = lastShareScope !== undefined;
	lastShareScope = key;
	if (!switched) return;
	shareLinksCache.clear();
	shareLinksReads.clear();
	shareLinksChanged();
}

/** Forgets every cached link list; for tests. */
export function resetShareCachesForTests(): void {
	shareLinksCache.clear();
	shareLinksReads.clear();
	lastShareScope = undefined;
}

/** Who each active link opens for, by the URL a published artifact carries as its shareUrl. */
export type ShareAccessByUrl = ReadonlyMap<string, CloudShareAccess>;

const NO_SHARE_ACCESS: ShareAccessByUrl = new Map();
/** One map per list read, so a surface re-renders only when the list changes. */
const accessByList = new WeakMap<CloudShareLinks, ShareAccessByUrl>();
function shareAccessOf(links: CloudShareLinks): ShareAccessByUrl {
	let access = accessByList.get(links);
	if (!access) {
		access = new Map(links.shares
			.filter((share) => !share.revokedAt)
			.map((share): [string, CloudShareAccess] => [artifactShareUrl(share), isPrivateShare(share) ? "restricted" : "anyone"]));
		accessByList.set(links, access);
	}
	return access;
}

/** Link-list reads that have already had the artifact list read again after them. */
const reconciledReads = new WeakSet<Promise<CloudShareLinks>>();

/**
 * Brings this computer's publish records up to date with the person's link
 * list, then reads the artifact list again. Records from before links were
 * kept with their URL name only the link's ID, and a link unpublished
 * elsewhere keeps its URL until the list says otherwise; the daemon serves
 * each artifact's shareUrl from those records, so until they are reconciled
 * a published file reads as not shared, or an unpublished one as public.
 * Runs once per mount, again when sign-in or the control plane changes, and
 * again after a panel changes a link; surfaces mounted together share one
 * read. Returns who each link opens for, so a private link is not marked
 * public: the last list read, and empty until one is.
 */
export function useReconciledShareLinks(): ShareAccessByUrl {
	const available = useShareAvailable();
	const { signedIn, scope } = useShareAccess();
	const queryClient = useQueryClient();
	const revision = useSyncExternalStore(subscribeShareLinks, () => shareLinksRevision);
	const cached = useSyncExternalStore(subscribeShareLinks, () => shareLinksCache.get(scopeKey(scope)));
	useEffect(() => {
		if (!available || !signedIn) return;
		const read = readShareLinks(scope);
		if (reconciledReads.has(read)) return;
		reconciledReads.add(read);
		read
			.then(() => queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost() }))
			// Offline or signed out: the records stay as they are until the next list.
			.catch(() => undefined);
	}, [available, signedIn, scope, queryClient, revision]);
	return available && signedIn && cached ? shareAccessOf(cached.links) : NO_SHARE_ACCESS;
}

/** The control plane URL and whether the person is signed in to share. */
export function useShareAccess(): { baseUrl: string; signedIn: boolean; scope: ShareScope } {
	const { settings } = useSettings();
	const { status, session } = useCloudSession();
	const baseUrl = settings?.cloudControlPlaneUrl ?? "";
	const signedIn = status === "authenticated" && baseUrl !== "";
	const account = signedIn ? session?.user.id ?? "" : "";
	// While the session is still loading, nothing changed yet.
	const known = status !== "loading";
	useEffect(() => {
		if (known) noteShareScope({ baseUrl, account });
	}, [known, baseUrl, account]);
	const scope = useMemo(() => ({ baseUrl, account }), [baseUrl, account]);
	return { baseUrl, signedIn, scope };
}

/**
 * The file an artifact is shared as, or undefined where the person cannot
 * share it: only the person who started a cloud session publishes its files,
 * and not once they are shared. A Scratchpad file belongs to the Scratchpad
 * workspace, as in the session view.
 */
export function useArtifactShareFile(artifact: Pick<ArtifactRow, "id" | "cloud" | "sessionId" | "path" | "projectId" | "sha256"> | null): Omit<CloudShareFile, "baseUrl" | "shareId"> | undefined {
	const cloudUserId = useCloudMeUserId(Boolean(artifact?.cloud));
	if (!artifact) return undefined;
	// The artifact's ID survives a rename, so the renamed file finds its link.
	const artifactId = artifact.id ? { artifactId: artifact.id } : {};
	if (artifact.cloud) {
		const owner = Boolean(cloudUserId) && artifact.cloud.createdByUserId === cloudUserId;
		if (artifact.cloud.shared || !owner) return undefined;
		// Its listed SHA-256 names the version: publishing needs no other read.
		const version = artifact.sha256 ? { fingerprint: artifact.sha256 } : {};
		return { source: "cloud", orgId: artifact.cloud.orgId, sessionId: artifact.sessionId, path: artifact.path, projectId: "", ...artifactId, ...version };
	}
	return { sessionId: artifact.sessionId, path: artifact.path, projectId: artifact.projectId ?? STANDALONE_WORKSPACE_ID, ...artifactId };
}

const POPOVER_CLASS = "w-[min(22rem,90vw)] p-4";
/** How long after a menu opens the panel focus may still leave it as the menu finishes closing. */
const MENU_HANDOFF_MS = 400;
/** The most people one request may share a link with; a link holds more, added later. */
export const MAX_INVITES_PER_REQUEST = 50;

/**
 * Closes an open popover when focus moves into a framed page. A click inside
 * an artifact preview lands in its iframe and never reaches this document, so
 * the popover's own click-outside cannot see it; the window's blur, with the
 * frame left as the active element, can. Read a tick later, once focus settles.
 */
function useCloseWhenFrameFocused(open: boolean, close: () => void): void {
	const closeRef = useRef(close);
	closeRef.current = close;
	useEffect(() => {
		if (!open) return;
		let timer: number | undefined;
		const check = () => {
			timer = window.setTimeout(() => {
				const active = document.activeElement;
				if (active instanceof HTMLIFrameElement || active?.tagName === "WEBVIEW") closeRef.current();
			}, 0);
		};
		window.addEventListener("blur", check);
		return () => {
			window.removeEventListener("blur", check);
			window.clearTimeout(timer);
		};
	}, [open]);
}

/**
 * The publish panel a menu's Publish or Unpublish item opens, anchored to the
 * menu's trigger. Unpublish opens on its confirmation and never publishes.
 */
export function ShareArtifactMenuPopover({ file, name, open, onOpenChange, intent = "publish", children }: {
	file: Omit<CloudShareFile, "baseUrl" | "shareId">;
	name: string;
	open: boolean;
	onOpenChange: (open: boolean) => void;
	intent?: "publish" | "unpublish";
	children: ReactElement;
}) {
	useCloseWhenFrameFocused(open, () => onOpenChange(false));
	const anchorRef = useRef<HTMLDivElement>(null);
	const openedAt = useRef(0);
	useEffect(() => {
		if (open) openedAt.current = performance.now();
	}, [open]);
	return (
		<Popover open={open} onOpenChange={onOpenChange}>
			<PopoverAnchor asChild ref={anchorRef}>{children}</PopoverAnchor>
			<PopoverContent
				align="end"
				className={POPOVER_CLASS}
				data-browser-native-overlay="true"
				// The menu that opened this panel hands focus back as it finishes
				// closing, a frame after the panel took it; that is not the person
				// leaving. Only that is ignored: focus going to the menu's own
				// trigger, or anywhere just after opening. Tabbing away later,
				// Escape, a click outside and a click in a preview still close it.
				onFocusOutside={(event) => {
					const target = event.target instanceof Node ? event.target : null;
					const justOpened = performance.now() - openedAt.current < MENU_HANDOFF_MS;
					if (justOpened || (target && anchorRef.current?.contains(target))) event.preventDefault();
				}}
			>
				{open ? <SharePanel file={file} name={name} onClose={() => onOpenChange(false)} unpublishOnOpen={intent === "unpublish"} /> : null}
			</PopoverContent>
		</Popover>
	);
}

/** Whether the link serves an older version of the file, or its old name: Republish would update it. */
function linkOutdated(share: Pick<CloudShareLink, "name">, inspection: CloudShareInspection): boolean {
	return share.name !== inspection.name ||
		Boolean(inspection.publishedFingerprint && !sameShareFingerprint(inspection.publishedFingerprint, inspection.fingerprint));
}

/** The file's active link, and whether the file changed since it was published to it. */
interface PublishedShare {
	share?: CloudShareLink;
	outdated: boolean;
	/** The check for this file has answered. */
	checked?: boolean;
}

/**
 * The file's active link as the toolbar shows it: Published, and whether the
 * file changed since (its newer content or its new name), which Republish
 * would put on the link. Read from this computer's publish record, the file's
 * size and modification time (a cloud file's SHA-256) and the link list,
 * cached when it was read moments ago: the file itself is never opened.
 * Checked again whenever `revision` changes.
 */
function usePublishedShare(file: CloudShareFile | null, scope: ShareScope, revision: string): PublishedShare {
	// Kept with the file and account it is for: a button reused for another
	// file, or after another account signs in, shows nothing published until
	// it is checked again, never the last one's link.
	const [state, setState] = useState<PublishedShare & { fileKey: string }>({ outdated: false, fileKey: "" });
	const fileKey = file ? JSON.stringify([file, scope.account]) : "";
	useEffect(() => {
		if (!file) return setState({ outdated: false, fileKey });
		let cancelled = false;
		void (async (): Promise<PublishedShare> => {
			const inspection = await aoBridge.cloudShare.inspect(file);
			if (!inspection.shareId) return { outdated: false };
			const links = freshShareLinks(scope) ?? await readShareLinks(scope);
			const share = links.shares.find((candidate) => candidate.id === inspection.shareId && !candidate.revokedAt);
			if (!share) return { outdated: false };
			return { share, outdated: linkOutdated(share, inspection) };
		})()
			.catch((): PublishedShare => ({ outdated: false }))
			.then((result) => {
				if (!cancelled) setState({ ...result, fileKey });
			});
		return () => {
			cancelled = true;
		};
	}, [fileKey, revision]); // eslint-disable-line react-hooks/exhaustive-deps
	return state.fileKey === fileKey ? { share: state.share, outdated: state.outdated, checked: true } : { outdated: false, checked: false };
}

/**
 * The Publish button beside an open artifact's other actions. Clicking it
 * publishes the file, privately where links can be private, and opens a
 * popover anchored to it on the published link: invite people, choose who it
 * opens for, copy it or unpublish it. A file on this computer opens the
 * popover first, to choose who it is for, and its Publish there uploads it.
 * Once published, the button reads Published, with the same Publish icon: who
 * the link opens for is shown in the popover, not guessed at on the button.
 * Once the file changed since, it reads Republish with an amber dot, and one
 * click puts the newer version on the same link and opens the popover on it.
 * For a video the button is disabled and says why.
 */
export function ShareArtifactButton({ file, name, className, iconOnly = false, revision = "", published = false }: {
	file: Omit<CloudShareFile, "baseUrl" | "shareId">;
	name: string;
	className?: string;
	iconOnly?: boolean;
	/** The file's last change, e.g. its updatedAt: a new value checks again whether it needs Republish. */
	revision?: string;
	/** Whether the artifact list has the file published: the label until the button's own check answers. */
	published?: boolean;
}) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	// Checked again when the panel changes the link and when it closes.
	const [checks, setChecks] = useState(0);
	const recheck = useCallback(() => setChecks((count) => count + 1), []);
	const available = useShareAvailable();
	const { baseUrl, signedIn, scope } = useShareAccess();
	const video = VIDEO_FILE.test(file.path);
	const publishing = usePublishing(pendingKey(baseUrl, file));
	const { share, outdated, checked } = usePublishedShare(available && signedIn && !video ? { ...file, baseUrl } : null, scope, `${revision}:${checks}:${publishing}`);
	// What the panel does on opening is fixed when it opens, so a check that
	// lands while it is open never starts a second publish.
	const [republishOnOpen, setRepublishOnOpen] = useState(false);
	const onOpenChange = (next: boolean) => {
		if (next) setRepublishOnOpen(outdated);
		else recheck();
		setOpen(next);
	};
	useCloseWhenFrameFocused(open, () => onOpenChange(false));
	const label = publishing ? t("artifacts.share.publishing")
		: outdated ? t("artifacts.share.republish")
		: share || (!checked && published) ? t("artifacts.share.published")
		: t("artifacts.share.publish");
	const icon = publishing ? <Loader2 aria-hidden="true" className="size-icon-sm animate-spin motion-reduce:animate-none" />
		: <Share aria-hidden="true" className="size-icon-sm" />;
	const amberDot = <span aria-hidden="true" className="pointer-events-none absolute top-0.5 right-0.5 size-1.5 rounded-full bg-status-in-review ring-1 ring-background" data-share-outdated="" />;
	const panel = (
		<PopoverContent align="end" className={POPOVER_CLASS} data-browser-native-overlay="true">
			{open ? <SharePanel file={file} name={name} onClose={() => onOpenChange(false)} onChanged={recheck} republishOnOpen={republishOnOpen} /> : null}
		</PopoverContent>
	);
	// Not offered where the control plane does not serve share links.
	if (!available) return null;
	if (video) {
		return (
			<Tooltip>
				<TooltipTrigger asChild>
					<span className="inline-flex" tabIndex={0}>
						<Button aria-label={`${label}: ${name}`} className={className} disabled size={iconOnly ? "icon-sm" : "sm"} type="button" variant="quiet">
							{icon}
							{iconOnly ? null : label}
						</Button>
					</span>
				</TooltipTrigger>
				<TooltipContent side="bottom">{t("artifacts.share.videoSoon")}</TooltipContent>
			</Tooltip>
		);
	}
	const button = (
		<Button
			aria-label={`${label}: ${name}`}
			// Muted like the header's other actions, bright while hovered, and pressed while open.
			className={cn(outdated && "relative", className)}
			size={iconOnly ? "icon-sm" : "sm"}
			type="button"
			variant="quiet"
		>
			{icon}
			{iconOnly ? null : label}
			{outdated && !publishing ? amberDot : null}
		</Button>
	);
	const popoverTrigger = <PopoverTrigger asChild>{button}</PopoverTrigger>;
	// Closing the popover mid-publish keeps the publish going; only Cancel stops it.
	return (
		<Popover open={open} onOpenChange={onOpenChange}>
			{iconOnly || (outdated && !publishing) ? (
				<Tooltip>
					<TooltipTrigger asChild>{popoverTrigger}</TooltipTrigger>
					<TooltipContent side="bottom">{outdated && !publishing ? t("artifacts.share.republishHint") : label}</TooltipContent>
				</Tooltip>
			) : popoverTrigger}
			{panel}
		</Popover>
	);
}

function SharePanel({
	file,
	name,
	onClose,
	onChanged,
	republishOnOpen = false,
	unpublishOnOpen = false,
}: {
	file: Omit<CloudShareFile, "baseUrl" | "shareId">;
	name: string;
	onClose: () => void;
	/** The link changed: published, republished, unpublished or its access. */
	onChanged?: () => void;
	/** Opened from Republish: put the newer version on the link at once. */
	republishOnOpen?: boolean;
	/** Opened from Unpublish: ask to unpublish the file's link, and close once it is done or cancelled. */
	unpublishOnOpen?: boolean;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	/**
	 * Reads the artifact list again after a publish or an unpublish: the daemon
	 * serves each artifact's shareUrl from the publish records this panel changed.
	 */
	const refreshArtifacts = () => {
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost() });
		shareLinksChanged();
	};
	const { baseUrl, signedIn, scope } = useShareAccess();
	const owner = useCloudSession().session?.user;
	const openSettings = useUiStore((state) => state.openGlobalSettings);
	const [phase, setPhase] = useState<Phase>({ kind: "loading" });
	const [copied, setCopied] = useState(false);
	// Links may be private here; known once the link list is read.
	const [privateLinks, setPrivateLinks] = useState(false);
	// Who a file on this computer is for, set before its Publish uploads it.
	const [stagedAccess, setStagedAccess] = useState<CloudShareAccess>("restricted");
	const [staged, setStaged] = useState<string[]>([]);
	// Adding past what one publish can share the link with: said under the field.
	const [stagedOver, setStagedOver] = useState(false);
	const [inviting, setInviting] = useState(false);
	const [removing, setRemoving] = useState<string>();
	const [changingAccess, setChangingAccess] = useState(false);
	const shareFile = { ...file, baseUrl };
	const key = pendingKey(baseUrl, file);
	// A cloud session's file is already in the bucket: the click publishes it.
	// A file on this computer is uploaded by the popover's own Publish.
	const onThisComputer = file.source !== "cloud";

	// Every read or change of who the link is shared with takes the next
	// number. A full list (a read, or an invite's answer) is shown only if
	// nothing started after it, so an older answer never replaces a newer one.
	const grantsSeq = useRef(0);
	// Every action taken in the panel (publish, unpublish, access, people)
	// takes the next number. The refresh behind an opening from a cached list
	// only applies while none started since: it never overwrites what an
	// action's answer put in the panel, such as a publish's newer version.
	const actions = useRef(0);
	const startAction = () => {
		actions.current += 1;
	};
	const loadGrants = (share: CloudShareLink) => {
		const seq = ++grantsSeq.current;
		setPhase((current) => current.kind === "published" && current.share.id === share.id ? { ...current, grantsError: undefined } : current);
		void aoBridge.cloudShare.grants(baseUrl, share.id)
			.then((grants) => setPhase((current) => current.kind === "published" && current.share.id === share.id && seq === grantsSeq.current
				? { ...current, grants, grantsError: undefined } : current))
			.catch(() => setPhase((current) => current.kind === "published" && current.share.id === share.id && seq === grantsSeq.current
				? { ...current, grantsError: true } : current));
	};
	const showPublished = (share: CloudShareLink, extra: Partial<Extract<Phase, { kind: "published" }>> = {}) => {
		setPhase({ kind: "published", share, ...extra });
		// Who it is shared with; listed when it answers.
		if (isPrivateShare(share) || share.grantCount) loadGrants(share);
	};

	// Shows how a publish ended. Its own refusal is shown as it came, not
	// worked out again from the link list. A republish names its link: a file
	// that changed under it stays on that link.
	const settle = (result: CloudSharePublishResult, links: CloudShareLinks, share?: CloudShareLink) => {
		switch (result.status) {
			case "published":
				setStaged([]);
				showPublished(result.share);
				onChanged?.();
				refreshArtifacts();
				return;
			case "cancelled":
				// Back to the file, waiting for Publish: opening it again would only redo the upload just stopped.
				return load({ publishNow: false });
			case "limit":
				return setPhase({ kind: "limit", limit: links.limit });
			case "changed":
				if (share) return showPublished(share, { republish: { inspection: result.inspection, links }, changed: true });
				return setPhase({ kind: "setup", inspection: result.inspection, links, changed: true });
			case "unsupported":
				return setPhase({ kind: "unsupported" });
			case "not_owner":
				return setPhase({ kind: "notOwner" });
			case "signed_out":
				return setPhase({ kind: "signin" });
			case "error":
				return setPhase({ kind: "error", message: result.message });
		}
	};

	// Opening the panel is the Publish click. A cloud session's file is
	// published at once, privately where links can be private; a file on this
	// computer opens on its setup. publishNow false (after a cancel) waits too.
	const load = useCallback(({ publishNow = true }: { publishNow?: boolean } = {}) => {
		if (!signedIn) {
			setPhase({ kind: "signin" });
			return () => undefined;
		}
		let cancelled = false;
		const pending = pendingShares.get(key);
		if (pending) {
			setPrivateLinks(pending.links.privateLinks);
			setPhase({ kind: "uploading", access: pending.access, invited: pending.invited });
			pending.result
				.then((result) => !cancelled && settle(result, pending.links, pending.share))
				.catch((error: unknown) => !cancelled && setPhase({ kind: "error", message: errorMessage(error) }));
			return () => {
				cancelled = true;
			};
		}
		setPhase({ kind: "loading" });
		// What decides the panel's first state: whether the file has an active
		// link, who it opens for, whether a new one fits the plan. A change in
		// any of these after opening on a cached list opens it again.
		const decisionKey = (inspection: CloudShareInspection, links: CloudShareLinks) => {
			const current = inspection.shareId ? links.shares.find((share) => share.id === inspection.shareId) : undefined;
			return JSON.stringify([
				links.privateLinks, links.unlimited, links.limit, links.used,
				current ? [current.revokedAt ?? null, current.access ?? null, current.name, current.grantCount ?? 0] : null,
				links.shares.some((share) => !share.revokedAt && share.name === inspection.name),
			]);
		};
		// shown: the list the panel was already decided on, when a fresher one
		// decides it again. The person's choices in it (who it is for, the
		// people added) are kept; only what the server says is updated.
		const decide = (inspection: CloudShareInspection, links: CloudShareLinks, publishNow: boolean, shown?: CloudShareLinks) => {
			setPrivateLinks(links.privateLinks);
			if (inspection.unsupported) return setPhase({ kind: "unsupported" });
			// The link this file was published to from here, while it is active.
			const current = inspection.shareId ? links.shares.find((share) => share.id === inspection.shareId && !share.revokedAt) : undefined;
			if (current) {
				if (unpublishOnOpen) return showPublished(current, { unpublishing: "confirm" });
				// The link may still have the old content or the old filename.
				const outdated = linkOutdated(current, inspection);
				// The Republish click, like Publish, needs no second one.
				if (publishNow && republishOnOpen && outdated) return void publish(inspection, links, current);
				// Changed since: Republish is offered on the link, wherever the panel was opened.
				return showPublished(current, outdated ? { republish: { inspection, links } } : {});
			}
			// It was published from here, but its link is unpublished now,
			// usually from somewhere else. Publishing gives it a new link.
			const unpublished = Boolean(inspection.shareId);
			// A file with no active link of its name is most likely a new
			// document; the server decides for sure when it uploads.
			const likelyNew = !links.shares.some((share) => !share.revokedAt && share.name === inspection.name);
			if (!links.unlimited && likelyNew && links.used >= links.limit) return setPhase({ kind: "limit", limit: links.limit });
			// A cloud session's file is already in the bucket: it publishes at once.
			if (publishNow && !onThisComputer) return void publish(inspection, links);
			// The default for who it is for, unless the person may already have
			// chosen: a fresher list keeps their choice, unless whether links can
			// be private at all changed, which changes the choices offered.
			if (!shown || shown.privateLinks !== links.privateLinks) setStagedAccess(links.privateLinks ? "restricted" : "anyone");
			setPhase({ kind: "setup", inspection, links, unpublished });
		};
		// The inspection reads only this computer's record and the file's
		// metadata, so it is quick. The list read moments ago (by an artifacts
		// view or the open artifact) opens the panel at once; it is read again
		// behind it, and a different answer decides the panel again.
		const cached = freshShareLinks(scope);
		const openedRevision = shareLinksRevision;
		const openedActions = actions.current;
		Promise.all([aoBridge.cloudShare.inspect({ ...file, baseUrl }), cached ?? readShareLinks(scope)])
			.then(([inspection, links]) => {
				if (cancelled) return;
				decide(inspection, links, publishNow);
				if (!cached) return;
				void readShareLinks(scope)
					.then((fresh) => {
						// Any action started since, finished or not (a publish this opening
						// started, one whose answer was "changed", an access change), and any
						// change made elsewhere in the app: its answer settles the panel, and
						// this read predates it.
						if (cancelled || actions.current !== openedActions || pendingShares.has(key) || shareLinksRevision !== openedRevision || decisionKey(inspection, fresh) === decisionKey(inspection, links)) return;
						decide(inspection, fresh, false, links);
					})
					.catch(() => undefined);
			})
			.catch((error: unknown) => {
				if (!cancelled) setPhase({ kind: "error", message: errorMessage(error) });
			});
		return () => {
			cancelled = true;
		};
	}, [signedIn, baseUrl, scope.account, key, file.projectId]); // eslint-disable-line react-hooks/exhaustive-deps

	useEffect(() => load({ publishNow: !unpublishOnOpen }), [load]); // eslint-disable-line react-hooks/exhaustive-deps

	useEffect(() => {
		if (!copied) return;
		const timer = setTimeout(() => setCopied(false), COPIED_MS);
		return () => clearTimeout(timer);
	}, [copied]);

	// With share, updates that active link in place: same URL, same slot, its
	// access and who it is shared with kept. A new link starts as set up here:
	// private and shared with nobody yet, unless the person chose otherwise.
	const publish = async (inspection: CloudShareInspection, links: CloudShareLinks, share?: CloudShareLink, startsWith?: { access: CloudShareAccess; emails: string[] }) => {
		startAction();
		const access = share ? (isPrivateShare(share) ? "restricted" : "anyone")
			: links.privateLinks ? startsWith?.access ?? "restricted" : "anyone";
		const emails = !share && links.privateLinks ? startsWith?.emails ?? [] : [];
		const invited = share ? share.grantCount ?? 0 : emails.length;
		setPhase({ kind: "uploading", access, invited });
		// The fingerprint ties the publish to what this popover showed. Access
		// is sent only where links can be private: an older control plane
		// refuses a field it does not know.
		const pending: PendingShare = {
			result: aoBridge.cloudShare.publish({
				...shareFile,
				fingerprint: inspection.fingerprint,
				shareId: share?.id,
				...(!share && links.privateLinks ? { access } : {}),
				...(emails.length > 0 ? { emails } : {}),
			}),
			links,
			share,
			access,
			invited,
		};
		setPending(key, pending);
		try {
			settle(await pending.result, links, share);
		} catch (error) {
			setPhase({ kind: "error", message: errorMessage(error) });
		} finally {
			if (pendingShares.get(key) === pending) setPending(key, undefined);
		}
	};

	const unpublish = async (shown: Extract<Phase, { kind: "published" }>) => {
		startAction();
		setPhase({ ...shown, unpublishing: "busy" });
		try {
			await aoBridge.cloudShare.unpublish(baseUrl, shown.share.id);
			refreshArtifacts();
			onChanged?.();
			if (unpublishOnOpen) return onClose();
			setPhase({ kind: "unpublished" });
		} catch (error) {
			setPhase({ ...shown, unpublishing: undefined, error: t("artifacts.share.unpublishFailed", { message: errorMessage(error) }) });
		}
	};

	const changeAccess = async (shown: Extract<Phase, { kind: "published" }>, access: CloudShareAccess) => {
		startAction();
		setChangingAccess(true);
		try {
			const share = await aoBridge.cloudShare.setAccess(baseUrl, shown.share.id, access);
			setPhase((current) => current.kind === "published" && current.share.id === share.id
				? { ...current, share: { ...current.share, ...share }, narrowed: !isPrivateShare(current.share) && access === "restricted", error: undefined }
				: current);
			onChanged?.();
			shareLinksChanged();
		} catch (error) {
			setPhase((current) => current.kind === "published" ? { ...current, error: t("artifacts.share.access.changeFailed", { message: errorMessage(error) }) } : current);
		} finally {
			setChangingAccess(false);
		}
	};

	// The link is live: Invite gives access at once.
	const invite = async (shown: Extract<Phase, { kind: "published" }>, emails: string[]): Promise<boolean> => {
		startAction();
		setInviting(true);
		const seq = ++grantsSeq.current;
		try {
			const grants = await aoBridge.cloudShare.invite(baseUrl, shown.share.id, emails);
			setPhase((current) => current.kind === "published" && current.share.id === shown.share.id && seq === grantsSeq.current
				? { ...current, grants, share: { ...current.share, grantCount: grants.length }, grantsError: undefined, error: undefined }
				: current);
			return true;
		} catch (error) {
			setPhase((current) => current.kind === "published" ? { ...current, error: t("artifacts.share.people.inviteFailed", { message: errorMessage(error) }) } : current);
			return false;
		} finally {
			setInviting(false);
		}
	};

	const removePerson = async (shown: Extract<Phase, { kind: "published" }>, person: ArtifactPerson) => {
		if (!person.id) return;
		startAction();
		setRemoving(person.email);
		// A list read or invited before this is out of date once it lands.
		++grantsSeq.current;
		try {
			await aoBridge.cloudShare.removeGrant(baseUrl, shown.share.id, person.id);
			setPhase((current) => {
				if (current.kind !== "published" || current.share.id !== shown.share.id) return current;
				const grants = (current.grants ?? []).filter((grant) => grant.id !== person.id);
				return { ...current, grants, share: { ...current.share, grantCount: grants.length }, error: undefined };
			});
		} catch (error) {
			setPhase((current) => current.kind === "published" ? { ...current, error: t("artifacts.share.people.removeFailed", { message: errorMessage(error) }) } : current);
		} finally {
			setRemoving(undefined);
		}
	};

	// Every state is a title, its body, and one action row whose primary
	// action sits at the right; its left holds the plan count or Unpublish.
	const heading = (text: string) => <p className="truncate text-control font-medium text-foreground">{text}</p>;
	const titled = (action: string) => (
		<p className="truncate text-control text-foreground" data-share-title="">
			<span className="font-semibold">{action}</span> <span className="text-muted-foreground">{publishTitle(name)}</span>
		</p>
	);
	const status = (text: string, role?: "alert" | "status") => (
		<p role={role} className={cn("mt-1.5 text-caption", role === "alert" ? "text-error" : "text-muted-foreground")}>{text}</p>
	);
	const note = (text: string) => (
		<p role="status" className="rounded-md bg-status-in-review/10 px-2.5 py-2 text-caption leading-4 text-status-in-review" data-share-note="">{text}</p>
	);
	// Buttons and inputs inherit their font (styles.css), so the row sets the
	// buttons' size and meta() the smaller secondary text. Labels too long for
	// one line, as in German, wrap with the actions at the right.
	const footer = (left: ReactNode, right: ReactNode) => (
		<div className="mt-4 flex min-h-control-md flex-wrap items-center justify-between gap-2 text-xs">
			{/* Not clipped: Unpublish sits a little outside it, so its label lines up with the title. */}
			<div className="min-w-0">{left}</div>
			<div className="ml-auto flex shrink-0 items-center gap-1.5">{right}</div>
		</div>
	);
	/** Secondary footer text, such as the plan usage. */
	const meta = (text: string) => <span className="block truncate text-caption text-muted-foreground tabular-nums">{text}</span>;
	const spinner = <Loader2 aria-hidden="true" className="size-3 animate-spin motion-reduce:animate-none" />;
	const ownerIdentity = { name: owner?.displayName, email: owner?.email };
	// Under the email field: who the people added there are.
	const viewersNote = <p className="-mt-2 text-caption leading-4 text-muted-foreground" data-share-viewers-note="">{t("artifacts.share.people.whoCanView")}</p>;

	switch (phase.kind) {
		case "loading":
			return <>{titled(t("artifacts.share.title.publish"))}{status(t("artifacts.share.checking"))}</>;
		case "signin":
			return (
				<>
					{heading(t("artifacts.share.signInTitle"))}
					{status(t("artifacts.share.signInDescription"))}
					{footer(null, <Button onClick={() => void aoBridge.cloud.signIn()} size="sm" type="button" variant="primary">{t("artifacts.share.signIn")}</Button>)}
				</>
			);
		case "unsupported":
			return <>{titled(t("artifacts.share.title.publish"))}{status(t("artifacts.share.videoSoon"))}</>;
		case "notOwner":
			return <>{titled(t("artifacts.share.title.publish"))}{status(t("artifacts.share.notOwner"))}</>;
		case "limit":
			return (
				<>
					{heading(t("artifacts.share.upgradeTitle"))}
					{status(t("artifacts.share.limitReached", { limit: phase.limit }))}
					{footer(
						null,
						<Button onClick={() => { onClose(); openSettings("billing"); }} size="sm" type="button" variant="primary">
							{t("artifacts.share.upgrade")}
						</Button>,
					)}
				</>
			);
		case "setup": {
			const { inspection, links } = phase;
			const people: ArtifactPerson[] = staged.map((email) => ({ email, pending: true }));
			return (
				<>
					{titled(t("artifacts.share.title.publish"))}
					{phase.changed ? status(t("artifacts.share.changed"), "status") : phase.unpublished ? status(t("artifacts.share.unpublished")) : null}
					{links.privateLinks ? (
						<div className="mt-3.5 flex flex-col gap-3.5">
							<InviteField
								actionLabel={t("artifacts.share.people.add")}
								onSubmit={(emails) => {
									const next = [...staged, ...emails.map((email) => email.toLowerCase()).filter((email, index, all) => !staged.includes(email) && all.indexOf(email) === index)];
									// One publish shares the link with at most this many; more are added once it is live.
									if (next.length > MAX_INVITES_PER_REQUEST) {
										setStagedOver(true);
										return false;
									}
									setStagedOver(false);
									setStaged(next);
									return true;
								}}
							/>
							{stagedOver ? <p role="alert" className="-mt-2 text-caption leading-4 text-error">{t("artifacts.share.people.tooManyBeforePublish", { count: MAX_INVITES_PER_REQUEST })}</p> : viewersNote}
							<ArtifactPeopleList
								onRemove={(person) => {
									setStagedOver(false);
									setStaged((current) => current.filter((email) => email !== person.email));
								}}
								owner={ownerIdentity}
								people={people}
							/>
							<ArtifactGeneralAccess access={stagedAccess} invited={staged.length} onChange={setStagedAccess} />
						</div>
					) : null}
					{footer(
						links.unlimited ? null : meta(t("artifacts.share.usage", { left: Math.max(0, links.limit - links.used), limit: links.limit })),
						<Button onClick={() => void publish(inspection, links, undefined, { access: stagedAccess, emails: staged })} size="sm" type="button" variant="primary">
							{t("artifacts.share.publish")}
						</Button>,
					)}
				</>
			);
		}
		case "uploading": {
			const sentence = phase.access === "restricted"
				? phase.invited > 0 ? t("artifacts.share.access.willHave", { count: phase.invited }) : t("artifacts.share.access.privateUploading")
				: accessSentence(t, "anyone", 0);
			const { Icon, className: iconClass } = shareAccessIcon(phase.access);
			return (
				<>
					{titled(t("artifacts.share.title.publishing"))}
					<div className="mt-3.5 flex items-start gap-2.5 rounded-md bg-muted/40 py-2 pr-2 pl-2.5" data-share-uploading="">
						<span className={cn("grid size-6 shrink-0 place-items-center rounded-full bg-muted", iconClass)} aria-hidden="true">
							<Icon className="size-3.5" />
						</span>
						<span className="flex min-w-0 flex-col">
							<span className="text-control font-medium text-foreground">{accessName(t, phase.access, phase.invited)}</span>
							<span className="text-xs leading-5 text-muted-foreground">{sentence}</span>
						</span>
					</div>
					{footer(
						null,
						<>
							{/* A cloud file is copied by the control plane in one quick request: nothing to cancel. */}
							{onThisComputer ? (
								<Button onClick={() => void aoBridge.cloudShare.cancel()} size="sm" type="button" variant="ghost">{t("artifacts.share.cancel")}</Button>
							) : null}
							<Button disabled size="sm" type="button" variant="primary">
								{spinner}
								{t("artifacts.share.publishing")}
							</Button>
						</>,
					)}
				</>
			);
		}
		case "published": {
			const { share, grants } = phase;
			const link = artifactShareUrl(share);
			const access: CloudShareAccess = isPrivateShare(share) ? "restricted" : "anyone";
			const invited = grants?.length ?? share.grantCount ?? 0;
			if (phase.unpublishing === "confirm" || phase.unpublishing === "busy") {
				const busy = phase.unpublishing === "busy";
				return (
					<>
						{heading(t("artifacts.share.unpublishTitle"))}
						{status(invited > 0 ? t("artifacts.share.unpublishInvited", { count: invited }) : t("artifacts.share.unpublishDescription"))}
						{invited > 0 ? status(t("artifacts.share.unpublishAgain")) : null}
						{footer(
							null,
							<>
								<Button disabled={busy} onClick={() => unpublishOnOpen ? onClose() : setPhase({ ...phase, unpublishing: undefined })} size="sm" type="button" variant="ghost">
									{t("artifacts.share.cancel")}
								</Button>
								<Button disabled={busy} onClick={() => void unpublish(phase)} size="sm" type="button" variant="destructive">
									{busy ? spinner : null}
									{t(busy ? "artifacts.share.unpublishing" : "artifacts.share.unpublish")}
								</Button>
							</>,
						)}
					</>
				);
			}
			const people: ArtifactPerson[] = (grants ?? []).map((grant) => ({ email: grant.email, id: grant.id }));
			return (
				<>
					{titled(t("artifacts.share.title.published"))}
					{phase.changed ? status(t("artifacts.share.changed"), "status") : null}
					<div className="mt-3.5 flex flex-col gap-3.5">
						{privateLinks ? (
							<>
								<InviteField actionLabel={t("artifacts.share.people.add")} busy={inviting} onSubmit={(emails) => invite(phase, emails)} />
								{viewersNote}
								{phase.grantsError ? (
									<div className="-mt-1 flex items-center justify-between gap-2" role="alert" data-share-grants-error="">
										<p className="text-caption leading-4 text-error">{t("artifacts.share.people.loadFailed")}</p>
										<Button onClick={() => loadGrants(share)} size="xs" type="button" variant="secondary">{t("artifacts.share.retry")}</Button>
									</div>
								) : null}
								<ArtifactPeopleList
									onRemove={(person) => void removePerson(phase, person)}
									owner={ownerIdentity}
									people={people}
									removing={removing}
								/>
							</>
						) : null}
						<ArtifactGeneralAccess
							access={access}
							choosable={privateLinks || access === "restricted"}
							disabled={changingAccess}
							invited={invited}
							onChange={(next) => void changeAccess(phase, next)}
						/>
						{phase.narrowed && access === "restricted" ? note(t("artifacts.share.access.narrowed")) : null}
					</div>
					{phase.error ? status(phase.error, "alert") : null}
					{footer(
						<Button
							className="-ml-2.5"
							onClick={() => setPhase({ ...phase, unpublishing: "confirm" })}
							size="sm"
							type="button"
							variant="destructive-ghost"
						>
							{t("artifacts.share.unpublish")}
						</Button>,
						<>
							<Button
								onClick={() => void aoBridge.clipboard.writeText(link).then(() => setCopied(true))}
								size="sm"
								type="button"
								variant={phase.republish ? "secondary" : "primary"}
							>
								{copied ? <Check aria-hidden="true" className="size-3" /> : <Copy aria-hidden="true" className="size-3" />}
								{t(copied ? "artifacts.share.copied" : "artifacts.share.copyLink")}
							</Button>
							{/* The file changed since it was published here: put the newer version on the link. */}
							{phase.republish ? (
								<Button onClick={() => void publish(phase.republish!.inspection, phase.republish!.links, share)} size="sm" type="button" variant="primary">
									{t("artifacts.share.republish")}
								</Button>
							) : null}
						</>,
					)}
				</>
			);
		}
		case "unpublished":
			return (
				<>
					{titled(t("artifacts.share.title.unpublished"))}
					{status(t(privateLinks ? "artifacts.share.unpublishedDescription" : "artifacts.share.unpublishedDescriptionPublic"))}
					{footer(null, <Button onClick={() => load()} size="sm" type="button" variant="primary">{t("artifacts.share.publish")}</Button>)}
				</>
			);
		case "error":
			return (
				<>
					{titled(t("artifacts.share.title.publish"))}
					{status(t("artifacts.share.failed", { message: phase.message }), "alert")}
					{footer(null, <Button onClick={() => load({ publishNow: !unpublishOnOpen })} size="sm" type="button" variant="secondary">{t("artifacts.share.retry")}</Button>)}
				</>
			);
	}
}
