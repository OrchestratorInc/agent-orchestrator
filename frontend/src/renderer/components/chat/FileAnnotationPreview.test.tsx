import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { captureFileReference } from "../../../shared/chat-file-reference";
import { FileAnnotationContext } from "./FileAnnotationPreview";
import { ChatAnnotationSummary } from "./ChatAnnotationSummary";

const request = vi.hoisted(() => vi.fn());
vi.mock("../../lib/host-clients", () => ({ clientForSessionHost: () => ({ POST: request }) }));
vi.mock("../../lib/workspace-file-events", () => ({ subscribeWorkspaceFileChanges: () => () => {} }));

const ref = captureFileReference(
	{ path: "src/a.ts", side: "file", line: 1, endLine: 1, lines: [{ kind: "context", oldNo: 1, newNo: 1, text: "captured();" }] },
	"session",
);
beforeEach(() => { request.mockReset(); });
function setup(onOpen?: () => void, generation = { current: 0 }, reference = ref) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<FileAnnotationContext.Provider value={{ sessionId: "session", hostId: "remote-one", onOpen, generation }}>
				<ChatAnnotationSummary annotations={[{ id: reference.id, text: reference.path, fileReference: reference }]} />
			</FileAnnotationContext.Provider>
		</QueryClientProvider>,
	);
	return client;
}
describe("captured file source state", () => {
	it("loads only when opened, revalidates invalidation, and preserves captured code", async () => {
		request.mockResolvedValue({ data: { state: "changed" } });
		const client = setup();
		expect(request).not.toHaveBeenCalled();
		fireEvent.click(screen.getByRole("button", { name: "1 annotation" }));
		await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Source changed"));
		expect(screen.getByText("captured();")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /src\/a.ts/ })).not.toBeInTheDocument();
		request.mockResolvedValue({ data: { state: "unavailable" } });
		await client.invalidateQueries({ queryKey: ["file-annotation-source", "remote-one", "session"] });
		await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Source unavailable"));
		expect(ref.lines[0].text).toBe("captured();");
	});
});

it("rechecks before opening and cancels an older click", async () => {
	request.mockResolvedValue({ data: { state: "unchanged", path: ref.path, line: 1, endLine: 1 } });
	const open = vi.fn(),
		generation = { current: 0 };
	setup(open, generation);
	fireEvent.click(screen.getByRole("button", { name: "1 annotation" }));
	const button = await screen.findByRole("button", { name: /^Open file:/ });
	let finish!: (value: unknown) => void;
	request.mockImplementationOnce(
		() =>
			new Promise((resolve) => {
				finish = resolve;
			}),
	);
	fireEvent.click(button);
	generation.current++;
	finish({ data: { state: "unchanged", path: ref.path, line: 1, endLine: 1 } });
	await waitFor(() => expect(button).not.toHaveAttribute("aria-disabled", "true"));
	expect(open).not.toHaveBeenCalled();
	request.mockResolvedValueOnce({ data: { state: "changed" } });
	fireEvent.click(button);
	await waitFor(() => expect(open).toHaveBeenCalledWith(ref, { state: "changed" }));
});

it("opens from the whole annotation and supports keyboard activation", async () => {
	const user = userEvent.setup();
	request.mockResolvedValue({ data: { state: "unchanged", path: ref.path, line: 1, endLine: 1 } });
	const open = vi.fn();
	setup(open);
	await user.click(screen.getByRole("button", { name: "1 annotation" }));
	await screen.findByRole("button", { name: /^Open file: src\/a.ts/ });
	expect(screen.queryByText("Open selected lines")).not.toBeInTheDocument();
	await user.click(screen.getByText("captured();"));
	await waitFor(() => expect(open).toHaveBeenCalledOnce());
	await user.click(screen.getByRole("button", { name: "1 annotation" }));
	(await screen.findByRole("button", { name: /^Open file: src\/a.ts/ })).focus();
	await user.keyboard("{Enter}");
	await waitFor(() => expect(open).toHaveBeenCalledTimes(2));
});

it("keeps capture-truncated references static even if the resolver reports unchanged", async () => {
	request.mockResolvedValue({ data: { state: "unchanged" } });
	setup(vi.fn(), { current: 0 }, { ...ref, truncated: true });
	fireEvent.click(screen.getByRole("button", { name: "1 annotation" }));
	await waitFor(() => expect(request).toHaveBeenCalledOnce());
	expect(screen.queryByRole("button", { name: /^Open file:/ })).not.toBeInTheDocument();
	expect(screen.getByText("Captured selection truncated")).toBeInTheDocument();
});

it.each([
 [{ error: {code: "CHAT_FILE_REFERENCE_INVALID"} }, /Selection could not be validated/],
 [{ error: {code: "INTERNAL_ERROR"} }, /Source check failed/],
])("does not report request errors as missing source (%j)", async (response, label) => {
 request.mockResolvedValue(response);
 setup();
 fireEvent.click(screen.getByRole("button", {name:"1 annotation"}));
 await waitFor(()=>expect(screen.getByRole("status")).toHaveTextContent(label));
 expect(screen.getByRole("status")).not.toHaveTextContent("Source unavailable");
 expect(screen.getByText("captured();")).toBeInTheDocument();
 expect(ref.lines[0].text).toBe("captured();");
});
it("distinguishes a disconnected source check from an unavailable source", async () => {
 request.mockRejectedValue(new TypeError("Failed to fetch"));setup();
 fireEvent.click(screen.getByRole("button", {name:"1 annotation"}));
 await waitFor(()=>expect(screen.getByRole("status")).toHaveTextContent("Source check failed"));
 expect(screen.getByRole("status")).not.toHaveTextContent("Source unavailable");
});

