import type { CloudSharePublishResult } from "../../shared/cloud-share";

// Closed vocabularies for the artifact publish funnel. Only these enums leave
// the machine: never a file name, path, URL, title, session name, content or
// the people a link is shared with.

/** Where an artifact session was started: the Scratchpad, a project, or AO Cloud. */
export const ARTIFACT_SESSION_SCOPES = ["standalone", "project", "cloud"] as const;

/** Why Publish asked to sign in: signed out when clicked, or a publish came back signed out. */
export const ARTIFACT_SIGN_IN_PROMPT_REASONS = ["signed_out", "session_expired"] as const;

/** Why a publish did not go through. `refused` is the plan's link limit, a file type not shared yet, or not the session's owner. */
export const ARTIFACT_PUBLISH_FAILURES = ["signed_out", "too_large", "upload_failed", "cancelled", "refused", "other"] as const;
export type ArtifactPublishFailure = (typeof ARTIFACT_PUBLISH_FAILURES)[number];

/** Where a public link was copied: the publish popover or the artifact's ⋯ menu. */
export const ARTIFACT_LINK_COPY_SURFACES = ["panel", "menu"] as const;

/** The failure category of a publish that did not go through. */
export function publishFailureReason(result: Exclude<CloudSharePublishResult, { status: "published" }>): ArtifactPublishFailure {
	switch (result.status) {
		case "signed_out":
		case "cancelled":
			return result.status;
		case "limit":
		case "unsupported":
		case "not_owner":
			return "refused";
		case "error":
			return result.failure ?? "other";
		case "changed":
			return "other";
	}
}
