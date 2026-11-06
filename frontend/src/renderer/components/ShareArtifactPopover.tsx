import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Check, Copy, Globe, Share } from "lucide-react";
import type { CloudShareFile, CloudShareInspection, CloudShareLink, CloudShareLinks, CloudSharePublishResult } from "../../shared/cloud-share";
import { useSettings } from "../hooks/useSettings";
import { aoBridge } from "../lib/bridge";
import { useCloudSession } from "../lib/cloud-session";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import { Button } from "./ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

type Phase =
	| { kind: "loading" }
	| { kind: "signin" }
	| { kind: "unsupported" }
	| { kind: "notOwner" }
	| { kind: "limit"; limit: number }
	| { kind: "publish"; inspection: CloudShareInspection; links: CloudShareLinks; changed?: boolean; unpublished?: boolean }
	| { kind: "uploading"; sent: number; total: number }
	| { kind: "published"; share: CloudShareLink; outdated?: OutdatedShare; unpublishing?: "confirm" | "busy"; error?: string }
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
const VIDEO_FILE = /\.(mp4|m4v|mov|webm|ogv|avi|mkv)$/i;
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

/** The control plane URL and whether the person is signed in to share. */
export function useShareAccess(): { baseUrl: string; signedIn: boolean } {
	const { settings } = useSettings();
	const { status } = useCloudSession();
	const baseUrl = settings?.cloudControlPlaneUrl ?? "";
	return { baseUrl, signedIn: status === "authenticated" && baseUrl !== "" };
}

/**
 * The Share button beside an open local artifact's other actions. It opens a
 * popover anchored to it: publish the file as a public link, or, once it is
 * published, copy the link, unpublish it, or republish a file that changed
 * since. For a video the button is disabled and says why.
 */
export function ShareArtifactButton({ file, name, className }: { file: Omit<CloudShareFile, "baseUrl" | "shareId">; name: string; className?: string }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const available = useShareAvailable();
	const video = VIDEO_FILE.test(file.path);
	const button = (
		<Button
			aria-label={`${t("artifacts.share.menu")}: ${name}`}
			// Muted like the header's other actions, bright while hovered, and pressed while open.
			className={className}
			disabled={video}
			size="sm"
			type="button"
			variant="quiet"
		>
			<Share aria-hidden="true" className="size-icon-sm" />
			{t("artifacts.share.menu")}
		</Button>
	);
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
		<Popover open={open} onOpenChange={setOpen}>
			<PopoverTrigger asChild>{button}</PopoverTrigger>
			<PopoverContent align="end" className="w-[min(18.5rem,90vw)] p-3" data-browser-native-overlay="true">
				{open ? <SharePanel file={file} name={name} onClose={() => setOpen(false)} /> : null}
			</PopoverContent>
		</Popover>
	);
}

