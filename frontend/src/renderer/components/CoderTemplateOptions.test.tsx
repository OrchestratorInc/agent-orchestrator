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
	evaluateCoderTemplateParameters: vi.fn(),
	hasCoder: true,
	orgCoder: { data: null, isLoading: false, isError: false } as { data: Record<string, unknown> | null | undefined; isLoading: boolean; isError: boolean },
	orgCoderOrgIds: [] as (string | undefined)[],
	workerWorkingFolder: undefined as boolean | undefined,
}));

vi.mock("../hooks/useCloudCp", () => ({
	useCloudCp: () => ({
		client: {
			getCoderTemplateParameters: mocks.getCoderTemplateParameters,
			evaluateCoderTemplateParameters: mocks.evaluateCoderTemplateParameters,
		},
		ready: true,
		baseUrl: "https://cp.test",
	}),
}));
vi.mock("../hooks/useCloudOffering", () => ({
	useCloudOffering: () => ({ hasCoder: mocks.hasCoder, coderWorkerWorkingFolder: mocks.workerWorkingFolder }),
}));
vi.mock("../hooks/useOrgCoderConfig", () => ({
	useOrgCoderConfig: (_enabled?: boolean, orgId?: string) => {
		mocks.orgCoderOrgIds.push(orgId);
		return mocks.orgCoder;
	},
}));

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

function Harness({ initial = {}, onChange, repositoryUrls, projectWorkingDirectory }: {
	initial?: Record<string, string>;
	onChange: (next: Record<string, string>) => void;
	repositoryUrls?: string[];
	projectWorkingDirectory?: string;
}) {
	const [overrides, setOverrides] = useState(initial);
	return (
		<CoderTemplateOptions
			orgId="org-1"
			templateId="tpl-1"
			repositoryUrls={repositoryUrls}
			projectWorkingDirectory={projectWorkingDirectory}
			overrides={overrides}
			onChange={(next) => {
				setOverrides(next);
				onChange(next);
			}}
		/>
	);
}

function renderOptions(initial?: Record<string, string>, repositoryUrls?: string[], projectWorkingDirectory?: string) {
	const onChange = vi.fn();
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<Harness initial={initial} onChange={onChange} repositoryUrls={repositoryUrls} projectWorkingDirectory={projectWorkingDirectory} />
		</QueryClientProvider>,
	);
	return { onChange };
}

