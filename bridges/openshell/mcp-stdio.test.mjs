import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import {randomBytes} from 'node:crypto';
import {spawn} from 'node:child_process';
import {createEgressProxy} from './proxy.mjs';
import {fileURLToPath} from 'node:url';
import {dirname,join} from 'node:path';

const directory=dirname(fileURLToPath(import.meta.url));
const shim=join(directory,'mcp-stdio.mjs');

test('stdio shim posts exact broker target through authenticated egress proxy', async t => {
  const proxyToken=randomBytes(32).toString('hex');
  const brokerID='selected-tools';
  let requestDetails;
  const proxy=http.createServer((request,response)=>{
    const chunks=[];
    request.on('data',chunk=>chunks.push(chunk));
    request.once('end',()=>{
      requestDetails={method:request.method,path:request.url,authorization:request.headers['proxy-authorization'],brokerID:request.headers['x-whirlwind-broker-id'],body:Buffer.concat(chunks).toString('utf8')};
      response.writeHead(200,{'content-type':'application/json'});
      response.end('{"jsonrpc":"2.0","id":7,"result":{"tools":[]}}');
    });
  });
  await listen(proxy);
  t.after(()=>close(proxy));
  const child=spawn(process.execPath,[shim,brokerID],{stdio:['pipe','pipe','pipe'],env:{PATH:process.env.PATH??'',HTTP_PROXY:`http://whirlwind:${proxyToken}@127.0.0.1:${proxy.address().port}`}});
  const output=collect(child.stdout);
  const errors=collect(child.stderr);
  const message={jsonrpc:'2.0',id:7,method:'tools/list',params:{}};
  child.stdin.end(JSON.stringify(message)+'\n');
  const result=await wait(child);
  assert.equal(result.code,0,`shim failed: ${result.stderr}`);
  assert.deepEqual(JSON.parse(result.stdout.trim()),{jsonrpc:'2.0',id:7,result:{tools:[]}});
  assert.equal(requestDetails.method,'POST');
  assert.equal(requestDetails.path,'http://whirlwind-mcp.openshell.internal/mcp');
  assert.equal(requestDetails.authorization,`Basic ${Buffer.from(`whirlwind:${proxyToken}`).toString('base64')}`);
  assert.equal(requestDetails.brokerID,brokerID);
  assert.deepEqual(JSON.parse(requestDetails.body),message);
  assert.equal(result.stderr,'');
  assert.equal(output(),result.stdout);
  assert.equal(errors(),result.stderr);
});

test('stdio shim and host proxy deliver only the broker capability to its Unix listener', async t => {
  const proxyToken=randomBytes(32).toString('hex');
  const brokerToken=randomBytes(32).toString('hex');
  const brokerID='selected-tools';
  const socketPath=`${process.env.TMPDIR??'/tmp'}/whirlwind-shim-${process.pid}-${randomBytes(6).toString('hex')}.sock`;
  let received;
  const broker=http.createServer((request,response)=>{
    received={method:request.method,path:request.url,brokerToken:request.headers['x-whirlwind-mcp-token'],proxyAuthorization:request.headers['proxy-authorization'],brokerID:request.headers['x-whirlwind-broker-id']};
    request.resume();
    request.once('end',()=>{response.writeHead(200,{'content-type':'application/json'});response.end('{"jsonrpc":"2.0","id":8,"result":{"tools":[{"name":"read_issue"}]}}');});
  });
  await new Promise((resolve,reject)=>{broker.once('error',reject);broker.listen(socketPath,resolve);});
  t.after(async()=>{await close(broker);await import('node:fs/promises').then(fs=>fs.rm(socketPath,{force:true}));});
  const proxy=createEgressProxy({token:proxyToken,mcpBroker:{id:brokerID,socket_path:socketPath,host:'whirlwind-mcp.openshell.internal',path:'/mcp'},mcpBrokerToken:brokerToken});
  await listen(proxy);
  t.after(()=>close(proxy));
  const child=spawn(process.execPath,[shim,brokerID],{stdio:['pipe','pipe','pipe'],env:{PATH:process.env.PATH??'',HTTP_PROXY:`http://whirlwind:${proxyToken}@127.0.0.1:${proxy.address().port}`} });
  const resultPromise=wait(child);
  child.stdin.end(JSON.stringify({jsonrpc:'2.0',id:8,method:'tools/list',params:{}})+'\n');
  const result=await resultPromise;
  assert.equal(result.code,0,`shim failed: ${result.stderr}`);
  assert.deepEqual(JSON.parse(result.stdout.trim()),{jsonrpc:'2.0',id:8,result:{tools:[{name:'read_issue'}]}});
  assert.deepEqual(received,{method:'POST',path:'/mcp',brokerToken,proxyAuthorization:undefined,brokerID:undefined});
});

function collect(stream) {
  let value='';
  stream.setEncoding('utf8');
  stream.on('data',chunk=>{value+=chunk;});
  return ()=>value;
}

function wait(child) {
  return new Promise((resolve,reject)=>{
    child.once('error',reject);
    child.once('close',(code,signal)=>resolve({code,signal,stdout:stdout(),stderr:stderr()}));
    const stdout=collect(child.stdout);
    const stderr=collect(child.stderr);
  });
}

function listen(server) {
  return new Promise((resolve,reject)=>{
    server.once('error',reject);
    server.listen(0,'127.0.0.1',resolve);
  });
}

function close(server) {
  return new Promise(resolve=>server.close(()=>resolve()));
}
