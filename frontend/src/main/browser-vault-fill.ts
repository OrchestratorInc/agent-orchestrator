// Fills vault secrets into a page at the edge. The plaintext goes from the
// vault straight into CDP Input.insertText; callers get back only a mask, and
// the session's redactor scrubs the value from every later result.
import { SECRET_MASK, createRedactor, normalizeVaultOrigin, type Redactor } from "./browser-vault";
import type { VaultField, VaultItemSummary, VaultStore } from "./browser-vault-store";

export type CdpLike = {
  sendCommand(method: string, params?: Record<string, unknown>): Promise<unknown>;
};

export type FillTarget = {
  cdp: CdpLike;
  sessionId: string;
  /** Trusted project id stamped by the daemon, never by the agent. */
  projectId: string;
  /** The top-level page URL, read live. */
  pageURL: () => string;
  pageTitle?: () => string;
  /** The command's cancellation; a late approval must not type anything. */
  signal?: AbortSignal;
  /**
   * The origin the user approved in the daemon's approval gate. Fills refuse
   * without it, and refuse any page that is not on it.
   */
  approvedOrigin?: string;
  /** `ao browser login <origin>`: the page must still be on this origin. */
  requestedOrigin?: string;
};

type Frame = { id: string; url: string };
type FocusProbe = { tag: string; type: string; visible?: boolean; hasFocus?: boolean } | null;

const WORLD = "ao-vault";

// Runs in an isolated world: page scripts cannot patch what it reads.
const PROBE = `/*ao-vault:probe*/(() => {
  const el = document.activeElement;
  if (!el || el === document.body) return null;
  const r = el.getBoundingClientRect();
  const s = getComputedStyle(el);
  const visible = r.width > 0 && r.height > 0 && s.visibility !== "hidden" && s.display !== "none" && Number(s.opacity) > 0 && !el.disabled && !el.readOnly;
  return { tag: el.tagName, type: (el.getAttribute("type") || "").toLowerCase(), visible, hasFocus: document.hasFocus() };
})()`;

const SELECT = `/*ao-vault:select*/(() => {
  const el = document.activeElement;
  if (el && typeof el.select === "function") el.select();
  return true;
})()`;

export const FIND = `/*ao-vault:find*/(() => {
  const visible = (el) => {
    const r = el.getBoundingClientRect();
    const s = getComputedStyle(el);
    return r.width > 0 && r.height > 0 && s.visibility !== "hidden" && s.display !== "none" && !el.disabled && !el.readOnly;
  };
  const inputs = [...document.querySelectorAll("input")].filter(visible);
  const roles = {};
  const otp = /one.?time|otp|totp|2fa|mfa|verification|authenticator|auth.?code|security.?code|^code$/i;
  // Each attribute on its own, so a field named just "code" matches ^code$.
  const otpHint = (el) => [el.name, el.id, el.getAttribute("aria-label"), el.placeholder].some((v) => v && otp.test(v.trim()));
  const totp = inputs.findIndex((el) => el.autocomplete === "one-time-code" || (el.type !== "password" && otpHint(el)));
  const password = inputs.findIndex((el) => el.type === "password" && el.autocomplete !== "new-password");
  if (password >= 0) {
    roles.password = password;
    const form = inputs[password].form;
    const textual = ["text", "email", "tel", ""];
    let user = inputs.findIndex((el) => el.autocomplete === "username" && (!form || el.form === form));
    if (user < 0) {
      for (let i = password - 1; i >= 0; i--) {
        if (textual.includes(inputs[i].type) && (!form || inputs[i].form === form)) { user = i; break; }
      }
    }
    if (user >= 0) roles.username = user;
  } else if (totp >= 0) {
    roles.totp = totp;
  }
  window.__aoVaultFields = inputs;
  return roles;
})()`;

const focusField = (index: number) => `/*ao-vault:focus:${index}*/(() => {
  const el = (window.__aoVaultFields || [])[${index}];
  if (!el) return false;
  el.focus();
  return document.activeElement === el;
})()`;

const TEXTUAL_TYPES = new Set(["", "text", "email", "tel", "number", "search"]);

class FillRefused extends Error {
  constructor(
    message: string,
    readonly reason: string,
  ) {
    super(message);
  }
}

export type CredentialFiller = ReturnType<typeof createCredentialFiller>;

