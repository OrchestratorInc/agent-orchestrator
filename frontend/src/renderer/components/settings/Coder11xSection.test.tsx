import { render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import type { UseCoderTemplatesResult } from "../../hooks/useCoderTemplates";
import { Coder11xSection } from "./Coder11xSection";

// Mutable mock state so each test drives the loaded config and the template list
// independently. The section under test is gated on a saved connection
// (tokenSet) and rendered from the useCoderTemplates result.
const mocks = vi.hoisted(() => ({
	config: { data: undefined as unknown },
	templates: { value: { templates: [], isLoading: false, isError: false } as UseCoderTemplatesResult },
}));

vi.mock("../../hooks/useCloudGate", () => ({ useCloudGate: () => ({ cloudEnabled: true }) }));
vi.mock("../../lib/cloud-session", () => ({ useCloudSession: () => ({ status: "authenticated" }) }));
vi.mock("../../hooks/useCloudCp", () => ({
	useCloudCp: () => ({ client: {}, ready: true, baseUrl: "https://cp.test" }),
}));
vi.mock("../../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: { id: "org-1" } }) }));
vi.mock("../../hooks/useOrgCoderConfig", () => ({
	orgCoderConfigQueryKey: ["cloud-org-coder-config"],
	useOrgCoderConfig: () => mocks.config,
}));
vi.mock("../../hooks/useCoderTemplates", () => ({ useCoderTemplates: () => mocks.templates.value }));

const savedConfig = { baseUrl: "https://coder.test", owner: "me", defaultTemplateId: "t-1", tokenSet: true };

function renderSection() {
	render(
		<QueryClientProvider client={new QueryClient()}>
			<Coder11xSection />
		</QueryClientProvider>,
	);
}

describe("Coder11xSection templates list", () => {
	beforeEach(async () => {
		await appI18n.changeLanguage("en");
		mocks.config.data = undefined;
		mocks.templates.value = { templates: [], isLoading: false, isError: false };
	});

	it("hides the templates list until a connection is saved", () => {
		mocks.config.data = { baseUrl: "https://coder.test", owner: "me", defaultTemplateId: "t-1", tokenSet: false };
		renderSection();
		expect(screen.queryByText("Templates on your Coder")).not.toBeInTheDocument();
	});

	it("shows a loading state while listing templates", () => {
		mocks.config.data = savedConfig;
		mocks.templates.value = { templates: [], isLoading: true, isError: false };
		renderSection();
		expect(screen.getByText("Templates on your Coder")).toBeInTheDocument();
		expect(screen.getByText("Loading templates…")).toBeInTheDocument();
	});

	it("shows an error when the Coder instance or token is bad", () => {
		mocks.config.data = savedConfig;
		mocks.templates.value = { templates: [], isLoading: false, isError: true };
		renderSection();
		expect(screen.getByRole("alert")).toHaveTextContent("Couldn't load templates");
	});

	it("shows an empty state when the Coder has no templates", () => {
		mocks.config.data = savedConfig;
		mocks.templates.value = { templates: [], isLoading: false, isError: false };
		renderSection();
		expect(screen.getByText("No templates found on this Coder deployment.")).toBeInTheDocument();
	});

	it("lists each template with its name and one-line spec", () => {
		mocks.config.data = savedConfig;
		mocks.templates.value = {
			templates: [
				{ id: "t-1", name: "ao-devkit-medium", displayName: "AO Dev-kit (Medium)", description: "4 vCPU / 16 GB", icon: "", parameters: [] },
				{ id: "t-2", name: "ao-devkit-large", displayName: "", description: "", icon: "", parameters: [] },
			],
			isLoading: false,
			isError: false,
		};
		renderSection();
		// Scope to the read-only list: the selected template's name also appears in
		// the default-template dropdown trigger above it.
		const list = within(screen.getByRole("list", { name: "Templates on your Coder" }));
		// Prefers displayName, with the short description as subtext.
		expect(list.getByText("AO Dev-kit (Medium)")).toBeInTheDocument();
		expect(list.getByText("4 vCPU / 16 GB")).toBeInTheDocument();
		// Falls back to the slug name when no displayName is set.
		expect(list.getByText("ao-devkit-large")).toBeInTheDocument();
	});
});
