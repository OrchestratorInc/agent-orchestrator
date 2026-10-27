import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { aoBridge } from "../../lib/bridge";
import type { ConversationActivity } from "../../types/conversation";
import { ElicitationDock } from "./ElicitationDock";

function activity(detail: ConversationActivity["detail"]): ConversationActivity {
	return {
		kind: "activity",
		id: "question-1",
		sequence: 1,
		revision: 0,
		activityKind: "user_input",
		status: "pending",
		summary: "Choose a direction",
		requestId: "request-1",
		detail,
		createdAt: "2026-08-04T00:00:00Z",
	};
}

/** The marker claude-agent-acp puts on the Other field it adds after each question. */
function otherAnswerFor(questionId: string) {
	return { _askUserQuestionCustomAnswer: { questionId, isCustomAnswer: true } };
}

describe("ElicitationDock", () => {
	const claudeQuestions = {
		type: "object" as const,
		required: ["question_0", "question_1"],
		properties: {
			question_0: {
				type: "string",
				title: "Approach",
				oneOf: [
					{ const: "Native", title: "Native", description: "Use ACP directly" },
					{ const: "Bridge", title: "Bridge" },
				],
			},
			question_0_custom: { type: "string", title: "Other approach", _meta: otherAnswerFor("question_0") },
			question_1: {
				type: "string",
				title: "Language",
				oneOf: [
					{ const: "Go", title: "Go" },
					{ const: "TypeScript", title: "TypeScript" },
				],
			},
			question_1_custom: { type: "string", title: "Other language", _meta: otherAnswerFor("question_1") },
		},
	};

	it("shows the full single-question message above the options instead of its topic label", () => {
		const prompt = "Where should we move the untracked specification file before removing the old worktree? Keep it available as a reference for the test plan.\nChoose the location that works best for you.";
		render(
			<ElicitationDock
				activity={activity({
					inputMode: "form",
					message: prompt,
					schema: {
						type: "object",
						properties: {
							question_0: {
								type: "string",
								title: "Spec file",
								oneOf: [{ const: "Keep", title: "Move it here, untracked" }],
							},
						},
					},
				})}
				onResolve={vi.fn()}
			/>,
		);

		const question = screen.getByText(prompt, { exact: true, normalizer: (text) => text });
		expect(question).toBeVisible();
		expect(question.compareDocumentPosition(screen.getByRole("radio")) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		expect(screen.getByRole("group", { name: /Where should we move/ })).toBeInTheDocument();
	});

	it("shows each full question description when navigating a multi-question form", async () => {
		const user = userEvent.setup();
		const firstPrompt = "Which implementation approach should we use for this project?";
		const secondPrompt = "Which language should we use for the implementation?";
		render(
			<ElicitationDock
				activity={activity({
					inputMode: "form",
					message: "Please answer the following questions.",
					schema: {
						...claudeQuestions,
						properties: {
							...claudeQuestions.properties,
							question_0: { ...claudeQuestions.properties.question_0, description: firstPrompt },
							question_1: { ...claudeQuestions.properties.question_1, description: secondPrompt },
						},
					},
				})}
				onResolve={vi.fn()}
			/>,
		);

		expect(screen.getByRole("group", { name: firstPrompt })).toBeVisible();
		expect(screen.queryByText("Please answer the following questions.")).not.toBeInTheDocument();
		await user.click(screen.getByRole("radio", { name: /Native/ }));
		await user.click(screen.getByRole("button", { name: "Next" }));
		expect(screen.getByRole("group", { name: secondPrompt })).toBeVisible();
		expect(screen.queryByText(firstPrompt)).not.toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Back" }));
		expect(screen.getByRole("group", { name: firstPrompt })).toBeVisible();
	});

	it("shows one Claude question and its Other field at a time", () => {
		render(
			<ElicitationDock
				activity={activity({ inputMode: "form", schema: claudeQuestions })}
				onResolve={vi.fn()}
			/>,
		);

		expect(screen.getByRole("group", { name: /Approach/ })).toBeInTheDocument();
		expect(screen.getByLabelText("Other approach")).toBeInTheDocument();
		expect(screen.queryByRole("group", { name: /Language/ })).not.toBeInTheDocument();
		expect(screen.queryByLabelText("Other language")).not.toBeInTheDocument();
	});

	it("validates the active Claude question before moving forward", async () => {
		const user = userEvent.setup();
		render(
			<ElicitationDock
				activity={activity({ inputMode: "form", schema: claudeQuestions })}
				onResolve={vi.fn()}
			/>,
		);

		await user.click(screen.getByRole("button", { name: "Next" }));

		expect(screen.getByText("Choose an answer.")).toBeInTheDocument();
		expect(screen.queryByRole("group", { name: /Language/ })).not.toBeInTheDocument();
	});

	it("navigates Claude questions and preserves answers when going back", async () => {
		const user = userEvent.setup();
		render(
			<ElicitationDock
				activity={activity({ inputMode: "form", schema: claudeQuestions })}
				onResolve={vi.fn()}
			/>,
		);

		await user.type(screen.getByLabelText("Other approach"), "Hybrid");
		await user.click(screen.getByRole("button", { name: "Next" }));
		expect(screen.getByRole("group", { name: /Language/ })).toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Back" }));
		expect(screen.getByLabelText("Other approach")).toHaveValue("Hybrid");
	});

	it("submits all Claude answers together from the final question", async () => {
		const user = userEvent.setup();
		const onResolve = vi.fn().mockResolvedValue(undefined);
		render(
			<ElicitationDock
				activity={activity({
					inputMode: "form",
					message: "Which implementation should we use?",
					schema: claudeQuestions,
				})}
				onResolve={onResolve}
			/>,
		);

		// Each question is answered once: by a choice or by its Other field.
		await user.click(screen.getByRole("radio", { name: /Native/ }));
		await user.click(screen.getByRole("button", { name: "Next" }));
		await user.type(screen.getByLabelText("Other language"), "Rust");
		await user.click(screen.getByRole("button", { name: "Continue" }));
		expect(onResolve).toHaveBeenCalledWith("request-1", "accept", {
			question_0: "Native",
			question_1_custom: "Rust",
		});
	});

	it("keeps generic MCP forms in the all-fields layout", () => {
		render(
			<ElicitationDock
				activity={activity({
					inputMode: "form",
					schema: {
						type: "object",
						properties: {
							name: { type: "string", title: "Name" },
							team: { type: "string", title: "Team" },
						},
					},
				})}
				onResolve={vi.fn()}
			/>,
		);

		expect(screen.getByLabelText("Name")).toBeInTheDocument();
		expect(screen.getByLabelText("Team")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Next" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Continue" })).toBeInTheDocument();
	});

	it("keeps a generic form's name_custom field independent of name", async () => {
		const user = userEvent.setup();
		const onResolve = vi.fn().mockResolvedValue(undefined);
		render(
			<ElicitationDock
				activity={activity({
					inputMode: "form",
					schema: {
						type: "object",
						required: ["name"],
						properties: {
							name: { type: "string", title: "Name" },
							name_custom: { type: "string", title: "Alias" },
						},
					},
				})}
				onResolve={onResolve}
			/>,
		);

		// Without the adapter's marker, name_custom never stands in for name.
		await user.type(screen.getByLabelText("Alias"), "Al");
		await user.click(screen.getByRole("button", { name: "Continue" }));
		expect(screen.getByText("This field is required.")).toBeInTheDocument();
		expect(onResolve).not.toHaveBeenCalled();

		await user.type(screen.getByLabelText(/^Name/), "Alice");
		await user.type(screen.getByLabelText("Alias"), "ias");
		await user.click(screen.getByRole("button", { name: "Continue" }));
		expect(onResolve).toHaveBeenCalledWith("request-1", "accept", { name: "Alice", name_custom: "Alias" });
	});

	it("keeps required fields actionable instead of sending an invalid form", async () => {
		const user = userEvent.setup();
		const onResolve = vi.fn();
		render(
			<ElicitationDock
				activity={activity({
					inputMode: "form",
					schema: {
						type: "object",
						required: ["name"],
						properties: { name: { type: "string", title: "Name" } },
					},
				})}
				onResolve={onResolve}
			/>,
		);
		await user.click(screen.getByRole("button", { name: "Continue" }));
		expect(screen.getByText("This field is required.")).toBeInTheDocument();
		expect(onResolve).not.toHaveBeenCalled();
	});

	it("gives an invalid boolean the error node its aria-describedby names", async () => {
		const user = userEvent.setup();
		const onResolve = vi.fn();
		render(
			<ElicitationDock
				activity={activity({
					inputMode: "form",
					schema: {
						type: "object",
						required: ["diagnostics"],
						properties: { diagnostics: { type: "boolean", title: "Share diagnostics" } },
					},
				})}
				onResolve={onResolve}
			/>,
		);

		const checkbox = screen.getByRole("checkbox", { name: /Share diagnostics/ });
		expect(checkbox).not.toHaveAttribute("aria-describedby");

		await user.click(screen.getByRole("button", { name: "Continue" }));
		expect(onResolve).not.toHaveBeenCalled();

		// A description that points at nothing reads as an unlabelled error to a
		// screen reader, so the target has to exist and carry the wording.
		const describedBy = checkbox.getAttribute("aria-describedby") ?? "";
		expect(describedBy).not.toBe("");
		expect(document.getElementById(describedBy)).toHaveTextContent("This field is required.");
	});

	it("shows the Other row as a single field named by its placeholder", () => {
		render(
			<ElicitationDock
				activity={activity({ inputMode: "form", schema: claudeQuestions })}
				onResolve={vi.fn()}
			/>,
		);

		// The hidden label keeps the accessible name; the placeholder is what shows.
		const other = screen.getByLabelText("Other approach");
		expect(other).toHaveAttribute("placeholder", "Other approach…");
	});

	it("picks a choice with its number key and jumps to Other with 0", async () => {
		const user = userEvent.setup();
		render(
			<ElicitationDock
				activity={activity({ inputMode: "form", schema: claudeQuestions })}
				onResolve={vi.fn()}
			/>,
		);

		screen.getAllByRole("radio")[0]?.focus();
		await user.keyboard("2");
		expect(screen.getAllByRole("radio")[1]).toBeChecked();
		await user.keyboard("0");
		expect(screen.getByLabelText("Other approach")).toHaveFocus();
	});

	it("sends with Enter on a choice and clears Other when a choice is picked", async () => {
		const user = userEvent.setup();
		render(
			<ElicitationDock
				activity={activity({ inputMode: "form", schema: claudeQuestions })}
				onResolve={vi.fn()}
			/>,
		);

		await user.type(screen.getByLabelText("Other approach"), "Something custom");
		screen.getAllByRole("radio")[0]?.focus();
		await user.keyboard("1");
		expect(screen.getByLabelText("Other approach")).toHaveValue("");
		await user.keyboard("{Enter}");
		expect(screen.getByLabelText("Other language")).toBeInTheDocument();
	});

	it("answers every question from the keyboard alone", async () => {
		const user = userEvent.setup();
		const onResolve = vi.fn().mockResolvedValue(undefined);
		render(
			<ElicitationDock
				activity={activity({ inputMode: "form", schema: claudeQuestions })}
				onResolve={onResolve}
			/>,
		);

		// Focus is placed once; Enter swaps the question under it, and the next
		// number key still has to land in the card.
		screen.getAllByRole("radio")[0]?.focus();
		await user.keyboard("1{Enter}");
		expect(screen.getByRole("radio", { name: /Go/ })).toHaveFocus();
		await user.keyboard("2{Enter}");
		expect(onResolve).toHaveBeenCalledWith("request-1", "accept", {
			question_0: "Native",
			question_1: "TypeScript",
		});
	});

	it("keeps focus in the card when Back leaves the first question", async () => {
		const user = userEvent.setup();
		render(
			<ElicitationDock
				activity={activity({ inputMode: "form", schema: claudeQuestions })}
				onResolve={vi.fn()}
			/>,
		);

		screen.getAllByRole("radio")[0]?.focus();
		await user.keyboard("1{Enter}");
		screen.getByRole("button", { name: "Back" }).focus();
		await user.keyboard("{Enter}");
		expect(screen.getByRole("radio", { name: /Native/ })).toHaveFocus();
	});

	it("opens an external URL only after the user explicitly consents", async () => {
		const user = userEvent.setup();
		const openExternal = vi.spyOn(aoBridge.app, "openExternal").mockResolvedValue(undefined);
		const onResolve = vi.fn().mockResolvedValue(undefined);
		render(
			<ElicitationDock
				activity={activity({ inputMode: "url", url: "https://console.anthropic.com/oauth", message: "Sign in" })}
				onResolve={onResolve}
			/>,
		);
		expect(openExternal).not.toHaveBeenCalled();
		expect(screen.getByText("https://console.anthropic.com/oauth")).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Open console.anthropic.com" }));
		expect(openExternal).toHaveBeenCalledWith("https://console.anthropic.com/oauth");
		expect(onResolve).toHaveBeenCalledWith("request-1", "accept", undefined);
	});

	it("refuses unsafe URL schemes", () => {
		render(
			<ElicitationDock
				activity={activity({ inputMode: "url", url: "file:///Users/alice/.ssh/id_rsa" })}
				onResolve={vi.fn()}
			/>,
		);
		expect(screen.getByRole("alert")).toHaveTextContent(/unsafe or invalid URL/i);
		expect(screen.getByRole("button", { name: "Open link" })).toBeDisabled();
	});

	it("makes the form body scrollable with composer scrollbar and caps dock height", () => {
		render(
			<ElicitationDock
				activity={activity({ inputMode: "form", schema: claudeQuestions })}
				onResolve={vi.fn()}
			/>,
		);

		const dock = screen.getByTestId("elicitation-dock");
		expect(dock).toHaveClass("flex", "max-h-[min(65vh,32rem)]", "flex-col", "overflow-hidden");

		const scrollBody = screen.getByTestId("elicitation-scroll-body");
		expect(scrollBody).toHaveClass("chat-composer-scrollbar", "min-h-0", "flex-1", "overflow-y-auto", "overscroll-contain");
	});

	it("keeps multi-question generic forms in a scrollable body with pinned footer", () => {
		const genericSchema = {
			type: "object" as const,
			properties: {
				scope: {
					type: "string",
					title: "Scope",
					description: "Which scope?",
					oneOf: [
						{ const: "entries", title: "Only entries" },
						{ const: "all", title: "All crontab" },
					],
				},
				scope_other: { type: "string", title: "Scope (other)" },
				location: {
					type: "string",
					title: "Code location",
					description: "Where to put code?",
					oneOf: [
						{ const: "tool", title: "Own tool" },
						{ const: "repo", title: "In repo" },
					],
				},
				location_other: { type: "string", title: "Code location (other)" },
			},
		};

		render(
			<ElicitationDock
				activity={activity({ inputMode: "form", schema: genericSchema })}
				onResolve={vi.fn()}
			/>,
		);

		const scrollBody = screen.getByTestId("elicitation-scroll-body");
		expect(scrollBody).toHaveClass("chat-composer-scrollbar", "overflow-y-auto");

		expect(screen.getByRole("group", { name: /Scope/ })).toBeInTheDocument();
		expect(screen.getByLabelText("Scope (other)")).toBeInTheDocument();
		expect(screen.getByRole("group", { name: /Code location/ })).toBeInTheDocument();
		expect(screen.getByLabelText("Code location (other)")).toBeInTheDocument();

		expect(screen.getByRole("button", { name: "Cancel" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Skip" })).toBeInTheDocument();
		const continueBtn = screen.getByRole("button", { name: "Continue" });
		expect(continueBtn).toBeInTheDocument();
		expect(scrollBody).not.toContainElement(continueBtn);
	});

	it("caps a long question so the answers and actions keep their room", () => {
		const longQuestion = "Before I refactor the scheduler, confirm the scope. ".repeat(20).trim();
		const schema = {
			type: "object" as const,
			properties: { answer: { type: "string", title: "Answer" } },
			required: ["answer"],
		};
		render(
			<ElicitationDock activity={activity({ inputMode: "form", message: longQuestion, schema })} onResolve={vi.fn()} />,
		);

		const question = screen.getByTestId("elicitation-question");
		expect(question).toHaveTextContent(longQuestion);
		expect(question).toHaveClass("max-h-[min(25vh,8rem)]", "overflow-y-auto");
		expect(screen.getByTestId("elicitation-scroll-body")).toContainElement(screen.getByLabelText(/Answer/));
		expect(question).not.toContainElement(screen.getByRole("button", { name: "Continue" }));
	});

	it("scrolls a long URL inside the dock with the consent actions pinned", () => {
		const longURL = `https://console.anthropic.com/oauth/authorize?${"scope=org%3Acreate_api_key&".repeat(40)}`;
		render(
			<ElicitationDock
				activity={activity({ inputMode: "url", url: longURL, message: "Sign in" })}
				onResolve={vi.fn()}
			/>,
		);

		expect(screen.getByTestId("elicitation-dock")).toHaveClass("max-h-[min(65vh,32rem)]", "flex-col");
		const scrollBody = screen.getByTestId("elicitation-scroll-body");
		expect(scrollBody).toHaveClass("min-h-0", "flex-1", "overflow-y-auto");
		expect(scrollBody).toContainElement(screen.getByText(longURL));
		for (const name of ["Cancel", "Decline", "Open console.anthropic.com"]) {
			expect(scrollBody).not.toContainElement(screen.getByRole("button", { name }));
		}
	});

	it("keeps an open failure beside the actions when a long URL scrolls", async () => {
		const user = userEvent.setup();
		vi.spyOn(aoBridge.app, "openExternal").mockRejectedValue(new Error("refused"));
		const onResolve = vi.fn();
		const longURL = `https://console.anthropic.com/oauth/authorize?${"scope=org%3Acreate_api_key&".repeat(40)}`;
		render(<ElicitationDock activity={activity({ inputMode: "url", url: longURL, message: "Sign in" })} onResolve={onResolve} />);

		await user.click(screen.getByRole("button", { name: "Open console.anthropic.com" }));

		const error = await screen.findByRole("alert");
		expect(error).toHaveTextContent("The link could not be opened. Nothing was approved.");
		expect(screen.getByTestId("elicitation-scroll-body")).not.toContainElement(error);
		expect(onResolve).not.toHaveBeenCalled();
	});
});
