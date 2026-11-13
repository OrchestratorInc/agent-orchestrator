import { useCallback, useEffect, useRef, useState, useSyncExternalStore, type ReactElement, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { Check, ChevronDown, Copy, Globe, Loader2, Lock, Share } from "@/components/app-icons";
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
	/**
	 * Before the first publish of a file on this computer (or of any file that
	 * looks like it holds a credential where links are public only): who it is
	 * for is set here, and Publish uploads it.
	 */
	| { kind: "setup"; inspection: CloudShareInspection; links: CloudShareLinks; changed?: boolean; unpublished?: boolean }
	| { kind: "uploading"; access: CloudShareAccess; invited: number }
	| {
		kind: "published";
		share: CloudShareLink;
		/** The file as last inspected, for its credential warning. */
		inspection?: CloudShareInspection;
		/** Who the link is shared with, once read. */
		grants?: CloudShareGrant[];
		/** Reading who the link is shared with failed: say so and offer to try again. */
		grantsError?: boolean;
		/** Anyone with the link was chosen for a file that looks like it holds a credential: confirm first. */
		confirmPublic?: boolean;
		/** The file changed while it was being republished: say so, and offer its newest version. */
		changed?: boolean;
		unpublishing?: "confirm" | "busy";
		error?: string;
		/** The link was just made private: say what that does to people without an invite. */
		narrowed?: boolean;
		/**
		 * The file changed since it was published to this link: Republish puts
		 * the newer version on it. Under a credential warning on a public link,
		 * the toolbar's Republish waits here too.
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
	/** The file as inspected for this publish, for the published link's credential warning. */
	inspection: CloudShareInspection;
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

/**
 * What a link's file looks like it holds: the file now, and the copy last
 * published to the link, which may still hold a credential since removed.
 */
function linkFindings(inspection: CloudShareInspection | undefined): string[] {
	if (!inspection) return [];
	return [...new Set([...inspection.secretFindings, ...(inspection.publishedSecretFindings ?? [])])];
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
 * so useReconciledShareLinks reads the link list again.
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

/** Who each active link opens for, by the URL a published artifact carries as its shareUrl. */
export type ShareAccessByUrl = ReadonlyMap<string, CloudShareAccess>;

/**
 * Brings this computer's publish records up to date with the person's link
 * list, then reads the artifact list again. Records from before links were
 * kept with their URL name only the link's ID, and a link unpublished
 * elsewhere keeps its URL until the list says otherwise; the daemon serves
 * each artifact's shareUrl from those records, so until they are reconciled
 * a published file reads as not shared, or an unpublished one as public.
 * Runs once per mount, again when sign-in or the control plane changes, and
 * again after a panel changes a link. Returns who each link opens for, so a
 * private link is not marked public; empty until the list is read.
 */
export function useReconciledShareLinks(): ShareAccessByUrl {
	const available = useShareAvailable();
	const { baseUrl, signedIn } = useShareAccess();
	const queryClient = useQueryClient();
	const revision = useSyncExternalStore(subscribeShareLinks, () => shareLinksRevision);
	const [accessByUrl, setAccessByUrl] = useState<ShareAccessByUrl>(() => new Map());
	useEffect(() => {
		if (!available || !signedIn) return;
		let cancelled = false;
		aoBridge.cloudShare.list(baseUrl)
			.then((links) => {
				if (cancelled) return;
				setAccessByUrl(new Map(links.shares
					.filter((share) => !share.revokedAt)
					.map((share): [string, CloudShareAccess] => [artifactShareUrl(share), isPrivateShare(share) ? "restricted" : "anyone"])));
				void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost() });
			})
			// Offline or signed out: the records stay as they are until the next list.
			.catch(() => undefined);
		return () => {
			cancelled = true;
		};
	}, [available, signedIn, baseUrl, queryClient, revision]);
	return accessByUrl;
}

/** The control plane URL and whether the person is signed in to share. */
export function useShareAccess(): { baseUrl: string; signedIn: boolean } {
	const { settings } = useSettings();
	const { status } = useCloudSession();
	const baseUrl = settings?.cloudControlPlaneUrl ?? "";
	return { baseUrl, signedIn: status === "authenticated" && baseUrl !== "" };
}

/**
 * The file an artifact is shared as, or undefined where the person cannot
 * share it: only the person who started a cloud session publishes its files,
 * and not once they are shared. A Scratchpad file belongs to the Scratchpad
 * workspace, as in the session view.
 */
export function useArtifactShareFile(artifact: Pick<ArtifactRow, "id" | "cloud" | "sessionId" | "path" | "projectId"> | null): Omit<CloudShareFile, "baseUrl" | "shareId"> | undefined {
	const cloudUserId = useCloudMeUserId(Boolean(artifact?.cloud));
	if (!artifact) return undefined;
	// The artifact's ID survives a rename, so the renamed file finds its link.
	const artifactId = artifact.id ? { artifactId: artifact.id } : {};
	if (artifact.cloud) {
		const owner = Boolean(cloudUserId) && artifact.cloud.createdByUserId === cloudUserId;
		if (artifact.cloud.shared || !owner) return undefined;
		return { source: "cloud", orgId: artifact.cloud.orgId, sessionId: artifact.sessionId, path: artifact.path, projectId: "", ...artifactId };
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

/** The file's active link, and whether the file changed since it was published to it. */
interface PublishedShare {
	share?: CloudShareLink;
	outdated: boolean;
}

/**
 * The file's active link as the toolbar shows it: Published with its access,
 * and whether the file changed since (its newer content or its new name),
 * which Republish would put on the link. Checked again whenever `revision`
 * changes.
 */
function usePublishedShare(file: CloudShareFile | null, revision: string): PublishedShare {
	// Kept with the file it is for: a button reused for another file shows
	// nothing published until that file is checked, never the last file's link.
	const [state, setState] = useState<PublishedShare & { fileKey: string }>({ outdated: false, fileKey: "" });
	const fileKey = file ? JSON.stringify(file) : "";
	useEffect(() => {
		if (!file) return setState({ outdated: false, fileKey });
		let cancelled = false;
		void (async (): Promise<PublishedShare> => {
			const inspection = await aoBridge.cloudShare.inspect(file);
			if (!inspection.shareId) return { outdated: false };
			const links = await aoBridge.cloudShare.list(file.baseUrl);
			const share = links.shares.find((candidate) => candidate.id === inspection.shareId && !candidate.revokedAt);
			if (!share) return { outdated: false };
			const changed = Boolean(inspection.publishedFingerprint && inspection.publishedFingerprint !== inspection.fingerprint);
			return { share, outdated: changed || share.name !== inspection.name };
		})()
			.catch((): PublishedShare => ({ outdated: false }))
			.then((result) => {
				if (!cancelled) setState({ ...result, fileKey });
			});
		return () => {
			cancelled = true;
		};
	}, [fileKey, revision]); // eslint-disable-line react-hooks/exhaustive-deps
	return state.fileKey === fileKey ? { share: state.share, outdated: state.outdated } : { outdated: false };
}

/**
 * The Publish button beside an open artifact's other actions. Clicking it
 * publishes the file, privately where links can be private, and opens a
 * popover anchored to it on the published link: invite people, choose who it
 * opens for, copy it or unpublish it. A file on this computer opens the
 * popover first, to choose who it is for, and its Publish there uploads it.
 * Once published, the button reads Published with a lock or a globe. Once the
 * file changed since, it splits: Republish puts the newer version on the same
 * link, and the lock or globe opens the popover without republishing. For a
 * video the button is disabled and says why.
 */
export function ShareArtifactButton({ file, name, className, iconOnly = false, revision = "" }: {
	file: Omit<CloudShareFile, "baseUrl" | "shareId">;
	name: string;
	className?: string;
	iconOnly?: boolean;
	/** The file's last change, e.g. its updatedAt: a new value checks again whether it needs Republish. */
	revision?: string;
}) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	// Checked again when the panel changes the link and when it closes.
	const [checks, setChecks] = useState(0);
	const recheck = useCallback(() => setChecks((count) => count + 1), []);
	const available = useShareAvailable();
	const { baseUrl, signedIn } = useShareAccess();
	const video = VIDEO_FILE.test(file.path);
	const publishing = usePublishing(pendingKey(baseUrl, file));
	const { share, outdated } = usePublishedShare(available && signedIn && !video ? { ...file, baseUrl } : null, `${revision}:${checks}:${publishing}`);
	// What the panel does on opening is fixed when it opens, so a check that
	// lands while it is open never starts a second publish.
	const [republishOnOpen, setRepublishOnOpen] = useState(false);
	const openPanel = (republish: boolean) => {
		setRepublishOnOpen(republish);
		setOpen(true);
	};
	const onOpenChange = (next: boolean) => {
		if (next) setRepublishOnOpen(outdated);
		else recheck();
		setOpen(next);
	};
	useCloseWhenFrameFocused(open, () => onOpenChange(false));
	const isPublic = Boolean(share && !isPrivateShare(share));
	const AccessIcon = isPublic ? Globe : Lock;
	// A public link's globe is green, as in General access: anyone can open it.
	const accessIconClass = cn("size-icon-sm", isPublic && "text-success");
	const label = publishing ? t("artifacts.share.publishing")
		: outdated ? t("artifacts.share.republish")
		: share ? t("artifacts.share.published")
		: t("artifacts.share.publish");
	const icon = publishing ? <Loader2 aria-hidden="true" className="size-icon-sm animate-spin motion-reduce:animate-none" />
		: share && !outdated ? <AccessIcon aria-hidden="true" className={accessIconClass} />
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
	// The file changed since it was published: Republish on the left, the link's
	// access on the right, which opens the popover as it is.
	if (outdated && share && !publishing && !iconOnly) {
		return (
			<Popover open={open} onOpenChange={onOpenChange}>
				<PopoverAnchor asChild>
					<span className={cn("inline-flex items-center", className)} data-share-split="">
						<Tooltip>
							<TooltipTrigger asChild>
								<Button aria-label={`${label}: ${name}`} className="relative" onClick={() => openPanel(true)} size="sm" type="button" variant="quiet">
									<Share aria-hidden="true" className="size-icon-sm" />
									{label}
									{amberDot}
								</Button>
							</TooltipTrigger>
							<TooltipContent side="bottom">{t("artifacts.share.republishHint")}</TooltipContent>
						</Tooltip>
						<Button
							aria-label={t("artifacts.share.options", { name })}
							className="gap-0.5 px-1.5"
							data-state={open ? "open" : "closed"}
							onClick={() => openPanel(false)}
							size="sm"
							type="button"
							variant="quiet"
						>
							<AccessIcon aria-hidden="true" className={accessIconClass} />
							<ChevronDown aria-hidden="true" className="size-3" />
						</Button>
					</span>
				</PopoverAnchor>
				{panel}
			</Popover>
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
			{iconOnly ? (
				<Tooltip>
					<TooltipTrigger asChild>{popoverTrigger}</TooltipTrigger>
					<TooltipContent side="bottom">{outdated ? t("artifacts.share.republishHint") : label}</TooltipContent>
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
	const { baseUrl, signedIn } = useShareAccess();
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
	const settle = (result: CloudSharePublishResult, links: CloudShareLinks, share?: CloudShareLink, inspection?: CloudShareInspection) => {
		switch (result.status) {
			case "published":
				setStaged([]);
				showPublished(result.share, { inspection });
				onChanged?.();
				refreshArtifacts();
				return;
			case "cancelled":
				// Back to the file, waiting for Publish: opening it again would only redo the upload just stopped.
				return load({ publishNow: false });
			case "limit":
				return setPhase({ kind: "limit", limit: links.limit });
			case "changed":
				if (share) return showPublished(share, { inspection: result.inspection, republish: { inspection: result.inspection, links }, changed: true });
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
	// computer opens on its setup, and so does one that looks like it holds a
	// credential where links are public only. publishNow false (after a
	// cancel) waits too.
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
				.then((result) => !cancelled && settle(result, pending.links, pending.share, pending.inspection))
				.catch((error: unknown) => !cancelled && setPhase({ kind: "error", message: errorMessage(error) }));
			return () => {
				cancelled = true;
			};
		}
		setPhase({ kind: "loading" });
		Promise.all([aoBridge.cloudShare.inspect({ ...file, baseUrl }), aoBridge.cloudShare.list(baseUrl)])
			.then(([inspection, links]) => {
				if (cancelled) return;
				setPrivateLinks(links.privateLinks);
				if (inspection.unsupported) return setPhase({ kind: "unsupported" });
				// The link this file was published to from here, while it is active.
				const current = inspection.shareId ? links.shares.find((share) => share.id === inspection.shareId && !share.revokedAt) : undefined;
				if (current) {
					if (unpublishOnOpen) return showPublished(current, { inspection, unpublishing: "confirm" });
					// The link may still have the old content or the old filename.
					const outdated = current.name !== inspection.name ||
						Boolean(inspection.publishedFingerprint && inspection.publishedFingerprint !== inspection.fingerprint);
					if (publishNow && republishOnOpen && outdated) {
						// The Republish click, like Publish, needs no second one. A
						// public link waits under a credential warning.
						if (inspection.secretFindings.length > 0 && !isPrivateShare(current)) return showPublished(current, { inspection, republish: { inspection, links } });
						return void publish(inspection, links, current);
					}
					// Changed since: Republish is offered on the link, wherever the panel was opened.
					return showPublished(current, { inspection, ...(outdated ? { republish: { inspection, links } } : {}) });
				}
				// It was published from here, but its link is unpublished now,
				// usually from somewhere else. Publishing gives it a new link.
				const unpublished = Boolean(inspection.shareId);
				// A file with no active link of its name is most likely a new
				// document; the server decides for sure when it uploads.
				const likelyNew = !links.shares.some((share) => !share.revokedAt && share.name === inspection.name);
				if (!links.unlimited && likelyNew && links.used >= links.limit) return setPhase({ kind: "limit", limit: links.limit });
				// A private link exposes nothing until people are invited, so it
				// publishes at once whatever the file holds; its warning shows on the link.
				const atOnce = !onThisComputer && (links.privateLinks || inspection.secretFindings.length === 0);
				if (publishNow && atOnce) return void publish(inspection, links);
				setStagedAccess(links.privateLinks ? "restricted" : "anyone");
				setPhase({ kind: "setup", inspection, links, unpublished });
			})
			.catch((error: unknown) => {
				if (!cancelled) setPhase({ kind: "error", message: errorMessage(error) });
			});
		return () => {
			cancelled = true;
		};
	}, [signedIn, baseUrl, key, file.projectId]); // eslint-disable-line react-hooks/exhaustive-deps

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
			inspection,
		};
		setPending(key, pending);
		try {
			settle(await pending.result, links, share, inspection);
		} catch (error) {
			setPhase({ kind: "error", message: errorMessage(error) });
		} finally {
			if (pendingShares.get(key) === pending) setPending(key, undefined);
		}
	};

	const unpublish = async (shown: Extract<Phase, { kind: "published" }>) => {
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

	const changeAccess = async (shown: Extract<Phase, { kind: "published" }>, access: CloudShareAccess, confirmed = false) => {
		const findings = linkFindings(shown.republish?.inspection ?? shown.inspection);
		// Making a link that looks like it holds a credential public waits for a confirm.
		if (access === "anyone" && findings.length > 0 && !confirmed) {
			setPhase({ ...shown, confirmPublic: true });
			return;
		}
		setPhase((current) => current.kind === "published" ? { ...current, confirmPublic: undefined } : current);
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
							<ArtifactGeneralAccess access={stagedAccess} invited={staged.length} onChange={setStagedAccess} secretFindings={inspection.secretFindings} />
						</div>
					) : null}
					{inspection.secretFindings.length > 0
						? status(t(links.privateLinks && stagedAccess === "restricted" ? "artifacts.share.secretsPrivate" : "artifacts.share.secrets", { kinds: inspection.secretFindings.join(", ") }), "alert")
						: null}
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
			const Icon = phase.access === "anyone" ? Globe : Lock;
			return (
				<>
					{titled(t("artifacts.share.title.publishing"))}
					<div className="mt-3.5 flex items-start gap-2.5 rounded-md bg-muted/40 py-2 pr-2 pl-2.5" data-share-uploading="">
						<span className={cn("grid size-6 shrink-0 place-items-center rounded-full bg-muted", phase.access === "anyone" ? "text-success" : "text-muted-foreground")} aria-hidden="true">
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
			const { share, grants, inspection } = phase;
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
			const findings = linkFindings(phase.republish?.inspection ?? inspection);
			if (phase.confirmPublic) {
				return (
					<>
						{heading(t("artifacts.share.access.confirmPublicTitle"))}
						{status(t("artifacts.share.access.secretsPublic", { kinds: findings.join(", ") }), "alert")}
						{footer(
							null,
							<>
								<Button onClick={() => setPhase({ ...phase, confirmPublic: undefined })} size="sm" type="button" variant="ghost">
									{t("artifacts.share.cancel")}
								</Button>
								<Button onClick={() => void changeAccess(phase, "anyone", true)} size="sm" type="button" variant="primary">
									{t("artifacts.share.access.confirmPublic")}
								</Button>
							</>,
						)}
					</>
				);
			}
			return (
				<>
					{titled(t("artifacts.share.title.published"))}
					{phase.changed ? status(t("artifacts.share.changed"), "status") : null}
					<div className="mt-3.5 flex flex-col gap-3.5">
						{privateLinks ? (
							<>
								<InviteField actionLabel={t("artifacts.share.people.invite")} busy={inviting} onSubmit={(emails) => invite(phase, emails)} />
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
							secretFindings={findings}
						/>
						{phase.narrowed && access === "restricted" ? note(t("artifacts.share.access.narrowed")) : null}
					</div>
					{phase.republish && access === "anyone" && findings.length > 0 ? status(t("artifacts.share.secrets", { kinds: findings.join(", ") }), "alert")
						: findings.length > 0 ? status(t(access === "restricted" ? "artifacts.share.secretsPrivate" : "artifacts.share.secretsLink", { kinds: findings.join(", ") }), "alert")
						: null}
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
