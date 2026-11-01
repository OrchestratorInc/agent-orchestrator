// PostHog project the mobile app reports to.
//
// This is the APP project, the same one the desktop app uses, so desktop and
// mobile usage sit together and split by the `client` context property. The
// marketing site is a separate project. The key is a public project key (it
// ships in every client build by design, like the desktop's), not a secret, so
// hardcoding it mirrors the desktop's shared constant. An EXPO_PUBLIC_ override
// lets a build point elsewhere without a code change.
export const MOBILE_POSTHOG_KEY =
	process.env.EXPO_PUBLIC_POSTHOG_KEY?.trim() || "phc_uXAqS8nokL2QLSGBZSEMHTUNVXsFeXu3SrcWG7fjEyVH";

export const MOBILE_POSTHOG_HOST =
	process.env.EXPO_PUBLIC_POSTHOG_HOST?.trim() || "https://us.i.posthog.com";

/**
 * Kill switches, mirroring the desktop's AO_TELEMETRY_DISABLED_EVENTS. Build-time
 * (EXPO_PUBLIC_* is inlined), so it controls the next build. A shipped binary
 * cannot be hotfixed from here; the runtime kill switch for an event already in
 * the field is the PostHog-side ingestion drop rule, the same lever used for the
 * legacy desktop events (see docs/engineering/posthog-cost-controls.mdx).
 */
export const MOBILE_TELEMETRY_DISABLED =
	(process.env.EXPO_PUBLIC_AO_TELEMETRY_DISABLED ?? "").trim() === "1";

export const MOBILE_DISABLED_EVENTS = (process.env.EXPO_PUBLIC_AO_TELEMETRY_DISABLED_EVENTS ?? "")
	.split(",")
	.map((name: string) => name.trim())
	.filter((name: string) => name.length > 0);
