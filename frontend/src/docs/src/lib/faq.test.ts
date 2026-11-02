// @vitest-environment node
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { faqEntries } from "./faq";
import { JSX_TAG } from "./markdown-export";

describe("faqEntries", () => {
  it("reads each accordion as a question and a plain-text answer", () => {
    const source = '---\ntitle: FAQ\n---\n\n<Accordions>\n  <Accordion title="Is it free?">\n    Yes. See [pricing](/pricing).\n  </Accordion>\n</Accordions>\n';
    expect(faqEntries(source)).toEqual([{ question: "Is it free?", answer: "Yes. See pricing." }]);
  });

  it("finds every question on the FAQ page", () => {
    const source = readFileSync(fileURLToPath(new URL("../../content/faq.mdx", import.meta.url)), "utf8");
    const entries = faqEntries(source);
    expect(entries).toHaveLength((source.match(/<Accordion /g) ?? []).length);
    expect(entries.every(({ answer }) => answer.length > 0 && !JSX_TAG.test(answer))).toBe(true);
  });
});
