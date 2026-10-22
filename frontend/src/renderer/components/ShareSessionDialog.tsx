import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Share2, X } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { useCloudCp } from "../hooks/useCloudCp";
import { useRestoreFocusOnClose } from "../hooks/useRestoreFocusOnClose";
import { aoBridge } from "../lib/bridge";
import type { CloudCpShareLink } from "../lib/cloud-cp";
import { shareLinkUrl } from "../lib/share-links";
import { TopbarButton } from "./TopbarButton";
import { Button } from "./ui/button";
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
import { Input } from "./ui/input";
import { Label } from "./ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

type ShareAccess = "viewer" | "editor";
type ShareExpiry = "24" | "168" | "never";
type ShareModeCap = "read-only" | "standard" | "trusted";

const EXPIRY_HOURS: Record<ShareExpiry, number | undefined> = { "24": 24, "168": 168, never: undefined };

export function shareLinksQueryKey(baseUrl: string, orgId: string, projectId: string) {
	return ["cloud-share-links", baseUrl, orgId, projectId] as const;
}

/** The session topbar's Share control for a cloud session the person's organization owns. */
export function ShareSessionButton({ orgId, projectId, sessionId }: { orgId: string; projectId: string; sessionId: string }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	return (
		<>
			<Tooltip>
				<TooltipTrigger asChild>
					<TopbarButton variant="icon" aria-label={t("share.open")} onClick={() => setOpen(true)} data-share-session-button>
						<Share2 className="size-4" aria-hidden="true" />
					</TopbarButton>
				</TooltipTrigger>
				<TooltipContent side="bottom">{t("share.open")}</TooltipContent>
			</Tooltip>
			<ShareSessionDialog open={open} onOpenChange={setOpen} orgId={orgId} projectId={projectId} sessionId={sessionId} />
		</>
	);
}

/**
 * Makes a link that lets other people open this cloud session, and lists the
 * session's live links so they can be revoked. The link is shown once: the
 * control plane keeps only a hash of its token.
 */
