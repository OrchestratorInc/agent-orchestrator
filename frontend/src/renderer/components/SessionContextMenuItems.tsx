import { useCallback, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Archive, Code2, Copy, ExternalLink, GitBranch, Pencil, Pin, PinOff, Play } from "@/components/app-icons";
import type { OpenTarget } from "../../shared/editor-handoff";
import { resumeAgentOnOpen, useCanResumeAgent } from "../hooks/useCanResumeAgent";
import { conversationQueryKey } from "../hooks/useConversation";
import { editorHandoffErrorMessage, useEditorHandoffState, useOpenSessionTarget } from "../hooks/useEditorHandoff";
import { usePinSession, useUnpinSession } from "../hooks/usePinSession";
import { workspaceQueryKeyForHost } from "../hooks/useWorkspaceQuery";
import { aoBridge } from "../lib/bridge";
import { sessionBranchPresentation } from "../lib/session-branch";
import { useUiStore } from "../stores/ui-store";
import { isOrchestratorSession, primaryPR, type WorkspaceSession } from "../types/workspace";
import { MenuEntries, type MenuEntry } from "./menu-model";
import { OpenTargetIcon } from "./OpenTargetIcon";

/** Copy from a menu, which has no inline place to confirm the copy. */
export function useCopyToClipboard() {
	const { t } = useTranslation();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	return useCallback(
		(text: string) => {
			void aoBridge.clipboard.writeText(text).then(
				() => showGlobalToast(t("shell.copied")),
				() => showGlobalToast(t("shell.copyFailed"), undefined, "error"),
			);
		},
		[showGlobalToast, t],
	);
}

export type SessionMenuOptions = {
	/** Absent where the session cannot be archived from here. */
	onArchive?: () => void;
	/** Absent where the title is not editable here; the row shows disabled. */
	onRename?: () => void;
	/** Key hint for the rename row, where a focused trigger takes one. */
	renameShortcut?: string;
};

/**
 * A session's actions as menu entries: open in and copy path, rename and pin,
 * resume, PR and branch links, then archive. Call it from menu content, which
 * Radix mounts only while open, so the lookups run on demand rather than once
 * per sidebar row. An absent session yields no entries.
 */
export function useSessionMenuEntries(session: WorkspaceSession | undefined, { onArchive, onRename, renameShortcut }: SessionMenuOptions = {}): MenuEntry[] {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	const copyToClipboard = useCopyToClipboard();
	const { mutate: pinSession } = usePinSession();
	const { mutate: unpinSession } = useUnpinSession();
	// A local worker whose agent exited and that is not switching interfaces;
	// orchestrators and remote sessions resume elsewhere, so they skip the check.
	const canResume = useCanResumeAgent(!session || session.hostId || isOrchestratorSession(session) ? undefined : session);
	// Remote and Cloud sessions have no local workspace for Electron to hand off.
	const hasLocalWorkspace = Boolean(session && !session.hostId && !session.cloud);
	const workspaceEntries = useSessionWorkspaceEntries(hasLocalWorkspace ? session : undefined);
	if (!session) return [];

	const branch = sessionBranchPresentation(session).branch;
	const showBranch = Boolean(branch) && !isOrchestratorSession(session) && branch !== `session/${session.id}`;
	const pr = primaryPR(session);
	// Rename and pin go to whoever owns the session: its daemon, or the control
	// plane for a Cloud session. A session shared from another organization is
	// the sharer's to name and lists outside this sidebar's pins.
	const canRenameAndPin = !session.cloud?.shared;
	const resume = () => {
		void resumeAgentOnOpen(session.id)
			.catch((error: unknown) => {
				showGlobalToast(t("inspector.resumeAgent"), error instanceof Error ? error.message : undefined, "error");
			})
			.finally(() => {
				void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(session.hostId) });
				void queryClient.invalidateQueries({ queryKey: conversationQueryKey(session.id) });
			});
	};
	const openPullRequest = (url: string, title: string) => {
		aoBridge.app.openExternal(url).catch((error: unknown) => {
			showGlobalToast(title, error instanceof Error ? error.message : undefined, "error");
		});
	};

	const entries: MenuEntry[] = [...workspaceEntries, { kind: "separator", id: "session-workspace-sep" }];
	if (canRenameAndPin) {
		entries.push(
			{
				kind: "item",
				id: "session-rename",
				ariaLabel: t("shell.renameSession", { title: session.title }),
				disabled: !onRename,
				icon: <Pencil aria-hidden="true" />,
				label: t("shell.rename"),
				onSelect: onRename,
				shortcut: renameShortcut,
			},
			{
				kind: "item",
				id: "session-pin",
				icon: session.isPinned ? <PinOff aria-hidden="true" /> : <Pin aria-hidden="true" />,
				label: session.isPinned ? t("shell.unpinSession") : t("shell.pinSession"),
				onSelect: () => (session.isPinned ? unpinSession(session) : pinSession(session)),
			},
		);
	}
	if (canResume) {
		entries.push({ kind: "item", id: "session-resume", icon: <Play aria-hidden="true" />, label: t("command.resumeAgent"), onSelect: resume });
	}
	entries.push({ kind: "separator", id: "session-links-sep" });
	if (pr) {
		entries.push(
			{
				kind: "item",
				id: "session-open-pr",
				icon: <ExternalLink aria-hidden="true" />,
				label: t("command.openPr", { number: pr.number }),
				onSelect: () => openPullRequest(pr.url, t("command.openPr", { number: pr.number })),
			},
			{
				kind: "item",
				id: "session-copy-pr",
				icon: <Copy aria-hidden="true" />,
				label: t("command.copyPrUrl", { number: pr.number }),
				onSelect: () => copyToClipboard(pr.url),
			},
		);
	}
	if (showBranch && branch) {
		entries.push({ kind: "item", id: "session-copy-branch", icon: <GitBranch aria-hidden="true" />, label: t("command.copyBranch"), onSelect: () => copyToClipboard(branch) });
	}
	if (onArchive) {
		entries.push(
			{ kind: "separator", id: "session-archive-sep" },
			{ kind: "item", id: "session-archive", icon: <Archive aria-hidden="true" />, label: t("shell.archiveSession"), onSelect: onArchive },
		);
	}
	return entries;
}

