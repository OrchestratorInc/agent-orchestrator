import { agentLabel } from "@aoagents/product-ui";
import { SquareTerminal, X } from "@/components/app-icons";
import { type KeyboardEvent, type MouseEvent, useEffect, useRef, useState } from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { useTruncatedText } from "../hooks/useTruncatedText";
import type { ShellTerminal } from "../hooks/useShellTerminals";
import { cn } from "../lib/utils";
import { type ShellAgent, useShellAgentStore, useShellAgentStream } from "../stores/shell-agent-store";
import { AgentAvatar } from "./AgentAvatar";
import { TabContextMenu } from "./TabContextMenu";
import { TerminalTabFrame, terminalTabActionClassName, terminalTabRenameInputClassName } from "./TerminalTabFrame";
import { Pressable } from "./ui/pressable";

type ShellTerminalTabProps = {
	shell: ShellTerminal;
	isActive: boolean;
	onSelect: () => void;
	onClose: () => void;
	/** Visually connects the active tab to a terminal pane directly below it. */
	appearance?: "pill" | "connected";
	/** Commit a new tab title. Omitted where rename is not wired. */
	onRename?: (title: string) => void;
	/** Every shell tab handle in this strip; tabs running the same agent keep their numbers. */
	stripShellHandleIds?: readonly string[];
};

