export function sanitizeWorkerMessagePart(value: string): string {
	return value.replace(/[\u0000-\u0008\u000B\u000C\u000E-\u001F\u007F]/g, "");
}
