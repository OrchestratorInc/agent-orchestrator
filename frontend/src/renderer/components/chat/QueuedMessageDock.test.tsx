import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { QueuedMessageDock, type QueuedMessage } from "./QueuedMessageDock";
import { ChatWorkspace } from "./ChatWorkspace";
import { chatFixture } from "../../lib/chat-fixture";
import { renderChat } from "../../test/render-chat";

const { dragEnds, dragStarts } = vi.hoisted(() => ({
	dragEnds: new Map<string, (event: { active: { id: string }; over: { id: string } | null }) => void>(),
	dragStarts: new Map<string, (event: {
		active: { id: string; rect: { current: { initial: { width: number } } } };
	}) => void>(),
}));

vi.mock("@dnd-kit/core", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@dnd-kit/core")>();
	return {
		...actual,
		DndContext: ({
			children,
			id,
			onDragEnd,
			onDragStart,
		}: {
			children: React.ReactNode;
			id?: string;
			onDragEnd?: (event: { active: { id: string }; over: { id: string } | null }) => void;
			onDragStart?: (event: {
				active: { id: string; rect: { current: { initial: { width: number } } } };
			}) => void;
		}) => {
			if (id && onDragEnd) dragEnds.set(id, onDragEnd);
			if (id && onDragStart) dragStarts.set(id, onDragStart);
			return children;
		},
		DragOverlay: ({ children }: { children: React.ReactNode }) => children,
	};
});

function queuedMessages(texts: string[]): QueuedMessage[] {
	return texts.map((text, index) => {
		const turnId = `queued-${index + 1}`;
		return {
			turnId,
			message: {
				kind: "message",
				id: `${turnId}-message`,
				turnId,
				sequence: 100 + index,
				revision: 0,
				role: "user",
				origin: "human",
				text,
				streaming: false,
				createdAt: `2026-08-11T10:0${index + 1}:00Z`,
			},
		};
	});
}

function rowTexts() {
	const dock = screen.getByTestId("queued-message-dock");
	return within(dock)
		.getAllByTestId(/^queued-message-queued-/, { exact: false })
		.map((row) => row.textContent?.replace(/\s+/g, " ").trim() ?? "");
}

function simulateDrag(activeId: string, overId: string) {
	const dragStart = dragStarts.get("queue-dock-reorder");
	const dragEnd = dragEnds.get("queue-dock-reorder");
	if (!dragStart || !dragEnd) {
		throw new Error("queue dock drag handlers were not registered");
	}
	act(() => {
		dragStart({
			active: {
				id: activeId,
				rect: { current: { initial: { width: 420 } } },
			},
		});
		dragEnd({ active: { id: activeId }, over: { id: overId } });
	});
}

describe("QueuedMessageDock reorder", () => {
	it("submits FIFO order when dragging the last row to the top", async () => {
		const onReorderQueuedTurns = vi.fn().mockResolvedValue(undefined);
		render(
			<QueuedMessageDock
				messages={queuedMessages(["first queued", "second queued", "third queued"])}
				onReorderQueuedTurns={onReorderQueuedTurns}
			/>,
		);

		simulateDrag("queued-3", "queued-1");

		await waitFor(() => {
			expect(onReorderQueuedTurns).toHaveBeenCalledWith(["queued-3", "queued-1", "queued-2"]);
		});
	});

	it("submits FIFO order when dragging the first row to the bottom", async () => {
		const onReorderQueuedTurns = vi.fn().mockResolvedValue(undefined);
		render(
			<QueuedMessageDock
				messages={queuedMessages(["first queued", "second queued", "third queued"])}
				onReorderQueuedTurns={onReorderQueuedTurns}
			/>,
		);

		simulateDrag("queued-1", "queued-3");

		await waitFor(() => {
			expect(onReorderQueuedTurns).toHaveBeenCalledWith(["queued-2", "queued-3", "queued-1"]);
		});
	});

	it("restores the visual order and surfaces an error when reorder fails", async () => {
		const onReorderQueuedTurns = vi.fn().mockRejectedValue(new Error("reorder failed"));
		render(
			<QueuedMessageDock
				messages={queuedMessages(["first queued", "second queued", "third queued"])}
				onReorderQueuedTurns={onReorderQueuedTurns}
			/>,
		);

		expect(rowTexts()).toEqual([
			expect.stringContaining("first queued"),
			expect.stringContaining("second queued"),
			expect.stringContaining("third queued"),
		]);

		simulateDrag("queued-3", "queued-1");

		await waitFor(() => {
			expect(screen.getByRole("status")).toHaveTextContent("reorder failed");
		});
		expect(rowTexts()).toEqual([
			expect.stringContaining("first queued"),
			expect.stringContaining("second queued"),
			expect.stringContaining("third queued"),
		]);
	});

	it("freezes hover-only steer visibility while dragging", async () => {
		const user = userEvent.setup();
		const onReorderQueuedTurns = vi.fn().mockResolvedValue(undefined);
		render(
			<QueuedMessageDock
				canSteer
				messages={queuedMessages(["first queued", "second queued"])}
				onReorderQueuedTurns={onReorderQueuedTurns}
			/>,
		);

		await user.hover(screen.getByTestId("queued-message-queued-2"));
		simulateDrag("queued-2", "queued-1");

		await waitFor(() => {
			expect(onReorderQueuedTurns).toHaveBeenCalledWith(["queued-2", "queued-1"]);
		});
	});
});

