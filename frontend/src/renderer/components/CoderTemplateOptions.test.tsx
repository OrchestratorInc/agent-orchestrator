import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../i18n";
import { CloudCpError } from "../lib/cloud-cp";
import type { CloudCpCoderTemplateParameter } from "../lib/cloud-cp/types";
import { CoderTemplateOptions } from "./CoderTemplateOptions";

const mocks = vi.hoisted(() => ({
	getCoderTemplateParameters: vi.fn(),
	hasCoder: true,
}));

vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		client: { getCoderTemplateParameters: mocks.getCoderTemplateParameters },
		ready: true,
		baseUrl: "https://cp.test",
	}),
}));
vi.mock("../hooks/useCloudOffering", () => ({ useCloudOffering: () => ({ hasCoder: mocks.hasCoder }) }));

const PARAMETERS: CloudCpCoderTemplateParameter[] = [
	{ name: "branch", displayName: "Base branch", description: "Checked out on boot.", type: "string", mutable: true, required: false, defaultValue: "main", order: 2 },
	{ name: "cpus", displayName: "CPUs", type: "number", mutable: true, required: true, defaultValue: "4", order: 1 },
	{ name: "docker", displayName: "Docker", type: "bool", mutable: true, required: false, defaultValue: "false", order: 3 },
	{
		name: "region", displayName: "Region", type: "string", mutable: false, required: false, defaultValue: "eu", order: 4,
		options: [{ name: "Europe", value: "eu" }, { name: "United States", value: "us" }],
	},
	{
		name: "services", displayName: "Services", type: "list(string)", mutable: true, required: false, defaultValue: "[\"db\"]", order: 5,
		options: [{ name: "Database", value: "db" }, { name: "Cache", value: "cache" }],
	},
	// AO sets size and startup_script itself, so they never show here.
	{ name: "size", type: "string", mutable: true, required: false, defaultValue: "medium" },
];

function Harness({ initial = {}, onChange }: { initial?: Record<string, string>; onChange: (next: Record<string, string>) => void }) {
	const [overrides, setOverrides] = useState(initial);
	return (
		<CoderTemplateOptions
			orgId="org-1"
			templateId="tpl-1"
			overrides={overrides}
			onChange={(next) => {
				setOverrides(next);
				onChange(next);
			}}
		/>
	);
}

function renderOptions(initial?: Record<string, string>) {
	const onChange = vi.fn();
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<Harness initial={initial} onChange={onChange} />
		</QueryClientProvider>,
	);
	return { onChange };
}

