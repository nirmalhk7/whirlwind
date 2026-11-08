import http from 'node:http';
import net from 'node:net';
import dns from 'node:dns/promises';
import {isIP} from 'node:net';

const DENIED = 'Proxy request denied';
const MCP_BODY_LIMIT = 1024 * 1024;
const MCP_HOST = 'whirlwind-mcp.openshell.internal';
const MCP_PATH = '/mcp';

function privateAddress(address) {
  const family = isIP(address);
  if (family === 4) {
    const [a,b,c] = address.split('.').map(Number);
    return a === 0 || a === 10 || a === 127 || a >= 224 ||
      (a === 100 && b >= 64 && b <= 127) ||
      (a === 169 && b === 254) ||
      (a === 172 && b >= 16 && b <= 31) ||
      (a === 192 && (b === 0 || b === 168 || (b === 88 && c === 99) || (b === 51 && c === 100))) ||
      (a === 198 && (b === 18 || b === 19 || (b === 51 && c === 100))) ||
      (a === 203 && b === 0 && c === 113);
  }
  if (family === 6) {
    const groups = ipv6Groups(address);
    if (!groups) return true;
    if (groups.slice(0,5).every(group => group === 0) && groups[5] === 0xffff) {
      return privateAddress(`${groups[6] >> 8}.${groups[6] & 255}.${groups[7] >> 8}.${groups[7] & 255}`);
    }
    const first = groups[0];
    const second = groups[1];
    const globalUnicast = first >= 0x2000 && first <= 0x3fff;
    const special2001 = first === 0x2001 && second <= 0x01ff;
    const documentation = first === 0x2001 && second === 0x0db8 || first === 0x3fff && second < 0x1000;
    return !globalUnicast || special2001 || documentation || first === 0x2002;
  }
  return true;
}

function ipv6Groups(address) {
  let value = address.toLowerCase().split('%')[0];
  const dotted = value.match(/(?:^|:)(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})$/);
  if (dotted) {
    const octets = dotted[1].split('.').map(Number);
    if (octets.some(octet => octet > 255)) return null;
    const hex = `${((octets[0] << 8) | octets[1]).toString(16)}:${((octets[2] << 8) | octets[3]).toString(16)}`;
    value = value.slice(0, -dotted[1].length) + hex;
  }
  const split = value.split('::');
  if (split.length > 2) return null;
  const left = split[0] ? split[0].split(':') : [];
  const right = split.length === 2 && split[1] ? split[1].split(':') : [];
  if ([...left, ...right].some(group => !/^[a-f0-9]{1,4}$/.test(group))) return null;
  const zeros = 8 - left.length - right.length;
  if (split.length === 1 && zeros !== 0 || split.length === 2 && zeros < 1) return null;
  return [...left.map(group => Number.parseInt(group, 16)), ...Array(zeros).fill(0), ...right.map(group => Number.parseInt(group, 16))];
}

function authority(value) {
  if (typeof value !== 'string' || value.includes('@') || value.includes('/') || value.includes('\\')) return null;
  let host, port;
  if (value.startsWith('[')) {
    const end = value.indexOf(']');
    if (end < 0 || value[end + 1] !== ':') return null;
    host = value.slice(1, end);
    port = value.slice(end + 2);
  } else {
    const split = value.lastIndexOf(':');
    if (split <= 0 || value.slice(0, split).includes(':')) return null;
    host = value.slice(0, split);
    port = value.slice(split + 1);
  }
  if (!/^[0-9]{1,5}$/.test(port)) return null;
  const number = Number(port);
  if (number < 1 || number > 65535 || ![80,443].includes(number)) return null;
  return {host:host.toLowerCase().replace(/\.$/, ''), port:number};
}

function authenticated(request, token) {
  const header = request.headers['proxy-authorization'];
  if (!header?.startsWith('Basic ')) return false;
  const decoded = Buffer.from(header.slice(6), 'base64').toString('utf8');
  return decoded === `whirlwind:${token}`;
}

function authorizedTarget(request, token, value) {
  if (!authenticated(request,token)) return null;
  return authority(value);
}

function authorizedPrivateAddress(address) {
  if (isIP(address) === 4) {
    const [a,b] = address.split('.').map(Number);
    return a === 10 || a === 172 && b >= 16 && b <= 31 || a === 192 && b === 168;
  }
  if (isIP(address) === 6) {
    const groups = ipv6Groups(address);
    return !!groups && (groups[0] & 0xfe00) === 0xfc00;
  }
  return false;
}

function permittedPrivateHost(host, allowedHosts) {
  const normalized = host.toLowerCase().replace(/\.$/, '');
  return allowedHosts.some(entry => typeof entry === 'string' &&
    entry.toLowerCase().replace(/\.$/, '') === normalized);
}

async function resolvePublic(host, lookup, allowedPrivateHosts=[]) {
  const literalFamily = isIP(host);
  const addresses = literalFamily ? [{address:host,family:literalFamily}] : await lookup(host,{all:true,verbatim:true});
  if (!addresses.length || addresses.some(entry => privateAddress(entry.address) &&
      !(permittedPrivateHost(host,allowedPrivateHosts) && authorizedPrivateAddress(entry.address)))) throw new Error(DENIED);
  return addresses;
}

function deny(response,reason='target') {
  response.writeHead(403,{'content-type':'text/plain','content-length':0,'connection':'close','x-whirlwind-proxy-denial':reason});
  response.end();
}

