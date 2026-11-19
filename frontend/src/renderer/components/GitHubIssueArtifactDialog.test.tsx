import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import { GitHubIssueArtifactDialog } from "./GitHubIssueArtifactDialog";

vi.mock("./GitHubRepoPicker", () => ({
	GitHubRepoPicker: ({ onChange }: { onChange: (url: string) => void }) =>
		<>{["first", "second"].map(repo => <button key={repo} onClick={() => onChange(`https://github.com/team/${repo}`)}>{repo}</button>)}</>,
}));

it("clears the old destination and posts the full saved file only after confirmation", async () => {
	const user = userEvent.setup();
	const find = vi.spyOn(aoBridge.app, "findArtifactIssues").mockImplementation(async repo => [{ number: 42, title: repo }]);
	const publish = vi.spyOn(aoBridge.app, "publishIssueArtifact").mockResolvedValue("https://github.com/team/second/issues/42/artifacts/1");
	const content = "# Notes\n" + "x".repeat(300_000);
	const read = vi.fn(async () => content);
	render(<QueryClientProvider client={new QueryClient()}><GitHubIssueArtifactDialog name="notes.md" readContent={read} onClose={() => {}} /></QueryClientProvider>);
	await user.click(screen.getByRole("button", { name: "first" }));
	const picker = screen.getByRole("combobox", { name: "Issue" });
	expect(picker).toHaveAttribute("aria-expanded", "false");
	await user.click(picker);
	expect(picker).toHaveAttribute("aria-expanded", "true");
	await user.click(await screen.findByRole("option", { name: /#42.*first/ }));
	expect(picker).toHaveAttribute("aria-expanded", "false");
	await user.click(screen.getByRole("button", { name: "second" }));
	expect(screen.getByRole("button", { name: "Add to issue" })).toBeDisabled();
	expect(read).not.toHaveBeenCalled();
	await user.click(picker);
	await screen.findByRole("option", { name: /#42.*second/ });
	await user.keyboard("{ArrowDown}{Enter}");
	await user.click(screen.getByRole("button", { name: "Add to issue #42" }));
	expect(await screen.findByRole("button", { name: "View on GitHub" })).toBeVisible();
	expect(publish).toHaveBeenCalledWith({ repository: "team/second", issue: 42, name: "notes.md", body: content });
	find.mockRestore();
	publish.mockRestore();
});

it("resolves a pasted issue URL before choosing a repository and keeps the link visible", async () => {
	const user = userEvent.setup();
	const url = "https://github.com/OrchestratorInc/agent-orchestrator/issues/8630#issuecomment-1";
	const find = vi.spyOn(aoBridge.app, "findArtifactIssues").mockResolvedValue([{ number: 8630, title: "HTML artifact preview" }]);
	const read = vi.fn(async () => "# Demo");
	render(<QueryClientProvider client={new QueryClient()}><GitHubIssueArtifactDialog name="demo.md" readContent={read} onClose={() => {}} /></QueryClientProvider>);
	const field = screen.getByLabelText("Issue");
	await user.click(field);
	await user.paste(url);
	expect(await screen.findByRole("link", { name: /#8630.*HTML artifact preview/ })).toHaveAttribute("href", url.split("#")[0]);
	expect(field).toHaveTextContent(url);
	expect(find).toHaveBeenCalledWith("OrchestratorInc/agent-orchestrator", "8630");
	expect(screen.getByRole("button", { name: "Add to issue #8630" })).toBeEnabled();
	await user.click(field);
	await user.clear(screen.getByPlaceholderText(/Paste an issue link/));
	await user.paste("https://github.com/team/second/pull/42");
	expect(screen.getByRole("status")).toHaveTextContent("Use a GitHub issue link");
	expect(screen.getByRole("button", { name: "Add to issue" })).toBeDisabled();
	expect(read).not.toHaveBeenCalled();
	find.mockRestore();
});

it("offers another search or a direct link when there are no matches", async () => {
	const user = userEvent.setup();
	const find = vi.spyOn(aoBridge.app, "findArtifactIssues").mockResolvedValue([]);
	render(<QueryClientProvider client={new QueryClient()}><GitHubIssueArtifactDialog name="demo.md" readContent={vi.fn()} onClose={() => {}} /></QueryClientProvider>);
	await user.click(screen.getByRole("button", { name: "first" }));
	await user.click(screen.getByLabelText("Issue"));
	await user.type(screen.getByPlaceholderText(/Paste an issue link/), "missing");
	expect(await screen.findByText("Try another title, enter an issue number, or paste its GitHub link.")).toBeVisible();
	expect(screen.getByRole("button", { name: "Add to issue" })).toBeDisabled();
	find.mockRestore();
});
