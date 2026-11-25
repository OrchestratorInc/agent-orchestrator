import { fireEvent, render as rtlRender, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { draftStagedPaths, readChatSessionDraft, writeChatAttachments } from "../../lib/chat-drafts";
import { useUiStore } from "../../stores/ui-store";
import { TooltipProvider } from "../ui/tooltip";
import { ChatComposer } from "./ChatComposer";

function render(ui: ReactElement) {
	return rtlRender(ui, { wrapper: TooltipProvider });
}

function clipboardData(files: File[]) {
	return { files, items: [], getData: () => "", setData: () => undefined };
}

const textFile = (name: string) => new File(["hello"], name, { type: "text/plain" });

const releasedPaths = (leases: ReturnType<typeof vi.fn>) =>
	leases.mock.calls.flatMap(([release]) => release as string[]);

beforeEach(() => useUiStore.setState({ chatSendKeyMode: "enter" }));

describe("draft attachment leases", () => {
	it("releases the staged file of a chip the person removes", async () => {
		const path = ".ao/attachments/attachment-0a1b2c3d.txt";
		const stage = vi.fn().mockResolvedValue([path]);
		const leases = vi.fn().mockResolvedValue([]);
		render(<ChatComposer onSend={vi.fn()} draftSessionId="leases-remove" onStageAttachments={stage} onUpdateAttachmentLeases={leases} />);
		fireEvent.paste(screen.getByLabelText("Message the agent"), { clipboardData: clipboardData([textFile("drop.txt")]) });
		await userEvent.click(await screen.findByRole("button", { name: "Remove drop.txt" }));
		await waitFor(() => expect(leases).toHaveBeenCalledWith([path]));
		expect(readChatSessionDraft("leases-remove").composer.attachments).toEqual([]);
	});

	it("never releases a file because its message was sent", async () => {
		const path = ".ao/attachments/attachment-4e5f6a7b.txt";
		const stage = vi.fn().mockResolvedValue([path]);
		const leases = vi.fn().mockResolvedValue([]);
		const onSend = vi.fn().mockResolvedValue(undefined);
		render(<ChatComposer onSend={onSend} draftSessionId="leases-send" onStageAttachments={stage} onUpdateAttachmentLeases={leases} />);
		fireEvent.paste(screen.getByLabelText("Message the agent"), { clipboardData: clipboardData([textFile("keep.txt")]) });
		await screen.findByRole("button", { name: "Remove keep.txt" });
		await waitFor(() => expect(screen.getByRole("button", { name: "Send message" })).toBeEnabled());
		await userEvent.click(screen.getByRole("button", { name: "Send message" }));
		await waitFor(() => expect(onSend).toHaveBeenCalled());
		await waitFor(() => expect(screen.queryByRole("button", { name: "Remove keep.txt" })).not.toBeInTheDocument());
		expect(releasedPaths(leases)).not.toContain(path);
	});

	it("renews a restored draft's files and drops the ones already reclaimed", async () => {
		const sessionId = "leases-restore";
		const kept = ".ao/attachments/attachment-kept01.txt";
		const gone = ".ao/attachments/attachment-gone01.txt";
		writeChatAttachments(sessionId, [
			{ id: "kept", name: "kept.txt", mimeType: "text/plain", bytes: 5, path: kept },
			{ id: "gone", name: "gone.txt", mimeType: "text/plain", bytes: 5, path: gone },
		]);
		const leases = vi.fn().mockResolvedValue([gone]);
		render(<ChatComposer onSend={vi.fn()} draftSessionId={sessionId} onStageAttachments={vi.fn()} onUpdateAttachmentLeases={leases} />);
		await waitFor(() => expect(leases).toHaveBeenCalledWith([], [kept, gone]));
		await waitFor(() => expect(screen.queryByRole("button", { name: "Remove gone.txt" })).not.toBeInTheDocument());
		expect(screen.getByRole("button", { name: "Remove kept.txt" })).toBeInTheDocument();
		// Dropping a reclaimed chip is not a release of anything still held.
		expect(releasedPaths(leases)).not.toContain(kept);
	});

	it("does not renew AO Cloud references", async () => {
		const sessionId = "leases-cloud";
		writeChatAttachments(sessionId, [
			{ id: "cloud", name: "c.txt", mimeType: "text/plain", bytes: 5, path: "ao-cloud-attachment:6f1c2a8e-0b1d-4c3e-9f5a-2d7e8b9c0a11/c.txt" },
		]);
		const leases = vi.fn().mockResolvedValue([]);
		render(<ChatComposer onSend={vi.fn()} draftSessionId={sessionId} onStageAttachments={vi.fn()} onUpdateAttachmentLeases={leases} />);
		await screen.findByRole("button", { name: "Remove c.txt" });
		expect(leases).not.toHaveBeenCalled();
	});
});

describe("draftStagedPaths", () => {
	it("names the composer's files only when asked", () => {
		const sessionId = "leases-held";
		const path = ".ao/attachments/attachment-held01.txt";
		writeChatAttachments(sessionId, [{ id: "held", name: "held.txt", mimeType: "text/plain", bytes: 5, path }]);
		const draft = readChatSessionDraft(sessionId);
		expect([...draftStagedPaths(draft, { composer: true })]).toEqual([path]);
		expect([...draftStagedPaths(draft, { composer: false })]).toEqual([]);
	});
});
