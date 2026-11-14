import { useCallback, useEffect, useRef, useState } from "react";
import { chatDraftScopeSessionId } from "../lib/chat-drafts";
import { isCloudAttachmentReference } from "../lib/cloud-attachment-reference";

// Client-side mirror of the backend spawn caps in
// backend/internal/httpd/controllers/sessions.go (maxAttachments /
// maxAttachmentBytes / maxAttachmentsBytes). Enforced here too so the user gets
// inline feedback at paste/drop time instead of a late rejection after submit.
export const MAX_ATTACHMENTS = 8;
export const MAX_ATTACHMENT_BYTES = 50 * 1024 * 1024;
export const MAX_ATTACHMENTS_BYTES = 100 * 1024 * 1024;

const mb = (bytes: number) => Math.round(bytes / (1024 * 1024));

/** A single file staged for a task/orchestrator brief. */
export type FileAttachment = {
	/** Stable id for list keys and removal. */
	id: string;
	/** Browser-reported MIME type (e.g. "image/png", "text/plain"). */
	mimeType: string;
	/** Decoded byte size (from File.size), used to enforce the total-size cap. */
	bytes: number;
	/** File name for display. */
	name: string;
	/** data: URL used to render the thumbnail preview (for images only). */
	dataUrl?: string;
	/** Base64 payload without the "data:...;base64," prefix, for upload. */
	data?: string;
	/** Durable worktree-relative path, present only after daemon staging succeeds. */
	stagedPath?: string;
	/** The picked file, kept in memory only, so a sent message can show it at once. */
	file?: File;
};

/** Attachment payload shape accepted by the spawn API. */
export type FileAttachmentPayload = {
	mimeType: string;
	data: string;
	name?: string;
};

export type FileAttachmentOptions = {
	/** Previously staged descriptors restored for this owning surface. */
	initialAttachments?: FileAttachment[];
	/** Changes when the hook must replace its state with initialAttachments. */
	initialKey?: string;
	/**
	 * Make newly accepted bytes durable before their chips become visible. A
	 * rejection leaves the existing attachment list untouched. The signal
	 * aborts when the person removes every file it prepares while they wait.
	 */
	prepareAttachments?: (attachments: FileAttachment[], signal: AbortSignal) => Promise<FileAttachment[]>;
	/**
	 * Prepare each file on its own, as an upload to AO Cloud is: a file that
	 * fails shows its own reason while the rest attach, and removing one while
	 * it uploads cancels only that upload. Off, a batch attaches all or nothing.
	 */
	prepareEach?: boolean;
	/** Called after an accepted add, removal, or clear. */
	onAttachmentsChange?: (attachments: FileAttachment[]) => void;
	/** The largest file accepted; the daemon's MAX_ATTACHMENT_BYTES when absent (AO Cloud takes less). */
	maxAttachmentBytes?: number;
};

type PendingFileAttachment = Pick<FileAttachment, "id" | "name">;

type SharedAttachmentUpdate = {
	pending: number;
	pendingAttachments: PendingFileAttachment[];
	attachments?: FileAttachment[];
	error?: string | null;
};

type SharedAttachmentEntry = {
	pending: Map<symbol, PendingFileAttachment[]>;
	generation: number;
	listeners: Map<symbol, (update: SharedAttachmentUpdate) => void>;
	attachments?: FileAttachment[];
	error?: string | null;
};

type SharedAttachmentWork = { token: symbol; generation: number };

// One controller per file still being read or prepared, by its pending id.
// Removing the file while it waits aborts it; so does discarding the work.
const pendingFileControllers = new Map<string, AbortController>();

function abortPendingFiles(files: Iterable<PendingFileAttachment>): void {
	for (const file of files) pendingFileControllers.get(file.id)?.abort();
}

/** A signal that aborts once every one of these has. */
function whenAllAborted(signals: AbortSignal[]): AbortSignal {
	const controller = new AbortController();
	const check = () => {
		if (signals.every((signal) => signal.aborted)) controller.abort();
	};
	for (const signal of signals) signal.addEventListener("abort", check, { once: true });
	check();
	return controller.signal;
}

/**
 * Take one file off a shared work's pending list. A file that was cancelled
 * and leaves its work with none takes the work off too, so nothing waits on it
 * and whatever it still finishes is not attached.
 */