// One shell tab in the session pane's tab strip (CenterPane). Session-pane
// tabs use a connected treatment that visually continues into xterm below.
// The full title only becomes the hover tooltip when the strip truncates it.
//
// Renaming happens inline: double-click, F2, or Rename in the right-click menu.
// Enter or blur commits, Escape cancels, and an empty or unchanged name is
// discarded. The close control is a sibling
// button, not nested inside the tab button - nesting interactive elements is
// invalid HTML and breaks keyboard traversal. Connected session-strip tabs use
// the shared TerminalTabFrame, which shows that sibling in the glyph's slot on hover.
export function ShellTerminalTab({
	shell,
	isActive,
	onSelect,
	onClose,
	appearance = "pill",
	onRename,
	stripShellHandleIds,
}: ShellTerminalTabProps) {
	const { t } = useTranslation();
	const [isEditing, setIsEditing] = useState(false);
	const [draft, setDraft] = useState(shell.title);
	const inputRef = useRef<HTMLInputElement | null>(null);
	const lastClickAtRef = useRef(0);
	const canEdit = Boolean(onRename) && !shell.optimistic;
	// Only the local daemon watches shells, so cloud and remote-host tabs stay plain.
	const tracksAgent = !shell.optimistic && !shell.cloud && !shell.hostId;
	useShellAgentStream(tracksAgent);
	const agent = useShellAgentStore((state) => (tracksAgent ? state.agents[shell.handleId] : undefined));
	const unseenDone = useShellAgentStore((state) => Boolean(state.unseenDone[shell.handleId]));
	const markSeen = useShellAgentStore((state) => state.markSeen);
	// A default-named tab ("Terminal 3") reads as its agent ("Claude Code"),
	// keeping the number when another tab runs the same agent. Display only:
	// rename edits the stored title, which returns when the agent exits.
	const defaultNumber = /^Terminal (\d+)$/.exec(shell.title)?.[1];
	const sharesAgent = useShellAgentStore((state) =>
		Boolean(agent && stripShellHandleIds?.some((id) => id !== shell.handleId && state.agents[id]?.harness === agent.harness)),
	);
	const agentName = agent && defaultNumber ? agentLabel(agent.harness) : undefined;
	const terminalTitle = shell.terminalTitle;
	const title = terminalTitle ?? (agentName ? (sharesAgent ? `${agentName} ${defaultNumber}` : agentName) : shell.title);
	// An agent's name is display only, so renaming starts from the real title.
	const renameBase = terminalTitle ?? shell.title;
	const { ref, isTruncated } = useTruncatedText<HTMLButtonElement>(title);
	useEffect(() => {
		if (isActive && unseenDone) markSeen(shell.handleId);
	}, [isActive, markSeen, shell.handleId, unseenDone]);
	useEffect(() => {
		if (isEditing) {
			inputRef.current?.focus();
			inputRef.current?.select();
		}
	}, [isEditing]);

	const beginEdit = () => {
		if (!canEdit || isEditing) return;
		setDraft(renameBase);
		setIsEditing(true);
	};

	// Detect a double-click ourselves from two quick clicks rather than relying on
	// the native dblclick event: some trackpad configurations deliver two taps as
	// two separate clicks that never synthesize a dblclick, so onDoubleClick would
	// never fire. Two clicks within 500ms anywhere on the tab start the rename.
	const handleClick = (event: MouseEvent) => {
		onSelect();
		// Roving tab navigation activates the focused tab with HTMLElement.click(),
		// whose detail is 0. It should select, but must not participate in the
		// pointer-only double-click rename gesture.
		if (event.detail === 0) return;
		const now = Date.now();
		const isDoubleClick = now - lastClickAtRef.current < 500;
		lastClickAtRef.current = isDoubleClick ? 0 : now;
		if (isDoubleClick) beginEdit();
	};

	// The selection button owns the whole visible tab surface; the
	// click-timing detector above catches the double-click rename.
	const tabRenameHandlers = isEditing ? {} : { onClick: handleClick, onDoubleClick: beginEdit };

	const renameKeyProps = canEdit
		? {
				"aria-keyshortcuts": "F2",
				onKeyDown: (event: KeyboardEvent) => {
					if (event.key !== "F2") return;
					event.preventDefault();
					beginEdit();
				},
			}
		: {};

	const commit = () => {
		if (!isEditing) return;
		setIsEditing(false);
		const next = draft.trim();
		// Confirming unchanged must not pin a program's title as the tab's name.
		if (next && next !== renameBase) onRename?.(next);
	};

	const cancel = () => {
		setIsEditing(false);
		setDraft(renameBase);
	};

	const closeControl = {
		"aria-label": t("terminal.closeNamed", { title }),
		"data-terminal-tab-action": true,
		onClick: (event: MouseEvent) => {
			event.stopPropagation();
			onClose();
		},
		onDoubleClick: (event: MouseEvent) => event.stopPropagation(),
		type: "button" as const,
	};
	if (appearance === "connected") {
		const closeAction = isEditing || shell.optimistic ? undefined : (
			<Pressable {...closeControl} className={terminalTabActionClassName}>
				<X aria-hidden="true" className="size-icon-sm" />
			</Pressable>
		);
		const editingContent = isEditing ? (
			<input
				aria-label={t("terminal.rename", { title })}
				className={terminalTabRenameInputClassName}
				onBlur={commit}
				onChange={(event) => setDraft(event.target.value)}
				onKeyDown={(event) => {
					if (event.key === "Enter") {
						event.preventDefault();
						commit();
					} else if (event.key === "Escape") {
						event.preventDefault();
						cancel();
					}
				}}
				ref={inputRef}
				value={draft}
			/>
		) : undefined;
		return (
			<TabContextMenu
				disabled={isEditing || shell.optimistic}
				spec={{ kind: "shell", tabKey: shell.handleId, title, onRename: canEdit ? beginEdit : undefined, onClose, closeShortcut: isActive }}
			>
				<TerminalTabFrame
					wide={Boolean(agent)}
					action={closeAction}
					onClose={closeAction ? onClose : undefined}
					active={isActive}
					buttonProps={{
						"aria-current": isActive,
						"aria-selected": isActive,
						...tabRenameHandlers,
						...renameKeyProps,
						role: "tab",
						tabIndex: isActive ? 0 : -1,
						title: isTruncated
							? title
							: t("terminal.renameHintDoubleClick", { workingDir: shell.workingDir }),
						type: "button",
					}}
					buttonRef={ref}
					editingContent={editingContent}
					editingValue={draft}
					icon={
						agent ? (
							<ShellAgentIcon agent={agent} done={unseenDone && !isActive} />
						) : (
							<SquareTerminal aria-hidden="true" className="size-icon-sm" />
						)
					}
					label={title}
				/>
			</TabContextMenu>
		);
	}

	return (
		<span
			className={cn(
				"group relative inline-flex h-full min-w-shell-tab-min shrink-0 self-stretch items-center gap-1 rounded-md px-2 py-1",
				isActive ? "bg-interactive-active" : "hover:bg-interactive-hover/60",
			)}
		>
			{isEditing ? (
				<input
					aria-label={t("terminal.rename", { title })}
					className={cn("min-w-flex-min rounded-sm border border-accent bg-background px-1 font-mono text-control font-semibold text-foreground shadow-sm outline-none ring-1 ring-accent", agent ? "max-w-agent-shell-tab-max" : "max-w-shell-tab-max")}
					onBlur={commit}
					onChange={(event) => setDraft(event.target.value)}
					onKeyDown={(event) => {
						if (event.key === "Enter") {
							event.preventDefault();
							commit();
						} else if (event.key === "Escape") {
							event.preventDefault();
							cancel();
						}
					}}
					ref={inputRef}
					value={draft}
				/>
			) : (
				<Pressable
					ref={ref}
					aria-current={isActive}
					aria-selected={isActive}
					className={cn(
						"min-w-flex-min cursor-pointer select-none truncate font-mono text-control font-semibold focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent/50",
						isActive ? "text-foreground" : "text-passive group-hover:text-foreground",
						agent ? "max-w-agent-shell-tab-max" : "max-w-shell-tab-max",
					)}
					{...tabRenameHandlers}
					{...renameKeyProps}
					role="tab"
					tabIndex={isActive ? 0 : -1}
					title={
						isTruncated
							? title
							: t("terminal.renameHintDoubleClick", { workingDir: shell.workingDir })
					}
					type="button"
				>
					<span className="inline-flex min-w-0 -translate-y-px items-center">
						<span className="truncate">{title}</span>
					</span>
				</Pressable>
			)}
			<Pressable
				{...closeControl}
				className="inline-flex h-control-sm w-control-sm shrink-0 items-center justify-center overflow-hidden text-passive opacity-0 transition-colors duration-fast ease-out hover:text-foreground group-hover:opacity-100 group-focus-within:opacity-100 focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-accent/50 motion-reduce:transition-none"
			>
				<X aria-hidden="true" className="size-icon-sm" />
			</Pressable>
		</span>
	);
}

