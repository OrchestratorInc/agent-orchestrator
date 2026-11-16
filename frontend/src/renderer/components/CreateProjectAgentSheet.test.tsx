import { useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { agentReadiness } from "../test/agent-readiness-fixtures";
import { CreateProjectAgentSheet, RequiredAgentField } from "./CreateProjectAgentSheet";
import { TooltipProvider } from "./ui/tooltip";
import { useUiStore } from "../stores/ui-store";

beforeEach(() => {
	useUiStore.setState({ settingsModal: null });
});

function renderSheet(
	onSubmit = vi.fn().mockResolvedValue(undefined),
	options: { shake?: boolean; intakeVisible?: boolean; error?: string; repositorySetupNeeded?: boolean; settingsLoading?: boolean } = {},
) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<CreateProjectAgentSheet
					error={options.error}
					intakeVisible={options.intakeVisible ?? true}
					isCreating={false}
					kind="single_repo"
					onOpenChange={() => undefined}
					onSubmit={onSubmit}
					open={true}
					path="/repo/new-project"
					repositorySetupNeeded={options.repositorySetupNeeded}
					settingsLoading={options.settingsLoading}
					shake={options.shake}
				/>
			</TooltipProvider>
		</QueryClientProvider>,
	);
	return onSubmit;
}

it("unmounts the modal on submit and does not bring it back when creation succeeds", async () => {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	let finish!: () => void;
	const pending = new Promise<void>((resolve) => { finish = resolve; });
	function Harness() {
		const [busy, setBusy] = useState(false);
		const [open, setOpen] = useState(true);
		return <QueryClientProvider client={queryClient}><TooltipProvider>
			<CreateProjectAgentSheet open={open} isCreating={busy} kind="single_repo" path="/repo/new-project" onOpenChange={setOpen} onSubmit={async () => {
				setBusy(true);
				await pending;
				setOpen(false);
				setBusy(false);
			}} />
		</TooltipProvider></QueryClientProvider>;
	}
	render(<Harness />);
	await userEvent.click(await screen.findByRole("button", { name: "Create and start" }));
	expect(screen.queryByRole("dialog", { hidden: true })).not.toBeInTheDocument();
	await act(async () => finish());
	expect(screen.queryByRole("dialog", { hidden: true })).not.toBeInTheDocument();
});

