// The one place credential fills ask the user. The shared risky-action
// approval surface can replace `createDialogApprover` without touching the
// filler: anything with this signature works.
import type { BaseWindow, MessageBoxOptions } from "electron";

export type CredentialApprovalRequest = {
  kind: "credential-fill";
  sessionId: string;
  origin: string;
  itemLabel: string;
  username: string;
};

export type CredentialApprovalDecision = "allow-always" | "allow-once" | "deny";

export type CredentialApprover = (request: CredentialApprovalRequest) => Promise<CredentialApprovalDecision>;

type ShowMessageBox = (
  window: BaseWindow | undefined,
  options: MessageBoxOptions,
) => Promise<{ response: number }>;

const BUTTONS: { label: string; decision: CredentialApprovalDecision }[] = [
  { label: "Always allow on this site", decision: "allow-always" },
  { label: "Allow once", decision: "allow-once" },
  { label: "Don't allow", decision: "deny" },
];

export function createDialogApprover(
  showMessageBox: ShowMessageBox,
  getWindow: () => BaseWindow | undefined,
): CredentialApprover {
  return async (request) => {
    const { response } = await showMessageBox(getWindow(), {
      type: "question",
      title: "Sign in with a saved password?",
      message: `Let ${request.sessionId} sign in to ${request.origin}?`,
      detail:
        `It will use "${request.itemLabel}" (${request.username || "no username"}). ` +
        "Orchestrator types the password into the page itself; the agent never sees it.",
      buttons: BUTTONS.map((button) => button.label),
      defaultId: 2,
      cancelId: 2,
      noLink: true,
    });
    return BUTTONS[response]?.decision ?? "deny";
  };
}
