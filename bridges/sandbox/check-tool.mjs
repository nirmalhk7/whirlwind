import readline from 'node:readline';
import http from 'node:http';

const [socketPath,token]=process.argv.slice(2);
const schema={type:'object',properties:{check_id:{type:'string',description:'Exact committed project check ID'}},required:['check_id'],additionalProperties:false};
function runCheck(args){return new Promise((resolve,reject)=>{
 const request=http.request({socketPath,path:'/run_check',method:'POST',headers:{'Content-Type':'application/json','X-Whirlwind-Check-Token':token}},response=>{
  let text='';response.setEncoding('utf8');response.on('data',part=>{text+=part;if(text.length>1024*1024)request.destroy(new Error('Broker output exceeds limit'));});response.on('end',()=>response.statusCode===200?resolve(text):reject(new Error(text)));
 });request.on('error',reject);request.end(JSON.stringify(args));
});}
readline.createInterface({input:process.stdin}).on('line',async line=>{
 let request;try{request=JSON.parse(line);}catch{return;}
 if(request.id===undefined)return;
 const reply=value=>process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:request.id,...value})+'\n');
 try{
  let result;
  if(request.method==='initialize')result={protocolVersion:request.params?.protocolVersion??'2024-11-05',capabilities:{tools:{}},serverInfo:{name:'whirlwind-checks',version:'1.0.0'}};
  else if(request.method==='tools/list')result={tools:[{name:'run_check',description:'Run a committed check in an isolated snapshot. No arbitrary commands.',inputSchema:schema}]};
  else if(request.method==='tools/call'&&request.params?.name==='run_check')result={content:[{type:'text',text:await runCheck(request.params.arguments)}]};
  else throw new Error('Unsupported reviewer tool');
  reply({result});
 }catch(error){reply({error:{code:-32602,message:error.message}});}
});
