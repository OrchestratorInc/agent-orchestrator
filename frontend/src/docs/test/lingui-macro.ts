import type { MessageDescriptor } from "@lingui/core";

/** Test stand-in for `msg`: returns the English source as the descriptor. */
export function msg(
  input: TemplateStringsArray | { message: string; context?: string },
  ...values: unknown[]
): MessageDescriptor {
  if (!Array.isArray(input)) {
    const { message } = input as { message: string };
    return { id: message, message };
  }
  const strings = input as TemplateStringsArray;
  const message = strings.reduce((text, part, i) => text + part + (i < values.length ? String(values[i]) : ""), "");
  return { id: message, message };
}
