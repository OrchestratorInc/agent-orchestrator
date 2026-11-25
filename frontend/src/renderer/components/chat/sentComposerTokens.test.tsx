import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { recordSentComposerTokens, sessionTokenScope, useComposerTokenRenderer } from "./sentComposerTokens";

function SentText({ sessionId, text }: { sessionId: string; text: string }) {
	const renderTokens = useComposerTokenRenderer(sessionId, text);
	return <p>{renderTokens ? renderTokens(text) : text}</p>;
}

describe("sent composer tokens", () => {
	it("shows a sent @-file as a file chip with its Files-tree icon and the sent text", () => {
		recordSentComposerTokens(sessionTokenScope("session-file-chip"), [
			{ kind: "file", value: "src/example.ts", display: "example.ts", wire: "src/example.ts" },
		]);
		const { container } = render(<SentText sessionId="session-file-chip" text="Explain src/example.ts please" />);

		const token = container.querySelector('[data-message-token="file"]');
		expect(token).toHaveTextContent("src/example.ts");
		expect(token?.querySelector("svg")).toHaveAttribute("data-file-icon", "typescript");
		expect(container.textContent).toBe("Explain src/example.ts please");
	});
});
