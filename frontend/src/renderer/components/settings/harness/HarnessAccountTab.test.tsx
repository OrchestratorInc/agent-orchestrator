import { cleanup, render, screen } from "@testing-library/react";
import type { TFunction } from "i18next";
import { appI18n } from "../../../i18n";
import type { ComponentProps } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AgentAuthPlan } from "../../../hooks/useAgentAuth";
import type { InstallJob } from "../../../hooks/useHarnessUpdates";
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

describe("HarnessAccountTab diagnostics", () => {
	function withDiagnostics() {
		const job = { agentId: "goose", status: "failed", method: "npm", error: "install failed" } as unknown as InstallJob;
		return { ...details(), hasDiagnostics: true, job };
	}

	it("labels the copy button Copy diagnostics before a copy", () => {
		const view = renderTab({ details: withDiagnostics(), diagnosticsExpanded: true });
		const button = view.container.querySelector("[data-harness-copy-diagnostics]");
		expect(button).toHaveAccessibleName("Copy diagnostics");
	});

	it("confirms a copy with a check and Copied, like the Health tab", () => {
		const view = renderTab({ details: withDiagnostics(), diagnosticsExpanded: true, copied: true });
		const button = view.container.querySelector("[data-harness-copy-diagnostics]");
		expect(button).toHaveAccessibleName("Copied");
		expect(button?.querySelector(".icon-check")).not.toBeNull();
		expect(button?.querySelector(".icon-copy")).toBeNull();
	});
});

describe("HarnessAccountTab install finishing", () => {
	const running = { target: "goose", status: "installing", startedAt: "2026-11-19T14:03:50Z" } as InstallJob;
	const succeeded = { ...running, status: "succeeded", finishedAt: "2026-11-19T14:04:22Z" } as InstallJob;

	function installedDetails(installed: boolean, installCheckPending: boolean, job: InstallJob) {
		return describeHarness({
			agentId: "goose", t: appI18n.t.bind(appI18n) as TFunction, language: "en", job,
			readinessAgent: agentReadiness("goose", "goose", { installation: installed ? "installed" : "not_installed" }),
			plan: { agentId: "goose", methods: [{ id: "npm", label: "npm", available: true, recommended: true }] } as never,
			installed, installCheckPending, pending: false, readinessPending: false, readinessFailed: false,
			authPlansPending: false, authPlansFailed: false, authWorkflow: null,
		});
	}

	function finish(installed: boolean, installCheckPending: boolean) {
		const view = renderTab({ details: installedDetails(false, false, running) });
		view.rerender(<HarnessAccountTab details={installedDetails(installed, installCheckPending, succeeded)} authWorkflowActive={false} copied={false} diagnosticsExpanded={false}
			onToggleDiagnostics={vi.fn()} onCopyDiagnostics={vi.fn()} onInstall={vi.fn()} onSelectMethod={vi.fn()} onCopyCommand={vi.fn()} onUpdate={vi.fn()}
			onLogin={vi.fn()} onLogout={vi.fn()} onVerify={vi.fn()} onCloseAuth={vi.fn()} onRetryAuth={vi.fn()} onTerminalState={vi.fn()} />);
		return view;
	}

	it("verifies the install instead of offering Install again", () => {
		const view = finish(false, true);
		expect(view.container.querySelector("[data-harness-install-progress='verifying']")).not.toBeNull();
		expect(screen.getAllByText("Verifying…").length).toBeGreaterThan(0);
		expect(screen.queryByRole("button", { name: "Install" })).toBeNull();
		expect(screen.queryByText("Not installed")).toBeNull();
		expect(screen.queryByText("Done")).toBeNull();
	});

	it("ends on Done once the check finds it installed", () => {
		const view = finish(true, false);
		expect(view.container.querySelector("[data-harness-install-progress='done']")).not.toBeNull();
	});

	it("never says Done next to Not installed", () => {
		const view = finish(false, false);
		expect(view.container.querySelector("[data-harness-install-progress='done']")).toBeNull();
		expect(screen.getByText("Not installed")).toBeInTheDocument();
		expect(screen.queryByText("Done")).toBeNull();
	});
});
