import { useCallback, useEffect, useState, type ReactElement } from "react";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { Check, Copy, Globe, Share } from "@/components/app-icons";
import { type CloudShareFile, type CloudShareInspection, type CloudShareLink, type CloudShareLinks, type CloudSharePublishResult, artifactShareUrl } from "../../shared/cloud-share";
import { useCloudMeUserId } from "../hooks/useCloudMeUserId";
import { workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { useSettings } from "../hooks/useSettings";
import { aoBridge } from "../lib/bridge";
import type { ArtifactRow } from "../lib/artifacts";
import { useCloudSession } from "../lib/cloud-session";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import { STANDALONE_WORKSPACE_ID } from "../types/workspace";
import { Button } from "./ui/button";
import { Popover, PopoverAnchor, PopoverContent, PopoverTrigger } from "./ui/popover";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

type Phase =
	| { kind: "loading" }
	| { kind: "signin" }
	| { kind: "unsupported" }
	| { kind: "notOwner" }
	| { kind: "limit"; limit: number }
	| { kind: "publish"; inspection: CloudShareInspection; links: CloudShareLinks; changed?: boolean; unpublished?: boolean }
	| { kind: "uploading"; sent: number; total: number }
	| { kind: "published"; share: CloudShareLink; links?: CloudShareLinks; outdated?: OutdatedShare; unpublishing?: "confirm" | "busy"; error?: string }
	| { kind: "error"; message: string };

/**
 * What a published file holds now that its link does not: what Republish puts
 * on the link. Changed says the file moved again after this was offered.
 */
interface OutdatedShare {
	inspection: CloudShareInspection;
	links: CloudShareLinks;
	changed?: boolean;
}

/** Videos are not shared yet: there is no partial-file serving for them. */
export const VIDEO_FILE = /\.(mp4|m4v|mov|webm|ogv|avi|mkv)$/i;
const COPIED_MS = 2_000;

/**
 * A publish still running. It outlives the popover: closing the popover
 * keeps it going, and reopening Share on the file shows its progress again.
 */
interface PendingShare {
	result: Promise<CloudSharePublishResult>;
	links: CloudShareLinks;
	/** The active link this publish updates, for a republish. */
	share?: CloudShareLink;
	sent: number;
	total: number;
}

const pendingShares = new Map<string, PendingShare>();

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
 * they are off, Share is not offered. The answer is kept for this run.
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
 * Brings this computer's publish records up to date with the person's link
 * list, then reads the artifact list again. Records from before links were
 * kept with their URL name only the link's ID, and a link unpublished
 * elsewhere keeps its URL until the list says otherwise; the daemon serves
 * each artifact's shareUrl from those records, so until they are reconciled
 * a published file reads as not shared, or an unpublished one as public.
 * Runs once per mount, and again when sign-in or the control plane changes.
 */
export function useReconciledShareLinks(): void {
	const available = useShareAvailable();
	const { baseUrl, signedIn } = useShareAccess();
	const queryClient = useQueryClient();
	useEffect(() => {
		if (!available || !signedIn) return;
		let cancelled = false;
		aoBridge.cloudShare.list(baseUrl)
			.then(() => {
				if (!cancelled) void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost() });
			})
			// Offline or signed out: the records stay as they are until the next list.
			.catch(() => undefined);
		return () => {
			cancelled = true;
		};
	}, [available, signedIn, baseUrl, queryClient]);
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
	return (
		<Popover open={open} onOpenChange={onOpenChange}>
			<PopoverAnchor asChild>{children}</PopoverAnchor>
			<PopoverContent align="end" className="w-[min(20rem,90vw)] p-4" data-browser-native-overlay="true">
				{open ? <SharePanel file={file} name={name} onClose={() => onOpenChange(false)} unpublishOnOpen={intent === "unpublish"} /> : null}
			</PopoverContent>
		</Popover>
	);
}

/**
 * Whether the file changed since it was published to a link that is still
 * active: what Republish would put on that link, its newer content or its new
 * name. The local fingerprint is checked first, and the link list is asked
 * for only when it differs, or for a file named by its artifact ID: only such
 * a file keeps its link through a rename, and only the link says what name it
 * was published under. Checked again whenever `revision` changes.
 */
