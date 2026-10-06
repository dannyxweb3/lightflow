let csrf = '';
let page = 'overview';
const $ = id => document.getElementById(id);
const node = (tag, text, cls) => { const n=document.createElement(tag); if(text!==undefined)n.textContent=String(text); if(cls)n.className=cls; return n; };
const add = (parent,...children) => { for(const child of children)parent.append(child); return parent; };
const fmt = value => value ? new Date(value).toLocaleString() : '—';
const notice = message => { $('notice').textContent=message || ''; };
async function api(path, options={}) {
  const headers={'Accept':'application/json',...(options.body?{'Content-Type':'application/json'}:{}),...(options.method&&options.method!=='GET'?{'X-CSRF-Token':csrf}:{})};
  const response=await fetch('/console/api/'+path,{credentials:'same-origin',...options,headers});
  if(response.status===401){showLogin();throw Error('管理会话已失效，请重新登录');}
  if(!response.ok){let body={};try{body=await response.json();}catch{}throw Error(body.code||`HTTP ${response.status}`);}
  return response.status===204?null:response.json();
}
function showLogin(){csrf='';$('app').hidden=true;$('login').hidden=false;}
function showApp(){$('login').hidden=true;$('app').hidden=false;render();}
function field(form,label,name,type='text',value='',required=true){const wrap=node('div');const caption=node('label',label);const input=node('input');input.name=name;input.type=type;input.value=value??'';input.required=required;add(caption,input);add(wrap,caption);form.append(wrap);return input;}
function checkbox(form,label,name,checked){const wrap=node('label');const input=node('input');input.type='checkbox';input.name=name;input.checked=checked;input.style.width='auto';wrap.append(input,document.createTextNode(' '+label));form.append(wrap);return input;}
function action(text,fn,cls='secondary'){const b=node('button',text,cls);b.type='button';b.addEventListener('click',()=>Promise.resolve(fn()).catch(e=>notice(e.message)));return b;}
function table(headers,rows){const wrap=node('div',undefined,'table-wrap');const t=node('table'),head=node('tr');headers.forEach(x=>head.append(node('th',x)));const thead=node('thead');thead.append(head);t.append(thead);const body=node('tbody');for(const row of rows){const tr=node('tr');for(const value of row){const td=node('td');if(value instanceof Node)td.append(value);else td.textContent=value??'—';tr.append(td)}body.append(tr)}t.append(body);wrap.append(t);return wrap;}
function card(title){const section=node('section',undefined,'card');section.append(node('h2',title));return section;}
function formSubmit(form,fn){form.addEventListener('submit',async e=>{e.preventDefault();try{const result=await fn(new FormData(form));if(result===false)return;await render();notice('操作成功');}catch(err){notice(err.message)}});}
async function overview(root){const data=await api('overview');const grid=node('div',undefined,'grid');[['用户总数',data.users],['活跃连接',data.active_sessions],['等待确认',data.pending_sessions],['可用节点',`${data.ready_endpoints} / ${data.total_endpoints}`]].forEach(([title,value])=>{const c=card(title);c.classList.add('metric');c.append(node('strong',value));grid.append(c)});root.append(grid);const hint=card('状态说明');hint.append(node('p','可用节点指已启用、已就绪，且最近 10 秒收到网关确认的节点。连接数为当前未过期的租约数。','muted'));root.append(hint);}
async function users(root){const top=node('div',undefined,'toolbar');const search=node('input');search.placeholder='按邮箱搜索';const go=action('搜索',()=>loadUsers());top.append(search,go);root.append(top);const list=card('用户列表');root.append(list);async function loadUsers(){const data=await api('users?search='+encodeURIComponent(search.value));const rows=data.users.map(u=>[u.email,u.subscription.plan,fmt(u.subscription.expires_at),u.device_count,u.active_sessions,u.subscription.enabled?'有效':'停用',action('查看',()=>userDetail(root,u.id))]);list.replaceChildren(node('h2','用户列表'),table(['邮箱','套餐','到期时间','设备','连接','订阅','操作'],rows));}search.addEventListener('keydown',e=>{if(e.key==='Enter')loadUsers().catch(x=>notice(x.message))});await loadUsers();
 const create=card('创建用户');const form=node('form',undefined,'form-grid');field(form,'邮箱','email','email');field(form,'初始密码（至少 12 位）','password','password');field(form,'套餐','plan','text','standard');field(form,'到期时间','expires_at','datetime-local');field(form,'设备上限','device_limit','number','5');field(form,'并发上限','concurrent_limit','number','2');checkbox(form,'启用订阅','enabled',true);const submit=node('button','创建用户');submit.type='submit';form.append(submit);
 formSubmit(form,async f=>{
   const subscription={plan:f.get('plan'),expires_at:new Date(f.get('expires_at')).toISOString(),device_limit:Number(f.get('device_limit')),concurrent_limit:Number(f.get('concurrent_limit')),enabled:f.has('enabled')};
   await api('users',{method:'POST',body:JSON.stringify({email:f.get('email'),password:f.get('password'),subscription})});
 });create.append(form);root.append(create);
}
async function userDetail(root,id){
 const u=await api('users/'+encodeURIComponent(id));root.replaceChildren();root.append(action('← 返回用户列表',()=>render()));
 const info=card(u.email);info.append(node('p',`账号：${u.enabled?'启用':'停用'}　创建：${fmt(u.created_at)}`,'muted'));root.append(info);
 const edit=card('订阅权益'),form=node('form',undefined,'form-grid');
 field(form,'套餐','plan','text',u.subscription.plan);field(form,'到期时间','expires_at','datetime-local',new Date(u.subscription.expires_at).toISOString().slice(0,16));field(form,'设备上限','device_limit','number',u.subscription.device_limit);field(form,'并发上限','concurrent_limit','number',u.subscription.concurrent_limit);checkbox(form,'启用订阅','enabled',u.subscription.enabled);
 const save=node('button','保存权益');save.type='submit';form.append(save);
 formSubmit(form,async f=>{
   if(!confirm('修改订阅会撤销该用户现有连接，确定继续？'))return false;
   const subscription={plan:f.get('plan'),expires_at:new Date(f.get('expires_at')).toISOString(),device_limit:Number(f.get('device_limit')),concurrent_limit:Number(f.get('concurrent_limit')),enabled:f.has('enabled')};
   await api('users/'+encodeURIComponent(id)+'/subscription',{method:'PUT',body:JSON.stringify(subscription)});
 });edit.append(form);root.append(edit);
 const ds=card('设备（最近 100 条）');ds.append(table(['名称','系统','创建时间','状态'],u.devices.map(d=>[d.name,d.os,fmt(d.created_at),d.revoked_at?'已删除':'有效'])));root.append(ds);
 const ss=card('连接（最近 100 条）');ss.append(table(['租约','节点','状态','创建','到期'],u.sessions.map(s=>[s.id,s.endpoint_id,s.state,fmt(s.created_at),fmt(s.expires_at)])));root.append(ss);
}
async function endpoints(root){const data=await api('endpoints');const list=card('协议入口');list.append(table(['ID','国家','地址','状态','最近确认','操作'],data.endpoints.map(e=>{const status=e.enabled?(e.ready?'就绪':'未就绪'):'停用';return [e.id,e.country_code,`${e.host}:${e.port}`,status,fmt(e.last_seen_at),action(e.enabled?'停用':'启用',async()=>{if(e.enabled&&!confirm('停用节点会撤销现有连接，确定继续？'))return;await api('endpoints/'+encodeURIComponent(e.id),{method:'PATCH',body:JSON.stringify({enabled:!e.enabled})});notice('节点状态已更新');render()},e.enabled?'danger':'secondary')]})));root.append(list);const create=card('新增节点');const form=node('form',undefined,'form-grid');[['节点 ID','id'],['国家代码','country_code'],['地址','host'],['SNI','server_name'],['端口','port'],['容量','capacity'],['网关令牌（至少 32 字符）','auth_token']].forEach(([label,name])=>field(form,label,name,name==='port'||name==='capacity'?'number':'text'));const submit=node('button','创建节点');submit.type='submit';form.append(submit);formSubmit(form,async f=>{await api('endpoints',{method:'POST',body:JSON.stringify({id:f.get('id'),country_code:f.get('country_code'),host:f.get('host'),server_name:f.get('server_name'),port:Number(f.get('port')),capacity:Number(f.get('capacity')),auth_token:f.get('auth_token')})})});create.append(form);root.append(create);}
async function countries(root){const data=await api('countries');const list=card('国家');list.append(table(['代码','名称','状态'],data.countries.map(c=>[c.code,c.name,c.enabled?'启用':'停用'])));root.append(list);const create=card('新增或更名');const form=node('form',undefined,'form-grid');field(form,'两位国家代码','code');field(form,'显示名称','name');const submit=node('button','保存');submit.type='submit';form.append(submit);formSubmit(form,async f=>{await api('countries',{method:'POST',body:JSON.stringify({code:f.get('code').toUpperCase(),name:f.get('name')})})});create.append(form);root.append(create);}
async function render(){const titles={overview:'概览',users:'用户与订阅',endpoints:'节点',countries:'国家'};$('title').textContent=titles[page];document.querySelectorAll('nav button').forEach(b=>b.classList.toggle('active',b.dataset.page===page));const root=$('content');root.replaceChildren();notice('');try{await ({overview,users,endpoints,countries})[page](root)}catch(e){notice(e.message)}}
$('login-form').addEventListener('submit',async e=>{e.preventDefault();const form=e.currentTarget;$('login-error').textContent='';try{const password=new FormData(form).get('password');const response=await fetch('/console/api/login',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json'},body:JSON.stringify({password})});const data=await response.json();if(!response.ok)throw Error(data.code||'登录失败');csrf=data.csrf_token;form.reset();showApp()}catch(err){$('login-error').textContent=err.message}});
document.querySelectorAll('nav button').forEach(b=>b.addEventListener('click',()=>{page=b.dataset.page;render()}));$('refresh').addEventListener('click',render);$('logout').addEventListener('click',async()=>{try{await api('logout',{method:'POST'})}finally{showLogin()}});
api('session').then(data=>{csrf=data.csrf_token;showApp()}).catch(()=>showLogin());
