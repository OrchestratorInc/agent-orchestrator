// Types the cloudShare IPC carries between the renderer and the main process.
// Publishing an artifact as a public share link runs only in the main
// process, which holds the sign-in token. A local file is read inside the
// session's artifacts folder only and uploaded straight to storage; a cloud
// session's file is copied by the control plane inside its bucket. The
// renderer names a file and shows progress; it never sees the file's bytes or
// the token.
//
// There is no second, native confirmation: Publish in the app's share popover
// is the person's decision. Only the app's own window can ask. The bridge
// exists in its main frame alone, never in the frames artifacts render in,
// and agents, the ao CLI, MCP and the daemon have no route to it. Code
// running in the app's own renderer could publish without a click; that is
// the trade-off of dropping the native dialog. What is published is what the
// popover showed: publish carries the inspection's fingerprint and refuses a
// file that changed since.

/**
 * A file to share: one in a local session's artifacts folder, or, with
 * source "cloud", one committed file of a cloud session.
 */
export interface CloudShareFile {
	/** The cloud control plane's base URL (settings.cloudControlPlaneUrl). */
	baseUrl: string;
	source?: "local" | "cloud";
	/** A cloud session's organization. */
	orgId?: string;
	sessionId: string;
	/** The file's path in the session's artifacts, slash-separated. */
	path: string;
	/**
	 * A local session's project; with the session and folder, it names the
	 * document a link serves. A cloud session's file is named by its
	 * organization, session and path instead.
	 */
	projectId: string;
	/** Update this active link instead of publishing a new document. */
	shareId?: string;
	/** The fingerprint of the inspection the person saw; publishing refuses a file that changed since. */
	fingerprint?: string;
}

/** One committed file of a cloud session. */
export interface CloudSessionShareFile extends CloudShareFile {
	source: "cloud";
	orgId: string;
}

/** A public share link, as the control plane lists it. */
export interface CloudShareLink {
	id: string;
	/** The link's ID, the first label of its contentUrl host. */
	slug: string;
	name: string;
	contentUrl: string;
	viewerUrl?: string;
	contentType: string;
	size: number;
	sha256: string;
	createdAt: string;
	updatedAt: string;
	revokedAt?: string;
}

/** The signed-in user's links and what their plan allows. */
export interface CloudShareLinks {
	shares: CloudShareLink[];
	/** The free plan's limit of active links. */
	limit: number;
	used: number;
	/** The user's plan has no link limit. Files have no size limit on any plan. */
	unlimited: boolean;
}

/** What the share popover shows before the person publishes. */
export interface CloudShareInspection {
	name: string;
	size: number;
	/** Kinds of credential the file looks like it holds, such as "AWS access key". */
	secretFindings: string[];
	/** Video is not shared yet. */
	unsupported: boolean;
	/** The link this file was last published to from this computer, if any. */
	shareId?: string;
	/** Names exactly what was scanned: the hash of the file's start, its size and modification time. */
	fingerprint: string;
}

export type CloudSharePublishResult =
	| { status: "published"; share: CloudShareLink; replaced: boolean }
	/** The person cancelled before the upload finished; nothing was published. */
	| { status: "cancelled" }
	/** The file changed since it was inspected; nothing was uploaded. Show this inspection instead. */
	| { status: "changed"; inspection: CloudShareInspection }
	/** Over the free plan's link limit: offer Upgrade. */
	| { status: "limit"; message: string }
	| { status: "unsupported"; message: string }
	| { status: "signed_out" };

/**
 * Publishing progress, sent to the window that asked to publish: preparing
 * the snapshot is the first half of totalBytes, uploading it the second.
 */
export interface CloudShareProgress {
	sessionId: string;
	path: string;
	sentBytes: number;
	totalBytes: number;
}

export const CLOUD_SHARE_PROGRESS_CHANNEL = "cloudShare:progress";
