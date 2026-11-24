import { execFileSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import path from "node:path";
import ts from "typescript";
import { expect, it } from "vitest";

const BASELINE = 917;

function countBareErrors(filename: string, contents: string): number {
	if (!contents.includes("Error") || !/\bnew\b/.test(contents)) return 0;
	const source = ts.createSourceFile(filename, contents, ts.ScriptTarget.Latest);
	let count = 0;
	function visit(node: ts.Node): void {
		if (ts.isNewExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === "Error") count++;
		ts.forEachChild(node, visit);
	}
	visit(source);
	return count;
}

it("counts bare Error constructions without counting comments, strings or subclasses", () => {
	expect(countBareErrors("app.ts", 'new Error("text"); new Error ("more"); new AoError("CODE", "text"); // new Error()')).toBe(2);
});

it("keeps bare Error constructors at or below the app baseline", () => {
	const root = execFileSync("git", ["rev-parse", "--show-toplevel"], { encoding: "utf8" }).trim();
	const files = new Set(execFileSync("git", ["ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "frontend/src", "packages"], { cwd: root, encoding: "utf8" })
		.split("\0")
		.filter((file) => /\.[cm]?[jt]sx?$/.test(file) && !/\.(?:test|spec|fakes)\.|\/(?:__tests__|__mocks__|test|fixtures)\//.test(file)));
	const count = [...files].map((file) => path.join(root, file)).filter(existsSync)
		.reduce((sum, file) => sum + countBareErrors(file, readFileSync(file, "utf8")), 0);
	expect(count, "Use AoError with a stable code for new application errors").toBeLessThanOrEqual(BASELINE);
});