// The agent's state as a dot on its logo, the sidebar's vocabulary: pulsing
// while it works, red while it waits on the user, muted for a finished turn
// not yet seen. It sits on the icon, so it never moves the title.
const AGENT_BADGE = {
	working: { label: "terminal.agentWorking", className: "bg-foreground animate-pulse motion-reduce:animate-none" },
	waiting: { label: "terminal.agentWaiting", className: "bg-destructive" },
	done: { label: "terminal.agentDone", className: "bg-muted-foreground" },
} as const;

function shellAgentBadgeState(agent: ShellAgent, done: boolean) {
	return agent.state === "idle" ? (done ? "done" : undefined) : agent.state;
}

/** The agent's state in words, for an accessible name that would hide the badge's own text. */
export function shellAgentStateLabel(t: TFunction, agent: ShellAgent, done: boolean): string | undefined {
	const state = shellAgentBadgeState(agent, done);
	return state ? t(AGENT_BADGE[state].label, { agent: agentLabel(agent.harness) }) : undefined;
}

/** A shell's agent logo, with its state as a badge on the logo's corner. */
export function ShellAgentIcon({ agent, done }: { agent: ShellAgent; done: boolean }) {
	const { t } = useTranslation();
	const state = shellAgentBadgeState(agent, done);
	return (
		<span className="relative grid size-icon-base place-items-center" data-shell-agent-state={state}>
			<AgentAvatar className="size-icon-sm!" decorative provider={agent.harness} />
			{state ? (
				<>
					<span
						aria-hidden="true"
						className={cn("absolute -bottom-px -right-px size-1.5 rounded-full ring-1 ring-background", AGENT_BADGE[state].className)}
					/>
					<span className="sr-only">{shellAgentStateLabel(t, agent, done)}</span>
				</>
			) : null}
		</span>
	);
}
