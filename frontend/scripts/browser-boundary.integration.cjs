// Run by TestBrowserBoundaryElectron against the real Go router and in-memory
// terminals. This exercises Chromium's actual Origin and fetch metadata, not
// a renderer mock. It does not launch the complete AO desktop application.
const { app, BrowserWindow, protocol } = require("electron");
const path = require("node:path");
const assert = require("node:assert/strict");
// Chromium flags remain in process.argv, so the script/URL positions differ
// between the local invocation and Linux CI. Never resolve a flag as an app URL.
const base = process.env.AO_BOUNDARY_URL;
assert.equal(new URL(base).protocol, "http:");
assert.equal(new URL(base).hostname, "127.0.0.1");
app.setPath("userData", path.join(process.env.AO_BOUNDARY_DATA, "electron"));
app.setPath("sessionData", path.join(process.env.AO_BOUNDARY_DATA, "session"));
protocol.registerSchemesAsPrivileged([{ scheme: "app", privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true } }]);
const result = (code) => { console.log(`AO_BOUNDARY_RESULT:${code}`); app.exit(code); };
app.whenReady().then(async () => {
 protocol.handle("app", () => new Response("<title>boundary fixture</title>"));
 const win = new BrowserWindow({ show: false, webPreferences: { sandbox: true, contextIsolation: true, nodeIntegration: false } });
 await win.loadURL("app://renderer/index.html");
 const created = await win.webContents.executeJavaScript(`fetch(${JSON.stringify(base + "/api/v1/shell-terminals")}, { method: "POST" }).then(r => r.status)`);
 assert.equal(created, 201, "packaged renderer can create a shell");
 const echo = await win.webContents.executeJavaScript(`new Promise((resolve,reject)=>{
  const ws=new WebSocket(${JSON.stringify(base.replace(/^http/, "ws") + "/mux")});
  const timer=setTimeout(()=>{ws.close();reject(new Error('mux timeout'))},3000);
  ws.onopen=()=>ws.send(JSON.stringify({ch:'terminal',type:'open',id:'shellterm-boundary'}));
  ws.onmessage=({data})=>{const f=JSON.parse(data);if(f.type==='opened')ws.send(JSON.stringify({ch:'terminal',type:'data',id:'shellterm-boundary',data:btoa('electron echo')}));if(f.type==='data'){clearTimeout(timer);ws.close();resolve(atob(f.data));}};
  ws.onerror=()=>{clearTimeout(timer);reject(new Error('mux refused'));};
 })`);
 assert.equal(echo, "electron echo");
 for (const fixture of ["plain", "sandbox"]) {
  await win.loadURL(`${base}/boundary-fixture/${fixture}`);
  const attempts = await win.webContents.executeJavaScript(`Promise.all([
   fetch(${JSON.stringify(base + "/api/v1/shell-terminals")},{method:'POST',headers:{'content-type':'text/plain'}}).then(r=>r.status,()=>0),
   fetch(${JSON.stringify(base + "/api/v1/shell-terminals")}).then(r=>r.status,()=>0),
   new Promise(resolve=>{const ws=new WebSocket(${JSON.stringify(base.replace(/^http/, "ws") + "/mux")});ws.onopen=()=>{ws.close();resolve(101)};ws.onerror=()=>resolve(0);})
  ])`);
  assert(attempts.every(status => status === 0 || status === 403), `${fixture} browser cannot reach shell or mux: ${attempts}`);
 }
 win.destroy();
 console.log("Packaged renderer REST/mux echo passed; untrusted and sandboxed browser requests refused.");
 result(0);
}).catch(error => { console.error(error); result(1); });
