import { useEffect, useState } from "react";
import { X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useCloudCp } from "../hooks/useCloudCp";
import { aoBridge } from "../lib/bridge";
import type { CloudCpSessionShareDeepLink } from "../lib/cloud-cp";
import {
	centeredOnboardingDialogClass,
	onboardingFieldErrorClass,
	onboardingFieldHintClass,
	onboardingFooterActionsEndClass,
	onboardingFormLabelClass,
} from "../lib/onboarding-ui";
import { cn } from "../lib/utils";
import { useShareDialogStore } from "../stores/share-dialog-store";
import { StyledQRCode } from "./settings/StyledQRCode";
import { Button } from "./ui/button";
import { Checkbox } from "./ui/checkbox";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from "./ui/dialog";
import { Input } from "./ui/input";
import { Label } from "./ui/label";

// Owner half of session sharing. Mints a single-use, short-lived
// ao-app://share link bound to one recipient email and shows it as text + QR.
// A share is fully interactive unless the owner ticks "Read-only". The secret
// is only ever returned once, so closing the dialog discards it.
export function ShareSessionDialog() {
	const { t } = useTranslation();
	const { client } = useCloudCp();
	const target = useShareDialogStore((s) => s.target);
	const close = useShareDialogStore((s) => s.closeShareSession);
	const [email, setEmail] = useState("");
	const [readOnly, setReadOnly] = useState(false);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [share, setShare] = useState<CloudCpSessionShareDeepLink | null>(null);
	const [copied, setCopied] = useState(false);

	useEffect(() => {
		if (!target) return;
		setEmail("");
		setReadOnly(false);
		setBusy(false);
		setError(null);
		setShare(null);
		setCopied(false);
	}, [target]);

	const trimmed = email.trim();
	const submit = async () => {
		if (!target || trimmed === "" || busy) return;
		setBusy(true);
		setError(null);
		try {
			const response = await client.createSessionShareDeepLink(
				target.orgId,
				target.sessionId,
				trimmed,
				readOnly ? "view" : "interact",
			);
			setShare(response.share);
		} catch (err) {
			setError(err instanceof Error ? err.message : t("share.createFailed"));
		} finally {
			setBusy(false);
		}
	};
	const copy = async () => {
		if (!share) return;
		await aoBridge.clipboard.writeText(share.deepLink);
		setCopied(true);
	};
	const minutes = share ? Math.max(1, Math.round((Date.parse(share.expiresAt) - Date.now()) / 60_000)) : 0;

	return (
		<Dialog open={target !== null} onOpenChange={(open) => !open && close()}>
			<DialogContent className={centeredOnboardingDialogClass} showCloseButton={false}>
				<DialogClose asChild>
					<button type="button" className="settings-dialog-close-button settings-close-button" aria-label={t("common.close")}>
						<X className="size-icon-base" aria-hidden="true" />
					</button>
				</DialogClose>
				<div className="px-4 pr-12 pt-3">
					<DialogTitle className="text-balance text-[18px] font-semibold text-[var(--color-text-import-title)]">
						{t("share.title")}
					</DialogTitle>
				</div>
				<DialogDescription className="px-4 pr-12 pt-1 text-pretty text-[13px] leading-5 text-muted-foreground">
					{t("share.description", { title: target?.title ?? "" })}
				</DialogDescription>

				<div className="flex min-h-0 flex-col gap-4 overflow-y-auto px-4 pb-1 pt-4">
					{share === null ? (
						<>
						<div className="space-y-2">
							<Label htmlFor="share-recipient-email" className={onboardingFormLabelClass}>
								{t("share.recipientEmail")}
							</Label>
							<Input
								id="share-recipient-email"
								type="email"
								autoComplete="off"
								spellCheck={false}
								className="text-[13px]"
								disabled={busy}
								value={email}
								onChange={(e) => setEmail(e.target.value)}
								onKeyDown={(e) => {
									if (e.key === "Enter") void submit();
								}}
							/>
							<p className={onboardingFieldHintClass}>{t("share.recipientHint")}</p>
						</div>
						<div className="space-y-2">
							<label className="flex items-center gap-2 text-[13px] text-[var(--color-text-import-title)]">
								<Checkbox
									checked={readOnly}
									disabled={busy}
									onCheckedChange={(checked) => setReadOnly(checked === true)}
								/>
								{t("share.readOnlyOption")}
							</label>
							<p className={onboardingFieldHintClass}>
								{readOnly ? t("share.accessViewHint") : t("share.accessInteractHint")}
							</p>
						</div>
						</>
					) : (
						<div className="flex flex-col items-center gap-3">
							<StyledQRCode value={share.deepLink} size={184} />
							<div className="flex w-full items-center gap-2">
								<Input readOnly aria-label={t("share.link")} className="font-mono text-[12px]" value={share.deepLink} onFocus={(e) => e.currentTarget.select()} />
								<Button type="button" variant="outline" onClick={() => void copy()}>
									{copied ? t("share.copied") : t("share.copy")}
								</Button>
							</div>
							<p className={cn(onboardingFieldHintClass, "w-full")}>
								{t(share.role === "editor" ? "share.linkSummaryInteract" : "share.linkSummary", { email: share.recipient, minutes })}
							</p>
						</div>
					)}
					{error ? (
						<p role="alert" className={onboardingFieldErrorClass}>
							{error}
						</p>
					) : null}
				</div>

				<div className={cn(onboardingFooterActionsEndClass, "px-4 pb-4")}>
					{share === null ? (
						<>
							<DialogClose asChild>
								<Button type="button" variant="outline" disabled={busy}>
									{t("share.cancel")}
								</Button>
							</DialogClose>
							<Button type="button" variant="primary" disabled={busy || trimmed === ""} onClick={() => void submit()}>
								{busy ? t("share.creating") : t("share.createLink")}
							</Button>
						</>
					) : (
						<DialogClose asChild>
							<Button type="button" variant="primary">
								{t("share.done")}
							</Button>
						</DialogClose>
					)}
				</div>
			</DialogContent>
		</Dialog>
	);
}
