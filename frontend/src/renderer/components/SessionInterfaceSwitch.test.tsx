import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { publishHarnessUpdateNoticeRect } from "../hooks/useHarnessUpdateNoticeClearance";
import { appI18n } from "../i18n";
import type { SessionInterfaceTransition } from "../hooks/useSessionInterfaceTransition";
import {
	SessionInterfaceActionGroup,
	SessionInterfaceSwitchButton,
	SessionInterfaceSwitchDialog,
	SessionInterfaceTransitionNotice,
} from "./SessionInterfaceSwitch";
import { TooltipProvider } from "./ui/tooltip";

function renderSwitchButton(target: "chat" | "tui", onClick = vi.fn()) {
	return {
		onClick,
		...render(
			<TooltipProvider>
				<SessionInterfaceSwitchButton target={target} supported onClick={onClick} />
			</TooltipProvider>,
		),
	};
}

function transition(phase: SessionInterfaceTransition["phase"]): SessionInterfaceTransition {
	return {
		id: "switch-1",
		sessionId: "project-1",
		sourceMode: "tui",
		targetMode: "chat",
		policy: "drain",
		historyPolicy: "strict",
		phase,
		createdAt: "2026-08-05T10:00:00Z",
		updatedAt: "2026-08-05T10:00:01Z",
	};
}

describe("SessionInterfaceSwitchButton", () => {
	it("disables the action when Cloud reports a terminated session", () => {
		const onClick = vi.fn();
		render(
			<TooltipProvider>
				<SessionInterfaceSwitchButton
					target="chat"
					supported={false}
					disabledReason="Terminated sessions must be restored before switching interfaces."
					onClick={onClick}
				/>
			</TooltipProvider>,
		);

		const button = screen.getByRole("button", { name: "Switch to chat UI" });
		expect(button).toBeDisabled();
		fireEvent.click(button);
		expect(onClick).not.toHaveBeenCalled();
	});

	it("uses the shared topbar spacing between adjacent session actions", () => {
		render(
			<SessionInterfaceActionGroup>
				<button type="button">First action</button>
				<button type="button">Second action</button>
			</SessionInterfaceActionGroup>,
		);

		const group = screen.getByRole("button", { name: "First action" }).parentElement;
		expect(group).toHaveClass("gap-2");
		expect(group).not.toHaveClass("gap-px");
	});

	it("shows a spinner while switching and reveals cancel on the tab hover target", () => {
		const onCancel = vi.fn();
		render(
			<TooltipProvider>
				{/* TerminalTabFrame marks the session tab with Tailwind `group`. */}
				<div className="group">
					<SessionInterfaceSwitchButton
						target="chat"
						supported
						transition={transition("draining")}
						onClick={vi.fn()}
						onCancel={onCancel}
					/>
				</div>
			</TooltipProvider>,
		);

		const status = screen.getByRole("status");
		expect(status).toHaveAttribute(
			"aria-label",
			"Waiting to switch… Switching to Chat UI.",
		);
		expect(status.querySelector(".animate-spin")).not.toBeNull();
		const cancel = screen.getByRole("button", { name: "Cancel switch to Chat UI" });
		expect(cancel).toHaveClass("opacity-0", "group-hover:opacity-100");
		fireEvent.click(cancel);
		expect(onCancel).toHaveBeenCalledOnce();
	});

	it.each([
		["source_stopping", "Stopping controller… Switching to Chat UI."],
		["target_starting", "Resuming agent… Switching to Chat UI."],
	] as const)("stays non-interactive while %s is progressing", (phase, expectedLabel) => {
		render(
			<TooltipProvider>
				<SessionInterfaceSwitchButton
					target="chat"
					supported
					transition={transition(phase)}
					onClick={vi.fn()}
					onCancel={vi.fn()}
				/>
			</TooltipProvider>,
		);

		const status = screen.getByRole("status");
		expect(status).toHaveAttribute("aria-label", expectedLabel);
		expect(status.querySelector(".animate-spin")).not.toBeNull();
		expect(screen.queryByRole("button", { name: "Cancel switch to Chat UI" })).not.toBeInTheDocument();
	});

	it("replaces progress with a non-interactive warning when target shutdown is unconfirmed", () => {
		const detail =
			"AO could not confirm the target controller stopped. Restart AO to retry shutdown before restoring the original interface.";
		render(
			<TooltipProvider>
				<SessionInterfaceSwitchButton
					target="chat"
					supported
					transition={{
						...transition("target_starting"),
						errorCode: "TARGET_STOP_UNCONFIRMED",
						errorDetail: detail,
					}}
					onClick={vi.fn()}
					onCancel={vi.fn()}
				/>
			</TooltipProvider>,
		);

		const status = screen.getByRole("status");
		expect(status).toHaveAttribute("aria-label", `Interface switch needs attention. ${detail}`);
		expect(status.querySelector(".animate-spin")).toBeNull();
		expect(screen.queryByRole("button")).not.toBeInTheDocument();
	});

	it.each([
		["chat", "Switch to chat UI", "lucide-message-square"],
		["tui", "Switch to terminal UI", "lucide-square-terminal"],
	] as const)("uses an icon-only %s destination control", (target, label, iconClass) => {
		const { onClick } = renderSwitchButton(target);

		const button = screen.getByRole("button", { name: label });
		expect(button.classList.contains("topbar-control--icon")).toBe(true);
		expect(button.textContent).toBe("");
		expect(button.querySelector(`.${iconClass}`)).not.toBeNull();
		fireEvent.click(button);
		expect(onClick).toHaveBeenCalledOnce();
	});

	it("describes recovered history accurately and hides it after durable acknowledgement", () => {
		const recovered = { ...transition("recovery_required"), errorCode: "DAEMON_RESTARTED" };
		const { rerender } = render(
			<SessionInterfaceTransitionNotice transition={recovered} onDismiss={vi.fn()} />,
		);

		expect(screen.getByText("Interface switch recovered")).toBeInTheDocument();
		expect(screen.getByRole("status").querySelector(".text-success")).toBeNull();
		expect(screen.queryByText("Interface switch needs recovery")).not.toBeInTheDocument();

		rerender(
			<SessionInterfaceTransitionNotice
				transition={{ ...recovered, noticeAcknowledgedAt: "2026-08-13T08:00:00Z" }}
				onDismiss={vi.fn()}
			/>,
		);
		expect(screen.queryByText("Interface switch recovered")).not.toBeInTheDocument();
	});

	it("does not claim an unresolved recovery-required transition was recovered", () => {
		render(
			<SessionInterfaceTransitionNotice
				transition={{
					...transition("recovery_required"),
					errorCode: "SOURCE_STOP_UNCERTAIN",
				}}
				onDismiss={vi.fn()}
			/>,
		);

		expect(screen.getByText("Interface switch needs attention")).toBeInTheDocument();
		expect(screen.queryByText("Interface switch recovered")).not.toBeInTheDocument();
	});
});

