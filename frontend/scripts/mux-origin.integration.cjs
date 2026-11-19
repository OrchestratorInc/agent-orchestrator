// Real Chromium transport against the Go router and the production remote proxy.
const { app, BrowserWindow, protocol } = require("electron");
const path = require("node:path");
const assert = require("node:assert/strict");
const ts = require("typescript");
require.extensions[".ts"] = (module, filename) => module._compile(ts.transpileModule(
 require("node:fs").readFileSync(filename, "utf8"),
 { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } },
).outputText, filename);
const { startRemoteProxy } = require("../src/main/remote-proxy.ts");
const base = process.env.AO_MUX_URL;
assert.equal(new URL(base).hostname, "127.0.0.1");
app.setPath("userData", path.join(process.env.AO_MUX_DATA, "electron"));
app.setPath("sessionData", path.join(process.env.AO_MUX_DATA, "session"));
protocol.registerSchemesAsPrivileged([{ scheme: "app", privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true } }]);
app.whenReady().then(async () => {
 protocol.handle("app", () => new Response("<p>trusted renderer</p>"));
 const proxy = await startRemoteProxy({ label: "mux regression", url: base, password: "fake-password" });
 const win = new BrowserWindow({ show: false, webPreferences: { sandbox: true, contextIsolation: true, nodeIntegration: false } });
 await win.loadURL("app://renderer/index.html");
 for (const endpoint of [base, proxy.base]) {
  const echo = await win.webContents.executeJavaScript(`new Promise((resolve,reject)=>{
   const ws=new WebSocket(${JSON.stringify("ws" + endpoint.slice(4) + "/mux")});
   const timer=setTimeout(()=>{ws.close();reject(new Error('mux timeout'))},3000);
   ws.onopen=()=>ws.send(JSON.stringify({ch:'terminal',type:'open',id:'existing-agent-handle'}));
   ws.onmessage=({data})=>{const f=JSON.parse(data);if(f.type==='opened')ws.send(JSON.stringify({ch:'terminal',type:'data',id:'existing-agent-handle',data:btoa('safe echo')}));if(f.type==='data'){clearTimeout(timer);ws.close();resolve(atob(f.data));}};
   ws.onerror=()=>{clearTimeout(timer);reject(new Error('mux refused'));};
  })`);
  assert.equal(echo, "safe echo");
 }
 await win.loadURL(base + "/mux-hostile-fixture");
 for (const endpoint of [base, proxy.base]) {
  const opened = await win.webContents.executeJavaScript(`new Promise(resolve=>{
   const ws=new WebSocket(${JSON.stringify("ws" + endpoint.slice(4) + "/mux")});
   ws.onopen=()=>{ws.close();resolve(true)}; ws.onerror=()=>resolve(false);
  })`);
  assert.equal(opened, false, "untrusted local content cannot attach, including through a known proxy token");
 }
 win.destroy(); await proxy.close();
 console.log("AO_MUX_RESULT:0"); app.exit(0);
}).catch(error => { console.error(error); app.exit(1); });