describe("CoderTemplateOptions", () => {
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		mocks.hasCoder = true;
		mocks.getCoderTemplateParameters.mockReset();
	});

	it("renders an input per parameter type, prefilled with defaults", async () => {
		mocks.getCoderTemplateParameters.mockResolvedValue({ parameters: PARAMETERS.slice(0, 3) });
		renderOptions();
		expect(await screen.findByText("Template options")).toBeInTheDocument();
		expect(mocks.getCoderTemplateParameters).toHaveBeenCalledWith("org-1", "tpl-1", expect.anything());

		const branch = screen.getByRole("textbox", { name: "Base branch" });
		expect(branch).toHaveValue("main");
		expect(screen.getByText("Checked out on boot.")).toBeInTheDocument();
		// Required parameters carry a marker.
		expect(screen.getByRole("spinbutton", { name: /CPUs/ })).toHaveValue(4);
		expect(screen.getByText("Required")).toBeInTheDocument();
		expect(screen.getByRole("switch", { name: "Docker" })).not.toBeChecked();
		// Ordered by the template's `order`.
		const labels = Array.from(document.querySelectorAll("label")).map((node) => node.textContent?.replace(" *Required", ""));
		expect(labels).toEqual(["CPUs", "Base branch", "Docker"]);
		expect(screen.queryByText("size")).not.toBeInTheDocument();
	});

	it("marks a single-choice parameter required, since it always holds a value", async () => {
		mocks.getCoderTemplateParameters.mockResolvedValue({ parameters: PARAMETERS });
		const user = userEvent.setup();
		renderOptions();
		await user.click(await screen.findByRole("button", { name: "Template options (5)" }));
		const marked = Array.from(document.querySelectorAll("label"))
			.filter((node) => node.textContent?.endsWith(" *Required"))
			.map((node) => node.textContent?.replace(" *Required", ""));
		// CPUs is required by Coder; Region is a dropdown. A free-text field, a
		// switch and a multi-select (which may be left empty) stay unmarked.
		expect(marked).toEqual(["CPUs", "Region"]);
		expect(screen.getByRole("spinbutton", { name: /CPUs/ })).toHaveAttribute("aria-required", "true");
		expect(screen.getByRole("combobox", { name: /^Region/ })).toHaveAttribute("aria-required", "true");
		expect(screen.getByRole("textbox", { name: "Base branch" })).not.toHaveAttribute("aria-required");
	});

	it("keeps only values changed from the default", async () => {
		mocks.getCoderTemplateParameters.mockResolvedValue({ parameters: PARAMETERS.slice(0, 3) });
		const user = userEvent.setup();
		const { onChange } = renderOptions();
		const branch = await screen.findByRole("textbox", { name: "Base branch" });
		await user.clear(branch);
		await user.type(branch, "dev");
		expect(onChange).toHaveBeenLastCalledWith({ branch: "dev" });

		await user.click(screen.getByRole("switch", { name: "Docker" }));
		expect(onChange).toHaveBeenLastCalledWith({ branch: "dev", docker: "true" });

		// Back to the default drops the key.
		await user.clear(branch);
		await user.type(branch, "main");
		expect(onChange).toHaveBeenLastCalledWith({ docker: "true" });
	});

	it("shows a select for options and a multi-select for list(string)", async () => {
		mocks.getCoderTemplateParameters.mockResolvedValue({ parameters: PARAMETERS.slice(3) });
		const user = userEvent.setup();
		const { onChange } = renderOptions();
		expect(await screen.findByRole("combobox", { name: /^Region/ })).toHaveTextContent("Europe");

		const services = screen.getByRole("group", { name: "Services" });
		const [database, cache] = Array.from(services.querySelectorAll("button[role='checkbox']"));
		expect(database).toHaveAttribute("data-state", "checked");
		expect(cache).toHaveAttribute("data-state", "unchecked");
		await user.click(cache);
		expect(onChange).toHaveBeenLastCalledWith({ services: "[\"db\",\"cache\"]" });
	});

	it("collapses behind a counted header past four parameters", async () => {
		mocks.getCoderTemplateParameters.mockResolvedValue({ parameters: PARAMETERS });
		const user = userEvent.setup();
		renderOptions();
		const header = await screen.findByRole("button", { name: "Template options (5)" });
		expect(header).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByRole("textbox", { name: "Base branch" })).not.toBeInTheDocument();
		await user.click(header);
		expect(screen.getByRole("textbox", { name: "Base branch" })).toBeInTheDocument();
	});

	it("shows saved overrides over the defaults", async () => {
		mocks.getCoderTemplateParameters.mockResolvedValue({ parameters: PARAMETERS.slice(0, 1) });
		renderOptions({ branch: "release" });
		expect(await screen.findByRole("textbox", { name: "Base branch" })).toHaveValue("release");
	});

	it("is hidden for a template without parameters", async () => {
		mocks.getCoderTemplateParameters.mockResolvedValue({ parameters: [] });
		renderOptions();
		await waitFor(() => expect(mocks.getCoderTemplateParameters).toHaveBeenCalled());
		expect(screen.queryByTestId("coder-template-options")).not.toBeInTheDocument();
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});

	it("is hidden when the control plane predates the parameters route (404)", async () => {
		mocks.getCoderTemplateParameters.mockRejectedValue(new CloudCpError("Not found", { status: 404 }));
		renderOptions();
		await waitFor(() => expect(mocks.getCoderTemplateParameters).toHaveBeenCalled());
		expect(screen.queryByTestId("coder-template-options")).not.toBeInTheDocument();
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});

	it("never fetches for an org whose offering lacks coder", () => {
		mocks.hasCoder = false;
		renderOptions();
		expect(mocks.getCoderTemplateParameters).not.toHaveBeenCalled();
		expect(screen.queryByTestId("coder-template-options")).not.toBeInTheDocument();
	});

	it("says so when the options fail to load for another reason", async () => {
		mocks.getCoderTemplateParameters.mockRejectedValue(new CloudCpError("Bad gateway", { status: 502 }));
		renderOptions();
		// The hook retries once before giving up.
		expect(await screen.findByRole("alert", {}, { timeout: 4000 })).toHaveTextContent("Couldn't load this template's options.");
	});
});
