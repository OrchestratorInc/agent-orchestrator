import { memo } from "react";
import { X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { isMacPlatform } from "../../lib/platform";
import { useRestoreFocusOnClose } from "../../hooks/useRestoreFocusOnClose";
import {
	Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle,
	settingsDialogBodyClass, settingsDialogContentClass, settingsDialogHeaderClass,
} from "../ui/dialog";
import { KeyboardShortcutsContent } from "./KeyboardShortcutsContent";

export const KeyboardShortcutsSettingsDialog = memo(function KeyboardShortcutsSettingsDialog({
	open,
	onOpenChange,
	isMac = isMacPlatform(),
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	isMac?: boolean;
}) {
	const { t } = useTranslation();
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent
				showCloseButton={false}
				className={cn(settingsDialogContentClass, "h-[min(var(--size-settings-dialog-max-h),calc(100svh-var(--space-8)))] w-[min(960px,calc(100vw-32px))]")}
				onCloseAutoFocus={restoreFocusOnClose}
			>
				<div className={cn(settingsDialogHeaderClass, "flex-row items-center justify-between gap-3 px-6 py-4")}>
					<DialogTitle className="settings-dialog-title">{t("shortcut.dialogTitle")}</DialogTitle>
					<DialogClose asChild>
						<button type="button" className="settings-close-button" aria-label={t("common.close")}><X className="size-4" aria-hidden="true" /></button>
					</DialogClose>
				</div>
				<DialogDescription className="sr-only">{t("shortcut.dialogDescriptionSettings")}</DialogDescription>
				<div className={cn(settingsDialogBodyClass, "shortcuts-customize-body flex-1 overflow-hidden px-6 py-5")}>
					<KeyboardShortcutsContent active={open} titleHidden isMac={isMac} />
				</div>
			</DialogContent>
		</Dialog>
	);
});