describe("CreateProjectAgentSheet", () => {
	it("asks for no agents", () => {
		renderSheet();

		expect(screen.queryByLabelText("Worker agent")).not.toBeInTheDocument();
		expect(screen.queryByLabelText("Orchestrator agent")).not.toBeInTheDocument();
	});

	it("holds Create until settings say whether intake applies", () => {
		renderSheet(undefined, { intakeVisible: false, settingsLoading: true });

		expect(screen.getByRole("button", { name: "Create and start" })).toBeDisabled();
	});

	it("shakes the active sheet when creation fails", () => {
		renderSheet(undefined, { shake: true });

		expect(screen.getByRole("dialog")).toHaveClass("modal-shake");
	});

	it("shows a failed create and retries with the primary button", async () => {
		const onSubmit = renderSheet(undefined, { intakeVisible: false, error: "Setup failed: disk full" });

		expect(screen.getByRole("alert")).toHaveTextContent("disk full");
		await userEvent.click(screen.getByRole("button", { name: "Create and start" }));
		await waitFor(() => expect(onSubmit).toHaveBeenCalledWith({ trackerIntake: undefined }));
	});

	it("shows the repository setup notice", () => {
		renderSheet(undefined, { intakeVisible: false, repositorySetupNeeded: true });

		expect(screen.getByRole("dialog")).toHaveTextContent(/git/i);
	});

	it("uses the compact trigger size for agent fields", () => {
		render(
			<RequiredAgentField
				id="agent"
				label="Agent"
				onChange={() => undefined}
				placeholder="Project default"
				value="claude-code"
			/>,
		);

		expect(screen.getByLabelText("Agent")).toHaveAttribute("data-size", "sm");
	});

	it("caps the agent menu height with a theme token", async () => {
		render(
			<RequiredAgentField id="agent" label="Agent" onChange={() => undefined} placeholder="Project default" value="" />,
		);

		await userEvent.click(screen.getByLabelText("Agent"));

		expect(await screen.findByRole("listbox")).toHaveClass("max-h-select-menu-max!");
	});

	it.each(["chip", "settings-row"] as const)("uses the fixed compact width for the %s agent menu", async (variant) => {
		render(
			<RequiredAgentField
				id="agent"
				label="Agent"
				onChange={() => undefined}
				placeholder="Choose agent"
				value="claude-code"
				variant={variant}
			/>,
		);

		await userEvent.click(screen.getByRole("button", { name: "Agent" }));

		expect(screen.getByRole("menu")).toHaveClass(
			"w-56!",
			"min-w-56!",
			"max-w-56!",
		);
	});

	it.each(["stacked", "chip", "settings-row"] as const)("%s offers agents with unknown readiness and opens Harness for definite failures", async (variant) => {
		const onChange = vi.fn();
		useUiStore.setState({ settingsModal: null });
		render(<RequiredAgentField
			id="agent" label="Agent" placeholder="Choose agent" value="codex" variant={variant} onChange={onChange}
			agents={[
				agentReadiness("claude-code", "Claude Code", { freshness: "stale" }),
				agentReadiness("codex", "Codex", { authentication: "unauthorized" }),
				agentReadiness("aider", "Aider", { authentication: "not_applicable" }),
				agentReadiness("fx", "fx", { authentication: "configured" }),
				agentReadiness("cursor", "Cursor", { installation: "not_installed" }),
				agentReadiness("opencode", "OpenCode", { authentication: "unknown" }),
			]}
		/>);
		const trigger = screen.getByLabelText("Agent");
		expect(trigger).toHaveTextContent("Codex");
		expect(trigger).toHaveTextContent("Needs setup");
		expect(screen.queryByRole("button", { name: "Log in" })).not.toBeInTheDocument();
		await userEvent.click(trigger);
		const role = variant === "stacked" ? "option" : "menuitem";
		expect(screen.getByRole(role, { name: /Claude Code/ })).toBeInTheDocument();
		expect(screen.getByRole(role, { name: /Aider/ })).toBeInTheDocument();
		expect(screen.getByRole(role, { name: "fx" })).toBeInTheDocument();
		expect(screen.queryByText("Unverified")).not.toBeInTheDocument();
		expect(screen.getByRole(role, { name: "OpenCode" })).not.toHaveAttribute("aria-disabled", "true");
		expect(screen.queryByText("Auth unknown")).not.toBeInTheDocument();
		for (const name of [/Codex/, /Cursor/]) expect(screen.queryByRole(role, { name })).not.toBeInTheDocument();
		await userEvent.keyboard("{End}{Enter}");
		await waitFor(() => expect(useUiStore.getState().settingsModal).toEqual({ scope: "global", section: "harness", focusAgentId: "codex" }));
		expect(onChange).not.toHaveBeenCalled();
		expect(trigger).toHaveTextContent("Codex");
	});

	it("preserves cloud agent choices without offering local Harness management", async () => {
		render(<RequiredAgentField id="agent" label="Agent" placeholder="Choose agent" value="" manageAgents={false} variant="chip" onChange={() => undefined}
			agents={[agentReadiness("codex", "Codex", { authentication: "unknown" })]} />);
		await userEvent.click(screen.getByLabelText("Agent"));
		expect(screen.getByRole("menuitem", { name: /Codex/ })).not.toHaveAttribute("aria-disabled", "true");
		expect(screen.queryByRole("menuitem", { name: "Manage agents…" })).not.toBeInTheDocument();
	});

	it("does not send an installed configured agent back to setup", () => {
		render(<RequiredAgentField id="agent" label="Agent" placeholder="Choose agent" value="fx" variant="chip" onChange={() => undefined}
			agents={[agentReadiness("fx", "fx", { authentication: "configured" })]} />);

		expect(screen.getByLabelText("Agent")).toHaveTextContent("fx");
		expect(screen.getByLabelText("Agent")).not.toHaveTextContent("Needs setup");
	});

	it("keeps agent management available with an empty ready list", async () => {
		const onChange = vi.fn();
		useUiStore.setState({ settingsModal: null });
		render(<RequiredAgentField id="agent" label="Agent" placeholder="Choose agent" value="" onChange={onChange} agents={[]} />);
		await userEvent.click(screen.getByLabelText("Agent"));
		expect(screen.getByText("No agents ready")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("option", { name: "Manage agents…" }));
		await waitFor(() => expect(useUiStore.getState().settingsModal).toEqual({ scope: "global", section: "harness" }));
		expect(onChange).not.toHaveBeenCalled();
	});

	it("opens management on the remote host for an unavailable selected agent", async () => {
		useUiStore.setState({ settingsModal: null });
		render(<RequiredAgentField id="agent" label="Agent" placeholder="Choose agent" value="codex" hostId="box-a" onChange={() => undefined}
			agents={[agentReadiness("codex", "Codex", { authentication: "unauthorized" })]} />);
		await userEvent.click(screen.getByLabelText("Agent"));
		await userEvent.click(screen.getByRole("option", { name: "Manage agents…" }));
		await waitFor(() => expect(useUiStore.getState().settingsModal).toEqual({ scope: "global", section: "harness", focusAgentId: "codex", hostId: "box-a" }));
	});

	it("keeps fallback agents usable until a readiness snapshot arrives", async () => {
		render(<RequiredAgentField id="agent" label="Agent" placeholder="Choose agent" value="codex" variant="settings-row" onChange={() => undefined} />);

		const trigger = screen.getByRole("button", { name: "Agent" });
		expect(trigger).toHaveTextContent("Codex");
		expect(trigger).not.toHaveTextContent("Needs setup");
		await userEvent.click(trigger);
		expect(screen.getByRole("menuitem", { name: /Claude Code/ })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: /Codex/ })).toBeInTheDocument();
		expect(screen.queryByText("No agents ready")).not.toBeInTheDocument();
	});

	it("creates without intake when the toggle is left off", async () => {
		const onSubmit = renderSheet();

		expect(screen.getByRole("dialog")).not.toHaveTextContent("/repo/new-project");
		expect(screen.queryByRole("button", { name: "Cancel" })).not.toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "Create and start" }));

		await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
		expect(onSubmit).toHaveBeenCalledWith({ trackerIntake: undefined });
	});

	it("blocks submit when intake is enabled with no assignee, then passes the intake payload once one is set", async () => {
		const onSubmit = renderSheet();

		await userEvent.click(screen.getByLabelText("Automatically work on assigned issues"));
		// Enabled with no eligibility rule: submit stays disabled (compact sheet
		// carries no inline guard prose; gating is the disabled button).
		expect(screen.getByRole("button", { name: "Create and start" })).toBeDisabled();

		await userEvent.type(screen.getByLabelText("Assignee"), "octocat");
		await userEvent.click(screen.getByRole("button", { name: "Create and start" }));

		await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
		expect(onSubmit).toHaveBeenCalledWith({
			trackerIntake: { enabled: true, assignee: "octocat" },
		});
	});

	it("omits the intake control, and submits no intake, when the daemon gate is off", async () => {
		const onSubmit = renderSheet(undefined, { intakeVisible: false });

		expect(screen.queryByLabelText("Automatically work on assigned issues")).not.toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "Create and start" }));
		await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
		expect(onSubmit.mock.calls[0]?.[0]?.trackerIntake).toBeUndefined();
	});

	it("keeps the create sheet minimal: no repo row or credential hint", async () => {
		renderSheet();
		// The compact setup control uses the shared switch styling; descriptive prose is not shown.
		expect(screen.getByLabelText("Automatically work on assigned issues")).toBeInTheDocument();
		expect(screen.queryByText(/Auto-spawn worker sessions from matching tracker issues/)).not.toBeInTheDocument();

		await userEvent.click(screen.getByLabelText("Automatically work on assigned issues"));
		expect(screen.queryByText("Repository")).not.toBeInTheDocument();
		expect(screen.queryByText(/Reads credentials from/)).not.toBeInTheDocument();
	});
});
