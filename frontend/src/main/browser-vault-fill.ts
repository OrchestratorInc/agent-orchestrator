// Fills vault secrets into a page at the edge. The plaintext goes from the
// vault straight into CDP Input.insertText; callers get back only a mask, and
// the session's redactor scrubs the value from every later result.
import { randomBytes } from "node:crypto";
import { MIN_REDACT_LENGTH, SECRET_MASK, createRedactor, normalizeVaultOrigin, secureCredentialOrigin, type Redactor } from "./browser-vault";
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
  /**
   * The tab and document now shown (`<tab>:<document generation>`). It changes
   * only when the document is replaced, not on an in-page URL change.
   */
  documentKey: () => string;
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

// Runs in an isolated world: page scripts cannot patch what it reads, and the
// token it sets on the element is a property only this world can see.
export const probeFocus = (token: string) => `/*ao-vault:probe*/(() => {
  const el = document.activeElement;
  if (!el || el === document.body) return null;
  el.__aoVaultToken = ${JSON.stringify(token)};
  const r = el.getBoundingClientRect();
  const s = getComputedStyle(el);
  const visible = r.width > 0 && r.height > 0 && s.visibility !== "hidden" && s.display !== "none" && Number(s.opacity) > 0 && !el.disabled && !el.readOnly;
  const type = el.tagName === "INPUT" ? el.type : "";
  return { tag: el.tagName, type, visible, hasFocus: document.hasFocus() };
})()`;

// Right before typing: the element verified by the probe must still hold
// focus in the same document (a navigation creates a new document, which has
// no token). Input.insertText has no target node, so this binds it.
// It also tags the element when a secret (not a username) goes in, so later
// reads, key presses and screenshots can treat it by element, not by string.
export const selectVerified = (token: string, kind: string) => `/*ao-vault:select*/(() => {
  const el = document.activeElement;
  if (!el || el.__aoVaultToken !== ${JSON.stringify(token)}) return false;
  if (typeof el.select === "function") el.select();
  if (${JSON.stringify(kind)}) el.__aoVaultFilled = ${JSON.stringify(kind)};
  return true;
})()`;

/** True when focus is in a field that holds a filled secret. */
export const filledFieldFocused = `/*ao-vault:filled-focus*/(() => {
  const el = document.activeElement;
  return Boolean(el && el.__aoVaultFilled);
})()`;

/**
 * The current values of fields holding a filled secret, whatever they hold
 * now (an agent may have edited them). Read only to mask them.
 */
export const filledFieldValues = `/*ao-vault:filled-values*/(() =>
  [...document.querySelectorAll("input, textarea")]
    .filter((el) => el.__aoVaultFilled && el.isConnected)
    .map((el) => el.value)
)()`;

/**
 * True when a field Orchestrator filled is on the page, visible and not masked:
 * a code in a text field, or a password the page (or an eye toggle) turned
 * into type=text. `ignore` lists kinds that no longer matter (expired codes).
 */
export const revealedSecretsCheck = (ignore: string[]) => `/*ao-vault:revealed ${ignore.map((kind) => `ignore:${kind}`).join(" ")}*/(() => {
  const ignore = ${JSON.stringify(ignore)};
  for (const el of document.querySelectorAll("input, textarea")) {
    const kind = el.__aoVaultFilled;
    if (!kind || ignore.includes(kind) || !el.isConnected) continue;
    if (el.tagName === "INPUT" && el.type === "password") continue;
    const r = el.getBoundingClientRect();
    const s = getComputedStyle(el);
    if (r.width > 0 && r.height > 0 && s.visibility !== "hidden" && s.display !== "none") return true;
  }
  return false;
})()`;

// Right after typing: the verified element must still hold focus. If a page
// script moved focus mid-insert, clear the field that took the text.
// It also keeps the field's value out of its "value" attribute: frameworks
// such as React mirror a controlled input's value there, and a CSS selector
// like input[value^="a"] would then reveal the secret a character at a time.
export const afterInsert = (token: string) => `/*ao-vault:after-insert*/(() => {
  const el = document.activeElement;
  if (el && el.__aoVaultToken === ${JSON.stringify(token)}) {
    el.removeAttribute("value");
    if (!el.__aoVaultValueGuard) {
      el.__aoVaultValueGuard = new MutationObserver(() => {
        if (el.hasAttribute("value")) el.removeAttribute("value");
      });
      el.__aoVaultValueGuard.observe(el, { attributes: true, attributeFilter: ["value"] });
    }
    return true;
  }
  if (el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA")) el.value = "";
  return false;
})()`;

const secureTransport = secureCredentialOrigin;

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

// A TOTP code is valid for its 30 s step plus the clock skew sites accept.
const CODE_ON_SCREEN_MS = 90_000;

// The frames of one document that received a password or code.
type FilledDocument = { frames: Set<string>; codeUntil?: number; hasPassword: boolean };

