import type { I18n, MessageDescriptor } from "@lingui/core";
import { msg } from "@lingui/core/macro";

// Strings rendered by the docs framework's own components, keyed the way its
// translation provider looks them up: the English text, then its notes.
const FRAMEWORK_MESSAGES: Record<string, MessageDescriptor> = {
  "Close Search(search dialog)(aria-label)": msg({ message: "Close search", context: "search dialog" }),
  "Close Sidebar(aria-label)": msg({ message: "Close sidebar", context: "sidebar" }),
  "Close Sidebar(sidebar)(aria-label)": msg({ message: "Close sidebar", context: "sidebar" }),
  "Collapse Sidebar(sidebar)(aria-label)": msg({ message: "Collapse sidebar", context: "sidebar" }),
  "Copied Text(code block)(aria-label)": msg({ message: "Copied", context: "code block" }),
  "Copy Text(code block)(aria-label)": msg({ message: "Copy code", context: "code block" }),
  "Copy Anchor Link(heading anchor)(aria-label)": msg({ message: "Copy link to this section", context: "heading anchor" }),
  "Hide Sidebar(sidebar)": msg({ message: "Hide sidebar", context: "sidebar" }),
  "Show Sidebar(sidebar)": msg({ message: "Show sidebar", context: "sidebar" }),
  "Open Sidebar(sidebar)(aria-label)": msg({ message: "Open sidebar", context: "sidebar" }),
  "Next Page(pagination)": msg({ message: "Next", context: "pagination" }),
  "Previous Page(pagination)": msg({ message: "Previous", context: "pagination" }),
  "No Headings(table of contents)": msg({ message: "No headings", context: "table of contents" }),
  "On this page(table of contents)": msg({ message: "On this page", context: "table of contents" }),
  "Table of Contents(inline table of contents)": msg({ message: "Table of contents", context: "table of contents" }),
  "No results found(search dialog)": msg({ message: "No results found", context: "search dialog" }),
  "Search(search dialog)": msg({ message: "Search", context: "search" }),
  "Search(search trigger)": msg({ message: "Search", context: "search" }),
  "Open Search(search trigger)(aria-label)": msg({ message: "Open search", context: "search" }),
  "Toggle Menu(mobile menu)(aria-label)": msg({ message: "Toggle menu", context: "mobile menu" }),
};

export function frameworkTranslations(i18n: I18n): Record<string, string> {
  const translations: Record<string, string> = {};
  for (const [key, descriptor] of Object.entries(FRAMEWORK_MESSAGES)) {
    translations[key] = i18n._(descriptor);
  }
  return translations;
}

// Sidebar section and folder titles from the content's meta.json files. Page
// titles come from each article and stay in the article's language.
const NAV_LABELS: Record<string, MessageDescriptor> = {
  "Getting Started": msg({ message: "Getting Started", context: "sidebar section" }),
  "Using Orchestrator": msg({ message: "Using Orchestrator", context: "sidebar section" }),
  "Built-in capabilities": msg({ message: "Built-in capabilities", context: "sidebar section" }),
  Reference: msg({ message: "Reference", context: "sidebar section" }),
  Help: msg({ message: "Help", context: "sidebar section" }),
  Configuration: msg({ message: "Configuration", context: "sidebar section" }),
  Guides: msg({ message: "Guides", context: "sidebar section" }),
  Agents: msg({ message: "Agents", context: "sidebar section" }),
  Notifications: msg({ message: "Notifications", context: "sidebar section" }),
  Runtimes: msg({ message: "Runtimes", context: "sidebar section" }),
  Terminals: msg({ message: "Terminals", context: "sidebar section" }),
  Trackers: msg({ message: "Trackers", context: "sidebar section" }),
  Workspaces: msg({ message: "Workspaces", context: "sidebar section" }),
};

/** Translates a sidebar label when it is one we know. Others pass through. */
export function navLabel(i18n: I18n, label: string): string {
  const descriptor = NAV_LABELS[label];
  return descriptor ? i18n._(descriptor) : label;
}
