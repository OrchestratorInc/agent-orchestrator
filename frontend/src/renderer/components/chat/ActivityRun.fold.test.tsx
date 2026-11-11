import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ConversationActivity } from "../../types/conversation";
import { ActivityRun } from "./ActivityRun";
import { ChatLinkProvider } from "./ChatMarkdown";

function command(id: string, text: string, status: ConversationActivity["status"] = "completed"): ConversationActivity {
	return {
		kind: "activity",
		id,
		sequence: Number(id),
		revision: 1,
		activityKind: "command",
		status,
		summary: text,
		detail: { command: text, ...(status === "failed" ? { exitCode: 1 } : {}) },
		createdAt: "",
	};
}

async function openRun(activities: ConversationActivity[]) {
	const user = userEvent.setup();
	render(
		<ChatLinkProvider>
			<ActivityRun activities={activities} />
		</ChatLinkProvider>,
	);
	await user.click(screen.getByRole("button", { name: new RegExp(`Ran ${activities.length} tool calls`) }));
	return user;
}

describe("repeated steps", () => {
	it("folds back-to-back identical steps into one row that still opens onto every call", async () => {
		const user = await openRun([
			command("1", "ao browser console --json | python3 -c 'print(1)'"),
			command("2", "ao browser console --json | python3 -c 'print(2)'"),
		]);
		expect(screen.getAllByText("Checked the browser console")).toHaveLength(1);
		await user.click(screen.getByRole("button", { name: /Checked the browser console/ }));
		expect(screen.getByText(/print\(1\)/)).toBeInTheDocument();
		expect(screen.getByText(/print\(2\)/)).toBeInTheDocument();
	});

	it("keeps a failed repeat and a step in between as their own rows", async () => {
		await openRun([
			command("1", "ao browser console --json"),
			command("2", "ao browser console --json", "failed"),
			command("3", "ao browser errors"),
			command("4", "ao browser console --json"),
		]);
		expect(screen.getAllByText("Checked the browser console")).toHaveLength(3);
	});
});
