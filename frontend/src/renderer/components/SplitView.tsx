import {
	Fragment,
	useCallback,
	useEffect,
	useRef,
	useState,
	type CSSProperties,
	type KeyboardEvent as ReactKeyboardEvent,
	type PointerEvent as ReactPointerEvent,
	type ReactNode,
	type RefObject,
} from "react";
import { agentLabel } from "@aoagents/product-ui";
import { GripVertical, X } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import { beginPaneTabDrag } from "../lib/pane-drag";
import { MIN_PANE_PX, ratioVar, type CssDivider } from "../lib/pane-split";
import { clampTerminalFontSize, initialTerminalFontSize, terminalFontSizeStorageKey } from "../lib/terminal-font-size";
import { cn } from "../lib/utils";
import type { Theme } from "../stores/ui-store";
import type { TerminalTarget } from "../types/terminal";
import type { WorkspaceSession } from "../types/workspace";
import { useShellAgentStore, useShellAgentStream } from "../stores/shell-agent-store";
import { ShellAgentIcon, shellAgentStateLabel } from "./ShellTerminalTab";
import { TerminalPane } from "./TerminalPane";
import { Pressable } from "./ui/pressable";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { TabContextMenu, type TabMenuSpec } from "./TabContextMenu";
import { terminalTabActionClassName } from "./TerminalTabFrame";

/**
 * A terminal shown in a split pane. Shell terminals come from the shared
 * cache, so moving one into or around a split re-parents the live xterm
 * instead of remounting it.
 */
export function SplitPaneTerminal({
	session,
	target,
	theme,
	daemonReady,
	createMux,
	focusRequested,
}: {
	session?: WorkspaceSession;
	target: TerminalTarget;
	theme: Theme;
	daemonReady: boolean;
	createMux?: Parameters<typeof TerminalPane>[0]["createMux"];
	focusRequested: boolean;
}) {
	const [fontSize, setFontSize] = useState(initialTerminalFontSize);
	const changeFontSize = useCallback((delta: number) => {
		setFontSize((current) => {
			const next = clampTerminalFontSize(current + delta);
			window.localStorage?.setItem(terminalFontSizeStorageKey, String(next));
			return next;
		});
	}, []);
	return (
		<TerminalPane
			createMux={createMux}
			daemonReady={daemonReady}
			focusRequested={focusRequested}
			fontSize={fontSize}
			onChangeFontSize={changeFontSize}
			session={session}
			terminalTarget={target}
			theme={theme}
		/>
	);
}

const KEYBOARD_STEP = 0.05;

/**
 * The split's own box never depends on its own ratio, so a divider's ratio can
 * be measured from the screen and the layout's fractions alone.
 */
function clampDividerRatio(divider: CssDivider, rect: DOMRect | undefined, ratio: number): number {
	const span = rect ? (divider.dir === "row" ? divider.box.w * rect.width : divider.box.h * rect.height) : 0;
	const min = span > 0 ? Math.min(0.45, MIN_PANE_PX / span) : 0.1;
	return Math.min(1 - min, Math.max(min, ratio));
}

function ratioFromPoint(divider: CssDivider, rect: DOMRect, x: number, y: number): number {
	const ratio =
		divider.dir === "row"
			? (x - rect.left - divider.box.x * rect.width) / (divider.box.w * rect.width)
			: (y - rect.top - divider.box.y * rect.height) / (divider.box.h * rect.height);
	return clampDividerRatio(divider, rect, ratio);
}

/**
 * Where a vertical divider and a horizontal one meet: the horizontal one ends
 * on the vertical one (or the other way round). Dragging that point moves both.
 */