function denySocket(socket,reason='target') {
  socket.end(`HTTP/1.1 403 Forbidden\r\ncontent-type: text/plain\r\ncontent-length: 0\r\nx-whirlwind-proxy-denial: ${reason}\r\nconnection: close\r\n\r\n`);
}

function forwardedHeaders(headers,host) {
  const result={...headers,host};
  const connectionHeaders=String(result.connection??'').split(',').map(value=>value.trim().toLowerCase()).filter(Boolean);
  for(const name of ['proxy-authorization','proxy-authenticate','proxy-connection','connection','keep-alive','te','trailer','transfer-encoding','upgrade',...connectionHeaders]) delete result[name];
  return result;
}

function validMCPBroker(broker,token) {
  return broker && typeof broker.id==='string' && broker.id.length>0 &&
    typeof broker.socket_path==='string' && broker.socket_path.startsWith('/') &&
    broker.host===MCP_HOST && broker.path===MCP_PATH && typeof token==='string' && token.length>=32;
}

function collectBrokerBody(request,response) {
  return new Promise((resolve,reject)=>{
    const chunks=[];
    let size=0;
    let tooLarge=false;
    request.on('data',chunk=>{
      size+=chunk.length;
      if(size>MCP_BODY_LIMIT) { tooLarge=true; chunks.length=0; }
      else if(!tooLarge) chunks.push(chunk);
    });
    request.once('end',()=>{
      if(tooLarge) {
        response.writeHead(413,{'content-length':0,'connection':'close','x-whirlwind-proxy-denial':'broker_body_limit'});
        response.end();
        resolve(null);
      } else resolve(Buffer.concat(chunks,size));
    });
    request.once('error',reject);
  });
}

async function relayMCPBroker(request,response,url,broker,brokerToken,isActive) {
  if(!isActive()) return deny(response,'broker_inactive');
  if(request.method!=='POST'||request.headers['x-whirlwind-broker-id']!==broker.id) return deny(response,'broker_authorization');
  if(url.pathname!==broker.path||url.search||url.port&&url.port!=='80') return deny(response,'target');
  const body=await collectBrokerBody(request,response);
  if(body===null) return;
  if(!isActive()) return deny(response,'broker_inactive');
  const headers=forwardedHeaders(request.headers,broker.host);
  delete headers['x-whirlwind-broker-id'];
  headers['x-whirlwind-mcp-token']=brokerToken;
  headers['content-length']=String(body.length);
  const upstream=http.request({socketPath:broker.socket_path,method:'POST',path:broker.path,headers});
  upstream.on('response',result=>{
    const safeHeaders=forwardedHeaders(result.headers,broker.host);
    response.writeHead(result.statusCode??502,safeHeaders);
    result.pipe(response);
  });
  upstream.on('error',()=>deny(response,'broker_unavailable'));
  upstream.end(body);
}

export function createEgressProxy({token, allowedPrivateHosts=[], mcpBroker, mcpBrokerToken, lookup=dns.lookup, connect=net.connect, request:makeRequest=http.request}) {
  if (typeof token !== 'string' || token.length < 32) throw new Error('OpenShell proxy requires a per-sandbox token');
  if(mcpBroker!==undefined&&!validMCPBroker(mcpBroker,mcpBrokerToken)) throw new Error('OpenShell MCP broker descriptor or host-only capability is invalid');
  let active=true;
  const server = http.createServer(async (request,response) => {
    if (!authenticated(request,token)) return deny(response,'authentication');
    let url;
    try { url=new URL(request.url); } catch { return deny(response,'target'); }
    if (url.protocol!=='http:'||url.username||url.password) return deny(response,'target');
    if(mcpBroker&&url.hostname.toLowerCase()===MCP_HOST) return relayMCPBroker(request,response,url,mcpBroker,mcpBrokerToken,()=>active).catch(()=>deny(response,'broker_unavailable'));
    const hostname=url.hostname.replace(/^\[|\]$/g,'');
    const target=authority(`${hostname.includes(':') ? `[${hostname}]` : hostname}:${url.port || 80}`);
    if (!target) return deny(response,'target');
    let addresses;
    try { addresses=await resolvePublic(target.host,lookup,allowedPrivateHosts); } catch { return deny(response,'dns_policy'); }
    const headers=forwardedHeaders(request.headers,url.host);
    const upstream=makeRequest({host:addresses[0].address,family:addresses[0].family,port:target.port,method:request.method,path:`${url.pathname}${url.search}`,headers});
    upstream.on('response',result=>{response.writeHead(result.statusCode??502,result.headers);result.pipe(response);});
    upstream.on('error',()=>deny(response,'upstream_connection'));
    request.pipe(upstream);
  });
  server.on('connect',async (request,client,head) => {
    if(!authenticated(request,token)) return denySocket(client,'authentication');
    const target=authority(request.url);
    if (!target||mcpBroker&&target.host===MCP_HOST) return denySocket(client,'target');
    try {
      const addresses=await resolvePublic(target.host,lookup,allowedPrivateHosts);
      const upstream=connect({host:addresses[0].address,family:addresses[0].family,port:target.port});
      upstream.once('connect',()=>{client.write('HTTP/1.1 200 Connection Established\r\n\r\n');if(head.length)upstream.write(head);upstream.pipe(client);client.pipe(upstream);});
      upstream.once('error',()=>denySocket(client,'upstream_connection'));
      client.once('error',()=>upstream.destroy());
    } catch { denySocket(client,'dns_policy'); }
  });
  server.once('close',()=>{active=false;});
  return server;
}

export const proxyInternals = {privateAddress,authority,resolvePublic,authorizedTarget,forwardedHeaders};