export function createCredentialFiller(options: { store: VaultStore; now?: () => number }) {
  const now = options.now ?? Date.now;
  const redactors = new Map<string, Redactor>();
  // Text redaction cannot reach pixels. For each document a secret was typed
  // into, agent screenshots first check that no filled field is visible
  // unmasked. Keyed by session, then by `<tab>:<document>`.
  const filledDocuments = new Map<string, Map<string, FilledDocument>>();
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
    })) as { result?: { value?: T }; exceptionDetails?: { text?: string } };
    // A script that threw has no answer; callers fail closed on this.
    if (response.exceptionDetails) throw new Error(`In-page check failed: ${response.exceptionDetails.text ?? "exception"}`);
    return response.result?.value as T;
  };

  /** The one frame whose document holds focus on a form control. */
  const focusedFrame = async (cdp: CdpLike, token: string): Promise<{ frame: Frame; probe: NonNullable<FocusProbe> }> => {
    const hits: { frame: Frame; probe: NonNullable<FocusProbe> }[] = [];
    const all = await frames(cdp);
    for (const [index, frame] of all.entries()) {
      let probe: FocusProbe;
      try {
        probe = await evaluate<FocusProbe>(cdp, frame.id, probeFocus(token));
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
    const token = randomBytes(16).toString("hex");
    const { frame, probe } = await focusedFrame(target.cdp, token);
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
    if (field !== "username" && (!secureTransport(origin) || !secureTransport(originOf(frame.url)))) {
      throw new FillRefused(`Saved secrets are only filled over HTTPS (or on this computer); ${origin} is not`, "insecure transport");
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
    if (field !== "username" && secret.length < MIN_REDACT_LENGTH) {
      throw new FillRefused("This secret is too short to mask in later output, so it is not filled", "secret too short");
    }
    ensureLive();
    if (originOf(target.pageURL()) !== origin) {
      throw new FillRefused("Page origin changed before the fill", "origin changed");
    }
    if (field !== "username") redactorFor(target.sessionId).add(secret);
    if (!(await evaluate<boolean>(target.cdp, frame.id, selectVerified(token, field === "username" ? "" : field)))) {
      throw new FillRefused("The verified field changed before typing; nothing was typed", "field changed");
    }
    ensureLive();
    await target.cdp.sendCommand("Input.insertText", { text: secret });
    if (!(await evaluate<boolean>(target.cdp, frame.id, afterInsert(token)))) {
      throw new FillRefused("Focus moved while the secret went in; that field was cleared", "focus moved");
    }
    if (field !== "username") {
      const documents = filledDocuments.get(target.sessionId) ?? new Map<string, FilledDocument>();
      const key = target.documentKey();
      const filled = documents.get(key) ?? { frames: new Set<string>(), hasPassword: false };
      filled.frames.add(frame.id);
      if (field === "password") filled.hasPassword = true;
      else filled.codeUntil = now() + CODE_ON_SCREEN_MS;
      documents.set(key, filled);
      filledDocuments.set(target.sessionId, documents);
    }
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

  /**
   * The fields `login` will fill on this page, checked before the prompt and
   * before anything is typed: a code field needs a 2FA key, and every secret
   * must be fillable (HTTPS or this computer, long enough to mask). So a
   * login never stops half way through a form.
   */
  const planLogin = async (target: FillTarget, item: VaultItemSummary) => {
    const topFrame = (await frames(target.cdp))[0];
    if (!item.origins.includes(originOf(topFrame.url)) || !item.origins.includes(originOf(target.pageURL()))) {
      throw new FillRefused("Page origin does not match this credential", "origin mismatch");
    }
    const roles = await evaluate<Partial<Record<VaultField, number>>>(target.cdp, topFrame.id, FIND);
    const found = (["username", "password", "totp"] as const).filter((role) => roles?.[role] !== undefined);
    if (!found.length) throw new FillRefused("No sign-in fields found on this page", "no fields");
    const planned = found.filter((role) => role !== "totp" || item.hasTotp);
    if (!planned.length) {
      throw new FillRefused(`"${item.label}" has no 2FA key for the code field on this page`, "no 2FA key");
    }
    const origin = originOf(target.pageURL());
    for (const role of planned) {
      if (role === "username") continue;
      if (!secureTransport(origin)) {
        throw new FillRefused(`Saved secrets are only filled over HTTPS (or on this computer); ${origin} is not`, "insecure transport");
      }
      if (role === "password") {
        const secret = await options.store.resolveSecret({ itemId: item.id, field: role, pageURL: target.pageURL(), projectId: target.projectId });
        if (secret.length < MIN_REDACT_LENGTH) {
          throw new FillRefused("This secret is too short to mask in later output, so it is not filled", "secret too short");
        }
      }
    }
    return { topFrame, roles: roles!, planned };
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
      // Refuse before the prompt when `login` could fill nothing here.
      if (request.action === "login") await planLogin(target, item);
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
        const { topFrame, roles, planned } = await planLogin(target, item);
        const filled: VaultField[] = [];
        for (const role of planned) {
          if (!(await evaluate<boolean>(target.cdp, topFrame.id, focusField(roles[role]!)))) {
            throw new FillRefused(`Could not focus the ${role} field`, "focus failed");
          }
          await fillFocused(target, item, role);
          filled.push(role);
        }
        return { filled, itemId: item.id, origin: originOf(target.pageURL()), value: SECRET_MASK };
      });
    },

    /**
     * Why an agent screenshot of this document must wait, if it must: a filled
     * password or unexpired code is visible unmasked. If the check itself
     * fails, the screenshot waits too.
     */
    async screenshotBlocked(sessionId: string, documentKey: string, cdp: CdpLike): Promise<string | undefined> {
      const documents = filledDocuments.get(sessionId);
      const filled = documents?.get(documentKey);
      if (!documents || !filled) return undefined;
      const codeExpired = filled.codeUntil === undefined || now() >= filled.codeUntil;
      if (codeExpired && !filled.hasPassword) {
        documents.delete(documentKey);
        if (!documents.size) filledDocuments.delete(sessionId);
        return undefined;
      }
      const check = revealedSecretsCheck(codeExpired ? ["totp"] : []);
      for (const frameId of filled.frames) {
        let revealed: boolean;
        try {
          revealed = await evaluate<boolean>(cdp, frameId, check);
        } catch (error) {
          // A frame that is gone took its fields with it.
          if (/no frame/i.test(error instanceof Error ? error.message : "")) continue;
          return "Orchestrator could not check whether a filled password is visible, so screenshots are paused.";
        }
        if (revealed) {
          return "A password or 2FA code Orchestrator filled is visible on screen, so screenshots are paused. Mask or submit it and try again.";
        }
      }
      return undefined;
    },

    /**
     * Why an agent key press must not reach this document, if it must not:
     * focus is in a field holding a filled secret, where Backspace or Delete
     * would turn it into a value the redactor no longer recognises.
     */
    async keyPressBlocked(sessionId: string, documentKey: string, cdp: CdpLike): Promise<string | undefined> {
      const filled = filledDocuments.get(sessionId)?.get(documentKey);
      if (!filled) return undefined;
      for (const frameId of filled.frames) {
        let focused: boolean;
        try {
          focused = await evaluate<boolean>(cdp, frameId, filledFieldFocused);
        } catch (error) {
          if (/no frame/i.test(error instanceof Error ? error.message : "")) continue;
          return "Orchestrator could not check whether focus is in a filled password field, so the key was not sent.";
        }
        if (focused) {
          return "Keys cannot be sent to a field Orchestrator filled with a saved secret. Focus another field, or submit the form.";
        }
      }
      return undefined;
    },

    /**
     * Masks a result by element as well as by string: the current values of
     * this document's filled fields are masked whatever they now hold. Values
     * too short to mask inside other text are masked when a whole string
     * equals them.
     */
    async scrubLive<T>(sessionId: string, documentKey: string, cdp: CdpLike, value: T): Promise<T> {
      const filled = filledDocuments.get(sessionId)?.get(documentKey);
      const short = new Set<string>();
      if (filled) {
        for (const frameId of filled.frames) {
          let current: string[];
          try {
            current = await evaluate<string[]>(cdp, frameId, filledFieldValues);
          } catch {
            // Known secrets are still masked below.
            continue;
          }
          for (const text of Array.isArray(current) ? current : []) {
            if (typeof text !== "string" || !text) continue;
            if (text.length >= MIN_REDACT_LENGTH) redactorFor(sessionId).add(text);
            else short.add(text);
          }
        }
      }
      const scrubbed = redactors.get(sessionId)?.scrub(value) ?? value;
      if (!short.size) return scrubbed;
      const walk = (item: unknown): unknown => {
        if (typeof item === "string") return short.has(item) ? SECRET_MASK : item;
        if (Array.isArray(item)) return item.map(walk);
        if (item && typeof item === "object") {
          return Object.fromEntries(Object.entries(item).map(([key, entry]) => [key, walk(entry)]));
        }
        return item;
      };
      return walk(scrubbed) as T;
    },

    /**
     * Why an agent selector must not run against this document, if it must
     * not: while a field holds a filled secret, a selector or raw ref that
     * tests a value (input[value^="a"], :has(...[value$="9"])) would turn
     * match / no match into the secret one character at a time.
     */
    selectorProbeBlocked(sessionId: string, documentKey: string, args: Record<string, unknown>): string | undefined {
      if (!filledDocuments.get(sessionId)?.has(documentKey)) return undefined;
      for (const key of ["selector", "ref", "targetRef", "target"]) {
        const text = args[key];
        if (typeof text !== "string" || /^@?e\d+$/.test(text.trim())) continue;
        if (/value/i.test(text)) {
          return "Selectors that test a value are refused while this page holds a password Orchestrator filled. Use a snapshot ref instead.";
        }
      }
      return undefined;
    },

    scrub<T>(sessionId: string, value: T): T {
      return redactors.get(sessionId)?.scrub(value) ?? value;
    },

    forget(sessionId: string) {
      redactors.delete(sessionId);
      filledDocuments.delete(sessionId);
    },
  };
}
