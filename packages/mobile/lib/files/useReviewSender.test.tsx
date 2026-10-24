import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { FileAnnotationComment } from "../../../shared/files/file-annotations";
import type { WorkspaceSource } from "./workspaceSource";
const effects = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("../haptics", () => ({ haptics: effects }));
vi.mock("./api", () => ({ sendFileFeedback: async () => { throw new Error("Unexpected daemon send"); } }));
import { addReviewComment, clearCloudReviewsForAccount, cloudReviewKey, reviewComments } from "./reviewStore";
import { useReviewSender } from "./useReviewSender";

function deferred<T>() { let resolve!: (value: T) => void; let reject!: (error: Error) => void; const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; }); return { promise, resolve, reject }; }
function fixture(name: string) {
	let current = true;
	const completion = deferred<number>();
	const deliveries: FileAnnotationComment[][] = [];
	let accepted: ((count: number) => void) | undefined;
	const source = { scopeKey: `["https://cloud.test","${name}","org"]`, isCurrent: () => current, sendFeedback: async (_id: string, comments: FileAnnotationComment[], onSent?: (count: number) => void) => { deliveries.push(comments); accepted = onSent; return completion.promise; } } as WorkspaceSource;
	const key = cloudReviewKey(source.scopeKey, "same");
	return { source, key, deliveries, completion, accept: (count: number) => accepted?.(count), retire: () => { current = false; } };
}
const target = { path: "a.ts", side: "new" as const, line: 3 };
let first: ReturnType<typeof useReviewSender>, second: ReturnType<typeof useReviewSender>;
let renderer: ReactTestRenderer | undefined;
function Harness({ source, reviewKey }: { source: WorkspaceSource | null; reviewKey: string }) { first = useReviewSender(source, reviewKey, "same"); second = useReviewSender(source, reviewKey, "same"); return null; }
async function render(source: WorkspaceSource | null, reviewKey: string) { await act(async () => { const element = <Harness source={source} reviewKey={reviewKey} />; if (renderer) renderer.update(element); else renderer = create(element); }); }
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); vi.clearAllMocks(); });
afterEach(async () => { await act(async () => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

it("shares one send between retained Files screens and keeps only unaccepted comments queued", async () => {
	const owner = fixture("partial");
	const a = addReviewComment(owner.key, { target, label: "a", feedback: "rename" });
	addReviewComment(owner.key, { target, label: "b", feedback: "explain" });
	await render(owner.source, owner.key);
	let send!: Promise<void>, duplicate!: Promise<void>;
	await act(async () => { send = first.send(); duplicate = second.send(); });
	expect(owner.deliveries).toHaveLength(1); expect(first.sending).toBe(true); expect(second.sending).toBe(true);
	await act(async () => { owner.accept(1); owner.completion.reject(new Error("Offline")); await Promise.all([send, duplicate]); });
	expect(reviewComments(owner.key).map((comment) => [comment.id === a.id, comment.status])).toEqual([[true, "sent"], [false, "queued"]]);
	expect(first.comments.map((comment) => comment.feedback)).toEqual(["explain"]);
	expect(first.error).toBeTruthy(); expect(first.sending).toBe(false);
});

it("ignores old account delivery callbacks and UI effects after logout", async () => {
	const owner = fixture("logout-send"); addReviewComment(owner.key, { target, label: "a", feedback: "private" });
	await render(owner.source, owner.key); let send!: Promise<void>;
	await act(async () => { send = first.send(); });
	await act(async () => { owner.retire(); clearCloudReviewsForAccount("https://cloud.test", "logout-send"); }); await render(null, owner.key);
	await act(async () => { owner.accept(1); owner.completion.resolve(1); await send; });
	expect(reviewComments(owner.key)).toEqual([]); expect(first.error).toBeUndefined(); expect(first.sending).toBe(false);
	expect(effects.success).not.toHaveBeenCalled(); expect(effects.error).not.toHaveBeenCalled();
});

it("records an accepted Local-style delivery after ordinary navigation without notifying the departed screen", async () => {
	const owner = fixture("navigation-send"); addReviewComment(owner.key, { target, label: "a", feedback: "rename" });
	await render(owner.source, owner.key); let send!: Promise<void>;
	await act(async () => { send = first.send(); });
	await act(async () => { renderer!.unmount(); }); renderer = undefined;
	await act(async () => { owner.accept(1); owner.completion.resolve(1); await send; });
	expect(reviewComments(owner.key).map((comment) => comment.status)).toEqual(["sent"]);
	expect(effects.success).not.toHaveBeenCalled();
});

it("allows a fresh login to send while a retired login's response is still pending", async () => {
	const retired = fixture("same-account-login"); addReviewComment(retired.key, { target, label: "old", feedback: "old private text" });
	await render(retired.source, retired.key); let oldSend!: Promise<void>;
	await act(async () => { oldSend = first.send(); });
	await act(async () => { retired.retire(); clearCloudReviewsForAccount("https://cloud.test", "same-account-login"); });
	const fresh = fixture("same-account-login"); await act(async () => { addReviewComment(fresh.key, { target, label: "new", feedback: "new text" }); });
	await render(fresh.source, fresh.key); let newSend!: Promise<void>;
	await act(async () => { newSend = first.send(); });
	expect(fresh.deliveries.map((comments) => comments[0].feedback)).toEqual(["new text"]);
	await act(async () => { retired.accept(1); retired.completion.resolve(1); await oldSend; });
	expect(first.sending).toBe(true); expect(first.comments.map((comment) => comment.feedback)).toEqual(["new text"]);
	await act(async () => { fresh.accept(1); fresh.completion.resolve(1); await newSend; });
	expect(first.comments).toEqual([]); expect(first.sending).toBe(false);
});
