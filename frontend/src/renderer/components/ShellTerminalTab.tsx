import { agentLabel } from "@aoagents/product-ui";
import { CircleAlert, LoaderCircle, SquareTerminal, X } from "lucide-react";
import { type KeyboardEvent, type MouseEvent, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useTruncatedText } from "../hooks/useTruncatedText";
import type { ShellTerminal } from "../hooks/useShellTerminals";
import { isWindowsPlatform } from "../lib/platform";
import { cn } from "../lib/utils";
import { type ShellAgent, useShellAgentStore, useShellAgentStream } from "../stores/shell-agent-store";
import { useUiStore } from "../stores/ui-store";
import { AgentAvatar } from "./AgentAvatar";
import { TerminalTabFrame, terminalTabActionClassName } from "./TerminalTabFrame";
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
// Renaming happens inline with the platform's native gesture: double-click on
// macOS/Linux, right-click on Windows. Enter or blur commits, Escape cancels,
// and an empty or unchanged name is discarded. The close control is a sibling
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
	// The agent tab is a Developer Mode preview, like the simplified topbar.
	const developerMode = useUiStore((state) => state.developerMode);
	const tracksAgent = developerMode && !shell.optimistic && !shell.cloud && !shell.hostId;
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
	const title = agentName ? (sharesAgent ? `${agentName} ${defaultNumber}` : agentName) : shell.title;
	const { ref, isTruncated } = useTruncatedText<HTMLButtonElement>(title);
	useEffect(() => {
		if (isActive && unseenDone) markSeen(shell.handleId);
	}, [isActive, markSeen, shell.handleId, unseenDone]);
	// Rename gesture per platform: Windows uses right-click (its convention for
	// tab/file renames); macOS and Linux use double-click.
	const renameViaRightClick = isWindowsPlatform();

	useEffect(() => {
		if (isEditing) {
			inputRef.current?.focus();
			inputRef.current?.select();
		}
	}, [isEditing]);

	const beginEdit = () => {
		if (!canEdit || isEditing) return;
		setDraft(shell.title);
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
		if (renameViaRightClick || event.detail === 0) return;
		const now = Date.now();
		const isDoubleClick = now - lastClickAtRef.current < 500;
		lastClickAtRef.current = isDoubleClick ? 0 : now;
		if (isDoubleClick) beginEdit();
	};

	// The selection button owns the whole visible tab surface. Windows uses
	// right-click for rename; macOS/Linux use the click-timing detector above.
	const tabRenameHandlers = isEditing
		? {}
		: renameViaRightClick
			? {
					onClick: handleClick,
					onContextMenu: (event: MouseEvent) => {
						event.preventDefault();
						beginEdit();
					},
				}
			: { onClick: handleClick, onDoubleClick: beginEdit };

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
		if (next && next !== shell.title) onRename?.(next);
	};

	const cancel = () => {
		setIsEditing(false);
		setDraft(shell.title);
	};

	const closeControl = {
		"aria-label": t("terminal.closeNamed", { title }),
		"data-terminal-tab-action": true,
		onClick: (event: MouseEvent) => {
			event.stopPropagation();
			onClose();
		},
		onDoubleClick: (event: MouseEvent) => event.stopPropagation(),
		onContextMenu: (event: MouseEvent) => event.stopPropagation(),
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
				className="min-w-0 flex-1 rounded-xs border border-accent bg-background px-1 text-control text-foreground outline-none ring-1 ring-accent"
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
			<TerminalTabFrame
				wide={Boolean(agent)}
				action={closeAction}
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
						: t(renameViaRightClick ? "terminal.renameHintRightClick" : "terminal.renameHintDoubleClick", {
								workingDir: shell.workingDir,
							}),
					type: "button",
				}}
				buttonRef={ref}
				editingContent={editingContent}
				icon={
					agent ? (
						<AgentAvatar className="size-icon-sm!" decorative provider={agent.harness} />
					) : (
						<SquareTerminal aria-hidden="true" className="size-icon-sm" />
					)
				}
				label={title}
				trailing={agent && !isEditing ? <ShellAgentStatus agent={agent} done={unseenDone && !isActive} /> : undefined}
			/>
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
							: t(renameViaRightClick ? "terminal.renameHintRightClick" : "terminal.renameHintDoubleClick", {
									workingDir: shell.workingDir,
								})
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

const AGENT_STATUS = {
	working: {
		label: "terminal.agentWorking",
		icon: <LoaderCircle aria-hidden="true" className="size-3 animate-spin text-muted-foreground motion-reduce:animate-none" />,
	},
	waiting: { label: "terminal.agentWaiting", icon: <CircleAlert aria-hidden="true" className="size-3 text-destructive" /> },
	done: { label: "terminal.agentDone", icon: <span aria-hidden="true" className="size-2 rounded-full bg-muted-foreground/60" /> },
} as const;

// The sidebar's status vocabulary: a spinner while the agent works, the red
// alert while it waits on the user, and a dot for a finished turn not yet seen.
function ShellAgentStatus({ agent, done }: { agent: ShellAgent; done: boolean }) {
	const { t } = useTranslation();
	const state = agent.state === "idle" ? (done ? "done" : undefined) : agent.state;
	if (!state) return null;
	const label = t(AGENT_STATUS[state].label, { agent: agentLabel(agent.harness) });
	return (
		<span className="inline-flex size-3 items-center justify-center" data-shell-agent-state={state} title={label}>
			{AGENT_STATUS[state].icon}
			<span className="sr-only">{label}</span>
		</span>
	);
}