function removeSharedPendingFile(key: string, id: string, cancelled: boolean): void {
	const entry = sharedAttachmentEntries.get(key);
	if (!entry) return;
	for (const [token, files] of entry.pending) {
		if (!files.some((file) => file.id === id)) continue;
		const rest = files.filter((file) => file.id !== id);
		if (cancelled && rest.length === 0) entry.pending.delete(token);
		else entry.pending.set(token, rest);
		notifySharedAttachmentEntry(key);
		return;
	}
}

/** The reason one file could not be prepared, as its preparer said it. */
function preparationFailure(error: unknown): string {
	const message = error instanceof Error ? error.message.trim() : "";
	return message || "The file couldn’t be uploaded.";
}

type CapturedPendingFileAttachmentEntry = {
	key: string;
	generation: number;
	tokens: readonly symbol[];
};

const pendingFileAttachmentCaptureEntries = Symbol("pending-file-attachment-capture");

/** Opaque handle for attachment work that was pending at a user-approved boundary. */
export type PendingFileAttachmentCapture = {
	readonly [pendingFileAttachmentCaptureEntries]: readonly CapturedPendingFileAttachmentEntry[];
};

function sharedAttachmentSnapshot(attachments: FileAttachment[]): FileAttachment[] {
	return attachments.map(({ id, mimeType, bytes, name, stagedPath, data, dataUrl }) => ({
		id,
		mimeType,
		bytes,
		name,
		// Unstaged files have no durable copy. Keep their bytes in memory for retry.
		// A cloud file's only local preview is its bytes, so it keeps its dataUrl.
		...(stagedPath ? { stagedPath, ...(isCloudAttachmentReference(stagedPath) ? { dataUrl } : {}) } : { data, dataUrl }),
	}));
}

// Staging belongs to the AO session, not to one React mount. A controller or
// surface remount can happen while the daemon is writing bytes; keeping this
// tiny registry lets the replacement hook remain pending and receive the staged
// descriptors instead of restoring storage before the write and missing them.
const sharedAttachmentEntries = new Map<string, SharedAttachmentEntry>();

function sharedEntry(key: string): SharedAttachmentEntry {
	let entry = sharedAttachmentEntries.get(key);
	if (!entry) {
		entry = { pending: new Map(), generation: 0, listeners: new Map() };
		sharedAttachmentEntries.set(key, entry);
	}
	return entry;
}

function notifySharedAttachmentEntry(
	key: string,
	update: Omit<SharedAttachmentUpdate, "pending" | "pendingAttachments"> = {},
	originToken?: symbol,
): void {
	const entry = sharedAttachmentEntries.get(key);
	if (!entry) return;
	if (update.attachments !== undefined) entry.attachments = update.attachments;
	if (update.error !== undefined) entry.error = update.error;
	const notification = { pending: entry.pending.size, pendingAttachments: [...entry.pending.values()].flat(), ...update };
	for (const [token, listener] of entry.listeners) {
		if (token !== originToken) listener(notification);
	}
}

function beginSharedAttachmentWork(key: string, files: PendingFileAttachment[]): SharedAttachmentWork {
	const entry = sharedEntry(key);
	const work = { token: Symbol("chat-attachment-work"), generation: entry.generation };
	entry.pending.set(work.token, files);
	notifySharedAttachmentEntry(key);
	return work;
}

function endSharedAttachmentWork(key: string, token: symbol): void {
	const entry = sharedAttachmentEntries.get(key);
	if (!entry) return;
	entry.pending.delete(token);
	notifySharedAttachmentEntry(key);
	if (
		entry.pending.size === 0 &&
		entry.listeners.size === 0 &&
		(entry.attachments?.length ?? 0) === 0
	) {
		sharedAttachmentEntries.delete(key);
	}
}

function subscribeSharedAttachmentWork(
	key: string,
	token: symbol,
	listener: (update: SharedAttachmentUpdate) => void,
): () => void {
	const entry = sharedEntry(key);
	entry.listeners.set(token, listener);
	listener({
		pending: entry.pending.size,
		pendingAttachments: [...entry.pending.values()].flat(),
		...(entry.attachments !== undefined ? { attachments: entry.attachments } : {}),
		...(entry.error !== undefined ? { error: entry.error } : {}),
	});
	return () => {
		entry.listeners.delete(token);
		if (
			entry.pending.size === 0 &&
			entry.listeners.size === 0 &&
			(entry.attachments?.length ?? 0) === 0
		) {
			sharedAttachmentEntries.delete(key);
		}
	};
}

