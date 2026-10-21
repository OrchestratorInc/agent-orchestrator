import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { ConversationActivity } from "../../types/conversation";
import { ElicitationHistory } from "./ElicitationDock";

function question(overrides: Partial<ConversationActivity> = {}): ConversationActivity {
	return {
		kind: "activity", id: "question-1", sequence: 1, revision: 1,
		activityKind: "user_input", status: "resolved", summary: "Choose worktrees",
		createdAt: "2026-10-19T00:00:00Z",
		detail: {
			inputMode: "form", message: "Which worktrees should I delete?",
			schema: {
				type: "object",
				properties: {
					question_0: {
						type: "array", title: "Delete",
						items: { anyOf: [
							{ const: "a", title: "fix/login", description: "Merged with no local changes" },
							{ const: "b", title: "fix/settings" },
						] },
					},
					question_0_custom: { type: "string", title: "Other" },
				},
			},
		},
		inputResponse: { action: "accept", content: { question_0: ["a", "b"], question_0_custom: "Keep fix/settings until I check it.\nShow me the names first." } },
		...overrides,
	};
}

describe("ElicitationHistory", () => {
	it("retains the full prompt, selected option labels and descriptions, and verbatim feedback", () => {
		render(<ElicitationHistory activity={question()} />);
		expect(screen.getByText("Which worktrees should I delete?")).toBeVisible();
		expect(screen.getByText("fix/login — Merged with no local changes, fix/settings")).toBeVisible();
		expect(screen.getByText("Keep fix/settings until I check it.\nShow me the names first.", { normalizer: (value) => value })).toBeVisible();
		expect(screen.queryByRole("button")).not.toBeInTheDocument();
	});

	it("associates each answer with its full prompt in a multi-question form", () => {
		render(<ElicitationHistory activity={question({
			detail: { schema: { properties: {
				question_0: { title: "Delete", description: "Which worktrees should I delete?" },
				question_1: { title: "Keep", description: "Which worktrees should I keep?" },
			} } },
			inputResponse: { action: "accept", content: { question_0: "fix/login", question_1: "fix/settings" } },
		})} />);
		expect(screen.getByText("Which worktrees should I delete?").parentElement).toHaveTextContent("fix/login");
		expect(screen.getByText("Which worktrees should I keep?").parentElement).toHaveTextContent("fix/settings");
	});

	it.each([
		{ action: "decline", label: "Skipped" },
		{ action: "cancel", label: "Cancelled" },
	] satisfies Array<{ action: "decline" | "cancel"; label: string }>)("shows $label without claiming an answer", ({ action, label }) => {
		render(<ElicitationHistory activity={question({ inputResponse: { action } })} />);
		expect(screen.getByText(label)).toBeVisible();
		expect(screen.getByText("Which worktrees should I delete?")).toBeVisible();
		expect(screen.queryByText("Answered")).not.toBeInTheDocument();
		expect(screen.queryByText("Answer unavailable")).not.toBeInTheDocument();
	});

	it("preserves an expired question without presenting a stale answer as accepted", () => {
		render(<ElicitationHistory activity={question({ status: "failed" })} />);
		expect(screen.getByText("Question expired")).toBeVisible();
		expect(screen.queryByText("Answered")).not.toBeInTheDocument();
		expect(screen.queryByText(/Keep fix\/settings until/)).not.toBeInTheDocument();
	});

	it("keeps generic false and zero answers and omits empty optional fields", () => {
		render(<ElicitationHistory activity={question({
			detail: { message: "Confirm cleanup", schema: { properties: {
				removeBranches: { type: "boolean", title: "Remove local branches" },
				keepDays: { type: "integer", title: "Days to keep" },
				note: { type: "string", title: "Note" },
			} } },
			inputResponse: { action: "accept", content: { removeBranches: false, keepDays: 0, note: "" } },
		})} />);
		expect(screen.getByText("No")).toBeVisible();
		expect(screen.getByText("0")).toBeVisible();
		expect(screen.queryByText("Note")).not.toBeInTheDocument();
	});

	it("retains URL consent as a read-only record", () => {
		render(<ElicitationHistory activity={question({
			detail: { inputMode: "url", message: "Connect the account", url: "https://example.com/connect" },
			inputResponse: { action: "accept" },
		})} />);
		expect(screen.getByText("Connect the account")).toBeVisible();
		expect(screen.getByText("https://example.com/connect")).toBeVisible();
		expect(screen.queryByText("Answer unavailable")).not.toBeInTheDocument();
	});
});
