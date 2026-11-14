"use client";

import { useLingui } from "@lingui/react";
import { CodeBlock, type CodeBlockProps, Pre } from "fumadocs-ui/components/codeblock";

export function LocalizedCodeBlock({ children, ...props }: CodeBlockProps) {
  const { i18n } = useLingui();
  return (
    <CodeBlock {...props} lang={i18n.locale}>
      <Pre lang="en">{children}</Pre>
    </CodeBlock>
  );
}
