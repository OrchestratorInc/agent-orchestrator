import { renderHook, waitFor } from "@testing-library/react";
import { act } from "react";
import { describe, expect, it, vi } from "vitest";

import {
	capturePendingFileAttachmentsForSession,
	discardCapturedPendingFileAttachments,
	discardPendingFileAttachments,
	discardPendingFileAttachmentsForSession,
	MAX_ATTACHMENTS,
	purgeFileAttachmentsForSession,
	useFileAttachments,
	type FileAttachment,
} from "./useFileAttachments";
import { chatDraftScopeKey } from "../lib/chat-drafts";
import { INLINE_ATTACHMENT_BYTES } from "../lib/attachment-uploads";

const file = (name: string, bytes = 8, type = "text/plain") =>
	new File([new Uint8Array(bytes).fill(1)], name, { type });

const mb = 1024 * 1024;

describe("useFileAttachments", () => {
	it("stages a supported file", async () => {
		const { result } = renderHook(() => useFileAttachments());
		await act(async () => {
			await result.current.addFiles([file("notes.txt")]);
		});
		expect(result.current.attachments).toHaveLength(1);
		expect(result.current.attachments[0]?.mimeType).toBe("text/plain");
		expect(result.current.attachments[0]?.name).toBe("notes.txt");
		expect(result.current.error).toBeNull();
	});

	it("makes pending previews only for the images that can still be attached", async () => {
		const createObjectURL = vi.fn((blob: Blob) => `blob:${(blob as File).name}`);
		const original = { createObjectURL: URL.createObjectURL, revokeObjectURL: URL.revokeObjectURL };
		URL.createObjectURL = createObjectURL;
		URL.revokeObjectURL = vi.fn();
		try {
			let release!: () => void;
			const prepareAttachments = vi.fn((attachments: FileAttachment[]) =>
				new Promise<FileAttachment[]>((resolve) => { release = () => resolve(attachments); }));
			const { result } = renderHook(() => useFileAttachments({ prepareAttachments }));
			const drop = Array.from({ length: MAX_ATTACHMENTS + 2 }, (_, index) => file(`shot-${index}.png`, 8, "image/png"));
			let added!: Promise<FileAttachment[]>;
			act(() => { added = result.current.addFiles(drop); });
			// The two files past the limit are refused, so they load no picture.
			expect(createObjectURL.mock.calls.map(([blob]) => (blob as File).name)).toEqual(drop.slice(0, MAX_ATTACHMENTS).map((f) => f.name));
			// A second drop while the first is still uploading finds no free slot either.
			let again!: Promise<FileAttachment[]>;
			act(() => { again = result.current.addFiles([file("late.png", 8, "image/png")]); });
			expect(createObjectURL).toHaveBeenCalledTimes(MAX_ATTACHMENTS);
			await waitFor(() => expect(prepareAttachments).toHaveBeenCalled());
			await act(async () => { release(); await added; await again; });
		} finally {
			URL.createObjectURL = original.createObjectURL;
			URL.revokeObjectURL = original.revokeObjectURL;
		}
	});

	it("keeps the whole read-and-stage window pending and serializes concurrent batches", async () => {
		const releases: Array<() => void> = [];
		let activePreparations = 0;
		let maxActivePreparations = 0;
		const prepareAttachments = vi.fn(
			(attachments: FileAttachment[]) =>
				new Promise<FileAttachment[]>((resolve) => {
					activePreparations += 1;
					maxActivePreparations = Math.max(maxActivePreparations, activePreparations);
					releases.push(() => {
						activePreparations -= 1;
						resolve(
							attachments.map((attachment) => ({
								...attachment,
								stagedPath: `.ao/attachments/${attachment.name}`,
							})),
						);
					});
				}),
		);
		const { result } = renderHook(() => useFileAttachments({ prepareAttachments }));
		let first!: Promise<unknown>;
		let second!: Promise<unknown>;
		act(() => {
			first = result.current.addFiles([file("first.txt")]);
			second = result.current.addFiles([file("second.txt")]);
		});

		expect(result.current.preparing).toBe(true);
		expect(result.current.attachments).toHaveLength(0);
		await waitFor(() => expect(prepareAttachments).toHaveBeenCalledTimes(1));
		await act(async () => releases.shift()?.());
		await waitFor(() => expect(prepareAttachments).toHaveBeenCalledTimes(2));
		expect(result.current.preparing).toBe(true);
		await act(async () => {
			releases.shift()?.();
			await Promise.all([first, second]);
		});

		expect(maxActivePreparations).toBe(1);
		expect(result.current.preparing).toBe(false);
		expect(result.current.attachments.map((attachment) => attachment.name)).toEqual([
			"first.txt",
			"second.txt",
		]);
	});

	it("waits for every rapidly queued batch before returning the settled payload", async () => {
		const releases: Array<() => void> = [];
		const prepareAttachments = vi.fn(
			(attachments: FileAttachment[]) =>
				new Promise<FileAttachment[]>((resolve) => {
					releases.push(() => resolve(attachments));
				}),
		);
		const { result } = renderHook(() => useFileAttachments({ prepareAttachments }));
		let first!: Promise<unknown>;
		let second!: Promise<unknown>;
		let settled: Awaited<ReturnType<typeof result.current.toSettledPayload>> | undefined;
		act(() => {
			first = result.current.addFiles([file("first.txt")]);
			second = result.current.addFiles([file("second.txt")]);
			void result.current.toSettledPayload().then((payload) => {
				settled = payload;
			});
		});

		await waitFor(() => expect(prepareAttachments).toHaveBeenCalledTimes(1));
		await act(async () => releases.shift()?.());
		await waitFor(() => expect(prepareAttachments).toHaveBeenCalledTimes(2));
		expect(settled).toBeUndefined();

		await act(async () => {
			releases.shift()?.();
			await Promise.all([first, second]);
		});
		await waitFor(() => expect(settled).toHaveLength(2));
		expect(settled?.every((attachment) => attachment.mimeType === "text/plain")).toBe(true);
	});

	it("does not resurrect pending attachments after their session is explicitly discarded", async () => {
		const sessionId = "discard-pending-attachments";
		let finishStaging!: (attachments: FileAttachment[]) => void;
		const prepareAttachments = vi.fn(
			() =>
				new Promise<FileAttachment[]>((resolve) => {
					finishStaging = resolve;
				}),
		);
		const first = renderHook(() =>
			useFileAttachments({ initialKey: sessionId, prepareAttachments }),
		);
		let pending!: Promise<unknown>;
		act(() => {
			pending = first.result.current.addFiles([file("discard-me.txt")]);
		});
		await waitFor(() => expect(prepareAttachments).toHaveBeenCalledTimes(1));

		act(() => discardPendingFileAttachments(sessionId));
		await act(async () => {
			finishStaging([
				{
					id: "discarded",
					mimeType: "text/plain",
					bytes: 8,
					name: "discard-me.txt",
					data: "AQ==",
					stagedPath: ".ao/attachments/discard-me.txt",
				},
			]);
			await pending;
		});
		expect(first.result.current.attachments).toEqual([]);
		expect(first.result.current.preparing).toBe(false);
		first.unmount();

		const replacement = renderHook(() => useFileAttachments({ initialKey: sessionId }));
		expect(replacement.result.current.attachments).toEqual([]);
		expect(replacement.result.current.preparing).toBe(false);
	});

	it("ignores a discarded completion that resolves after a replacement attachment", async () => {
		const sessionId = "discard-out-of-order-attachments";
		const firstKey = chatDraftScopeKey({
			sessionId,
			incarnation: "2026-08-25T09:00:00.000Z",
		});
		const replacementKey = chatDraftScopeKey({
			sessionId,
			incarnation: "2026-08-26T09:00:00.000Z",
		});
		let finishOld!: (attachments: FileAttachment[]) => void;
		const first = renderHook(() =>
			useFileAttachments({
				initialKey: firstKey,
				prepareAttachments: () =>
					new Promise<FileAttachment[]>((resolve) => {
						finishOld = resolve;
					}),
			}),
		);
		let oldPending!: Promise<unknown>;
		act(() => {
			oldPending = first.result.current.addFiles([file("old.txt")]);
		});
		await waitFor(() => expect(first.result.current.preparing).toBe(true));
		await waitFor(() => expect(finishOld).toBeTypeOf("function"));
		act(() => purgeFileAttachmentsForSession(sessionId));
		first.unmount();

		const replacement = renderHook(() =>
			useFileAttachments({
				initialKey: replacementKey,
				prepareAttachments: async (attachments) =>
					attachments.map((attachment) => ({
						...attachment,
						stagedPath: `.ao/attachments/${attachment.name}`,
					})),
			}),
		);
		await act(async () => {
			await replacement.result.current.addFiles([file("new.txt")]);
		});
		expect(replacement.result.current.attachments.map((attachment) => attachment.name)).toEqual([
			"new.txt",
		]);

		await act(async () => {
			finishOld([
				{
					id: "old",
					mimeType: "text/plain",
					bytes: 8,
					name: "old.txt",
					stagedPath: ".ao/attachments/old.txt",
				},
			]);
			await oldPending;
		});
		expect(replacement.result.current.attachments.map((attachment) => attachment.name)).toEqual([
			"new.txt",
		]);
	});

	it("cancels pending work for every incarnation of one logical session", async () => {
		const sessionId = "discard-all-session-incarnations";
		const releases = new Map<string, (attachments: FileAttachment[]) => void>();
		const hook = (key: string, name: string) =>
			renderHook(() =>
				useFileAttachments({
					initialKey: key,
					prepareAttachments: () =>
						new Promise<FileAttachment[]>((resolve) => releases.set(name, resolve)),
				}),
			);
		const first = hook(
			chatDraftScopeKey({ sessionId, incarnation: "2026-08-25T09:00:00.000Z" }),
			"first",
		);
		const replacement = hook(
			chatDraftScopeKey({ sessionId, incarnation: "2026-08-26T09:00:00.000Z" }),
			"replacement",
		);
		const other = hook(
			chatDraftScopeKey({ sessionId: "other-session", incarnation: "2026-08-26T09:00:00.000Z" }),
			"other",
		);
		let firstPending!: Promise<unknown>;
		let replacementPending!: Promise<unknown>;
		let otherPending!: Promise<unknown>;
		act(() => {
			firstPending = first.result.current.addFiles([file("first.txt")]);
			replacementPending = replacement.result.current.addFiles([file("replacement.txt")]);
			otherPending = other.result.current.addFiles([file("other.txt")]);
		});
		await waitFor(() => expect(releases.size).toBe(3));

		act(() => discardPendingFileAttachmentsForSession(sessionId));
		await act(async () => {
			releases.get("first")?.([{ id: "first", mimeType: "text/plain", bytes: 8, name: "first.txt", stagedPath: ".ao/attachments/first.txt" }]);
			releases.get("replacement")?.([{ id: "replacement", mimeType: "text/plain", bytes: 8, name: "replacement.txt", stagedPath: ".ao/attachments/replacement.txt" }]);
			releases.get("other")?.([{ id: "other", mimeType: "text/plain", bytes: 8, name: "other.txt", stagedPath: ".ao/attachments/other.txt" }]);
			await Promise.all([firstPending, replacementPending, otherPending]);
		});

		expect(first.result.current.attachments).toEqual([]);
		expect(replacement.result.current.attachments).toEqual([]);
		expect(other.result.current.attachments.map((attachment) => attachment.name)).toEqual([
			"other.txt",
		]);
	});

	it("discards only work captured before confirmation and preserves later attachments", async () => {
		const sessionId = "discard-confirmed-attachment-work-only";
		const key = chatDraftScopeKey({
			sessionId,
			incarnation: "2026-08-26T12:00:00.000Z",
		});
		const releases: Array<(attachments: FileAttachment[]) => void> = [];
		const prepareAttachments = vi.fn(
			() =>
				new Promise<FileAttachment[]>((resolve) => {
					releases.push(resolve);
				}),
		);
		const staging = renderHook(() =>
			useFileAttachments({ initialKey: key, prepareAttachments }),
		);
		let beforeConfirmation!: Promise<unknown>;
		act(() => {
			beforeConfirmation = staging.result.current.addFiles([file("before-confirmation.txt")]);
		});
		await waitFor(() => expect(prepareAttachments).toHaveBeenCalledTimes(1));

		const confirmedWork = capturePendingFileAttachmentsForSession(sessionId);
		let afterConfirmation!: Promise<unknown>;
		act(() => {
			afterConfirmation = staging.result.current.addFiles([file("after-confirmation.txt")]);
			discardCapturedPendingFileAttachments(confirmedWork);
		});
		expect(staging.result.current.preparing).toBe(true);

		await act(async () => {
			releases[0]?.([
				{
					id: "before-confirmation",
					mimeType: "text/plain",
					bytes: 8,
					name: "before-confirmation.txt",
					stagedPath: ".ao/attachments/before-confirmation.txt",
				},
			]);
			await beforeConfirmation;
		});
		await waitFor(() => expect(prepareAttachments).toHaveBeenCalledTimes(2));
		await act(async () => {
			releases[1]?.([
				{
					id: "after-confirmation",
					mimeType: "text/plain",
					bytes: 8,
					name: "after-confirmation.txt",
					stagedPath: ".ao/attachments/after-confirmation.txt",
				},
			]);
			await afterConfirmation;
		});

		expect(staging.result.current.preparing).toBe(false);
		expect(staging.result.current.attachments.map((attachment) => attachment.name)).toEqual([
			"after-confirmation.txt",
		]);
	});

	it("purges shared descriptors for a recreated logical session", async () => {
		const sessionId = "purge-shared-session-descriptors";
		const key = chatDraftScopeKey({
			sessionId,
			incarnation: "2026-08-25T09:00:00.000Z",
		});
		const first = renderHook(() => useFileAttachments({ initialKey: key }));
		await act(async () => {
			await first.result.current.addFiles([file("old.txt")]);
		});
		expect(first.result.current.attachments).toHaveLength(1);
		first.unmount();

		act(() => purgeFileAttachmentsForSession(sessionId));
		const staleRemount = renderHook(() => useFileAttachments({ initialKey: key }));
		expect(staleRemount.result.current.attachments).toEqual([]);
		expect(staleRemount.result.current.preparing).toBe(false);
	});

	it("rejects unsupported SVG files with inline feedback", async () => {
		const { result } = renderHook(() => useFileAttachments());
		await act(async () => {
			await result.current.addFiles([file("vector.svg", 8, "image/svg+xml")]);
		});
		expect(result.current.attachments).toHaveLength(0);
		expect(result.current.error).toMatch(/svg/i);
	});

	it("keeps a file too large to inline as its handle on disk, unread", async () => {
		const read = vi.spyOn(FileReader.prototype, "readAsDataURL");
		const huge = file("huge.mov", 8, "video/quicktime");
		// 2 GB: well past the old 50 MB cap. Only the size is faked; nothing reads it.
		Object.defineProperty(huge, "size", { value: 2048 * mb });
		try {
			const { result } = renderHook(() => useFileAttachments());
			await act(async () => {
				await result.current.addFiles([huge]);
			});
			expect(result.current.error).toBeNull();
			expect(result.current.attachments).toHaveLength(1);
			expect(result.current.attachments[0]).toMatchObject({ name: "huge.mov", bytes: 2048 * mb, file: huge });
			expect(result.current.attachments[0]?.data).toBeUndefined();
			expect(read).not.toHaveBeenCalled();
			expect(result.current.toPayload()).toEqual([{ mimeType: "video/quicktime", data: "", file: huge }]);
		} finally {
			read.mockRestore();
		}
	});

	it("previews an image too large to read from the file on disk", async () => {
		const read = vi.spyOn(FileReader.prototype, "readAsDataURL");
		const originalCreate = URL.createObjectURL;
		URL.createObjectURL = vi.fn(() => "blob:app/photo");
		const photo = file("photo.png", 8, "image/png");
		Object.defineProperty(photo, "size", { value: INLINE_ATTACHMENT_BYTES + 1 });
		try {
			const { result } = renderHook(() => useFileAttachments());
			await act(async () => {
				await result.current.addFiles([photo]);
			});
			expect(result.current.attachments[0]).toMatchObject({ name: "photo.png", dataUrl: "blob:app/photo", file: photo });
			expect(result.current.attachments[0]?.data).toBeUndefined();
			expect(URL.createObjectURL).toHaveBeenCalledWith(photo);
			expect(read).not.toHaveBeenCalled();
		} finally {
			URL.createObjectURL = originalCreate;
			read.mockRestore();
		}
	});

	it("still reads a file up to the inline size", async () => {
		const { result } = renderHook(() => useFileAttachments());
		await act(async () => {
			await result.current.addFiles([file("edge.bin", INLINE_ATTACHMENT_BYTES, "application/octet-stream")]);
		});
		expect(result.current.attachments[0]?.data).toBeTruthy();
	});

	it("holds files to a smaller limit when one is given, as AO Cloud's 25 MiB", async () => {
		const { result } = renderHook(() => useFileAttachments({ maxAttachmentBytes: 25 * mb }));
		await act(async () => {
			// Fine for the daemon, which takes any size; over Cloud's 25 MiB.
			await result.current.addFiles([file("big.bin", 25 * mb + 1, "application/octet-stream")]);
		});
		expect(result.current.attachments).toHaveLength(0);
		expect(result.current.error).toBe("Each file must be under 25 MB.");
		const small = renderHook(() => useFileAttachments({ maxAttachmentBytes: 1024 }));
		await act(async () => {
			await small.result.current.addFiles([file("fits.txt", 1024), file("over.txt", 1025)]);
		});
		expect(small.result.current.attachments.map((attachment) => attachment.name)).toEqual(["fits.txt"]);
	});

	it("uploads each file on its own: one that fails says why in the control plane's words, the rest attach", async () => {
		const prepareAttachments = vi.fn(async ([attachment]: FileAttachment[]) => {
			if (attachment!.name !== "fits.txt") throw new Error("A session can hold at most 200 attachments.");
			return [{ ...attachment!, stagedPath: `ao-cloud-attachment:${attachment!.id}/fits.txt` }];
		});
		const { result } = renderHook(() => useFileAttachments({ initialKey: "cloud-each", prepareAttachments, prepareEach: true }));
		await act(async () => {
			await result.current.addFiles([file("fits.txt"), file("second.txt"), file("third.txt")]);
		});
		expect(prepareAttachments).toHaveBeenCalledTimes(3);
		expect(prepareAttachments.mock.calls.every(([batch]) => batch.length === 1)).toBe(true);
		expect(result.current.attachments.map((attachment) => attachment.name)).toEqual(["fits.txt"]);
		expect(result.current.error).toBe("Couldn’t attach second.txt, third.txt: A session can hold at most 200 attachments.");
		expect(result.current.preparing).toBe(false);
	});

	it("cancels a file's upload when it is removed while uploading, and Send no longer waits on it", async () => {
		const signals = new Map<string, AbortSignal>();
		const releases = new Map<string, () => void>();
		const prepareAttachments = vi.fn(([attachment]: FileAttachment[], signal: AbortSignal) => {
			signals.set(attachment!.name, signal);
			return new Promise<FileAttachment[]>((resolve, reject) => {
				signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), { once: true });
				releases.set(attachment!.name, () => resolve([{ ...attachment!, stagedPath: `ao-cloud-attachment:${attachment!.id}/${attachment!.name}` }]));
			});
		});
		const { result } = renderHook(() => useFileAttachments({ initialKey: "cloud-cancel", prepareAttachments, prepareEach: true }));
		let pending!: Promise<unknown>;
		act(() => {
			pending = result.current.addFiles([file("stalled.bin"), file("kept.txt")]);
		});
		await waitFor(() => expect(signals.size).toBe(2));
		expect(result.current.pendingAttachments.map((attachment) => attachment.name)).toEqual(["stalled.bin", "kept.txt"]);
		const stalled = result.current.pendingAttachments[0]!;
		act(() => result.current.cancelPending(stalled.id));
		expect(signals.get("stalled.bin")?.aborted).toBe(true);
		expect(signals.get("kept.txt")?.aborted).toBe(false);
		expect(result.current.pendingAttachments.map((attachment) => attachment.name)).toEqual(["kept.txt"]);
		await act(async () => {
			releases.get("kept.txt")?.();
			await pending;
		});
		expect(result.current.attachments.map((attachment) => attachment.name)).toEqual(["kept.txt"]);
		expect(result.current.error).toBeNull();
		expect(result.current.preparing).toBe(false);
	});

	it("stops waiting at once when every uploading file is removed, and attaches nothing that finishes later", async () => {
		const releases: Array<() => void> = [];
		const prepareAttachments = vi.fn(([attachment]: FileAttachment[]) => new Promise<FileAttachment[]>((resolve) => {
			// A preparer that ignores the signal, as a stalled request would.
			releases.push(() => resolve([{ ...attachment!, stagedPath: "ao-cloud-attachment:x/late.bin" }]));
		}));
		const { result } = renderHook(() => useFileAttachments({ initialKey: "cloud-cancel-all", prepareAttachments, prepareEach: true }));
		let pending!: Promise<unknown>;
		act(() => {
			pending = result.current.addFiles([file("late.bin")]);
		});
		await waitFor(() => expect(releases).toHaveLength(1));
		expect(result.current.preparing).toBe(true);
		act(() => result.current.cancelPending(result.current.pendingAttachments[0]!.id));
		// Send is enabled again without waiting for the stalled upload.
		expect(result.current.preparing).toBe(false);
		expect(result.current.hasPendingReads()).toBe(false);
		await act(async () => {
			releases[0]?.();
			await pending;
		});
		expect(result.current.attachments).toEqual([]);
	});

	it("drops a batch file removed while it is staged, and keeps the rest of the batch", async () => {
		let release!: () => void;
		const prepareAttachments = vi.fn((attachments: FileAttachment[]) => new Promise<FileAttachment[]>((resolve) => {
			release = () => resolve(attachments.map((attachment) => ({ ...attachment, stagedPath: `.ao/attachments/${attachment.name}` })));
		}));
		const { result } = renderHook(() => useFileAttachments({ initialKey: "local-batch-cancel", prepareAttachments }));
		let pending!: Promise<unknown>;
		act(() => {
			pending = result.current.addFiles([file("drop.txt"), file("keep.txt")]);
		});
		await waitFor(() => expect(prepareAttachments).toHaveBeenCalled());
		act(() => result.current.cancelPending(result.current.pendingAttachments[0]!.id));
		await act(async () => {
			release();
			await pending;
		});
		expect(result.current.attachments.map((attachment) => attachment.name)).toEqual(["keep.txt"]);
	});

	it("accepts a video above the previous 10 MiB limit", async () => {
		const { result } = renderHook(() => useFileAttachments());
		await act(async () => {
			await result.current.addFiles([file("video.mov", 11 * mb, "video/quicktime")]);
		});
		expect(result.current.attachments[0]).toMatchObject({ name: "video.mov", bytes: 11 * mb });
		expect(result.current.error).toBeNull();
	});

	it("enforces the count cap", async () => {
		const read = vi.spyOn(FileReader.prototype, "readAsDataURL");
		const { result } = renderHook(() => useFileAttachments());
		try {
			await act(async () => {
				await result.current.addFiles(Array.from({ length: MAX_ATTACHMENTS + 2 }, (_, i) => file(`f-${i}.txt`)));
			});
			expect(result.current.attachments).toHaveLength(MAX_ATTACHMENTS);
			expect(result.current.error).toMatch(/up to/i);
			expect(read).toHaveBeenCalledTimes(MAX_ATTACHMENTS);
		} finally {
			read.mockRestore();
		}
	});

	it("has no total size cap", async () => {
		const read = vi.spyOn(FileReader.prototype, "readAsDataURL");
		const files = ["a.bin", "b.bin", "c.bin"].map((name) => {
			const big = file(name, 8, "application/octet-stream");
			Object.defineProperty(big, "size", { value: 600 * mb });
			return big;
		});
		try {
			const { result } = renderHook(() => useFileAttachments());
			await act(async () => {
				await result.current.addFiles(files);
			});
			expect(result.current.attachments.map((a) => a.name)).toEqual(["a.bin", "b.bin", "c.bin"]);
			expect(result.current.error).toBeNull();
			expect(read).not.toHaveBeenCalled();
		} finally {
			read.mockRestore();
		}
	});

	it("skips only a file that fails to read", async () => {
		const unreadable = file("unreadable.bin");
		const second = file("second.bin");
		const small = file("small.bin");
		const originalRead = FileReader.prototype.readAsDataURL;
		const read = vi.spyOn(FileReader.prototype, "readAsDataURL").mockImplementation(function (this: FileReader, blob: Blob) {
			if (blob === unreadable) {
				queueMicrotask(() => this.dispatchEvent(new ProgressEvent("error")));
				return;
			}
			originalRead.call(this, blob);
		});
		try {
			const { result } = renderHook(() => useFileAttachments());
			await act(async () => {
				await result.current.addFiles([unreadable, second, small]);
			});
			expect(result.current.attachments.map((attachment) => attachment.name)).toEqual(["second.bin", "small.bin"]);
			expect(result.current.error).toMatch(/couldn't be read/i);
			expect(read).toHaveBeenCalledTimes(3);
		} finally {
			read.mockRestore();
		}
	});

	it("clears a notice about files that were not attached when it is dismissed", async () => {
		const key = "dismiss-notice";
		const { result } = renderHook(() => useFileAttachments({ initialKey: key }));
		await act(async () => {
			await result.current.addFiles([file("vector.svg", 8, "image/svg+xml")]);
		});
		expect(result.current.error).toMatch(/svg/i);
		act(() => result.current.dismissError());
		expect(result.current.error).toBeNull();
		// A surface that mounts later for the same draft does not bring it back.
		const later = renderHook(() => useFileAttachments({ initialKey: key }));
		expect(later.result.current.error).toBeNull();
	});

	it("discards a pending file read when its draft is cleared", async () => {
		const readers: FileReader[] = [];
		const read = vi.spyOn(FileReader.prototype, "readAsDataURL").mockImplementation(function (this: FileReader) {
			readers.push(this);
		});
		try {
			const { result } = renderHook(() => useFileAttachments());
			let pending!: Promise<unknown>;
			act(() => {
				pending = result.current.addFiles([file("discarded.png", 8, "image/png")]);
			});
			expect(result.current.hasPendingReads()).toBe(true);
			act(() => result.current.clear());
			expect(result.current.hasPendingReads()).toBe(false);
			await act(async () => {
				Object.defineProperty(readers[0], "result", { value: "data:image/png;base64,AQ==" });
				readers[0].dispatchEvent(new ProgressEvent("load"));
				await pending;
			});
			expect(result.current.attachments).toEqual([]);
			expect(await result.current.toSettledPayload()).toEqual([]);
		} finally {
			read.mockRestore();
		}
	});
});