export function findJunctions(dividers: CssDivider[]): { row: CssDivider; column: CssDivider }[] {
	const eps = 1e-6;
	const junctions: { row: CssDivider; column: CssDivider }[] = [];
	for (const row of dividers) {
		if (row.dir !== "row") continue;
		const x = row.box.x + row.box.w * row.ratio;
		for (const column of dividers) {
			if (column.dir !== "column") continue;
			const y = column.box.y + column.box.h * column.ratio;
			const columnEndsOnRow = Math.abs(column.box.x - x) < eps || Math.abs(column.box.x + column.box.w - x) < eps;
			const rowEndsOnColumn = Math.abs(row.box.y - y) < eps || Math.abs(row.box.y + row.box.h - y) < eps;
			const rowReachesY = y > row.box.y + eps && y < row.box.y + row.box.h - eps;
			const columnReachesX = x > column.box.x + eps && x < column.box.x + column.box.w - eps;
			if ((columnEndsOnRow && rowReachesY) || (rowEndsOnColumn && columnReachesX)) junctions.push({ row, column });
		}
	}
	return junctions;
}

/** The point where two dividers meet; dragging it resizes both panes' edges at once. */
export function SplitJunction({
	screenRef,
	row,
	column,
	onResize,
}: {
	screenRef: RefObject<HTMLElement | null>;
	row: CssDivider;
	column: CssDivider;
	onResize: (path: string, ratio: number) => void;
}) {
	const dragRef = useRef<{ frame: number | null; x: number; y: number } | null>(null);
	useEffect(() => () => {
		const drag = dragRef.current;
		if (!drag) return;
		dragRef.current = null;
		if (drag.frame !== null) cancelAnimationFrame(drag.frame);
		document.body.classList.remove("is-resizing-x");
		document.body.style.cursor = "";
	}, []);
	const ratios = (x: number, y: number) => {
		const rect = screenRef.current?.getBoundingClientRect();
		if (!rect || rect.width <= 0 || rect.height <= 0) return null;
		return { row: ratioFromPoint(row, rect, x, y), column: ratioFromPoint(column, rect, x, y) };
	};
	const apply = (x: number, y: number) => {
		const next = ratios(x, y);
		if (!next) return null;
		screenRef.current?.style.setProperty(ratioVar(row.path), String(next.row));
		screenRef.current?.style.setProperty(ratioVar(column.path), String(next.column));
		return next;
	};
	const onPointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
		if (event.button !== 0) return;
		event.preventDefault();
		event.currentTarget.setPointerCapture(event.pointerId);
		dragRef.current = { frame: null, x: event.clientX, y: event.clientY };
		document.body.classList.add("is-resizing-x");
		document.body.style.cursor = "move";
	};
	const onPointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
		const drag = dragRef.current;
		if (!drag) return;
		drag.x = event.clientX;
		drag.y = event.clientY;
		if (drag.frame !== null) return;
		drag.frame = requestAnimationFrame(() => {
			drag.frame = null;
			apply(drag.x, drag.y);
		});
	};
	const endDrag = () => {
		const drag = dragRef.current;
		if (!drag) return;
		dragRef.current = null;
		if (drag.frame !== null) cancelAnimationFrame(drag.frame);
		const next = apply(drag.x, drag.y);
		document.body.classList.remove("is-resizing-x");
		document.body.style.cursor = "";
		if (!next) return;
		onResize(row.path, next.row);
		onResize(column.path, next.column);
	};
	return (
		<div
			aria-hidden="true"
			className="pane-split-junction"
			data-testid="split-junction"
			onDoubleClick={() => {
				onResize(row.path, 0.5);
				onResize(column.path, 0.5);
			}}
			onLostPointerCapture={endDrag}
			onPointerCancel={endDrag}
			onPointerDown={onPointerDown}
			onPointerMove={onPointerMove}
			onPointerUp={endDrag}
			style={{ left: row.style.left, top: column.style.top }}
		/>
	);
}

/**
 * The boundary between two panes of a split. Dragging rewrites only that
 * split's ratio variable on the screen element, so no pane re-renders until
 * the drag ends; terminals refit through their own resize observers.
 */
