import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import type { AgentSwitchProps } from "./chat/AgentSwitchPicker";
import { TerminalAgentPickerRow } from "./TerminalAgentControls";

const mocks = vi.hoisted(() => ({
	picker: undefined as undefined | { agentSwitch: AgentSwitchProps; pendingFooter?: ReactNode },
	localSwitch: vi.fn(),
	cloudStart: vi.fn(),
	closeMenu: vi.fn(),
}));

vi.mock("./chat/AgentSwitchPicker", async (original) => ({
	...await original<typeof import("./chat/AgentSwitchPicker")>(),
	AgentModelPicker: (props: { agentSwitch: AgentSwitchProps; pendingFooter?: ReactNode }) => {
		mocks.picker = props;
		return <div>{props.pendingFooter}</div>;
	},
}));
vi.mock("../hooks/useConversation", () => ({
	useProviderSwitchCommand: () => ({ mutateAsync: mocks.localSwitch, isPending: false }),
}));
vi.mock("../hooks/useCloudAgentSwitch", () => ({
	useCloudAgentSwitch: (session: WorkspaceSession | undefined) => ({
		available: Boolean(session), switching: false, running: undefined,
		agents: session ? [{ id: "codex", label: "Codex" }, { id: "claude-code", label: "Claude Code" }] : [],
		start: mocks.cloudStart,
	}),
}));
vi.mock("./menu-model", () => ({ useCloseMenu: () => mocks.closeMenu }));

const session: WorkspaceSession = {
	id: "session-1", workspaceId: "project-1", workspaceName: "Project", title: "Agent", provider: "codex",
	kind: "worker", mode: "tui", status: "idle", updatedAt: "2026-10-01T00:00:00Z", prs: [],
};

describe("TerminalAgentPickerRow", () => {
	beforeEach(() => {
		mocks.localSwitch.mockReset().mockResolvedValue({});
		mocks.cloudStart.mockReset().mockResolvedValue({});
		mocks.closeMenu.mockReset();
	});

	it("switches a Cloud terminal's agent with its interface transition", async () => {
		render(<TerminalAgentPickerRow session={{ ...session, cloud: { orgId: "org-1" } }} blocked={false} />);
		expect(mocks.picker?.agentSwitch).toMatchObject({ cloudOrgId: "org-1", harnessView: "cloud", agents: [{ id: "codex" }, { id: "claude-code" }] });
		act(() => mocks.picker?.agentSwitch.onPendingChange({ harness: "claude-code", model: "claude-opus-4-5" }));
		fireEvent.click(await screen.findByRole("button", { name: "Switch" }));
		await waitFor(() => expect(mocks.closeMenu).toHaveBeenCalled());
		expect(mocks.cloudStart).toHaveBeenCalledWith(expect.objectContaining({ harness: "claude-code", model: "claude-opus-4-5" }));
		expect(mocks.localSwitch).not.toHaveBeenCalled();
	});

	it("keeps a local terminal on the daemon's provider switch", async () => {
		render(<TerminalAgentPickerRow session={session} blocked={false} />);
		expect(mocks.picker?.agentSwitch.agents).toBeUndefined();
		act(() => mocks.picker?.agentSwitch.onPendingChange({ harness: "claude-code" }));
		fireEvent.click(await screen.findByRole("button", { name: "Switch" }));
		await waitFor(() => expect(mocks.localSwitch).toHaveBeenCalledWith(expect.objectContaining({ targetHarness: "claude-code" })));
		expect(mocks.cloudStart).not.toHaveBeenCalled();
	});
});