/** The sidebar and board rows' menu: a session's entries in the surrounding menu. */
export function SessionContextMenuItems({ session, ...options }: SessionMenuOptions & { session: WorkspaceSession }) {
	return <MenuEntries entries={useSessionMenuEntries(session, options)} />;
}

/** Open in the default editor, or any other detected target from its submenu,
 *  and copy the worktree path: the same Electron handoff as the session topbar's
 *  editor button. The editor row holds its place while the lookup runs so the
 *  menu does not jump when it resolves. No session, no lookup and no entries. */
function useSessionWorkspaceEntries(session: WorkspaceSession | undefined): MenuEntry[] {
	const { t } = useTranslation();
	const showGlobalToast = useUiStore((state) => state.showGlobalToast);
	// An empty id leaves the query disabled.
	const handoff = useEditorHandoffState(session?.id ?? "", {
		sessionCreatedAt: session?.createdAt,
		sessionTerminated: session?.isTerminated,
	});
	const { data: state, isPending } = handoff;
	const refreshedTargets = useRef(false);
	const open = useOpenSessionTarget();
	if (!session) return [];
	const targets = state?.workspaceAvailable ? state.targets : [];
	const preferred = targets.find((target) => target.kind === "editor" && target.id === state?.preferredEditorId);
	// The default editor leads the submenu; the file manager and terminals follow.
	const editors = targets.filter((target) => target.kind === "editor" && target !== preferred);
	const editorTargets = preferred ? [preferred, ...editors] : editors;
	const otherTargets = targets.filter((target) => target.kind !== "editor");
	// The menu unmounts as soon as an item is picked, so report failure from the
	// promise rather than from mutate callbacks that only run while mounted.
	const launch = (target: OpenTarget) => {
		open.mutateAsync({ sessionId: session.id, projectId: session.workspaceId, targetId: target.id }).catch((error: unknown) => {
			showGlobalToast(t("editor.openInTarget", { name: target.name }), error instanceof Error ? error.message : undefined, "error");
		});
	};
	const copyPath = () => {
		aoBridge.editorHandoff.copyWorkspacePath(session.id).then(
			() => showGlobalToast(t("shortcut.sessionPathCopied")),
			(error: unknown) =>
				showGlobalToast(t("shortcut.sessionPathCopyFailed"), editorHandoffErrorMessage(error) ?? undefined, "error"),
		);
	};
	const targetEntry = (target: OpenTarget): MenuEntry => ({
		kind: "item",
		id: `open-in-${target.id}`,
		icon: <OpenTargetIcon target={target} />,
		label: target.name,
		onSelect: () => launch(target),
	});
	const entries: MenuEntry[] = [];
	if (isPending) {
		entries.push({ kind: "item", id: "open-in-pending", disabled: true, icon: <Code2 aria-hidden="true" />, label: t("editor.preparingWorkspace") });
	} else if (targets.length > 0) {
		// Opening the submenu is all this row does, so it names no single target;
		// the default editor leads the list.
		entries.push({
			kind: "submenu",
			id: "open-in",
			icon: <OpenTargetIcon target={preferred} />,
			label: t("editor.openIn"),
			items: [...editorTargets.map(targetEntry), { kind: "separator", id: "open-in-sep" }, ...otherTargets.map(targetEntry)],
			onOpenChange: (isOpen) => {
				// Like the topbar menu, look for newly installed terminals, once per menu.
				if (!isOpen || refreshedTargets.current) return;
				refreshedTargets.current = true;
				void handoff.refreshTargets().catch(() => undefined);
			},
		});
	}
	if (state?.workspaceAvailable !== false) {
		entries.push({ kind: "item", id: "copy-workspace-path", icon: <Copy aria-hidden="true" />, label: t("editor.copyPath"), onSelect: copyPath });
	}
	return entries;
}