export function SplitDivider({
	screenRef,
	divider,
	onResize,
}: {
	screenRef: RefObject<HTMLElement | null>;
	divider: CssDivider;
	onResize: (path: string, ratio: number) => void;
}) {
	const { t } = useTranslation();
	const row = divider.dir === "row";
	const dragRef = useRef<{ frame: number | null; ratio: number } | null>(null);
	useEffect(() => () => {
		const drag = dragRef.current;
		if (!drag) return;
		dragRef.current = null;
		if (drag.frame !== null) cancelAnimationFrame(drag.frame);
		document.body.classList.remove("is-resizing-x");
		document.body.style.cursor = "";
	}, []);
	const ratioNow = () => {
		const value = Number.parseFloat(screenRef.current?.style.getPropertyValue(ratioVar(divider.path)) ?? "");
		return Number.isFinite(value) ? value : 0.5;
	};
	const clampRatio = (ratio: number) => clampDividerRatio(divider, screenRef.current?.getBoundingClientRect(), ratio);
	const ratioAt = (x: number, y: number) => {
		const rect = screenRef.current?.getBoundingClientRect();
		return rect && rect.width > 0 && rect.height > 0 ? ratioFromPoint(divider, rect, x, y) : ratioNow();
	};
	const onPointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
		if (event.button !== 0) return;
		event.preventDefault();
		event.currentTarget.setPointerCapture(event.pointerId);
		dragRef.current = { frame: null, ratio: ratioNow() };
		// Shared with the shell resize grips: terminals do their final fit when
		// the class goes away.
		document.body.classList.add("is-resizing-x");
		if (!row) document.body.style.cursor = "row-resize";
	};
	const onPointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
		const drag = dragRef.current;
		if (!drag) return;
		drag.ratio = ratioAt(event.clientX, event.clientY);
		if (drag.frame !== null) return;
		drag.frame = requestAnimationFrame(() => {
			drag.frame = null;
			screenRef.current?.style.setProperty(ratioVar(divider.path), String(drag.ratio));
		});
	};
	const endDrag = () => {
		const drag = dragRef.current;
		if (!drag) return;
		dragRef.current = null;
		if (drag.frame !== null) cancelAnimationFrame(drag.frame);
		screenRef.current?.style.setProperty(ratioVar(divider.path), String(drag.ratio));
		document.body.classList.remove("is-resizing-x");
		document.body.style.cursor = "";
		onResize(divider.path, drag.ratio);
	};
	const onKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
		const keys = row ? { ArrowLeft: -1, ArrowRight: 1 } : { ArrowUp: -1, ArrowDown: 1 };
		const direction = keys[event.key as keyof typeof keys];
		if (direction === undefined) return;
		event.preventDefault();
		onResize(divider.path, clampRatio(ratioNow() + direction * KEYBOARD_STEP));
	};
	return (
		<div
			aria-label={t("splitPane.resize")}
			aria-orientation={row ? "vertical" : "horizontal"}
			aria-valuemax={100}
			aria-valuemin={0}
			aria-valuenow={Math.round(divider.ratio * 100)}
			className="pane-split-divider"
			data-orientation={row ? "vertical" : "horizontal"}
			data-testid="split-divider"
			onDoubleClick={() => onResize(divider.path, 0.5)}
			onKeyDown={onKeyDown}
			onLostPointerCapture={endDrag}
			onPointerCancel={endDrag}
			onPointerDown={onPointerDown}
			onPointerMove={onPointerMove}
			onPointerUp={endDrag}
			role="separator"
			style={row
				? { left: divider.style.left, top: divider.style.top, height: divider.style.height }
				: { left: divider.style.left, top: divider.style.top, width: divider.style.width }}
			tabIndex={0}
		/>
	);
}

export type SplitGroupMember = {
	key: string;
	label: string;
	icon: ReactNode;
	onSelect: () => void;
	/** Closes the member's pane (a member that cannot close leaves the group). */
	onClose: () => void;
	/** A shell whose running agent should show, as on its strip tab. */
	agentHandleId?: string;
};

/**
 * A shell in a split has no strip tab of its own, so wherever its name shows
 * (the group tab, its pane header) the agent running in it shows as on that tab.
 */
function withMemberAgent(member: SplitGroupMember, agents: ReturnType<typeof useShellAgentStore.getState>["agents"]) {
	const agent = member.agentHandleId ? agents[member.agentHandleId] : undefined;
	if (!agent) return { ...member, agent };
	// A default-named shell ("Terminal 3") reads as its agent.
	const label = /^Terminal \d+$/.test(member.label) ? agentLabel(agent.harness) : member.label;
	return { ...member, agent, label, icon: <ShellAgentIcon agent={agent} done={false} /> };
}

