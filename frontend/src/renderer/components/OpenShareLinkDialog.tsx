import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { useCloudCp } from "../hooks/useCloudCp";
import { sharedCloudWorkspacesQueryKey } from "../hooks/useWorkspaceQuery";
import { aoBridge } from "../lib/bridge";
import { CloudCpError } from "../lib/cloud-cp/errors";
import { projectNavigateTarget, sessionNavigateTarget } from "../lib/navigate-to-session";
import { parseShareLink } from "../lib/share-links";
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

const OPEN_SHARE_LINK_EVENT = "ao:open-share-link";

/**
 * What went wrong opening a link, in terms the person can act on: a link that
 * no longer works, or one for someone else, rather than the control plane's
 * generic access error.
 */
export function shareLinkErrorMessage(error: unknown, t: (key: "shareLink.expired" | "shareLink.notForYou") => string): string {
	if (error instanceof CloudCpError && error.status === 404) return t("shareLink.expired");
	if (error instanceof CloudCpError && error.status === 403) return t("shareLink.notForYou");
	return error instanceof Error ? error.message : String(error);
}

/** Asks the shell to open a shared session's link: with one, opens it; without, asks for it. */
export function requestOpenShareLink(link?: string): void {
	window.dispatchEvent(new CustomEvent<string | undefined>(OPEN_SHARE_LINK_EVENT, { detail: link }));
}

/**
 * Opens shared cloud sessions: a link opened from outside the app
 * (ao-app://share/...), one pasted here, or the command palette's request.
 * Redeeming a link gives this person their own access to the session, which
 * then appears beside their projects; the session opens at once.
 */
export function ShareLinkHost() {
	const { t } = useTranslation();
	const { client, baseUrl, ready } = useCloudCp();
	const queryClient = useQueryClient();
	const navigate = useNavigate();
	const [open, setOpen] = useState(false);
	const [text, setText] = useState("");
	const autoOpened = useRef<string | undefined>(undefined);

	const redeem = useMutation({
		mutationFn: async (link: string) => {
			const target = parseShareLink(link);
			if (!target) throw new Error(t("shareLink.invalid"));
			const { shared } = await client.redeemShareLink(target.orgId, target.token);
			return shared;
		},
		onSuccess: async (shared) => {
			await queryClient.invalidateQueries({ queryKey: [...sharedCloudWorkspacesQueryKey, baseUrl] });
			setOpen(false);
			setText("");
			void navigate(shared.sessionId
				? sessionNavigateTarget(shared.project.id, shared.sessionId)
				: projectNavigateTarget(shared.project.id));
		},
	});
	const redeemRef = useRef(redeem);
	redeemRef.current = redeem;

	useEffect(() => {
		const show = (link?: string) => {
			redeemRef.current.reset();
			setText(link ?? "");
			setOpen(true);
			// A link from outside the app opens at once; the dialog shows only
			// what happened (or that sign-in is needed first).
			if (link && autoOpened.current !== link) {
				autoOpened.current = link;
				if (ready) redeemRef.current.mutate(link);
			}
		};
		const onRequest = (event: Event) => show((event as CustomEvent<string | undefined>).detail);
		window.addEventListener(OPEN_SHARE_LINK_EVENT, onRequest);
		const stopListening = aoBridge.cloud.onOpenShareLink((url) => show(url));
		void aoBridge.cloud.takePendingShareLink().then((url) => { if (url) show(url); });
		return () => {
			window.removeEventListener(OPEN_SHARE_LINK_EVENT, onRequest);
			stopListening();
		};
	}, [ready]);

	const error = !ready ? t("shareLink.signInFirst") : redeem.error ? shareLinkErrorMessage(redeem.error, t) : undefined;
	return (
		<Dialog open={open} onOpenChange={(next) => { if (!redeem.isPending) setOpen(next); }}>
			<DialogContent
				showCloseButton={false}
				className={cn(settingsDialogContentClass, "w-[min(480px,calc(100vw-24px))]")}
				data-open-share-link-dialog
			>
				<DialogClose asChild>
					<button type="button" className="settings-dialog-close-button settings-close-button" aria-label={t("confirm.close")}>
						<X className="size-4" aria-hidden="true" />
					</button>
				</DialogClose>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 p-5 pr-12")}>
					<DialogTitle className="settings-dialog-title text-base">{t("shareLink.title")}</DialogTitle>
					<DialogDescription className="text-control leading-5 text-settings-muted">{t("shareLink.description")}</DialogDescription>
				</div>
				<form
					className={cn(settingsDialogBodyClass, "gap-3 px-5 pt-0 pb-5")}
					onSubmit={(event) => {
						event.preventDefault();
						if (ready && text.trim()) redeem.mutate(text);
					}}
				>
					<Input
						autoFocus
						value={text}
						onChange={(event) => setText(event.target.value)}
						aria-label={t("shareLink.label")}
						placeholder={t("shareLink.placeholder")}
						className="font-mono text-xs"
						data-share-link-input
					/>
					{error ? <p role="alert" className="text-caption leading-4 text-error">{error}</p> : null}
					<Button type="submit" variant="primary" className="self-end" disabled={!ready || !text.trim() || redeem.isPending}>
						{t("shareLink.open")}
					</Button>
				</form>
			</DialogContent>
		</Dialog>
	);
}
