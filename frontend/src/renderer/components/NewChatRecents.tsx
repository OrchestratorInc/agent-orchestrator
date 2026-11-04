import { useNavigate } from "@tanstack/react-router";
import { Folder, MessageSquare } from "lucide-react";
import { useMemo, type ReactNode } from "react";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import { formatTimeCompact } from "../lib/format-time";
import { projectNavigateTarget, sessionNavigateTarget } from "../lib/navigate-to-session";
import { getProjectLastOpenedAt } from "../lib/project-history";
import { cn } from "../lib/utils";
import { isOrchestratorSession, sessionIsActive, STANDALONE_PROJECT_KIND, type WorkspaceSession, type WorkspaceSummary } from "../types/workspace";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "./NavRowHighlight";

const PROJECT_LIMIT = 3;
const SESSION_LIMIT = 4;

function sessionActivity(session: WorkspaceSession): string | undefined {
	return session.lastInteractionAt ?? session.lastUserMessageAt ?? session.createdAt;
}

function projectActivity(project: WorkspaceSummary): string | undefined {
	return [getProjectLastOpenedAt(project.id), ...project.sessions.map(sessionActivity)]
		.filter((value): value is string => Boolean(value))
		.sort()
		.at(-1);
}

function Row({ icon, label, detail, time, onClick }: { icon: ReactNode; label: string; detail?: string; time?: string; onClick: () => void }) {
	return (
		<button
			type="button"
			onClick={onClick}
			className={cn(
				"flex w-full items-center gap-3 rounded-lg px-3 py-1 text-left text-muted-foreground",
				"focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
				NAV_ROW_HIGHLIGHT_HOST_CLASS,
			)}
		>
			<NavRowHighlight className="bg-card" />
			<span className="relative z-[1] grid size-6 shrink-0 place-items-center [&_svg]:size-4" aria-hidden="true">{icon}</span>
			<span className="relative z-[1] min-w-0 flex-1 truncate text-sm leading-5">
				<span className="font-medium text-foreground">{label}</span>
				{detail ? <span className="ml-2 text-muted-foreground">{detail}</span> : null}
			</span>
			{time ? <span className="relative z-[1] shrink-0 text-caption tabular-nums">{formatTimeCompact(time)}</span> : null}
		</button>
	);
}

/**
 * The last projects and sessions, shown under the empty composer when the app
 * opens. Reached only from the root redirect; the new chat page opened by
 * "New chat" stays bare.
 */
export function NewChatRecents() {
	const navigate = useNavigate();
	const workspaces = useWorkspaceQuery().data;
	const { projects, sessions } = useMemo(() => {
		const list = workspaces ?? [];
		const byRecent = (a?: string, b?: string) => (b ?? "").localeCompare(a ?? "");
		const projects = list
			.filter((workspace) => workspace.kind !== STANDALONE_PROJECT_KIND)
			.sort((a, b) => byRecent(projectActivity(a), projectActivity(b)))
			.slice(0, PROJECT_LIMIT);
		const sessions = list
			.flatMap((workspace) => workspace.sessions.map((session) => ({ session, workspace })))
			.filter(({ session }) => sessionIsActive(session) && !isOrchestratorSession(session))
			.sort((a, b) => byRecent(sessionActivity(a.session), sessionActivity(b.session)))
			.slice(0, SESSION_LIMIT);
		return { projects, sessions };
	}, [workspaces]);
	if (projects.length === 0 && sessions.length === 0) return null;
	return (
		<div className="mt-8 w-full space-y-3">
			{projects.length > 0 ? (
				<div>
					{projects.map((project) => (
						<Row
							key={project.id}
							icon={<Folder strokeWidth={1.8} />}
							label={project.name}
							time={projectActivity(project)}
							onClick={() => void navigate(projectNavigateTarget(project.id))}
						/>
					))}
				</div>
			) : null}
			{sessions.length > 0 ? (
				<div>
					{sessions.map(({ session, workspace }) => (
						<Row
							key={session.id}
							icon={<MessageSquare strokeWidth={1.8} />}
							label={session.title}
							detail={workspace.kind === STANDALONE_PROJECT_KIND ? undefined : workspace.name}
							time={sessionActivity(session)}
							onClick={() => void navigate(sessionNavigateTarget(workspace.id, session.id))}
						/>
					))}
				</div>
			) : null}
		</div>
	);
}
