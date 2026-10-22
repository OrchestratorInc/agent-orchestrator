#!/usr/bin/env node
// Step 4 of the harness workflow: drive the REAL AO desktop app the way a
// user does and save a screenshot at every checkpoint, pass or fail.
//
// Start the dev app with a debugging port (see SKILL.md step 4), then:
//   node scripts/ui_audit.mjs --harness codex --out ~/Desktop/agents/ao-audit-reports/run-ui
//
// Checkpoints: harness listed → install → account/models → auth → update (v0:
// skipped) → New Task agent → New Task models → spawn and run (TUI and/or
// Chat). Writes <out>/NN-<checkpoint>-<STATUS>.png, ui-report.json, ui-report.md.
//
// Selectors are in SELECTORS below; update them there when the UI changes.

import { mkdir, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { homedir } from "node:os";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

// Harness page layout (current main): a list of harnesses; each opens a
// detail view with Account, Models and Health tabs.
export const SELECTORS = Object.freeze({
  settingsButton: { role: "button", name: "Settings" },
  settingsDialog: '[data-testid="settings-page"]',
  settingsClose: { role: "button", name: "Close settings" },
  navSection: (name) => ({ role: "button", name }),
  harnessSection: '[data-testid="settings-section"][data-section="harness"]',
  harnessSearch: { label: "Search harnesses" },
  harnessListItem: (id) => `button[data-agent="${id}"]`,
  harnessDetail: (id) => `[data-harness-detail] div[data-agent="${id}"]`,
  harnessName: (id) => `#harness-agent-${id}`,
  tab: (name) => ({ role: "tab", name }),
  primaryAction: "[data-harness-primary-action]",
  progress: '[role="status"]',
  loginButton: { role: "button", name: /^(Login|Log in|Log in again|Set up|Refresh login|Retry login)$/ },
  updateButton: { role: "button", name: /^Update$/ },
  authTerminal: '[data-testid="harness-auth-terminal"]',
  modelList: '[aria-label="Available models"]',
  modelRow: '[aria-label="Available models"] li',
  modelsMessage: /reported no models|to see its models|to load its models|Could not load models/,
  modelsSummary: /\d+ of \d+ shown in model pickers/,
  checkAgain: { role: "button", name: "Check again" },
  standaloneStart: { role: "button", name: /Start a standalone agent|New agent/ },
  agentPicker: { label: "Agent" },
  modelPicker: { label: "Model" },
  menuItem: "[role=menuitem]",
  taskField: { label: "Task" },
  submit: 'button[type="submit"]',
  sessionDetail: '[data-testid="session-detail"]',
  terminal: '[data-testid="session-terminal"]',
  chat: 'section[data-session-mode="chat"]',
  chatMessages: "[data-chat-message-role=assistant]",
  interfaceSetting: { label: /Default session interface/ },
});

// Status words the Account and Health tabs show (frontend i18n en.json).
const STATUS_TEXT = {
  connected: /\bConnected\b/,
  unverified: /Credentials not verified|\bConfigured\b/,
  signedOut: /Signed out|Not set up|Login required|Not connected/,
  notInstalled: /Not installed|Installation status unknown/,
};

function loadPlaywright(repo) {
  const require = createRequire(join(resolve(repo), "frontend", "package.json"));
  for (const name of ["playwright-core", "playwright", "@playwright/test"]) {
    try { return require(name); } catch { /* try the next package */ }
  }
  throw new Error(`Playwright is not installed in ${resolve(repo)}/frontend. Run \`npm ci\` in that frontend once.`);
}

const locate = (scope, spec) => (typeof spec === "string" ? scope.locator(spec)
  : spec.role ? scope.getByRole(spec.role, { name: spec.name })
  : scope.getByLabel(spec.label));

async function visible(locator, timeout = 5_000) {
  try { await locator.first().waitFor({ state: "visible", timeout }); return true; } catch { return false; }
}

// First-run dialogs that cover the app (e.g. "Import projects from your
// earlier AO?"). Only a defer-style answer is clicked, never one that decides
// for the user (Proceed, Don't Migrate); anything else is left for a human.
const DEFER_BUTTON = /^(Skip|Not now|Later|Maybe later|Dismiss)$/;

export async function dismissBlockingDialogs(page, context) {
  for (let attempt = 0; attempt < 3; attempt += 1) {
    // Any open dialog that offers a defer answer, wherever it sits in the stack.
    const dialog = page.locator('[role="dialog"], [role="alertdialog"]')
      .filter({ has: page.getByRole("button", { name: DEFER_BUTTON }) }).last();
    if (!(await visible(dialog, 300))) return;
    const title = ((await dialog.getByRole("heading").first().textContent().catch(() => "")) ?? "").trim();
    const defer = dialog.getByRole("button", { name: DEFER_BUTTON }).first();
    const label = ((await defer.textContent()) ?? "").trim();
    await defer.click();
    context.dismissed.push(`${title || "dialog"} → ${label}`);
    console.error(`dismissed first-run dialog "${title}" with ${label}`);
    await page.waitForTimeout(500);
  }
}

// A run that was stopped can leave New Task or Settings open; close them so
// every audit starts from the same screen.
async function resetToHome(page, context) {
  await dismissBlockingDialogs(page, context);
  for (let attempt = 0; attempt < 3; attempt += 1) {
    if (!(await visible(page.locator('[role="dialog"]'), 300))) break;
    await page.keyboard.press("Escape");
    await page.waitForTimeout(300);
  }
  await page.evaluate(() => { window.location.hash = "#/"; });
  await page.waitForTimeout(800);
  await dismissBlockingDialogs(page, context);
}

// One checkpoint: run it, then always screenshot, whatever happened.
async function checkpoint(context, id, title, run) {
  await dismissBlockingDialogs(context.page, context);
  const index = String(context.results.length + 1).padStart(2, "0");
  let outcome;
  try { outcome = await run(); } catch (error) { outcome = { status: "FAIL", reason: `error: ${error.message.split("\n")[0]}` }; }
  const screenshot = join(context.out, `${index}-${id}-${outcome.status}.png`);
  try { await context.page.screenshot({ path: screenshot, fullPage: false }); } catch (error) { outcome.screenshotError = error.message; }
  const result = { id, title, ...outcome, screenshot: outcome.screenshotError ? null : screenshot };
  context.results.push(result);
  console.error(`${result.status.padEnd(7)} ${id}: ${result.reason}`);
  return result;
}

// The search box filters by display name ("Claude Code"), not by harness ID
// ("claude-code"), so the row is found by its data-agent ID with an empty search.
// true once the terminal shows `pattern`, false on timeout, null when the
// terminal text is not in the DOM at all.
async function waitForTerminalText(page, pattern, timeoutMs) {
  const rows = page.locator(".xterm-rows");
  if (!(await visible(rows, 5_000))) return null;
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (pattern.test((await rows.last().innerText().catch(() => "")) ?? "")) return true;
    await page.waitForTimeout(2_000);
  }
  return false;
}

