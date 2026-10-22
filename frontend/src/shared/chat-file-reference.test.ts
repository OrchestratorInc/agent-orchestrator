import { describe, expect, it } from "vitest";
import { captureFileReference, formatCapturedFileReferences } from "./chat-file-reference";
import { normalizeCodeRowText } from "./file-annotations";

describe("captured row boundaries", () => {
	it("formats bounded cloud captures in order without clipping again or mutating them", () => {
		const ref = captureFileReference({path:"a.ts",side:"new",line:1,endLine:12,lines:Array.from({length:12},(_,i)=>({kind:"add",oldNo:null,newNo:i+1,text:`${i} ${"x".repeat(690)}`}))},"session");
		const first = captureFileReference({path:"b.ts",side:"file",line:1,endLine:1,lines:[{kind:"context",oldNo:1,newNo:1,text:"```UNIQUE_FENCE"}]},"session");
		first.order=1;ref.order=2;
		const saved=JSON.stringify([ref,first]);
		const output=formatCapturedFileReferences("question",[ref,first]);
		expect(output.indexOf("b.ts#L1-L1")).toBeLessThan(output.indexOf("a.ts#L1-L12"));
		for (const row of ref.lines) expect(output).toContain(`+${row.text}\n`);
		expect(output).toContain("``\u200b`UNIQUE_FENCE");
		expect(output).toContain("[Captured selection truncated]");
		expect(JSON.stringify([ref,first])).toBe(saved);
	});
 it.each(["\n", "\r\n", "\r"])("normalizes a single row terminator %j without trimming code", ending => {
  const texts = ["services:", ending, "  web:" + ending, " \t" + ending, "😀  "];
  const selection = { path: "docker-compose.yaml", side: "file" as const, line: 1, endLine: texts.length,
   lines: texts.map((text, i) => ({kind: "context" as const, oldNo:i+1, newNo:i+1, text})) };
  const before = JSON.stringify(selection);
  const ref = captureFileReference(selection, "session");
  expect(ref.lines.map(row => row.text)).toEqual(["services:", "", "  web:", " \t", "😀  "]);
  expect(ref.truncated).toBe(false);
  expect(ref.lines.map(row => row.newNo)).toEqual([1,2,3,4,5]);
  expect(JSON.stringify(selection)).toBe(before);
  expect(ref.lines.map(row => normalizeCodeRowText(row.text))).toEqual(ref.lines.map(row => row.text));
 });
 it.each(["one\ntwo", "\n\n", "one\r\ntwo\r\n", "one\0two"])("does not flatten malformed multi-line/NUL row %j", text => {
  expect(normalizeCodeRowText(text)).toBe(text);
 });
 it("does not count a viewer terminator as capture truncation at the per-row boundary", () => {
  const ref = captureFileReference({path:"a.ts",side:"file",line:1,endLine:1,lines:[{kind:"context",oldNo:1,newNo:1,text:"x".repeat(700)+"\r\n"}]},"session");
  expect(ref.lines[0].text).toHaveLength(700);
  expect(ref.truncated).toBe(false);
 });
});
