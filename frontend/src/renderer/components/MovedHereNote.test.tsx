import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { useMovedHereStore } from "../lib/move-finish";
import { MovedHereNote } from "./MovedHereNote";

const { navigate, localWorkspaces } = vi.hoisted(() => ({ navigate: vi.fn(), localWorkspaces: { current: [] as unknown[] } }));
vi.mock("../lib/navigate-to-session", () => ({ useNavigateToSession: () => navigate }));
vi.mock("../hooks/useWorkspaceQuery", () => ({ useWorkspaceQuery: () => ({ data: localWorkspaces.current }) }));

const movedAway = { id: "cloud-1", cloud: { orgId: "org-1", movedAway: true as const } };

beforeEach(() => {
	navigate.mockReset();
	localWorkspaces.current = [];
	useMovedHereStore.setState({ moves: {} });
});

it("says a Cloud session moved to this computer and opens it here", async () => {
	useMovedHereStore.getState().record("org-1", "cloud-1", { projectId: "app", sessionId: "app-9" });
	localWorkspaces.current = [{ id: "app", sessions: [{ id: "app-9" }] }];
	// Inside an archived card, whose own click opens the Cloud copy.
	const openCard = vi.fn();
	render(<div onClick={openCard}><MovedHereNote session={movedAway} /></div>);
	expect(screen.getByText("Moved to this computer")).toBeTruthy();
	await userEvent.setup().click(screen.getByRole("button", { name: "Open session" }));
	expect(navigate).toHaveBeenCalledWith("app", "app-9");
	expect(openCard).not.toHaveBeenCalled();
});

it("says it continues on another computer when this one has no session of it", () => {
	// Moved here once, but that session is gone: no link to it.
	useMovedHereStore.getState().record("org-1", "cloud-1", { projectId: "app", sessionId: "app-9" });
	render(<MovedHereNote session={movedAway} />);
	expect(screen.getByText(
		"This session moved to another computer and continues there. To use Orchestrator Cloud again, move it from that computer.",
	)).toBeTruthy();
	expect(screen.queryByRole("button")).toBeNull();
});

it("renders nothing for a Cloud session that did not move away", () => {
	const { container } = render(<MovedHereNote session={{ id: "cloud-1", cloud: { orgId: "org-1" } }} />);
	expect(container.innerHTML).toBe("");
});

it("keeps at most the newest 200 moves", () => {
	const record = useMovedHereStore.getState().record;
	for (let n = 0; n < 205; n++) record("org-1", `cloud-${n}`, { projectId: "app", sessionId: `app-${n}` });
	const moves = useMovedHereStore.getState().moves;
	expect(Object.keys(moves)).toHaveLength(200);
	expect(moves["org-1:cloud-204"]).toBeTruthy();
});
