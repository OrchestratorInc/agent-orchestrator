import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { RemoteHostsSection } from "./RemoteHostsSection";
import { SidebarMenu, SidebarMenuItem, SidebarProvider } from "./ui/sidebar";

it("keeps host-specific offline controls around shared project rows", () => {
	const onRetry = vi.fn();
	const onUpdatePassword = vi.fn();
	const renderProject = vi.fn((host: { hostId: string }, workspace: { name: string }) =>
		<SidebarMenuItem data-testid={`project-${host.hostId}`}>{workspace.name}</SidebarMenuItem>);
	render(<SidebarProvider><SidebarMenu><RemoteHostsSection
		hosts={[
			{ hostId: "box-a", label: "Box A", url: "https://a.test", status: "connected" },
			{ hostId: "box-b", label: "Box B", url: "https://b.test", status: "offline", failureReason: "unauthorized" },
			{ hostId: "box-c", label: "Box C", url: "https://c.test", status: "offline" },
		]}
		workspaces={[
			{ hostId: "box-a", id: "shared", name: "Project A", path: "/a", sessions: [] },
			{ hostId: "box-b", id: "shared", name: "Project B", path: "/b", sessions: [] },
		]}
		renderProject={renderProject}
		onRetry={onRetry}
		onUpdatePassword={onUpdatePassword}
	/></SidebarMenu></SidebarProvider>);
	expect(screen.getByTestId("project-box-a")).toHaveTextContent("Project A");
	expect(screen.queryByTestId("project-box-b")).toBeNull();
	expect(screen.queryByRole("button", { name: "New project on Box A" })).toBeNull();
	// A rejected password is fixed by entering the new one, not by retrying.
	expect(screen.getByText("Update password")).toBeVisible();
	fireEvent.click(screen.getByRole("button", { name: "Update the password for Box B" }));
	expect(onUpdatePassword).toHaveBeenCalledWith(expect.objectContaining({ hostId: "box-b" }));
	expect(onRetry).not.toHaveBeenCalled();
	fireEvent.click(screen.getByRole("button", { name: "Retry Box C" }));
	expect(onRetry).toHaveBeenCalledOnce();
});
