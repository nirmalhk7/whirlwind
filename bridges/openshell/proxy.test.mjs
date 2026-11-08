import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import {proxyInternals} from './proxy.mjs';
import {createEgressProxy} from './proxy.mjs';

test('proxy accepts public DNS results only and rejects local or reserved destinations', async () => {
  const lookup = async host => host === 'public.example'
    ? [{address:'93.184.216.34',family:4}]
    : [{address:'127.0.0.1',family:4}];
  assert.deepEqual(await proxyInternals.resolvePublic('public.example',lookup),[{address:'93.184.216.34',family:4}]);
  await assert.rejects(proxyInternals.resolvePublic('rebound.example',lookup),/denied/i);
});

test('proxy rejects IPv6 documentation and transition ranges while allowing global unicast', () => {
  for (const address of ['2001:db8::1','2001:0::1','2002:0808:0808::1','fc00::1','fe80::1','::ffff:127.0.0.1']) {
    assert.equal(proxyInternals.privateAddress(address),true,`${address} must not be a public egress destination`);
  }
  assert.equal(proxyInternals.privateAddress('2001:4860:4860::8888'),false);
});

test('proxy permits private addresses only for explicitly named integration hosts', async () => {
  const lookup = async () => [{address:'10.20.30.40',family:4}];
  assert.deepEqual(await proxyInternals.resolvePublic('git.internal',lookup,['git.internal']),[{address:'10.20.30.40',family:4}]);
  await assert.rejects(proxyInternals.resolvePublic('other.internal',lookup,['git.internal']),/denied/i);
  await assert.rejects(proxyInternals.resolvePublic('metadata.internal',async () => [{address:'169.254.169.254',family:4}],['metadata.internal']),/denied/i);
});

test('proxy destination parser permits only well-formed host and port authorities', () => {
  assert.deepEqual(proxyInternals.authority('example.com:443'),{host:'example.com',port:443});
  assert.equal(proxyInternals.authority('example.com:22'),null);
  assert.equal(proxyInternals.authority('user@example.com:443'),null);
});

test('broker authorization requires its token and rejects SSH before dialing', () => {
  const token='test-proxy-token-0123456789abcdef';
  const accepted={headers:{'proxy-authorization':`Basic ${Buffer.from(`whirlwind:${token}`).toString('base64')}`}};
  const denied={headers:{}};
  assert.deepEqual(proxyInternals.authorizedTarget(accepted,token,'example.com:443'),{host:'example.com',port:443});
  assert.equal(proxyInternals.authorizedTarget(accepted,token,'example.com:22'),null);
  assert.equal(proxyInternals.authorizedTarget(denied,token,'example.com:443'),null);
});

test('forwarded destination headers exclude proxy credentials and proxy-only connection hints', () => {
  const headers=proxyInternals.forwardedHeaders({
    host:'public.example',
    'proxy-authorization':'Basic private-capability',
    'proxy-connection':'keep-alive',
    connection:'keep-alive, x-hop',
    'x-hop':'private-hop-value',
    accept:'application/json',
  },'public.example');
  assert.deepEqual(headers,{host:'public.example',accept:'application/json'});
});

test('HTTP broker forwards to a controlled destination without proxy credentials', async t => {
  let destinationHeaders;
  const destination=http.createServer((request,response)=>{
    destinationHeaders=request.headers;
    response.writeHead(204);
    response.end();
  });
  await listen(destination);
  t.after(()=>close(destination));
  const destinationPort=destination.address().port;
  const token='controlled-proxy-token-0123456789abcdef';
  const proxy=createEgressProxy({
    token,
    lookup:async host=>host.includes(':')
      ? [{address:'2606:4700:4700::1111',family:6}]
      : [{address:'93.184.216.34',family:4}],
    request:options=>http.request({...options,host:'127.0.0.1',port:destinationPort}),
  });
  await listen(proxy);
  t.after(()=>close(proxy));
  const result=await new Promise((resolve,reject)=>{
    const request=http.request({
      host:'127.0.0.1',port:proxy.address().port,method:'GET',path:'http://public.example/resource',
      headers:{'Proxy-Authorization':`Basic ${Buffer.from(`whirlwind:${token}`).toString('base64')}`,'Proxy-Connection':'keep-alive'},
    },response=>{response.resume();response.once('end',()=>resolve(response.statusCode));});
    request.once('error',reject);
    request.end();
  });
  assert.equal(result,204);
  assert.equal(destinationHeaders['proxy-authorization'],undefined);
  assert.equal(destinationHeaders['proxy-connection'],undefined);
  const ipv6Result=await new Promise((resolve,reject)=>{
    const request=http.request({
      host:'127.0.0.1',port:proxy.address().port,method:'GET',path:'http://[2606:4700:4700::1111]/ipv6',
      headers:{'Proxy-Authorization':`Basic ${Buffer.from(`whirlwind:${token}`).toString('base64')}`},
    },response=>{response.resume();response.once('end',()=>resolve(response.statusCode));});
    request.once('error',reject);
    request.end();
  });
  assert.equal(ipv6Result,204);
});