async function openHarnessSettings(page, harness, label = "") {
  if (!(await visible(page.locator(SELECTORS.settingsDialog), 500))) await locate(page, SELECTORS.settingsButton).first().click();
  await page.locator(SELECTORS.settingsDialog).waitFor({ timeout: 10_000 });
  await locate(page, SELECTORS.navSection("Harness")).first().click();
  const section = page.locator(SELECTORS.harnessSection);
  await section.waitFor({ timeout: 10_000 });
  const search = locate(section, SELECTORS.harnessSearch);
  if (await visible(search, 1_000)) await search.fill(label);
  return section;
}

async function closeSettings(page) {
  if (await visible(page.locator(SELECTORS.settingsDialog), 500)) {
    await locate(page, SELECTORS.settingsClose).first().click().catch(() => page.keyboard.press("Escape"));
  }
}

// innerText keeps the line breaks between elements; textContent would glue
// "Account" + "Connected" into one word and hide every status.
async function textOf(locator) {
  return ((await locator.first().innerText().catch(() => "")) ?? "").replace(/\s+/g, " ").trim();
}

async function openTab(detail, name) {
  await locate(detail, SELECTORS.tab(name)).first().click();
  await detail.page().waitForTimeout(500);
}

async function setInterface(page, label) {
  await locate(page, SELECTORS.settingsButton).first().click();
  await page.locator(SELECTORS.settingsDialog).waitFor({ timeout: 10_000 });
  await locate(page, SELECTORS.navSection("General")).first().click();
  const control = locate(page, SELECTORS.interfaceSetting).first();
  await control.click();
  const option = page.getByRole("menuitem", { name: label }).or(page.getByRole("option", { name: label })).or(page.getByRole("radio", { name: label }));
  await option.first().click();
  await closeSettings(page);
}

