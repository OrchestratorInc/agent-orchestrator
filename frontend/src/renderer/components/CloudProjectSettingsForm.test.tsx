import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { CloudCpProject } from "../lib/cloud-cp";
import { CloudProjectSettingsForm } from "./CloudProjectSettingsForm";

const h = vi.hoisted(() => ({
	updateProject: vi.fn(),
	templates: [] as Array<{ id: string; name: string; displayName: string }>,
	orgCoderConfig: null as { baseUrl: string; templateId?: string } | null,
}));

vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ client: { updateProject: h.updateProject }, ready: true, baseUrl: "https://cp.test" }),
}));

vi.mock("../hooks/useCloudOrg", () => ({
	useCloudOrg: () => ({ org: { id: "org-1" } }),
}));

vi.mock("../hooks/useOrgCoderConfig", () => ({
	useOrgCoderConfig: () => ({ data: h.orgCoderConfig, isLoading: false }),
}));

vi.mock("../hooks/useCoderTemplates", () => ({
	useCoderTemplates: () => ({ templates: h.templates, isLoading: false, isError: false }),
}));

function cloudProject(config: Record<string, unknown> = {}): CloudCpProject {
	return {
		id: "cloud-1",
		orgId: "org-1",
		displayName: "ao-landing",
		repositoryUrl: "https://github.com/acme/ao-landing",
		defaultBranch: "main",
		config,
		createdAt: "2026-10-08T00:00:00Z",
		updatedAt: "2026-10-08T00:00:00Z",
	};
}

function renderForm(project: CloudCpProject) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={queryClient}>
			<CloudProjectSettingsForm project={project} />
		</QueryClientProvider>,
	);
}

describe("CloudProjectSettingsForm", () => {
	beforeEach(() => {
		h.updateProject.mockReset().mockResolvedValue({ project: cloudProject() });
		h.templates = [{ id: "tpl-1", name: "azure-linux", displayName: "Azure Linux" }];
		h.orgCoderConfig = null;
	});

	it("shows the cloud project's details and its Coder template", () => {
		renderForm(cloudProject({ coder: { templateId: "tpl-1", size: "medium" } }));

		expect(screen.getByText("Cloud project")).toBeInTheDocument();
		expect(screen.getByText("https://github.com/acme/ao-landing")).toBeInTheDocument();
		expect(screen.getByText("Azure Linux")).toBeInTheDocument();
		expect(screen.getByText("medium")).toBeInTheDocument();
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});

	it("flags a template-less project when the org's own Coder has no default template", () => {
		h.orgCoderConfig = { baseUrl: "https://coder.acme.test" };
		renderForm(cloudProject());

		expect(screen.getByRole("alert")).toHaveTextContent("No template. Sessions can't start");
	});

	it("shows the inherited default when the org has no bring-your-own Coder", () => {
		renderForm(cloudProject());

		expect(screen.getByText("Default template")).toBeInTheDocument();
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});

	it("shows the inherited default when the org's own Coder sets a default template", () => {
		h.orgCoderConfig = { baseUrl: "https://coder.acme.test", templateId: "tpl-1" };
		renderForm(cloudProject());

		expect(screen.getByText("Default template")).toBeInTheDocument();
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});

	it("saves a renamed project to the control plane", async () => {
		renderForm(cloudProject({ coder: { templateId: "tpl-1" } }));

		await userEvent.click(screen.getByRole("button", { name: "Edit Project name" }));
		const input = screen.getByRole("textbox");
		await userEvent.clear(input);
		await userEvent.type(input, "Landing site");

		await waitFor(() =>
			expect(h.updateProject).toHaveBeenCalledWith("org-1", "cloud-1", { displayName: "Landing site", defaultBranch: "main" }),
		);
	});

	it("does not save an empty default branch", async () => {
		renderForm(cloudProject({ coder: { templateId: "tpl-1" } }));

		await userEvent.click(screen.getByRole("button", { name: "Edit Default branch" }));
		await userEvent.clear(screen.getByRole("textbox"));

		await waitFor(() => expect(screen.getByRole("textbox")).toHaveValue(""));
		await new Promise((resolve) => setTimeout(resolve, 800));
		expect(h.updateProject).not.toHaveBeenCalled();
	});
});