test('CONNECT broker rejects missing auth and port 22 before opening a socket', async t => {
  let connects=0;
  const token='controlled-connect-token-0123456789abcdef';
  const proxy=createEgressProxy({token,connect:()=>{connects++;throw new Error('must not dial');}});
  await listen(proxy);
  t.after(()=>close(proxy));
  for (const testCase of [
    {authority:'public.example:443',headers:{},label:'missing proxy auth'},
    {authority:'public.example:22',headers:{'Proxy-Authorization':`Basic ${Buffer.from(`whirlwind:${token}`).toString('base64')}`},label:'SSH port'},
  ]) {
    const response=await new Promise((resolve,reject)=>{
      const request=http.request({host:'127.0.0.1',port:proxy.address().port,method:'CONNECT',path:testCase.authority,headers:testCase.headers});
      request.once('connect',(incoming,socket)=>{socket.destroy();resolve(incoming.statusCode);});
      request.once('error',reject);
      request.end();
    });
    assert.equal(response,403,testCase.label);
  }
  assert.equal(connects,0);
});

function listen(server) {
  return new Promise((resolve,reject)=>{
    server.once('error',reject);
    server.listen(0,'127.0.0.1',resolve);
  });
}

function close(server) {
  return new Promise(resolve=>server.close(()=>resolve()));
}

test('reserved MCP relay injects its private capability only on the declared broker path', async t => {
  const {randomBytes}=await import('node:crypto');
  const token=randomBytes(32).toString('hex');
  const brokerToken=randomBytes(32).toString('hex');
  const socketPath=`${process.env.TMPDIR??'/tmp'}/whirlwind-mcp-${process.pid}-${randomBytes(6).toString('hex')}.sock`;
  let received;
  const broker=http.createServer((request,response)=>{
    received={method:request.method,path:request.url,token:request.headers['x-whirlwind-mcp-token'],proxyAuth:request.headers['proxy-authorization']};
    request.resume();
    request.once('end',()=>{response.writeHead(200,{'content-type':'application/json'});response.end('{"jsonrpc":"2.0","id":1,"result":{}}');});
  });
  await new Promise((resolve,reject)=>{broker.once('error',reject);broker.listen(socketPath,resolve);});
  t.after(async()=>{await close(broker);await import('node:fs/promises').then(fs=>fs.rm(socketPath,{force:true}));});
  const proxy=createEgressProxy({token,mcpBroker:{id:'selected-tools',socket_path:socketPath,host:'whirlwind-mcp.openshell.internal',path:'/mcp'},mcpBrokerToken:brokerToken});
  await listen(proxy);
  t.after(()=>close(proxy));
  const response=await requestThroughProxy(proxy,{token,brokerId:'selected-tools',path:'http://whirlwind-mcp.openshell.internal/mcp',method:'POST',body:'{"jsonrpc":"2.0","id":1,"method":"tools/list"}'});
  assert.equal(response.statusCode,200);
  assert.equal(received.method,'POST');
  assert.equal(received.path,'/mcp');
  assert.equal(received.token,brokerToken);
  assert.equal(received.proxyAuth,undefined);
  assert.equal(received.brokerId,undefined);
  for(const testCase of [
    {path:'http://whirlwind-mcp.openshell.internal/other',method:'POST'},
    {path:'http://whirlwind-mcp.openshell.internal/mcp',method:'GET'},
    {path:'http://whirlwind-mcp.openshell.internal/mcp',method:'POST',brokerId:'other-broker'},
  ]) {
    const denied=await requestThroughProxy(proxy,{token,...testCase});
    assert.equal(denied.statusCode,403);
  }
});

test('MCP relay requires a complete descriptor and opaque host capability', () => {
  const token=Buffer.alloc(32,7).toString('hex');
  const broker={id:'selected-tools',socket_path:'/tmp/selected-tools.sock',host:'whirlwind-mcp.openshell.internal',path:'/mcp'};
  assert.throws(()=>createEgressProxy({token:'proxy-capability-long-enough-0123456789',mcpBroker:broker}),/descriptor or host-only capability/i);
  assert.throws(()=>createEgressProxy({token:'proxy-capability-long-enough-0123456789',mcpBroker:{...broker,host:'other.internal'},mcpBrokerToken:token}),/descriptor or host-only capability/i);
});

test('proxy denials expose only fixed diagnostic categories', async t => {
  const token=Buffer.alloc(32,9).toString('hex');
  const proxy=createEgressProxy({token,lookup:async()=>[{address:'127.0.0.1',family:4}],request:()=>{throw new Error('must not dial');}});
  await listen(proxy);
  t.after(()=>close(proxy));
  const unauthenticated=await requestThroughProxy(proxy,{token:'wrong-token-value-long-enough',path:'http://public.example/'});
  assert.equal(unauthenticated.statusCode,403);
  assert.equal(unauthenticated.headers['x-whirlwind-proxy-denial'],'authentication');
  const deniedPort=await requestThroughProxy(proxy,{token,path:'http://public.example:22/'});
  assert.equal(deniedPort.statusCode,403);
  assert.equal(deniedPort.headers['x-whirlwind-proxy-denial'],'target');
  const deniedDNS=await requestThroughProxy(proxy,{token,path:'http://private.example/'});
  assert.equal(deniedDNS.statusCode,403);
  assert.equal(deniedDNS.headers['x-whirlwind-proxy-denial'],'dns_policy');
  assert.equal(deniedDNS.headers['content-length'],'0');
});

function requestThroughProxy(proxy,{token,brokerId,path,method='POST',body=''}) {
  return new Promise((resolve,reject)=>{
    const headers={'Proxy-Authorization':`Basic ${Buffer.from(`whirlwind:${token}`).toString('base64')}`};
    if(brokerId) headers['X-Whirlwind-Broker-ID']=brokerId;
    const request=http.request({host:'127.0.0.1',port:proxy.address().port,method,path,headers},response=>{
      response.resume();
      response.once('end',()=>resolve(response));
    });
    request.once('error',reject);
    request.end(body);
  });
}
