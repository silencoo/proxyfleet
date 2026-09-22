import { copyText, readJSON } from '../api';
import { tr, translateTree, localizedAPIMessage } from '../i18n';
import { jobMessages } from './jobs-messages';

export interface JobConfig {
  [key: string]: string | number;
  name: string; mode: string; selection: string; profile: string; endpoint: string;
  target_url: string; size: number; concurrency: number; timeout: string;
  refresh_interval: string; session_ttl: string; max_sessions: number;
  probe_batch_size: number; probe_concurrency: number; max_response_bytes: number;
  expected_status: number; body_contains: string;
}
interface SelectedNode { node: string; name?: string; duration_ms?: number; success_rate?: number; checked_at?: string }
interface JobStatus { name: string; mode: string; selection: string; candidates: number; measured: number; refreshing: boolean; selected: SelectedNode[] }
interface SessionInfo { job: string; session: string; node: string; state: string; expires_at: string }
interface Access extends SessionInfo { proxy_url?: string; concurrency: number; timeout: string }
interface SessionPage { sessions: SessionInfo[]; pagination: { page: number; total_items: number; total_pages: number } }

function readJobStatuses(data: {jobs: JobStatus[]}): JobStatus[] {
  if (!Array.isArray(data.jobs) || data.jobs.some(j => !j || typeof j.name !== 'string'
      || !['pooled','pinned'].includes(j.mode) || !['manual','auto'].includes(j.selection)
      || typeof j.refreshing !== 'boolean' || !Array.isArray(j.selected)
      || j.selected.some(n => !n || typeof n.node !== 'string'
        || (n.duration_ms !== undefined && !Number.isFinite(n.duration_ms))
        || (n.success_rate !== undefined && !Number.isFinite(n.success_rate))))) {
    throw new Error(tr('服务器响应格式无效，请重试。'));
  }
  return data.jobs;
}

class JobAPIError extends Error {
  constructor(message: string, readonly status: number) { super(message); }
}

const defaults: JobConfig = {name:'', mode:'pinned', selection:'manual', profile:'', endpoint:'', target_url:'', size:0,
  concurrency:8, timeout:'15s', refresh_interval:'10m', session_ttl:'30m', max_sessions:1024,
  probe_batch_size:32, probe_concurrency:4, max_response_bytes:2097152, expected_status:200, body_contains:''};
const esc = (value: unknown): string => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]!));
const icon = (name: string): string => `<svg class="icon" aria-hidden="true"><use href="#i-${name}"></use></svg>`;
const stateLabel = (state: string): string => tr(({ready:'可用', paused:'已暂停', expired:'已过期', policy_changed:'配置已变更', removed:'任务已删除', unavailable:'运行时不可用'} as Record<string,string>)[state] || state);
const date = (value: string): string => value && !value.startsWith('0001-') ? new Date(value).toLocaleString(document.documentElement.lang || undefined) : '—';
const durationMS = (value: string): number => {
  const units: Record<string,number> = {ns:.000001, us:.001, 'µs':.001, 'μs':.001, ms:1, s:1000, m:60000, h:3600000};
  const parts = [...value.matchAll(/(\d+(?:\.\d*)?|\.\d+)(ns|us|µs|μs|ms|s|m|h)/g)];
  if (!parts.length || parts.map(p => p[0]).join('') !== value) return NaN;
  return parts.reduce((sum, p) => sum + Number(p[1])*units[p[2]], 0);
};

declare global {
  interface Window {
    proxyFleetJobs?: JobsEditor;
    proxyFleetJobRuntime?: JobsRuntime;
    switchTab?: (name: string) => Promise<void>;
  }
}

