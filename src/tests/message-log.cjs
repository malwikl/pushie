'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const source=fs.readFileSync(require('node:path').join(__dirname,'../static/app.js'),'utf8');
function fixture(){
 const context=vm.createContext({language:'en',locale:()=> 'en-GB',t:x=>x,deliveryLabels:{accepted:'accepted',confirmed:'confirmed',retry:'retry'}});
 vm.runInContext(source.slice(source.indexOf('function messageLogEntries('),source.indexOf('async function retryPushReceipts(')),context);
 return {context};
}
test('message list no longer offers the protocol toggle or diagnostic details',()=>{
 const html=fs.readFileSync(require('node:path').join(__dirname,'../static/index.html'),'utf8');
 assert.ok(!html.includes('messageLogToggle'));
 assert.ok(!source.includes('renderMessageLog('));
});
test('log orders recorded events and includes per-device retry and error details',()=>{
 const {context}=fixture();
 const lines=context.messageLogEntries({id:'message-1',createdAt:'2026-11-09T10:00:00Z',readAt:'2026-11-09T10:05:00Z',deliveries:[
  {deviceName:'Phone',status:'confirmed',attempts:1,acceptedAt:'2026-11-09T10:01:00Z',confirmedAt:'2026-11-09T10:02:00Z'},
  {deviceName:'Tablet',status:'retry',attempts:2,nextAttemptAt:'2026-11-09T10:06:00Z',lastError:'HTTP 503 <provider>'}
 ]});
 assert.equal(lines[0],'Message ID: message-1');assert.match(lines[1],/Message queued/);assert.match(lines[2],/Phone: accepted/);assert.match(lines[3],/Phone: confirmed/);assert.match(lines[4],/Marked as read/);
 assert.ok(lines.includes('Tablet: retry · 2 attempts'));assert.ok(lines.some(x=>x.startsWith('Next attempt:')));assert.ok(lines.includes('Last error: HTTP 503 <provider>'));
});
test('messages without delivery jobs explain why there is no push log',()=>{
 const {context}=fixture();const lines=context.messageLogEntries({id:'one',createdAt:'2026-11-09T10:00:00Z'});
 assert.equal(lines.at(-1),'Keine Push-Geräte zum Versandzeitpunkt');
});
