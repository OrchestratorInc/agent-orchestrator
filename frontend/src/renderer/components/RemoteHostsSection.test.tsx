import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { RemoteHostsSection } from "./RemoteHostsSection";
import { SidebarMenu, SidebarMenuItem, SidebarProvider } from "./ui/sidebar";

it("keeps host-specific offline and add-project controls around shared project rows", () => {
	const onAddProject = vi.fn();
	const onRetry = vi.fn();
	const renderProject = vi.fn((host: { hostId: string }, workspace: { name: string }) =>
		<SidebarMenuItem data-testid={`project-${host.hostId}`}>{workspace.name}</SidebarMenuItem>);
	render(<SidebarProvider><SidebarMenu><RemoteHostsSection
		hosts={[
			{ hostId: "box-a", label: "Box A", url: "https://a.test", status: "connected" },
			{ hostId: "box-b", label: "Box B", url: "https://b.test", status: "offline", failureReason: "unauthorized" },
		]}
		workspaces={[
			{ hostId: "box-a", id: "shared", name: "Project A", path: "/a", sessions: [] },
			{ hostId: "box-b", id: "shared", name: "Project B", path: "/b", sessions: [] },
		]}
		loadedProjectHostIds={["box-a"]}
		renderProject={renderProject}
		onAddProject={onAddProject}
		onRetry={onRetry}
	/></SidebarMenu></SidebarProvider>);
	expect(screen.getByTestId("project-box-a")).toHaveTextContent("Project A");
	expect(screen.queryByTestId("project-box-b")).toBeNull();
	expect(screen.getByText("1 project")).toBeVisible();
	expect(screen.getByText("Password rejected")).toBeVisible();
	fireEvent.click(screen.getByRole("button", { name: "Add project on Box A" }));
	expect(onAddProject).toHaveBeenCalledWith("box-a");
	fireEvent.click(screen.getByRole("button", { name: "Retry Box B" }));
	expect(onRetry).toHaveBeenCalledOnce();
});
