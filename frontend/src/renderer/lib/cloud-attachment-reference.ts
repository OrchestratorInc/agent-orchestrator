export const CLOUD_ATTACHMENT_REFERENCE_PREFIX = "ao-cloud-attachment:";

export function isCloudAttachmentReference(path: string | undefined): boolean {
	return path?.startsWith(CLOUD_ATTACHMENT_REFERENCE_PREFIX) === true;
}
