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

export type TabSplitActions = { right?: () => void; down?: () => void; newTerminal?: boolean };

export const TabSplitMenuContext = createContext<((tabKey: string) => TabSplitActions) | undefined>(undefined);

export type SessionTabAgentMenu = {
	session: WorkspaceSession;
	hostId?: string;
	blocked: boolean;
	picksAgent: boolean;
	switchTarget: SessionInterfaceMode;
	switchAction?: { disabled: boolean; reason?: string; run: () => void };
};

export const SessionTabAgentMenuContext = createContext<SessionTabAgentMenu | undefined>(undefined);

export type TabMenuSpec = {
	tabKey?: string;
	onClose?: () => void;
	closeShortcut?: boolean;
} & (
	| { kind: "session"; session: WorkspaceSession; onRename?: () => void; canArchive: boolean }
	| { kind: "plain" }
	| { kind: "shell"; title: string; onRename?: () => void }
	| { kind: "file"; file: FileTabMenuTarget }
	| { kind: "group"; onUngroup: () => void; member?: TabMenuSpec }
);

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
	if (own.kind !== "session" || own.onClose) {
		entries.push({
			kind: "item",
			id: "close-tab",
			icon: <X aria-hidden="true" />,
			label: t("tabMenu.close"),
			onSelect: own.onClose,
			shortcutId: own.closeShortcut ? "close-shell-terminal" : undefined,
		});
	}
	if (session?.canArchive) {
		entries.push(
			{ kind: "separator", id: "archive-sep" },
			{
				kind: "item",
				id: "archive-session",
				destructive: true,
				icon: <Archive aria-hidden="true" />,
				label: t("shell.archiveSession"),
				onSelect: () => window.setTimeout(() => runAppShortcut({ id: "archive-session" }), 0),
				shortcutId: "archive-session",
			},
		);
	}
	return entries;
}

function TabMenuItems({ spec }: { spec: TabMenuSpec }) {
	return <MenuEntries entries={useTabMenuItems(spec)} />;
}

function isMenuKey(event: KeyboardEvent) {
	return event.key === "ContextMenu" || (event.key === "F10" && event.shiftKey);
}

export function TabContextMenu({ children, spec, disabled = false }: { children: ReactNode; spec: TabMenuSpec; disabled?: boolean }) {
	const [generation, setGeneration] = useState(0);
	if (disabled) return children;
	return (
		<ContextMenu key={generation}>
			<ContextMenuTrigger asChild>
				<span
					className="contents"
					onKeyDown={(event) => {
						if (!isMenuKey(event)) return;
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

export function TabDropdownMenu({ spec, label }: { spec: TabMenuSpec; label: string }) {
	const [open, setOpen] = useState(false);
	return (
		<DropdownMenu open={open} onOpenChange={setOpen}>
			<DropdownMenuTrigger asChild>
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

export const TabMenuSpecContext = createContext<TabMenuSpec | undefined>(undefined);

export function SessionTabMenuButton() {
	const { t } = useTranslation();
	const spec = useContext(TabMenuSpecContext);
	return spec ? <TabDropdownMenu label={t("session.actionsMenu")} spec={spec} /> : null;
}