export async function uiAudit({ cdp, repo, harness, label = "", model = "", out, interfaces = ["tui", "chat"], install = false, login = false, spawn = true, prompt }) {
  const { chromium } = loadPlaywright(repo);
  await mkdir(out, { recursive: true });
  let browser;
  try { browser = await chromium.connectOverCDP(cdp); } catch (error) {
    throw new Error(`cannot reach the AO app at ${cdp} (${error.message.split("\n")[0]}). Start it with a debugging port; see SKILL.md step 4.`);
  }
  const page = browser.contexts().flatMap((context) => context.pages()).find((candidate) => !candidate.url().startsWith("devtools://"));
  if (!page) throw new Error("connected, but the AO window has no page");
  await page.setViewportSize?.({ width: 1440, height: 900 }).catch(() => {});
  const context = { page, out, results: [], dismissed: [] };
  await resetToHome(page, context);
  let detail;
  let displayName = label;

  await checkpoint(context, "harness-listed", "Agent Harness page lists the harness", async () => {
    const section = await openHarnessSettings(page, harness, label);
    const item = section.locator(SELECTORS.harnessListItem(harness));
    await item.first().scrollIntoViewIfNeeded().catch(() => {});
    if (!(await visible(item))) return { status: "FAIL", reason: `no harness "${harness}" on the Harness page: not registered, or hidden on this host` };
    await item.first().click();
    detail = page.locator(SELECTORS.harnessDetail(harness));
    if (!(await visible(detail))) return { status: "FAIL", reason: "listed, but its detail view did not open" };
    displayName ||= (await textOf(page.locator(SELECTORS.harnessName(harness)))) || harness;
    return { status: "PASS", reason: `listed as "${displayName}"; detail view opened` };
  });
  if (!detail || !(await visible(detail, 500))) return finish(browser, context, { harness, displayName });

  await checkpoint(context, "install", "Install from the Account tab", async () => {
    await openTab(detail, "Account");
    const text = await textOf(detail);
    const installButton = detail.locator(SELECTORS.primaryAction).filter({ hasText: /^(Install|Retry install)$/ });
    if (!(await visible(installButton, 1_000))) {
      return STATUS_TEXT.notInstalled.test(text)
        ? { status: "FAIL", reason: "not installed and no Install button (manual-only installer?)" }
        : { status: "PASS", reason: "already installed; the Install action was not exercised" };
    }
    if (!install) return { status: "NOT_RUN", reason: "Install is offered; pass --install to click it (installs the CLI on this machine)" };
    await installButton.first().click();
    await detail.locator(SELECTORS.progress).first().waitFor({ state: "detached", timeout: 600_000 }).catch(() => {});
    const after = await textOf(detail);
    return /Installation failed|Retry install/.test(after) ? { status: "FAIL", reason: "install failed (see diagnostics in the screenshot)" } : { status: "PASS", reason: "installed from the Account tab" };
  });

  await checkpoint(context, "health", "Health tab shows status, authentication and executable", async () => {
    await openTab(detail, "Health");
    const text = await textOf(detail);
    const parts = ["Authentication", "Executable", "Last checked"].filter((label) => text.includes(label));
    return parts.length >= 2 ? { status: "PASS", reason: `health shows ${parts.join(", ")}` } : { status: "FAIL", reason: `health tab is missing ${["Authentication", "Executable", "Last checked"].filter((label) => !parts.includes(label)).join(", ")}` };
  });

  await checkpoint(context, "models", "Models tab lists the harness models", async () => {
    await openTab(detail, "Models");
    const rows = detail.locator(SELECTORS.modelRow);
    if (await visible(rows, 15_000)) {
      const summary = (await textOf(detail)).match(SELECTORS.modelsSummary)?.[0];
      return { status: "PASS", reason: `${await rows.count()} model(s) on this page${summary ? `; ${summary}` : ""}` };
    }
    const message = (await textOf(detail)).match(SELECTORS.modelsMessage)?.[0];
    return { status: /to load its models/.test(message ?? "") ? "NEEDS_USER" : "FAIL", reason: message ? `Models tab says: "${message}"` : "Models tab shows no models" };
  });

  await checkpoint(context, "login", "Login or configure is offered (or already connected)", async () => {
    await openTab(detail, "Account");
    const text = await textOf(detail);
    const loginButton = locate(detail, SELECTORS.loginButton);
    const offered = await visible(loginButton, 1_000);
    if (STATUS_TEXT.connected.test(text) && !STATUS_TEXT.signedOut.test(text)) return { status: "PASS", reason: `connected${offered ? "; Refresh login is offered" : ""}` };
    if (!offered) return { status: "FAIL", reason: "not connected and no Login/Set up button" };
    if (!login) return { status: "NEEDS_USER", reason: "Login/Set up is offered; log in (or pass --login to open it), then rerun" };
    await loginButton.first().click();
    const opened = await visible(page.locator(SELECTORS.authTerminal), 15_000);
    return { status: "NEEDS_USER", reason: opened ? "login panel opened; finish the login, then rerun" : "clicked Login but no login panel appeared" };
  });

  await checkpoint(context, "auth-check", "Auth check: Health says Connected", async () => {
    await openTab(detail, "Health");
    const again = locate(detail, SELECTORS.checkAgain);
    if (await visible(again, 1_000)) { await again.first().click(); await page.waitForTimeout(5_000); }
    const text = await textOf(detail);
    if (STATUS_TEXT.connected.test(text) && !STATUS_TEXT.unverified.test(text)) return { status: "PASS", reason: "authentication verified: Connected" };
    if (STATUS_TEXT.unverified.test(text)) return { status: "BLOCKED", reason: "credentials found but not verified" };
    return { status: "NEEDS_USER", reason: "not authenticated: log in or provide the API key, then rerun" };
  });

  await checkpoint(context, "update", "Update the harness", async () => {
    await openTab(detail, "Account");
    const offered = await visible(locate(detail, SELECTORS.updateButton), 1_000);
    return { status: "NOT_RUN", reason: `skipped in v0${offered ? " (an update is offered)" : ""}` };
  });
  await closeSettings(page);

  for (const mode of interfaces) {
    if (interfaces.length > 1) {
      await setInterface(page, mode === "chat" ? "Chat" : "Terminal").catch((error) => console.error(`could not set the default interface to ${mode}: ${error.message.split("\n")[0]}`));
    }
    let dialog;
    await checkpoint(context, `new-task-agent-${mode}`, `New Task lists the agent (${mode})`, async () => {
      // Route by hash, not page.goto: a reload re-opens first-run dialogs
      // that were already deferred for this page.
      await page.evaluate(() => { window.location.hash = "#/"; });
      await page.waitForTimeout(800);
      await dismissBlockingDialogs(page, context);
      await locate(page, SELECTORS.standaloneStart).first().click();
      dialog = page.getByRole("dialog");
      await dialog.waitFor({ timeout: 10_000 });
      await locate(dialog, SELECTORS.agentPicker).first().click();
      const option = page.getByRole("menuitem", { name: new RegExp(`^${displayName.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}`) });
      if (!(await visible(option))) return { status: "FAIL", reason: `"${displayName}" is not in the New Task agent list` };
      await option.first().click();
      return { status: "PASS", reason: `"${displayName}" selectable in New Task` };
    });
    await checkpoint(context, `new-task-models-${mode}`, `New Task lists the harness models (${mode})`, async () => {
      if (!dialog) return { status: "NOT_RUN", reason: "New Task did not open" };
      const picker = locate(dialog, SELECTORS.modelPicker).first();
      if (!(await visible(picker))) return { status: "FAIL", reason: "no Model picker for this agent" };
      await picker.click();
      const items = page.locator(SELECTORS.menuItem);
      await visible(items, 15_000);
      const names = (await items.allTextContents()).map((item) => item.trim()).filter(Boolean);
      if (!names.length) return { status: "FAIL", reason: "Model picker opened with no models" };
      const choice = model ? items.filter({ hasText: model }) : items;
      await choice.first().click();
      return { status: "PASS", reason: `${names.length} model option(s), e.g. ${names.slice(0, 3).join(", ")}` };
    });
    if (!spawn) continue;
    await checkpoint(context, `running-${mode}`, `Spawned agent runs in ${mode === "chat" ? "Chat" : "the terminal (TUI)"}`, async () => {
      if (!dialog) return { status: "NOT_RUN", reason: "New Task did not open" };
      await locate(dialog, SELECTORS.taskField).first().fill(prompt);
      await dialog.locator(SELECTORS.submit).first().click();
      await page.locator(SELECTORS.sessionDetail).first().waitFor({ timeout: 60_000 });
      // Some CLIs (Claude Code) take well over 30 s to paint their first frame.
      const surface = page.locator(mode === "chat" ? SELECTORS.chat : `${SELECTORS.terminal}, .xterm`);
      if (!(await visible(surface, 90_000))) return { status: "FAIL", reason: `session opened without a ${mode} surface within 90 s` };
      if (mode === "chat") {
        const answered = await visible(page.locator(SELECTORS.chatMessages), 120_000);
        return answered ? { status: "PASS", reason: "Chat session answered" } : { status: "FAIL", reason: "Chat session opened but no assistant reply within 2 minutes" };
      }
      // The DOM renderer exposes the screen text; the WebGL renderer does not,
      // so a missing reply there leaves the screenshot as the evidence.
      const replied = await waitForTerminalText(page, /\bREADY\b/, 90_000);
      return replied === true ? { status: "PASS", reason: "terminal session answered READY" }
        : replied === null ? { status: "PASS", reason: "terminal session is running; its text is not readable from the page (WebGL renderer), see the screenshot" }
        : { status: "FAIL", reason: "terminal session opened but the agent did not answer within 90 s" };
    });
  }
  return finish(browser, context, { harness, displayName });
}

