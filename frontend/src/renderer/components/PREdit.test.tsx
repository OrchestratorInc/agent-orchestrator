import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { PREditConflict, type PREditContent, type usePREdit } from "../hooks/usePREdit";
import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { noPRDetails } from "../lib/pr-display";
import { PREditableDescription, PREditableTitle, PR_GITHUB_BODY_MAX_LENGTH } from "./PREdit";

const summary = (overrides: Partial<SessionPRSummary> = {}): SessionPRSummary => ({
	...noPRDetails(),
	url: "https://api.github.com/repos/acme/repo/pulls/7",
	htmlUrl: "https://github.com/acme/repo/pull/7",
	number: 7,
	title: "Fix dashboard",
	state: "open",
	provider: "github",
	repo: "acme/repo",
	author: "ada",
	sourceBranch: "fix/dashboard",
	targetBranch: "main",
	headSha: "abc123",
	additions: 10,
	deletions: 3,
	changedFiles: 2,
	ci: { autoInjectCI: true, state: "passing", failingChecks: [], checks: [] },
	review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [] },
	mergeability: { state: "mergeable", reasons: [], prUrl: "https://github.com/acme/repo/pull/7", conflictFiles: [] },
	updatedAt: "2026-10-22T00:00:00Z",
	detailsObservedAt: "2026-10-22T00:00:00Z",
	description: "Stored body",
	...overrides,
});

type Edit = ReturnType<typeof usePREdit>;
type Save = { base: string; field: "title" | "body"; value: string };

/** The hook's surface, with the provider's answers scripted. */
function fakeEdit(content: PREditContent = { title: "Fix dashboard", body: "Provider body\r\nline two", canEdit: true }, accessKnown = true) {
	const state = { content };
	const load = vi.fn(async () => state.content);
	const mutateAsync = vi.fn(async (_input: Save) => ({ ok: true, prNumber: 7, title: "", body: "", updatedAt: "" }));
	const edit = { access: { data: accessKnown ? content : undefined }, load, save: { isPending: false, mutateAsync } } as unknown as Edit;
	return { edit, load, mutateAsync, state };
}

