// The message the desktop app sends to the artifact's session when its owner
// uses "Send to agent" on a comment thread (design §10). It is capped at the
// file-annotation limit with the same truncation rules: each field is
// whitespace-collapsed and cut with " [truncated]", and the whole message
// with "\n[truncated]". The frame (file, element, note, closing request)
// always fits; when the thread does not, its first comment and as many of the
// latest as fit are kept and the middle ones are counted instead.

import { compactText, limitMessage, MAX_FILE_ANNOTATION_MESSAGE_LENGTH } from "../files/file-annotations";
import { artifactCommentAuthorLabel, type ArtifactComment, type ArtifactCommentThread } from "./types";

export const MAX_ARTIFACT_COMMENT_MESSAGE_LENGTH = MAX_FILE_ANNOTATION_MESSAGE_LENGTH;
/** The Send to agent dialog's note limit; a note this long reaches the agent whole. */
export const MAX_ARTIFACT_COMMENT_NOTE_LENGTH = 2000;

const MAX_PATH_LENGTH = 500;
const MAX_EXCERPT_LENGTH = 120;
const MAX_COMMENT_LENGTH = 1200;

export type ArtifactCommentMessageArtifact = {
	/** The file's path in the session, as the owner sees it. */
	path: string;
	/** The shared link version the thread is on. */
	version: number;
};

function commentLine(comment: ArtifactComment): string {
	const author = compactText(artifactCommentAuthorLabel(comment.author), 80);
	if (comment.deleted) return `- ${author}: (comment deleted)`;
	return `- ${author}: ${compactText(comment.body, MAX_COMMENT_LENGTH) || "(empty)"}`;
}

function omitted(count: number): string {
	return `- … ${count} more comment${count === 1 ? "" : "s"} omitted …`;
}

// How much of the first comment a long thread always keeps, so the latest
// comments can take the rest of the room before the first is shortened.
const MIN_FIRST_COMMENT_LENGTH = 200;
const MIN_LINE_LENGTH = 40;

const lineCost = (line: string) => line.length + 1;

/**
 * Keeps the first comment and the latest ones that fit in `budget`
 * characters. Room for the latest is reserved before the first comment is
 * shortened, and the latest is itself shortened when nothing else fits.
 */
function threadLines(comments: readonly ArtifactComment[], budget: number): string[] {
	const lines = comments.map(commentLine);
	if (lines.reduce((sum, line) => sum + lineCost(line), 0) <= budget) return lines;

	const [first, ...rest] = lines;
	if (rest.length === 0) return [compactText(first, Math.max(MIN_LINE_LENGTH, budget - 1))];

	// The latest comments that fit in `room`, newest last, and the room left.
	const latest = (room: number): { tail: string[]; left: number } => {
		const tail: string[] = [];
		for (let index = rest.length - 1; index >= 0; index -= 1) {
			if (lineCost(rest[index]) > room) break;
			room -= lineCost(rest[index]);
			tail.unshift(rest[index]);
		}
		return { tail, left: room };
	};
	// The first comment's floor is held back; the omission marker only when
	// something is actually omitted (its longest form, so the count fits).
	const roomAfterFloor = budget - Math.min(lineCost(first), MIN_FIRST_COMMENT_LENGTH + 1);
	let { tail, left } = latest(roomAfterFloor);
	if (tail.length < rest.length) ({ tail, left } = latest(roomAfterFloor - lineCost(omitted(rest.length))));
	if (tail.length === 0) {
		// Nothing fits whole: shorten the latest rather than drop it.
		const room = rest.length > 1 ? left : roomAfterFloor;
		tail = [compactText(rest[rest.length - 1], Math.max(MIN_LINE_LENGTH, room - 1))];
	}

	const skipped = rest.length - tail.length;
	const used = tail.reduce((sum, line) => sum + lineCost(line), 0) + (skipped > 0 ? lineCost(omitted(skipped)) : 0);
	const headRoom = budget - used - 1;
	const head = first.length <= headRoom ? first : compactText(first, Math.max(MIN_LINE_LENGTH, headRoom));
	return skipped > 0 ? [head, omitted(skipped), ...tail] : [head, ...tail];
}

export function formatArtifactCommentMessage(
	thread: ArtifactCommentThread,
	artifact: ArtifactCommentMessageArtifact,
	note?: string,
): string {
	const excerpt = compactText(thread.anchor.excerpt, MAX_EXCERPT_LENGTH);
	const path = compactText(artifact.path, MAX_PATH_LENGTH);
	const selector = compactText(thread.anchor.path, MAX_PATH_LENGTH);
	const where = `Where: <${thread.anchor.tag}>${excerpt ? ` "${excerpt}"` : ""}  [selector: ${selector}]`;
	const ownerNote = note === undefined ? "" : compactText(note, MAX_ARTIFACT_COMMENT_NOTE_LENGTH);

	const before = [
		"The owner forwarded a review comment on a shared artifact they published from this session.",
		"",
		`File: ${path}  (shared link version ${artifact.version})`,
		where,
		"",
		"Thread:",
	];
	const after = [
		...(ownerNote ? ["", `Owner's note: ${ownerNote}`] : []),
		"",
		"Update the file to address this, then tell the owner what changed. They will republish it.",
	];
	const frameLength = [...before, ...after].reduce((sum, line) => sum + line.length + 1, 0);
	const threadBody = threadLines(thread.comments, MAX_ARTIFACT_COMMENT_MESSAGE_LENGTH - frameLength);
	return limitMessage([...before, ...threadBody, ...after].join("\n"), MAX_ARTIFACT_COMMENT_MESSAGE_LENGTH);
}
