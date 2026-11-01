import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Check, Copy, Share } from "lucide-react";
import type { CloudShareFile, CloudShareInspection, CloudShareLink, CloudShareLinks } from "../../shared/cloud-share";
import { useSettings } from "../hooks/useSettings";
import { aoBridge } from "../lib/bridge";
import { useCloudSession } from "../lib/cloud-session";
import { useUiStore } from "../stores/ui-store";
import { Button } from "./ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

type Phase =
	| { kind: "loading" }
	| { kind: "signin" }
	| { kind: "unsupported" }
	| { kind: "limit"; limit: number }
	| { kind: "publish"; inspection: CloudShareInspection; links: CloudShareLinks }
	| { kind: "uploading"; sent: number; total: number }
	| { kind: "published"; share: CloudShareLink; unpublishing?: "confirm" | "busy"; error?: string }
	| { kind: "error"; message: string };

/** Videos are not shared yet: there is no partial-file serving for them. */
const VIDEO_FILE = /\.(mp4|m4v|mov|webm|ogv|avi|mkv)$/i;
const COPIED_MS = 2_000;

/** "2.0 KB", "64 MB": a byte count for the share popover. */
export function formatBytes(bytes: number): string {
	if (bytes < 1024) return `${bytes} B`;
	const units = ["KB", "MB", "GB", "TB"];
	let value = bytes / 1024;
	let unit = 0;
	while (value >= 1024 && unit < units.length - 1) {
		value /= 1024;
		unit++;
	}
	return `${value < 10 ? value.toFixed(1) : Math.round(value)} ${units[unit]}`;
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
 * published, copy the link, update it or unpublish it. For a video the button
 * is disabled and says why.
 */
export function ShareArtifactButton({ file, name, className }: { file: Omit<CloudShareFile, "baseUrl" | "shareId">; name: string; className?: string }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const cancelUpload = useRef<(() => void) | null>(null);
	const video = VIDEO_FILE.test(file.path);
	const button = (
		<Button
			aria-label={`${t("artifacts.share.menu")}: ${name}`}
			className={className}
			disabled={video}
			size="sm"
			type="button"
			variant="ghost"
		>
			<Share aria-hidden="true" className="size-icon-sm" />
			{t("artifacts.share.menu")}
		</Button>
	);
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
	// Closing the popover mid-upload cancels the upload.
	const changeOpen = (next: boolean) => {
		if (!next) cancelUpload.current?.();
		setOpen(next);
	};
	return (
		<Popover open={open} onOpenChange={changeOpen}>
			<PopoverTrigger asChild>{button}</PopoverTrigger>
			<PopoverContent align="end" className="w-[min(22rem,90vw)] p-3" data-browser-native-overlay="true">
				{open ? <SharePanel file={file} name={name} onClose={() => changeOpen(false)} cancelUpload={cancelUpload} /> : null}
			</PopoverContent>
		</Popover>
	);
}

function SharePanel({
	file,
	name,
	onClose,
	cancelUpload,
}: {
	file: Omit<CloudShareFile, "baseUrl" | "shareId">;
	name: string;
	onClose: () => void;
	cancelUpload: React.MutableRefObject<(() => void) | null>;
}) {
	const { t } = useTranslation();
	const { baseUrl, signedIn } = useShareAccess();
	const openSettings = useUiStore((state) => state.openGlobalSettings);
	const [phase, setPhase] = useState<Phase>({ kind: "loading" });
	const [copied, setCopied] = useState(false);
	const shareFile = { ...file, baseUrl };

	const load = useCallback(() => {
		if (!signedIn) {
			setPhase({ kind: "signin" });
			return () => undefined;
		}
		let cancelled = false;
		setPhase({ kind: "loading" });
		Promise.all([aoBridge.cloudShare.inspect({ ...file, baseUrl }), aoBridge.cloudShare.list(baseUrl)])
			.then(([inspection, links]) => {
				if (cancelled) return;
				if (inspection.unsupported) return setPhase({ kind: "unsupported" });
				// The link this file was published to from here, while it is active.
				const current = inspection.shareId ? links.shares.find((share) => share.id === inspection.shareId && !share.revokedAt) : undefined;
				if (current) return setPhase({ kind: "published", share: current });
				// A file with no active link of its name is most likely a new
				// document; the server decides for sure when it uploads.
				const likelyNew = !links.shares.some((share) => !share.revokedAt && share.name === inspection.name);
				if (!links.unlimited && likelyNew && links.used >= links.limit) return setPhase({ kind: "limit", limit: links.limit });
				setPhase({ kind: "publish", inspection, links });
			})
			.catch((error: unknown) => {
				if (!cancelled) setPhase({ kind: "error", message: error instanceof Error ? error.message : String(error) });
			});
		return () => {
			cancelled = true;
		};
	}, [signedIn, baseUrl, file.sessionId, file.path, file.projectId]); // eslint-disable-line react-hooks/exhaustive-deps

	useEffect(() => load(), [load]);

	useEffect(() => {
		if (phase.kind !== "uploading") return;
		return aoBridge.cloudShare.onProgress((progress) => {
			if (progress.sessionId !== file.sessionId || progress.path !== file.path) return;
			setPhase({ kind: "uploading", sent: progress.sentBytes, total: progress.totalBytes });
		});
	}, [phase.kind, file.sessionId, file.path]);

	useEffect(() => {
		if (!copied) return;
		const timer = setTimeout(() => setCopied(false), COPIED_MS);
		return () => clearTimeout(timer);
	}, [copied]);

	const publish = async (shareId?: string) => {
		setPhase({ kind: "uploading", sent: 0, total: 0 });
		cancelUpload.current = () => void aoBridge.cloudShare.cancel();
		try {
			const result = await aoBridge.cloudShare.publish({ ...shareFile, shareId });
			switch (result.status) {
				case "published":
					return setPhase({ kind: "published", share: result.share });
				case "cancelled":
					return load();
				case "limit":
					return load();
				case "unsupported":
					return setPhase({ kind: "unsupported" });
				case "signed_out":
					return setPhase({ kind: "signin" });
			}
		} catch (error) {
			setPhase({ kind: "error", message: error instanceof Error ? error.message : String(error) });
		} finally {
			cancelUpload.current = null;
		}
	};

	const unpublish = async (share: CloudShareLink) => {
		setPhase({ kind: "published", share, unpublishing: "busy" });
		try {
			await aoBridge.cloudShare.unpublish(baseUrl, share.id);
			load();
		} catch (error) {
			setPhase({ kind: "published", share, error: t("artifacts.share.unpublishFailed", { message: error instanceof Error ? error.message : String(error) }) });
		}
	};

	const title = <p className="truncate text-control font-medium text-foreground">{t("artifacts.share.title", { name })}</p>;
	const note = (text: string) => <p className="mt-1 text-caption text-muted-foreground">{text}</p>;
	const actions = (children: React.ReactNode) => <div className="mt-3 flex items-center justify-end gap-2">{children}</div>;

	switch (phase.kind) {
		case "loading":
			return <>{title}{note(t("artifacts.share.checking"))}</>;
		case "signin":
			return (
				<>
					<p className="text-control font-medium text-foreground">{t("artifacts.share.signInTitle")}</p>
					{note(t("artifacts.share.signInDescription"))}
					{actions(<Button onClick={() => void aoBridge.cloud.signIn()} size="sm" type="button" variant="primary">{t("artifacts.share.signIn")}</Button>)}
				</>
			);
		case "unsupported":
			return <>{title}{note(t("artifacts.share.videoSoon"))}</>;
		case "limit":
			return (
				<>
					<p className="text-control font-medium text-foreground">{t("artifacts.share.upgradeTitle")}</p>
					{note(t("artifacts.share.limitReached", { limit: phase.limit }))}
					{actions(
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
					{note(t("artifacts.share.public"))}
					<p className="mt-1 text-caption text-muted-foreground">
						{formatBytes(inspection.size)}
						{links.unlimited ? null : ` · ${t("artifacts.share.usage", { used: links.used + 1, limit: links.limit })}`}
					</p>
					{inspection.secretFindings.length > 0 ? (
						<p role="alert" className="mt-2 text-caption leading-4 text-error">
							{t("artifacts.share.secrets", { kinds: inspection.secretFindings.join(", ") })}
						</p>
					) : null}
					{actions(<Button onClick={() => void publish()} size="sm" type="button" variant="primary">{t("artifacts.share.publish")}</Button>)}
				</>
			);
		}
		case "uploading": {
			const percent = phase.total > 0 ? Math.round((phase.sent / phase.total) * 100) : 0;
			return (
				<>
					{title}
					{note(t("artifacts.share.uploading", { percent }))}
					<div className="mt-2 h-1 overflow-hidden rounded-full bg-muted" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent}>
						<div className="h-full bg-accent transition-[width]" style={{ width: `${percent}%` }} />
					</div>
					{actions(<Button onClick={() => void aoBridge.cloudShare.cancel()} size="sm" type="button" variant="ghost">{t("artifacts.share.cancelUpload")}</Button>)}
				</>
			);
		}
		case "published": {
			const { share } = phase;
			const link = share.viewerUrl ?? share.contentUrl;
			if (phase.unpublishing === "confirm" || phase.unpublishing === "busy") {
				const busy = phase.unpublishing === "busy";
				return (
					<>
						<p className="text-control font-medium text-foreground">{t("artifacts.share.unpublishTitle")}</p>
						{note(t("artifacts.share.unpublishDescription"))}
						{actions(
							<>
								<Button disabled={busy} onClick={() => setPhase({ kind: "published", share })} size="sm" type="button" variant="ghost">
									{t("artifacts.share.cancel")}
								</Button>
								<Button className="bg-danger-strong hover:bg-danger-strong" disabled={busy} onClick={() => void unpublish(share)} size="sm" type="button" variant="primary">
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
					{note(t("artifacts.share.public"))}
					<div className="mt-2 flex items-center gap-1 rounded-md bg-muted py-1 pr-1 pl-2">
						<input
							aria-label={t("artifacts.share.linkLabel")}
							className="min-w-0 flex-1 truncate bg-transparent font-mono text-caption text-foreground outline-none"
							onFocus={(event) => event.currentTarget.select()}
							readOnly
							value={link}
						/>
						<Tooltip>
							<TooltipTrigger asChild>
								<Button
									aria-label={t(copied ? "artifacts.share.copied" : "artifacts.share.copyLink")}
									onClick={() => void aoBridge.clipboard.writeText(link).then(() => setCopied(true))}
									size="icon-sm"
									type="button"
									variant="ghost"
								>
									{copied ? <Check aria-hidden="true" className="size-icon-sm" /> : <Copy aria-hidden="true" className="size-icon-sm" />}
								</Button>
							</TooltipTrigger>
							<TooltipContent side="bottom">{t(copied ? "artifacts.share.copied" : "artifacts.share.copyLink")}</TooltipContent>
						</Tooltip>
					</div>
					{phase.error ? <p role="alert" className="mt-2 text-caption text-error">{phase.error}</p> : null}
					<div className="mt-3 flex items-center justify-between gap-2">
						<Button className="text-destructive hover:text-destructive" onClick={() => setPhase({ kind: "published", share, unpublishing: "confirm" })} size="sm" type="button" variant="ghost">
							{t("artifacts.share.unpublish")}
						</Button>
						<Button onClick={() => void publish(share.id)} size="sm" type="button" variant="secondary">
							{t("artifacts.share.update")}
						</Button>
					</div>
				</>
			);
		}
		case "error":
			return (
				<>
					{title}
					<p role="alert" className="mt-1 text-caption text-error">{t("artifacts.share.failed", { message: phase.message })}</p>
					{actions(<Button onClick={() => load()} size="sm" type="button" variant="secondary">{t("artifacts.share.retry")}</Button>)}
				</>
			);
	}
}
