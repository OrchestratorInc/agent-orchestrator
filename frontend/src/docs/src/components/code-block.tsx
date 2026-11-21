"use client";

import { useLingui } from "@lingui/react";
import { CodeBlock, type CodeBlockProps, Pre } from "fumadocs-ui/components/codeblock";

/**
 * The article around a code block is marked English, but the copy button is
 * part of the interface, so the frame takes the interface language and only
 * the code itself stays English.
 */
export function LocalizedCodeBlock({ children, ...props }: CodeBlockProps) {
  const { i18n } = useLingui();
  return (
    <CodeBlock {...props} lang={i18n.locale}>
      <Pre lang="en">{children}</Pre>
    </CodeBlock>
  );
}
