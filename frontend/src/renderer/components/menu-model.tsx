import { createContext, Fragment, useContext, type ComponentType, type ReactNode } from "react";
import type { AppShortcutId } from "../../shared/shortcuts";
import { isMacPlatform } from "../lib/platform";
import { cn } from "../lib/utils";
import { actionMenuDestructiveItemClass } from "./ui/menu-styles";
import {
	ContextMenuItem,
	ContextMenuSeparator,
	ContextMenuShortcut,
	ContextMenuSub,
	ContextMenuSubContent,
	ContextMenuSubTrigger,
} from "./ui/context-menu";
import {
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuShortcut,
	DropdownMenuSub,
	DropdownMenuSubContent,
	DropdownMenuSubTrigger,
} from "./ui/dropdown-menu";
import { useShortcutKeys } from "./ui/tooltip";

/**
 * One row of an action menu, independent of the menu that shows it. A menu's
 * rows are built once as entries and rendered by `MenuEntries` inside either a
 * right-click ContextMenu or a DropdownMenu, so the two cannot drift apart.
 */
export type MenuEntry =
	| {
			kind: "item";
			id: string;
			label: string;
			icon?: ReactNode;
			/** A literal key hint, such as F2. */
			shortcut?: string;
			/** An app shortcut's key hint, honouring custom keybindings. */
			shortcutId?: AppShortcutId;
			/** A muted trailing count in the hint column; give `ariaLabel` the spoken form. */
			count?: number;
			disabled?: boolean;
			/** A muted line under the label, such as why the item is disabled. */
			description?: string;
			destructive?: boolean;
			title?: string;
			ariaLabel?: string;
			onSelect?: () => void;
	  }
	| {
			kind: "submenu";
			id: string;
			label: string;
			icon?: ReactNode;
			items: MenuEntry[];
			onOpenChange?: (open: boolean) => void;
	  }
	| { kind: "separator"; id: string }
	/** A row with its own controls (the terminal agent picker); `label` names it in tests. */
	| { kind: "custom"; id: string; label: string; render: () => ReactNode };

type ItemProps = {
	className?: string;
	disabled?: boolean;
	title?: string;
	"aria-label"?: string;
	onSelect?: (event: Event) => void;
	children?: ReactNode;
};

/** The menu primitives a menu's entries render with. */
export type MenuKit = {
	kind: "context" | "dropdown";
	Item: ComponentType<ItemProps>;
	Separator: ComponentType<{ className?: string }>;
	Shortcut: ComponentType<{ children?: ReactNode; "aria-hidden"?: "true" }>;
	Sub: ComponentType<{ children?: ReactNode; onOpenChange?: (open: boolean) => void }>;
	SubTrigger: ComponentType<{ children?: ReactNode }>;
	SubContent: ComponentType<{ children?: ReactNode; className?: string }>;
};

export const contextMenuKit: MenuKit = {
	kind: "context",
	Item: ContextMenuItem,
	Separator: ContextMenuSeparator,
	Shortcut: ContextMenuShortcut,
	Sub: ContextMenuSub,
	SubTrigger: ContextMenuSubTrigger,
	SubContent: ContextMenuSubContent,
};

export const dropdownMenuKit: MenuKit = {
	kind: "dropdown",
	Item: DropdownMenuItem,
	Separator: DropdownMenuSeparator,
	// The context menu's hint keeps a gap from the label; match it.
	Shortcut: ({ children, ...props }) => <DropdownMenuShortcut className="pl-4" {...props}>{children}</DropdownMenuShortcut>,
	Sub: DropdownMenuSub,
	SubTrigger: DropdownMenuSubTrigger,
	SubContent: DropdownMenuSubContent,
};

/** Right-click menus are the default, for rows mounted inside a bare ContextMenu. */
export const MenuKitContext = createContext<MenuKit>(contextMenuKit);

/** Closes the menu that shows the entries, for a custom row that finishes its own work. */
export const MenuCloseContext = createContext<() => void>(() => {});

export function useMenuKit() {
	return useContext(MenuKitContext);
}

export function useCloseMenu() {
	return useContext(MenuCloseContext);
}

/** Separators only between groups: none leading, trailing or doubled. */
export function tidyMenuEntries(entries: MenuEntry[]): MenuEntry[] {
	const out: MenuEntry[] = [];
	for (const entry of entries) {
		if (entry.kind === "separator" && (out.length === 0 || out[out.length - 1]!.kind === "separator")) continue;
		out.push(entry.kind === "submenu" ? { ...entry, items: tidyMenuEntries(entry.items) } : entry);
	}
	while (out.length && out[out.length - 1]!.kind === "separator") out.pop();
	return out;
}

function AppShortcutHint({ id }: { id: AppShortcutId }) {
	const { Shortcut } = useMenuKit();
	const keys = useShortcutKeys(id);
	return keys ? <Shortcut aria-hidden="true">{keys.join(isMacPlatform() ? "" : "+")}</Shortcut> : null;
}

/** Renders entries with the surrounding menu's primitives. */
export function MenuEntries({ entries }: { entries: MenuEntry[] }) {
	const kit = useMenuKit();
	return (
		<>
			{tidyMenuEntries(entries).map((entry) => {
				switch (entry.kind) {
					case "separator":
						return <kit.Separator key={entry.id} />;
					case "custom":
						return <Fragment key={entry.id}>{entry.render()}</Fragment>;
					case "submenu":
						return (
							<kit.Sub key={entry.id} onOpenChange={entry.onOpenChange}>
								<kit.SubTrigger>
									{entry.icon}
									{entry.label}
								</kit.SubTrigger>
								<kit.SubContent className="min-w-40">
									<MenuEntries entries={entry.items} />
								</kit.SubContent>
							</kit.Sub>
						);
					case "item":
						return (
							<kit.Item
								key={entry.id}
								aria-label={entry.ariaLabel}
								className={cn(entry.destructive && actionMenuDestructiveItemClass)}
								disabled={entry.disabled || !entry.onSelect}
								onSelect={entry.onSelect ? () => entry.onSelect?.() : undefined}
								title={entry.title}
							>
								{entry.icon}
								{entry.description ? (
									<span className="flex min-w-0 flex-col">
										<span>{entry.label}</span>
										<span className="text-xs text-muted-foreground">{entry.description}</span>
									</span>
								) : (
									entry.label
								)}
								{entry.shortcut ? <kit.Shortcut aria-hidden="true">{entry.shortcut}</kit.Shortcut> : null}
								{!entry.shortcut && entry.shortcutId ? <AppShortcutHint id={entry.shortcutId} /> : null}
								{entry.count !== undefined ? <kit.Shortcut aria-hidden="true">{entry.count}</kit.Shortcut> : null}
							</kit.Item>
						);
				}
			})}
		</>
	);
}
