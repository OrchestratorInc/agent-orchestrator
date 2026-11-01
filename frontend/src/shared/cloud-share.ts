// Types the cloudShare IPC carries between the renderer and the main process.
// Publishing a local artifact as a public share link runs only in the main
// process: it reads the file (inside the session's artifacts folder only),
// holds the sign-in token, asks the person to confirm in a native dialog, and
// uploads the file straight to storage. The renderer names a file and shows
// progress; it never sees the file's bytes or the token.

/** One file in a local session's artifacts folder. */
export interface CloudShareFile {
	/** The cloud control plane's base URL (settings.cloudControlPlaneUrl). */
	baseUrl: string;
	sessionId: string;
	/** The file's path in the session's artifacts folder, slash-separated. */
	path: string;
	/** The session's project; with the session and folder, it names the document a link serves. */
	projectId: string;
	/** Update this active link instead of publishing a new document. */
	shareId?: string;
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

/** What the share dialog shows before the person confirms. */
export interface CloudShareInspection {
	name: string;
	size: number;
	/** Kinds of credential the file looks like it holds, such as "AWS access key". */
	secretFindings: string[];
	/** Video is not shared yet. */
	unsupported: boolean;
	/** The link this file was last published to from this computer, if any. */
	shareId?: string;
}

export type CloudSharePublishResult =
	| { status: "published"; share: CloudShareLink; replaced: boolean }
	/** The person dismissed the confirmation; nothing was uploaded. */
	| { status: "cancelled" }
	/** Over the free plan's link limit: offer Upgrade. */
	| { status: "limit"; message: string }
	| { status: "unsupported"; message: string }
	| { status: "signed_out" };

/** Upload progress, sent to the window that asked to publish. */
export interface CloudShareProgress {
	sessionId: string;
	path: string;
	sentBytes: number;
	totalBytes: number;
}

export const CLOUD_SHARE_PROGRESS_CHANNEL = "cloudShare:progress";
