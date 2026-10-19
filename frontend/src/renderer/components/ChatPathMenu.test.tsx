import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import { useUiStore } from "../stores/ui-store";
import { ChatLinkProvider, ChatMarkdown } from "./chat/ChatMarkdown";

afterEach(() => vi.restoreAllMocks());

const paths = ["src/a file.ts"];
const reference = "/other-project/src/a file.ts:12";

function chat(text: string, source: { remoteHost?: boolean; sessionLinkSourceKind?: "cloud"; sessionLinkHostId?: string } = {}) {
	const openFile = vi.fn();
	render(<ChatLinkProvider sessionId="session-1" onFileOpen={openFile} workspacePaths={paths} {...source}>
		<ChatMarkdown text={text} />
	</ChatLinkProvider>);
	return openFile;
}

describe("chat path menus", () => {
	it.each([false, true])("confirms copying only after clipboard success (remote: %s)", async (remoteHost) => {
		let complete!: () => void;
		const pending = new Promise<void>((resolve) => { complete = resolve; });
		if (remoteHost) vi.spyOn(aoBridge.clipboard, "writeText").mockReturnValue(pending);
		else vi.spyOn(aoBridge.editorHandoff, "pathAction").mockReturnValue(pending);
		const toast = vi.spyOn(useUiStore.getState(), "showGlobalToast").mockClear();
		chat("`src/a file.ts`", { remoteHost });
		fireEvent.contextMenu(screen.getByRole("button"));
		await userEvent.click(screen.getByRole("menuitem", { name: "Copy path" }));
		expect(toast).not.toHaveBeenCalled();
		complete();
		await waitFor(() => expect(toast).toHaveBeenCalledExactlyOnceWith("Copied to clipboard", undefined, { tone: "success", placement: "bottom-center", dedupeKey: "path-copied" }));
	});

	it.each([false, true])("does not confirm a failed copy (remote: %s)", async (remoteHost) => {
		if (remoteHost) vi.spyOn(aoBridge.clipboard, "writeText").mockRejectedValue(new Error("Clipboard unavailable"));
		else vi.spyOn(aoBridge.editorHandoff, "pathAction").mockRejectedValue(new Error("Clipboard unavailable"));
		const toast = vi.spyOn(useUiStore.getState(), "showGlobalToast").mockClear();
		chat("`src/a file.ts`", { remoteHost });
		fireEvent.contextMenu(screen.getByRole("button"));
		await userEvent.click(screen.getByRole("menuitem", { name: "Copy path" }));
		await waitFor(() => expect(toast).toHaveBeenCalledExactlyOnceWith("Could not complete the path action", "Clipboard unavailable", "error"));
	});

	it.each(["inline", "link"])("preserves %s clicks and sends the actual path to native actions", async (kind) => {
		const action = vi.spyOn(aoBridge.editorHandoff, "pathAction").mockResolvedValue(undefined);
		const getState = vi.spyOn(aoBridge.editorHandoff, "getState");
		const openFile = chat(kind === "inline" ? `\`${reference}\`` : `[source](</other-project/src/a file.ts:12>)`);
		const trigger = kind === "inline" ? screen.getByRole("button", { name: "Open src/a file.ts in Files" }) : screen.getByRole("link", { name: "source" });
		await userEvent.click(trigger);
		expect(openFile).toHaveBeenCalledExactlyOnceWith("/other-project/src/a file.ts", 12);
		expect(getState).not.toHaveBeenCalled();
		fireEvent.contextMenu(trigger);
		expect(action).not.toHaveBeenCalled();
		await userEvent.click(await screen.findByRole("menuitem", { name: "Reveal in Finder" }));
		expect(action).toHaveBeenLastCalledWith({ sessionId: "session-1", path: "/other-project/src/a file.ts", action: "reveal" });
		fireEvent.contextMenu(trigger);
		await userEvent.click(await screen.findByRole("menuitem", { name: "Open with Cursor" }));
		expect(action).toHaveBeenLastCalledWith({ sessionId: "session-1", path: "/other-project/src/a file.ts", action: "open", editorId: "cursor" });
		fireEvent.contextMenu(trigger);
		await userEvent.click(screen.getByRole("menuitem", { name: "Copy path" }));
		expect(action).toHaveBeenLastCalledWith({ sessionId: "session-1", path: "/other-project/src/a file.ts", action: "copy" });
		expect(openFile).toHaveBeenCalledTimes(1);
	});

	it("offers a menu on an unlisted absolute inline path without adding a click action", async () => {
		const action = vi.spyOn(aoBridge.editorHandoff, "pathAction").mockResolvedValue(undefined);
		const openFile = chat("`/tmp/new folder/file.ts:9`");
		const code = screen.getByText("/tmp/new folder/file.ts:9");
		expect(code.closest("button")).toBeNull();
		await userEvent.click(code);
		expect(openFile).not.toHaveBeenCalled();
		fireEvent.contextMenu(code);
		await userEvent.click(screen.getByRole("menuitem", { name: "Copy path" }));
		expect(action).toHaveBeenCalledWith({ sessionId: "session-1", path: "/tmp/new folder/file.ts", action: "copy" });
	});

	it.each([
		{ remoteHost: true },
		{ sessionLinkHostId: "remote-a" },
		{ sessionLinkSourceKind: "cloud" as const },
	])("never calls local handoff for remote/cloud paths: %j", async (source) => {
		const getState = vi.spyOn(aoBridge.editorHandoff, "getState");
		const action = vi.spyOn(aoBridge.editorHandoff, "pathAction");
		const copy = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		chat(`\`${reference}\``, source);
		fireEvent.contextMenu(screen.getByRole("button"));
		expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Open in AO", "Copy path"]);
		await userEvent.click(screen.getByRole("menuitem", { name: "Copy path" }));
		expect(copy).toHaveBeenCalledExactlyOnceWith("/other-project/src/a file.ts");
		expect(getState).not.toHaveBeenCalled();
		expect(action).not.toHaveBeenCalled();
	});

	it("keeps remote markdown file navigation available with copy-only native actions", async () => {
		const getState = vi.spyOn(aoBridge.editorHandoff, "getState");
		const action = vi.spyOn(aoBridge.editorHandoff, "pathAction");
		const copy = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue(undefined);
		const openFile = chat("[source](src/a%20file.ts#L12)", { remoteHost: true });
		fireEvent.contextMenu(screen.getByRole("link"));
		expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Open in AO", "Copy path"]);
		await userEvent.click(screen.getByRole("menuitem", { name: "Copy path" }));
		expect(copy).toHaveBeenCalledWith("src/a file.ts");
		fireEvent.contextMenu(screen.getByRole("link"));
		await userEvent.click(screen.getByRole("menuitem", { name: "Open in AO" }));
		expect(openFile).toHaveBeenCalledWith("src/a file.ts");
		expect(getState).not.toHaveBeenCalled();
		expect(action).not.toHaveBeenCalled();
	});

	it("offers native actions on explicit file links before the workspace list catches up", async () => {
		const action = vi.spyOn(aoBridge.editorHandoff, "pathAction").mockResolvedValue(undefined);
		chat("[new file](new-file.txt)");
		fireEvent.contextMenu(screen.getByRole("link"));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Reveal in Finder" }));
		expect(action).toHaveBeenCalledWith({ sessionId: "session-1", path: "new-file.txt", action: "reveal" });
	});

	it("normalizes file URLs once without decoding literal percent escapes twice", async () => {
		const action = vi.spyOn(aoBridge.editorHandoff, "pathAction").mockResolvedValue(undefined);
		render(<ChatLinkProvider sessionId="session-1" onFileOpen={vi.fn()} workspacePaths={["src/a%20file.ts"]}>
			<ChatMarkdown text="[source](file:///other-project/src/a%2520file.ts#L12)" />
		</ChatLinkProvider>);
		fireEvent.contextMenu(screen.getByRole("link"));
		await userEvent.click(screen.getByRole("menuitem", { name: "Copy path" }));
		expect(action).toHaveBeenCalledWith({ sessionId: "session-1", path: "/other-project/src/a%20file.ts", action: "copy" });
	});

	it("disables native actions when the workspace is unavailable and lists only installed editors", async () => {
		vi.spyOn(aoBridge.editorHandoff, "getState").mockResolvedValue({
			targets: [{ id: "vscode", name: "VS Code", kind: "editor" }, { id: "file-manager", name: "Finder", kind: "file_manager" }],
			preferredEditorId: "vscode", workspaceAvailable: false,
		});
		const action = vi.spyOn(aoBridge.editorHandoff, "pathAction");
		chat("`src/a file.ts`");
		fireEvent.contextMenu(screen.getByRole("button"));
		const reveal = await screen.findByRole("menuitem", { name: "Reveal in Finder" });
		expect(reveal).toHaveAttribute("aria-disabled", "true");
		expect(screen.getByRole("menuitem", { name: "Open with VS Code" })).toHaveAttribute("aria-disabled", "true");
		expect(screen.queryByRole("menuitem", { name: "Open with Cursor" })).not.toBeInTheDocument();
		await userEvent.click(reveal);
		expect(action).not.toHaveBeenCalled();
	});

	it.each(["inline", "link"])("offers Open in AO without a browser action for %s paths", async (kind) => {
		const openFile = chat(kind === "inline" ? "`src/a file.ts:12`" : "[source](<src/a file.ts:12>)");
		fireEvent.contextMenu(kind === "inline" ? screen.getByRole("button") : screen.getByRole("link"));
		expect(screen.queryByRole("menuitem", { name: "Open in ao browser" })).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("menuitem", { name: "Open in AO" }));
		expect(openFile).toHaveBeenCalledExactlyOnceWith(...(kind === "inline" ? ["src/a file.ts", 12] : ["src/a file.ts"]));
	});

	it("preserves established symbols and automatically uses native icons for other editors", async () => {
		const iconDataUrl = "data:image/png;base64,dGVzdA==";
		vi.spyOn(aoBridge.editorHandoff, "getState").mockResolvedValue({
			targets: [
				{ id: "file-manager", name: "Finder", kind: "file_manager", iconDataUrl },
				{ id: "cursor", name: "Cursor", kind: "editor", iconDataUrl },
				{ id: "vscode", name: "VS Code", kind: "editor", iconDataUrl },
				{ id: "kiro", name: "Kiro", kind: "editor", iconDataUrl },
			],
			preferredEditorId: "cursor", workspaceAvailable: true,
		});
		chat("`src/a file.ts`");
		fireEvent.contextMenu(screen.getByRole("button"));
		for (const name of ["Reveal in Finder", "Open with Cursor", "Open with VS Code"]) {
			const item = await screen.findByRole("menuitem", { name });
			expect(item.querySelector("image")).toBeNull();
			expect(item.querySelector("svg")).not.toBeNull();
		}
		const editor = await screen.findByRole("menuitem", { name: "Open with Kiro" });
		expect(editor.querySelector("image")).toHaveAttribute("href", iconDataUrl);
		fireEvent.error(editor.querySelector("image")!);
		expect(editor.querySelector("image")).toBeNull();
		expect(editor.querySelector("svg")).not.toBeNull();
	});

	it("does not load non-native icon URLs into the menu", async () => {
		vi.spyOn(aoBridge.editorHandoff, "getState").mockResolvedValue({
			targets: [{ id: "vscode", name: "VS Code", kind: "editor", iconDataUrl: "https://example.test/icon.png" }],
			preferredEditorId: "vscode", workspaceAvailable: true,
		});
		chat("`src/a file.ts`");
		fireEvent.contextMenu(screen.getByRole("button"));
		const item = await screen.findByRole("menuitem", { name: "Open with VS Code" });
		expect(item.querySelector("image")).toBeNull();
	});

	it("shows failed native actions to the user", async () => {
		vi.spyOn(aoBridge.editorHandoff, "pathAction").mockRejectedValue(new Error("That path no longer exists."));
		const toast = vi.spyOn(useUiStore.getState(), "showGlobalToast").mockClear();
		chat("`src/a file.ts`");
		fireEvent.contextMenu(screen.getByRole("button"));
		await userEvent.click(await screen.findByRole("menuitem", { name: "Reveal in Finder" }));
		await waitFor(() => expect(toast).toHaveBeenCalledWith("Could not complete the path action", "That path no longer exists.", "error"));
	});
});
