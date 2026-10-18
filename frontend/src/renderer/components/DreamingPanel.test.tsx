import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { DreamingRow } from "./DreamingPanel";

type Instructions = {
	projectId?: string;
	body: string;
	revision: number;
	updatedAt?: string;
	updatedBy?: { sessionId?: string; title?: string; dreamed: boolean };
	previousBody?: string;
	protectedLines: string[];
	removedLines: string[];
};

const mocks = vi.hoisted(() => ({
	instructions: {} as Record<string, Instructions>,
	save: vi.fn(),
	create: vi.fn(),
	update: vi.fn(),
	navigate: vi.fn(),
}));

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => mocks.navigate }));
vi.mock("../hooks/useInstructions", () => ({
	useInstructions: (projectId: string | null) => ({ data: mocks.instructions[projectId ?? ""], isLoading: false, error: null }),
	useSaveInstructions: () => ({ mutate: mocks.save, isPending: false, error: null }),
}));
vi.mock("../hooks/useAutomations", () => ({
	useCreateAutomation: () => ({ mutateAsync: mocks.create }),
	useUpdateAutomation: () => ({ mutateAsync: mocks.update }),
}));

const projects = [{ id: "notes", name: "Notes" }, { id: "web", name: "Web" }];
const noop = () => {};
function DreamingPanel({ automations, projects: list, expanded = true }: { automations: unknown[]; projects: typeof projects; expanded?: boolean }) {
	return <DreamingRow automations={automations as never} projects={list} expanded={expanded} onExpand={noop} onEdit={noop} />;
}
const empty = (projectId?: string): Instructions => ({ projectId, body: "", revision: 0, protectedLines: [], removedLines: [] });
const dreaming = (projectId: string, enabled: boolean) => ({
	id: `dream-${projectId}`, projectId, purpose: "dreaming", enabled, displayName: "Dreaming", prompt: "Learn.",
	kind: "worker", trigger: "session_ended", rrule: "", timezone: "UTC", nextRunAt: "2026-10-17T09:00:00Z",
	createdAt: "2026-10-17T09:00:00Z", updatedAt: "2026-10-17T09:00:00Z",
});

describe("DreamingRow", () => {
	beforeEach(() => {
		mocks.instructions = { "": empty(), notes: empty("notes"), web: empty("web") };
		mocks.save.mockReset();
		mocks.create.mockReset().mockResolvedValue({});
		mocks.update.mockReset().mockResolvedValue({});
		mocks.navigate.mockReset();
	});

	it("saves what the user types, with the button or Cmd+Enter", async () => {
		const user = userEvent.setup();
		render(<DreamingPanel automations={[]} projects={projects} />);
		const save = screen.getByRole("button", { name: "Save" });
		expect(save).toBeDisabled();
		const box = screen.getByRole("textbox", { name: "Instructions for You (every project)" });
		await user.type(box, "Answer first.");
		expect(save).toBeEnabled();
		await user.click(save);
		expect(mocks.save).toHaveBeenCalledWith("Answer first.", expect.anything());
		await user.type(box, "{Meta>}{Enter}{/Meta}");
		expect(mocks.save).toHaveBeenCalledTimes(2);
	});

	it("says when dreaming changed the text, links the dream, and undoes it", async () => {
		const user = userEvent.setup();
		mocks.instructions[""] = {
			...empty(), body: "Answer first.\nAsk before committing.", revision: 4, updatedAt: new Date().toISOString(),
			updatedBy: { sessionId: "dream-1", title: "Dreaming: Fix note ordering", dreamed: true }, previousBody: "Answer first.",
		};
		render(<DreamingPanel automations={[]} projects={projects} />);
		expect(screen.getByRole("textbox", { name: /You/ })).toHaveValue("Answer first.\nAsk before committing.");
		expect(screen.getByText(/Dreaming updated this just now/)).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "View session" }));
		expect(mocks.navigate).toHaveBeenCalledWith({ to: "/sessions/$sessionId", params: { sessionId: "dream-1" } });
		await user.click(screen.getByRole("button", { name: "Undo" }));
		expect(mocks.save).toHaveBeenCalledWith("Answer first.", expect.anything());
	});

	it("keeps the user's draft when the text changes underneath, until they ask for the new version", async () => {
		const user = userEvent.setup();
		mocks.instructions[""] = { ...empty(), body: "Answer first.", revision: 1 };
		const view = render(<DreamingPanel automations={[]} projects={projects} />);
		const box = screen.getByRole("textbox", { name: /You/ });
		await user.type(box, " Always.");
		mocks.instructions[""] = { ...empty(), body: "Answer first.\nPrefer small PRs.", revision: 2, updatedBy: { dreamed: true } };
		view.rerender(<DreamingPanel automations={[]} projects={projects} />);
		expect(box).toHaveValue("Answer first. Always.");
		expect(screen.getByText(/This changed while you were editing/)).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Show the new version" }));
		expect(box).toHaveValue("Answer first.\nPrefer small PRs.");
	});

	it("turns dreaming on in every project: creates the missing one and enables the paused one", async () => {
		const user = userEvent.setup();
		render(<DreamingPanel automations={[dreaming("web", false)] as never} projects={projects} />);
		const toggle = screen.getByRole("switch", { name: "Dreaming in every project" });
		expect(toggle).not.toBeChecked();
		await user.click(toggle);
		expect(mocks.create).toHaveBeenCalledTimes(1);
		expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({ projectId: "notes", purpose: "dreaming", trigger: "session_ended", prompt: "" }));
		expect(mocks.update).toHaveBeenCalledWith({ id: "dream-web", body: { enabled: true } });
	});

	it("shows a project's own instructions and turns dreaming on for just that project", async () => {
		const user = userEvent.setup();
		mocks.instructions.notes = { ...empty("notes"), body: "Run python3 -m unittest.", revision: 2 };
		render(<DreamingPanel automations={[dreaming("web", true)] as never} projects={projects} />);
		expect(screen.getByText("On in 1 of 2 projects")).toBeInTheDocument();
		await user.click(screen.getByRole("combobox", { name: "Instructions for" }));
		await user.click(within(screen.getByRole("listbox")).getByRole("option", { name: "Notes" }));
		expect(screen.getByRole("textbox", { name: "Instructions for Notes" })).toHaveValue("Run python3 -m unittest.");
		expect(screen.getByText("Dreaming is off for this project.")).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Turn on" }));
		expect(mocks.create).toHaveBeenCalledTimes(1);
		expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({ projectId: "notes", purpose: "dreaming" }));
		expect(mocks.update).not.toHaveBeenCalled();
	});

	it("is one built-in row that opens to the instructions and has no delete action", () => {
		const view = render(<DreamingPanel automations={[dreaming("notes", true), dreaming("web", true)]} projects={projects} expanded={false} />);
		expect(screen.getByRole("heading", { name: "Dreaming" })).toBeInTheDocument();
		expect(screen.getByText("Every project")).toBeInTheDocument();
		expect(screen.getByText("After a session ends")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Open Dreaming" })).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /Delete/ })).not.toBeInTheDocument();
		view.rerender(<DreamingPanel automations={[dreaming("notes", true), dreaming("web", true)]} projects={projects} />);
		expect(screen.getByRole("textbox", { name: "Instructions for You (every project)" })).toBeInTheDocument();
		expect(screen.queryByText(/Run history/)).not.toBeInTheDocument();
	});
});
