// The composer treats a staged file as a path it lists under "Attached
// files". A cloud file is staged by uploading it to the session, and its
// "path" is this reference, which the cloud send turns back into the message's
// attachment ids. It names no file on this machine, so nothing may read it as
// a daemon path. Kept free of imports so the draft store can use it.

export const CLOUD_ATTACHMENT_REFERENCE_PREFIX = "ao-cloud-attachment:";

/** Whether a staged path is a cloud attachment reference. */
export function isCloudAttachmentReference(path: string | undefined): boolean {
	return path?.startsWith(CLOUD_ATTACHMENT_REFERENCE_PREFIX) === true;
}