function sharedAttachmentPending(key: string | undefined): boolean {
	return Boolean(key && (sharedAttachmentEntries.get(key)?.pending.size ?? 0) > 0);
}

function sharedAttachmentWorkIsCurrent(key: string, work: SharedAttachmentWork): boolean {
	const entry = sharedAttachmentEntries.get(key);
	return Boolean(entry && entry.generation === work.generation && entry.pending.has(work.token));
}

export function discardPendingFileAttachments(key: string): void {
	const entry = sharedAttachmentEntries.get(key);
	if (!entry) return;
	entry.generation += 1;
	for (const files of entry.pending.values()) abortPendingFiles(files);
	entry.pending.clear();
	notifySharedAttachmentEntry(key, { error: null });
}

function attachmentKeyBelongsToSession(key: string, sessionId: string): boolean {
	return key === sessionId || chatDraftScopeSessionId(key) === sessionId;
}

/** Capture only the work that is pending when the user confirms leaving Chat. */
export function capturePendingFileAttachmentsForSession(
	sessionId: string,
): PendingFileAttachmentCapture {
	const entries: CapturedPendingFileAttachmentEntry[] = [];
	for (const [key, entry] of sharedAttachmentEntries) {
		if (!attachmentKeyBelongsToSession(key, sessionId) || entry.pending.size === 0) continue;
		entries.push({ key, generation: entry.generation, tokens: [...entry.pending.keys()] });
	}
	return { [pendingFileAttachmentCaptureEntries]: entries };
}

/**
 * Cancel exactly the pending work represented by a prior confirmation. Work
 * begun afterward remains current and can finish into the recoverable draft.
 */
export function discardCapturedPendingFileAttachments(
	capture: PendingFileAttachmentCapture,
): void {
	for (const captured of capture[pendingFileAttachmentCaptureEntries]) {
		const entry = sharedAttachmentEntries.get(captured.key);
		if (!entry || entry.generation !== captured.generation) continue;
		let changed = false;
		for (const token of captured.tokens) {
			abortPendingFiles(entry.pending.get(token) ?? []);
			changed = entry.pending.delete(token) || changed;
		}
		if (!changed) continue;
		notifySharedAttachmentEntry(captured.key);
		if (
			entry.pending.size === 0 &&
			entry.listeners.size === 0 &&
			(entry.attachments?.length ?? 0) === 0
		) {
			sharedAttachmentEntries.delete(captured.key);
		}
	}
}

/** Cancel every in-flight renderer generation owned by one logical AO session. */
export function discardPendingFileAttachmentsForSession(sessionId: string): void {
	for (const key of [...sharedAttachmentEntries.keys()]) {
		if (attachmentKeyBelongsToSession(key, sessionId)) discardPendingFileAttachments(key);
	}
}

/**
 * Remove only renderer descriptors/work for a deleted session incarnation. The
 * durable worktree bytes are intentionally outside this registry and untouched.
 */
export function purgeFileAttachmentsForSession(sessionId: string): void {
	for (const key of [...sharedAttachmentEntries.keys()]) {
		if (attachmentKeyBelongsToSession(key, sessionId)) purgeFileAttachments(key);
	}
}

/** Retire one completed owner without touching other drafts or staged bytes. */
export function purgeFileAttachments(key: string): void {
	const entry = sharedAttachmentEntries.get(key);
	if (!entry) return;
	entry.generation += 1;
	for (const files of entry.pending.values()) abortPendingFiles(files);
	entry.pending.clear();
	notifySharedAttachmentEntry(key, { attachments: [], error: null });
	if (entry.listeners.size === 0) sharedAttachmentEntries.delete(key);
}

// Client-side mirror of the backend image-preview allowlist. Non-image files can
// still be attached; they render with the generic file icon.
const SUPPORTED_IMAGE_TYPES = new Set(["image/png", "image/jpeg", "image/jpg", "image/gif", "image/webp", "image/bmp"]);

export const isSupportedImageAttachment = (type: string) =>
	SUPPORTED_IMAGE_TYPES.has(type.toLowerCase().trim());

