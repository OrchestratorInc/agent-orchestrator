import {
	APP_SHORTCUTS,
	effectiveShortcutBindings,
	SHORTCUT_CATEGORIES,
	shortcutBindingKeys,
	type AppShortcutId,
	type ShortcutCategory,
} from "../../shared/shortcuts";
import { Fragment } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { shortcutCategoryLabelKeys, shortcutLabelKeys } from "../i18n/key-maps";
import type { MessageKey } from "../i18n/messages";
import { useCommandPaletteEnabled } from "../hooks/useCommandPaletteEnabled";
import { useRestoreFocusOnClose } from "../hooks/useRestoreFocusOnClose";
import { useKeybindingsStore } from "../stores/keybindings-store";
import { X } from "lucide-react";
import { cn } from "@/lib/utils";
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
import { Button } from "./ui/button";

function shortcutLabel(id: AppShortcutId, t: TFunction): string {
	return t(shortcutLabelKeys[id]);
}

function shortcutCategoryLabel(category: ShortcutCategory, t: TFunction): string {
	return t(shortcutCategoryLabelKeys[category]);
}

// Fixed, context-sensitive keys that belong to a surface rather than the app:
// the chat composer, terminal, file editor and Browser panel handle them
// themselves, so they are listed here for reference and are not customizable.
function contextualShortcuts(isMac: boolean): readonly { id: string; label: MessageKey; keys: readonly string[] }[] {
	const primary = isMac ? "⌘" : "Ctrl";
	return [
		{ id: "send", label: "shortcut.context.send", keys: ["Enter"] },
		{ id: "steer", label: "shortcut.context.steer", keys: [primary, "Enter"] },
		{ id: "stop", label: "shortcut.context.stop", keys: ["Esc"] },
		{ id: "find", label: "shortcut.context.find", keys: [primary, "F"] },
		{ id: "text-size", label: "shortcut.context.textSize", keys: [primary, "+ / −"] },
		{ id: "save", label: "shortcut.context.save", keys: [primary, "S"] },
		{ id: "address-bar", label: "shortcut.context.addressBar", keys: [primary, "L"] },
		{ id: "reload", label: "shortcut.context.reload", keys: [primary, "R"] },
	];
}

type KeyboardShortcutsDialogProps = {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	onCustomize?: () => void;
	isMac?: boolean;
};

function isMacPlatform(): boolean {
	if (typeof navigator === "undefined") return false;
	const platform =
		(navigator as Navigator & { userAgentData?: { platform?: string } }).userAgentData?.platform ?? navigator.platform;
	return platform.toLowerCase().includes("mac");
}

// Screen readers skip or mangle the modifier glyphs, so spell them out.
const spokenKeys: Record<string, string> = { "⌘": "Command", "⌥": "Option", "⌃": "Control", "⇧": "Shift" };

function Keys({ keys }: { keys: readonly string[] }) {
	return (
		<span className="inline-flex items-center gap-1" aria-label={keys.map((key) => spokenKeys[key] ?? key).join("+")}>
			{keys.map((key, index) => (
				<kbd
					className="inline-flex h-6 min-w-6 items-center justify-center rounded-sm bg-muted px-1.5 font-mono text-caption font-medium text-muted-foreground"
					// Keys can repeat (⌘ + ⌘ never, but "1-9" labels may), so index disambiguates.
					key={`${key}-${index}`}
				>
					{key}
				</kbd>
			))}
		</span>
	);
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
	return (
		<li className="flex min-h-8 items-center justify-between gap-3 py-0.5">
			<span className="min-w-0 text-control text-foreground">{label}</span>
			<span className="flex shrink-0 flex-wrap items-center justify-end gap-x-2 gap-y-1">{children}</span>
		</li>
	);
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
	return (
		<section className="mb-5 break-inside-avoid">
			<h2 className="mb-1 text-caption font-medium text-passive">{title}</h2>
			<ul>{children}</ul>
		</section>
	);
}

export function KeyboardShortcutsDialog({
	open,
	onOpenChange,
	onCustomize,
	isMac = isMacPlatform(),
}: KeyboardShortcutsDialogProps) {
	const { t } = useTranslation();
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const isCommandPaletteEnabled = useCommandPaletteEnabled();
	const overrides = useKeybindingsStore((state) => state.overrides);
	const paletteShortcuts: readonly AppShortcutId[] = ["command-palette", "quick-open-file"];
	// The dialog's own chord sits in the header, so it is not repeated as a row.
	const availableShortcuts = APP_SHORTCUTS.filter(
		(shortcut) =>
			shortcut.id !== "keyboard-shortcuts" && (!paletteShortcuts.includes(shortcut.id) || isCommandPaletteEnabled),
	);
	const helpBinding = effectiveShortcutBindings("keyboard-shortcuts", isMac, overrides)[0];

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent
				showCloseButton={false}
				className={cn(settingsDialogContentClass, "w-[min(1040px,calc(100vw-32px))]")}
				onCloseAutoFocus={restoreFocusOnClose}
			>
				<div className={cn(settingsDialogHeaderClass, "flex-row items-center gap-3 px-4 py-2.5")}>
					<DialogTitle className="settings-dialog-title">{t("shortcut.dialogTitle")}</DialogTitle>
					{helpBinding ? <Keys keys={shortcutBindingKeys(helpBinding, isMac)} /> : null}
					{onCustomize ? (
						<Button className="ml-auto h-8"type="button" variant="ghost" size="sm" onClick={onCustomize}>
							{t("shortcut.customize")}
						</Button>
					) : null}
					<DialogClose asChild>
						<button type="button" className="settings-close-button" aria-label={t("common.close")}>
							<X className="size-4" aria-hidden="true" />
						</button>
					</DialogClose>
				</div>
				<DialogDescription className="sr-only">{t("shortcut.dialogDescription")}</DialogDescription>

				<div role="region" aria-label={t("shortcut.dialogTitle")} tabIndex={0} className={cn(settingsDialogBodyClass, "block columns-1 gap-x-10 sm:columns-2 lg:columns-3")}>
					{SHORTCUT_CATEGORIES.map((category) => {
						const shortcuts = availableShortcuts.filter((shortcut) => shortcut.category === category);
						if (shortcuts.length === 0) return null;
						return (
							<Section key={category} title={shortcutCategoryLabel(category, t)}>
								{shortcuts.map((shortcut) => (
									<Row key={shortcut.id} label={shortcutLabel(shortcut.id, t)}>
										{effectiveShortcutBindings(shortcut.id, isMac, overrides).map((binding, bindingIndex) => (
											<Fragment key={`${binding.key}-${bindingIndex}`}>
												{/* A divider keeps two alternative chords from reading as one long chord. */}
												{bindingIndex > 0 ? <span aria-hidden="true" className="h-4 w-px bg-border" /> : null}
												<Keys keys={shortcutBindingKeys(binding, isMac)} />
											</Fragment>
										))}
									</Row>
								))}
							</Section>
						);
					})}
					<Section title={t("shortcut.category.context")}>
						{contextualShortcuts(isMac).map((shortcut) => (
							<Row key={shortcut.id} label={t(shortcut.label)}>
								<Keys keys={shortcut.keys} />
							</Row>
						))}
					</Section>
				</div>
			</DialogContent>
		</Dialog>
	);
}
