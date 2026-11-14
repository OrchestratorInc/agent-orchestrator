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
	| { kind: "setup"; inspection: CloudShareInspection; links: CloudShareLinks; changed?: boolean; unpublished?: boolean }
	| { kind: "uploading"; access: CloudShareAccess; invited: number }
	| {
		kind: "published";
		share: CloudShareLink;
		grants?: CloudShareGrant[];
		grantsError?: boolean;
		changed?: boolean;
		unpublishing?: "confirm" | "busy";
		error?: string;
		narrowed?: boolean;
		republish?: { inspection: CloudShareInspection; links: CloudShareLinks };
	}
	| { kind: "unpublished" }
	| { kind: "error"; message: string };

export const VIDEO_FILE = /\.(mp4|m4v|mov|webm|ogv|avi|mkv)$/i;
const COPIED_MS = 2_000;

interface PendingShare {
	result: Promise<CloudSharePublishResult>;
	links: CloudShareLinks;
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

function usePublishing(key: string): boolean {
	return useSyncExternalStore(subscribePending, () => pendingShares.has(key));
}

function pendingKey(baseUrl: string, file: Pick<CloudShareFile, "sessionId" | "path">): string {
	return JSON.stringify([baseUrl, file.sessionId, file.path]);
}

export function publishTitle(name: string): string {
	return name.replace(/(?<=[^.])\.[^.]+$/, "");
}

function errorMessage(error: unknown): string {
	const message = error instanceof Error ? error.message : String(error);
	return message.replace(/^Error invoking remote method '[^']*': (?:\w*Error: )?/, "");
}

const shareAvailability = new Map<string, boolean>();
const shareAvailabilityChecks = new Map<string, Promise<boolean | null>>();

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

export interface ShareScope {
	baseUrl: string;
	account: string;
}

function scopeKey(scope: ShareScope): string {
	return JSON.stringify([scope.baseUrl, scope.account]);
}

interface CachedShareLinks {
	links: CloudShareLinks;
	revision: number;
	at: number;
}
const shareLinksCache = new Map<string, CachedShareLinks>();
const shareLinksReads = new Map<string, Promise<CloudShareLinks>>();
const SHARE_LINKS_FRESH_MS = 5 * 60_000;

function readShareLinks(scope: ShareScope): Promise<CloudShareLinks> {
	const revision = shareLinksRevision;
	const cacheKey = scopeKey(scope);
	const key = JSON.stringify([cacheKey, revision]);
	let read = shareLinksReads.get(key);
	if (!read) {
		read = aoBridge.cloudShare.list(scope.baseUrl)
			.then((links) => {
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

function freshShareLinks(scope: ShareScope): CloudShareLinks | undefined {
	const cached = shareLinksCache.get(scopeKey(scope));
	if (!cached || cached.revision !== shareLinksRevision || Date.now() - cached.at > SHARE_LINKS_FRESH_MS) return undefined;
	return cached.links;
}

let lastShareScope: string | undefined;

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

export function resetShareCachesForTests(): void {
	shareLinksCache.clear();
	shareLinksReads.clear();
	lastShareScope = undefined;
}

export type ShareAccessByUrl = ReadonlyMap<string, CloudShareAccess>;

const NO_SHARE_ACCESS: ShareAccessByUrl = new Map();
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

const reconciledReads = new WeakSet<Promise<CloudShareLinks>>();

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
			.catch(() => undefined);
	}, [available, signedIn, scope, queryClient, revision]);
	return available && signedIn && cached ? shareAccessOf(cached.links) : NO_SHARE_ACCESS;
}

export function useShareAccess(): { baseUrl: string; signedIn: boolean; scope: ShareScope } {
	const { settings } = useSettings();
	const { status, session } = useCloudSession();
	const baseUrl = settings?.cloudControlPlaneUrl ?? "";
	const signedIn = status === "authenticated" && baseUrl !== "";
	const account = signedIn ? session?.user.id ?? "" : "";
	const known = status !== "loading";
	useEffect(() => {
		if (known) noteShareScope({ baseUrl, account });
	}, [known, baseUrl, account]);
	const scope = useMemo(() => ({ baseUrl, account }), [baseUrl, account]);
	return { baseUrl, signedIn, scope };
}

export function useArtifactShareFile(artifact: Pick<ArtifactRow, "id" | "cloud" | "sessionId" | "path" | "projectId" | "sha256"> | null): Omit<CloudShareFile, "baseUrl" | "shareId"> | undefined {
	const cloudUserId = useCloudMeUserId(Boolean(artifact?.cloud));
	if (!artifact) return undefined;
	const artifactId = artifact.id ? { artifactId: artifact.id } : {};
	if (artifact.cloud) {
		const owner = Boolean(cloudUserId) && artifact.cloud.createdByUserId === cloudUserId;
		if (artifact.cloud.shared || !owner) return undefined;
		const version = artifact.sha256 ? { fingerprint: artifact.sha256 } : {};
		return { source: "cloud", orgId: artifact.cloud.orgId, sessionId: artifact.sessionId, path: artifact.path, projectId: "", ...artifactId, ...version };
	}
	return { sessionId: artifact.sessionId, path: artifact.path, projectId: artifact.projectId ?? STANDALONE_WORKSPACE_ID, ...artifactId };
}

const POPOVER_CLASS = "w-[min(22rem,90vw)] p-4";
const MENU_HANDOFF_MS = 400;
export const MAX_INVITES_PER_REQUEST = 50;

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

function linkOutdated(share: Pick<CloudShareLink, "name">, inspection: CloudShareInspection): boolean {
	return share.name !== inspection.name ||
		Boolean(inspection.publishedFingerprint && !sameShareFingerprint(inspection.publishedFingerprint, inspection.fingerprint));
}

interface PublishedShare {
	share?: CloudShareLink;
	outdated: boolean;
	checked?: boolean;
}

function usePublishedShare(file: CloudShareFile | null, scope: ShareScope, revision: string): PublishedShare {
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

export function ShareArtifactButton({ file, name, className, iconOnly = false, revision = "", published = false }: {
	file: Omit<CloudShareFile, "baseUrl" | "shareId">;
	name: string;
	className?: string;
	iconOnly?: boolean;
	revision?: string;
	published?: boolean;
}) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const [checks, setChecks] = useState(0);
	const recheck = useCallback(() => setChecks((count) => count + 1), []);
	const available = useShareAvailable();
	const { baseUrl, signedIn, scope } = useShareAccess();
	const video = VIDEO_FILE.test(file.path);
	const publishing = usePublishing(pendingKey(baseUrl, file));
	const { share, outdated, checked } = usePublishedShare(available && signedIn && !video ? { ...file, baseUrl } : null, scope, `${revision}:${checks}:${publishing}`);
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
	onChanged?: () => void;
	republishOnOpen?: boolean;
	unpublishOnOpen?: boolean;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const refreshArtifacts = () => {
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost() });
		shareLinksChanged();
	};
	const { baseUrl, signedIn, scope } = useShareAccess();
	const owner = useCloudSession().session?.user;
	const openSettings = useUiStore((state) => state.openGlobalSettings);
	const [phase, setPhase] = useState<Phase>({ kind: "loading" });
	const [copied, setCopied] = useState(false);
	const [privateLinks, setPrivateLinks] = useState(false);
	const [stagedAccess, setStagedAccess] = useState<CloudShareAccess>("restricted");
	const [staged, setStaged] = useState<string[]>([]);
	const [stagedOver, setStagedOver] = useState(false);
	const [inviting, setInviting] = useState(false);
	const [removing, setRemoving] = useState<string>();
	const [changingAccess, setChangingAccess] = useState(false);
	const shareFile = { ...file, baseUrl };
	const key = pendingKey(baseUrl, file);
	const onThisComputer = file.source !== "cloud";