describe("PREditableTitle", () => {
	it("is the plain title, with no pencil, when the PR can't be edited here", () => {
		render(<PREditableTitle pr={summary()} />);
		expect(screen.getByRole("heading", { name: "Fix dashboard" })).toBeInTheDocument();
		expect(screen.queryByRole("button")).not.toBeInTheDocument();
	});

	it("edits in place from the provider's current title and saves on Enter", async () => {
		const { edit, load, mutateAsync } = fakeEdit({ title: "Fix dashboard (renamed elsewhere)", body: "", canEdit: true });
		render(<PREditableTitle edit={edit} pr={summary()} />);
		await userEvent.click(screen.getByRole("button", { name: "Edit title" }));
		const input = await screen.findByRole("textbox", { name: "Pull request title" }) as HTMLInputElement;
		expect(load).toHaveBeenCalledTimes(1);
		expect(input.value).toBe("Fix dashboard (renamed elsewhere)");
		await userEvent.clear(input);
		await userEvent.type(input, "  Fix the dashboard  {Enter}");
		await waitFor(() => expect(mutateAsync).toHaveBeenCalledWith({ base: "Fix dashboard (renamed elsewhere)", field: "title", value: "Fix the dashboard" }));
		await waitFor(() => expect(screen.queryByRole("textbox")).not.toBeInTheDocument());
	});

	it("saves on ⌘↵, and Escape cancels only the editor", async () => {
		const { edit, mutateAsync } = fakeEdit();
		const onOuterKeyDown = vi.fn();
		render(<div onKeyDown={onOuterKeyDown}><PREditableTitle edit={edit} pr={summary()} /></div>);
		await userEvent.click(screen.getByRole("button", { name: "Edit title" }));
		let input = await screen.findByRole("textbox", { name: "Pull request title" });
		// Untouched, Escape closes it at once.
		fireEvent.keyDown(input, { key: "Escape" });
		expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
		expect(onOuterKeyDown).not.toHaveBeenCalledWith(expect.objectContaining({ key: "Escape" }));

		// Changed, Escape asks first; a second Escape goes back to the draft.
		await userEvent.click(screen.getByRole("button", { name: "Edit title" }));
		input = await screen.findByRole("textbox", { name: "Pull request title" });
		await userEvent.type(input, " now");
		fireEvent.keyDown(input, { key: "Escape" });
		expect(screen.getByText("Discard your changes?")).toBeInTheDocument();
		expect((input as HTMLInputElement).value).toBe("Fix dashboard now");
		fireEvent.keyDown(input, { key: "Escape" });
		expect(screen.queryByText("Discard your changes?")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Save" })).toBeInTheDocument();
		fireEvent.keyDown(input, { key: "Escape" });
		await userEvent.click(screen.getByRole("button", { name: "Discard" }));
		expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
		expect(onOuterKeyDown).not.toHaveBeenCalledWith(expect.objectContaining({ key: "Escape" }));
		expect(mutateAsync).not.toHaveBeenCalled();

		await userEvent.click(screen.getByRole("button", { name: "Edit title" }));
		input = await screen.findByRole("textbox", { name: "Pull request title" });
		await userEvent.type(input, " now");
		fireEvent.keyDown(input, { key: "Enter", metaKey: true });
		await waitFor(() => expect(mutateAsync).toHaveBeenCalledWith({ base: "Fix dashboard", field: "title", value: "Fix dashboard now" }));
	});

	it("refuses an empty title and closes without a request when nothing changed", async () => {
		const { edit, mutateAsync } = fakeEdit();
		render(<PREditableTitle edit={edit} pr={summary()} />);
		await userEvent.click(screen.getByRole("button", { name: "Edit title" }));
		const input = await screen.findByRole("textbox", { name: "Pull request title" });
		await userEvent.clear(input);
		await userEvent.click(screen.getByRole("button", { name: "Save" }));
		expect(screen.getByRole("alert")).toHaveTextContent("A title is required.");
		await userEvent.type(input, "Fix dashboard");
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Save" }));
		await waitFor(() => expect(screen.queryByRole("textbox")).not.toBeInTheDocument());
		expect(mutateAsync).not.toHaveBeenCalled();
	});

	it("greys the pencil out with the reason when the PR is merged, closed, or not the viewer's to edit", async () => {
		const { edit, load } = fakeEdit();
		const { rerender } = render(<PREditableTitle edit={edit} pr={summary({ state: "merged" })} />);
		let button = screen.getByRole("button", { name: "Edit title" });
		expect(button).toHaveAttribute("aria-disabled", "true");
		expect(button).toHaveAttribute("title", "A merged or closed pull request can't be edited here.");
		await userEvent.click(button);
		expect(load).not.toHaveBeenCalled();
		expect(screen.queryByRole("textbox")).not.toBeInTheDocument();

		rerender(<PREditableTitle edit={edit} pr={summary({ state: "closed" })} />);
		expect(screen.getByRole("button", { name: "Edit title" })).toHaveAttribute("aria-disabled", "true");

		const denied = fakeEdit({ title: "Fix dashboard", body: "", canEdit: false, reason: "no_permission" });
		rerender(<PREditableTitle edit={denied.edit} pr={summary()} />);
		button = screen.getByRole("button", { name: "Edit title" });
		expect(button).toHaveAttribute("aria-disabled", "true");
		expect(button).toHaveAttribute("title", "Only the pull request's author or someone with write access to the repository can edit it.");
	});

	it("says why inline when the provider only refuses once the editor is opened", async () => {
		const { edit } = fakeEdit({ title: "Fix dashboard", body: "", canEdit: false, reason: "no_permission" }, false);
		render(<PREditableTitle edit={edit} pr={summary()} />);
		await userEvent.click(screen.getByRole("button", { name: "Edit title" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Only the pull request's author");
		expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
	});

	it("shows a failed save inline and keeps the draft", async () => {
		const { edit, mutateAsync } = fakeEdit();
		mutateAsync.mockRejectedValueOnce(new Error("The SCM provider rejected the PR action"));
		render(<PREditableTitle edit={edit} pr={summary()} />);
		await userEvent.click(screen.getByRole("button", { name: "Edit title" }));
		const input = await screen.findByRole("textbox", { name: "Pull request title" }) as HTMLInputElement;
		await userEvent.type(input, " now{Enter}");
		expect(await screen.findByRole("alert")).toHaveTextContent("The SCM provider rejected the PR action");
		expect(input.value).toBe("Fix dashboard now");
	});
});

const renderDescription = (edit: Edit, pr = summary()) => render(
	<PREditableDescription edit={edit} pr={pr}><p>Stored body</p></PREditableDescription>,
);

describe("PREditableDescription", () => {
	it("shows the description with an Edit, and opens on the provider's whole body", async () => {
		const { edit, load } = fakeEdit();
		renderDescription(edit);
		expect(screen.getByText("Stored body")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Edit" }));
		const box = await screen.findByRole("textbox", { name: "Edit description" }) as HTMLTextAreaElement;
		expect(load).toHaveBeenCalledTimes(1);
		// Not the stored copy, which is cut to a cap and can be a poll behind.
		expect(box.value).toBe("Provider body\nline two");
		expect(screen.queryByText("Stored body")).not.toBeInTheDocument();
	});

	it("previews the draft as the description renders, and keeps it when going back to Write", async () => {
		const { edit } = fakeEdit({ title: "T", body: "", canEdit: true });
		renderDescription(edit);
		await userEvent.click(screen.getByRole("button", { name: "Edit" }));
		const box = await screen.findByRole("textbox", { name: "Edit description" }) as HTMLTextAreaElement;
		await userEvent.click(screen.getByRole("tab", { name: "Preview" }));
		expect(screen.getByText("Nothing to preview.")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("tab", { name: "Write" }));
		fireEvent.change(box, { target: { value: "## Why\n\nIt was **slow**.\n\n<script>window.__pwned = true</script>" } });
		await userEvent.click(screen.getByRole("tab", { name: "Preview" }));
		expect(screen.getByRole("heading", { name: "Why" })).toBeInTheDocument();
		expect(screen.getByText("slow").tagName).toBe("STRONG");
		expect(document.querySelector("script")).toBeNull();
		await userEvent.click(screen.getByRole("tab", { name: "Write" }));
		expect(box.value).toContain("It was **slow**.");
	});

	it("saves on ⌘↵ with the text it started from, formats with ⌘B, and can clear the description", async () => {
		const { edit, mutateAsync } = fakeEdit();
		renderDescription(edit);
		await userEvent.click(screen.getByRole("button", { name: "Edit" }));
		let box = await screen.findByRole("textbox", { name: "Edit description" }) as HTMLTextAreaElement;
		fireEvent.change(box, { target: { value: "make it fast" } });
		box.setSelectionRange(8, 12);
		fireEvent.keyDown(box, { key: "b", metaKey: true });
		await waitFor(() => expect(box.value).toBe("make it **fast**"));
		fireEvent.keyDown(box, { key: "Enter", ctrlKey: true });
		await waitFor(() => expect(mutateAsync).toHaveBeenCalledWith({ base: "Provider body\r\nline two", field: "body", value: "make it **fast**" }));
		await waitFor(() => expect(screen.queryByRole("textbox")).not.toBeInTheDocument());

		await userEvent.click(screen.getByRole("button", { name: "Edit" }));
		box = await screen.findByRole("textbox", { name: "Edit description" }) as HTMLTextAreaElement;
		fireEvent.change(box, { target: { value: "" } });
		await userEvent.click(screen.getByRole("button", { name: "Save" }));
		await waitFor(() => expect(mutateAsync).toHaveBeenLastCalledWith({ base: "Provider body\r\nline two", field: "body", value: "" }));
	});

	it("Escape from Write or Preview asks before discarding a changed draft, and stays inside the editor", async () => {
		const { edit, mutateAsync } = fakeEdit();
		const onOuterKeyDown = vi.fn();
		render(<div onKeyDown={onOuterKeyDown}><PREditableDescription edit={edit} pr={summary()}><p>Stored body</p></PREditableDescription></div>);
		await userEvent.click(screen.getByRole("button", { name: "Edit" }));
		const box = await screen.findByRole("textbox", { name: "Edit description" });
		fireEvent.change(box, { target: { value: "draft" } });
		await userEvent.click(screen.getByRole("tab", { name: "Preview" }));
		fireEvent.keyDown(screen.getByRole("tab", { name: "Preview" }), { key: "Escape" });
		expect(screen.getByTestId("pr-edit-discard-confirm")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Keep editing" }));
		await userEvent.click(screen.getByRole("tab", { name: "Write" }));
		expect((screen.getByRole("textbox", { name: "Edit description" }) as HTMLTextAreaElement).value).toBe("draft");

		// Typing dismisses the question; Cancel asks it again, and Discard closes.
		await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
		expect(screen.getByTestId("pr-edit-discard-confirm")).toBeInTheDocument();
		fireEvent.change(screen.getByRole("textbox", { name: "Edit description" }), { target: { value: "draft two" } });
		expect(screen.queryByTestId("pr-edit-discard-confirm")).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
		await userEvent.click(screen.getByRole("button", { name: "Discard" }));
		expect(screen.queryByTestId("pr-description-editor")).not.toBeInTheDocument();
		expect(screen.getByText("Stored body")).toBeInTheDocument();
		expect(onOuterKeyDown).not.toHaveBeenCalledWith(expect.objectContaining({ key: "Escape" }));
		expect(mutateAsync).not.toHaveBeenCalled();

		// An untouched editor closes on Cancel without asking.
		await userEvent.click(screen.getByRole("button", { name: "Edit" }));
		await userEvent.click(await screen.findByRole("button", { name: "Cancel" }));
		expect(screen.queryByTestId("pr-description-editor")).not.toBeInTheDocument();
	});

	it("on a conflict keeps the draft, says so, and only replaces the newer text when saved again", async () => {
		const { edit, load, mutateAsync, state } = fakeEdit();
		renderDescription(edit);
		await userEvent.click(screen.getByRole("button", { name: "Edit" }));
		const box = await screen.findByRole("textbox", { name: "Edit description" }) as HTMLTextAreaElement;
		fireEvent.change(box, { target: { value: "mine" } });
		// Someone edits the description on the provider while this editor is open.
		state.content = { title: "Fix dashboard", body: "theirs", canEdit: true };
		mutateAsync.mockRejectedValueOnce(new PREditConflict("Someone changed this while you were editing."));
		await userEvent.click(screen.getByRole("button", { name: "Save" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Someone changed this while you were editing.");
		expect(mutateAsync).toHaveBeenLastCalledWith({ base: "Provider body\r\nline two", field: "body", value: "mine" });
		expect(box.value).toBe("mine");
		expect(load).toHaveBeenCalledTimes(2);

		// Typing leaves the warning up; the second save is against the newer text.
		fireEvent.change(box, { target: { value: "mine, merged with theirs" } });
		expect(screen.getByRole("alert")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Save anyway" }));
		await waitFor(() => expect(mutateAsync).toHaveBeenLastCalledWith({ base: "theirs", field: "body", value: "mine, merged with theirs" }));
		await waitFor(() => expect(screen.queryByTestId("pr-description-editor")).not.toBeInTheDocument());
	});

	it("refuses a description over GitHub's limit before sending it", async () => {
		const { edit, mutateAsync } = fakeEdit({ title: "T", body: "", canEdit: true });
		renderDescription(edit);
		await userEvent.click(screen.getByRole("button", { name: "Edit" }));
		const box = await screen.findByRole("textbox", { name: "Edit description" });
		fireEvent.change(box, { target: { value: "x".repeat(PR_GITHUB_BODY_MAX_LENGTH + 1) } });
		await userEvent.click(screen.getByRole("button", { name: "Save" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("GitHub's limit");
		expect(mutateAsync).not.toHaveBeenCalled();
	});

	it("greys Edit out with the reason on a closed PR and shows a failed load inline", async () => {
		const closed = fakeEdit();
		const { unmount } = renderDescription(closed.edit, summary({ state: "closed" }));
		const button = screen.getByRole("button", { name: "Edit" });
		expect(button).toHaveAttribute("aria-disabled", "true");
		expect(button).toHaveAttribute("title", "A merged or closed pull request can't be edited here.");
		await userEvent.click(button);
		expect(closed.load).not.toHaveBeenCalled();
		unmount();

		const failing = fakeEdit();
		failing.load.mockRejectedValueOnce(new Error("Couldn't load this pull request for editing."));
		renderDescription(failing.edit);
		await userEvent.click(screen.getByRole("button", { name: "Edit" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't load this pull request for editing.");
		expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
	});
});
