import { useMutation } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { Check, Loader2 } from "@/components/app-icons";
import { connectorsApi, type Connector } from "../../hooks/useConnectors";
import { aoBridge } from "../../lib/bridge";
import { Button } from "../ui/button";
import { CloseButton } from "../ui/close-button";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogHeaderClass,
} from "../ui/dialog";
import { Input } from "../ui/input";
import { Label } from "../ui/label";

/** Opens the sign-in page. A client that cannot open a browser still shows the link to copy. */
function openPage(url: string) {
	void Promise.resolve().then(() => aoBridge.app.openExternal(url)).catch(() => undefined);
}

/**
 * Signs a connector in. The daemon gives a page; the user approves there. The
 * browser then opens an address on the computer that runs the daemon. When the
 * browser is on another device that address cannot load, so the user pastes it
 * here and the daemon reads the answer from it.
 */
export function ConnectorSignInDialog({ connector, onClose }: { connector: Connector; onClose: () => void }) {
	const { t } = useTranslation();
	const [address, setAddress] = useState("");
	const [copied, setCopied] = useState(false);
	const started = useRef(false);

	const start = useMutation({
		mutationFn: () => connectorsApi.startSignIn(connector.id),
		onSuccess: (result) => {
			if (result.authorizationUrl) openPage(result.authorizationUrl);
		},
	});
	const finish = useMutation({ mutationFn: () => connectorsApi.finishSignIn(connector.id, address.trim()) });
	const startSignIn = start.mutate;
	useEffect(() => {
		if (started.current) return;
		started.current = true;
		startSignIn();
	}, [startSignIn]);

	const page = start.data?.authorizationUrl;
	const done = finish.isSuccess || connector.signedIn || (start.isSuccess && !page);
	const error = start.error ?? finish.error;

	return (
		<Dialog open onOpenChange={(open) => { if (!open) onClose(); }}>
			<DialogContent showCloseButton={false} className={cn(settingsDialogContentClass, "w-[min(480px,calc(100vw-24px))]")} data-connector-sign-in aria-describedby={undefined}>
				<DialogClose asChild>
					<CloseButton floating aria-label={t("confirm.close")} />
				</DialogClose>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 pb-0 pr-12")}>
					<DialogTitle className="settings-dialog-title text-base">{t("settings.connectors.signInDialog.title", { name: connector.name })}</DialogTitle>
				</div>
				<div className={cn(settingsDialogBodyClass, "gap-3")}>
					{done ? (
						<>
							<p className="flex items-center gap-2 text-sm text-settings-label" role="status">
								<Check className="size-4 text-success" aria-hidden="true" />
								{t(start.isSuccess && !page && !connector.signedIn ? "settings.connectors.signInDialog.notNeeded" : "settings.connectors.signInDialog.done")}
							</p>
							<Button type="button" variant="primary" className="self-end" onClick={onClose}>{t("settings.connectors.signInDialog.close")}</Button>
						</>
					) : start.isPending || !page ? (
						start.isPending ? (
							<p className="flex items-center gap-2 text-sm text-settings-muted" role="status">
								<Loader2 className="size-4 animate-spin" aria-hidden="true" />
								{t("settings.connectors.signInDialog.starting")}
							</p>
						) : null
					) : (
						<>
							<p className="flex items-center gap-2 text-sm text-settings-label" role="status">
								<Loader2 className="size-4 animate-spin text-settings-muted" aria-hidden="true" />
								{t("settings.connectors.signInDialog.waiting")}
							</p>
							<div className="flex flex-wrap gap-2">
								<Button type="button" variant="secondary" size="sm" onClick={() => openPage(page)}>
									{t("settings.connectors.signInDialog.openAgain")}
								</Button>
								<Button
									type="button"
									variant="ghost"
									size="sm"
									onClick={() => void navigator.clipboard.writeText(page).then(() => setCopied(true))}
								>
									{t(copied ? "settings.connectors.signInDialog.copied" : "settings.connectors.signInDialog.copyLink")}
								</Button>
							</div>
							<form
								className="flex flex-col gap-1.5"
								onSubmit={(event) => {
									event.preventDefault();
									if (address.trim()) finish.mutate();
								}}
							>
								<Label htmlFor="connector-callback-address" className="text-control font-medium text-settings-label">
									{t("settings.connectors.signInDialog.pasteLabel")}
								</Label>
								<div className="flex gap-2">
									<Input
										id="connector-callback-address"
										className="min-w-0 flex-1 font-mono text-xs"
										value={address}
										placeholder={start.data?.callbackUrl}
										onChange={(event) => setAddress(event.target.value)}
									/>
									<Button type="submit" variant="secondary" disabled={!address.trim() || finish.isPending}>
										{t("settings.connectors.signInDialog.finish")}
									</Button>
								</div>
							</form>
						</>
					)}
					{error && !done ? <p role="alert" className="text-caption leading-4 text-error">{error.message}</p> : null}
				</div>
			</DialogContent>
		</Dialog>
	);
}
