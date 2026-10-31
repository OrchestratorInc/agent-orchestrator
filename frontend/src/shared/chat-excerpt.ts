/** Provenance for one content-only piece of a mixed transcript selection. */
export interface ChatExcerptSegment {
	kind: "message" | "command";
	sourceId: string;
	revision: number;
	field?: "command" | "output" | "terminalInput" | "explanation";
	text: string;
}

export interface ChatExcerptSource {
	conversationId: string;
	messageId: string;
	revision: number;
	text: string;
	segments?: ChatExcerptSegment[];
}

export const MAX_EXCERPT_BYTES = 24 * 1024;
export const MAX_EXCERPT_SEGMENTS = 32;

/** Renderer-only ownership checks; never serialized into excerpt requests. */
export interface ChatExcerptActivation {
	isCurrent?: () => boolean;
	shouldActivate: () => boolean;
}

export function validExcerptSource(value: ChatExcerptSource): boolean {
	if (
		typeof value.text !== "string" ||
		!value.text.trim() ||
		new TextEncoder().encode(value.text).length > MAX_EXCERPT_BYTES ||
		!Number.isSafeInteger(value.revision) ||
		value.revision < 0
	)
		return false;
	if (value.segments === undefined)
		return typeof value.messageId === "string" && value.messageId.length > 0;
	if (
		value.messageId !== "" ||
		value.revision !== 0 ||
		!Array.isArray(value.segments) ||
		!value.segments.length ||
		value.segments.length > MAX_EXCERPT_SEGMENTS
	)
		return false;
	for (const part of value.segments) {
		if (
			!part ||
			typeof part.sourceId !== "string" ||
			!part.sourceId ||
			!Number.isSafeInteger(part.revision) ||
			part.revision < 0 ||
			typeof part.text !== "string" ||
			!part.text.trim()
		)
			return false;
		if (
			part.kind === "message"
				? part.field !== undefined
				: part.kind !== "command" ||
					!["command", "output", "terminalInput", "explanation"].includes(
						part.field ?? "",
					)
		)
			return false;
	}
	return value.segments.map((part) => part.text).join("\n\n") === value.text;
}

/** Legacy identities retain their original tuple; groups include every source. */
export function excerptSourceIdentity(ref: ChatExcerptSource): string {
	return JSON.stringify(
		ref.segments
			? [
					ref.conversationId,
					ref.segments.map((part) => [
						part.kind,
						part.sourceId,
						part.revision,
						part.field ?? "",
						part.text,
					]),
				]
			: [ref.conversationId, ref.messageId, ref.revision, ref.text],
	);
}

export function excerptRequest<T extends ChatExcerptSource>(ref: T) {
	return {
		conversationId: ref.conversationId,
		messageId: ref.messageId,
		revision: ref.revision,
		text: ref.text,
		...(ref.segments
			? { segments: ref.segments.map((part) => ({ ...part })) }
			: {}),
	};
}

export function cloneExcerptReference<T extends ChatExcerptSource>(ref: T): T {
	return {
		...ref,
		...(ref.segments
			? { segments: ref.segments.map((part) => ({ ...part })) }
			: {}),
	};
}