/** Height of a pane's title bar; the pane's content sits below it. */
export const PANE_HEADER_PX = 28;

/**
 * The title bar of one pane in a split. It is what makes a pane read as a tab:
 * it carries the tab's icon and name, is the handle to drag the pane (onto a
 * pane's edge to rearrange, onto the strip to unsplit), and has a close
 * button where the pane can close. Pressing anywhere on it focuses the pane at once.
 */
export function SplitPaneHeader({
	member,
	focused,
	closable,
	style,
	menu,
	onPress,
}: {
	member: SplitGroupMember;
	focused: boolean;
	closable: boolean;
	style: CSSProperties;
	menu: TabMenuSpec & { kind: "group" };
	/** The header was pressed: the pane should take keyboard focus. */
	onPress?: () => void;
}) {
	const { t } = useTranslation();
	useShellAgentStream(Boolean(member.agentHandleId));
	const agents = useShellAgentStore((state) => state.agents);
	const shown = withMemberAgent(member, agents);
	const header = (
		<div
			className={cn(
				"pane-split-header absolute flex items-center gap-1 overflow-hidden border-b border-border pr-2 pl-1",
				focused ? "bg-overlay text-foreground" : "bg-background text-passive hover:text-foreground",
			)}
			data-focused={focused || undefined}
			data-split-pane-header={member.key}
			onPointerDown={(event) => {
				if (event.button !== 0) return;
				member.onSelect();
				onPress?.();
				// The title chip is the tab being carried, so the ghost looks like a tab.
				beginPaneTabDrag(event, member.key, event.currentTarget, undefined, { immediate: true });
			}}
			style={style}
			title={t("splitPane.dragHint", { name: shown.label })}
		>
			<span
				className="pointer-events-none inline-flex min-w-0 items-center gap-1.5 rounded-md px-1 text-control leading-none"
				data-pane-drag-chip
			>
				<GripVertical aria-hidden="true" className="size-icon-sm shrink-0 opacity-50" />
				<span className="grid size-icon-base shrink-0 place-items-center">{shown.icon}</span>
				<span className="truncate">{shown.label}</span>
			</span>
			<span className="min-w-1 flex-1" />
			{closable ? (
				<Pressable
					aria-label={t("splitPane.closePane", { name: shown.label })}
					className={cn(terminalTabActionClassName, "shrink-0")}
					onClick={(event) => {
						event.stopPropagation();
						member.onClose();
					}}
					onPointerDown={(event) => event.stopPropagation()}
					type="button"
				>
					<X aria-hidden="true" className="size-icon-sm" />
				</Pressable>
			) : null}
		</div>
	);
	return <TabContextMenu spec={menu}>{header}</TabContextMenu>;
}

/**
 * One strip tab for tabs shown together. The focused member shows its name;
 * the others show only their icon, named by a tooltip. Each member is its own
 * target: clicking it shows the group with that pane focused, and dragging it
 * moves it within the split or out of the group. Hovering the tab swaps the
 * focused member's icon for a close button, as on other tabs, and its menu
 * (`useTabMenuItems`) ungroups it, followed by the focused member's own rows.
 */