function SharePanel({
	file,
	name,
	onClose,
}: {
	file: Omit<CloudShareFile, "baseUrl" | "shareId">;
	name: string;
	onClose: () => void;
}) {
	const { t } = useTranslation();
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
			case "published":
				return setPhase({ kind: "published", share: result.share });
			case "cancelled":
				return load();
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

	const load = useCallback(() => {
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
				// The file changed since it was published here: the link still
				// serves what it held then.
				const outdated = Boolean(inspection.publishedFingerprint && inspection.publishedFingerprint !== inspection.fingerprint);
				if (current) return setPhase({ kind: "published", share: current, outdated: outdated ? { inspection, links } : undefined });
				// It was published from here, but its link is unpublished now,
				// usually from somewhere else. Publishing gives it a new link.
				const unpublished = Boolean(inspection.shareId);
				// A file with no active link of its name is most likely a new
				// document; the server decides for sure when it uploads.
				const likelyNew = !links.shares.some((share) => !share.revokedAt && share.name === inspection.name);
				if (!links.unlimited && likelyNew && links.used >= links.limit) return setPhase({ kind: "limit", limit: links.limit });
				setPhase({ kind: "publish", inspection, links, unpublished });
			})
			.catch((error: unknown) => {
				if (!cancelled) setPhase({ kind: "error", message: errorMessage(error) });
			});
		return () => {
			cancelled = true;
		};
	}, [signedIn, baseUrl, key, file.projectId]); // eslint-disable-line react-hooks/exhaustive-deps

	useEffect(() => load(), [load]);

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

	const unpublish = async (share: CloudShareLink, outdated?: OutdatedShare) => {
		setPhase({ kind: "published", share, outdated, unpublishing: "busy" });
		try {
			await aoBridge.cloudShare.unpublish(baseUrl, share.id);
			load();
		} catch (error) {
			setPhase({ kind: "published", share, outdated, error: t("artifacts.share.unpublishFailed", { message: errorMessage(error) }) });
		}
	};

	// Every state is a title, an optional status line, and one action row whose
	// primary action sits at the right; its left holds the plan count, the
	// progress or Unpublish.
	const heading = (text: string) => <p className="truncate text-control font-medium text-foreground">{text}</p>;
	const title = heading(t("artifacts.share.title", { name }));
	const status = (text: string, role?: "alert" | "status") => (
		<p role={role} className={cn("mt-1 text-caption", role === "alert" ? "text-error" : "text-muted-foreground")}>{text}</p>
	);
	// Buttons and inputs inherit their font (styles.css), so the row sets the
	// buttons' size and meta() the smaller secondary text. Labels too long for
	// one line, as with three actions in German, wrap with the actions at the right.
	const footer = (left: React.ReactNode, right: React.ReactNode) => (
		<div className="mt-3 flex min-h-control-md flex-wrap items-center justify-between gap-2 text-xs">
			<div className="min-w-0 truncate">{left}</div>
			<div className="ml-auto flex shrink-0 items-center gap-1.5">{right}</div>
		</div>
	);
	const meta = (text: string) => <span className="text-caption text-muted-foreground tabular-nums">{text}</span>;

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
					<div className="mt-2.5 h-1 overflow-hidden rounded-full bg-muted" role="progressbar" aria-label={t("artifacts.share.uploaded", { percent })} aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent}>
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
			const { share, outdated } = phase;
			const link = share.viewerUrl ?? share.contentUrl;
			if (phase.unpublishing === "confirm" || phase.unpublishing === "busy") {
				const busy = phase.unpublishing === "busy";
				return (
					<>
						{heading(t("artifacts.share.unpublishTitle"))}
						{status(t("artifacts.share.unpublishDescription"))}
						{footer(
							null,
							<>
								<Button disabled={busy} onClick={() => setPhase({ kind: "published", share, outdated })} size="sm" type="button" variant="ghost">
									{t("artifacts.share.cancel")}
								</Button>
								<Button disabled={busy} onClick={() => void unpublish(share, outdated)} size="sm" type="button" variant="destructive">
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
					<p className="mt-1 flex items-center gap-1.5 text-caption text-muted-foreground">
						<Globe aria-hidden="true" className="size-3 shrink-0 text-success" />
						{t("artifacts.share.publicStatus")}
					</p>
					<div className="mt-2.5 font-mono text-caption">
						<input
							aria-label={t("artifacts.share.linkLabel")}
							className="block h-control-md w-full truncate rounded-md bg-muted px-2 text-foreground outline-none focus-visible:ring-1 focus-visible:ring-ring"
							onFocus={(event) => event.currentTarget.select()}
							readOnly
							value={link}
						/>
					</div>
					{outdated ? status(t(outdated.changed ? "artifacts.share.changed" : "artifacts.share.outdated"), "status") : null}
					{outdated && outdated.inspection.secretFindings.length > 0 ? status(t("artifacts.share.secrets", { kinds: outdated.inspection.secretFindings.join(", ") }), "alert") : null}
					{phase.error ? status(phase.error, "alert") : null}
					{footer(
						<Button
							className="-ml-2.5"
							onClick={() => setPhase({ kind: "published", share, outdated, unpublishing: "confirm" })}
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
					{footer(null, <Button onClick={() => load()} size="sm" type="button" variant="secondary">{t("artifacts.share.retry")}</Button>)}
				</>
			);
	}
}