describe("SessionInterfaceSwitchDialog", () => {
	it("requires an explicit Terminal stop for Cloud without claiming the agent is busy", () => {
		const onChoose = vi.fn();
		render(
			<SessionInterfaceSwitchDialog
				open
				target="chat"
				requireExplicitTerminalStop
				onOpenChange={vi.fn()}
				onChoose={onChoose}
			/>,
		);

		const dialog = screen.getByRole("dialog", { name: "Switch to Chat UI?" });
		expect(dialog).toHaveTextContent("AO cannot verify whether this Terminal is idle.");
		expect(within(dialog).queryByRole("button", { name: /^Finish work, then switch/ })).not.toBeInTheDocument();
		const action = within(dialog).getByRole("button", { name: /^Terminate and then switch/ });
		expect(onChoose).not.toHaveBeenCalled();
		fireEvent.click(action);
		expect(onChoose).toHaveBeenCalledWith("interrupt");
	});

	it("focuses Finish work as the safe default", async () => {
		render(<SessionInterfaceSwitchDialog open target="tui" onOpenChange={vi.fn()} onChoose={vi.fn()} />);

		await waitFor(() => {
			expect(screen.getByRole("button", { name: /^Finish work, then switch/ })).toHaveFocus();
		});
	});

	it("names active, queued, draft, and staged-attachment consequences before a Chat escape", () => {
		render(<SessionInterfaceSwitchDialog open target="tui" onOpenChange={vi.fn()} onChoose={vi.fn()} />);

		const dialog = screen.getByRole("dialog", { name: "Switch to Terminal UI?" });
		expect(within(dialog).getByRole("button", { name: /^Finish work, then switch/ })).toHaveTextContent(
			"running turn and anything already queued to finish",
		);
		expect(within(dialog).getByRole("button", { name: /^Stop now and switch/ })).toHaveTextContent(
			"unfinished output and queued Chat turns are cancelled",
		);
		expect(dialog).toHaveTextContent(
			"Any unsent Chat draft or staged attachments are discarded when the switch completes.",
		);
	});

	it("discloses that interrupting discards an unsent terminal draft", () => {
		render(<SessionInterfaceSwitchDialog open target="chat" onOpenChange={vi.fn()} onChoose={vi.fn()} />);

		expect(screen.getByText(/Any unsent Terminal UI draft is discarded/)).toBeInTheDocument();
	});
});

