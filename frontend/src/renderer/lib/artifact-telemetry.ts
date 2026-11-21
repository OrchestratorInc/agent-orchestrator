import type { CloudSharePublishResult } from "../../shared/cloud-share";

// Closed vocabularies for the artifact publish funnel. Only these enums leave
// the machine: never a file name, path, URL, title, session name, content or
// the people a link is shared with.

/** Where an artifact session was started: the Scratchpad, a project, or AO Cloud. */
export const ARTIFACT_SESSION_SCOPES = ["standalone", "project", "cloud"] as const;

/** Why Publish asked to sign in: signed out when clicked, or a publish came back signed out. */
export const ARTIFACT_SIGN_IN_PROMPT_REASONS = ["signed_out", "session_expired"] as const;

/** Why a publish attempt failed. `refused` is the server refusing the upload: the plan's link limit, a file type not shared yet, or not the session's owner. */
export const ARTIFACT_PUBLISH_FAILURES = ["signed_out", "too_large", "upload_failed", "refused", "other"] as const;
export type ArtifactPublishFailure = (typeof ARTIFACT_PUBLISH_FAILURES)[number];

/** Why the Publish popover opened on a refusal, before any attempt: the plan's link limit, or a file type not shared yet. */
export const ARTIFACT_PUBLISH_BLOCKS = ["limit", "unsupported"] as const;

/** Where a public link was copied: the publish popover or the artifact's ⋯ menu. */
export const ARTIFACT_LINK_COPY_SURFACES = ["panel", "menu"] as const;

/**
 * The event for a publish attempt's result: published, cancelled by the
 * person (not a failure), or failed with its category.
 */
export function publishResultEvent(
	result: CloudSharePublishResult,
	isRepublish: boolean,
): [event: string, properties: { is_republish: boolean; reason?: ArtifactPublishFailure }] {
	switch (result.status) {
		case "published":
			return ["ao.artifacts.published", { is_republish: isRepublish }];
		case "cancelled":
			return ["ao.artifacts.publish_cancelled", { is_republish: isRepublish }];
		case "signed_out":
			return ["ao.artifacts.publish_failed", { is_republish: isRepublish, reason: "signed_out" }];
		case "limit":
		case "unsupported":
		case "not_owner":
			return ["ao.artifacts.publish_failed", { is_republish: isRepublish, reason: "refused" }];
		case "error":
			return ["ao.artifacts.publish_failed", { is_republish: isRepublish, reason: result.failure ?? "other" }];
		case "changed":
			return ["ao.artifacts.publish_failed", { is_republish: isRepublish, reason: "other" }];
	}
}
