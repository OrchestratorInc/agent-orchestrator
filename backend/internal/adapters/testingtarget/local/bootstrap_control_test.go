package local

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestControllerBootstrapCallsOnlyExistingDaemonStart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	script := `
const assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs');
const {EventEmitter}=require('node:events');
const app=new EventEmitter();app.isPackaged=false;
let watcher,request,writes=[],calls=0,closed=false;
const ipc={handle(name,handler){}};let pinned=0,visible=false;const ownedWindow=new EventEmitter();
Object.assign(ownedWindow,{isDestroyed(){return false},isVisible(){return visible},isVisibleOnAllWorkspaces(){return pinned>0},setVisibleOnAllWorkspaces(allSpaces,options){assert.equal(visible,true);assert.equal(allSpaces,true);assert.equal(options.visibleOnFullScreen,true);pinned++}});
const source=fs.readFileSync('../../../skillassets/testing/scripts/bootstrap.cjs','utf8');
vm.runInNewContext(source,{
 process:{env:{AO_TESTING_CONTROL_DIR:'/private/attempt/control',AO_TESTING_STICKY_WINDOWS:'1'}},console,
 require(name){
  if(name==='electron')return {app,ipcMain:ipc,BaseWindow:{getAllWindows(){return [ownedWindow]}}};
  if(name==='node:fs')return {
   watch(dir,fn){assert.equal(dir,'/private/attempt/control');watcher=fn;return {close(){closed=true}}},
   readFileSync(){return JSON.stringify(request)},writeFileSync(path,data,options){writes.push({path,data,options})}
  };
  if(name==='./ao-main.cjs'){ipc.handle('daemon:start',async()=>{calls++});ipc.handle('other',()=>{throw Error('unrelated handler invoked')});return {}};
  return require(name);
 }
});
const unrelated=new EventEmitter();unrelated.getURL=()=> 'https://example.com';app.emit('web-contents-created',{},unrelated);unrelated.emit('did-finish-load');assert.equal(ownedWindow.listenerCount('show'),0);
const contents=new EventEmitter();contents.getURL=()=> 'app://renderer/index.html';app.emit('web-contents-created',{},contents);contents.emit('did-finish-load');assert.equal(pinned,0);assert.equal(ownedWindow.listenerCount('show'),1);
visible=true;ownedWindow.emit('show');assert.equal(pinned,1);
contents.emit('did-finish-load');assert.equal(ownedWindow.listenerCount('show'),1);assert.equal(pinned,1);
ownedWindow.emit('show');assert.equal(pinned,2);
(async()=>{
 request={id:'a'.repeat(32),action:'other'};await watcher('rename','request.json');assert.equal(calls,0);assert.equal(writes.length,0);
 request={id:'../bad',action:'restart_daemon'};await watcher('rename','request.json');assert.equal(calls,0);
 request={id:'a'.repeat(32),action:'restart_daemon'};await watcher('rename','request.json');assert.equal(calls,1);
 assert.equal(writes.length,1);assert.equal(writes[0].path,'/private/attempt/control/'+request.id+'.json');assert.equal(writes[0].options.flag,'wx');
 assert.equal(JSON.parse(writes[0].data).ok,true);app.emit('will-quit');assert.equal(closed,true);
})().catch(error=>{console.error(error);process.exitCode=1});`
	cmd := exec.CommandContext(ctx, "node", "-e", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bootstrap control: %v: %s", err, out)
	}
}
