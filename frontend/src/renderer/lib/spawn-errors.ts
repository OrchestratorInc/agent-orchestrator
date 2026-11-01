// Errors raised after the daemon may have created the session: a retry with the
// same request id must be possible, so these never count as a final refusal.
export const POST_START_ERRORS: ReadonlySet<string> = new Set([
	"SPAWN_COMMIT_FAILED", "SPAWN_INTERNAL", "SPAWN_PROMPT_DELIVERY_FAILED", "SPAWN_LAUNCH_COMMAND_FAILED",
	"SPAWN_SUPERVISOR_FAILED", "SPAWN_PREPARE_LAUNCH_FAILED", "RUNTIME_CREATE_FAILED",
]);
