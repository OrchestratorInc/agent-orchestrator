import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Share } from "lucide-react";
import type { CloudShareFile, CloudShareInspection, CloudShareLink, CloudShareLinks } from "../../shared/cloud-share";
import { useSettings } from "../hooks/useSettings";
import { aoBridge } from "../lib/bridge";
import { useCloudSession } from "../lib/cloud-session";
import { useUiStore } from "../stores/ui-store";
import { ConfirmDialog } from "./ConfirmDialog";
import { Button } from "./ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

type Phase =
	| { kind: "loading" }
	| { kind: "signin" }
	| { kind: "confirm"; inspection: CloudShareInspection; links: CloudShareLinks }
	| { kind: "uploading"; sent: number; total: number }
	| { kind: "done"; share: CloudShareLink }
	| { kind: "limit"; limit: number }
	| { kind: "unsupported" }
	| { kind: "error"; message: string };

/** "2.0 KB", "64 MB": a byte count for the share dialog. */
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

/** Videos are not shared yet: there is no partial-file serving for them. */
const VIDEO_FILE = /\.(mp4|m4v|mov|webm|ogv|avi|mkv)$/i;

/**
 * The Share button beside an open local artifact's other actions: it opens
 * the share dialog. For a video it is disabled and says why.
 */
export function ShareArtifactButton({ file, name, className }: { file: Omit<CloudShareFile, "baseUrl">; name: string; className?: string }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const video = VIDEO_FILE.test(file.path);
	const button = (
		<Button
			aria-label={`${t("artifacts.share.menu")}: ${name}`}
			className={className}
			disabled={video}
			onClick={() => setOpen(true)}
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
	return (
		<>
			{button}
			<ShareArtifactDialog file={file} name={name} open={open} onOpenChange={setOpen} />
		</>
	);
}

/** The control plane URL and whether the person is signed in to share. */
export function useShareAccess(): { baseUrl: string; signedIn: boolean } {
	const { settings } = useSettings();
	const { status } = useCloudSession();
	const baseUrl = settings?.cloudControlPlaneUrl ?? "";
	return { baseUrl, signedIn: status === "authenticated" && baseUrl !== "" };
}

/**
 * Shares one local artifact as a public link. The dialog shows what will be
 * public and what the plan allows; publishing itself runs in the main
 * process, which asks the person to confirm in a native dialog first.
 */
export function ShareArtifactDialog({
	file,
	name,
	open,
	onOpenChange,
}: {
	file: Omit<CloudShareFile, "baseUrl">;
	name: string;
	open: boolean;
	onOpenChange: (open: boolean) => void;
}) {
	const { t } = useTranslation();
	const { baseUrl, signedIn } = useShareAccess();
	const openSettings = useUiStore((state) => state.openGlobalSettings);
	const [phase, setPhase] = useState<Phase>({ kind: "loading" });
	const [copied, setCopied] = useState(false);

	useEffect(() => {
		if (!open) return;
		setCopied(false);
		if (!signedIn) {
			setPhase({ kind: "signin" });
			return;
		}
		let cancelled = false;
		setPhase({ kind: "loading" });
		const shareFile = { ...file, baseUrl };
		Promise.all([aoBridge.cloudShare.inspect(shareFile), aoBridge.cloudShare.list(baseUrl)])
			.then(([inspection, links]) => {
				if (cancelled) return;
				if (inspection.unsupported) return setPhase({ kind: "unsupported" });
				// A file with no active link of its name is most likely a new
				// document; the server decides for sure when it uploads.
				const likelyNew = !file.shareId && !links.shares.some((share) => !share.revokedAt && share.name === inspection.name);
				if (!links.unlimited && likelyNew && links.used >= links.limit) return setPhase({ kind: "limit", limit: links.limit });
				setPhase({ kind: "confirm", inspection, links });
			})
			.catch((error: unknown) => {
				if (!cancelled) setPhase({ kind: "error", message: error instanceof Error ? error.message : String(error) });
			});
		return () => {
			cancelled = true;
		};
	}, [open, signedIn, baseUrl, file.sessionId, file.path, file.projectId, file.shareId]); // eslint-disable-line react-hooks/exhaustive-deps

	useEffect(() => {
		if (phase.kind !== "uploading") return;
		return aoBridge.cloudShare.onProgress((progress) => {
			if (progress.sessionId !== file.sessionId || progress.path !== file.path) return;
			setPhase({ kind: "uploading", sent: progress.sentBytes, total: progress.totalBytes });
		});
	}, [phase.kind, file.sessionId, file.path]);

	const publish = async (links: CloudShareLinks) => {
		setPhase({ kind: "uploading", sent: 0, total: 0 });
		try {
			const result = await aoBridge.cloudShare.publish({ ...file, baseUrl });
			switch (result.status) {
				case "published":
					return setPhase({ kind: "done", share: result.share });
				case "cancelled":
					return onOpenChange(false);
				case "limit":
					return setPhase({ kind: "limit", limit: links.limit });
				case "unsupported":
					return setPhase({ kind: "unsupported" });
				case "signed_out":
					return setPhase({ kind: "signin" });
			}
		} catch (error) {
			setPhase({ kind: "error", message: error instanceof Error ? error.message : String(error) });
		}
	};

	const close = (next: boolean) => {
		if (!next && phase.kind === "uploading") void aoBridge.cloudShare.cancel();
		onOpenChange(next);
	};
	const upgrade = () => {
		onOpenChange(false);
		openSettings("billing");
	};

	const base = { open, onOpenChange: close };
	switch (phase.kind) {
		case "signin":
			return (
				<ConfirmDialog {...base} title={t("artifacts.share.signInTitle")} description={t("artifacts.share.signInDescription")}
					confirmLabel={t("artifacts.share.signIn")} onConfirm={() => void aoBridge.cloud.signIn()} />
			);
		case "limit":
			return (
				<ConfirmDialog {...base} title={t("artifacts.share.upgradeTitle")} description={t("artifacts.share.limitReached", { limit: phase.limit })}
					confirmLabel={t("artifacts.share.upgrade")} onConfirm={upgrade} />
			);
		case "unsupported":
			return (
				<ConfirmDialog {...base} title={t("artifacts.share.title", { name })} description={t("artifacts.share.videoSoon")}
					confirmLabel={t("artifacts.share.publish")} confirmDisabled onConfirm={() => undefined} />
			);
		case "done": {
			const link = phase.share.viewerUrl ?? phase.share.contentUrl;
			return (
				<ConfirmDialog {...base} title={t("artifacts.share.readyTitle")} description={t("artifacts.share.public")}
					confirmLabel={t(copied ? "artifacts.share.copied" : "artifacts.share.copyLink")} cancelLabel={t("artifacts.share.done")}
					onConfirm={() => void aoBridge.clipboard.writeText(link).then(() => setCopied(true))}>
					<p className="truncate font-mono text-caption text-settings-muted" title={link}>{link}</p>
				</ConfirmDialog>
			);
		}
		case "uploading": {
			const percent = phase.total > 0 ? Math.round((phase.sent / phase.total) * 100) : 0;
			return (
				<ConfirmDialog {...base} title={t("artifacts.share.title", { name })} description={t("artifacts.share.uploading", { percent })}
					confirmLabel={t("artifacts.share.publish")} confirmDisabled cancelLabel={t("artifacts.share.cancelUpload")} onConfirm={() => undefined}>
					<div className="h-1 overflow-hidden rounded-full bg-settings-muted/20" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent}>
						<div className="h-full bg-accent transition-[width]" style={{ width: `${percent}%` }} />
					</div>
				</ConfirmDialog>
			);
		}
		case "confirm": {
			const { inspection, links } = phase;
			const usage = links.unlimited || file.shareId ? "" : t("artifacts.share.usage", { used: links.used + 1, limit: links.limit });
			return (
				<ConfirmDialog {...base} title={t(file.shareId ? "artifacts.share.updateTitle" : "artifacts.share.title", { name })}
					description={usage ? `${t("artifacts.share.public")} ${usage}` : t("artifacts.share.public")}
					confirmLabel={t(file.shareId ? "artifacts.share.update" : "artifacts.share.publish")} onConfirm={() => void publish(links)}>
					<p className="text-caption text-settings-muted">{formatBytes(inspection.size)}</p>
					{inspection.secretFindings.length > 0 ? (
						<p role="alert" className="mt-2 text-caption leading-4 text-error">
							{t("artifacts.share.secrets", { kinds: inspection.secretFindings.join(", ") })}
						</p>
					) : null}
				</ConfirmDialog>
			);
		}
		case "error":
			return (
				<ConfirmDialog {...base} title={t("artifacts.share.title", { name })} description={t("artifacts.share.public")}
					error={t("artifacts.share.failed", { message: phase.message })} confirmLabel={t("artifacts.share.publish")} confirmDisabled onConfirm={() => undefined} />
			);
		default:
			return (
				<ConfirmDialog {...base} title={t("artifacts.share.title", { name })} description={t("artifacts.share.checking")}
					confirmLabel={t("artifacts.share.publish")} confirmDisabled onConfirm={() => undefined} />
			);
	}
}
