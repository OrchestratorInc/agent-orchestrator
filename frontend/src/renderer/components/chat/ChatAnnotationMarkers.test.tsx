import { render, waitFor } from "@testing-library/react";
import { useRef } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ChatDraftExcerptReference } from "../../lib/chat-drafts";
import { ChatAnnotationMarkers } from "./ChatAnnotationMarkers";

const registry = new Map<string, Set<Range>>();

beforeEach(() => {
	registry.clear();
	vi.stubGlobal("Highlight", class extends Set<Range> {
		constructor(...ranges: Range[]) { super(ranges); }
	});
	vi.stubGlobal("CSS", { escape: (value: string) => value, highlights: registry });
});

afterEach(() => vi.unstubAllGlobals());

function Timeline({ messageId, text }: { messageId: string; text: string }) {
	const viewport = useRef<HTMLDivElement>(null);
	const content = useRef<HTMLDivElement>(null);
	const reference = { id: `${messageId}-a`, conversationId: "c", messageId, text: "selected", order: 1 } as ChatDraftExcerptReference;
	return (
		<div>
			<div ref={viewport}>
				<div ref={content}>
					<div data-chat-message-id={messageId}><p key={text}>{text}</p></div>
				</div>
			</div>
			<ChatAnnotationMarkers markers={[{ number: 1, reference }]} conversationId="c" contentRef={content} viewportRef={viewport} onOpen={() => undefined} />
		</div>
	);
}

const painted = () => [...(registry.get("chat-annotations") ?? [])];

it("keeps each timeline's highlights when another timeline unmounts", () => {
	render(<Timeline messageId="m1" text="the selected words" />);
	const second = render(<Timeline messageId="m2" text="more selected words" />);
	expect(painted()).toHaveLength(2);
	second.unmount();
	expect(painted()).toHaveLength(1);
	expect(painted()[0].startContainer.isConnected).toBe(true);
});

it("repaints the highlight when the annotated text is re-rendered", async () => {
	const view = render(<Timeline messageId="m1" text="the selected words" />);
	const before = painted()[0];
	view.rerender(<Timeline messageId="m1" text="now the selected words" />);
	await waitFor(() => expect(painted()[0]).not.toBe(before));
	expect(painted()).toHaveLength(1);
	expect(painted()[0].startContainer.isConnected).toBe(true);
});
