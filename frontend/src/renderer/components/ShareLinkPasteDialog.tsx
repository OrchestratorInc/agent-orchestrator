import { useEffect, useState } from "react";
import { X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { parseShareDeepLink } from "../../shared/share-deeplink";
import {
	centeredOnboardingDialogClass,
	onboardingFieldErrorClass,
	onboardingFooterActionsEndClass,
	onboardingFormLabelClass,
} from "../lib/onboarding-ui";
import { cn } from "../lib/utils";
import { useShareDialogStore } from "../stores/share-dialog-store";
import { Button } from "./ui/button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from "./ui/dialog";
import { Input } from "./ui/input";
import { Label } from "./ui/label";

// A URL shape, not prose: identical in every locale.
const SHARE_LINK_PLACEHOLDER = "ao-app://share/…";

// In-app way to open an ao-app://share link, for when the OS does not route the
// link to this build (e.g. another installed copy owns the scheme). It applies
// the same validation as the OS path and only hands off to the consent dialog;
// nothing is redeemed here.
export function ShareLinkPasteDialog() {
	const { t } = useTranslation();
	const open = useShareDialogStore((s) => s.pasteOpen);
	const setOpen = useShareDialogStore((s) => s.setPasteOpen);
	const setInvite = useShareDialogStore((s) => s.setInvite);
	const [value, setValue] = useState("");
	const [invalid, setInvalid] = useState(false);

	useEffect(() => {
		if (!open) return;
		setValue("");
		setInvalid(false);
	}, [open]);

	const submit = () => {
		const invite = parseShareDeepLink(value.trim());
		if (!invite) {
			setInvalid(true);
			return;
		}
		setInvite(invite);
	};

	return (
		<Dialog open={open} onOpenChange={setOpen}>
			<DialogContent className={centeredOnboardingDialogClass} showCloseButton={false}>
				<DialogClose asChild>
					<button type="button" className="settings-dialog-close-button settings-close-button" aria-label={t("common.close")}>
						<X className="size-icon-base" aria-hidden="true" />
					</button>
				</DialogClose>
				<div className="px-4 pr-12 pt-3">
					<DialogTitle className="text-balance text-[18px] font-semibold text-[var(--color-text-import-title)]">
						{t("share.pasteTitle")}
					</DialogTitle>
				</div>
				<DialogDescription className="px-4 pr-12 pt-1 text-pretty text-[13px] leading-5 text-muted-foreground">
					{t("share.pasteDescription")}
				</DialogDescription>
				<div className="flex flex-col gap-2 px-4 pb-1 pt-4">
					<Label htmlFor="share-link-paste" className={onboardingFormLabelClass}>
						{t("share.link")}
					</Label>
					<Input
						id="share-link-paste"
						autoComplete="off"
						spellCheck={false}
						placeholder={SHARE_LINK_PLACEHOLDER}
						className="font-mono text-[12px]"
						value={value}
						onChange={(e) => {
							setValue(e.target.value);
							setInvalid(false);
						}}
						onKeyDown={(e) => {
							if (e.key === "Enter") submit();
						}}
					/>
					{invalid ? (
						<p role="alert" className={onboardingFieldErrorClass}>
							{t("share.pasteInvalid")}
						</p>
					) : null}
				</div>
				<div className={cn(onboardingFooterActionsEndClass, "px-4 pb-4")}>
					<DialogClose asChild>
						<Button type="button" variant="outline">
							{t("share.cancel")}
						</Button>
					</DialogClose>
					<Button type="button" variant="primary" disabled={value.trim() === ""} onClick={submit}>
						{t("share.pasteOpen")}
					</Button>
				</div>
			</DialogContent>
		</Dialog>
	);
}
