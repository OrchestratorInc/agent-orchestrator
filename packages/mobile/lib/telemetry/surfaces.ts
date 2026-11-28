// The app's screens and sheets as a closed vocabulary, keyed by route file.
// Pure, so the mapping is unit-tested: `surfaces.test.ts` reads app/ and fails
// when a route has no entry. A surface names a place in the app, never what it
// shows, so no session, project or file id ever rides on it.

export const SURFACES = [
	"board",
	"projects",
	"project",
	"compose",
	"spawn",
	"prs",
	"notifications",
	// The session route, before its screen knows whether it is a chat or a terminal.
	"session",
	"session_chat",
	"session_tui",
	// An Orchestrator Cloud session, on the same route.
	"session_cloud",
	"conversation_actions",
	"review",
	"review_actions",
	"reviewer",
	"files",
	"file_viewer",
	"files_review",
	"files_branch",
	"files_changes",
	"artifacts",
	"artifact",
	"artifact_history",
	"render",
	"preview",
	"browser",
	"device",
	"shell",
	"cloud_terminal",
	"cloud_new_project",
	"cloud_plan",
	"conversation_usage",
	"model_picker",
	"agent_picker",
	"account",
	"machine_name",
	"onboarding",
	"pair",
	"connect",
	"settings",
	"settings_theme",
	"settings_machine",
	"settings_cloud",
	"other",
] as const;

export type Surface = (typeof SURFACES)[number];

// Route path (groups removed, `index` dropped) to surface.
const ROUTES: Record<string, Surface> = {
	"": "board",
	projects: "projects",
	compose: "compose",
	"project/[id]": "project",
	spawn: "spawn",
	prs: "prs",
	notifications: "notifications",
	"session/[id]": "session",
	"conversation-actions": "conversation_actions",
	"review/[sessionId]": "review",
	"sheets/review-actions": "review_actions",
	"reviewer/[reviewId]": "reviewer",
	"files/[sessionId]": "files",
	"files/[sessionId]/folder": "files",
	"files/[sessionId]/file": "file_viewer",
	"files/[sessionId]/review": "files_review",
	"sheets/files-branch": "files_branch",
	"sheets/files-changes": "files_changes",
	"artifacts/[sessionId]": "artifacts",
	"artifact/[sessionId]": "artifact",
	"artifact-version/[sessionId]": "artifact",
	"shared-artifact": "artifact",
	"sheets/artifact-history": "artifact_history",
	render: "render",
	"preview/[id]": "preview",
	"browser/[id]": "browser",
	"device/[id]": "device",
	"shell/[handleId]": "shell",
	"cloud/terminal": "cloud_terminal",
	"cloud/new-project": "cloud_new_project",
	"sheets/cloud-plan": "cloud_plan",
	"sheets/conversation-usage": "conversation_usage",
	"sheets/model": "model_picker",
	"sheets/agent": "agent_picker",
	"sheets/account": "account",
	"sheets/machine-name": "machine_name",
	onboarding: "onboarding",
	pair: "pair",
	connect: "connect",
	settings: "settings",
	"settings/theme": "settings_theme",
	"settings/machine/[id]": "settings_machine",
	"settings/machine/[id]/edit": "settings_machine",
	"settings/cloud": "settings_cloud",
	"settings/cloud/agent/[agent]": "settings_cloud",
};

/** The route key for expo-router's segments: groups like `(tabs)` and `index` dropped. */
export function routeKey(segments: readonly string[]): string {
	return segments.filter((segment) => !/^\(.*\)$/.test(segment) && segment !== "index").join("/");
}

/** The surface for expo-router's `useSegments()`; `other` for a route not listed. */
export function surfaceForSegments(segments: readonly string[]): Surface {
	return ROUTES[routeKey(segments)] ?? "other";
}

/** The session route and the screens it can show (chat, terminal, Cloud) count as one place. */
export function isSessionSurface(surface: Surface): boolean {
	return surface === "session" || surface === "session_chat" || surface === "session_tui" || surface === "session_cloud";
}