class JobsEditor {
  private jobs: JobConfig[] = [];
  private saved = '[]';
  constructor(private mount: HTMLElement) {
    mount.innerHTML = `<section class="panel jobs-editor"><div class="panel-header"><h2 class="panel-title">抓取任务配置</h2><button type="button" class="btn btn-sm" data-add-job>${icon('plus')}<span>添加任务</span></button></div>
      <p class="job-help">节点来源与连接策略独立配置。与 Profile、Endpoint 一起保存后生效。</p>
      <div class="job-error" data-jobs-error role="alert"></div><div data-job-list></div></section>`;
    mount.addEventListener('click', event => { void this.click(event); });
    mount.addEventListener('input', event => this.changed(event));
    mount.addEventListener('change', event => this.changed(event));
    document.getElementById('settingsTab')?.addEventListener('input', event => { if (!mount.contains(event.target as Node)) this.syncReferences(); });
    document.getElementById('settingsTab')?.addEventListener('change', event => { if (!mount.contains(event.target as Node)) this.syncReferences(); });
    document.getElementById('settingsTab')?.addEventListener('settings-structure-change', () => this.syncReferences());
    document.addEventListener('proxyfleet:language', () => this.syncReferences());
    this.render();
  }
  load(jobs: JobConfig[]): void {
    this.jobs = (Array.isArray(jobs) ? jobs : []).map(j => ({...defaults, ...j, selection:j.selection || 'manual'}));
    this.render(); this.markSaved();
  }
  markSaved(): void { this.saved = JSON.stringify(this.serialize()); }
  dirty(): boolean { return this.saved !== JSON.stringify(this.serialize()); }
  serialize(): JobConfig[] {
    return [...this.mount.querySelectorAll<HTMLElement>('[data-job-row]')].map((row, index) => {
      const result = {...this.jobs[index]};
      row.querySelectorAll<HTMLInputElement | HTMLSelectElement>('[data-job-field]').forEach(input => {
        const field = input.dataset.jobField!;
        result[field] = typeof defaults[field] === 'number' ? Number(input.value) : (field === 'body_contains' ? input.value : input.value.trim());
      });
      return result;
    });
  }
  private field(job: JobConfig, index: number, key: string, label: string, help = '', bounds?: [number,number]): string {
    const id = `job-${index}-${key}`;
    const control = key==='body_contains' ? `<textarea id="${id}" class="setting-input" data-job-field="${key}" rows="2" aria-describedby="${id}-error ${id}-help">${esc(job[key])}</textarea>` : `<input id="${id}" class="setting-input" data-job-field="${key}" value="${esc(job[key])}" ${bounds ? `type="number" min="${bounds[0]}" max="${bounds[1]}" step="1"` : 'type="text"'} aria-describedby="${id}-error ${id}-help" autocomplete="off">`;
    return `<div class="form-group"><label for="${id}">${label}</label>${control}
      <div id="${id}-help" class="field-help">${help}</div><div id="${id}-error" class="field-error" role="alert"></div></div>`;
  }
  private select(index: number, key: string, label: string, options: [string,string][], value: string): string {
    const id = `job-${index}-${key}`;
    return `<div class="form-group"><label for="${id}">${label}</label><select id="${id}" class="setting-input" data-job-field="${key}" aria-describedby="${id}-error">${options.map(([v,l])=>`<option value="${esc(v)}" ${v===value?'selected':''}>${esc(l)}</option>`).join('')}</select><div class="field-error" id="${id}-error" role="alert"></div></div>`;
  }
  private render(): void {
    const list = this.mount.querySelector<HTMLElement>('[data-job-list]')!;
    list.innerHTML = this.jobs.length ? this.jobs.map((j,i) => `<details class="job-config-row" data-job-row ${i===this.jobs.length-1?'open':''}>
      <summary><strong data-job-title>${esc(j.name || tr('新任务'))}</strong><span>${esc(j.mode)} / ${esc(j.selection)}</span></summary>
      <div class="job-config-body"><div class="job-row-actions"><button type="button" class="btn btn-sm btn-danger" data-remove-job="${i}">${icon('trash')}<span>删除任务</span></button></div>
      <div class="job-grid">${this.field(j,i,'name','任务名称','1–32 位小写字母、数字、点、下划线或连字符。')}
      ${this.select(i,'mode','连接策略',[['pooled','pooled · 新连接分配节点'],['pinned','pinned · 固定会话节点']],j.mode)}
      ${this.select(i,'selection','节点来源',[['manual','manual · 使用指定 Profile'],['auto','auto · 目标测速优选']],j.selection)}
      ${this.select(i,'profile','候选 Profile',[],j.profile)}${this.select(i,'endpoint','代理入口 Endpoint',[],j.endpoint)}</div>
      <p class="field-help">入口需启用且绑定相同 Profile，每个任务独占一个入口。pinned 入口必须配置用户名和密码；multi-port / hybrid 可选择独立节点端口。</p>
      <p class="field-help" data-manual-note>手动选择直接使用 Profile 的可用节点，不做任务目标测速，也不按数量缩减节点。</p>
      <fieldset class="job-fieldset"><legend>客户端限制</legend><div class="job-grid">
      ${this.field(j,i,'concurrency','请求并发数','由客户端执行，不是服务端全局配额。',[1,1024])}
      ${this.field(j,i,'timeout','请求超时','100ms–2m；同时用于自动测速超时。')}</div></fieldset>
      <fieldset class="job-fieldset" data-pinned-fields><legend>会话设置</legend><div class="job-grid">
      ${this.field(j,i,'session_ttl','会话有效期','1m–168h；到期拒绝新连接，不自动换节点。')}
      ${this.field(j,i,'max_sessions','最大会话数','过期记录仍占额度，需显式释放。',[1,4096])}</div></fieldset>
      <fieldset class="job-fieldset" data-auto-fields><legend>目标测速</legend><div class="job-grid">
      ${this.field(j,i,'target_url','目标 URL','HTTP(S) GET 地址，不携带登录信息，不跟随重定向。')}
      ${this.field(j,i,'size','优选节点数','最多选择多少个可用节点。',[1,64])}
      ${this.field(j,i,'refresh_interval','测速间隔','1m–24h，例如 10m。')}
      ${this.field(j,i,'probe_batch_size','每批测速节点数','必须不少于优选节点数。',[1,256])}
      ${this.field(j,i,'probe_concurrency','测速并发数','所有任务共享最多 16 个并发探测。',[1,16])}
      ${this.field(j,i,'max_response_bytes','最大响应字节数','完整读取响应，超过上限视为失败。',[1,8388608])}</div></fieldset>
      <details class="job-response-settings"><summary>响应校验</summary><div class="job-grid">
      ${this.field(j,i,'expected_status','预期状态码','自动测速与可选客户端校验使用。',[200,299])}
      ${this.field(j,i,'body_contains','响应包含文本','可选，区分正常页面与验证码等响应。')}</div></details>
      </div></details>`).join('') : '<p class="job-empty">尚未配置任务。添加任务以使用固定会话或独立抓取池。</p>';
    this.syncReferences();
    this.mount.querySelectorAll<HTMLElement>('[data-job-row]').forEach(row => this.toggle(row));
    translateTree(this.mount);
  }
  syncReferences(): void {
    const profiles = window.proxyFleetProfiles?.serialize() || [];
    const endpoints = window.proxyFleetEndpoints?.serialize() || [];
    this.mount.querySelectorAll<HTMLElement>('[data-job-row]').forEach((row,index) => {
      for (const key of ['profile','endpoint']) {
        const select = row.querySelector<HTMLSelectElement>(`[data-job-field="${key}"]`)!;
        const selected = select.options.length ? select.value : String(this.jobs[index]?.[key] || '');
        const options: [string,string][] = key==='profile' ? [['',tr('请选择 Profile')], ...profiles.map(p=>[p.name,p.name] as [string,string])] :
          [['',tr('独立节点端口（仅 pinned）')], ...endpoints.map(e=>[e.name,`${e.name} · ${e.port} · ${e.profile || tr('未绑定 Profile')}${e.enabled?'':` · ${tr('已停用')}`}`] as [string,string])];
        if (selected && !options.some(([v])=>v===selected)) options.push([selected,`${selected} · ${tr('不存在')}`]);
        select.innerHTML = options.map(([v,l])=>`<option value="${esc(v)}">${esc(l)}</option>`).join(''); select.value=selected;
      }
    });
  }
  private toggle(row: HTMLElement): void {
    const auto = row.querySelector<HTMLSelectElement>('[data-job-field="selection"]')!.value==='auto';
    const pinned = row.querySelector<HTMLSelectElement>('[data-job-field="mode"]')!.value==='pinned';
    for (const [selector,active] of [['[data-auto-fields]',auto],['[data-pinned-fields]',pinned]] as const) {
      const fields = row.querySelector<HTMLFieldSetElement>(selector)!; fields.hidden=!active; fields.disabled=!active;
    }
    row.querySelector<HTMLElement>('[data-manual-note]')!.hidden=auto;
    const size = row.querySelector<HTMLInputElement>('[data-job-field="size"]')!;
    size.min=auto?'1':'0';
    if (auto && Number(size.value)===0) size.value='5';
  }
  private changed(event: Event): void {
    const input = event.target as HTMLInputElement;
    const row = input.closest<HTMLElement>('[data-job-row]'); if (!row) return;
    input.removeAttribute('aria-invalid');
    const error = document.getElementById(`${input.id}-error`); if (error) error.textContent='';
    this.mount.querySelector<HTMLElement>('[data-jobs-error]')!.textContent='';
    if (['mode','selection'].includes(input.dataset.jobField || '')) this.toggle(row);
    row.querySelector<HTMLElement>('[data-job-title]')!.textContent=row.querySelector<HTMLInputElement>('[data-job-field="name"]')!.value || tr('新任务');
    row.querySelector<HTMLElement>('summary > span')!.textContent=`${row.querySelector<HTMLSelectElement>('[data-job-field="mode"]')!.value} / ${row.querySelector<HTMLSelectElement>('[data-job-field="selection"]')!.value}`;
  }
  validate(): boolean {
    const jobs = this.serialize(), names=new Set<string>(), used=new Set<string>();
    const profiles = new Set((window.proxyFleetProfiles?.serialize() || []).map(p=>p.name));
    const endpoints = window.proxyFleetEndpoints?.serialize() || [];
    const mode=(document.getElementById('settingMode') as HTMLSelectElement).value;
    const fail=(i:number, field:string, message:string): false => {
      const input=this.mount.querySelector<HTMLInputElement>(`#job-${i}-${field}`);
      if (input) { input.closest<HTMLDetailsElement>('[data-job-row]')!.open=true; input.closest<HTMLDetailsElement>('.job-response-settings')?.setAttribute('open',''); const fieldset=input.closest('fieldset'); if(fieldset){fieldset.hidden=false;fieldset.disabled=false;} input.setAttribute('aria-invalid','true'); document.getElementById(`${input.id}-error`)!.textContent=message; input.focus(); }
      this.mount.querySelector<HTMLElement>('[data-jobs-error]')!.textContent=message; return false;
    };
    if (jobs.length>64) return fail(0,'name',tr('最多配置 64 个任务。'));
    for (const [i,j] of jobs.entries()) {
      if (!/^[a-z0-9][a-z0-9._-]{0,31}$/.test(j.name) || names.has(j.name)) return fail(i,'name',tr('任务名称无效或重复。'));
      names.add(j.name);
      if (!profiles.has(j.profile)) return fail(i,'profile',tr('请选择已配置的 Profile。'));
      if (j.mode==='pooled' || j.endpoint) {
        const e=endpoints.find(e=>e.name===j.endpoint);
        if (mode==='multi-port' || !e || !e.enabled || e.profile!==j.profile || used.has(j.endpoint)) return fail(i,'endpoint',tr('请选择启用且绑定相同 Profile 的独立入口；需要 pool / hybrid 模式。'));
        if (j.mode==='pinned' && (!e.username || !e.password || new TextEncoder().encode(e.username).length>118 || new TextEncoder().encode(e.password).length>255 || /[:\x00-\x1f\x7f]/.test(e.username) || /[\x00-\x1f\x7f]/.test(e.password))) return fail(i,'endpoint',tr('pinned 入口需要有效用户名和密码，请在 Endpoint 中配置。'));
        used.add(j.endpoint);
      } else if (mode==='pool') return fail(i,'endpoint',tr('独立节点端口需要 multi-port / hybrid 模式。'));
      for (const [key,min,max] of [['concurrency',1,1024],['max_sessions',1,4096],['expected_status',200,299],['size',j.selection==='auto'?1:0,64],['probe_batch_size',j.selection==='auto'?Number(j.size):1,256],['probe_concurrency',1,16],['max_response_bytes',1,8388608]] as [string,number,number][]) {
        const n=Number(j[key]); if (!Number.isInteger(n)||n<min||n>max) return fail(i,key,tr('请输入 {min}–{max} 范围内的整数。',{min,max}));
      }
      for (const [key,min,max] of [['timeout',100,120000],['session_ttl',60000,604800000],['refresh_interval',60000,86400000]] as [string,number,number][]) {
        const n=durationMS(String(j[key])); if (!Number.isFinite(n)||n<min||n>max) return fail(i,key,tr('时长无效或超出范围，请使用 ms / s / m / h。'));
      }
      if (j.selection==='auto' || j.target_url) {
        try { const u=new URL(j.target_url); if (!['http:','https:'].includes(u.protocol)||u.username||u.password||u.hash||j.target_url.length>4096) throw new Error(); }
        catch { return fail(i,'target_url',tr('请输入不含凭据和片段的 HTTP(S) 目标 URL。')); }
      }
      if (new TextEncoder().encode(j.body_contains).length>4096) return fail(i,'body_contains',tr('响应文本不能超过 4096 字节。'));
    }
    return true;
  }
  private async click(event: Event): Promise<void> {
    const target=event.target as Element;
    if (target.closest('[data-add-job]')) {
      this.jobs=this.serialize(); if (this.jobs.length>=64) return;
      let n=this.jobs.length+1; while(this.jobs.some(j=>j.name===`job-${n}`)) n++;
      const endpoint=window.proxyFleetEndpoints?.serialize().find(e=>e.enabled&&e.profile&&!this.jobs.some(j=>j.endpoint===e.name));
      this.jobs.push({...defaults,name:`job-${n}`,profile:endpoint?.profile||window.proxyFleetProfiles?.serialize()[0]?.name||'',endpoint:endpoint?.name||''});
      this.render(); this.mount.querySelector<HTMLInputElement>('[data-job-row]:last-child [data-job-field="name"]')?.focus();
    }
    const remove=target.closest<HTMLButtonElement>('[data-remove-job]');
    if (remove) {
      const index=Number(remove.dataset.removeJob); remove.disabled=true;
      const yes=await window.requestConfirmation?.(tr('删除任务'),tr('删除配置后入口恢复普通 Profile 路由。请先停止使用该任务的客户端；已有会话记录保留，可在任务页面释放。'),tr('删除'));
      if (yes) { this.jobs=this.serialize().filter((_,i)=>i!==index); this.render(); } else remove.disabled=false;
    }
    this.mount.dispatchEvent(new CustomEvent('jobs-draft-change',{bubbles:true}));
  }
}

