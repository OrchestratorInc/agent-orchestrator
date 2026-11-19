// Real Chromium credential/control requests against the Go router and real proxy.
const { app, BrowserWindow, protocol } = require("electron");
const path = require("node:path");
const assert = require("node:assert/strict");
const ts = require("typescript");
require.extensions[".ts"] = (module, filename) => module._compile(ts.transpileModule(
 require("node:fs").readFileSync(filename, "utf8"),
 { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } },
).outputText, filename);
const { startRemoteProxy } = require("../src/main/remote-proxy.ts");
const base = process.env.AO_MOBILE_CONTROL_URL;
assert.equal(new URL(base).hostname, "127.0.0.1");
app.setPath("userData", path.join(process.env.AO_MOBILE_CONTROL_DATA, "electron"));
app.setPath("sessionData", path.join(process.env.AO_MOBILE_CONTROL_DATA, "session"));
protocol.registerSchemesAsPrivileged([{ scheme: "app", privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true } }]);
app.whenReady().then(async () => {
 protocol.handle("app", () => new Response("<p>trusted renderer</p>"));
 const proxy = await startRemoteProxy({ label: "mobile boundary", url: base, password: "fake-password" });
 const win = new BrowserWindow({ show: false, webPreferences: { sandbox: true, contextIsolation: true, nodeIntegration: false } });
 await win.loadURL("app://renderer/index.html");
 const first = await win.webContents.executeJavaScript(`fetch(${JSON.stringify(base + "/api/v1/mobile/enable")},{method:'POST'}).then(r=>r.json())`);
 assert(first.enabled && first.password, "trusted renderer receives pairing password");
 await win.loadURL(base + "/mobile-hostile-fixture");
 for (const endpoint of [base, proxy.base]) {
  for (const action of ["enable", "regenerate", "status"]) {
   const attempt = await win.webContents.executeJavaScript(`fetch(${JSON.stringify(endpoint + "/api/v1/mobile/" + action)},{method:${JSON.stringify(action === "status" ? "GET" : "POST")},headers:{'content-type':'text/plain'}}).then(async r=>({status:r.status,body:await r.text()}),()=>({status:0,body:''}))`);
   assert([0,403].includes(attempt.status), `hostile ${action} refused`);
   assert(!attempt.body.includes(first.password), "password never reaches hostile browser content");
  }
 }
 await win.loadURL("app://renderer/index.html");
 for (const endpoint of [base, proxy.base]) {
  const status = await win.webContents.executeJavaScript(`fetch(${JSON.stringify(endpoint + "/api/v1/mobile/status")}).then(r=>r.json())`);
  assert.equal(status.password, first.password, "rejected requests neither enabled anew nor rotated the password");
 }
 win.destroy(); await proxy.close();
 console.log("AO_MOBILE_CONTROL_RESULT:0"); app.exit(0);
}).catch(error => { console.error(error); app.exit(1); });