describe("QueuedMessageDock order and steering", () => {
	it("lists messages in send order with a pending send last", () => {
		render(
			<QueuedMessageDock
				messages={[
					...queuedMessages(["first queued", "second queued"]),
					{ ...queuedMessages(["just sent"])[0]!, turnId: "local:pending", pending: true },
				]}
			/>,
		);

		expect(rowTexts()).toEqual([
			expect.stringContaining("first queued"),
			expect.stringContaining("second queued"),
		]);
		const dock = screen.getByTestId("queued-message-dock");
		const texts = Array.from(dock.querySelectorAll(".queue-dock-row-text")).map((node) => node.textContent);
		expect(texts).toEqual(["first queued", "second queued", "just sent"]);
	});

	it("keeps every row's actions and grip in place while a new message is sent", () => {
		const props = {
			canSteer: true,
			onPromoteQueuedTurn: vi.fn().mockResolvedValue(undefined),
			onCancelQueuedTurn: vi.fn().mockResolvedValue(undefined),
			onReorderQueuedTurns: vi.fn().mockResolvedValue(undefined),
		};
		const confirmed = queuedMessages(["first queued", "second queued"]);
		const pending = { ...queuedMessages(["just sent"])[0]!, turnId: "local:pending", pending: true };
		const { rerender } = render(<QueuedMessageDock {...props} messages={confirmed} />);
		const grips = () => screen.getAllByRole("button", { name: "Drag to reorder queued message" });
		const deletes = () => screen.getAllByRole("button", { name: "Delete queued message" });
		const steers = () => screen.getAllByRole("button", { name: "Steer this queued message into the running turn" });
		expect(grips()).toHaveLength(2);
		for (const grip of grips()) expect(grip).toBeEnabled();

		rerender(<QueuedMessageDock {...props} messages={[...confirmed, pending]} sending />);

		// Every row, the unconfirmed one included, keeps the same controls.
		expect(grips()).toHaveLength(3);
		expect(deletes()).toHaveLength(3);
		expect(steers()).toHaveLength(3);
		const pendingRow = screen.getByTestId("queued-message-local:pending");
		for (const button of within(pendingRow).getAllByRole("button")) {
			expect(button).toBeDisabled();
		}

		rerender(<QueuedMessageDock {...props} messages={[...confirmed, pending]} />);
		expect(grips()[0]).toBeEnabled();
		for (const button of within(pendingRow).getAllByRole("button")) {
			expect(button).toBeDisabled();
		}
	});

	it("keeps Steer on every row while a turn can be steered", () => {
		render(
			<QueuedMessageDock
				canSteer
				messages={queuedMessages(["first queued", "second queued", "third queued"])}
				onPromoteQueuedTurn={vi.fn().mockResolvedValue(undefined)}
			/>,
		);

		for (const turnId of ["queued-1", "queued-2", "queued-3"]) {
			const steer = within(screen.getByTestId(`queued-message-${turnId}`)).getByRole("button", {
				name: "Steer this queued message into the running turn",
			});
			expect(steer).not.toHaveClass("opacity-0");
		}
	});

	it("keeps rows steady instead of dimming them while the composer sends", () => {
		const props = {
			canSteer: true,
			messages: queuedMessages(["first queued", "second queued", "third queued"]),
			onPromoteQueuedTurn: vi.fn().mockResolvedValue(undefined),
			onCancelQueuedTurn: vi.fn().mockResolvedValue(undefined),
			onBeginQueuedEdit: vi.fn(),
		};
		const { rerender } = render(<QueuedMessageDock {...props} sending />);
		const steerButtons = () =>
			screen.getAllByRole("button", { name: "Steer this queued message into the running turn" });

		expect(steerButtons()).toHaveLength(3);
		for (const button of [
			...steerButtons(),
			...screen.getAllByRole("button", { name: "Delete queued message" }),
		]) {
			expect(button).toBeDisabled();
			expect(button).toHaveClass("disabled:opacity-100");
		}

		rerender(<QueuedMessageDock {...props} />);
		for (const button of steerButtons()) expect(button).toBeEnabled();
		expect(screen.getAllByRole("button", { name: "Delete queued message" })[0]).toBeEnabled();
	});
});

