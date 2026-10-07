import { describe, expect, it } from "vitest";
import {
	formatFileAnnotationMessage,
	formatFileChatReference,
	MAX_FILE_ANNOTATION_MESSAGE_LENGTH,
	type FileAnnotationTarget,
	type FileCodeReference,
} from "./file-annotations";

describe("formatFileAnnotationMessage", () => {
	it("formats precise new-side line context for the session agent", () => {
		const target: FileAnnotationTarget = {
			path: "src/App.tsx",
			side: "new",
			line: 42,
			oldLine: 41,
			newLine: 42,
			lineKind: "add",
			lineText: "return <Button />;",
		};

		const message = formatFileAnnotationMessage(target, "Use the shared primary action here.");

		expect(message).toContain("Use the shared primary action here.");
		expect(message).toContain("- Path: src/App.tsx");
		expect(message).toContain("- Location: New side, line 42");
		expect(message).toContain("- Code: return <Button />;");
	});

	it("supports whole-file feedback and caps the injected message", () => {
		const message = formatFileAnnotationMessage(
			{ path: "README.md", previousPath: "README.old.md", side: "file" },
			"x".repeat(10_000),
		);

		expect(message).toContain("- Location: Entire file");
		expect(message).toContain("- Previous path: README.old.md");
		expect(message.length).toBeLessThanOrEqual(MAX_FILE_ANNOTATION_MESSAGE_LENGTH);
	});
});

describe("formatFileChatReference", () => {
	const diffRange: FileCodeReference = {
		path: "src/retry.ts",
		side: "new",
		line: 10,
		endLine: 12,
		lines: [
			{ kind: "context", oldNo: 10, newNo: 10, text: "const retries = 2;" },
			{ kind: "del", oldNo: 11, newNo: null, text: "retry();" },
			{ kind: "add", oldNo: null, newNo: 11, text: "await retry();" },
		],
	};

	it("builds a compact chip that expands to the referenced diff rows with their markers", () => {
		const reference = formatFileChatReference(diffRange);
		expect(reference.display).toBe("retry.ts#L10-L12");
		expect(reference.wire).toContain("[src/retry.ts#L10-L12]");
		expect(reference.wire).toContain("```diff\n const retries = 2;\n-retry();\n+await retry();\n```");
	});

	it("quotes file lines with their indentation, tagged with the file's language", () => {
		const reference = formatFileChatReference({
			path: "server/main.py",
			side: "file",
			line: 7,
			endLine: 8,
			lines: [
				{ kind: "context", oldNo: 7, newNo: 7, text: "    if retry:" },
				{ kind: "context", oldNo: 8, newNo: 8, text: "        return run()" },
			],
		});
		expect(reference.display).toBe("main.py#L7-L8");
		expect(reference.wire).toContain("```py\n    if retry:\n        return run()\n```");
	});

	it("marks an old-side single line and leaves an extensionless file's fence untagged", () => {
		const old = formatFileChatReference({ path: "a/b.go", side: "old", line: 5, endLine: 5, lines: [{ kind: "del", oldNo: 5, newNo: null, text: "x := 1" }] });
		expect(old.display).toBe("b.go#L5 (old)");
		expect(old.wire).toContain("[a/b.go#L5] (old)");
		expect(old.wire).toContain("```diff\n-x := 1\n```");
		const plain = formatFileChatReference({ path: "Dockerfile", side: "file", line: 1, endLine: 1, lines: [{ kind: "context", oldNo: 1, newNo: 1, text: "FROM node:22" }] });
		expect(plain.wire).toContain("```\nFROM node:22\n```");
	});

	it("does not let quoted code close its fence", () => {
		const reference = formatFileChatReference({ ...diffRange, lines: [{ kind: "context", oldNo: 1, newNo: 1, text: "```" }, ...diffRange.lines] });
		expect(reference.wire.match(/^```$/gm)?.length).toBe(1);
	});

	it("keeps a huge selection bounded and says what it left out", () => {
		const lines = Array.from({ length: 2000 }, (_, index) => ({ kind: "context" as const, oldNo: index + 1, newNo: index + 1, text: `line ${index + 1} ${"x".repeat(40)}` }));
		const reference = formatFileChatReference({ path: "big.ts", side: "file", line: 1, endLine: 2000, lines });
		expect(reference.wire.length).toBeLessThan(8400);
		expect(reference.wire).toMatch(/… \d+ more lines omitted …/);
		expect(reference.wire).toContain("line 1 ");
		expect(reference.wire).toContain("line 2000 ");
	});
});