async function finish(browser, context, meta) {
  const report = { schemaVersion: 1, ...meta, finishedAt: new Date().toISOString(), dismissedDialogs: context.dismissed, checkpoints: context.results };
  const lines = [`# AO UI audit: ${meta.displayName || meta.harness}`, "", "| # | Checkpoint | Result | Reason | Screenshot |", "|---|---|---|---|---|"];
  context.results.forEach((item, index) => lines.push(`| ${index + 1} | ${item.title} | ${item.status} | ${String(item.reason).replaceAll("|", "\\|")} | ${item.screenshot ? `![](${item.screenshot.split("/").pop()})` : "none"} |`));
  await writeFile(join(context.out, "ui-report.json"), `${JSON.stringify(report, null, 2)}\n`);
  if (context.dismissed.length) lines.push("", `Dismissed first-run dialogs (deferred, nothing chosen): ${context.dismissed.join("; ")}`);
  await writeFile(join(context.out, "ui-report.md"), `${lines.join("\n")}\n`);
  await browser.close().catch(() => {});
  return report;
}

async function main(argv = process.argv.slice(2)) {
  const options = {
    cdp: "http://127.0.0.1:9222", repo: process.env.AO_REPO || join(homedir(), "Desktop", "agent-orchestrator"), harness: "", label: "", model: "",
    out: "", interfaces: ["tui", "chat"], install: false, login: false, spawn: true,
    prompt: "AO UI audit: reply with the single word READY, then stop.",
  };
  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    const next = () => { if (argv[index + 1] === undefined) throw new Error(`missing value for ${argument}`); return argv[++index]; };
    if (argument === "--install") options.install = true;
    else if (argument === "--login") options.login = true;
    else if (argument === "--no-spawn") options.spawn = false;
    else if (argument === "--interfaces") options.interfaces = next().split(",").filter((item) => ["tui", "chat"].includes(item));
    else if (["--cdp", "--repo", "--harness", "--label", "--model", "--out", "--prompt"].includes(argument)) options[argument.slice(2)] = next();
    else if (argument === "-h" || argument === "--help") { console.log("Usage: node scripts/ui_audit.mjs --harness ID [--out DIR] [--cdp URL] [--repo AO_CHECKOUT] [--label NAME] [--model ID] [--interfaces tui,chat] [--install] [--login] [--no-spawn]"); return 0; }
    else throw new Error(`unknown option ${argument}`);
  }
  if (!options.harness) throw new Error("--harness is required");
  options.out = resolve(options.out || join(homedir(), "Desktop", "agents", "ao-audit-reports", `${new Date().toISOString().replace(/[:.]/g, "-")}-ui-${options.harness}`));
  const report = await uiAudit(options);
  console.log(`UI report: ${join(options.out, "ui-report.md")}`);
  return report.checkpoints.every((item) => ["PASS", "NOT_RUN"].includes(item.status)) ? 0 : 1;
}

const invokedAsScript = process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href;
if (invokedAsScript) {
  try { process.exitCode = await main(); } catch (error) { console.error(`error: ${error.message}`); process.exitCode = 2; }
}
