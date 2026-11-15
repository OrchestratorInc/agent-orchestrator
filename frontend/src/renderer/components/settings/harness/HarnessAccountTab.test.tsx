import { cleanup, render, screen } from "@testing-library/react";
import type { TFunction } from "i18next";
import { appI18n } from "../../../i18n";
import type { ComponentProps } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AgentAuthPlan } from "../../../hooks/useAgentAuth";
import { agentReadiness } from "../../../test/agent-readiness-fixtures";
import { describeHarness } from "./describe-harness";
import { HarnessAccountTab } from "./HarnessAccountTab";

afterEach(cleanup);

function details(freshness: "fresh" | "stale" = "fresh") {
	return describeHarness({
		agentId: "goose", t: appI18n.t.bind(appI18n) as TFunction, language: "en",
		readinessAgent: agentReadiness("goose", "goose", { authentication: "authorized", freshness }),
		authPlan: { available: true, action: "login" } as AgentAuthPlan,
		installed: true, pending: false, readinessPending: false, readinessFailed: false,
		authPlansPending: false, authPlansFailed: false, authWorkflow: null,
	});
}

function renderTab(props: Partial<ComponentProps<typeof HarnessAccountTab>> = {}) {
	return render(<HarnessAccountTab details={details()} authWorkflowActive={false} copied={false} diagnosticsExpanded={false}
		onToggleDiagnostics={vi.fn()} onCopyDiagnostics={vi.fn()} onInstall={vi.fn()} onSelectMethod={vi.fn()} onCopyCommand={vi.fn()} onUpdate={vi.fn()}
		onLogin={vi.fn()} onLogout={vi.fn()} onVerify={vi.fn()} onCloseAuth={vi.fn()} onRetryAuth={vi.fn()} onTerminalState={vi.fn()} {...props} />);
}

describe("HarnessAccountTab sign-in row", () => {
	it("marks a fresh sign-in with the same green tone the Health tab uses", () => {
		renderTab();
		expect(screen.getByText("Connected", { selector: "span" }).querySelector(".bg-success")).not.toBeNull();
	});

	it("drops the green mark once the sign-in check is stale", () => {
		renderTab({ details: details("stale") });
		expect(screen.getByText("Connected", { selector: "span" }).querySelector(".bg-success")).toBeNull();
	});

	it("announces a verified sign-in and hands focus to the login action", () => {
		const view = renderTab({ loginPanel: <button type="button">Save and verify</button> });
		const announcer = view.container.querySelector("[data-signed-in-announcer]");
		expect(announcer).toHaveTextContent("");
		// It is not a status role, so the tab's own progress statuses stay unique.
		expect(screen.queryByRole("status")).toBeNull();
		view.rerender(<HarnessAccountTab details={details()} authWorkflowActive={false} signedIn copied={false} diagnosticsExpanded={false}
			onToggleDiagnostics={vi.fn()} onCopyDiagnostics={vi.fn()} onInstall={vi.fn()} onSelectMethod={vi.fn()} onCopyCommand={vi.fn()} onUpdate={vi.fn()}
			onLogin={vi.fn()} onLogout={vi.fn()} onVerify={vi.fn()} onCloseAuth={vi.fn()} onRetryAuth={vi.fn()} onTerminalState={vi.fn()} />);
		expect(announcer).toHaveTextContent("Connected");
		expect(screen.getByRole("button", { name: "Log in again" })).toHaveFocus();
	});
});