	const grantsSeq = useRef(0);
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
		if (isPrivateShare(share) || share.grantCount) loadGrants(share);
	};

	const settle = (result: CloudSharePublishResult, links: CloudShareLinks, share?: CloudShareLink) => {
		switch (result.status) {
			case "published":
				setStaged([]);
				showPublished(result.share);
				onChanged?.();
				refreshArtifacts();
				return;
			case "cancelled":
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
		const decisionKey = (inspection: CloudShareInspection, links: CloudShareLinks) => {
			const current = inspection.shareId ? links.shares.find((share) => share.id === inspection.shareId) : undefined;
			return JSON.stringify([
				links.privateLinks, links.unlimited, links.limit, links.used,
				current ? [current.revokedAt ?? null, current.access ?? null, current.name, current.grantCount ?? 0] : null,
				links.shares.some((share) => !share.revokedAt && share.name === inspection.name),
			]);
		};
		const decide = (inspection: CloudShareInspection, links: CloudShareLinks, publishNow: boolean, shown?: CloudShareLinks) => {
			setPrivateLinks(links.privateLinks);
			if (inspection.unsupported) return setPhase({ kind: "unsupported" });
			const current = inspection.shareId ? links.shares.find((share) => share.id === inspection.shareId && !share.revokedAt) : undefined;
			if (current) {
				if (unpublishOnOpen) return showPublished(current, { unpublishing: "confirm" });
				const outdated = linkOutdated(current, inspection);
				if (publishNow && republishOnOpen && outdated) return void publish(inspection, links, current);
				return showPublished(current, outdated ? { republish: { inspection, links } } : {});
			}
			const unpublished = Boolean(inspection.shareId);
			const likelyNew = !links.shares.some((share) => !share.revokedAt && share.name === inspection.name);
			if (!links.unlimited && likelyNew && links.used >= links.limit) return setPhase({ kind: "limit", limit: links.limit });
			if (publishNow && !onThisComputer) return void publish(inspection, links);
			if (!shown || shown.privateLinks !== links.privateLinks) setStagedAccess(links.privateLinks ? "restricted" : "anyone");
			setPhase({ kind: "setup", inspection, links, unpublished });
		};
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

	const publish = async (inspection: CloudShareInspection, links: CloudShareLinks, share?: CloudShareLink, startsWith?: { access: CloudShareAccess; emails: string[] }) => {
		startAction();
		const access = share ? (isPrivateShare(share) ? "restricted" : "anyone")
			: links.privateLinks ? startsWith?.access ?? "restricted" : "anyone";
		const emails = !share && links.privateLinks ? startsWith?.emails ?? [] : [];
		const invited = share ? share.grantCount ?? 0 : emails.length;
		setPhase({ kind: "uploading", access, invited });
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
	const footer = (left: ReactNode, right: ReactNode) => (
		<div className="mt-4 flex min-h-control-md flex-wrap items-center justify-between gap-2 text-xs">
			<div className="min-w-0">{left}</div>
			<div className="ml-auto flex shrink-0 items-center gap-1.5">{right}</div>
		</div>
	);
	const meta = (text: string) => <span className="block truncate text-caption text-muted-foreground tabular-nums">{text}</span>;
	const spinner = <Loader2 aria-hidden="true" className="size-3 animate-spin motion-reduce:animate-none" />;
	const ownerIdentity = { name: owner?.displayName, email: owner?.email };
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