describe("CoderTemplateOptions", () => {
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		mocks.hasCoder = true;
		mocks.getCoderTemplateParameters.mockReset();
		// An older control plane without the evaluate route: the form reads the GET.
		mocks.evaluateCoderTemplateParameters.mockReset().mockRejectedValue(new CloudCpError("Not found", { status: 404 }));
	});

	it("renders an input per parameter type, prefilled with defaults", async () => {
		mocks.getCoderTemplateParameters.mockResolvedValue({ parameters: PARAMETERS.slice(0, 3) });
		renderOptions();
		expect(await screen.findByText("Template options")).toBeInTheDocument();
		// Set apart from the repositories AO checks out itself.
		expect(screen.getByText("Your Coder template's own options. AO checks out this project's repositories itself.")).toBeInTheDocument();
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

describe("CoderTemplateOptions with dynamic parameters", () => {
	// Like 11x's template: other_github_repo exists only once "Other" is picked.
	const repos: CloudCpCoderTemplateParameter = {
		name: "github_repos", displayName: "Github Repositories", type: "list(string)", mutable: false, required: false, defaultValue: "[\"Alice\"]", order: 1,
		options: [{ name: "Alice", value: "Alice" }, { name: "Other", value: "Other" }],
	};
	const otherRepo: CloudCpCoderTemplateParameter = {
		name: "other_github_repo", displayName: "Other Github Repository", type: "string", mutable: false, required: false, defaultValue: "", order: 2,
	};
	const region: CloudCpCoderTemplateParameter = { name: "region", displayName: "Region", type: "string", mutable: true, required: false, defaultValue: "eu", order: 3 };

	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		mocks.hasCoder = true;
		mocks.getCoderTemplateParameters.mockReset().mockResolvedValue({ parameters: [repos, region] });
		mocks.evaluateCoderTemplateParameters.mockReset().mockImplementation(async (_org: string, _tpl: string, values: Record<string, string>) => {
			const parameters = [repos];
			if ((values.github_repos ?? "").includes("Other")) parameters.push(otherRepo);
			parameters.push(values.region === "mars"
				? { ...region, diagnostics: [{ severity: "error", summary: "Value must be a valid option" }] }
				: region);
			return { parameters };
		});
	});

	it("reveals a conditional parameter once its trigger is picked and keeps its value", async () => {
		const { onChange } = renderOptions();
		await screen.findByText("Github Repositories");
		expect(screen.queryByText("Other Github Repository")).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("checkbox", { name: "Other" }));
		const field = await screen.findByRole("textbox", { name: "Other Github Repository" }, { timeout: 3000 });
		expect(mocks.evaluateCoderTemplateParameters).toHaveBeenLastCalledWith("org-1", "tpl-1", { github_repos: "[\"Alice\",\"Other\"]" }, expect.anything());
		await userEvent.type(field, "https://github.com/acme/places");
		await waitFor(() => expect(onChange).toHaveBeenLastCalledWith({
			github_repos: "[\"Alice\",\"Other\"]", other_github_repo: "https://github.com/acme/places",
		}));
		expect(mocks.getCoderTemplateParameters).not.toHaveBeenCalled();
	});

	it("drops the value of a parameter that no longer applies after an edit", async () => {
		const { onChange } = renderOptions({ github_repos: "[\"Alice\",\"Other\"]", other_github_repo: "https://github.com/acme/places" });
		expect(await screen.findByRole("textbox", { name: "Other Github Repository" })).toHaveValue("https://github.com/acme/places");
		// Opening the form never prunes a saved value.
		expect(onChange).not.toHaveBeenCalled();
		await userEvent.click(screen.getByRole("checkbox", { name: "Other" }));
		await waitFor(() => expect(onChange).toHaveBeenLastCalledWith({}), { timeout: 3000 });
		expect(screen.queryByText("Other Github Repository")).not.toBeInTheDocument();
	});

	it("shows Coder's error about a value under its field", async () => {
		renderOptions({ region: "mars" });
		expect(await screen.findByRole("alert")).toHaveTextContent("Value must be a valid option");
	});

	it("keeps the fields editable when the control plane refuses a value", async () => {
		const evaluate = mocks.evaluateCoderTemplateParameters.getMockImplementation()!;
		mocks.evaluateCoderTemplateParameters.mockImplementation(async (org: string, tpl: string, values: Record<string, string>) => {
			if ((values.region ?? "").length > 10) throw new CloudCpError("template parameter value too long", { status: 422 });
			return evaluate(org, tpl, values);
		});
		renderOptions();
		const field = await screen.findByRole("textbox", { name: "Region" });
		expect(field).toHaveAttribute("maxLength", "4096");
		await userEvent.type(field, "much-too-long");
		expect(await screen.findByRole("alert", {}, { timeout: 4000 })).toHaveTextContent("Change the last value you edited");
		// The value can still be fixed.
		await userEvent.clear(screen.getByRole("textbox", { name: "Region" }));
		await userEvent.type(screen.getByRole("textbox", { name: "Region" }), "us");
		await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument(), { timeout: 4000 });
	});
});

