import {
	act,
	render,
	renderHook,
	screen,
	waitFor,
} from "@testing-library/react";
import { TooltipProvider } from "../ui/tooltip";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useIndependentSideChats } from "./IndependentSideChats";
import type { ChatDraftExcerptReference } from "../../lib/chat-drafts";

const { get, post, put, remove, patch, capture } = vi.hoisted(() => ({
	get: vi.fn(),
	post: vi.fn(),
	put: vi.fn(),
	remove: vi.fn(),
	patch: vi.fn(),
	capture: vi.fn(),
}));
vi.mock("../../lib/api-client", () => ({
	apiClient: { GET: get, POST: post, PUT: put, DELETE: remove, PATCH: patch },
	apiErrorMessage: () => "Failed",
	getApiBaseUrl: () => "",
	subscribeApiBaseUrl: () => () => {},
}));
vi.mock("../../lib/bridge", () => ({
	aoBridge: {
		sideChats: {
			capture,
			confirmClose: async () => {},
			getPendingCloses: async () => [],
			onPendingClosesChanged: () => () => {},
		},
	},
}));
function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((done) => {
		resolve = done;
	});
	return { promise, resolve };
}
const ref: ChatDraftExcerptReference = {
	id: "mixed",
	conversationId: "main",
	messageId: "",
	revision: 0,
	text: "Code",
	role: "assistant",
	segments: [
		{
			kind: "command",
			sourceId: "cmd",
			revision: 1,
			field: "output",
			text: "Code",
		},
	],
};
const A = {
	id: "A",
	sessionId: "session",
	state: "ready",
	mainConversationId: "main",
};

beforeEach(() => {
	HTMLElement.prototype.scrollTo = vi.fn();
	vi.clearAllMocks();
	capture.mockResolvedValue(undefined);
	post.mockResolvedValue({ data: { side: A } });
	put.mockResolvedValue({});
	remove.mockResolvedValue({});
	get.mockImplementation(
		async (
			path: string,
			options: { params: { path: { sideId?: string } } },
		) => {
			if (path.endsWith("/side-chats")) return { data: { sides: [] } };
			if (path.endsWith("/draft"))
				return {
					data: {
						contentJson: JSON.stringify({
							version: 1,
							text: "Saved draft",
							attachments: [],
							references: [],
						}),
					},
				};
			return {
				data: {
					snapshot: {
						side: { ...A, id: options.params.path.sideId },
						messages: [],
						turns: [],
						activities: [],
					},
				},
			};
		},
	);
});

describe("mixed side creation ownership", () => {
	it("rolls back only its new reference when draft persistence rejects", async () => {
		const save = deferred<{ error: { message: string } }>();
		put.mockReturnValueOnce(save.promise);
		let side!: ReturnType<typeof useIndependentSideChats>;
		function Harness() {
			side = useIndependentSideChats("session", [], [], async () => [], false);
			return <TooltipProvider>{side.panel}</TooltipProvider>;
		}
		render(<Harness />);
		await waitFor(() => expect(get).toHaveBeenCalled());
		let creating!: ReturnType<typeof side.create>;
		act(() => {
			creating = side.create(ref, false, "key", {
				shouldActivate: () => false,
			});
		});
		await waitFor(() => expect(put).toHaveBeenCalled());
		act(() => side.setQuestionDraft("A", "newer edit"));
		await act(async () => {
			save.resolve({ error: { message: "rejected" } });
			await expect(creating).rejects.toEqual({ message: "rejected" });
		});
		await act(async () => {
			await side.create(undefined, false, "retry", {
				shouldActivate: () => true,
			});
		});
		await waitFor(() =>
			expect(screen.getByLabelText("Side chat question")).toHaveTextContent(
				"newer edit",
			),
		);
		expect(screen.queryByRole("button", { name: "1 annotation" })).toBeNull();
		expect(side.pending).toBe(false);
	});
	it("retains an inactive destination's saved draft without stealing selection focus", async () => {
		const reply = deferred<{ data: { side: typeof A } }>();
		post.mockReturnValue(reply.promise);
		const original = get.getMockImplementation()!;
		get.mockImplementation((path: string, options: unknown) =>
			path.endsWith("/side-chats")
				? Promise.resolve({ data: { sides: [{ ...A, id: "B" }] } })
				: original(path, options),
		);
		const hook = renderHook(() =>
			useIndependentSideChats("session", [], [], async () => [], false),
		);
		await waitFor(() => expect(hook.result.current.activeId).toBe("B"));
		let creating!: ReturnType<typeof hook.result.current.create>;
		act(() => {
			creating = hook.result.current.create(ref, false, "key", {
				shouldActivate: () => false,
			});
		});
		act(() => hook.result.current.show("B"));
		await act(async () => {
			reply.resolve({ data: { side: A } });
			await creating;
		});
		expect(hook.result.current.activeId).toBe("B");
		const saved = put.mock.calls
			.filter(([path]) => path.endsWith("/draft"))
			.map(([, options]) => JSON.parse(options.body.contentJson))
			.find((draft) => draft.references?.length);
		expect(saved).toMatchObject({ text: "Saved draft", references: [ref] });
		expect(post.mock.calls[0][1].body.reference.segments).toEqual(ref.segments);
	});

	it("ignores completion when the session changes during desktop capture", async () => {
		const snapshot = deferred<void>();
		capture.mockReturnValue(snapshot.promise);
		const hook = renderHook(
			({ session }) =>
				useIndependentSideChats(session, [], [], async () => [], false),
			{ initialProps: { session: "session" } },
		);
		await waitFor(() => expect(get).toHaveBeenCalled());
		let creating!: ReturnType<typeof hook.result.current.create>;
		act(() => {
			creating = hook.result.current.create(ref, false, "key", {
				shouldActivate: () => true,
			});
		});
		await waitFor(() => expect(capture).toHaveBeenCalled());
		hook.rerender({ session: "next" });
		await act(async () => {
			snapshot.resolve();
			await expect(creating).rejects.toThrow("no longer active");
		});
		expect(hook.result.current.sides).toEqual([]);
		expect(hook.result.current.activeId).toBeUndefined();
		expect(hook.result.current.pending).toBe(false);
		expect(put).not.toHaveBeenCalled();
	});

	it("merges an edit queued with an inactive draft read", async () => {
		const saved = deferred<{ data: { contentJson: string } }>();
		get.mockImplementation(async (path: string) =>
			path.endsWith("/side-chats")
				? { data: { sides: [] } }
				: path.endsWith("/draft")
					? saved.promise
					: {
							data: {
								snapshot: { side: A, messages: [], turns: [], activities: [] },
							},
						},
		);
		const hook = renderHook(() =>
			useIndependentSideChats("session", [], [], async () => [], false),
		);
		await waitFor(() => expect(get).toHaveBeenCalled());
		let creating!: ReturnType<typeof hook.result.current.create>;
		act(() => {
			creating = hook.result.current.create(ref, false, "key", {
				shouldActivate: () => false,
			});
		});
		await waitFor(() =>
			expect(get.mock.calls.some(([path]) => path.endsWith("/draft"))).toBe(
				true,
			),
		);
		await act(async () => {
			saved.resolve({
				data: {
					contentJson: JSON.stringify({
						version: 1,
						text: "server",
						attachments: [],
						references: [],
					}),
				},
			});
			hook.result.current.setQuestionDraft("A", "queued edit");
			await creating;
		});
		const last = JSON.parse(put.mock.calls.at(-1)![1].body.contentJson);
		expect(last.text).toBe("queued edit");
		expect(last.references).toEqual([ref]);
	});
});
