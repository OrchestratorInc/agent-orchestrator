import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { appI18n } from "../../../i18n";
import { agentReadiness } from "../../../test/agent-readiness-fixtures";
import { describeHarness } from "./describe-harness";
import { HarnessAccountTab } from "./HarnessAccountTab";

describe("HarnessAccountTab setup", () => {
	it.each(["terminal", "documentation"] as const)("offers available %s setup while authentication remains unknown", async (launchMode) => {
		const onLogin = vi.fn();
		const details = describeHarness({
			agentId: "strands", t: appI18n.t, language: "en",
			installed: true, pending: false, readinessPending: false, readinessFailed: false,
			readinessAgent: agentReadiness("strands", "Strands", { authentication: "unknown" }),
			authPlan: { agentId: "strands", action: "setup", launchMode, available: true, documentationUrl: "https://example.test/setup" },
			authPlansPending: false, authPlansFailed: false, authWorkflow: null,
		});
		render(<HarnessAccountTab
			details={details} authWorkflowActive={false} copied={false} diagnosticsExpanded={false}
			onToggleDiagnostics={vi.fn()} onCopyDiagnostics={vi.fn()} onInstall={vi.fn()} onSelectMethod={vi.fn()}
			onCopyCommand={vi.fn()} onUpdate={vi.fn()} onLogin={onLogin} onLogout={vi.fn()} onVerify={vi.fn()}
			onCloseAuth={vi.fn()} onRetryAuth={vi.fn()} onTerminalState={vi.fn()}
		/>);
		await userEvent.click(screen.getByRole("button", { name: launchMode === "documentation" ? "View documentation" : "Set up" }));
		expect(onLogin).toHaveBeenCalledOnce();
		expect(screen.getByText("Setup status unavailable")).toBeInTheDocument();
		expect(screen.queryByText("Connected")).toBeNull();
	});
});
