import { useRef } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { useRestoreFocusOnClose } from "../hooks/useRestoreFocusOnClose";
import { Button } from "./ui/button";
import { CloseButton } from "./ui/close-button";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogDescription,
	DialogOverlay,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "./ui/dialog";

type ConfirmDialogProps = {
	open: boolean;
	title: string;
	description: React.ReactNode;
	confirmLabel: string;
	/** Screen-reader name for the confirm button when "Confirm" alone is vague. */
	confirmAriaLabel?: string;
	/** Defaults to "Cancel"; reversible actions can soften it to "No". */
	cancelLabel?: string;
	destructive?: boolean;
	/** Options shown between the description and the buttons. */
	children?: React.ReactNode;
	/** Starts here rather than on a button, for a dialog with a field to fill. */
	initialFocusRef?: React.RefObject<HTMLElement | null>;
	busy?: boolean;
	confirmDisabled?: boolean;
	error?: string | null;
	onConfirm: () => void;
	onOpenChange: (open: boolean) => void;
};

export function ConfirmDialog({
	open,
	title,
	description,
	confirmLabel,
	confirmAriaLabel,
	cancelLabel,
	destructive,
	children,
	initialFocusRef,
	busy,
	confirmDisabled,
	error,
	onConfirm,
	onOpenChange,
}: ConfirmDialogProps) {
	const { t } = useTranslation();
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const cancelRef = useRef<HTMLButtonElement>(null);
	const confirmRef = useRef<HTMLButtonElement>(null);

	return (
		<Dialog open={open} onOpenChange={(next) => { if (busy && !next) return; onOpenChange(next); }}>
			<DialogContent
				showCloseButton={false}
				className={cn(settingsDialogContentClass, "w-[min(420px,calc(100vw-24px))]")}
				// React portals re-dispatch synthetic events up the React tree, not the
				// DOM tree, so a confirm rendered from inside a clickable row or card
				// would otherwise also trigger that ancestor's onClick (opening the very
				// session being confirmed). The modal — dimmed backdrop included — owns
				// its own clicks, right-clicks included. Radix still dismisses on the
				// native pointerdown.
				onClick={(event) => event.stopPropagation()}
				onContextMenu={(event) => event.stopPropagation()}
				onOpenAutoFocus={(event) => {
					// Start on the confirm button, not the close X, so Enter confirms what
					// the person just asked for; Escape cancels. A disabled button can't
					// take focus, which would leave it behind the dialog: then Cancel, or
					// Radix's own choice.
					const preferred = confirmRef.current;
					const target = initialFocusRef?.current ?? (preferred && !preferred.disabled
						? preferred
						: [cancelRef.current, confirmRef.current].find((button) => button && !button.disabled));
					if (!target) return;
					event.preventDefault();
					target.focus();
				}}
				onCloseAutoFocus={restoreFocusOnClose}
				overlay={<DialogOverlay onClick={(event) => event.stopPropagation()} onContextMenu={(event) => event.stopPropagation()} />}
			>
				<DialogClose asChild>
					<CloseButton
						floating
						disabled={busy}
						tabIndex={-1}
						aria-label={t("confirm.close")}
						title={t("confirm.closeEsc")}
					/>
				</DialogClose>

				<div className={cn(settingsDialogHeaderClass, "border-b-0 p-5 pr-12")}>
					<DialogTitle className="settings-dialog-title text-base">{title}</DialogTitle>
					<DialogDescription asChild>
						<div className="text-control leading-5 text-settings-muted">{description}</div>
					</DialogDescription>
				</div>

				{children ? <div className={cn(settingsDialogBodyClass, "px-5 pt-0 pb-3")}>{children}</div> : null}

				{error ? (
					<div className={cn(settingsDialogBodyClass, "px-5 pt-0 pb-3")}>
						<p role="alert" className="text-caption leading-4 text-error">
							{error}
						</p>
					</div>
				) : null}

				<div className={cn(settingsDialogFooterClass, "gap-2 border-t-0 p-4 pt-1")}>
					<DialogClose asChild>
						<Button ref={cancelRef} type="button" variant="secondary" disabled={busy}>
							{cancelLabel ?? t("confirm.cancel")}
						</Button>
					</DialogClose>
					<Button
						ref={confirmRef}
						aria-label={confirmAriaLabel}
						type="button"
						variant={destructive ? "destructive" : "primary"}
						disabled={busy || confirmDisabled}
						onClick={onConfirm}
					>
						{confirmLabel}
					</Button>
				</div>
			</DialogContent>
		</Dialog>
	);
}
