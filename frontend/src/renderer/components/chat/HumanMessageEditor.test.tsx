import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CollapsibleHumanBody } from "./CollapsibleHumanBody";
import { HumanMessageEditor } from "./HumanMessageEditor";
import { TooltipProvider } from "../ui/tooltip";

afterEach(() => vi.restoreAllMocks());

describe("HumanMessageEditor", () => {
	it("sends with Cmd+Enter while Enter inserts a newline", async () => {
		const user = userEvent.setup();
		const onSend = vi.fn(async () => undefined);
		render(
			<TooltipProvider>
				<HumanMessageEditor
					text="original prompt"
					content={[]}
					sessionId="session-1"
					apiBaseUrl={null}
					pending={false}
					busy={false}
					onCancel={vi.fn()}
					onSend={onSend}
				/>
			</TooltipProvider>,
		);
		const editor = screen.getByRole("textbox", { name: "Edit message" });
		expect(editor).toHaveFocus();
		await user.clear(editor);
		await user.type(editor, "line one{enter}line two");
		expect(editor).toHaveValue("line one\nline two");
		fireEvent.keyDown(editor, { key: "Enter", metaKey: true });
		await waitFor(() => expect(onSend).toHaveBeenCalledWith("line one\nline two"));
	});

	it("also sends with Ctrl+Enter and Escape cancels", () => {
		const onSend = vi.fn();
		const onCancel = vi.fn();
		const { rerender } = render(
			<TooltipProvider>
				<HumanMessageEditor text="prompt" content={[]} sessionId="session-1" apiBaseUrl={null} pending={false} busy={false} onCancel={onCancel} onSend={onSend} />
			</TooltipProvider>,
		);
		const editor = screen.getByRole("textbox", { name: "Edit message" });
		fireEvent.keyDown(editor, { key: "Enter", ctrlKey: true });
		expect(onSend).toHaveBeenCalledWith("prompt");
		rerender(
			<TooltipProvider>
				<HumanMessageEditor text="prompt" content={[]} sessionId="session-1" apiBaseUrl={null} pending={false} busy={false} onCancel={onCancel} onSend={onSend} />
			</TooltipProvider>,
		);
		fireEvent.keyDown(screen.getByRole("textbox", { name: "Edit message" }), { key: "Escape" });
		expect(onCancel).toHaveBeenCalledTimes(1);
	});

	it("preserves content chips and disables blank or busy sends", async () => {
		const user = userEvent.setup();
		render(
			<TooltipProvider>
				<HumanMessageEditor
					text="prompt"
					content={[
						{ type: "image", mimeType: "image/png" },
						{ type: "resource", name: "notes.md", uri: "file:///notes.md" },
					]}
					sessionId="session-1"
					apiBaseUrl={null}
					pending={false}
					busy
					onCancel={vi.fn()}
					onSend={vi.fn()}
				/>
			</TooltipProvider>,
		);
		expect(screen.getByText("image/png")).toBeVisible();
		expect(screen.getByText("notes.md")).toBeVisible();
		const send = screen.getByRole("button", { name: "Send edited message" });
		expect(send).toBeDisabled();
		await user.clear(screen.getByRole("textbox", { name: "Edit message" }));
		expect(send).toBeDisabled();
	});

	it("shows staged attachments as the bubble does and writes them back on send", async () => {
		const user = userEvent.setup();
		const onSend = vi.fn();
		const onDraftChange = vi.fn();
		const image = ".ao/attachments/attachment-ab12.png";
		const file = ".ao/attachments/attachment-cd34.pdf";
		const block = (paths: string[]) =>
			`Attached files (read these files in the workspace):\n${paths.map((path) => `- ${path}`).join("\n")}`;
		render(
			<TooltipProvider>
				<HumanMessageEditor
					text={`rename the terminal\n\n${block([image, file])}`}
					content={[{ type: "image", mimeType: "image/png" }]}
					sessionId="session-1"
					apiBaseUrl="http://127.0.0.1:3002"
					pending={false}
					busy={false}
					onDraftChange={onDraftChange}
					onCancel={vi.fn()}
					onSend={onSend}
				/>
			</TooltipProvider>,
		);

		const editor = screen.getByRole("textbox", { name: "Edit message" });
		expect(editor).toHaveValue("rename the terminal");
		// The native image block is the same picture as the image chip.
		expect(screen.queryByText("image/png")).not.toBeInTheDocument();
		const chips = screen.getByRole("list", { name: "Attached files" });
		// As in the bubble, the attachments sit above the text.
		expect(chips.compareDocumentPosition(editor) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		expect(chips.querySelector("img")).toHaveAttribute("alt", "Image 1");
		expect(chips).toHaveTextContent("attachment-cd34.pdf");
		expect(chips.querySelector("img")).toHaveAttribute(
			"src",
			"http://127.0.0.1:3002/api/v1/sessions/session-1/preview/files/.ao/attachments/attachment-ab12.png",
		);
		// An edit resends the original image data, so only a path-only file can go.
		expect(screen.queryByRole("button", { name: "Remove Image 1" })).not.toBeInTheDocument();

		await user.type(editor, " now");
		expect(onDraftChange).toHaveBeenLastCalledWith(`rename the terminal now\n\n${block([image, file])}`);
		await user.click(screen.getByRole("button", { name: "Remove attachment-cd34.pdf" }));
		expect(chips).not.toHaveTextContent("attachment-cd34.pdf");
		expect(onDraftChange).toHaveBeenLastCalledWith(`rename the terminal now\n\n${block([image])}`);

		await user.click(screen.getByRole("button", { name: "Send edited message" }));
		expect(onSend).toHaveBeenCalledWith(`rename the terminal now\n\n${block([image])}`);
	});

	it("keeps typed whitespace when the parent echoes the published draft back", async () => {
		const user = userEvent.setup();
		const onSend = vi.fn();
		function Harness() {
			const [text, setText] = useState(
				"look\n\nAttached files (read these files in the workspace):\n- .ao/attachments/attachment-ab12.png",
			);
			return (
				<TooltipProvider>
					<HumanMessageEditor
						text={text}
						content={[]}
						sessionId="session-1"
						apiBaseUrl={null}
						pending={false}
						busy={false}
						onDraftChange={setText}
						onCancel={vi.fn()}
						onSend={onSend}
					/>
				</TooltipProvider>
			);
		}
		render(<Harness />);
		const editor = screen.getByRole("textbox", { name: "Edit message" });
		// Whitespace alone publishes as just the attachment block; splitting that echo
		// again would wipe the space out from under the cursor.
		fireEvent.change(editor, { target: { value: " " } });
		expect(editor).toHaveValue(" ");
		await user.type(editor, "a");
		expect(editor).toHaveValue(" a");
		await user.click(screen.getByRole("button", { name: "Send edited message" }));
		expect(onSend).toHaveBeenCalledWith(
			" a\n\nAttached files (read these files in the workspace):\n- .ao/attachments/attachment-ab12.png",
		);
	});

	it("keeps Send off until the message changes", async () => {
		const user = userEvent.setup();
		const onSend = vi.fn();
		const block = "Attached files (read these files in the workspace):\n- .ao/attachments/attachment-cd34.pdf";
		render(
			<TooltipProvider>
				<HumanMessageEditor
					text={`prompt\n\n${block}`}
					originalText={`prompt\n\n${block}`}
					content={[]}
					sessionId="session-1"
					apiBaseUrl={null}
					pending={false}
					busy={false}
					onCancel={vi.fn()}
					onSend={onSend}
				/>
			</TooltipProvider>,
		);
		const editor = screen.getByRole("textbox", { name: "Edit message" });
		const send = screen.getByRole("button", { name: "Send edited message" });
		expect(send).toHaveTextContent("Send");
		expect(screen.getByRole("button", { name: "Cancel edit" })).toHaveTextContent("Cancel");
		expect(send).toBeDisabled();
		fireEvent.keyDown(editor, { key: "Enter", metaKey: true });
		expect(onSend).not.toHaveBeenCalled();
		await user.type(editor, "!");
		expect(send).toBeEnabled();
		await user.type(editor, "{backspace}");
		expect(send).toBeDisabled();
		// Dropping a file is a change too.
		await user.click(screen.getByRole("button", { name: "Remove attachment-cd34.pdf" }));
		expect(send).toBeEnabled();
		await user.click(send);
		expect(onSend).toHaveBeenCalledWith("prompt");
	});

	it("counts leading whitespace as a change, since it is sent", async () => {
		const user = userEvent.setup();
		const onSend = vi.fn();
		render(
			<TooltipProvider>
				<HumanMessageEditor
					text="prompt"
					originalText="prompt"
					content={[]}
					sessionId="session-1"
					apiBaseUrl={null}
					pending={false}
					busy={false}
					onCancel={vi.fn()}
					onSend={onSend}
				/>
			</TooltipProvider>,
		);
		const editor = screen.getByRole("textbox", { name: "Edit message" });
		const send = screen.getByRole("button", { name: "Send edited message" });
		fireEvent.change(editor, { target: { value: "prompt  \n" } });
		expect(send).toBeDisabled();
		fireEvent.change(editor, { target: { value: "\n\n  prompt" } });
		expect(send).toBeEnabled();
		await user.click(send);
		expect(onSend).toHaveBeenCalledWith("\n\n  prompt");
	});

	it("never offers to remove an image, even when it shows as a file chip", () => {
		render(
			<TooltipProvider>
				<HumanMessageEditor
					text={"look\n\nAttached files (read these files in the workspace):\n- .ao/attachments/attachment-ab12.png\n- .ao/attachments/attachment-cd34.pdf"}
					content={[]}
					sessionId="session-1"
					apiBaseUrl={null}
					pending={false}
					busy={false}
					onCancel={vi.fn()}
					onSend={vi.fn()}
				/>
			</TooltipProvider>,
		);
		// With no asset URL the image has no preview, but the edit still resends it.
		expect(screen.getByRole("list", { name: "Attached files" })).toHaveTextContent("attachment-ab12.png");
		expect(screen.queryByRole("button", { name: "Remove attachment-ab12.png" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Remove attachment-cd34.pdf" })).toBeInTheDocument();
	});

	it("keeps the clicked label on Send while the send is in flight", () => {
		const props = {
			text: "prompt",
			content: [],
			sessionId: "session-1",
			apiBaseUrl: null,
			busy: false,
			onCancel: vi.fn(),
			onSend: vi.fn(),
		};
		const { rerender } = render(
			<TooltipProvider>
				<HumanMessageEditor {...props} pending={false} />
			</TooltipProvider>,
		);
		// Delivery records the edit for recovery as it starts, which brings a Retry label.
		rerender(
			<TooltipProvider>
				<HumanMessageEditor {...props} pending locked recoveryLabel="Retry edit safely" />
			</TooltipProvider>,
		);
		const send = screen.getByRole("button", { name: "Send edited message" });
		expect(send).toHaveTextContent("Send");
		expect(send).not.toHaveTextContent("Retry");
		// Retry shows only once the send has stalled.
		rerender(
			<TooltipProvider>
				<HumanMessageEditor {...props} pending={false} locked recoveryLabel="Retry edit safely" />
			</TooltipProvider>,
		);
		expect(screen.getByRole("button", { name: "Retry edit safely" })).toHaveTextContent("Retry edit safely");
		// A retry keeps its own label while it runs.
		rerender(
			<TooltipProvider>
				<HumanMessageEditor {...props} pending locked recoveryLabel="Retry edit safely" />
			</TooltipProvider>,
		);
		expect(screen.getByRole("button", { name: "Retry edit safely" })).toHaveTextContent("Retry edit safely");
	});

	it("opens a long message folded, as its bubble was, and scrolls inside the fold", () => {
		const long = "a long message";
		// The bubble measured the message and folded it (see CollapsibleHumanBody.test).
		vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(900);
		render(<CollapsibleHumanBody stateKey={long}><p>{long}</p></CollapsibleHumanBody>).unmount();
		vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(0);
		render(
			<TooltipProvider>
				<HumanMessageEditor
					text={long}
					originalText={long}
					content={[]}
					sessionId="session-1"
					apiBaseUrl={null}
					pending={false}
					busy={false}
					onCancel={vi.fn()}
					onSend={vi.fn()}
				/>
			</TooltipProvider>,
		);
		const toggle = screen.getByRole("button", { name: "Show more" });
		const fold = document.getElementById(toggle.getAttribute("aria-controls")!)!;
		expect(fold).toHaveAttribute("data-folded");
		expect(fold).toHaveAttribute("data-scroll-folded");
		expect(fold.style.height).toBe("220px");
		expect(fold).toContainElement(screen.getByRole("textbox", { name: "Edit message" }));
	});

	it("keeps attachments outside the fold, where the bubble draws them", () => {
		vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(900);
		const text = "a long edited message\n\nAttached files (read these files in the workspace):\n- .ao/attachments/attachment-ab12.png";
		render(
			<TooltipProvider>
				<HumanMessageEditor
					text={text}
					originalText={text}
					content={[]}
					sessionId="session-1"
					apiBaseUrl="http://127.0.0.1:3002"
					pending={false}
					busy={false}
					onCancel={vi.fn()}
					onSend={vi.fn()}
				/>
			</TooltipProvider>,
		);
		const toggle = screen.getByRole("button", { name: "Show more" });
		const fold = document.getElementById(toggle.getAttribute("aria-controls")!)!;
		expect(fold).toContainElement(screen.getByRole("textbox", { name: "Edit message" }));
		expect(fold).not.toContainElement(screen.getByRole("list", { name: "Attached files" }));
	});

	it("explains the branch through the field's description", () => {
		render(
			<TooltipProvider>
				<HumanMessageEditor text="prompt" content={[]} sessionId="session-1" apiBaseUrl={null} pending={false} busy={false} onCancel={vi.fn()} onSend={vi.fn()} />
			</TooltipProvider>,
		);
		expect(screen.getByRole("textbox", { name: "Edit message" })).toHaveAccessibleDescription(
			"Starts a new conversation branch. Files stay as they are.",
		);
	});

	it("retains the editor and shows a mutation error", () => {
		render(
			<TooltipProvider>
				<HumanMessageEditor text="prompt" content={[]} sessionId="session-1" apiBaseUrl={null} pending={false} busy={false} error="branch failed" onCancel={vi.fn()} onSend={vi.fn()} />
			</TooltipProvider>,
		);
		expect(screen.getByRole("textbox", { name: "Edit message" })).toHaveValue("prompt");
		expect(screen.getByRole("alert")).toHaveTextContent("branch failed");
		// On its own line in the box, under the field, not squeezed into the button row.
		expect(screen.getByRole("alert").parentElement).toContainElement(screen.getByRole("textbox", { name: "Edit message" }));
		expect(screen.getByRole("alert").parentElement).not.toContainElement(screen.getByRole("button", { name: "Send edited message" }));
	});
});
