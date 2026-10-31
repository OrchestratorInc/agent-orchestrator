import { useRef } from "react";
import { X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { useRestoreFocusOnClose } from "../hooks/useRestoreFocusOnClose";
import { Button } from "./ui/button";
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
	busy?: boolean;
	confirmDisabled?: boolean;
	error?: string | null;
	onConfirm: () => void;
	onOpenChange: (open: boolean) => void;
};

// Shared confirmation modal styled exactly like the settings dialogs — same
// frame, header typography, and footer buttons via the shared
// settingsDialog* class constants. Destructive confirms fill
// with the deep danger-strong token instead of the settings accent.
export function ConfirmDialog({
	open,
	title,
	description,
	confirmLabel,
	confirmAriaLabel,
	cancelLabel,
	destructive,
	children,
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

	// Sized for a two-line prompt, not a settings form: the shared settings
	// frame (575px, 38px footer pills) reads oversized around one question, so
	// the confirm narrows the dialog and compacts the buttons while keeping the
	// settings family's colors, borders, and typography.
	const compactButtonClass = "h-8 rounded-[10px] px-4 text-sm";
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
					// Destructive confirms start on the safe choice, not the close X.
					const target = destructive ? cancelRef.current : confirmRef.current;
					if (!target) return;
					event.preventDefault();
					target.focus();
				}}
				onCloseAutoFocus={restoreFocusOnClose}
				overlay={<DialogOverlay onClick={(event) => event.stopPropagation()} onContextMenu={(event) => event.stopPropagation()} />}
			>
				<DialogClose asChild>
					<button
						type="button"
						disabled={busy}
						tabIndex={-1}
						className="settings-dialog-close-button settings-close-button"
						aria-label={t("confirm.close")}
						title={t("confirm.closeEsc")}
					>
						<X className="size-4" aria-hidden="true" />
					</button>
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
						<Button ref={cancelRef} type="button" variant="footer" className={compactButtonClass} disabled={busy}>
							{cancelLabel ?? t("confirm.cancel")}
						</Button>
					</DialogClose>
					<Button
						ref={confirmRef}
						aria-label={confirmAriaLabel}
						type="button"
						variant="footer-primary"
						className={cn(compactButtonClass, destructive && "bg-danger-strong hover:bg-danger-strong")}
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
