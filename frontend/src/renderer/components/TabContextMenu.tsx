import { Archive, Columns2, Pencil, Rows2, X } from "lucide-react";
import { createContext, useContext, type KeyboardEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { AppShortcutId } from "../../shared/shortcuts";
import { runAppShortcut } from "../hooks/useAppShortcut";
import { isMacPlatform } from "../lib/platform";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuShortcut, ContextMenuTrigger } from "./ui/context-menu";
import { useShortcutKeys } from "./ui/tooltip";

/**
 * Split actions for one tab; an absent action shows disabled. Another tab
 * splits beside the one on screen; the tab on screen splits off a new
 * terminal (`newTerminal`), as ⌘D and ⌘⇧D do.
 */
export type TabSplitActions = { right?: () => void; down?: () => void; newTerminal?: boolean };

/**
 * Provided only while split panes are on (a Developer Mode preview); tab menus
 * then offer Split right and Split down for their tab key.
 */
export const TabSplitMenuContext = createContext<((tabKey: string) => TabSplitActions) | undefined>(undefined);

/** A key hint for an app shortcut, honouring custom keybindings. */
export function TabMenuShortcut({ id }: { id: AppShortcutId }) {
	const keys = useShortcutKeys(id);
	return keys ? <ContextMenuShortcut aria-hidden="true">{keys.join(isMacPlatform() ? "" : "+")}</ContextMenuShortcut> : null;
}

/** Rename for a tab that edits its title in place (F2 on the focused tab). */
export function TabRenameItem({ onRename, ariaLabel }: { onRename?: () => void; ariaLabel?: string }) {
	const { t } = useTranslation();
	return (
		<ContextMenuItem aria-label={ariaLabel} disabled={!onRename} onSelect={onRename}>
			<Pencil aria-hidden="true" />
			{t("shell.rename")}
			<ContextMenuShortcut aria-hidden="true">F2</ContextMenuShortcut>
		</ContextMenuItem>
	);
}

function isMenuKey(event: KeyboardEvent) {
	return event.key === "ContextMenu" || (event.key === "F10" && event.shiftKey);
}

/**
 * Right-click menu for one tab of a session tab strip: the tab's own actions,
 * then Close tab; `destructive` comes last behind one separator. Close tab
 * shows disabled on tabs that cannot close. Shift+F10 and the context menu key
 * open it from a focused tab.
 */
export function TabContextMenu({
	children,
	tabKey,
	items,
	destructive,
	onClose,
	closeShortcut = false,
	disabled = false,
}: {
	children: ReactNode;
	/** The tab's key in the strip, for split actions. */
	tabKey?: string;
	items?: ReactNode;
	destructive?: ReactNode;
	/** The tab's own close action; absent, Close tab shows disabled. */
	onClose?: () => void;
	/** The active tab's close chord applies to this tab. */
	closeShortcut?: boolean;
	disabled?: boolean;
}) {
	if (disabled) return children;
	return (
		<ContextMenu>
			<ContextMenuTrigger asChild>
				<span
					className="contents"
					onKeyDown={(event) => {
						if (!isMenuKey(event)) return;
						// Radix opens on the contextmenu event; anchor it under the tab.
						event.preventDefault();
						const target = event.target as HTMLElement;
						const rect = target.getBoundingClientRect();
						target.dispatchEvent(
							new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: rect.left, clientY: rect.bottom }),
						);
					}}
				>
					{children}
				</span>
			</ContextMenuTrigger>
			<ContextMenuContent className="min-w-56">
				<TabMenuBody closeShortcut={closeShortcut} destructive={destructive} items={items} onClose={onClose} tabKey={tabKey} />
			</ContextMenuContent>
		</ContextMenu>
	);
}

/**
 * The rows of a tab's menu: its own actions, Split (while split panes are on),
 * Close tab, then `destructive` behind one separator. A split group's tab
 * shows these for its focused member below Ungroup.
 */
export function TabMenuBody({
	tabKey,
	items,
	destructive,
	onClose,
	closeShortcut = false,
}: {
	tabKey?: string;
	items?: ReactNode;
	destructive?: ReactNode;
	onClose?: () => void;
	closeShortcut?: boolean;
}) {
	const { t } = useTranslation();
	const split = useContext(TabSplitMenuContext);
	return (
		<>
			{items}
			{items ? <ContextMenuSeparator /> : null}
			{split && tabKey ? <TabSplitItems actions={split(tabKey)} /> : null}
			<ContextMenuItem disabled={!onClose} onSelect={onClose}>
				<X aria-hidden="true" />
				{t("tabMenu.close")}
				{closeShortcut ? <TabMenuShortcut id="close-shell-terminal" /> : null}
			</ContextMenuItem>
			{destructive ? (
				<>
					<ContextMenuSeparator />
					{destructive}
				</>
			) : null}
		</>
	);
}

/** Archive the session on screen, through the same confirmation as ⇧⌘A. */
export function ArchiveSessionMenuItem() {
	const { t } = useTranslation();
	return (
		<ContextMenuItem
			className="text-destructive focus:text-destructive [&_svg]:text-destructive focus:[&_svg]:text-destructive"
			// After the menu closes, like the command palette, so the
			// confirmation dialog takes focus from the session.
			onSelect={() => window.setTimeout(() => runAppShortcut({ id: "archive-session" }), 0)}
		>
			<Archive aria-hidden="true" />
			{t("shell.archiveSession")}
			<TabMenuShortcut id="archive-session" />
		</ContextMenuItem>
	);
}

function TabSplitItems({ actions }: { actions: TabSplitActions }) {
	const { t } = useTranslation();
	return (
		<>
			<ContextMenuItem disabled={!actions.right} onSelect={actions.right}>
				<Columns2 aria-hidden="true" />
				{t("tabMenu.splitRight")}
				{actions.newTerminal ? <TabMenuShortcut id="split-shell-terminal" /> : null}
			</ContextMenuItem>
			<ContextMenuItem disabled={!actions.down} onSelect={actions.down}>
				<Rows2 aria-hidden="true" />
				{t("tabMenu.splitDown")}
				{actions.newTerminal ? <TabMenuShortcut id="split-shell-terminal-down" /> : null}
			</ContextMenuItem>
			<ContextMenuSeparator />
		</>
	);
}
