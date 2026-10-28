// Separate Sentry projects identify errors from the Go daemon and the desktop
// Electron surfaces. The daemon sends only when its existing consent gate allows
// it. Electron main sends consented desktop errors; renderer captures pass
// through the existing main-owned policy boundary. Mobile has its own project and DSN
// in packages/mobile because the Expo bundle cannot reach frontend/src.
// These client DSNs are write-only ingest endpoints, not secrets.
export const DEFAULT_DAEMON_SENTRY_DSN =
	"https://e21b875a3f97a58fa0cf4f6754285267@o4511955966361600.ingest.us.sentry.io/4512334276329472";

export const DEFAULT_ELECTRON_SENTRY_DSN =
	"https://5fe434b5838d57447e2a4b5b4e8b551a@o4511955966361600.ingest.us.sentry.io/4512334283931648";

export function daemonSentryDsn(override: string | undefined, isDev: boolean): string {
	return override ?? (isDev ? "" : DEFAULT_DAEMON_SENTRY_DSN);
}