describe("SessionInterfaceTransitionNotice", () => {
	it.each([undefined, "2026-08-13T08:00:00Z"])(
		"keeps unconfirmed target shutdown visible without unsafe recovery or dismissal actions (%s)",
		(noticeAcknowledgedAt) => {
			const detail =
				"AO could not confirm the target controller stopped. Restart AO to retry shutdown before restoring the original interface. target still running";
			render(
				<SessionInterfaceTransitionNotice
					transition={{
						...transition("target_starting"),
						errorCode: "TARGET_STOP_UNCONFIRMED",
						errorDetail: detail,
						noticeAcknowledgedAt,
					}}
					onDismiss={vi.fn()}
					dismissing
					onRetry={vi.fn()}
					retrying
					onUseProviderHistory={vi.fn()}
					onSwitchWithInterrupt={vi.fn()}
				/>,
			);

			const alert = screen.getByRole("alert");
			expect(alert).toHaveTextContent("Interface switch needs attention");
			expect(alert).toHaveTextContent(detail);
			expect(alert.querySelector(".animate-spin")).toBeNull();
			expect(screen.queryByRole("button")).not.toBeInTheDocument();
		},
	);

	it("provides the restart instruction if unconfirmed target shutdown has no detail", () => {
		render(
			<SessionInterfaceTransitionNotice
				transition={{ ...transition("target_starting"), errorCode: "TARGET_STOP_UNCONFIRMED" }}
				onDismiss={vi.fn()}
			/>,
		);

		expect(screen.getByRole("alert")).toHaveTextContent(
			"AO could not confirm the target controller stopped. Restart AO to retry shutdown before restoring the original interface.",
		);
		expect(screen.queryByText(/original interface remains available/)).not.toBeInTheDocument();
	});

	it("announces unsettled history and offers retry or stay actions", () => {
		const onRetry = vi.fn();
		const onDismiss = vi.fn();
		render(
			<SessionInterfaceTransitionNotice
				transition={{
					...transition("failed"),
					errorCode: "TARGET_HISTORY_UNSETTLED",
					errorDetail: "Interface switch failed (AO-2L): target history is not settled.",
				}}
				onDismiss={onDismiss}
				onRetry={onRetry}
			/>,
		);

		expect(screen.getByRole("alert")).toHaveTextContent("AO-2L");
		fireEvent.click(screen.getByRole("button", { name: "Retry switch to Chat UI" }));
		expect(onRetry).toHaveBeenCalledOnce();
		expect(screen.queryByRole("button", { name: "Use provider history and switch" })).not.toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Stay in Terminal" }));
		expect(onDismiss).toHaveBeenCalledOnce();
	});

	it("offers provider-history recovery only for a legacy text mismatch", () => {
		const onRetry = vi.fn();
		const onUseProviderHistory = vi.fn();
		const onDismiss = vi.fn();
		render(
			<SessionInterfaceTransitionNotice
				transition={{
					...transition("failed"),
					errorCode: "TARGET_HISTORY_UNTRUSTED_TEXT_MISMATCH",
					errorDetail: "Interface switch failed (AO-2L): legacy checkpoint text did not match.",
				}}
				onDismiss={onDismiss}
				onRetry={onRetry}
				onUseProviderHistory={onUseProviderHistory}
			/>,
		);

		expect(screen.getByRole("alert")).toHaveTextContent("AO-2L");
		fireEvent.click(screen.getByRole("button", { name: "Retry switch to Chat UI" }));
		fireEvent.click(screen.getByRole("button", { name: "Use provider history and switch" }));
		fireEvent.click(screen.getByRole("button", { name: "Stay in Terminal" }));
		expect(onRetry).toHaveBeenCalledOnce();
		expect(onUseProviderHistory).toHaveBeenCalledOnce();
		expect(onDismiss).toHaveBeenCalledOnce();
	});

	it("retains explicit provider-history recovery after daemon restart", () => {
		const onRetry = vi.fn();
		const onUseProviderHistory = vi.fn();
		const onDismiss = vi.fn();
		render(
			<SessionInterfaceTransitionNotice
				transition={{
					...transition("recovery_required"),
					historyPolicy: "provider_history",
					errorCode: "DAEMON_RESTARTED",
					errorDetail: "AO restored Terminal after the daemon restarted.",
				}}
				onDismiss={onDismiss}
				onRetry={onRetry}
				onUseProviderHistory={onUseProviderHistory}
			/>,
		);

		expect(screen.getByRole("status")).toHaveTextContent("AO restored Terminal");
		fireEvent.click(screen.getByRole("button", { name: "Retry switch to Chat UI" }));
		fireEvent.click(screen.getByRole("button", { name: "Use provider history and switch" }));
		fireEvent.click(screen.getByRole("button", { name: "Stay in Terminal" }));
		expect(onRetry).toHaveBeenCalledOnce();
		expect(onUseProviderHistory).toHaveBeenCalledOnce();
		expect(onDismiss).toHaveBeenCalledOnce();
	});

	it("announces a rejected recovery attempt once inside AO-2L", () => {
		render(
			<SessionInterfaceTransitionNotice
				transition={{
					...transition("failed"),
					errorCode: "TARGET_HISTORY_UNTRUSTED_TEXT_MISMATCH",
					errorDetail: "Interface switch failed (AO-2L).",
				}}
				onDismiss={vi.fn()}
				onRetry={vi.fn()}
				recoveryError="Terminal history changed; retry with a fresh choice."
			/>,
		);

		const [announcement] = screen.getAllByRole("alert");
		expect(screen.getAllByRole("alert")).toHaveLength(1);
		expect(announcement).toHaveTextContent("Interface switch failed (AO-2L).");
		expect(announcement).toHaveTextContent(
			"Recovery attempt failed: Terminal history changed; retry with a fresh choice.",
		);
	});

	it("announces a notice dismissal failure once inside AO-2L", () => {
		render(
			<SessionInterfaceTransitionNotice
				transition={{
					...transition("failed"),
					errorCode: "TARGET_HISTORY_UNSETTLED",
					errorDetail: "Interface switch failed (AO-2L).",
				}}
				onDismiss={vi.fn()}
				dismissError="Dismiss request was rejected."
			/>,
		);

		const [announcement] = screen.getAllByRole("alert");
		expect(screen.getAllByRole("alert")).toHaveLength(1);
		expect(announcement).toHaveTextContent("Interface switch failed (AO-2L).");
		expect(announcement).toHaveTextContent("Could not dismiss this message. Try again.");
	});

	it("offers an explicit cancellation action when a provider decision blocks drain", () => {
		const onSwitchWithInterrupt = vi.fn();
		render(
			<SessionInterfaceTransitionNotice
				transition={{
					...transition("failed"),
					errorCode: "DRAIN_DECISION_PENDING",
					errorDetail: "AO found a provider decision waiting in Terminal.",
				}}
				onDismiss={vi.fn()}
				onSwitchWithInterrupt={onSwitchWithInterrupt}
			/>,
		);

		const action = screen.getByRole("button", {
			name: "Cancel request and switch",
		});
		fireEvent.click(action);
		expect(onSwitchWithInterrupt).toHaveBeenCalledOnce();
	});

	it("offers termination only after Docker cannot verify an interactive source is idle", () => {
		const onSwitchWithInterrupt = vi.fn();
		render(
			<SessionInterfaceTransitionNotice
				transition={{
					...transition("failed"),
					errorCode: "SOURCE_DRAIN_FAILED",
					errorDetail: "source controller activity cannot be verified; use stop now to interrupt it",
				}}
				onDismiss={vi.fn()}
				onSwitchWithInterrupt={onSwitchWithInterrupt}
			/>,
		);

		const alert = screen.getByRole("alert");
		expect(alert).toHaveTextContent("AO cannot verify whether this Terminal is idle.");
		const action = within(alert).getByRole("button", { name: "Terminate and then switch" });
		expect(onSwitchWithInterrupt).not.toHaveBeenCalled();
		fireEvent.click(action);
		expect(onSwitchWithInterrupt).toHaveBeenCalledOnce();
	});

	it("does not offer a destructive retry for unrelated failures", () => {
		render(
			<SessionInterfaceTransitionNotice
				transition={{
					...transition("failed"),
					errorCode: "TARGET_UNAVAILABLE",
				}}
				onDismiss={vi.fn()}
				onSwitchWithInterrupt={vi.fn()}
			/>,
		);

		expect(screen.queryByRole("button", { name: "Stop now and switch" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Cancel request and switch" })).not.toBeInTheDocument();
	});

	it("does not offer a destructive retry when terminal quiescence is unverified", () => {
		render(
			<SessionInterfaceTransitionNotice
				transition={{
					...transition("failed"),
					errorCode: "DRAIN_QUIESCENCE_UNVERIFIED",
					errorDetail: "AO could not classify the current Terminal screen.",
				}}
				onDismiss={vi.fn()}
				onSwitchWithInterrupt={vi.fn()}
			/>,
		);

		expect(screen.queryByRole("button", { name: "Stop now and switch" })).not.toBeInTheDocument();
	});
});

describe("a Cloud session that started in the terminal", () => {
	it.each([
		["CHAT_ADAPTER_UNAVAILABLE", "Chat for Claude needs the Claude ACP adapter, which this workspace doesn't have yet."],
		["CHAT_START_UNSUPPORTED", "The AO worker in this workspace is an older version that can't set up Chat for Claude. You can switch this session to Chat."],
	])("reads as information, not a failed switch (%s)", (errorCode, message) => {
		const onDismiss = vi.fn();
		render(
			<SessionInterfaceTransitionNotice
				transition={{
					...transition("failed"),
					policy: "interrupt",
					errorCode,
					errorDetail: "Started in the terminal: control plane copy",
				}}
				onDismiss={onDismiss}
				onRetry={vi.fn()}
				onSwitchWithInterrupt={vi.fn()}
			/>,
		);

		const status = screen.getByRole("status");
		expect(status).toHaveTextContent("Started in the terminal");
		expect(status).toHaveTextContent(message);
		expect(status).not.toHaveTextContent(/failed|Interface switch/);
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		expect(status.querySelector(".text-warning")).toBeNull();
		const buttons = screen.getAllByRole("button");
		expect(buttons).toHaveLength(1);
		fireEvent.click(buttons[0]!);
		expect(onDismiss).toHaveBeenCalledTimes(1);
	});

	it("centers in the part of the pane the harness update notice leaves clear", () => {
		const pane = document.createElement("div");
		document.body.append(pane);
		vi.spyOn(HTMLElement.prototype, "offsetParent", "get").mockReturnValue(pane);
		vi.spyOn(pane, "getBoundingClientRect").mockReturnValue(new DOMRect(260, 60, 1056, 800));
		try {
			const view = render(
				<SessionInterfaceTransitionNotice
					transition={{ ...transition("failed"), errorCode: "CHAT_ADAPTER_UNAVAILABLE" }}
					onDismiss={vi.fn()}
				/>,
			);
			const status = screen.getByRole("status");
			vi.spyOn(status, "getBoundingClientRect").mockReturnValue(new DOMRect(516, 72, 544, 60));
			expect(status.style.left).toBe("");
			act(() => publishHarnessUpdateNoticeRect({ left: 1004, top: 72, right: 1304, bottom: 140 }));
			// The pane reaches 1316px; the notice starts at 1004px, 12px of gap kept.
			// The browser normalizes the calc(); both still subtract the 324px.
			expect(status.style.left).toMatch(/^calc\(.*100% - 324px.*\)$/);
			expect(status.style.maxWidth).toMatch(/^calc\(.*324px.*1\.5rem.*\)$/);
			act(() => publishHarnessUpdateNoticeRect(null));
			expect(status.style.left).toBe("");
			view.unmount();
		} finally {
			act(() => publishHarnessUpdateNoticeRect(null));
			vi.restoreAllMocks();
			pane.remove();
		}
	});

	it("stays centered in a wide pane where the harness update notice does not reach it", () => {
		const pane = document.createElement("div");
		document.body.append(pane);
		vi.spyOn(HTMLElement.prototype, "offsetParent", "get").mockReturnValue(pane);
		vi.spyOn(pane, "getBoundingClientRect").mockReturnValue(new DOMRect(260, 60, 2000, 800));
		try {
			const view = render(
				<SessionInterfaceTransitionNotice
					transition={{ ...transition("failed"), errorCode: "CHAT_ADAPTER_UNAVAILABLE" }}
					onDismiss={vi.fn()}
				/>,
			);
			const status = screen.getByRole("status");
			// Centered, the 544px notice spans 988px to 1532px, well left of the update notice.
			vi.spyOn(status, "getBoundingClientRect").mockReturnValue(new DOMRect(988, 72, 544, 60));
			act(() => publishHarnessUpdateNoticeRect({ left: 1956, top: 72, right: 2256, bottom: 140 }));
			expect(status.style.left).toBe("");
			expect(status.style.maxWidth).toBe("");
			view.unmount();
		} finally {
			act(() => publishHarnessUpdateNoticeRect(null));
			vi.restoreAllMocks();
			pane.remove();
		}
	});

	it("names its dismiss control and dismissal failure in the app's language", async () => {
		await appI18n.changeLanguage("de");
		try {
			render(
				<SessionInterfaceTransitionNotice
					transition={{ ...transition("failed"), errorCode: "CHAT_ADAPTER_UNAVAILABLE" }}
					onDismiss={vi.fn()}
					dismissError="rejected"
				/>,
			);
			expect(screen.getByRole("button", { name: "Meldung zum Oberflächenwechsel schließen" })).toBeInTheDocument();
			expect(screen.getByRole("status")).toHaveTextContent("Diese Meldung konnte nicht geschlossen werden. Versuche es erneut.");
		} finally {
			await act(() => appI18n.changeLanguage("en"));
		}
	});

	it("keeps an unrelated failed switch to Chat a failure", () => {
		render(
			<SessionInterfaceTransitionNotice
				transition={{ ...transition("failed"), errorCode: "CHAT_ADAPTER_UNAVAILABLE", sourceMode: "chat", targetMode: "tui" }}
				onDismiss={vi.fn()}
			/>,
		);
		expect(screen.getByRole("alert")).toHaveTextContent("Interface switch failed");
	});
});

describe("account moves", () => {
	function accountMove(phase: SessionInterfaceTransition["phase"]): SessionInterfaceTransition {
		return {
			...transition(phase),
			sourceMode: "chat",
			targetMode: "chat",
			sourceAccountId: "device-claude",
			targetAccountId: "acct-b",
		};
	}

	it("reports the account switch instead of an interface destination while it runs", () => {
		render(
			<TooltipProvider>
				<SessionInterfaceSwitchButton target="tui" supported transition={accountMove("target_starting")} onClick={vi.fn()} onCancel={vi.fn()} />
			</TooltipProvider>,
		);
		expect(screen.getByRole("status", { name: "Resuming on the new account…" })).toBeInTheDocument();
		expect(screen.queryByText(/Switching to/)).not.toBeInTheDocument();
	});

	it("keeps the agent on its previous account in the failure notice and offers no interface retry", () => {
		render(
			<SessionInterfaceTransitionNotice
				transition={{ ...accountMove("failed"), errorCode: "DRAIN_DECISION_PENDING" }}
				onDismiss={vi.fn()}
				onSwitchWithInterrupt={vi.fn()}
			/>,
		);
		expect(screen.getByText("Account switch failed")).toBeInTheDocument();
		expect(screen.getByText("The agent stays on its previous account. You can retry the switch.")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Cancel request and switch" })).not.toBeInTheDocument();
	});
});
