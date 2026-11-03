import { useRef } from "react";
import { CornerDownLeft } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { useRestoreFocusOnClose } from "../hooks/useRestoreFocusOnClose";
import { Button } from "./ui/button";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogDescription,
	DialogTitle,
	settingsDialogContentClass,
	settingsDialogFooterClass,
} from "./ui/dialog";

// Shown on ⌘Q (macOS). Same frame and tokens as ConfirmDialog, but the footer
// sits under a hairline and each action names its key — Esc cancels, Return
// quits — since the dialog is always opened from the keyboard. Quit starts
// focused so Return confirms. With the menu bar icon up, Quit only closes the
// window, so the body says AO keeps running instead of asking again.
export function QuitConfirmDialog({
	open,
	keepsRunning = false,
	onResolve,
}: {
	open: boolean;
	keepsRunning?: boolean;
	onResolve: (confirmed: boolean) => void;
}) {
	const { t } = useTranslation();
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const quitRef = useRef<HTMLButtonElement>(null);
	const actionClass = "h-8 gap-2 rounded-[10px] px-3 text-sm";

	return (
		<Dialog open={open} onOpenChange={(next) => { if (!next) onResolve(false); }}>
			<DialogContent
				showCloseButton={false}
				className={cn(settingsDialogContentClass, "w-[min(440px,calc(100vw-24px))]")}
				onOpenAutoFocus={(event) => {
					event.preventDefault();
					quitRef.current?.focus();
				}}
				onCloseAutoFocus={restoreFocusOnClose}
			>
				<div className="flex flex-col gap-1 p-5">
					<DialogTitle className="settings-dialog-title text-base">{t("quitConfirm.title")}</DialogTitle>
					<DialogDescription className="text-control leading-5 text-settings-muted">
						{t(keepsRunning ? "quitConfirm.bodyKeepsRunning" : "quitConfirm.body")}
					</DialogDescription>
				</div>

				<div className={cn(settingsDialogFooterClass, "gap-1.5 px-3 py-2.5")}>
					<DialogClose asChild>
						<Button type="button" variant="ghost" aria-keyshortcuts="Escape" className={cn(actionClass, "font-normal")}>
							{t("confirm.cancel")}
							<kbd aria-hidden="true" className="font-sans text-caption text-muted-foreground">Esc</kbd>
						</Button>
					</DialogClose>
					<Button
						ref={quitRef}
						type="button"
						variant="footer-primary"
						aria-keyshortcuts="Enter"
						className={cn(actionClass, "gap-1.5")}
						onClick={() => onResolve(true)}
					>
						{t("quitConfirm.quit")}
						<CornerDownLeft aria-hidden="true" className="size-3.5 opacity-80" />
					</Button>
				</div>
			</DialogContent>
		</Dialog>
	);
}