export function ShareSessionDialog({ open, onOpenChange, orgId, projectId, sessionId }: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	orgId: string;
	projectId: string;
	sessionId: string;
}) {
	const { t } = useTranslation();
	const { client, baseUrl, ready } = useCloudCp();
	const queryClient = useQueryClient();
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const [access, setAccess] = useState<ShareAccess>("viewer");
	const [modeCap, setModeCap] = useState<ShareModeCap>("standard");
	const [expiry, setExpiry] = useState<ShareExpiry>("168");
	const [created, setCreated] = useState<string>();
	const [copied, setCopied] = useState(false);
	const linksKey = shareLinksQueryKey(baseUrl, orgId, projectId);
	const links = useQuery({
		queryKey: linksKey,
		enabled: open && ready,
		queryFn: async () => (await client.listShareLinks(orgId, projectId)).links,
		select: (all: CloudCpShareLink[]) => all.filter((link) => link.sessionId === sessionId && link.status === "active"),
	});
	const create = useMutation({
		mutationFn: async () => {
			const hours = EXPIRY_HOURS[expiry];
			const { link } = await client.createShareLink(orgId, projectId, {
				sessionId,
				role: access,
				accessScope: "anyone",
				interaction: access === "editor" ? "interact" : "view",
				...(access === "editor" ? { modeCap } : { modeCap: "read-only" as const }),
				...(hours ? { expiresInHours: hours } : {}),
			});
			if (!link.token) throw new Error(t("share.noToken"));
			return shareLinkUrl(orgId, link.token);
		},
		onSuccess: (url) => {
			setCreated(url);
			setCopied(false);
			void queryClient.invalidateQueries({ queryKey: linksKey });
		},
	});
	const revoke = useMutation({
		mutationFn: (linkId: string) => client.revokeShareLink(orgId, projectId, linkId),
		onSettled: () => void queryClient.invalidateQueries({ queryKey: linksKey }),
	});
	const copy = async () => {
		if (!created) return;
		await aoBridge.clipboard.writeText(created);
		setCopied(true);
	};
	const close = (next: boolean) => {
		onOpenChange(next);
		if (!next) {
			setCreated(undefined);
			setCopied(false);
			create.reset();
		}
	};
	const error = create.error ?? revoke.error ?? links.error;

	return (
		<Dialog open={open} onOpenChange={close}>
			<DialogContent
				showCloseButton={false}
				className={cn(settingsDialogContentClass, "w-[min(480px,calc(100vw-24px))]")}
				onClick={(event) => event.stopPropagation()}
				onCloseAutoFocus={restoreFocusOnClose}
				data-share-session-dialog
			>
				<DialogClose asChild>
					<button type="button" className="settings-dialog-close-button settings-close-button" aria-label={t("confirm.close")}>
						<X className="size-4" aria-hidden="true" />
					</button>
				</DialogClose>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 p-5 pr-12")}>
					<DialogTitle className="settings-dialog-title text-base">{t("share.title")}</DialogTitle>
					<DialogDescription className="text-control leading-5 text-settings-muted">{t("share.description")}</DialogDescription>
				</div>
				<div className={cn(settingsDialogBodyClass, "gap-3 px-5 pt-0 pb-5")}>
					<div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] items-center gap-x-3 gap-y-2">
						<Label htmlFor="share-access" className="text-control">{t("share.access")}</Label>
						<Select value={access} onValueChange={(value) => setAccess(value as ShareAccess)}>
							<SelectTrigger id="share-access" className="w-full min-w-0"><SelectValue /></SelectTrigger>
							<SelectContent>
								<SelectItem value="viewer">{t("share.accessViewer")}</SelectItem>
								<SelectItem value="editor">{t("share.accessEditor")}</SelectItem>
							</SelectContent>
						</Select>
						{access === "editor" ? (
							<>
								<Label htmlFor="share-mode" className="text-control">{t("share.modeCap")}</Label>
								<Select value={modeCap} onValueChange={(value) => setModeCap(value as ShareModeCap)}>
									<SelectTrigger id="share-mode" className="w-full min-w-0"><SelectValue /></SelectTrigger>
									<SelectContent>
										<SelectItem value="read-only">{t("share.modeReadOnly")}</SelectItem>
										<SelectItem value="standard">{t("share.modeStandard")}</SelectItem>
										<SelectItem value="trusted">{t("share.modeTrusted")}</SelectItem>
									</SelectContent>
								</Select>
							</>
						) : null}
						<Label htmlFor="share-expiry" className="text-control">{t("share.expiry")}</Label>
						<Select value={expiry} onValueChange={(value) => setExpiry(value as ShareExpiry)}>
							<SelectTrigger id="share-expiry" className="w-full min-w-0"><SelectValue /></SelectTrigger>
							<SelectContent>
								<SelectItem value="24">{t("share.expiryDay")}</SelectItem>
								<SelectItem value="168">{t("share.expiryWeek")}</SelectItem>
								<SelectItem value="never">{t("share.expiryNever")}</SelectItem>
							</SelectContent>
						</Select>
					</div>
					{created ? (
						<div className="flex flex-col gap-1.5">
							<div className="flex items-center gap-2">
								<Input readOnly value={created} aria-label={t("share.link")} className="min-w-0 flex-1 font-mono text-xs" onFocus={(event) => event.currentTarget.select()} data-share-link />
								<Button type="button" variant="secondary" size="sm" onClick={() => void copy()}>
									{copied ? t("share.copied") : t("share.copy")}
								</Button>
							</div>
							<p className="text-xs text-muted-foreground">{t("share.copyOnce")}</p>
						</div>
					) : (
						<Button type="button" variant="primary" className="self-end" disabled={!ready || create.isPending} onClick={() => create.mutate()}>
							{t("share.create")}
						</Button>
					)}
					{error ? <p role="alert" className="text-caption leading-4 text-error">{error instanceof Error ? error.message : String(error)}</p> : null}
					{links.data && links.data.length > 0 ? (
						<div className="flex flex-col">
							<p className="pb-1 text-xs text-muted-foreground">{t("share.activeLinks")}</p>
							<ul className="flex flex-col divide-y divide-border rounded-md bg-muted/40">
								{links.data.map((link) => (
									<li key={link.id} className="flex items-center gap-2 px-3 py-1.5 text-control">
										<span className="min-w-0 flex-1 truncate">
											{link.role === "editor" ? t("share.accessEditor") : t("share.accessViewer")}
											<span className="text-muted-foreground"> · {link.expiresAt
												? t("share.expiresOn", { date: new Date(link.expiresAt).toLocaleDateString() })
												: t("share.expiryNever")}</span>
										</span>
										<Button type="button" variant="ghost" size="sm" disabled={revoke.isPending} onClick={() => revoke.mutate(link.id)}>
											{t("share.revoke")}
										</Button>
									</li>
								))}
							</ul>
						</div>
					) : null}
				</div>
			</DialogContent>
		</Dialog>
	);
}
