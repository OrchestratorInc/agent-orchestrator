import { useNavigate } from "@tanstack/react-router";
import { useMemo } from "react";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import { formatTimeCompact } from "../lib/format-time";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { cn } from "../lib/utils";
import { isOrchestratorSession, sessionIsActive, type WorkspaceSession } from "../types/workspace";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "./NavRowHighlight";

const SESSION_LIMIT = 3;

function sessionActivity(session: WorkspaceSession): string | undefined {
	return session.lastInteractionAt ?? session.lastUserMessageAt ?? session.createdAt;
}

/**
 * The last few sessions, shown quietly under the empty composer when the app
 * opens. Reached only from the root redirect; the new chat page opened by
 * "New chat" stays bare.
 */
export function NewChatRecents() {
	const navigate = useNavigate();
	const workspaces = useWorkspaceQuery().data;
	const sessions = useMemo(
		() =>
			(workspaces ?? [])
				.flatMap((workspace) => workspace.sessions.map((session) => ({ session, workspace })))
				.filter(({ session }) => sessionIsActive(session) && !isOrchestratorSession(session))
				.sort((a, b) => (sessionActivity(b.session) ?? "").localeCompare(sessionActivity(a.session) ?? ""))
				.slice(0, SESSION_LIMIT),
		[workspaces],
	);
	if (sessions.length === 0) return null;
	return (
		<div className="mt-6 w-full">
			{sessions.map(({ session, workspace }) => {
				const time = sessionActivity(session);
				return (
					<button
						key={session.id}
						type="button"
						onClick={() => void navigate(sessionNavigateTarget(workspace.id, session.id))}
						className={cn(
							"flex w-full items-center gap-3 rounded-lg px-3 py-1 text-left text-muted-foreground",
							"focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
							NAV_ROW_HIGHLIGHT_HOST_CLASS,
						)}
					>
						<NavRowHighlight className="bg-card" />
						<span className="relative z-[1] min-w-0 flex-1 truncate text-sm leading-5">{session.title}</span>
						{time ? <span className="relative z-[1] shrink-0 text-caption tabular-nums">{formatTimeCompact(time)}</span> : null}
					</button>
				);
			})}
		</div>
	);
}