export function createCredentialFiller(options: { store: VaultStore }) {
  const redactors = new Map<string, Redactor>();
  const redactorFor = (sessionId: string) => {
    let redactor = redactors.get(sessionId);
    if (!redactor) redactors.set(sessionId, (redactor = createRedactor()));
    return redactor;
  };

  const originOf = (url: string) => {
    try {
      return new URL(url).origin;
    } catch {
      return "null";
    }
  };

  const itemFor = async (itemId: string): Promise<VaultItemSummary> => {
    const item = (await options.store.list()).find((candidate) => candidate.id === itemId);
    if (!item) throw new FillRefused("Credential not found", "unknown item");
    return item;
  };

  const frames = async (cdp: CdpLike): Promise<Frame[]> => {
    type Node = { frame: Frame; childFrames?: Node[] };
    const { frameTree } = (await cdp.sendCommand("Page.getFrameTree")) as { frameTree: Node };
    const out: Frame[] = [];
    const walk = (node: Node) => {
      out.push({ id: node.frame.id, url: node.frame.url });
      node.childFrames?.forEach(walk);
    };
    walk(frameTree);
    return out;
  };

  const evaluate = async <T>(cdp: CdpLike, frameId: string, expression: string): Promise<T> => {
    const { executionContextId } = (await cdp.sendCommand("Page.createIsolatedWorld", {
      frameId,
      worldName: WORLD,
    })) as { executionContextId: number };
    const response = (await cdp.sendCommand("Runtime.evaluate", {
      expression,
      contextId: executionContextId,
      returnByValue: true,
    })) as { result?: { value?: T } };
    return response.result?.value as T;
  };

  /** The one frame whose document holds focus on a form control. */
  const focusedFrame = async (cdp: CdpLike): Promise<{ frame: Frame; probe: NonNullable<FocusProbe> }> => {
    const hits: { frame: Frame; probe: NonNullable<FocusProbe> }[] = [];
    const all = await frames(cdp);
    for (const [index, frame] of all.entries()) {
      let probe: FocusProbe;
      try {
        probe = await evaluate<FocusProbe>(cdp, frame.id, PROBE);
      } catch {
        // Out-of-process frames cannot be checked from here; never fill into them.
        continue;
      }
      if (!probe || probe.tag === "IFRAME" || probe.tag === "FRAME") continue;
      // The top page's focused element is where CDP types, even while the
      // app window is in the background (document.hasFocus() is then false).
      // A child frame keeps a stale activeElement after focus leaves it, so it
      // counts only while it really holds document focus.
      if (index > 0 && probe.hasFocus === false) continue;
      hits.push({ frame, probe });
    }
    if (hits.length !== 1 || !["INPUT", "TEXTAREA"].includes(hits[0].probe.tag)) {
      throw new FillRefused("No focused text field that Orchestrator can verify", "no verifiable focused field");
    }
    return hits[0];
  };

  /** Fills one field into whatever field currently holds focus. */
  const fillFocused = async (target: FillTarget, item: VaultItemSummary, field: VaultField) => {
    const topURL = target.pageURL();
    if (!item.origins.includes(originOf(topURL))) {
      throw new FillRefused("Page origin does not match this credential", "origin mismatch");
    }
    const { frame, probe } = await focusedFrame(target.cdp);
    if (!item.origins.includes(originOf(frame.url))) {
      throw new FillRefused("The focused field's frame origin does not match this credential", "frame origin mismatch");
    }
    if (field === "password" && probe.type !== "password") {
      throw new FillRefused("Passwords are only typed into password fields", "not a password field");
    }
    if (field !== "password" && !TEXTUAL_TYPES.has(probe.type) && !(field === "totp" && probe.type === "password")) {
      throw new FillRefused(`The focused field cannot take a ${field}`, "wrong field type");
    }
    if (probe.visible === false) {
      throw new FillRefused("The focused field is hidden or not editable", "field not visible");
    }
    const origin = originOf(topURL);
    if (!target.approvedOrigin) {
      throw new FillRefused("This fill was not approved in Orchestrator", "not approved");
    }
    if (origin !== target.approvedOrigin) {
      throw new FillRefused(`The page is on ${origin}, not the approved ${target.approvedOrigin}`, "origin changed");
    }
    const ensureLive = () => {
      if (target.signal?.aborted) throw new FillRefused("The command was cancelled before the fill", "cancelled");
    };
    ensureLive();
    // The page may have navigated while the user was deciding.
    if (originOf(target.pageURL()) !== origin) {
      throw new FillRefused("Page origin changed before the fill", "origin changed");
    }
    const secret = await options.store.resolveSecret({
      itemId: item.id,
      field,
      pageURL: frame.url,
      projectId: target.projectId,
    });
    if (field !== "username") redactorFor(target.sessionId).add(secret);
    await evaluate(target.cdp, frame.id, SELECT);
    ensureLive();
    await target.cdp.sendCommand("Input.insertText", { text: secret });
    return origin;
  };

  const audited = async <T>(
    target: FillTarget,
    itemId: string,
    field: VaultField | "login",
    run: () => Promise<T>,
  ): Promise<T> => {
    try {
      const result = await run();
      await options.store.audit({ origin: originOf(target.pageURL()), sessionId: target.sessionId, itemId, field, outcome: "filled" });
      return result;
    } catch (error) {
      const refused = error instanceof FillRefused;
      const message = error instanceof Error ? error.message : String(error);
      await options.store
        .audit({
          origin: originOf(target.pageURL()),
          sessionId: target.sessionId,
          itemId,
          field,
          outcome: refused || /project|origin/i.test(message) ? "denied" : "error",
          reason: refused ? (error as FillRefused).reason : message,
        })
        .catch(() => undefined);
      throw error;
    }
  };

  const checkProject = (item: VaultItemSummary, projectId: string) => {
    if (item.allowedProjects.length && !item.allowedProjects.includes(projectId)) {
      throw new FillRefused("This project may not use this credential", "project not allowed");
    }
  };

  /** Refuses when `login <origin>` was asked for and the page is elsewhere. */
  const checkRequestedOrigin = (target: FillTarget) => {
    if (!target.requestedOrigin) return;
    let requested: string;
    try {
      requested = normalizeVaultOrigin(target.requestedOrigin);
    } catch {
      throw new FillRefused(`${target.requestedOrigin} is not a site`, "invalid origin");
    }
    const page = originOf(target.pageURL());
    if (page !== requested) {
      throw new FillRefused(`The page is on ${page}, not ${requested}; it may have redirected`, "origin mismatch");
    }
  };

  return {
    /**
     * What a fill would use, for the approval prompt: the page origin and the
     * item's label and username. Never a secret.
     */
    async describe(target: FillTarget, request: { action: string; itemId?: string }) {
      checkRequestedOrigin(target);
      const pageURL = target.pageURL();
      const origin = originOf(pageURL);
      const base = { origin, pageUrl: pageURL, pageTitle: target.pageTitle?.() ?? "" };
      let item: VaultItemSummary;
      if (request.itemId) {
        item = await itemFor(request.itemId);
        checkProject(item, target.projectId);
        if (!item.origins.includes(origin)) {
          throw new FillRefused(`"${item.label}" is not saved for ${origin}`, "origin mismatch");
        }
      } else {
        const candidates = await options.store.candidates(pageURL, target.projectId);
        if (!candidates.length) throw new FillRefused(`No saved credential for ${origin}`, "no candidate");
        if (candidates.length > 1) {
          return {
            ...base,
            candidates: candidates.map((c) => ({ id: c.id, label: c.label, username: c.username, hasTotp: c.hasTotp })),
          };
        }
        item = candidates[0];
      }
      return { ...base, item: { id: item.id, label: item.label, username: item.username } };
    },

    async fillSecret(target: FillTarget, request: { itemId: string; field: VaultField }) {
      return audited(target, request.itemId, request.field, async () => {
        const item = await itemFor(request.itemId);
        checkProject(item, target.projectId);
        const origin = await fillFocused(target, item, request.field);
        return { filled: true, field: request.field, itemId: item.id, origin, value: SECRET_MASK };
      });
    },

    async login(target: FillTarget, request: { itemId?: string }) {
      let itemId = request.itemId;
      if (!itemId) {
        const candidates = await options.store.candidates(target.pageURL(), target.projectId);
        if (!candidates.length) {
          throw new FillRefused(`No saved credential for ${originOf(target.pageURL())}`, "no candidate");
        }
        if (candidates.length > 1) {
          return {
            filled: [],
            origin: originOf(target.pageURL()),
            candidates: candidates.map((c) => ({ id: c.id, label: c.label, username: c.username, hasTotp: c.hasTotp })),
          };
        }
        itemId = candidates[0].id;
      }
      const chosen = itemId;
      return audited(target, chosen, "login", async () => {
        checkRequestedOrigin(target);
        const item = await itemFor(chosen);
        checkProject(item, target.projectId);
        const topFrame = (await frames(target.cdp))[0];
        if (!item.origins.includes(originOf(topFrame.url)) || !item.origins.includes(originOf(target.pageURL()))) {
          throw new FillRefused("Page origin does not match this credential", "origin mismatch");
        }
        const roles = await evaluate<Partial<Record<VaultField, number>>>(target.cdp, topFrame.id, FIND);
        const order = (["username", "password", "totp"] as const).filter((role) => roles?.[role] !== undefined);
        if (!order.length) throw new FillRefused("No sign-in fields found on this page", "no fields");
        const filled: VaultField[] = [];
        for (const role of order) {
          if (role === "totp" && !item.hasTotp) continue;
          if (!(await evaluate<boolean>(target.cdp, topFrame.id, focusField(roles[role]!)))) {
            throw new FillRefused(`Could not focus the ${role} field`, "focus failed");
          }
          await fillFocused(target, item, role);
          filled.push(role);
        }
        return { filled, itemId: item.id, origin: originOf(target.pageURL()), value: SECRET_MASK };
      });
    },

    scrub<T>(sessionId: string, value: T): T {
      return redactors.get(sessionId)?.scrub(value) ?? value;
    },

    forget(sessionId: string) {
      redactors.delete(sessionId);
    },
  };
}