it("blocks navigation after revalidation rejects the annotation", async () => {
 request.mockResolvedValue({data:{state:"unchanged",path:ref.path,line:1,endLine:1}});
 const open=vi.fn();setup(open);
 fireEvent.click(screen.getByRole("button",{name:"1 annotation"}));
 const card=await screen.findByRole("button",{name:/^Open file:/});
 request.mockResolvedValueOnce({error:{code:"CHAT_FILE_REFERENCE_INVALID"}});
 fireEvent.click(card);
 await waitFor(()=>expect(screen.getByRole("status")).toHaveTextContent("Selection could not be validated"));
 expect(open).not.toHaveBeenCalled();
 expect(screen.queryByRole("button",{name:/^Open file:/})).not.toBeInTheDocument();
 expect(screen.getByText("captured();")).toBeInTheDocument();
});

it("opens a uniquely moved source without rewriting its captured reference", async () => {
	const moved = { state: "moved", path: ref.path, line: 3, endLine: 3, newLineOffset: 2 };
	request.mockResolvedValue({ data: moved });
	const open = vi.fn();
	setup(open);
	fireEvent.click(screen.getByRole("button", { name: "1 annotation" }));
	await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Source moved"));
	fireEvent.click(screen.getByRole("button", { name: /^Open file:/ }));
	await waitFor(() => expect(open).toHaveBeenCalledWith(ref, moved));
	expect(ref.line).toBe(1);
	expect(ref.lines[0].newNo).toBe(1);
});

it("can retry a failed check without navigating or replacing captured code", async () => {
 request.mockRejectedValueOnce(new TypeError("Failed to fetch"));
 const open=vi.fn();setup(open);
 fireEvent.click(screen.getByRole("button",{name:"1 annotation"}));
 await waitFor(()=>expect(screen.getByRole("status")).toHaveTextContent("Source check failed"));
 request.mockResolvedValue({data:{state:"unchanged",path:ref.path,line:1,endLine:1}});
 fireEvent.click(screen.getByRole("button",{name:"Retry source check"}));
 await screen.findByRole("button",{name:/^Open file:/});
 expect(open).not.toHaveBeenCalled();expect(ref.lines[0].text).toBe("captured();");
});
it("does not offer retry for an invalid saved capture", async () => {
 request.mockResolvedValue({error:{code:"CHAT_FILE_REFERENCE_INVALID"}});setup(vi.fn());
 fireEvent.click(screen.getByRole("button",{name:"1 annotation"}));
 await waitFor(()=>expect(screen.getByRole("status")).toHaveTextContent("Remove it and add the selection again"));
 expect(screen.queryByRole("button",{name:"Retry source check"})).not.toBeInTheDocument();
});

it("opens a changed source after revalidation without replacing captured code", async () => {
 request.mockResolvedValue({data:{state:"changed"}});
 const open=vi.fn();setup(open);
 fireEvent.click(screen.getByRole("button",{name:"1 annotation"}));
 const card=await screen.findByRole("button",{name:/^Open file:/});
 fireEvent.click(card);
 await waitFor(()=>expect(open).toHaveBeenCalledWith(ref,{state:"changed"}));
 expect(request).toHaveBeenCalledTimes(2);
 expect(ref.lines[0].text).toBe("captured();");
});

it("does not open a file deleted between preview and click", async () => {
 request.mockResolvedValue({data:{state:"changed"}});
 const open=vi.fn();setup(open);
 fireEvent.click(screen.getByRole("button",{name:"1 annotation"}));
 const card=await screen.findByRole("button",{name:/^Open file:/});
 request.mockResolvedValueOnce({data:{state:"unavailable"}});
 fireEvent.click(card);
 await waitFor(()=>expect(screen.getByRole("status")).toHaveTextContent("Source unavailable"));
 expect(open).not.toHaveBeenCalled();
 expect(screen.queryByRole("button",{name:/^Open file:/})).not.toBeInTheDocument();
});


it.each([undefined, "ambiguous"])("opens a verified renamed but changed file (%s) without rewriting capture", async (reason) => {
 const resolution={state:"changed",path:"src/renamed.ts",reason};
 request.mockResolvedValue({data:resolution});
 const open=vi.fn();setup(open);
 fireEvent.click(screen.getByRole("button",{name:"1 annotation"}));
 const card=await screen.findByRole("button",{name:/^Open file: src\/a.ts/});
 fireEvent.click(card);
 await waitFor(()=>expect(open).toHaveBeenCalledWith(ref,resolution));
 expect(request).toHaveBeenCalledTimes(2);
 expect(ref.path).toBe("src/a.ts");
 expect(ref.lines[0].text).toBe("captured();");
 expect(resolution).not.toHaveProperty("line");
});
