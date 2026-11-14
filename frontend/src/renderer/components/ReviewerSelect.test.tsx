import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { agentReadiness } from "../test/agent-readiness-fixtures";
import { useUiStore } from "../stores/ui-store";
import { ReviewerSelect } from "./ReviewerSelect";

describe("ReviewerSelect", () => {
	it("hides unavailable reviewers and opens Harness from the menu while preserving its value", async () => {
		const onChange = vi.fn();
		useUiStore.setState({ settingsModal: null });
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={client}><ReviewerSelect
			ariaLabel="Reviewer" value="codex" defaultHarness="claude-code" onChange={onChange}
			agents={[agentReadiness("claude-code", "Claude Code"), agentReadiness("codex", "Codex", { authentication: "unauthorized" })]}
		/></QueryClientProvider>);
		const trigger = screen.getByRole("button", { name: "Reviewer" });
		expect(trigger).toHaveTextContent("Needs setup");
		await userEvent.click(trigger);
		expect(screen.getByRole("menuitem", { name: /Claude Code/ })).toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: /Codex/ })).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("menuitem", { name: "Manage agents…" }));
		await waitFor(() => expect(useUiStore.getState().settingsModal).toEqual({ scope: "global", section: "harness", focusAgentId: "codex" }));
		expect(onChange).not.toHaveBeenCalled();
	});
	it("opens Harness on the selected remote host", async () => {
		useUiStore.setState({ settingsModal: null });
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={client}><ReviewerSelect
			ariaLabel="Reviewer" value="codex" defaultHarness="claude-code" hostId="box-a" onChange={() => undefined}
			agents={[agentReadiness("codex", "Codex", { authentication: "unauthorized" })]}
		/></QueryClientProvider>);
		await userEvent.click(screen.getByRole("button", { name: "Reviewer" }));
		await userEvent.click(screen.getByRole("menuitem", { name: "Manage agents…" }));
		await waitFor(() => expect(useUiStore.getState().settingsModal).toEqual({ scope: "global", section: "harness", focusAgentId: "codex", hostId: "box-a" }));
	});
	it("shows the resolved reviewer while keeping the inherited selection", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={client}><ReviewerSelect
			ariaLabel="Reviewer" value="" defaultHarness="codex" onChange={() => undefined}
			agents={[agentReadiness("codex", "Codex", { authentication: "unauthorized" })]}
		/></QueryClientProvider>);
		const trigger = screen.getByRole("button", { name: "Reviewer" });
		expect(trigger).toHaveTextContent("Codex");
		expect(trigger).not.toHaveTextContent("default");
		await userEvent.click(trigger);
		expect(screen.getByRole("menuitem", { name: /Codex/ })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: "Manage agents…" })).toBeInTheDocument();
	});

	it("keeps fallback reviewers usable until a readiness snapshot arrives", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(<QueryClientProvider client={client}><ReviewerSelect
			ariaLabel="Reviewer" value="codex" defaultHarness="claude-code" onChange={() => undefined}
		/></QueryClientProvider>);

		const trigger = screen.getByRole("button", { name: "Reviewer" });
		expect(trigger).not.toHaveTextContent("Needs setup");
		await userEvent.click(trigger);
		expect(screen.getByRole("menuitem", { name: /Claude Code/ })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: /Codex/ })).toBeInTheDocument();
	});

	it("shows the catalog's concrete model for an inherited reviewer", () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		client.setQueryData(["agent-models", "codex", ""], {
			agentId: "codex", selectionMode: "catalog", models: [
				{ id: "gpt-test", label: "GPT Test", isDefault: true },
			],
		});
		render(<QueryClientProvider client={client}><ReviewerSelect
			ariaLabel="Reviewer" value="" defaultHarness="codex" onChange={() => undefined}
		/></QueryClientProvider>);
		expect(screen.getByRole("button", { name: "Reviewer" })).toHaveTextContent("Codex · GPT Test");
	});

	it("selecting the reported reviewer model keeps following the agent", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		client.setQueryData(["agent-models", "codex", ""], {
			agentId: "codex", selectionMode: "catalog", models: [
				{ id: "gpt-test", label: "GPT Test", isDefault: true },
				{ id: "gpt-other", label: "GPT Other" },
			],
		});
		const onConfigChange = vi.fn();
		render(<QueryClientProvider client={client}><ReviewerSelect
			ariaLabel="Reviewer" value="codex" model="gpt-other" defaultHarness="claude-code"
			onChange={() => undefined} onConfigChange={onConfigChange}
		/></QueryClientProvider>);
		await userEvent.click(screen.getByRole("button", { name: "Reviewer" }));
		await userEvent.click(screen.getByRole("menuitem", { name: /Codex/ }));
		await userEvent.click(screen.getByRole("menuitem", { name: "GPT Test" }));
		expect(onConfigChange).toHaveBeenCalledWith("codex", {});
	});

	it("offers a keyboard-reachable agent default in a non-current harness submenu", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		client.setQueryData(["agent-models", "codex", ""], {
			agentId: "codex", selectionMode: "catalog", models: [
				{ id: "gpt-test", label: "GPT Test", isDefault: true },
				{ id: "gpt-other", label: "GPT Other" },
			],
		});
		const onConfigChange = vi.fn();
		render(<QueryClientProvider client={client}><ReviewerSelect
			ariaLabel="Reviewer" value="claude-code" defaultHarness="claude-code"
			agents={[agentReadiness("claude-code", "Claude Code"), agentReadiness("codex", "Codex")]}
			onChange={() => undefined} onConfigChange={onConfigChange}
		/></QueryClientProvider>);
		await userEvent.click(screen.getByRole("button", { name: "Reviewer" }));
		screen.getByRole("menuitem", { name: /Codex/ }).focus();
		await userEvent.keyboard("{ArrowRight}");
		const item = await screen.findByRole("menuitem", { name: "Use agent model" });
		item.focus();
		await userEvent.keyboard("{Enter}");
		expect(onConfigChange).toHaveBeenCalledWith("codex", {});
	});

	it("does not duplicate the agent default for the current harness, and marks the default model selected", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		client.setQueryData(["agent-models", "codex", ""], {
			agentId: "codex", selectionMode: "catalog", models: [
				{ id: "gpt-test", label: "GPT Test", isDefault: true },
				{ id: "gpt-other", label: "GPT Other" },
			],
		});
		render(<QueryClientProvider client={client}><ReviewerSelect
			ariaLabel="Reviewer" value="codex" defaultHarness="claude-code"
			onChange={() => undefined} onConfigChange={() => undefined}
		/></QueryClientProvider>);
		await userEvent.click(screen.getByRole("button", { name: "Reviewer" }));
		await userEvent.click(screen.getByRole("menuitem", { name: /Codex/ }));
		expect(screen.queryByRole("menuitem", { name: "Use agent model" })).not.toBeInTheDocument();
		expect(screen.getByText("GPT Test").closest("[data-active]")).not.toBeNull();
		expect(screen.getByText("GPT Other").closest("[data-active]")).toBeNull();
	});

	it("offers a single agent default for the current harness while a non-default model is selected without a reported default", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		client.setQueryData(["agent-models", "codex", ""], {
			agentId: "codex", selectionMode: "catalog", models: [{ id: "gpt-test", label: "GPT Test" }],
		});
		render(<QueryClientProvider client={client}><ReviewerSelect
			ariaLabel="Reviewer" value="codex" model="gpt-test" defaultHarness="claude-code"
			onChange={() => undefined} onConfigChange={() => undefined}
		/></QueryClientProvider>);
		await userEvent.click(screen.getByRole("button", { name: "Reviewer" }));
		await userEvent.click(screen.getByRole("menuitem", { name: /Codex/ }));
		expect(screen.getAllByRole("menuitem", { name: "Use agent model" })).toHaveLength(1);
	});

	it("can clear a reviewer model override when no agent model is reported", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		client.setQueryData(["agent-models", "codex", ""], {
			agentId: "codex", selectionMode: "catalog", models: [{ id: "gpt-test", label: "GPT Test" }],
		});
		const onConfigChange = vi.fn();
		render(<QueryClientProvider client={client}><ReviewerSelect
			ariaLabel="Reviewer" value="codex" model="gpt-test" defaultHarness="claude-code"
			onChange={() => undefined} onConfigChange={onConfigChange}
		/></QueryClientProvider>);
		await userEvent.click(screen.getByRole("button", { name: "Reviewer" }));
		await userEvent.click(screen.getByRole("menuitem", { name: /Codex/ }));
		await userEvent.click(screen.getByRole("menuitem", { name: "Use agent model" }));
		expect(onConfigChange).toHaveBeenCalledWith("codex", {});
	});

	it("requires a concrete reviewer model when the catalog reports no default", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		client.setQueryData(["agent-models", "codex", ""], {
			agentId: "codex", selectionMode: "catalog", models: [{ id: "gpt-test", label: "GPT Test" }],
		});
		const onConfigChange = vi.fn();
		render(<QueryClientProvider client={client}><ReviewerSelect
			ariaLabel="Reviewer" value="codex" defaultHarness="claude-code" onChange={() => undefined} onConfigChange={onConfigChange}
		/></QueryClientProvider>);

		const trigger = screen.getByRole("button", { name: "Reviewer" });
		// The unidentified model is left off the trigger.
		expect(trigger).toHaveTextContent(/^Codex$/);
		await userEvent.click(trigger);
		await userEvent.click(screen.getByRole("menuitem", { name: /Codex/ }));
		expect(screen.queryByRole("menuitem", { name: "Agent choice" })).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("menuitem", { name: "GPT Test" }));
		expect(onConfigChange).toHaveBeenCalledWith("codex", { model: "gpt-test" });
	});

	describe("effort", () => {
		const effortCatalog = {
			agentId: "codex", selectionMode: "catalog", models: [
				{ id: "gpt-test", label: "GPT Test", isDefault: true, efforts: ["low", "high"], defaultEffort: "low" },
				{ id: "gpt-other", label: "GPT Other", efforts: ["low", "high", "max"], defaultEffort: "low" },
				{ id: "gpt-plain", label: "GPT Plain" },
			],
		};
		const renderWithCatalog = (props: Partial<Parameters<typeof ReviewerSelect>[0]> = {}) => {
			const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
			client.setQueryData(["agent-models", "codex", ""], effortCatalog);
			const onConfigChange = vi.fn();
			render(<QueryClientProvider client={client}><ReviewerSelect
				ariaLabel="Reviewer" value="codex" defaultHarness="claude-code"
				onChange={() => undefined} onConfigChange={onConfigChange} {...props}
			/></QueryClientProvider>);
			return onConfigChange;
		};
		const openCodex = async () => {
			await userEvent.click(screen.getByRole("button", { name: "Reviewer" }));
			await userEvent.click(screen.getByRole("menuitem", { name: /Codex/ }));
		};

		it("shows the chosen effort on the trigger", () => {
			renderWithCatalog({ model: "gpt-other", effort: "high" });
			expect(screen.getByRole("button", { name: "Reviewer" })).toHaveTextContent("Codex · GPT Other · High");
		});

		it("leaves effort off a harness-only trigger", () => {
			renderWithCatalog({ effort: "high", harnessOnly: true });
			expect(screen.getByRole("button", { name: "Reviewer" })).toHaveTextContent(/^Codex$/);
		});

		it("offers the chosen model's efforts and keeps the model when one is picked", async () => {
			const onConfigChange = renderWithCatalog({ model: "gpt-other" });
			await openCodex();
			expect(screen.getByText("Effort")).toBeInTheDocument();
			expect(screen.getAllByRole("menuitemradio").map((item) => item.textContent)).toEqual(["Low", "High", "Max"]);
			await userEvent.click(screen.getByRole("menuitemradio", { name: "Max" }));
			expect(onConfigChange).toHaveBeenCalledWith("codex", { model: "gpt-other", effort: "max" });
		});

		it("offers the default model's efforts while following the agent model", async () => {
			const onConfigChange = renderWithCatalog();
			await openCodex();
			expect(screen.getAllByRole("menuitemradio").map((item) => item.textContent)).toEqual(["Low", "High"]);
			// Following the agent's effort marks the reported default.
			expect(screen.getByRole("menuitemradio", { name: "Low" })).toHaveAttribute("aria-checked", "true");
			await userEvent.click(screen.getByRole("menuitemradio", { name: "High" }));
			expect(onConfigChange).toHaveBeenCalledWith("codex", { effort: "high" });
		});

		it("clears the effort override when the model's default effort is picked", async () => {
			const onConfigChange = renderWithCatalog({ model: "gpt-other", effort: "high" });
			await openCodex();
			expect(screen.getByRole("menuitemradio", { name: "High" })).toHaveAttribute("aria-checked", "true");
			await userEvent.click(screen.getByRole("menuitemradio", { name: "Low" }));
			expect(onConfigChange).toHaveBeenCalledWith("codex", { model: "gpt-other" });
		});

		it("keeps the effort across a model switch only when the new model offers it", async () => {
			const onConfigChange = renderWithCatalog({ model: "gpt-other", effort: "high" });
			await openCodex();
			await userEvent.click(screen.getByRole("menuitem", { name: "GPT Plain" }));
			expect(onConfigChange).toHaveBeenLastCalledWith("codex", { model: "gpt-plain" });
		});

		it("carries a supported effort to the newly chosen model", async () => {
			const onConfigChange = renderWithCatalog({ model: "gpt-other", effort: "high" });
			await openCodex();
			await userEvent.click(screen.getByRole("menuitem", { name: "GPT Test" }));
			expect(onConfigChange).toHaveBeenCalledWith("codex", { effort: "high" });
		});

		it("drops the effort with the rest of the override when the agent model is chosen", async () => {
			const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
			client.setQueryData(["agent-models", "codex", ""], {
				agentId: "codex", selectionMode: "catalog", models: [{ id: "gpt-test", label: "GPT Test", efforts: ["low", "high"] }],
			});
			const onConfigChange = vi.fn();
			render(<QueryClientProvider client={client}><ReviewerSelect
				ariaLabel="Reviewer" value="codex" model="gpt-test" effort="high" defaultHarness="claude-code"
				onChange={() => undefined} onConfigChange={onConfigChange}
			/></QueryClientProvider>);
			await openCodex();
			await userEvent.click(screen.getByRole("menuitem", { name: "Use agent model" }));
			expect(onConfigChange).toHaveBeenCalledWith("codex", {});
		});

		it("shows no effort group for a model that reports none", async () => {
			renderWithCatalog({ model: "gpt-plain" });
			await openCodex();
			expect(screen.queryByText("Effort")).not.toBeInTheDocument();
			expect(screen.queryAllByRole("menuitemradio")).toHaveLength(0);
		});

		it("does not offer effort in a harness submenu that is not the current reviewer", async () => {
			const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
			client.setQueryData(["agent-models", "codex", ""], effortCatalog);
			render(<QueryClientProvider client={client}><ReviewerSelect
				ariaLabel="Reviewer" value="claude-code" defaultHarness="claude-code"
				agents={[agentReadiness("claude-code", "Claude Code"), agentReadiness("codex", "Codex")]}
				onChange={() => undefined} onConfigChange={() => undefined}
			/></QueryClientProvider>);
			await userEvent.click(screen.getByRole("button", { name: "Reviewer" }));
			screen.getByRole("menuitem", { name: /Codex/ }).focus();
			await userEvent.keyboard("{ArrowRight}");
			await screen.findByRole("menuitem", { name: "Use agent model" });
			expect(screen.queryByText("Effort")).not.toBeInTheDocument();
		});
	});
});
