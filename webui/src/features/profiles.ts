import { copyText, readJSON } from '../api';
import { localizedAPIMessage, tr, translateTree } from '../i18n';

export interface ProfileConfig {
  name: string;
  regions: string[];
  name_regex: string;
  tag_rules: {
    any: string[];
    must: string[];
    must_not: string[];
  };
  protocols: string[];
  sources: string[];
  min_quality: number;
}

interface ProfilePreviewSample {
  name: string;
  region: string;
  protocol: string;
  source: string;
  quality: number;
  matched: boolean;
  reason?: string;
  rule_group?: string;
  rule_index?: number;
  rule?: string;
}

interface ProfilePreview {
  total: number;
  matched: number;
  excluded: Record<string, number>;
  matched_samples: ProfilePreviewSample[];
  excluded_samples: ProfilePreviewSample[];
}

interface AccessInfo {
  mode: string;
  host: string;
  port: number;
  username: string;
  password: string;
  profiles: ProfileConfig[];
  endpoints?: AccessEndpoint[];
}

interface AccessEndpoint {
  name: string;
  enabled: boolean;
  host: string;
  port: number;
  username: string;
  password: string;
  profile: string;
  status: string;
  message?: string;
}

interface ProfilesModule {
  load(profiles: ProfileConfig[]): void;
  serialize(): ProfileConfig[];
  validate(): boolean;
}

declare global {
  interface Window {
    proxyFleetProfiles?: ProfilesModule;
    showToast?: (message: string, type?: string) => void;
  }
}