describe("ChatWorkspace queue reorder", () => {
	function queuedPrompt(turnId: string, sequence: number, origin: "human" | "automation", text: string) {
		return {
			kind: "message" as const,
			id: `${turnId}-message`,
			turnId,
			sequence,
			revision: 0,
			role: "user" as const,
			origin,
			text,
			streaming: false,
			createdAt: `2026-08-11T10:0${sequence - 99}:00Z`,
		};
	}

	it("keeps a queued automation turn in place when the user reorders their own prompts", async () => {
		const onReorderQueuedTurns = vi.fn().mockResolvedValue(undefined);
		renderChat(
			<ChatWorkspace
				snapshot={{
					...chatFixture,
					turns: [
						...chatFixture.turns,
						{ id: "queued-first", state: "queued" as const, requestedAt: "2026-08-11T10:01:00Z" },
						{ id: "queued-automation", state: "queued" as const, requestedAt: "2026-08-11T10:02:00Z" },
						{ id: "queued-last", state: "queued" as const, requestedAt: "2026-08-11T10:03:00Z" },
					],
					items: [
						...chatFixture.items,
						queuedPrompt("queued-first", 100, "human", "first prompt"),
						queuedPrompt("queued-automation", 101, "automation", "automation follow-up"),
						queuedPrompt("queued-last", 102, "human", "last prompt"),
					],
				}}
				onSteer={vi.fn()}
				onReorderQueuedTurns={onReorderQueuedTurns}
			/>,
		);
		expect(screen.queryByTestId("queued-message-queued-automation")).not.toBeInTheDocument();

		simulateDrag("queued-last", "queued-first");

		await waitFor(() => {
			expect(onReorderQueuedTurns).toHaveBeenCalledWith(["queued-last", "queued-automation", "queued-first"]);
		});
	});
});

describe("QueuedMessageDock action errors", () => {
	it("shows the daemon's reason when steering a queued message is refused", async () => {
		// The typed client throws the API error envelope, not an Error instance.
		const onPromoteQueuedTurn = vi.fn().mockRejectedValue({
			error: {
				code: "CHAT_TURN_NOT_STEERABLE",
				message: "the running turn cannot take guidance: it collects between tool calls",
			},
		});
		render(
			<QueuedMessageDock
				messages={queuedMessages(["use staging instead"])}
				canSteer
				onPromoteQueuedTurn={onPromoteQueuedTurn}
			/>,
		);

		await userEvent.click(
			screen.getAllByRole("button", { name: "Steer this queued message into the running turn" })[0]!,
		);

		await waitFor(() => {
			expect(screen.getByTestId("queued-message-dock")).toHaveTextContent(
				"the running turn cannot take guidance: it collects between tool calls",
			);
		});
		expect(screen.getByTestId("queued-message-dock")).not.toHaveTextContent("That action failed.");
		expect(screen.getByTestId("queued-message-queued-1")).toBeInTheDocument();
	});
});

