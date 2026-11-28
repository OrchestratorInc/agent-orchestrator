import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { MovedToCloudNote } from "./MovedToCloudNote";

const { navigate, cloudSessions } = vi.hoisted(() => ({ navigate: vi.fn(), cloudSessions: { current: [] as unknown[] } }));
vi.mock("../lib/navigate-to-session", () => ({ useNavigateToSession: () => navigate }));
vi.mock("../hooks/useWorkspaceQuery", () => ({ useCloudSessionsQuery: () => ({ data: cloudSessions.current }) }));

beforeEach(() => {
	navigate.mockReset();
	cloudSessions.current = [];
});

it("says a moved session continues in Orchestrator Cloud and opens it there", async () => {
	cloudSessions.current = [{ id: "cloud-9", orgId: "org-1", projectId: "cloud-proj" }];
	// Inside an archived card, whose own click opens the session here.
	const openCard = vi.fn();
	render(<div onClick={openCard}><MovedToCloudNote session={{ movedTo: { orgId: "org-1", sessionId: "cloud-9" } }} /></div>);
	expect(screen.getByText("This session continues in Orchestrator Cloud, so it can't be restored here.")).toBeTruthy();
	await userEvent.setup().click(screen.getByRole("button", { name: "Open in Orchestrator Cloud" }));
	expect(navigate).toHaveBeenCalledWith("cloud-proj", "cloud-9");
	expect(openCard).not.toHaveBeenCalled();
});

it("offers no link to a Cloud session this account cannot see", () => {
	render(<MovedToCloudNote session={{ movedTo: { orgId: "org-2", sessionId: "cloud-9" } }} />);
	expect(screen.getByTestId("moved-to-cloud")).toBeTruthy();
	expect(screen.queryByRole("button")).toBeNull();
});

it("renders nothing for a session that did not move", () => {
	const { container } = render(<MovedToCloudNote session={{}} />);
	expect(container.innerHTML).toBe("");
});
