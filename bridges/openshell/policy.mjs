import {bounded,buildFilesystemPolicy} from './policy_filesystem.mjs';
import {buildDriverMounts} from './policy_mounts.mjs';

export {buildDriverMounts};

const reservedWorkloadEnvironment=new Set(['PATH','HOME','CODEX_HOME','CLAUDE_CONFIG_DIR','PWD','TMPDIR','TMP','TEMP']);
const reservedWorkloadPrefixes=['XDG_','WHIRLWIND_','OPENSHELL_','DYLD_','LD_'];

function reservedEnvironmentName(name) {
  if(reservedWorkloadEnvironment.has(name)) return true;
  return reservedWorkloadPrefixes.some(prefix=>name.startsWith(prefix));
}

export function buildSandboxPolicy(profile,proxyPort,binaries) {
  if(!Number.isInteger(proxyPort)||proxyPort<1||proxyPort>65535) throw new Error('Invalid local egress proxy port');
  if(!Array.isArray(binaries)||!binaries.length) throw new Error('OpenShell policy needs the sandbox agent executable');
  const filesystem=buildFilesystemPolicy(profile);
  return {
    version:1,
    filesystem_policy:{include_workdir:false,...filesystem},
    landlock:{compatibility:'hard_requirement'},
    network_policies:{proxy_egress:{name:'Whirlwind controlled web egress',endpoints:[{host:'host.openshell.internal',port:proxyPort,protocol:'tcp',tls:'skip'}],binaries:[...new Set(binaries)].map(path=>({path:bounded(path,'agent executable')}))}},
  };
}

export function requireLocalGateway(gateways) {
  const gateway=Array.isArray(gateways)?gateways.find(item=>item?.active===true):undefined;
  if(!gateway||gateway.is_remote!==false||typeof gateway.endpoint!=='string')
    throw new Error('OpenShell requires a healthy local gateway; remote or unrecognized gateway metadata cannot reach the local policy proxy');
  let host;
  try { host=new URL(gateway.endpoint).hostname.toLowerCase().replace(/^\[|\]$/g,''); } catch { throw new Error('OpenShell requires a local gateway with a loopback endpoint'); }
  const loopback=host==='localhost'||host==='::1'||/^127(?:\.\d{1,3}){3}$/.test(host);
  if(!loopback) throw new Error('OpenShell requires a local gateway with a loopback endpoint');
  return gateway;
}

export function requirePinnedGateway(gateways,name,endpoint) {
  const gateway=Array.isArray(gateways)?gateways.find(item=>item?.name===name&&item?.endpoint===endpoint):undefined;
  if(!gateway||gateway.is_remote!==false||typeof gateway.endpoint!=='string')
    throw new Error('OpenShell pinned gateway is missing, remote, or has changed identity');
  let host;
  try { host=new URL(gateway.endpoint).hostname.toLowerCase().replace(/^\[|\]$/g,''); } catch { throw new Error('OpenShell pinned gateway must use a loopback endpoint'); }
  const loopback=host==='localhost'||host==='::1'||/^127(?:\.\d{1,3}){3}$/.test(host);
  if(!loopback) throw new Error('OpenShell pinned gateway must use a loopback endpoint');
  return gateway;
}

export function workloadEnvironment(profile,environment) {
  return (profile.environment??[]).filter(name=>!reservedEnvironmentName(name)&&environment[name]!==undefined);
}