describe("QueuedMessageDock excerpt rows", () => {
	const placeholder = (count: number, kind = "chat excerpt") => `Use the attached ${count} ${kind}(s) as context`;
	function withContent(text: string, content: NonNullable<QueuedMessage["message"]["content"]>): QueuedMessage[] {
		const [queued] = queuedMessages([text]);
		return [{ ...queued!, message: { ...queued!.message, content } }];
	}
	const excerpt = (text: string, order: number, comment?: string) => ({
		type: "excerpt", text, comment, annotationId: `ex-${order}`, annotationOrder: order, sourceMessageId: "m-1",
	});
	const rowText = () => screen.getByTestId("queued-message-queued-1").querySelector(".queue-dock-row-text");

	it("shows the selected text instead of the stand-in an excerpt-only message carries", () => {
		render(<QueuedMessageDock messages={withContent(placeholder(1), [excerpt("10.1 GB sits in\n  live sessions", 1)])} />);

		expect(rowText()).toHaveTextContent("“10.1 GB sits in live sessions”");
		expect(screen.getByTestId("queued-message-dock")).not.toHaveTextContent("Use the attached");
		expect(rowText()).toHaveAttribute("title", "1. “10.1 GB sits in live sessions”");
	});

	it("leads with the comment written on the excerpt", () => {
		render(<QueuedMessageDock messages={withContent(placeholder(1), [excerpt("prune build cache", 1, "Keep volumes")])} />);

		expect(rowText()).toHaveTextContent("Keep volumes · “prune build cache”");
	});

	it("shows the first excerpt and the count when there are several, and lists each in the tooltip", () => {
		render(<QueuedMessageDock messages={withContent(placeholder(2), [excerpt("second", 2), excerpt("first", 1)])} />);

		// The count sits beside the text, so truncating a long quote never hides it.
		expect(rowText()).toHaveTextContent(/^“first”$/);
		expect(rowText()?.nextElementSibling).toHaveTextContent("2 annotations");
		expect(rowText()).toHaveAttribute("title", "1. “first”\n2. “second”");
	});

	it("names a file reference by its file and lines", () => {
		render(<QueuedMessageDock messages={withContent(placeholder(1, "annotation"), [{
			type: "file_reference",
			annotationId: "file-1",
			annotationOrder: 1,
			fileReference: {
				id: "file-1", order: 1, sessionId: "s-1", path: "src/app/main.ts", side: "file", line: 4, endLine: 6,
				lines: [{ kind: "context", oldNo: 4, newNo: 4, text: "a" }], source: { kind: "workspace" }, truncated: false,
			},
		}])} />);

		expect(rowText()).toHaveTextContent("main.ts#L4-L6");
	});

	it("keeps what the user typed, and lists the excerpts under it in the tooltip", () => {
		render(<QueuedMessageDock messages={withContent("shorten this", [excerpt("a long paragraph", 1)])} />);

		expect(rowText()).toHaveTextContent("shorten this");
		expect(rowText()).toHaveAttribute("title", "shorten this\n1. “a long paragraph”");
	});

	it("keeps the stand-in when the message carries no excerpt to show", () => {
		render(<QueuedMessageDock messages={queuedMessages([placeholder(1)])} />);

		expect(rowText()).toHaveTextContent(placeholder(1));
	});
});

