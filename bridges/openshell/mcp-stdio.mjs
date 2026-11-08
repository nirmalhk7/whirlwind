import http from 'node:http';
import readline from 'node:readline';

const target = 'http://whirlwind-mcp.openshell.internal/mcp';
const maxMessageBytes = 1024 * 1024;
const brokerID = process.argv[2] ?? '';

function proxyAuthority() {
  const value = process.env.HTTP_PROXY || process.env.http_proxy || '';
  let proxy;
  try { proxy = new URL(value); } catch { throw new Error('MCP egress proxy is unavailable'); }
  if (proxy.protocol !== 'http:' || !proxy.hostname || !proxy.port || proxy.username !== 'whirlwind' || !proxy.password) {
    throw new Error('MCP egress proxy configuration is invalid');
  }
  return {
    host: proxy.hostname,
    port: Number(proxy.port),
    authorization: `Basic ${Buffer.from(`${decodeURIComponent(proxy.username)}:${decodeURIComponent(proxy.password)}`).toString('base64')}`,
  };
}

function validBrokerID(value) {
  return /^[A-Za-z0-9._-]{1,128}$/.test(value);
}

function rpcError(id, code, message) {
  return {jsonrpc: '2.0', id: id ?? null, error: {code, message}};
}

function writeMessage(message) {
  process.stdout.write(`${JSON.stringify(message)}\n`);
}

function forward(message, proxy) {
  return new Promise((resolve, reject) => {
    const body = Buffer.from(JSON.stringify(message));
    if (body.length > maxMessageBytes) return reject(new Error('MCP request exceeds size limit'));
    const request = http.request({
      host: proxy.host,
      port: proxy.port,
      method: 'POST',
      path: target,
      headers: {
        host: new URL(target).host,
        'content-type': 'application/json',
        'content-length': String(body.length),
        'proxy-authorization': proxy.authorization,
        'x-whirlwind-broker-id': brokerID,
      },
    }, response => {
      const chunks = [];
      let size = 0;
      response.on('data', chunk => {
        size += chunk.length;
        if (size > maxMessageBytes) {
          response.destroy(new Error('MCP response exceeds size limit'));
          return;
        }
        chunks.push(chunk);
      });
      response.once('end', () => resolve({status: response.statusCode ?? 502, reason: response.headers['x-whirlwind-proxy-denial'], body: Buffer.concat(chunks, size)}));
      response.once('error', reject);
    });
    request.once('error', reject);
    request.end(body);
  });
}

async function handle(line, proxy) {
  let message;
  try { message = JSON.parse(line); } catch { return; }
  if (!message || message.jsonrpc !== '2.0' || typeof message.method !== 'string') {
    if (message && Object.hasOwn(message, 'id')) writeMessage(rpcError(message.id, -32600, 'Invalid MCP request'));
    return;
  }
  if (!['initialize', 'notifications/initialized', 'tools/list', 'tools/call'].includes(message.method)) {
    if (Object.hasOwn(message, 'id')) writeMessage(rpcError(message.id, -32601, 'Unsupported MCP method'));
    return;
  }
  try {
    const response = await forward(message, proxy);
    if (!Object.hasOwn(message, 'id')) return;
    if (response.status < 200 || response.status >= 300) {
      const reason = typeof response.reason === 'string' ? response.reason : 'broker_unavailable';
      writeMessage(rpcError(message.id, -32000, `MCP broker request denied: ${reason}`));
      return;
    }
    const decoded = JSON.parse(response.body.toString('utf8'));
    writeMessage(decoded);
  } catch {
    if (Object.hasOwn(message, 'id')) writeMessage(rpcError(message.id, -32000, 'MCP broker transport failed'));
  }
}

if (!validBrokerID(brokerID)) {
  process.stderr.write('MCP broker identity is invalid\n');
  process.exitCode = 2;
} else {
  let proxy;
  try { proxy = proxyAuthority(); } catch (error) {
    process.stderr.write(`${error.message}\n`);
    process.exitCode = 2;
  }
  if (proxy) {
    const input = readline.createInterface({input: process.stdin, crlfDelay: Infinity});
    input.on('line', line => { void handle(line, proxy); });
  }
}
