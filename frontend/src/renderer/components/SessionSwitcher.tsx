import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../lib/bridge";
import {
	buildSessionSwitcherEntries,
	initialSessionSwitcherIndex,
	sessionSwitcherCommitId,
	sessionSwitcherEligibleIds,
	stepSessionSwitcherIndex,
	type SessionSwitcherEntry,
} from "../lib/session-switcher";
import { getSessionStatusDotView } from "../lib/session-presentation";
import { cn } from "../lib/utils";
import type { WorkspaceSession } from "../types/workspace";

type OpenSwitcher = {
	entries: SessionSwitcherEntry[];
	index: number;
	cancelled: boolean;
};

/**
 * Project session switcher. The main process owns Option+Tab (macOS) and
 * Ctrl+Tab (Windows/Linux), including while xterm, the chat composer, or the
 * embedded browser is focused. This surface does not take DOM focus, so the
 * previous control keeps it when the popup closes.
 */
export function SessionSwitcher({
	projectKey,
	sessions,
	currentSessionId,
	kanban,
	onCommit,
}: {
	projectKey: string | undefined;
	sessions: readonly WorkspaceSession[];
	currentSessionId?: string;
	kanban: boolean;
	onCommit: (sessionId: string) => void;
}) {
	const { t } = useTranslation();
	const listId = useId();
	const [open, setOpen] = useState<OpenSwitcher | null>(null);
	const openRef = useRef<OpenSwitcher | null>(null);
	const projectKeyRef = useRef(projectKey);
	const sessionsRef = useRef(sessions);
	const currentSessionIdRef = useRef(currentSessionId);
	const kanbanRef = useRef(kanban);
	const onCommitRef = useRef(onCommit);
	projectKeyRef.current = projectKey;
	sessionsRef.current = sessions;
	currentSessionIdRef.current = currentSessionId;
	kanbanRef.current = kanban;
	onCommitRef.current = onCommit;
	openRef.current = open;

	useEffect(() => {
		if (!openRef.current) return;
		openRef.current = null;
		setOpen(null);
	}, [projectKey]);

	useEffect(() => {
		const closeWithoutCommit = () => {
			if (!openRef.current) return;
			openRef.current = null;
			setOpen(null);
		};
		const disposeStep = aoBridge.app.onSessionSwitcherStep((direction) => {
			if (!projectKeyRef.current) return;
			const current = openRef.current;
			if (!current) {
				const entries = buildSessionSwitcherEntries(sessionsRef.current, currentSessionIdRef.current);
				if (entries.length === 0) return;
				const next = {
					entries,
					index: initialSessionSwitcherIndex(entries, currentSessionIdRef.current, kanbanRef.current),
					cancelled: false,
				};
				openRef.current = next;
				setOpen(next);
				return;
			}
			if (current.cancelled) return;
			const next = {
				...current,
				index: stepSessionSwitcherIndex(current.index, current.entries.length, direction),
			};
			openRef.current = next;
			setOpen(next);
		});
		const disposeRelease = aoBridge.app.onSessionSwitcherRelease(() => {
			const current = openRef.current;
			openRef.current = null;
			setOpen(null);
			if (!current) return;
			const selected = current.entries[current.index]?.id;
			const commitId = sessionSwitcherCommitId(
				selected,
				sessionSwitcherEligibleIds(sessionsRef.current),
				currentSessionIdRef.current,
				current.cancelled,
			);
			if (commitId) onCommitRef.current(commitId);
		});
		const disposeCancel = aoBridge.app.onSessionSwitcherCancel(closeWithoutCommit);
		return () => {
			disposeStep();
			disposeRelease();
			disposeCancel();
		};
	}, []);

	if (!open) return null;
	const selected = open.entries[open.index];
	const byId = new Map(sessions.map((session) => [session.id, session]));
	const selectedTitle = selected ? byId.get(selected.id)?.title ?? selected.id : "";
	return (
		<div className="pointer-events-none fixed inset-0 z-50 flex items-center justify-center p-6" data-testid="session-switcher">
			<div
				aria-activedescendant={selected ? `${listId}-${selected.id}` : undefined}
				aria-label={t("sessionSwitcher.label")}
				className="pointer-events-none w-72 overflow-hidden rounded-lg border border-border bg-popover text-popover-foreground shadow-md"
				data-browser-native-overlay="true"
				data-state="open"
				role="listbox"
				tabIndex={-1}
			>
				<div className="max-h-[min(24rem,70vh)] overflow-y-auto p-1">
					{open.entries.map((entry, index) => {
						const session = byId.get(entry.id);
						const title = session?.title ?? entry.id;
						const dot = session ? getSessionStatusDotView(session) : undefined;
						const active = index === open.index;
						return (
							<div
								aria-selected={active}
								className={cn(
									"flex h-8 items-center gap-2 rounded-md px-2 text-sm",
									active ? "bg-accent text-accent-foreground" : "text-muted-foreground",
								)}
								id={`${listId}-${entry.id}`}
								key={entry.id}
								role="option"
							>
								{dot ? (
									<span
										aria-hidden="true"
										className={cn("size-2 shrink-0 rounded-full", dot.className)}
										data-session-status={session?.status}
									/>
								) : (
									<span aria-hidden="true" className="size-2 shrink-0" />
								)}
								<span className="min-w-0 flex-1 truncate">{title}</span>
								{entry.role === "orchestrator" ? (
									<span className="shrink-0 text-2xs text-muted-foreground">{t("sessionSwitcher.orchestrator")}</span>
								) : null}
							</div>
						);
					})}
				</div>
			</div>
			<div aria-live="polite" className="sr-only" role="status">
				{t("sessionSwitcher.selected", {
					title: selectedTitle,
					position: open.index + 1,
					count: open.entries.length,
				})}
			</div>
		</div>
	);
}
