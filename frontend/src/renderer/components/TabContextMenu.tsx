import { MoreVertical, Archive, Columns2, Pencil, Rows2, Ungroup, X } from "@/components/app-icons";
import { createContext, useContext, useState, type KeyboardEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { SessionInterfaceMode } from "../hooks/useSessionInterfaceTransition";
import { runAppShortcut } from "../hooks/useAppShortcut";
import { agentLabel } from "../lib/agent-options";
import { cn } from "../lib/utils";
import type { WorkspaceSession } from "../types/workspace";
import { contextMenuKit, dropdownMenuKit, MenuCloseContext, MenuEntries, MenuKitContext, type MenuEntry } from "./menu-model";
import { useSessionMenuEntries } from "./SessionContextMenuItems";
import { RoundedInterfaceIcon } from "./SessionInterfaceSwitch";
import { useFileTabMenuEntries, type FileTabMenuTarget } from "./SessionFileTabs";
import { TerminalAgentPickerRow } from "./TerminalAgentControls";
import { terminalTabActionClassName } from "./TerminalTabFrame";
import { ContextMenu, ContextMenuContent, ContextMenuTrigger } from "./ui/context-menu";
import { DropdownMenu, DropdownMenuContent, DropdownMenuTrigger } from "./ui/dropdown-menu";
import { Pressable } from "./ui/pressable";

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

/**
 * The agent and interface rows of a session's tab, provided by the session
 * view while they apply: a local terminal's agent picker and the switch to
 * the other interface. Absent while a switch runs.
 */
export type SessionTabAgentMenu = {
	session: WorkspaceSession;
	hostId?: string;
	/** The agent is working, so it cannot be replaced right now. */
	blocked: boolean;
	/** A local terminal picks its agent here; a chat does so from its composer, and Cloud cannot. */
	picksAgent: boolean;
	/** The interface a switch goes to. */
	switchTarget: SessionInterfaceMode;
	switchAction?: { disabled: boolean; reason?: string; run: () => void };
};

export const SessionTabAgentMenuContext = createContext<SessionTabAgentMenu | undefined>(undefined);

/**
 * What one tab of a session tab strip is, for its menu. Every menu of a tab,
 * the ⋮ button and the right-click alike, is built from this by
 * `useTabMenuItems`, so they always list the same rows.
 */
export type TabMenuSpec = {
	/** The tab's key in the strip, for split actions. */
	tabKey?: string;
	/** The tab's close action; absent, Close tab shows disabled. */
	onClose?: () => void;
	/** The active tab's close chord applies to this tab. */
	closeShortcut?: boolean;
} & (
	| { kind: "session"; session: WorkspaceSession; onRename?: () => void; canArchive: boolean }
	/** A tab with no actions of its own: a reviewer, or a shell inside a split group. */
	| { kind: "plain" }
	| { kind: "shell"; title: string; onRename?: () => void }
	| { kind: "file"; file: FileTabMenuTarget }
	/** A split group's tab: Ungroup, then its focused member's own rows. */
	| { kind: "group"; onUngroup: () => void; member?: TabMenuSpec }
);

/**
 * The rows of a tab's menu, in order: the tab's own actions, Split (while
 * split panes are on), Close tab, then Archive for a session behind one
 * separator. A split group's tab leads with Ungroup.
 */
export function useTabMenuItems(spec: TabMenuSpec): MenuEntry[] {
	const { t } = useTranslation();
	const split = useContext(TabSplitMenuContext);
	const agentMenu = useContext(SessionTabAgentMenuContext);
	const own = spec.kind === "group" ? spec.member : spec;
	const session = own?.kind === "session" ? own : undefined;
	const sessionEntries = useSessionMenuEntries(session?.session, {
		onRename: session?.onRename,
		renameShortcut: session?.onRename ? "F2" : undefined,
	});
	const fileEntries = useFileTabMenuEntries(own?.kind === "file" ? own.file : undefined);

	const entries: MenuEntry[] = [];
	if (spec.kind === "group") {
		entries.push(
			{ kind: "item", id: "ungroup", icon: <Ungroup aria-hidden="true" />, label: t("splitPane.ungroup"), onSelect: spec.onUngroup },
			{ kind: "separator", id: "ungroup-sep" },
		);
	}
	if (!own) return entries;

	if (session && agentMenu && agentMenu.session.id === session.session.id) {
		if (agentMenu.picksAgent) {
			entries.push({
				kind: "custom",
				id: "agent-picker",
				label: agentLabel(agentMenu.session.provider),
				render: () => <TerminalAgentPickerRow blocked={agentMenu.blocked} hostId={agentMenu.hostId} session={agentMenu.session} />,
			});
		}
		const switchAction = agentMenu.switchAction;
		if (switchAction) {
			entries.push({
				kind: "item",
				id: "switch-interface",
				disabled: switchAction.disabled,
				icon: <RoundedInterfaceIcon className="size-icon-lg" target={agentMenu.switchTarget} />,
				label: agentMenu.switchTarget === "chat" ? t("agentSwitch.switchToChat") : t("agentSwitch.switchToTerminal"),
				onSelect: switchAction.run,
				title: switchAction.reason,
			});
		}
		entries.push({ kind: "separator", id: "agent-sep" });
	}
	if (session) entries.push(...sessionEntries);
	if (own.kind === "file") entries.push(...fileEntries);
	if (own.kind === "shell") {
		entries.push({
			kind: "item",
			id: "shell-rename",
			ariaLabel: t("terminal.rename", { title: own.title }),
			disabled: !own.onRename,
			icon: <Pencil aria-hidden="true" />,
			label: t("shell.rename"),
			onSelect: own.onRename,
			shortcut: "F2",
		});
	}
	entries.push({ kind: "separator", id: "own-sep" });
	if (split && own.tabKey) {
		const actions = split(own.tabKey);
		entries.push(
			{
				kind: "item",
				id: "split-right",
				icon: <Columns2 aria-hidden="true" />,
				label: t("tabMenu.splitRight"),
				onSelect: actions.right,
				shortcutId: actions.newTerminal ? "split-shell-terminal" : undefined,
			},
			{
				kind: "item",
				id: "split-down",
				icon: <Rows2 aria-hidden="true" />,
				label: t("tabMenu.splitDown"),
				onSelect: actions.down,
				shortcutId: actions.newTerminal ? "split-shell-terminal-down" : undefined,
			},
			{ kind: "separator", id: "split-sep" },
		);
	}
	entries.push({
		kind: "item",
		id: "close-tab",
		icon: <X aria-hidden="true" />,
		label: t("tabMenu.close"),
		onSelect: own.onClose,
		shortcutId: own.closeShortcut ? "close-shell-terminal" : undefined,
	});
	// Only where the ⇧⌘A confirmation is mounted (SessionTopbarActions).
	if (session?.canArchive) {
		entries.push(
			{ kind: "separator", id: "archive-sep" },
			{
				kind: "item",
				id: "archive-session",
				destructive: true,
				icon: <Archive aria-hidden="true" />,
				label: t("shell.archiveSession"),
				// After the menu closes, like the command palette, so the
				// confirmation dialog takes focus from the session.
				onSelect: () => window.setTimeout(() => runAppShortcut({ id: "archive-session" }), 0),
				shortcutId: "archive-session",
			},
		);
	}
	return entries;
}

/** A tab's rows in the surrounding menu. */
function TabMenuItems({ spec }: { spec: TabMenuSpec }) {
	return <MenuEntries entries={useTabMenuItems(spec)} />;
}

function isMenuKey(event: KeyboardEvent) {
	return event.key === "ContextMenu" || (event.key === "F10" && event.shiftKey);
}

/**
 * Right-click menu for one tab of a session tab strip, listing the tab's
 * `useTabMenuItems` rows. Shift+F10 and the context menu key open it from a
 * focused tab.
 */
export function TabContextMenu({ children, spec, disabled = false }: { children: ReactNode; spec: TabMenuSpec; disabled?: boolean }) {
	// Radix gives a context menu no open prop; remounting closes it, for a row
	// that finishes its own work (the agent picker's Switch).
	const [generation, setGeneration] = useState(0);
	if (disabled) return children;
	return (
		<ContextMenu key={generation}>
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
				<MenuKitContext.Provider value={contextMenuKit}>
					<MenuCloseContext.Provider value={() => setGeneration((value) => value + 1)}>
						<TabMenuItems spec={spec} />
					</MenuCloseContext.Provider>
				</MenuKitContext.Provider>
			</ContextMenuContent>
		</ContextMenu>
	);
}

/** The ⋮ button's menu for one tab: the same `useTabMenuItems` rows as its right-click menu. */
export function TabDropdownMenu({ spec, label }: { spec: TabMenuSpec; label: string }) {
	const [open, setOpen] = useState(false);
	return (
		<DropdownMenu open={open} onOpenChange={setOpen}>
			<DropdownMenuTrigger asChild>
				{/* A native title: a Radix tooltip would reopen when the menu hands focus back. */}
				<Pressable
					aria-label={label}
					className={cn(terminalTabActionClassName, "data-[state=open]:text-foreground")}
					data-session-actions-trigger
					title={label}
					type="button"
				>
					<MoreVertical aria-hidden="true" className="size-icon-sm" />
				</Pressable>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="start" className="min-w-56">
				<MenuKitContext.Provider value={dropdownMenuKit}>
					<MenuCloseContext.Provider value={() => setOpen(false)}>
						<TabMenuItems spec={spec} />
					</MenuCloseContext.Provider>
				</MenuKitContext.Provider>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

/** The spec of the tab a control sits in, so its ⋮ button opens that tab's menu. */
export const TabMenuSpecContext = createContext<TabMenuSpec | undefined>(undefined);

/** The session tab's ⋮ button, placed by the session view in the tab's action slot. */
export function SessionTabMenuButton() {
	const { t } = useTranslation();
	const spec = useContext(TabMenuSpecContext);
	return spec ? <TabDropdownMenu label={t("session.actionsMenu")} spec={spec} /> : null;
}
