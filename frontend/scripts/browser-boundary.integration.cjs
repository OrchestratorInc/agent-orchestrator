// Run by TestBrowserBoundaryElectron against the real Go router and in-memory
// terminals. This exercises Chromium's actual Origin and fetch metadata, not
// a renderer mock. It does not launch the complete AO desktop application.
const { app, BrowserWindow, protocol } = require("electron");
const path = require("node:path");
const assert = require("node:assert/strict");
// Load production TypeScript in Electron's main process for this transport test.
// This uses the installed compiler and leaves application code unmodified.
const ts = require("typescript");
require.extensions[".ts"] = (module, filename) => module._compile(ts.transpileModule(
 require("node:fs").readFileSync(filename, "utf8"),
 { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } },
).outputText, filename);
const { startRemoteProxy } = require("../src/main/remote-proxy.ts");
const { mediaCrossOrigin } = require("../src/renderer/lib/file-media.ts");
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
 const proxy = await startRemoteProxy({ label: "boundary", url: base, password: "fixture-password" });
 const renderPath = "/api/v1/sessions/boundary/renders/r1";
 for (const endpoint of [base, proxy.base]) {
  const noCors = await win.webContents.executeJavaScript(`new Promise(resolve => {
   const image = new Image(); image.onload = () => resolve(true); image.onerror = () => resolve(false);
   image.src = ${JSON.stringify(endpoint + "/boundary-media/image.png")};
  })`);
  assert.equal(noCors, false, "protected media still requires renderer-origin CORS");
  for (const tag of ["img", "video", "audio"]) {
   const src = `${endpoint}/boundary-media/${tag === "img" ? "image.png" : "sound.wav"}`;
   const loaded = await win.webContents.executeJavaScript(`new Promise(resolve => {
    const media = document.createElement(${JSON.stringify(tag)});
    const timer = setTimeout(() => { media.remove(); resolve(false); }, 5000);
    const finish = ok => { clearTimeout(timer); media.remove(); resolve(ok); };
    media.crossOrigin = ${JSON.stringify(mediaCrossOrigin(src))};
    media.on${tag === "img" ? "load" : "loadedmetadata"} = () => finish(true);
    media.onerror = () => finish(false);
    media.src = ${JSON.stringify(src)};
    document.body.appendChild(media);
   })`);
   assert.equal(loaded, true, `${tag} loads through ${endpoint}`);
  }
  const framed = await win.webContents.executeJavaScript(`new Promise(resolve => {
   const frame = document.createElement('iframe');
   frame.sandbox = 'allow-scripts';
   const finish = value => { clearTimeout(timer); removeEventListener('message', onMessage); frame.remove(); resolve(value); };
   const onMessage = event => { if (event.source === frame.contentWindow && event.data === 'boundary-render-loaded') finish(event.origin); };
   const timer = setTimeout(() => finish('timeout'), 5000);
   addEventListener('message', onMessage);
   frame.src = ${JSON.stringify(endpoint + renderPath)};
   document.body.appendChild(frame);
  })`);
  assert.equal(framed, "null", "render iframe loads in an opaque sandbox");
  const check = new BrowserWindow({ show: false, webPreferences: { sandbox: true, contextIsolation: true, nodeIntegration: false } });
  await check.loadURL(endpoint + renderPath);
  assert.equal(await check.webContents.executeJavaScript(`document.getElementById('render-marker')?.textContent`), "render loaded", "render check navigation loads its document");
  const escaped = await check.webContents.executeJavaScript(`fetch(${JSON.stringify(base + "/api/v1/shell-terminals")}, {method:'POST'}).then(r=>r.status,()=>0)`);
  assert([0,403].includes(escaped), "render sandbox cannot create terminals");
  check.destroy();
 }
 await win.loadURL(`${base}/boundary-fixture/plain`);
 for (const endpoint of [base, proxy.base]) {
  const loaded = await win.webContents.executeJavaScript(`new Promise(resolve => {
   const image = new Image(); image.crossOrigin = 'anonymous';
   image.onload = () => resolve(true); image.onerror = () => resolve(false);
   image.src = ${JSON.stringify(endpoint + "/boundary-media/image.png")};
  })`);
  assert.equal(loaded, false, "foreign origin cannot read protected media");
 }
 await proxy.close();
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
 console.log("Packaged renderer REST/mux, media and render navigations passed; untrusted and sandboxed browser requests refused.");
 result(0);
}).catch(error => { console.error(error); result(1); });