const readFileAsBase64 = (file: File): Promise<{ dataUrl: string; data: string }> =>
	new Promise((resolve, reject) => {
		const reader = new FileReader();
		reader.onerror = () => reject(reader.error ?? new Error("Failed to read file"));
		reader.onload = () => {
			const dataUrl = typeof reader.result === "string" ? reader.result : "";
			const comma = dataUrl.indexOf(",");
			if (!dataUrl || comma < 0) {
				reject(new Error("Unreadable file data"));
				return;
			}
			resolve({
				dataUrl,
				data: dataUrl.slice(comma + 1),
			});
		};
		reader.readAsDataURL(file);
	});

/**
 * useFileAttachments stages files pasted, dropped, or picked into a brief and
 * exposes them as upload-ready payloads. Count and size caps (mirroring the backend)
 * are enforced here, and rejections / unreadable files surface through `error` for
 * inline feedback.
 *
 * Supports all file types except SVG (blocked for security). Images show thumbnails,
 * other files show a generic file icon.
 */
export function useFileAttachments(options: FileAttachmentOptions = {}) {
	const {
		initialAttachments = [],
		initialKey,
		prepareAttachments,
		prepareEach = false,
		onAttachmentsChange,
		maxAttachmentBytes = MAX_ATTACHMENT_BYTES,
	} = options;
	const [attachments, setAttachments] = useState<FileAttachment[]>(() => initialAttachments);
	const [error, setError] = useState<string | null>(null);
	const [preparing, setPreparing] = useState(() => sharedAttachmentPending(initialKey));
	const [pendingAttachments, setPendingAttachments] = useState<PendingFileAttachment[]>(() =>
		initialKey ? [...(sharedAttachmentEntries.get(initialKey)?.pending.values() ?? [])].flat() : []);
	const attachmentsRef = useRef<FileAttachment[]>(initialAttachments);
	const initialKeyRef = useRef(initialKey);
	const listenerTokenRef = useRef(Symbol("file-attachment-listener"));
	const pendingReadsRef = useRef<Set<Promise<unknown>>>(new Set());
	const addQueueRef = useRef<Promise<unknown>>(Promise.resolve());
	const queuedAddsRef = useRef(0);
	const generationRef = useRef(0);

	useEffect(() => {
		if (initialKeyRef.current === initialKey) return;
		generationRef.current++;
		initialKeyRef.current = initialKey;
		attachmentsRef.current = initialAttachments;
		setAttachments(initialAttachments);
		setError(null);
		setPreparing(sharedAttachmentPending(initialKey));
		setPendingAttachments(initialKey ? [...(sharedAttachmentEntries.get(initialKey)?.pending.values() ?? [])].flat() : []);
	}, [initialAttachments, initialKey]);

	useEffect(() => {
		if (!initialKey) return;
		return subscribeSharedAttachmentWork(initialKey, listenerTokenRef.current, (update) => {
			setPendingAttachments((current) => current.length === update.pendingAttachments.length &&
				current.every((file, index) => file.id === update.pendingAttachments[index]?.id)
				? current : update.pendingAttachments);
			if (update.attachments) {
				attachmentsRef.current = update.attachments;
				setAttachments(update.attachments);
				onAttachmentsChange?.(update.attachments);
			}
			if (update.error !== undefined) setError(update.error);
			setPreparing(update.pending > 0);
		});
	}, [initialKey, onAttachmentsChange]);

	const processFiles = useCallback(async (batch: { file: File; id: string }[], generation: number, sharedWork?: SharedAttachmentWork): Promise<FileAttachment[]> => {
		if (generationRef.current !== generation) return [];
		if (initialKey && sharedWork && !sharedAttachmentWorkIsCurrent(initialKey, sharedWork)) return [];
		// A file removed while it waited is not read, staged or attached.
		const cancelled = (id: string) => pendingFileControllers.get(id)?.signal.aborted !== false;
		const pendingIds = new Map(batch.map(({ file, id }) => [file, id]));
		const files = batch.flatMap(({ file, id }) => (cancelled(id) ? [] : [file]));
		// Filter out directories - they have type "" and size 0 in most browsers
		const validFiles = files.filter((file) => {
			// Exclude directories (they typically have no type and size 0)
			// Also exclude items that might be folders based on common patterns
			if (file.type === "" && file.name.endsWith("/")) return false;
			// Some browsers report directories as size 0 with empty type
			if (file.type === "" && file.size === 0) return false;
			return true;
		});

		if (validFiles.length === 0) return [];

		const errors = new Set<string>();
		// Block SVG files for security (active content)
		const blockedFiles = validFiles.filter(
			(file) => file.type.toLowerCase().trim() === "image/svg+xml",
		);
		if (blockedFiles.length > 0) {
			errors.add("SVG files are not supported for security reasons.");
		}
		const valid = validFiles.filter(
			(file) => file.type.toLowerCase().trim() !== "image/svg+xml",
		);

		// Check metadata before each read so rejected files never enter JS memory.
		// Reserve budget only after a successful read: a later small file can still
		// fit if an earlier read fails.
		const pendingReads = (async () => {
			const results: Array<{ file: File; result: { dataUrl: string; data: string } }> = [];
			let freshBytes = 0;
			for (const file of valid) {
				if (generationRef.current !== generation ||
					(initialKey && sharedWork && !sharedAttachmentWorkIsCurrent(initialKey, sharedWork))) break;
				if (file.size > maxAttachmentBytes) {
					errors.add(`Each file must be under ${mb(maxAttachmentBytes)} MB.`);
					continue;
				}
				if (attachmentsRef.current.length + results.length >= MAX_ATTACHMENTS) {
					errors.add(`You can attach up to ${MAX_ATTACHMENTS} files.`);
					break;
				}
				const total = attachmentsRef.current.reduce((sum, attachment) => sum + attachment.bytes, freshBytes);
				if (total + file.size > MAX_ATTACHMENTS_BYTES) {
					errors.add(`Attachments must total under ${mb(MAX_ATTACHMENTS_BYTES)} MB.`);
					continue;
				}
				const result = await readFileAsBase64(file).catch(() => null);
				if (!result) {
					errors.add(`Some files couldn't be read and were skipped.`);
					continue;
				}
				results.push({ file, result });
				freshBytes += file.size;
			}
			return results;
		})();
		pendingReadsRef.current.add(pendingReads);
		const results = await pendingReads;
		pendingReadsRef.current.delete(pendingReads);
		if (generationRef.current !== generation) return [];
		if (initialKey && sharedWork && !sharedAttachmentWorkIsCurrent(initialKey, sharedWork)) return [];

		const fresh: FileAttachment[] = [];
		for (const { file, result } of results) {
			const isImage = file.type.startsWith("image/") && isSupportedImageAttachment(file.type);
			// The file keeps its pending id, which its chip and cancellation use.
			fresh.push({
				id: pendingIds.get(file) ?? crypto.randomUUID(),
				mimeType: file.type || "application/octet-stream",
				bytes: file.size,
				name: file.name,
				dataUrl: isImage ? result.dataUrl : undefined,
				data: result.data,
				file,
			});
		}

		const accepted = [...attachmentsRef.current];
		const acceptedFresh: FileAttachment[] = [];
		let total = accepted.reduce((sum, a) => sum + a.bytes, 0);
		for (const a of fresh) {
			if (accepted.length >= MAX_ATTACHMENTS) {
				errors.add(`You can attach up to ${MAX_ATTACHMENTS} files.`);
				break;
			}
			if (total + a.bytes > MAX_ATTACHMENTS_BYTES) {
				// Only this file is refused: the remaining budget cannot absorb it,
				// but a later file in the same batch still can. Aborting here (break)
				// would silently drop every smaller file staged after it.
				errors.add(`Attachments must total under ${mb(MAX_ATTACHMENTS_BYTES)} MB.`);
				continue;
			}
			accepted.push(a);
			acceptedFresh.push(a);
			total += a.bytes;
		}
		let added: FileAttachment[] = [];
		// Append to the live list, read after any wait: a removal that happened
		// while bytes were being staged must not resurrect an older snapshot.
		const commit = (prepared: FileAttachment[]) => {
			const next = [...attachmentsRef.current, ...prepared];
			added = [...added, ...prepared];
			attachmentsRef.current = next;
			setAttachments(next);
			onAttachmentsChange?.(next);
			if (initialKey) {
				notifySharedAttachmentEntry(
					initialKey,
					{ attachments: sharedAttachmentSnapshot(next) },
					listenerTokenRef.current,
				);
			}
		};
		const current = () => generationRef.current === generation &&
			!(initialKey && sharedWork && !sharedAttachmentWorkIsCurrent(initialKey, sharedWork));
		if (acceptedFresh.length > 0 && prepareAttachments && prepareEach) {
			// Each file on its own: one attaches as soon as it is ready, one that
			// fails says why, and one removed meanwhile is dropped.
			const failures = new Map<string, string[]>();
			await Promise.all(acceptedFresh.map(async (attachment) => {
				const signal = pendingFileControllers.get(attachment.id)?.signal;
				if (!signal || signal.aborted) return;
				try {
					const [prepared] = await prepareAttachments([attachment], signal);
					if (!prepared) throw new Error("Attachment staging returned an incomplete result");
					if (!current() || signal.aborted) return;
					commit([prepared]);
					if (initialKey) removeSharedPendingFile(initialKey, attachment.id, false);
					else setPendingAttachments((pending) => pending.filter((file) => file.id !== attachment.id));
				} catch (error) {
					if (!current() || signal.aborted) return;
					const reason = preparationFailure(error);
					failures.set(reason, [...(failures.get(reason) ?? []), attachment.name]);
				}
			}));
			if (generationRef.current !== generation) return [];
			for (const [reason, names] of failures) errors.add(`Couldn’t attach ${names.join(", ")}: ${reason}`);
		} else if (acceptedFresh.length > 0) {
			let prepared = acceptedFresh;
			if (prepareAttachments) {
				try {
					// Removing a file waits for its batch and drops it; removing them
					// all lets the preparer stop.
					const signals = acceptedFresh.flatMap((attachment) => pendingFileControllers.get(attachment.id)?.signal ?? []);
					prepared = await prepareAttachments(acceptedFresh, whenAllAborted(signals));
					if (generationRef.current !== generation) return [];
					if (initialKey && sharedWork && !sharedAttachmentWorkIsCurrent(initialKey, sharedWork)) return [];
					if (prepared.length !== acceptedFresh.length) {
						throw new Error("Attachment staging returned an incomplete result");
					}
				} catch {
					if (generationRef.current !== generation) return [];
					if (initialKey && sharedWork && !sharedAttachmentWorkIsCurrent(initialKey, sharedWork)) return [];
					errors.add("Files couldn’t be saved. Nothing was attached.");
					const message = Array.from(errors).join(" ");
					setError(message);
					if (initialKey) notifySharedAttachmentEntry(initialKey, { error: message });
					return [];
				}
			}
			// The preparer answers in order, one for each file it was given.
			const kept = prepared.filter((_, index) => !cancelled(acceptedFresh[index]!.id));
			if (kept.length > 0) commit(kept);
		}
		const nextError = errors.size > 0 ? Array.from(errors).join(" ") : null;
		setError(nextError);
		if (initialKey) notifySharedAttachmentEntry(initialKey, { error: nextError });
		return added;
	}, [initialKey, maxAttachmentBytes, onAttachmentsChange, prepareAttachments, prepareEach]);

	/** Resolves with the attachments this batch actually added, staged when staging is on. */
	const addFiles = useCallback((files: Iterable<File>): Promise<FileAttachment[]> => {
		// Serialize batches. Two paste/drop events can arrive before React publishes
		// `preparing`; processing both against the same attachment snapshot could
		// otherwise exceed count/byte caps or overwrite one batch with the other.
		const batch = Array.from(files);
		if (batch.length === 0) return Promise.resolve([]);
		const sharedKey = initialKey;
		const generation = generationRef.current;
		const pending = batch.map((file) => ({ id: crypto.randomUUID(), name: file.name }));
		for (const file of pending) pendingFileControllers.set(file.id, new AbortController());
		const entries = batch.map((file, index) => ({ file, id: pending[index]!.id }));
		const sharedWork = sharedKey ? beginSharedAttachmentWork(sharedKey, pending) : undefined;
		if (!sharedKey) setPendingAttachments((current) => [...current, ...pending]);
		queuedAddsRef.current += 1;
		setPreparing(true);
		// Preserve the hook's existing contract: the first FileReader starts in the
		// same turn as addFiles. Only a later overlapping batch needs to wait for the
		// current queue, otherwise callers cannot observe the pending read immediately.
		const run =
			queuedAddsRef.current === 1
				? processFiles(entries, generation, sharedWork)
				: addQueueRef.current.then(() => processFiles(entries, generation, sharedWork));
		const settled = run
			.catch((): FileAttachment[] => {
				setError("Some files couldn’t be prepared and were skipped.");
				return [];
			})
			.finally(() => {
				for (const file of pending) pendingFileControllers.delete(file.id);
				queuedAddsRef.current = Math.max(0, queuedAddsRef.current - 1);
				if (sharedKey && sharedWork) endSharedAttachmentWork(sharedKey, sharedWork.token);
				else {
					setPendingAttachments((current) => current.filter((file) => !pending.some((item) => item.id === file.id)));
					if (queuedAddsRef.current === 0) setPreparing(false);
				}
			});
		addQueueRef.current = settled;
		return settled;
	}, [initialKey, processFiles]);

	/**
	 * Remove a file that is still being read or uploaded: its upload is
	 * cancelled, its chip goes at once, and nothing waits on it any more.
	 */
	const cancelPending = useCallback((id: string) => {
		pendingFileControllers.get(id)?.abort();
		if (initialKey) removeSharedPendingFile(initialKey, id, true);
		else setPendingAttachments((current) => current.filter((file) => file.id !== id));
	}, [initialKey]);

	const remove = useCallback((id: string) => {
		const next = attachmentsRef.current.filter((a) => a.id !== id);
		attachmentsRef.current = next;
		setAttachments(next);
		onAttachmentsChange?.(next);
		if (initialKey) {
			notifySharedAttachmentEntry(
				initialKey,
				{ attachments: sharedAttachmentSnapshot(next), error: null },
				listenerTokenRef.current,
			);
		}
		setError(null);
	}, [initialKey, onAttachmentsChange]);

	const clear = useCallback(() => {
		generationRef.current++;
		pendingReadsRef.current.clear();
		attachmentsRef.current = [];
		setAttachments([]);
		onAttachmentsChange?.([]);
		if (initialKey) {
			notifySharedAttachmentEntry(
				initialKey,
				{ attachments: [], error: null },
				listenerTokenRef.current,
			);
		}
		setError(null);
	}, [initialKey, onAttachmentsChange]);

	const reconcilePersistedAttachments = useCallback((persisted: FileAttachment[]) => {
		// A hidden React Activity renders before its effects subscribe. By the time it
		// reconnects, another same-scope surface may have accepted and cleared the
		// durable draft. Prefer a live shared descriptor snapshot when one exists (it
		// owns pending work and failed-persistence recovery); otherwise re-seed from
		// storage at the commit boundary instead of reviving render-time descriptors.
		const shared = initialKey
			? sharedAttachmentEntries.get(initialKey)?.attachments
			: undefined;
		const next = shared ?? persisted;
		attachmentsRef.current = next;
		setAttachments(next);
	}, [initialKey]);

	const toPayload = useCallback(
		(): FileAttachmentPayload[] =>
			attachments.flatMap(({ mimeType, data }) =>
				data ? [{ mimeType, data }] : [],
			),
		[attachments],
	);

	const toSettledPayload = useCallback(async (): Promise<FileAttachmentPayload[]> => {
		while (queuedAddsRef.current > 0) {
			const queued = addQueueRef.current;
			await queued;
			if (queued === addQueueRef.current && queuedAddsRef.current === 0) break;
		}
		while (pendingReadsRef.current.size > 0) {
			await Promise.allSettled(Array.from(pendingReadsRef.current));
		}
		return attachmentsRef.current.flatMap(({ mimeType, data }) =>
			data ? [{ mimeType, data }] : [],
		);
	}, []);
	// Read from the same ref as toSettledPayload so a submit resumed after FileReader
	// completion can cache staged paths without waiting for another React render.
	const attachmentSignature = useCallback(
		() => attachmentsRef.current.map((attachment) => attachment.id).join(":"),
		[],
	);
	const hasPendingReads = useCallback(
		() => pendingReadsRef.current.size > 0 || sharedAttachmentPending(initialKey),
		[initialKey],
	);

	return {
		attachments,
		pendingAttachments,
		error,
		preparing,
		addFiles,
		remove,
		cancelPending,
		clear,
		reconcilePersistedAttachments,
		getAttachments: () => attachmentsRef.current,
		toPayload,
		toSettledPayload,
		attachmentSignature,
		hasPendingReads,
	};
}