describe("QueuedMessageDock attachment rows", () => {
	const shot = ".ao/attachments/attachment-6803746e3bc.png";
	const notes = ".ao/attachments/attachment-notes.md";
	const withFiles = (text: string, paths: string[]) =>
		`${text ? `${text}\n\n` : ""}Attached files (read these files in the workspace):\n${paths.map((path) => `- ${path}`).join("\n")}`;
	const source = { sessionId: "sess-1", apiBaseUrl: "http://127.0.0.1:3001" };
	const row = () => screen.getByTestId("queued-message-queued-1");
	const rowText = () => row().querySelector(".queue-dock-row-text");
	const fileNames = () => within(row()).getByTestId("queued-message-files");
	const shotURL = "http://127.0.0.1:3001/api/v1/sessions/sess-1/preview/files/.ao/attachments/attachment-6803746e3bc.png";

	it("shows an image-only message as its image, not the reference block", () => {
		render(<QueuedMessageDock messages={queuedMessages([withFiles("", [shot])])} attachmentSource={source} />);

		const image = within(row()).getByRole("button", { name: "Open image: Image 1" }).querySelector("img");
		expect(image).toHaveAttribute("src", shotURL);
		expect(row()).not.toHaveTextContent("Attached files");
		expect(image).toHaveAttribute("crossorigin", "anonymous");
		expect(rowText()).toBeNull();
	});

	it("shows the typed text beside the image, with inline images numbered", () => {
		render(<QueuedMessageDock messages={queuedMessages([withFiles(`fix ${shot} please`, [shot])])} attachmentSource={source} />);

		expect(within(row()).getByRole("button", { name: "Open image: Image 1" })).toBeInTheDocument();
		expect(rowText()).toHaveTextContent(/^fix \[Image 1\] please$/);
	});

	it("names attached files that are not images", () => {
		render(<QueuedMessageDock messages={queuedMessages([withFiles("", [notes])])} attachmentSource={source} />);

		expect(fileNames()).toHaveTextContent(/^attachment-notes\.md$/);
		expect(row()).not.toHaveTextContent("Attached files");
	});

	it("names attached files beside the typed text and its images", () => {
		render(<QueuedMessageDock messages={queuedMessages([withFiles("compare these", [shot, notes])])} attachmentSource={source} />);

		expect(within(row()).getByRole("button", { name: "Open image: Image 1" })).toBeInTheDocument();
		expect(rowText()).toHaveTextContent(/^compare these$/);
		expect(fileNames()).toHaveTextContent(/^attachment-notes\.md$/);
	});

	it("draws a few images and counts the rest, so the text keeps its room", () => {
		const shots = Array.from({ length: 6 }, (_, index) => `.ao/attachments/attachment-shot${index}.png`);
		render(<QueuedMessageDock messages={queuedMessages([withFiles("all of them", shots)])} attachmentSource={source} />);

		expect(within(row()).getAllByRole("button", { name: /^Open image:/ })).toHaveLength(4);
		expect(screen.getByTestId("queued-message-images")).toHaveTextContent(/^\+2$/);
	});

	it("shows the name of an image that cannot be read", () => {
		render(<QueuedMessageDock messages={queuedMessages([withFiles("", [shot])])} attachmentSource={source} />);

		fireEvent.error(within(row()).getByRole("button", { name: "Open image: Image 1" }).querySelector("img")!);
		expect(within(row()).queryByRole("button", { name: /^Open image/ })).not.toBeInTheDocument();
		expect(screen.getByTestId("queued-message-images")).toHaveTextContent(/^Image 1$/);
	});

	it("names images when there is nowhere to read them from", () => {
		render(<QueuedMessageDock messages={queuedMessages([withFiles("", [shot])])} attachmentSource={{ sessionId: "sess-1", apiBaseUrl: null }} />);

		expect(within(row()).queryByRole("button", { name: /^Open / })).not.toBeInTheDocument();
		expect(fileNames()).toHaveTextContent(/^Image 1$/);
	});
});

describe("ChatWorkspace queued excerpts", () => {
	it("shows the selected text on a queued excerpt the daemon has not confirmed yet", () => {
		renderChat(
			<ChatWorkspace
				snapshot={chatFixture}
				onSteer={vi.fn()}
				localEchos={[{
					clientMessageId: "echo-excerpt",
					text: "Use the attached 1 chat excerpt(s) as context",
					createdAt: "2026-08-11T10:05:00Z",
					queued: true,
					excerpts: [{ id: "ex-1", order: 1, text: "Volumes stay, since they hold data.", comment: "Delete them too", messageId: "m-1", revision: 1 }],
				}]}
			/>,
		);

		const dock = screen.getByTestId("queued-message-dock");
		expect(dock).toHaveTextContent("Delete them too · “Volumes stay, since they hold data.”");
		expect(dock).not.toHaveTextContent("Use the attached");
	});
});