function usePublishedOutdated(file: CloudShareFile | null, revision: string): boolean {
	const [outdated, setOutdated] = useState(false);
	const fileKey = file ? JSON.stringify(file) : "";
	useEffect(() => {
		if (!file) return setOutdated(false);
		let cancelled = false;
		void (async () => {
			const inspection = await aoBridge.cloudShare.inspect(file);
			if (!inspection.shareId || !inspection.publishedFingerprint) return false;
			const changed = inspection.publishedFingerprint !== inspection.fingerprint;
			if (!changed && !file.artifactId) return false;
			const links = await aoBridge.cloudShare.list(file.baseUrl);
			const share = links.shares.find((candidate) => candidate.id === inspection.shareId && !candidate.revokedAt);
			return Boolean(share && (changed || share.name !== inspection.name));
		})()
			.catch(() => false)
			.then((result) => {
				if (!cancelled) setOutdated(result);
			});
		return () => {
			cancelled = true;
		};
	}, [fileKey, revision]); // eslint-disable-line react-hooks/exhaustive-deps
	return outdated;
}

/**
 * The Publish button beside an open local artifact's other actions. Clicking it
 * publishes the file as a public link and opens a popover anchored to it with
 * that link to copy or unpublish. Once the file changed since it was
 * published, the button says Republish, and clicking it puts the newer version
 * on the same link. A file that looks like it holds a credential waits for a
 * second click under its warning. For a video the button is disabled and says why.
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
	// Checked again when a publish ends and when the popover closes.
	const [checks, setChecks] = useState(0);
	const recheck = useCallback(() => setChecks((count) => count + 1), []);
	const available = useShareAvailable();
	const { baseUrl, signedIn } = useShareAccess();
	const video = VIDEO_FILE.test(file.path);
	const outdated = usePublishedOutdated(available && signedIn && !video ? { ...file, baseUrl } : null, `${revision}:${checks}`);
	// What the panel does on opening is fixed when it opens, so a check that
	// lands while it is open never starts a second publish.
	const [republishOnOpen, setRepublishOnOpen] = useState(false);
	const label = t(outdated ? "artifacts.share.republish" : "artifacts.share.publish");
	const onOpenChange = (next: boolean) => {
		if (next) setRepublishOnOpen(outdated);
		else recheck();
		setOpen(next);
	};
	const button = (
		<Button
			aria-label={`${label}: ${name}`}
			// Muted like the header's other actions, bright while hovered, and pressed while open.
			className={className}
			disabled={video}
			size={iconOnly ? "icon-sm" : "sm"}
			type="button"
			variant="quiet"
		>
			<Share aria-hidden="true" className="size-icon-sm" />
			{iconOnly ? null : label}
		</Button>
	);
	const popoverTrigger = <PopoverTrigger asChild>{button}</PopoverTrigger>;
	const trigger = iconOnly ? (
		<Tooltip>
			<TooltipTrigger asChild>{popoverTrigger}</TooltipTrigger>
			<TooltipContent side="bottom">{label}</TooltipContent>
		</Tooltip>
	) : popoverTrigger;
	// Not offered where the control plane does not serve share links.
	if (!available) return null;
	if (video) {
		return (
			<Tooltip>
				<TooltipTrigger asChild>
					<span className="inline-flex" tabIndex={0}>{button}</span>
				</TooltipTrigger>
				<TooltipContent side="bottom">{t("artifacts.share.videoSoon")}</TooltipContent>
			</Tooltip>
		);
	}
	// Closing the popover mid-publish keeps the publish going; only Cancel stops it.
	return (
		<Popover open={open} onOpenChange={onOpenChange}>
			{trigger}
			<PopoverContent align="end" className="w-[min(20rem,90vw)] p-4" data-browser-native-overlay="true">
				{open ? <SharePanel file={file} name={name} onClose={() => onOpenChange(false)} onPublished={recheck} republishOnOpen={republishOnOpen} /> : null}
			</PopoverContent>
		</Popover>
	);
}

function SharePanel({
	file,
	name,
	onClose,
	onPublished,
	republishOnOpen = false,
	unpublishOnOpen = false,
}: {
	file: Omit<CloudShareFile, "baseUrl" | "shareId">;
	name: string;
	onClose: () => void;
	/** A publish or republish put the file on its link. */
	onPublished?: () => void;
	/** Opened from a Republish button: put the newer version on the link at once. */
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
	const refreshArtifacts = () => void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost() });
	const { baseUrl, signedIn } = useShareAccess();
	const openSettings = useUiStore((state) => state.openGlobalSettings);
	const [phase, setPhase] = useState<Phase>({ kind: "loading" });
	const [copied, setCopied] = useState(false);
	const shareFile = { ...file, baseUrl };
	const key = pendingKey(baseUrl, file);

	// Shows how a publish ended. Its own refusal is shown as it came, not
	// worked out again from the link list. A republish names its link: a file
	// that changed under it stays on that link, offering the newer version.
	const settle = (result: CloudSharePublishResult, links: CloudShareLinks, share?: CloudShareLink) => {
		switch (result.status) {
			case "published": {
				const published = result.share;
				setPhase({ kind: "published", share: published });
				onPublished?.();
				refreshArtifacts();
				// The plan count now includes this link; it shows once the list says so.
				void aoBridge.cloudShare.list(baseUrl)
					.then((latest) => setPhase((current) => current.kind === "published" && current.share.id === published.id && !current.links ? { ...current, links: latest } : current))
					.catch(() => undefined);
				return;
			}
			case "cancelled":
				// Back to the file, waiting for Publish: opening it again would only redo the upload just stopped.
				return load({ publishNow: false });
			case "limit":
				return setPhase({ kind: "limit", limit: links.limit });
			case "changed":
				if (share) return setPhase({ kind: "published", share, outdated: { inspection: result.inspection, links, changed: true } });
				return setPhase({ kind: "publish", inspection: result.inspection, links, changed: true });
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

	// Opening the panel is the Publish click: a file without an active link is
	// published at once. One that looks like it holds a credential waits for
	// Publish under its warning, and publishNow false (after a cancel or an
	// unpublish) waits too.
	const load = useCallback(({ publishNow = true }: { publishNow?: boolean } = {}) => {
		if (!signedIn) {
			setPhase({ kind: "signin" });
			return () => undefined;
		}
		let cancelled = false;
		const pending = pendingShares.get(key);
		if (pending) {
			setPhase({ kind: "uploading", sent: pending.sent, total: pending.total });
			pending.result
				.then((result) => !cancelled && settle(result, pending.links, pending.share))
				.catch((error: unknown) => !cancelled && setPhase({ kind: "error", message: errorMessage(error) }));
			return () => {
				cancelled = true;
			};
		}
		setPhase({ kind: "loading" });
		Promise.all([aoBridge.cloudShare.inspect({ ...file, baseUrl }), aoBridge.cloudShare.list(baseUrl)])
			.then(([inspection, links]) => {
				if (cancelled) return;
				if (inspection.unsupported) return setPhase({ kind: "unsupported" });
				// The link this file was published to from here, while it is active.
				const current = inspection.shareId ? links.shares.find((share) => share.id === inspection.shareId && !share.revokedAt) : undefined;
				// The link may still have the old content or the old filename.
				const outdated = Boolean(current && (current.name !== inspection.name ||
					(inspection.publishedFingerprint && inspection.publishedFingerprint !== inspection.fingerprint)));
				if (current) {
					if (unpublishOnOpen) return setPhase({ kind: "published", share: current, links, unpublishing: "confirm" });
					// The Republish click, like Publish, needs no second one, unless the newer version looks like it holds a credential.
					if (publishNow && republishOnOpen && outdated && inspection.secretFindings.length === 0) return void publish(inspection, links, current);
					return setPhase({ kind: "published", share: current, links, outdated: outdated ? { inspection, links } : undefined });
				}
				// It was published from here, but its link is unpublished now,
				// usually from somewhere else. Publishing gives it a new link.
				const unpublished = Boolean(inspection.shareId);
				// A file with no active link of its name is most likely a new
				// document; the server decides for sure when it uploads.
				const likelyNew = !links.shares.some((share) => !share.revokedAt && share.name === inspection.name);
				if (!links.unlimited && likelyNew && links.used >= links.limit) return setPhase({ kind: "limit", limit: links.limit });
				if (publishNow && inspection.secretFindings.length === 0) return void publish(inspection, links);
				setPhase({ kind: "publish", inspection, links, unpublished });
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
		if (phase.kind !== "uploading") return;
		return aoBridge.cloudShare.onProgress((progress) => {
			if (progress.sessionId !== file.sessionId || progress.path !== file.path) return;
			const pending = pendingShares.get(key);
			if (pending) Object.assign(pending, { sent: progress.sentBytes, total: progress.totalBytes });
			setPhase({ kind: "uploading", sent: progress.sentBytes, total: progress.totalBytes });
		});
	}, [phase.kind, file.sessionId, file.path, key]);

	useEffect(() => {
		if (!copied) return;
		const timer = setTimeout(() => setCopied(false), COPIED_MS);
		return () => clearTimeout(timer);
	}, [copied]);

	// With share, updates that active link in place: same URL, same slot.
	const publish = async (inspection: CloudShareInspection, links: CloudShareLinks, share?: CloudShareLink) => {
		setPhase({ kind: "uploading", sent: 0, total: 0 });
		// The fingerprint ties the publish to what this popover showed.
		const pending: PendingShare = {
			result: aoBridge.cloudShare.publish({ ...shareFile, fingerprint: inspection.fingerprint, shareId: share?.id }),
			links,
			share,
			sent: 0,
			total: 0,
		};
		pendingShares.set(key, pending);
		try {
			settle(await pending.result, links, share);
		} catch (error) {
			setPhase({ kind: "error", message: errorMessage(error) });
		} finally {
			if (pendingShares.get(key) === pending) pendingShares.delete(key);
		}
	};

	const unpublish = async (shown: Extract<Phase, { kind: "published" }>) => {
		const { share, links, outdated } = shown;
		setPhase({ kind: "published", share, links, outdated, unpublishing: "busy" });
		try {
			await aoBridge.cloudShare.unpublish(baseUrl, share.id);
			refreshArtifacts();
			if (unpublishOnOpen) return onClose();
			load({ publishNow: false });
		} catch (error) {
			setPhase({ kind: "published", share, links, outdated, error: t("artifacts.share.unpublishFailed", { message: errorMessage(error) }) });
		}
	};

	// Every state is a title, an optional status line, and one action row whose
	// primary action sits at the right; its left holds the plan count, the
	// progress or Unpublish.
	const heading = (text: string) => <p className="truncate text-control font-medium text-foreground">{text}</p>;
	const title = heading(publishTitle(name));
	const status = (text: string, role?: "alert" | "status") => (
		<p role={role} className={cn("mt-1.5 text-caption", role === "alert" ? "text-error" : "text-muted-foreground")}>{text}</p>
	);
	// Buttons and inputs inherit their font (styles.css), so the row sets the
	// buttons' size and meta() the smaller secondary text. Labels too long for
	// one line, as with three actions in German, wrap with the actions at the right.
	const footer = (left: React.ReactNode, right: React.ReactNode) => (
		<div className="mt-4 flex min-h-control-md flex-wrap items-center justify-between gap-2 text-xs">
			{/* Not clipped: Unpublish sits a little outside it, so its label lines up with the title. */}
			<div className="min-w-0">{left}</div>
			<div className="ml-auto flex shrink-0 items-center gap-1.5">{right}</div>
		</div>
	);
	/** Secondary footer text, such as the plan usage or upload progress. */
	const meta = (text: string) => <span className="block truncate text-caption text-muted-foreground tabular-nums">{text}</span>;

	switch (phase.kind) {
		case "loading":
			return <>{title}{status(t("artifacts.share.checking"))}</>;
		case "signin":
			return (
				<>
					{heading(t("artifacts.share.signInTitle"))}
					{status(t("artifacts.share.signInDescription"))}
					{footer(null, <Button onClick={() => void aoBridge.cloud.signIn()} size="sm" type="button" variant="primary">{t("artifacts.share.signIn")}</Button>)}
				</>
			);
		case "unsupported":
			return <>{title}{status(t("artifacts.share.videoSoon"))}</>;
		case "notOwner":
			return <>{title}{status(t("artifacts.share.notOwner"))}</>;
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
		case "publish": {
			const { inspection, links } = phase;
			return (
				<>
					{title}
					{phase.changed ? status(t("artifacts.share.changed"), "status") : phase.unpublished ? status(t("artifacts.share.unpublished")) : null}
					{inspection.secretFindings.length > 0 ? status(t("artifacts.share.secrets", { kinds: inspection.secretFindings.join(", ") }), "alert") : null}
					{footer(
						links.unlimited ? null : meta(t("artifacts.share.usage", { left: Math.max(0, links.limit - links.used), limit: links.limit })),
						<Button onClick={() => void publish(inspection, links)} size="sm" type="button" variant="primary">{t("artifacts.share.publish")}</Button>,
					)}
				</>
			);
		}
		case "uploading": {
			const percent = phase.total > 0 ? Math.round((phase.sent / phase.total) * 100) : 0;
			return (
				<>
					{title}
					<div className="mt-3.5 h-1 overflow-hidden rounded-full bg-muted" role="progressbar" aria-label={t("artifacts.share.uploaded", { percent })} aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent}>
						<div className="h-full rounded-full bg-primary transition-[width]" style={{ width: `${percent}%` }} />
					</div>
					{footer(
						meta(t("artifacts.share.uploaded", { percent })),
						<>
							{/* A cloud file is copied by the control plane in one quick request: nothing to cancel. */}
							{file.source === "cloud" ? null : (
								<Button onClick={() => void aoBridge.cloudShare.cancel()} size="sm" type="button" variant="ghost">{t("artifacts.share.cancel")}</Button>
							)}
							<Button disabled size="sm" type="button" variant="primary">{t("artifacts.share.publishing")}</Button>
						</>,
					)}
				</>
			);
		}
		case "published": {
			const { share, links, outdated } = phase;
			const link = artifactShareUrl(share);
			if (phase.unpublishing === "confirm" || phase.unpublishing === "busy") {
				const busy = phase.unpublishing === "busy";
				return (
					<>
						{heading(t("artifacts.share.unpublishTitle"))}
						{status(t("artifacts.share.unpublishDescription"))}
						{footer(
							null,
							<>
								<Button disabled={busy} onClick={() => unpublishOnOpen ? onClose() : setPhase({ kind: "published", share, links, outdated })} size="sm" type="button" variant="ghost">
									{t("artifacts.share.cancel")}
								</Button>
								<Button disabled={busy} onClick={() => void unpublish(phase)} size="sm" type="button" variant="destructive">
									{t("artifacts.share.unpublish")}
								</Button>
							</>,
						)}
					</>
				);
			}
			return (
				<>
					{title}
					<p className="mt-1.5 flex items-center gap-1.5 text-caption text-muted-foreground">
						<Globe aria-hidden="true" className="size-3 shrink-0 text-success" />
						{t("artifacts.share.publicStatus")}
					</p>
					<div className="mt-3.5 text-caption">
						<input
							aria-label={t("artifacts.share.linkLabel")}
							className="block h-control-md w-full truncate rounded-md bg-muted px-2 text-foreground outline-none focus-visible:ring-1 focus-visible:ring-ring"
							onFocus={(event) => event.currentTarget.select()}
							readOnly
							value={link}
						/>
					</div>
					{links && !links.unlimited ? (
						<p className="mt-2 text-caption tabular-nums text-muted-foreground">
							{t("artifacts.share.usage", { left: Math.max(0, links.limit - links.used), limit: links.limit })}
						</p>
					) : null}
					{outdated ? status(t(outdated.changed ? "artifacts.share.changed" : "artifacts.share.outdated"), "status") : null}
					{outdated && outdated.inspection.secretFindings.length > 0 ? status(t("artifacts.share.secrets", { kinds: outdated.inspection.secretFindings.join(", ") }), "alert") : null}
					{phase.error ? status(phase.error, "alert") : null}
					{footer(
						<Button
							className="-ml-2.5"
							onClick={() => setPhase({ kind: "published", share, links, outdated, unpublishing: "confirm" })}
							size="sm"
							type="button"
							variant="destructive-ghost"
						>
							{t("artifacts.share.unpublish")}
						</Button>,
						<>
							<Button onClick={() => void aoBridge.clipboard.writeText(link).then(() => setCopied(true))} size="sm" type="button" variant={outdated ? "secondary" : "primary"}>
								{copied ? <Check aria-hidden="true" className="size-3" /> : <Copy aria-hidden="true" className="size-3" />}
								{t(copied ? "artifacts.share.copied" : "artifacts.share.copyLink")}
							</Button>
							{outdated ? (
								<Button onClick={() => void publish(outdated.inspection, outdated.links, share)} size="sm" type="button" variant="primary">
									{t("artifacts.share.republish")}
								</Button>
							) : null}
						</>,
					)}
				</>
			);
		}
		case "error":
			return (
				<>
					{title}
					{status(t("artifacts.share.failed", { message: phase.message }), "alert")}
					{footer(null, <Button onClick={() => load({ publishNow: !unpublishOnOpen })} size="sm" type="button" variant="secondary">{t("artifacts.share.retry")}</Button>)}
				</>
			);
	}
}