const splitList = (value: string): string[] => value.split(',').map(item => item.trim().toLowerCase()).filter(Boolean);
const splitRules = (value: string): string[] => value.split(/\r?\n/).map(item => item.trim()).filter(Boolean);
const escapeHTML = (value: unknown): string => String(value ?? '').replace(/[&<>"']/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char] || char));

class ProfileController implements ProfilesModule {
  private profiles: ProfileConfig[] = [];
  private access: AccessInfo | null = null;
  private previewTimer: number | undefined;

  constructor(private readonly mount: HTMLElement) {
    this.renderShell();
    this.mount.addEventListener('click', event => this.handleClick(event));
    this.mount.addEventListener('input', event => this.handleFormChange(event));
    this.mount.addEventListener('change', event => this.handleFormChange(event));
    void this.loadAccess();
  }

  load(profiles: ProfileConfig[]): void {
    this.profiles = Array.isArray(profiles) ? profiles.map(profile => ({
      name: String(profile.name || ''), regions: [...(profile.regions || [])],
      name_regex: '', tag_rules: {
        any: [...(profile.tag_rules?.any || [])],
        must: [...(profile.tag_rules?.must || (profile.name_regex ? [profile.name_regex] : []))],
        must_not: [...(profile.tag_rules?.must_not || [])],
      }, protocols: [...(profile.protocols || [])],
      sources: [...(profile.sources || [])], min_quality: Number(profile.min_quality) || 0,
    })) : [];
    this.renderRows();
    void this.loadAccess();
  }

  serialize(): ProfileConfig[] {
    return [...this.mount.querySelectorAll<HTMLElement>('[data-profile-row]')].map(row => this.serializeRow(row));
  }

  private serializeRow(row: HTMLElement): ProfileConfig {
    return {
      name: this.value(row, 'name').toLowerCase(),
      regions: splitList(this.value(row, 'regions')),
      name_regex: '',
      tag_rules: {
        any: splitRules(this.value(row, 'tag_any')),
        must: splitRules(this.value(row, 'tag_must')),
        must_not: splitRules(this.value(row, 'tag_must_not')),
      },
      protocols: splitList(this.value(row, 'protocols')),
      sources: splitList(this.value(row, 'sources')),
      min_quality: Number(this.value(row, 'min_quality')) || 0,
    };
  }

  validate(): boolean {
    const profiles = this.serialize();
    const names = new Set<string>();
    for (const profile of profiles) {
      if (!/^[a-z0-9][a-z0-9._-]{0,31}$/.test(profile.name)) return this.invalid('Profile 名称需为 1–32 位小写字母、数字、点、下划线或连字符。');
      if (names.has(profile.name)) return this.invalid(`Profile 名称重复：${profile.name}`);
      names.add(profile.name);
      if (profile.min_quality < 0 || profile.min_quality > 100) return this.invalid('最低质量分必须在 0 到 100 之间。');
      for (const [group, rules] of Object.entries(profile.tag_rules)) {
        if (rules.length > 64) return this.invalid(`Profile ${profile.name} 的 ${group} 规则不能超过 64 条。`);
        for (let index = 0; index < rules.length; index += 1) {
          try { new RegExp(rules[index]); } catch { return this.invalid(`Profile ${profile.name} 的 ${group} 第 ${index + 1} 条正则无效。`); }
        }
      }
    }
    return true;
  }

  private invalid(message: string): false {
    window.showToast?.(tr(message), 'error');
    return false;
  }

  private value(row: HTMLElement, field: string): string {
    return (row.querySelector<HTMLInputElement>(`[data-field="${field}"]`)?.value || '').trim();
  }

  private renderShell(): void {
    this.mount.innerHTML = `
      <section class="panel profile-panel" style="margin-bottom:16px">
        <div class="panel-header"><div><div class="panel-title">Named Profiles</div><div class="field-help">组合地域、协议、来源、质量与节点名称规则；静态成员视图在池创建时预计算。</div></div><button type="button" class="btn btn-sm" data-add-profile><svg class="icon"><use href="#i-plus"></use></svg><span>添加 Profile</span></button></div>
        <div class="profile-list" data-profile-list></div>
      </section>
      <section class="panel access-panel" style="margin-bottom:16px">
        <div class="panel-header"><div><div class="panel-title">访问助手 (Access Assistant)</div><div class="field-help">生成 HTTP / SOCKS5 URI 与 curl 命令；复制内容来自当前运行配置。</div></div><span class="badge badge-offline" data-access-status>读取中</span></div>
        <div class="access-grid">
          <div class="form-group"><label>Endpoint</label><select class="setting-input" data-access-endpoint><option value="">默认入口</option></select></div>
          <div class="form-group"><label>协议</label><select class="setting-input" data-access-scheme><option value="http">HTTP</option><option value="socks5">SOCKS5</option></select></div>
          <div class="form-group"><label>Profile</label><select class="setting-input" data-access-profile><option value="">全部节点</option></select></div>
          <div class="form-group access-output"><label>代理 URI</label><div class="copy-row"><input class="setting-input tt-mono" data-access-uri readonly><button type="button" class="btn btn-sm" data-copy="uri"><svg class="icon"><use href="#i-copy"></use></svg><span>复制</span></button></div></div>
          <div class="form-group access-output"><label>curl</label><div class="copy-row"><input class="setting-input tt-mono" data-access-curl readonly><button type="button" class="btn btn-sm" data-copy="curl"><svg class="icon"><use href="#i-copy"></use></svg><span>复制</span></button></div></div>
        </div>
        <div class="settings-help" data-access-help>正在读取统一入口…</div>
      </section>`;
    this.renderRows();
    translateTree(this.mount);
  }

  private renderRows(): void {
    const list = this.mount.querySelector<HTMLElement>('[data-profile-list]');
    if (!list) return;
    if (!this.profiles.length) {
      list.innerHTML = '<div class="profile-empty">尚未配置 Profile。所有连接继续使用全局池。</div>';
    } else {
      list.innerHTML = this.profiles.map((profile, index) => {
        const id = `profile-${index}`;
        return `
        <div class="profile-row" data-profile-row>
          <div class="profile-row-head"><div class="profile-row-title"><strong>Profile ${index + 1}</strong><span class="badge badge-offline" data-profile-preview-status>尚未预览</span></div><div class="profile-row-actions"><button type="button" class="btn btn-sm" data-preview-profile><span>刷新预览</span></button><button type="button" class="btn btn-sm btn-danger" data-remove-profile="${index}"><svg class="icon"><use href="#i-trash"></use></svg><span>删除</span></button></div></div>
          <div class="profile-fields">
            <div class="form-group"><label for="${id}-name">名称</label><input id="${id}-name" class="setting-input" data-field="name" value="${escapeHTML(profile.name)}" placeholder="hk-fast"></div>
            <div class="form-group"><label for="${id}-regions">地域（逗号分隔）</label><input id="${id}-regions" class="setting-input" data-field="regions" value="${escapeHTML(profile.regions.join(', '))}" placeholder="hk, sg"></div>
            <div class="form-group"><label for="${id}-protocols">协议</label><input id="${id}-protocols" class="setting-input" data-field="protocols" value="${escapeHTML(profile.protocols.join(', '))}" placeholder="vless, hysteria2"></div>
            <div class="form-group"><label for="${id}-sources">来源</label><input id="${id}-sources" class="setting-input" data-field="sources" value="${escapeHTML(profile.sources.join(', '))}" placeholder="subscription"></div>
            <div class="form-group"><label for="${id}-quality">最低质量分</label><input id="${id}-quality" type="number" min="0" max="100" class="setting-input" data-field="min_quality" value="${profile.min_quality}"></div>
          </div>
          <div class="profile-rule-grid">
            <div class="form-group"><label for="${id}-any">ANY · 至少命中一条</label><textarea id="${id}-any" class="setting-input tt-mono" data-field="tag_any" rows="3" placeholder="HK|Hong Kong&#10;JP|Japan">${escapeHTML(profile.tag_rules.any.join('\n'))}</textarea><div class="field-help">留空表示不限制；每行一条正则。</div></div>
            <div class="form-group"><label for="${id}-must">MUST · 每条都要命中</label><textarea id="${id}-must" class="setting-input tt-mono" data-field="tag_must" rows="3" placeholder="Premium|Dedicated">${escapeHTML(profile.tag_rules.must.join('\n'))}</textarea><div class="field-help">适合叠加套餐、线路或用途条件。</div></div>
            <div class="form-group"><label for="${id}-must-not">MUST NOT · 任一命中即排除</label><textarea id="${id}-must-not" class="setting-input tt-mono" data-field="tag_must_not" rows="3" placeholder="Expired|Traffic">${escapeHTML(profile.tag_rules.must_not.join('\n'))}</textarea><div class="field-help">优先排除到期、流量提示或不需要的节点。</div></div>
          </div>
          <div class="profile-preview" data-profile-preview><div class="profile-preview-empty">正在准备匹配预览…</div></div>
        </div>`;
      }).join('');
    }
    translateTree(list);
    this.renderProfileOptions();
    window.proxyFleetEndpoints?.setProfiles(this.serialize());
    queueMicrotask(() => this.mount.querySelectorAll<HTMLElement>('[data-profile-row]').forEach(row => { void this.loadPreview(row); }));
  }

  private async loadAccess(): Promise<void> {
    const status = this.mount.querySelector<HTMLElement>('[data-access-status]');
    try {
      const response = await fetch('/api/access');
      this.access = await readJSON<AccessInfo>(response);
      if (status) { status.textContent = '就绪'; status.className = 'badge badge-success'; }
      this.renderEndpointOptions();
      this.renderProfileOptions();
      this.renderAccessOutput();
      translateTree(this.mount);
    } catch (error) {
      if (status) { status.textContent = '读取失败'; status.className = 'badge badge-error'; }
      const help = this.mount.querySelector<HTMLElement>('[data-access-help]');
      if (help) help.textContent = localizedAPIMessage(error instanceof Error ? error.message : '', '无法读取访问配置');
      translateTree(this.mount);
    }
  }

  private renderProfileOptions(): void {
    const select = this.mount.querySelector<HTMLSelectElement>('[data-access-profile]');
    if (!select) return;
    const selected = select.value;
    const profiles = this.serialize();
    select.innerHTML = '<option value="">全部节点</option>' + profiles.map(profile => `<option value="${escapeHTML(profile.name)}">${escapeHTML(profile.name)}</option>`).join('');
    if ([...select.options].some(option => option.value === selected)) select.value = selected;
    translateTree(select);
  }

  private renderEndpointOptions(): void {
    const select = this.mount.querySelector<HTMLSelectElement>('[data-access-endpoint]');
    if (!select || !this.access) return;
    const selected = select.value;
    const endpoints = this.access.endpoints?.length ? this.access.endpoints : [{
      name: 'default', enabled: true, host: this.access.host, port: this.access.port,
      username: this.access.username, password: this.access.password, profile: '', status: 'running',
    }];
    select.innerHTML = endpoints.map(endpoint => {
      const state = endpoint.enabled && endpoint.status === 'running' ? '' : ` · ${endpoint.status || 'waiting'}`;
      return `<option value="${escapeHTML(endpoint.name)}">${escapeHTML(endpoint.name + state)}</option>`;
    }).join('');
    if ([...select.options].some(option => option.value === selected)) select.value = selected;
  }

  private renderAccessOutput(): void {
    if (!this.access) return;
    const scheme = this.mount.querySelector<HTMLSelectElement>('[data-access-scheme]')?.value || 'http';
    const endpointName = this.mount.querySelector<HTMLSelectElement>('[data-access-endpoint]')?.value || '';
    const endpoints = this.access.endpoints?.length ? this.access.endpoints : [{
      name: 'default', enabled: true, host: this.access.host, port: this.access.port,
      username: this.access.username, password: this.access.password, profile: '', status: 'running',
    }];
    const endpoint = endpoints.find(item => item.name === endpointName) || endpoints[0];
    if (!endpoint) return;
    const profileSelect = this.mount.querySelector<HTMLSelectElement>('[data-access-profile]');
    if (endpoint.profile && profileSelect) profileSelect.value = endpoint.profile;
    if (profileSelect) profileSelect.disabled = Boolean(endpoint.profile);
    const profile = endpoint.profile || profileSelect?.value || '';
    const username = profile && !endpoint.profile && endpoint.username ? `${endpoint.username}@${profile}` : endpoint.username;
    const credentials = username ? `${encodeURIComponent(username)}:${encodeURIComponent(endpoint.password)}@` : '';
    const host = endpoint.host.includes(':') ? `[${endpoint.host}]` : endpoint.host;
    const uri = endpoint.port ? `${scheme}://${credentials}${host}:${endpoint.port}` : '';
    const curl = uri ? `curl --proxy '${uri}' https://api.ipify.org` : '';
    const uriField = this.mount.querySelector<HTMLInputElement>('[data-access-uri]');
    const curlField = this.mount.querySelector<HTMLInputElement>('[data-access-curl]');
    if (uriField) uriField.value = uri;
    if (curlField) curlField.value = curl;
    const help = this.mount.querySelector<HTMLElement>('[data-access-help]');
    if (help) {
      if (endpoint.profile) help.textContent = tr('Endpoint {endpoint} 已固定到 Profile {profile}，客户端无需修改用户名。', {endpoint: endpoint.name, profile: endpoint.profile});
      else if (endpoint.username) help.textContent = tr('可选择 Profile；访问用户名会自动生成为 base@profile。');
      else help.textContent = tr('该 Endpoint 未启用认证；如需客户端选择 Profile，请为入口配置用户名和密码，或将 Endpoint 固定到一个 Profile。');
      if (endpoint.status !== 'running') help.textContent += tr(' 当前状态：{status}{message}。', {status: endpoint.status, message: endpoint.message ? ` (${localizedAPIMessage(endpoint.message, '状态异常')})` : ''});
    }
  }

  private handleFormChange(event: Event): void {
    const row = (event.target as Element).closest<HTMLElement>('[data-profile-row]');
    if (row) {
      this.renderProfileOptions();
      window.proxyFleetEndpoints?.setProfiles(this.serialize());
      this.schedulePreview(row);
    }
    this.renderAccessOutput();
  }

  private handleClick(event: Event): void {
    const target = event.target as Element;
    if (target.closest('[data-add-profile]')) {
      this.profiles = this.serialize();
      this.profiles.push({name:'', regions:[], name_regex:'', tag_rules:{any:[], must:[], must_not:[]}, protocols:[], sources:[], min_quality:0});
      this.renderRows();
      this.mount.querySelector<HTMLInputElement>('[data-profile-row]:last-child [data-field="name"]')?.focus();
      return;
    }
    const remove = target.closest<HTMLElement>('[data-remove-profile]');
    if (remove) {
      const index = Number(remove.dataset.removeProfile);
      this.profiles = this.serialize().filter((_, itemIndex) => itemIndex !== index);
      this.renderRows();
      return;
    }
    const copy = target.closest<HTMLElement>('[data-copy]');
    if (copy) {
      const field = this.mount.querySelector<HTMLInputElement>(`[data-access-${copy.dataset.copy}]`);
      if (field?.value) void copyText(field.value).then(() => window.showToast?.(tr('已复制')));
    }
    const preview = target.closest<HTMLElement>('[data-preview-profile]');
    if (preview) {
      const row = preview.closest<HTMLElement>('[data-profile-row]');
      if (row) void this.loadPreview(row);
    }
  }

  private schedulePreview(row: HTMLElement): void {
    if (this.previewTimer !== undefined) window.clearTimeout(this.previewTimer);
    this.previewTimer = window.setTimeout(() => { void this.loadPreview(row); }, 350);
  }

  private async loadPreview(row: HTMLElement): Promise<void> {
    const status = row.querySelector<HTMLElement>('[data-profile-preview-status]');
    const output = row.querySelector<HTMLElement>('[data-profile-preview]');
    if (!status || !output) return;
    const sequence = String((Number(row.dataset.previewSequence) || 0) + 1);
    row.dataset.previewSequence = sequence;
    status.textContent = '计算中';
    status.className = 'badge badge-warning';
    output.innerHTML = '<div class="profile-preview-empty">正在按当前运行节点计算…</div>';
    translateTree(row);
    try {
      const response = await fetch('/api/profiles/preview', {
        method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify(this.serializeRow(row)),
      });
      const preview = await readJSON<ProfilePreview>(response);
      if (row.dataset.previewSequence !== sequence) return;
      status.textContent = tr('{matched}/{total} 命中', {matched: preview.matched, total: preview.total});
      status.className = preview.matched ? 'badge badge-healthy' : 'badge badge-warning';
      output.innerHTML = this.previewHTML(preview);
      translateTree(output);
    } catch (error) {
      if (row.dataset.previewSequence !== sequence) return;
      status.textContent = tr('预览失败');
      status.className = 'badge badge-error';
      output.innerHTML = `<div class="profile-preview-error" role="alert">${escapeHTML(localizedAPIMessage(error instanceof Error ? error.message : '', '无法计算匹配预览'))}</div>`;
    }
  }

  private previewHTML(preview: ProfilePreview): string {
    if (!preview.total) return `<div class="profile-preview-empty">${tr('当前运行配置中没有可预览节点。')}</div>`;
    const reasonLabels: Record<string,string> = {region:'地域', protocol:'协议', source:'来源', tag_rule:'名称规则', quality:'质量'};
    const reasons = Object.entries(preview.excluded || {}).filter(([, count]) => count > 0)
      .map(([reason, count]) => `<span class="profile-preview-chip">${tr('{reason} excluded: {count}', {reason: tr(reasonLabels[reason] || reason), count})}</span>`).join('');
    const samples = (items: ProfilePreviewSample[], kind: string) => items.length ? `<div class="profile-preview-samples"><strong>${tr(kind)}</strong>${items.map(item => `<span title="${escapeHTML([item.region, item.protocol, item.source, tr('质量 {quality}', {quality: item.quality.toFixed(0)}), item.rule ? `${item.rule_group}: ${item.rule}` : ''].filter(Boolean).join(' · '))}">${escapeHTML(item.name)}</span>`).join('')}</div>` : '';
    return `<div class="profile-preview-summary"><span><strong>${preview.matched}</strong> ${tr('命中')}</span><span><strong>${preview.total - preview.matched}</strong> ${tr('排除')}</span>${reasons}</div>${samples(preview.matched_samples || [], '命中样本')}${samples(preview.excluded_samples || [], '未命中样本')}`;
  }
}

export function installProfilesModule(mount: HTMLElement | null): void {
  if (!mount) return;
  window.proxyFleetProfiles = new ProfileController(mount);
}