class JobsRuntime {
  private active=false;
  private generation=0;
  private accessRevision=0;
  private readAbort?: AbortController;
  private sessionAbort?: AbortController;
  private statusAbort?: AbortController;
  private statusTimer?: number;
  private statusRetryDelay=2000;
  private accessOperation?: Promise<Access>;
  private jobs: JobStatus[]=[];
  private configs: JobConfig[]=[];
  private page=1;
  private pages=0;
  private timer?: number;
  private pending=new Set<string>();
  private proxyURL='';
  constructor(private mount: HTMLElement) {
    mount.innerHTML=`<div class="job-toolbar"><h1>抓取任务</h1><div class="job-actions"><button type="button" class="btn" data-configure-jobs>${icon('settings')}<span>配置任务</span></button><button type="button" class="btn" data-reload-jobs>${icon('refresh')}<span>刷新状态</span></button></div></div>
      <p class="field-help" data-jobs-updated role="status"></p><div class="job-error" data-runtime-error role="alert"></div><div data-job-status-list></div>
      <section class="panel job-access"><div class="panel-header"><h2 class="panel-title">任务访问助手</h2></div><form data-job-access-form class="job-section-body">
      <div class="job-grid"><div class="form-group"><label for="jobAccessName">任务</label><select id="jobAccessName" class="setting-input" data-access-job></select></div>
      <div class="form-group"><label for="jobAccessSession">会话 ID</label><input id="jobAccessSession" class="setting-input" data-access-session placeholder="account-a" maxlength="128" autocomplete="off"><div class="field-help">同一逻辑会话复用同一 ID；不同 ID 可能共享节点。</div></div>
      <div class="form-group"><label for="jobAccessProtocol">代理协议</label><select id="jobAccessProtocol" class="setting-input" data-access-protocol><option value="http">HTTP / HTTPS CONNECT</option><option value="socks5h">SOCKS5</option></select></div></div>
      <div class="job-actions"><button type="submit" class="btn btn-primary" data-acquire-job>获取入口 / 创建会话</button></div>
      <div class="job-access-result" data-access-result role="status"></div><div class="job-error" data-access-error role="alert"></div>
      <div class="form-group" data-access-output hidden><label for="jobAccessURL">代理 URI</label><div class="copy-row"><input id="jobAccessURL" type="password" class="setting-input tt-mono" readonly><button type="button" class="btn" data-reveal-job-url aria-pressed="false">显示</button><button type="button" class="btn" data-copy-job-url>${icon('copy')}<span>复制</span></button></div><div class="field-help">地址包含代理凭据。并发和请求超时由爬虫执行；已有隧道不会因会话到期而自动关闭。</div></div></form></section>
      <section class="panel job-sessions"><div class="panel-header"><h2 class="panel-title">会话绑定</h2><button type="button" class="btn btn-sm" data-reload-sessions>刷新会话</button></div>
      <div class="job-section-body"><div class="job-grid job-session-filters"><div class="form-group"><label for="jobSessionSearch">搜索会话 / 节点</label><input id="jobSessionSearch" class="setting-input" data-session-search type="search"></div>
      <div class="form-group"><label for="jobSessionFilter">任务筛选</label><select id="jobSessionFilter" class="setting-input" data-session-job><option value="">全部任务</option></select></div>
      <div class="form-group"><label for="jobSessionState">会话状态</label><select id="jobSessionState" class="setting-input" data-session-state><option value="">全部状态</option>${['ready','paused','expired','policy_changed','removed','unavailable'].map(s=>`<option value="${s}">${stateLabel(s)}</option>`).join('')}</select></div></div>
      <p class="field-help">查看列表不会创建或续期会话。释放前请关闭客户端连接并停止使用该 ID。</p><div class="job-error" data-session-error role="alert"></div><div data-session-list></div>
      <div class="job-pagination"><span data-session-count role="status"></span><button type="button" class="btn btn-sm" data-session-prev>上一页</button><label for="jobSessionPage">页码</label><input id="jobSessionPage" type="number" min="1" value="1" class="setting-input" data-session-page><button type="button" class="btn btn-sm" data-session-go>跳转</button><button type="button" class="btn btn-sm" data-session-next>下一页</button></div></div></section>`;
    mount.addEventListener('click', e=>{void this.click(e);});
    mount.querySelector<HTMLFormElement>('[data-job-access-form]')!.addEventListener('submit',e=>{e.preventDefault();void this.acquire();});
    mount.addEventListener('change',e=>this.change(e));
    mount.querySelector('[data-access-session]')!.addEventListener('input',()=>this.clearAccess());
    mount.querySelector('[data-session-search]')!.addEventListener('input',()=>{window.clearTimeout(this.timer);this.timer=window.setTimeout(()=>{this.page=1;void this.loadSessions();},250);});
    mount.querySelector('[data-session-page]')!.addEventListener('keydown',e=>{if ((e as KeyboardEvent).key==='Enter'){e.preventDefault();this.jumpPage();}});
    translateTree(mount);
  }
  setActive(active: boolean): void {
    this.active=active; this.generation++;
    this.stopStatusPolling();
    this.readAbort?.abort(); this.sessionAbort?.abort(); window.clearTimeout(this.timer); this.clearAccess();
    if(active) void this.reload();
    else { this.jobs=[]; this.configs=[]; this.mount.querySelector<HTMLElement>('[data-session-list]')!.innerHTML=''; }
  }
  private value(selector:string): string { return this.mount.querySelector<HTMLInputElement|HTMLSelectElement>(selector)!.value; }
  private error(selector:string, error:unknown): void { this.mount.querySelector<HTMLElement>(selector)!.textContent=error ? localizedAPIMessage(error instanceof Error?error.message:error,'任务操作失败') : ''; }
  private async api<T>(path:string, options:RequestInit={}):Promise<T> {
    const response=await fetch(path,{cache:'no-store',...options});
    if(response.status===401) {this.clearAccess();document.getElementById('loginOverlay')?.classList.add('show');}
    try {return await readJSON<T>(response);}
    catch(error) {throw new JobAPIError(error instanceof Error?error.message:String(error),response.status);}
  }
  async reload():Promise<void> {
    this.stopStatusPolling();
    this.readAbort?.abort(); const abort=new AbortController();this.readAbort=abort;const generation=++this.generation;
    this.clearAccess();
    this.error('[data-runtime-error]','');
    this.mount.querySelector<HTMLElement>('[data-jobs-updated]')!.textContent=tr('正在读取任务状态…');
    try {
      const [runtime,settings]=await Promise.all([this.api<{jobs:JobStatus[]}>('/api/jobs',{signal:abort.signal}),this.api<{jobs:JobConfig[],mode:string}>('/api/settings',{signal:abort.signal})]);
      if(!this.active||generation!==this.generation)return;
      const jobs=readJobStatuses(runtime);
      if(!['pool','hybrid','multi-port'].includes(settings.mode)||(settings.jobs!=null&&!Array.isArray(settings.jobs)))throw new Error(tr('服务器响应格式无效，请重试。'));
      this.jobs=jobs;this.configs=settings.jobs||[];this.renderJobs();
      for(const selector of ['[data-access-job]','[data-session-job]']) {
        const select=this.mount.querySelector<HTMLSelectElement>(selector)!; const old=select.value;
        select.innerHTML=(selector==='[data-session-job]'?`<option value="">${tr('全部任务')}</option>`:'')+this.jobs.map(j=>`<option value="${esc(j.name)}">${esc(j.name)}</option>`).join('');
        if([...select.options].some(o=>o.value===old))select.value=old;
      }
      this.accessControls();
      this.mount.querySelector<HTMLElement>('[data-jobs-updated]')!.textContent=tr('状态更新于 {time}',{time:new Date().toLocaleTimeString()});
      this.scheduleStatusPoll();
      await this.loadSessions();
    } catch(error) { if(!abort.signal.aborted&&generation===this.generation){this.error('[data-runtime-error]',error);this.mount.querySelector<HTMLElement>('[data-jobs-updated]')!.textContent=tr('读取失败，请重试；已有内容可能过期。');} }
  }
  private stopStatusPolling():void {
    window.clearTimeout(this.statusTimer);this.statusAbort?.abort();
    this.statusRetryDelay=2000;
  }
  private scheduleStatusPoll(delay=2000):void {
    window.clearTimeout(this.statusTimer);
    if(this.active&&this.jobs.some(j=>j.refreshing))this.statusTimer=window.setTimeout(()=>{void this.pollStatus();},delay);
  }
  private async pollStatus():Promise<void> {
    if(!this.active)return;
    const abort=new AbortController(),generation=this.generation;this.statusAbort=abort;
    try {
      const data=await this.api<{jobs:JobStatus[]}>('/api/jobs',{signal:abort.signal});
      if(abort.signal.aborted||!this.active||generation!==this.generation)return;
      const jobs=readJobStatuses(data);
      if(jobs.length!==this.jobs.length||jobs.some((job,i)=>job.name!==this.jobs[i].name||job.mode!==this.jobs[i].mode||job.selection!==this.jobs[i].selection)) {await this.reload();return;}
      this.jobs=jobs;this.renderJobs();
      this.statusRetryDelay=2000;this.error('[data-runtime-error]','');
      this.mount.querySelector<HTMLElement>('[data-jobs-updated]')!.textContent=tr('状态更新于 {time}',{time:new Date().toLocaleTimeString()});
      this.scheduleStatusPoll();
    } catch(error) {
      if(abort.signal.aborted||!this.active||generation!==this.generation)return;
      this.error('[data-runtime-error]',error);
      const retry=!(error instanceof JobAPIError)||(error.status>=200&&error.status<300)||error.status>=500||error.status===408||error.status===429;
      this.mount.querySelector<HTMLElement>('[data-jobs-updated]')!.textContent=tr(retry?'状态更新暂时失败，正在重试；已有内容可能过期。':'读取失败，请重试；已有内容可能过期。');
      if(retry){this.scheduleStatusPoll(this.statusRetryDelay);this.statusRetryDelay=Math.min(this.statusRetryDelay*2,30000);}
    }
  }
  private renderJobs():void {
    const focused=document.activeElement as HTMLElement|null;
    const focusedJob=focused?.closest<HTMLElement>('[data-status-job]')?.dataset.statusJob;
    const focusControl=focused?.matches('summary')?'summary':focused?.matches('[data-refresh-job]')?'[data-refresh-job]':'';
    const expanded=new Set([...this.mount.querySelectorAll<HTMLElement>('[data-status-job]')].filter(card=>card.querySelector('details')?.open).map(card=>card.dataset.statusJob));
    this.mount.querySelector<HTMLElement>('[data-job-status-list]')!.innerHTML=this.jobs.length?this.jobs.map(j=>{
      const cfg=this.configs.find(c=>c.name===j.name);
      return `<article class="panel job-status-card" data-status-job="${esc(j.name)}"><div class="panel-header"><h2 class="panel-title">${esc(j.name)}</h2><div class="job-actions"><span>${esc(j.mode)} · ${esc(j.selection)}</span>${j.selection==='auto'?`<button type="button" class="btn btn-sm" data-refresh-job="${esc(j.name)}" ${j.refreshing||this.pending.has(`refresh:${j.name}`)?'disabled':''}>${j.refreshing?tr('测速中…'):tr('立即测速')}</button>`:''}</div></div>
        <div class="job-section-body"><p class="job-summary">${esc(tr('候选 {candidates} · 已测 {measured} · 可选 {selected}',{candidates:j.candidates,measured:j.measured,selected:j.selected.length}))}</p><p class="field-help">Profile: ${esc(cfg?.profile||'—')} · Endpoint: ${esc(cfg?.endpoint||tr('独立节点端口'))}</p>
        ${j.selected.length?`<details ${expanded.has(j.name)?'open':''}><summary>${tr('查看选中节点')}</summary><ul class="job-node-list">${j.selected.map(n=>`<li><span class="tt-mono">${esc(n.name||n.node)}</span>${n.duration_ms!==undefined?`<span>${n.duration_ms.toFixed(1)} ms · ${((n.success_rate||0)*100).toFixed(0)}%</span>`:''}</li>`).join('')}</ul></details>`:`<p class="job-empty">${tr(j.selection==='auto'?'暂无通过测速的可用节点，请检查目标或执行测速。':'Profile 内暂无可用节点，请检查匹配规则和健康状态。')}</p>`}</div></article>`;
    }).join(''):'<div class="panel job-empty">尚未配置任务。点击“配置任务”开始。</div>';
    translateTree(this.mount.querySelector('[data-job-status-list]')!);
    if(focusedJob&&focusControl)[...this.mount.querySelectorAll<HTMLElement>('[data-status-job]')].find(card=>card.dataset.statusJob===focusedJob)?.querySelector<HTMLElement>(focusControl)?.focus({preventScroll:true});
  }
  private clearAccess():void {
    this.accessRevision++;
    this.proxyURL='';this.mount.querySelector<HTMLInputElement>('#jobAccessURL')!.value='';
    this.mount.querySelector<HTMLInputElement>('#jobAccessURL')!.type='password';
    this.mount.querySelector<HTMLElement>('[data-access-output]')!.hidden=true;
    this.mount.querySelector<HTMLElement>('[data-access-result]')!.textContent='';this.error('[data-access-error]','');
    const reveal=this.mount.querySelector<HTMLButtonElement>('[data-reveal-job-url]')!;reveal.textContent=tr('显示');reveal.setAttribute('aria-pressed','false');
  }
  private accessControls():void {
    const job=this.jobs.find(j=>j.name===this.value('[data-access-job]'));
    const input=this.mount.querySelector<HTMLInputElement>('[data-access-session]')!;input.disabled=job?.mode!=='pinned';input.required=job?.mode==='pinned';
    this.mount.querySelector<HTMLButtonElement>('[data-acquire-job]')!.disabled=!job||this.accessBlocked();
  }
  private accessBlocked():boolean {return this.pending.has('access')||[...this.pending].some(key=>key.startsWith('release:'));}
  private async acquire():Promise<void> {
    if(this.accessBlocked())return;
    const name=this.value('[data-access-job]'),job=this.jobs.find(j=>j.name===name);if(!job)return;
    const session=job.mode==='pinned'?this.value('[data-access-session]').trim():'';
    const cfg=this.configs.find(c=>c.name===name);
    if(job.mode==='pinned'&&(!session||(cfg?.endpoint&&!/^[A-Za-z0-9_-]{1,128}$/.test(session)))) {this.error('[data-access-error]',tr('会话 ID 需为 1–128 位字母、数字、下划线或连字符。'));return;}
    this.clearAccess();this.pending.add('access');this.accessControls();const generation=this.generation,accessRevision=this.accessRevision;
    try {
      this.accessOperation=this.api<Access>(`/api/jobs/${encodeURIComponent(name)}/acquire`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({session})});
      const result=await this.accessOperation;
      if(!this.active||generation!==this.generation||accessRevision!==this.accessRevision||name!==this.value('[data-access-job]')||(job.mode==='pinned'&&session!==this.value('[data-access-session]').trim()))return;
      this.mount.querySelector<HTMLElement>('[data-access-result]')!.textContent=`${stateLabel(result.state)} · ${result.node||name}${result.session?` · ${tr('到期时间')}: ${date(result.expires_at)}`:''} · ${tr('请求并发数')}: ${result.concurrency} · ${tr('请求超时')}: ${result.timeout}`;
      if(result.state==='ready'&&result.proxy_url){this.proxyURL=result.proxy_url;this.showURL();}
      else this.error('[data-access-error]',tr('此会话不可用。请先停止客户端，再显式释放或使用新的逻辑会话。'));
      await this.loadSessions();
    } catch(error){if(this.active&&generation===this.generation&&accessRevision===this.accessRevision&&name===this.value('[data-access-job]')&&(job.mode!=='pinned'||session===this.value('[data-access-session]').trim()))this.error('[data-access-error]',error);}
    finally {this.accessOperation=undefined;this.pending.delete('access');this.accessControls();}
  }
  private showURL():void {this.mount.querySelector<HTMLInputElement>('#jobAccessURL')!.value=this.proxyURL.replace(/^http:/,this.value('[data-access-protocol]')+':');this.mount.querySelector<HTMLElement>('[data-access-output]')!.hidden=!this.proxyURL;}
  private async loadSessions():Promise<void> {
    if(!this.active)return;this.sessionAbort?.abort();const abort=new AbortController();this.sessionAbort=abort;
    const query=new URLSearchParams({page:String(this.page),page_size:'20',job:this.value('[data-session-job]'),state:this.value('[data-session-state]'),q:this.value('[data-session-search]')});
    this.error('[data-session-error]','');
    try {
      const data=await this.api<SessionPage>('/api/jobs/sessions?'+query,{signal:abort.signal});if(abort.signal.aborted||!this.active)return;
      this.page=data.pagination.page;this.pages=data.pagination.total_pages;
      this.mount.querySelector<HTMLElement>('[data-session-list]')!.innerHTML=data.sessions.length?`<div class="job-table-wrap"><table class="job-session-table"><thead><tr><th>任务 / 会话</th><th>节点</th><th>状态 / 到期时间</th><th>操作</th></tr></thead><tbody>${data.sessions.map(s=>`<tr><td><strong>${esc(s.job)}</strong><div class="tt-mono">${esc(s.session)}</div></td><td class="tt-mono">${esc(s.node)}</td><td>${stateLabel(s.state)}<div class="field-help">${esc(date(s.expires_at))}</div></td><td><button type="button" class="btn btn-sm btn-danger" data-release-job="${esc(s.job)}" data-release-session="${esc(s.session)}">释放会话</button></td></tr>`).join('')}</tbody></table></div>`:'<p class="job-empty">没有匹配的会话。</p>';
      this.mount.querySelector<HTMLElement>('[data-session-count]')!.textContent=tr('共 {count} 条 · {page} / {pages} 页',{count:data.pagination.total_items,page:this.page,pages:Math.max(1,this.pages)});
      const input=this.mount.querySelector<HTMLInputElement>('[data-session-page]')!;input.value=String(this.page);input.max=String(Math.max(1,this.pages));
      this.mount.querySelector<HTMLButtonElement>('[data-session-prev]')!.disabled=this.page<=1;
      this.mount.querySelector<HTMLButtonElement>('[data-session-next]')!.disabled=this.page>=this.pages;
      translateTree(this.mount.querySelector('[data-session-list]')!);
    }catch(error){if(!abort.signal.aborted)this.error('[data-session-error]',error);}
  }
  private change(event:Event):void {
    const target=event.target as Element;
    if(target.matches('[data-access-job]')){this.clearAccess();this.accessControls();}
    if(target.matches('[data-access-protocol]'))this.showURL();
    if(target.matches('[data-session-job],[data-session-state]')){this.page=1;void this.loadSessions();}
  }
  private jumpPage():void {const page=Number(this.value('[data-session-page]'));if(Number.isInteger(page)&&page>=1&&page<=Math.max(1,this.pages)){this.page=page;void this.loadSessions();}}
  private async click(event:Event):Promise<void> {
    const target=event.target as Element;
    if(target.closest('[data-configure-jobs]')){await window.switchTab?.('settings');document.getElementById('jobSettingsMount')?.scrollIntoView({block:'start'});document.querySelector<HTMLButtonElement>('[data-add-job]')?.focus();}
    if(target.closest('[data-reload-jobs]'))void this.reload();
    if(target.closest('[data-reload-sessions]'))void this.loadSessions();
    if(target.closest('[data-session-prev]')&&this.page>1){this.page--;void this.loadSessions();}
    if(target.closest('[data-session-next]')&&this.page<this.pages){this.page++;void this.loadSessions();}
    if(target.closest('[data-session-go]'))this.jumpPage();
    if(target.closest('[data-copy-job-url]')){try{await copyText(this.value('#jobAccessURL'));window.showToast?.(tr('已复制'));}catch(error){this.error('[data-access-error]',error);}}
    if(target.closest('[data-reveal-job-url]')){const input=this.mount.querySelector<HTMLInputElement>('#jobAccessURL')!;const visible=input.type==='password';input.type=visible?'text':'password';const btn=this.mount.querySelector<HTMLButtonElement>('[data-reveal-job-url]')!;btn.textContent=tr(visible?'隐藏':'显示');btn.setAttribute('aria-pressed',String(visible));}
    const refresh=target.closest<HTMLButtonElement>('[data-refresh-job]');
    if(refresh){const name=refresh.dataset.refreshJob!,key=`refresh:${name}`;if(this.pending.has(key))return;this.pending.add(key);refresh.disabled=true;try{await this.api(`/api/jobs/${encodeURIComponent(name)}/refresh`,{method:'POST'});if(this.active)await this.reload();}catch(error){this.error('[data-runtime-error]',error);}finally{this.pending.delete(key);if(this.active)this.renderJobs();}}
    const release=target.closest<HTMLButtonElement>('[data-release-job]');
    if(release){const job=release.dataset.releaseJob!,session=release.dataset.releaseSession!,key=`release:${job}:${session}`;if(this.pending.has(key))return;this.pending.add(key);release.disabled=true;this.accessControls();
      try{const confirmed=await window.requestConfirmation?.(tr('释放会话'),tr('释放 {session} 后，同一 ID 再次连接可能分配到其他节点。请先停止客户端并关闭旧连接。',{session}),tr('释放'));if(!confirmed)return;
        this.clearAccess();await this.accessOperation;
        await this.api(`/api/jobs/${encodeURIComponent(job)}/release`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({session})});this.clearAccess();if(this.active)await this.loadSessions();
      }catch(error){this.error('[data-session-error]',error);}finally{this.pending.delete(key);release.disabled=false;this.accessControls();}
    }
  }
}

export function installJobsModule(editor:HTMLElement|null,runtime:HTMLElement|null):void {
  window.proxyFleetI18n?.registerMessages?.(jobMessages);
  if(editor)window.proxyFleetJobs=new JobsEditor(editor);
  if(runtime)window.proxyFleetJobRuntime=new JobsRuntime(runtime);
}
