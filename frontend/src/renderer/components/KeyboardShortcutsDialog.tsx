import {
	APP_SHORTCUTS,
	effectiveShortcutBindings,
	SHORTCUT_CATEGORIES,
	shortcutBindingKeys,
	type AppShortcutId,
	type ShortcutCategory,
	type ShortcutBinding,
} from "../../shared/shortcuts";
import { memo, useRef } from "react";
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
import { ShortcutKeys, shortcutKeysDescription } from "./ShortcutKeys";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

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

function BindingSummary({ bindings, isMac }: { bindings: readonly ShortcutBinding[]; isMac: boolean }) {
	const { t } = useTranslation();
	const [first, ...alternatives] = bindings;
	if (!first) return <span className="text-xs text-muted-foreground">{t("shortcut.unassigned")}</span>;
	return <span className="inline-flex shrink-0 items-center gap-1.5">
		<ShortcutKeys keys={shortcutBindingKeys(first, isMac)} />
		{alternatives.length > 0 ? <Tooltip>
			<TooltipTrigger asChild>
				<span role="img" tabIndex={0} aria-label={t("shortcut.moreBindings", { count: alternatives.length })} className="inline-flex h-6 min-w-6 items-center justify-center rounded-full border border-(--color-border-settings-input) bg-(--color-bg-settings-input) px-1.5 text-xs font-medium tabular-nums text-settings-label outline-none focus-visible:ring-2 focus-visible:ring-ring">
					<span aria-hidden="true">+{alternatives.length}</span>
				</span>
			</TooltipTrigger>
			<TooltipContent className="max-w-xs">
				<p>{t("shortcut.moreBindings", { count: alternatives.length })}</p>
				{alternatives.map((binding, index) => <p className="mt-1 font-mono" key={`${binding.key}-${index}`}>{shortcutKeysDescription(shortcutBindingKeys(binding, isMac))}</p>)}
			</TooltipContent>
		</Tooltip> : null}
	</span>;
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
	return (
		<li className="flex min-h-8 flex-wrap items-center justify-between gap-x-2 gap-y-1 py-0.5">
			<span className="min-w-0 flex-1 basis-28 text-control leading-5 text-foreground">{label}</span>
			<span className="flex min-w-0 max-w-full flex-wrap items-center justify-end gap-x-2 gap-y-1">{children}</span>
		</li>
	);
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
	return (
		<section className="mb-4 break-inside-avoid">
			<h2 className="mb-1 text-caption font-medium text-passive">{title}</h2>
			<ul>{children}</ul>
		</section>
	);
}

export const KeyboardShortcutsDialog = memo(function KeyboardShortcutsDialog({
	open,
	onOpenChange,
	onCustomize,
	isMac = isMacPlatform(),
}: KeyboardShortcutsDialogProps) {
	const { t } = useTranslation();
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const customizingRef = useRef(false);
	const isCommandPaletteEnabled = useCommandPaletteEnabled();
	const overrides = useKeybindingsStore((state) => state.overrides);
	const paletteShortcuts: readonly AppShortcutId[] = ["command-palette", "quick-open-file"];
	// The dialog's own chord sits in the header, so it is not repeated as a row.
	const availableShortcuts = APP_SHORTCUTS.filter(
		(shortcut) =>
			shortcut.id !== "keyboard-shortcuts" && (!paletteShortcuts.includes(shortcut.id) || isCommandPaletteEnabled),
	);
	const helpBindings = effectiveShortcutBindings("keyboard-shortcuts", isMac, overrides);

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent
				showCloseButton={false}
				className={cn(settingsDialogContentClass, "w-[min(1040px,calc(100vw-32px))] max-h-[calc(100svh-32px)]")}
				onCloseAutoFocus={(event) => {
					if (!customizingRef.current) { restoreFocusOnClose(event); return; }
					customizingRef.current = false;
					event.preventDefault();
					// The settings page has replaced the prior surface by the time
					// the help dialog finishes closing; focus its command search.
					requestAnimationFrame(() => document.querySelector<HTMLInputElement>('[data-section="keyboard-shortcuts"] input[type="search"]')?.focus());
				}}
			>
				<div className={cn(settingsDialogHeaderClass, "flex-row flex-wrap items-center gap-3 px-4 py-2.5")}>
					<DialogTitle className="settings-dialog-title">{t("shortcut.dialogTitle")}</DialogTitle>
					{helpBindings.length > 0 ? <BindingSummary bindings={helpBindings} isMac={isMac} /> : null}
					{onCustomize ? (
						<Button className="ml-auto h-8" type="button" variant="ghost" size="sm" onClick={() => { customizingRef.current = true; onCustomize(); }}>
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

				<div role="region" aria-label={t("shortcut.dialogTitle")} tabIndex={0} className={cn(settingsDialogBodyClass, "block columns-1 gap-x-6 p-4 sm:columns-2 lg:columns-3")}>
					{SHORTCUT_CATEGORIES.map((category) => {
						const shortcuts = availableShortcuts.filter((shortcut) => shortcut.category === category);
						if (shortcuts.length === 0) return null;
						return (
							<Section key={category} title={shortcutCategoryLabel(category, t)}>
								{shortcuts.map((shortcut) => (
									<Row key={shortcut.id} label={shortcutLabel(shortcut.id, t)}>
										<BindingSummary bindings={effectiveShortcutBindings(shortcut.id, isMac, overrides)} isMac={isMac} />
									</Row>
								))}
							</Section>
						);
					})}
					<Section title={t("shortcut.category.context")}>
						{contextualShortcuts(isMac).map((shortcut) => (
							<Row key={shortcut.id} label={t(shortcut.label)}>
								<ShortcutKeys keys={shortcut.keys} />
							</Row>
						))}
					</Section>
				</div>
			</DialogContent>
		</Dialog>
	);
});
