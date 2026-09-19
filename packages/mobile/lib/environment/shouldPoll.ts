import type { EnvironmentKind } from "./types";

/**
 * Whether lib/store.tsx's REST poll loop should run for the given
 * environment.
 *
 * Only "local" polls the daemon. A user on Cloud has no daemon to poll — and
 * a paired-but-asleep Mac would otherwise get hammered with failing requests
 * forever, since nothing on the cloud path consumes the result (see
 * lib/cloud/source.ts). `null` (the persisted choice hasn't loaded yet) also
 * does not poll: resolveSessionSource treats it the same way, and starting
 * the loop before the environment is known would poll the daemon for a user
 * who turns out to be on Cloud.
 *
 * Kept as a standalone predicate, mirroring shouldPoll in appStatePoll.ts, so
 * the poll loop's environment gate stays unit-testable without pulling
 * react-native into a vitest run.
 */
export function shouldPollLocal(environment: EnvironmentKind | null): boolean {
	return environment === "local";
}