export function SplitGroupTab({
	active,
	focusedKey,
	members,
	menu,
}: {
	active: boolean;
	focusedKey?: string;
	members: SplitGroupMember[];
	menu: TabMenuSpec & { kind: "group" };
}) {
	const { t } = useTranslation();
	// A shell in a split has no strip tab of its own, so the group tab keeps
	// the agent watch open and shows the agent as that tab would.
	useShellAgentStream(members.some((member) => member.agentHandleId));
	const agents = useShellAgentStore((state) => state.agents);
	const shown = members.map((member) => withMemberAgent(member, agents));
	const frame = (
		<span
			aria-label={t("splitPane.group", { names: shown.map((member) => member.label).join(", ") })}
			className={cn(
				"group relative inline-flex h-full shrink-0 self-stretch items-stretch border-r border-border",
				active ? "bg-overlay text-foreground" : "text-passive hover:bg-raised",
			)}
			// The floating top bar draws the selected tab's pill from data-active.
			data-active={active || undefined}
			data-split-group-tab
			data-terminal-tab-frame
			role="group"
		>
			{shown.map((member, index) => {
				const focused = member.key === focusedKey;
				// The aria-label hides the badge's own text, so it names the agent's state too.
				const stateLabel = member.agent ? shellAgentStateLabel(t, member.agent, false) : undefined;
				const segment = (
					<Pressable
						aria-label={stateLabel ? `${member.label} · ${stateLabel}` : member.label}
						aria-selected={active && focused}
						className={cn(
							"inline-flex h-full min-w-0 cursor-pointer items-center gap-2 leading-none",
							focused ? "max-w-shell-tab-max px-2 text-control" : "max-w-40 px-2 text-control",
							active && focused ? "text-foreground" : active ? "text-muted-foreground hover:text-foreground" : "hover:text-foreground",
						)}
						data-split-member={member.key}
						onClick={(event) => {
							if (event.detail > 1) return;
							member.onSelect();
						}}
						// A middle-click closes this member's pane, as its close button does.
						onMouseDown={(event) => { if (event.button === 1) event.preventDefault(); }}
						onAuxClick={(event) => {
							if (event.button !== 1) return;
							event.preventDefault();
							member.onClose();
						}}
						onPointerDown={(event) => {
							// A member drags on its own; the group tab itself stays in place.
							event.stopPropagation();
							beginPaneTabDrag(event, member.key, event.currentTarget);
						}}
						role="tab"
						tabIndex={active && focused ? 0 : -1}
						type="button"
					>
						<span
							className={cn(
								"pointer-events-none grid size-icon-base shrink-0 place-items-center",
								focused && "group-hover:opacity-0 group-has-[:focus-visible]:opacity-0",
							)}
						>
							{member.icon}
						</span>
						<span className="truncate">{member.label}</span>
					</Pressable>
				);
				return (
					<Fragment key={member.key}>
						{index > 0 ? (
							<span aria-hidden="true" className="my-2.5 w-px shrink-0 self-stretch bg-border" />
						) : null}
						{focused ? (
							<span className="relative inline-flex min-w-0">
								{segment}
								{/* Takes the icon's slot on hover, like the close button on other tabs. */}
								<span
									className="pointer-events-none absolute left-2 top-1/2 grid size-icon-base -translate-y-1/2 place-items-center opacity-0 group-hover:pointer-events-auto group-hover:opacity-100 group-has-[:focus-visible]:pointer-events-auto group-has-[:focus-visible]:opacity-100"
									data-terminal-tab-action
								>
									<Pressable
										aria-label={t("splitPane.closePane", { name: member.label })}
										className={terminalTabActionClassName}
										onClick={(event) => {
											event.stopPropagation();
											member.onClose();
										}}
										// The X covers the member's icon, so a middle-click there closes the pane too.
										onMouseDown={(event) => { if (event.button === 1) event.preventDefault(); }}
										onAuxClick={(event) => {
											if (event.button !== 1) return;
											event.preventDefault();
											member.onClose();
										}}
										onPointerDown={(event) => event.stopPropagation()}
										type="button"
									>
										<X aria-hidden="true" className="size-icon-sm" />
									</Pressable>
								</span>
							</span>
						) : (
							<Tooltip>
								<TooltipTrigger asChild>{segment}</TooltipTrigger>
								<TooltipContent side="bottom">{member.label}</TooltipContent>
							</Tooltip>
						)}
					</Fragment>
				);
			})}
			{active ? (
				<span
					aria-hidden="true"
					className="pointer-events-none absolute inset-x-0 bottom-0 h-0.5 bg-foreground/80"
					data-testid="active-terminal-tab-indicator"
				/>
			) : null}
		</span>
	);
	return <TabContextMenu spec={menu}>{frame}</TabContextMenu>;
}