describe("CoderTemplateOptions duplicate clone warning", () => {
	const repos: CloudCpCoderTemplateParameter = {
		name: "github_repos", displayName: "Github Repositories", type: "list(string)", mutable: false, required: false, defaultValue: "[]", order: 1,
		options: [{ name: "Alice", value: "Alice" }, { name: "Bob", value: "bob-svc" }],
	};
	const projectRepos = ["https://github.com/11x-ai/alice", "https://github.com/11x-ai/carol"];
	const warning = /Your template also clones .*\. Untick to keep one copy\./;
	// The visible warning, not its live announcement.
	const shown = { ignore: "script, style, [role=status]" };

	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		mocks.hasCoder = true;
		mocks.orgCoder = { data: null, isLoading: false, isError: false };
		mocks.workerWorkingFolder = undefined;
		mocks.getCoderTemplateParameters.mockReset().mockResolvedValue({ parameters: [repos] });
		mocks.evaluateCoderTemplateParameters.mockReset().mockRejectedValue(new CloudCpError("Not found", { status: 404 }));
	});

	it("warns when a ticked option names a project repository and AO clones as ao-worker", async () => {
		mocks.orgCoder = { data: { baseUrl: "https://coder.test", workerUser: "ao-worker", tokenSet: true }, isLoading: false, isError: false };
		const { onChange } = renderOptions({ github_repos: "[\"Alice\"]" }, projectRepos);
		const notice = await screen.findByText("Your template also clones Alice. Untick to keep one copy.", shown);
		// A heads-up, not an error: nothing blocks saving, and it reads as muted
		// helper text behind a warning icon rather than in the error color.
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		expect(notice.parentElement).toHaveClass("text-muted-foreground");
		expect(notice.parentElement).not.toHaveClass("text-warning");
		expect(notice.parentElement?.querySelector("svg.text-warning")).not.toBeNull();
		await userEvent.click(screen.getByRole("checkbox", { name: "Alice" }));
		expect(onChange).toHaveBeenLastCalledWith({});
		expect(screen.queryByText(warning)).not.toBeInTheDocument();
	});

	it("says the session will not start when the project has a working folder and AO runs as ao-worker", async () => {
		mocks.workerWorkingFolder = true;
		mocks.orgCoder = { data: { baseUrl: "https://coder.test", workerUser: "ao-worker", tokenSet: true }, isLoading: false, isError: false };
		renderOptions({ github_repos: "[\"Alice\"]" }, projectRepos, "~/work/{repo}");
		// This one means the session will fail to start, so it keeps the warning color.
		expect((await screen.findByText("Your template also clones Alice into the working folder, so AO cannot start there. Untick it.", shown)).parentElement).toHaveClass("text-warning");
		expect(screen.queryByText(warning)).not.toBeInTheDocument();
	});

	it("keeps the two-copies warning while the control plane does not check ao-worker sessions out in the working folder", async () => {
		mocks.orgCoder = { data: { baseUrl: "https://coder.test", workerUser: "ao-worker", tokenSet: true }, isLoading: false, isError: false };
		for (const flag of [false, undefined]) {
			mocks.workerWorkingFolder = flag;
			const { unmount } = render(
				<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
					<Harness initial={{ github_repos: "[\"Alice\"]" }} onChange={vi.fn()} repositoryUrls={projectRepos} projectWorkingDirectory="~/work/{repo}" />
				</QueryClientProvider>,
			);
			expect(await screen.findByText("Your template also clones Alice. Untick to keep one copy.", shown)).toBeInTheDocument();
			expect(screen.queryByText(/so AO cannot start there/)).not.toBeInTheDocument();
			unmount();
		}
	});

	it("reads the Coder connection of the project's org", async () => {
		mocks.orgCoderOrgIds = [];
		renderOptions({ github_repos: "[\"Alice\"]" }, projectRepos);
		await screen.findByText("Github Repositories");
		expect(mocks.orgCoderOrgIds.length).toBeGreaterThan(0);
		expect(mocks.orgCoderOrgIds.every((orgId) => orgId === "org-1")).toBe(true);
	});

	it("warns for the default ao-worker when the org has no Coder connection of its own", async () => {
		renderOptions({ github_repos: "[\"Alice\"]" }, ["https://github.com/11x-ai/Alice.git"]);
		expect(await screen.findByText(warning, shown)).toBeInTheDocument();
	});

	it("stays hidden while the option is unticked", async () => {
		renderOptions(undefined, projectRepos);
		await screen.findByText("Github Repositories");
		expect(screen.queryByText(warning)).not.toBeInTheDocument();
	});

	it("stays hidden when no ticked option names a project repository", async () => {
		renderOptions({ github_repos: "[\"bob-svc\"]" }, projectRepos);
		await screen.findByText("Github Repositories");
		expect(screen.queryByText(warning)).not.toBeInTheDocument();
	});

	it("stays hidden when the session reuses the template's checkout as the workspace user", async () => {
		mocks.orgCoder = { data: { baseUrl: "https://coder.test", workerUser: "workspace", reuseExistingCheckout: true, tokenSet: true }, isLoading: false, isError: false };
		renderOptions({ github_repos: "[\"Alice\"]" }, projectRepos);
		await screen.findByText("Github Repositories");
		expect(screen.queryByText(warning)).not.toBeInTheDocument();
	});

	it("shows the warning under the collapsed header, and only under its field once expanded", async () => {
		const extra = (name: string): CloudCpCoderTemplateParameter => ({ name, displayName: name, type: "string", mutable: true, required: false, defaultValue: "", order: 2 });
		mocks.getCoderTemplateParameters.mockResolvedValue({ parameters: [repos, extra("a"), extra("b"), extra("c"), extra("d")] });
		const user = userEvent.setup();
		renderOptions({ github_repos: "[\"Alice\"]" }, projectRepos);
		const header = await screen.findByRole("button", { name: "Template options (5)" });
		// Not opened for it: the warning sits under the header instead.
		expect(header).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByRole("group", { name: "Github Repositories" })).not.toBeInTheDocument();
		expect(screen.getAllByText(warning, shown)).toHaveLength(1);
		await user.click(header);
		const group = screen.getByRole("group", { name: "Github Repositories" });
		expect(screen.getAllByText(warning, shown)).toHaveLength(1);
		// Now under the field, right after its checkboxes.
		expect(group.nextElementSibling).toHaveTextContent(warning);
	});

	it("announces the warning and describes the option with it", async () => {
		renderOptions({ github_repos: "[\"Alice\"]" }, projectRepos);
		const notice = await screen.findByRole("status");
		expect(notice).toHaveTextContent("Your template also clones Alice. Untick to keep one copy.");
		expect(screen.getByRole("group", { name: "Github Repositories" })).toHaveAccessibleDescription(
			"Your template also clones Alice. Untick to keep one copy.",
		);
	});

	it("announces a warning that appears once the options are shown, from a status already in the page", async () => {
		renderOptions({}, projectRepos);
		const status = await screen.findByRole("status");
		expect(status).toBeEmptyDOMElement();
		await userEvent.click(await screen.findByRole("checkbox", { name: "Alice" }));
		expect(screen.getByRole("status")).toBe(status);
		expect(status).toHaveTextContent("Your template also clones Alice. Untick to keep one copy.");
	});

	it("describes the collapsed header with the warning it shows", async () => {
		const extra = (name: string): CloudCpCoderTemplateParameter => ({ name, displayName: name, type: "string", mutable: true, required: false, defaultValue: "", order: 2 });
		mocks.getCoderTemplateParameters.mockResolvedValue({ parameters: [repos, extra("a"), extra("b"), extra("c"), extra("d")] });
		renderOptions({ github_repos: "[\"Alice\"]" }, projectRepos);
		const header = await screen.findByRole("button", { name: "Template options (5)" });
		expect(header).toHaveAccessibleDescription("Your template also clones Alice. Untick to keep one copy.");
	});

	it("speaks of several repositories in the plural", async () => {
		const bothRepos = [...projectRepos, "https://github.com/11x-ai/bob-svc"];
		renderOptions({ github_repos: "[\"Alice\",\"bob-svc\"]" }, bothRepos);
		expect(await screen.findByText("Your template also clones Alice and Bob. Untick them to keep one copy of each.", shown)).toBeInTheDocument();
	});

	it("speaks of several repositories in the plural when the session cannot start", async () => {
		mocks.workerWorkingFolder = true;
		mocks.orgCoder = { data: { baseUrl: "https://coder.test", workerUser: "ao-worker", tokenSet: true }, isLoading: false, isError: false };
		const bothRepos = [...projectRepos, "https://github.com/11x-ai/bob-svc"];
		renderOptions({ github_repos: "[\"Alice\",\"bob-svc\"]" }, bothRepos, "~/work/{repo}");
		expect(await screen.findByText("Your template also clones Alice and Bob into the working folder, so AO cannot start there. Untick them.", shown)).toBeInTheDocument();
	});

	it("stays hidden until the connection's mode is known", async () => {
		mocks.orgCoder = { data: undefined, isLoading: true, isError: false };
		renderOptions({ github_repos: "[\"Alice\"]" }, projectRepos);
		await screen.findByText("Github Repositories");
		expect(screen.queryByText(warning)).not.toBeInTheDocument();
	});
});
