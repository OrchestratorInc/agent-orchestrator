/**
 * Whether opening a chat should restart its stopped agent.
 *
 * The daemon leaves agents stopped after a restart; the session screen starts
 * them, as desktop does. Workers and orchestrators resume alike. A terminated
 * session needs an explicit Restore, and a session that failed to provision has
 * nothing to resume.
 */
export function shouldAutoResume(
	session: { status?: string | null; provisionState?: string },
	terminated: boolean,
	paired: boolean,
): boolean {
	return paired && !terminated && session.status === "exited" && session.provisionState !== "failed";
}
