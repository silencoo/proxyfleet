import { copyText, readJSON } from '../api';

export interface ProfileConfig {
  name: string;
  regions: string[];
  name_regex: string;
  protocols: string[];
  sources: string[];
  min_quality: number;
}

interface AccessInfo {
  mode: string;
  host: string;
  port: number;
  username: string;
  password: string;
  profiles: ProfileConfig[];
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
const escapeHTML = (value: unknown): string => String(value ?? '').replace(/[&<>"']/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char] || char));

class ProfileController implements ProfilesModule {
  private profiles: ProfileConfig[] = [];
  private access: AccessInfo | null = null;

  constructor(private readonly mount: HTMLElement) {
    this.renderShell();
    this.mount.addEventListener('click', event => this.handleClick(event));
    this.mount.addEventListener('input', () => this.renderAccessOutput());
    this.mount.addEventListener('change', () => this.renderAccessOutput());
    void this.loadAccess();
  }

  load(profiles: ProfileConfig[]): void {
    this.profiles = Array.isArray(profiles) ? profiles.map(profile => ({
      name: String(profile.name || ''), regions: [...(profile.regions || [])],
      name_regex: String(profile.name_regex || ''), protocols: [...(profile.protocols || [])],
      sources: [...(profile.sources || [])], min_quality: Number(profile.min_quality) || 0,
    })) : [];
    this.renderRows();
    void this.loadAccess();
  }

  serialize(): ProfileConfig[] {
    return [...this.mount.querySelectorAll<HTMLElement>('[data-profile-row]')].map(row => ({
      name: this.value(row, 'name').toLowerCase(),
      regions: splitList(this.value(row, 'regions')),
      name_regex: this.value(row, 'name_regex'),
      protocols: splitList(this.value(row, 'protocols')),
      sources: splitList(this.value(row, 'sources')),
      min_quality: Number(this.value(row, 'min_quality')) || 0,
    }));
  }

  validate(): boolean {
    const profiles = this.serialize();
    const names = new Set<string>();
    for (const profile of profiles) {
      if (!/^[a-z0-9][a-z0-9._-]{0,31}$/.test(profile.name)) return this.invalid('Profile 名称需为 1–32 位小写字母、数字、点、下划线或连字符。');
      if (names.has(profile.name)) return this.invalid(`Profile 名称重复：${profile.name}`);
      names.add(profile.name);
      if (profile.min_quality < 0 || profile.min_quality > 100) return this.invalid('最低质量分必须在 0 到 100 之间。');
      try { if (profile.name_regex) new RegExp(profile.name_regex); } catch { return this.invalid(`Profile ${profile.name} 的名称正则无效。`); }
    }
    const user = (document.getElementById('settingListenerUser') as HTMLInputElement | null)?.value.trim() || '';
    const password = (document.getElementById('settingListenerPass') as HTMLInputElement | null)?.value || '';
    if (profiles.length && (!user || !password)) return this.invalid('启用 Named Profiles 前，请先设置统一入口用户名和密码。');
    return true;
  }

  private invalid(message: string): false {
    window.showToast?.(message, 'error');
    return false;
  }

  private value(row: HTMLElement, field: string): string {
    return (row.querySelector<HTMLInputElement>(`[data-field="${field}"]`)?.value || '').trim();
  }

  private renderShell(): void {
    this.mount.innerHTML = `
      <section class="panel profile-panel" style="margin-bottom:16px">
        <div class="panel-header"><div><div class="panel-title">Named Profiles</div><div class="field-help">通过统一入口用户名 base@profile 选择预过滤节点池；成员视图在池创建时预计算。</div></div><button type="button" class="btn btn-sm" data-add-profile><svg class="icon"><use href="#i-plus"></use></svg><span>添加 Profile</span></button></div>
        <div class="profile-list" data-profile-list></div>
      </section>
      <section class="panel access-panel" style="margin-bottom:16px">
        <div class="panel-header"><div><div class="panel-title">访问助手 (Access Assistant)</div><div class="field-help">生成 HTTP / SOCKS5 URI 与 curl 命令；复制内容来自当前运行配置。</div></div><span class="badge badge-offline" data-access-status>读取中</span></div>
        <div class="access-grid">
          <div class="form-group"><label>协议</label><select class="setting-input" data-access-scheme><option value="http">HTTP</option><option value="socks5">SOCKS5</option></select></div>
          <div class="form-group"><label>Profile</label><select class="setting-input" data-access-profile><option value="">全部节点</option></select></div>
          <div class="form-group access-output"><label>代理 URI</label><div class="copy-row"><input class="setting-input tt-mono" data-access-uri readonly><button type="button" class="btn btn-sm" data-copy="uri"><svg class="icon"><use href="#i-copy"></use></svg><span>复制</span></button></div></div>
          <div class="form-group access-output"><label>curl</label><div class="copy-row"><input class="setting-input tt-mono" data-access-curl readonly><button type="button" class="btn btn-sm" data-copy="curl"><svg class="icon"><use href="#i-copy"></use></svg><span>复制</span></button></div></div>
        </div>
        <div class="settings-help" data-access-help>正在读取统一入口…</div>
      </section>`;
    this.renderRows();
  }

  private renderRows(): void {
    const list = this.mount.querySelector<HTMLElement>('[data-profile-list]');
    if (!list) return;
    if (!this.profiles.length) {
      list.innerHTML = '<div class="profile-empty">尚未配置 Profile。所有连接继续使用全局池。</div>';
    } else {
      list.innerHTML = this.profiles.map((profile, index) => `
        <div class="profile-row" data-profile-row>
          <div class="profile-row-head"><strong>Profile ${index + 1}</strong><button type="button" class="btn btn-sm btn-danger" data-remove-profile="${index}"><svg class="icon"><use href="#i-trash"></use></svg><span>删除</span></button></div>
          <div class="profile-fields">
            <div class="form-group"><label>名称</label><input class="setting-input" data-field="name" value="${escapeHTML(profile.name)}" placeholder="hk-fast"></div>
            <div class="form-group"><label>地域（逗号分隔）</label><input class="setting-input" data-field="regions" value="${escapeHTML(profile.regions.join(', '))}" placeholder="hk, sg"></div>
            <div class="form-group"><label>协议</label><input class="setting-input" data-field="protocols" value="${escapeHTML(profile.protocols.join(', '))}" placeholder="vless, hysteria2"></div>
            <div class="form-group"><label>来源</label><input class="setting-input" data-field="sources" value="${escapeHTML(profile.sources.join(', '))}" placeholder="subscription"></div>
            <div class="form-group"><label>节点名称正则</label><input class="setting-input tt-mono" data-field="name_regex" value="${escapeHTML(profile.name_regex)}" placeholder="(?i)residential"></div>
            <div class="form-group"><label>最低质量分</label><input type="number" min="0" max="100" class="setting-input" data-field="min_quality" value="${profile.min_quality}"></div>
          </div>
        </div>`).join('');
    }
    this.renderProfileOptions();
  }

  private async loadAccess(): Promise<void> {
    const status = this.mount.querySelector<HTMLElement>('[data-access-status]');
    try {
      const response = await fetch('/api/access');
      this.access = await readJSON<AccessInfo>(response);
      if (status) { status.textContent = '就绪'; status.className = 'badge badge-success'; }
      this.renderProfileOptions();
      this.renderAccessOutput();
    } catch (error) {
      if (status) { status.textContent = '读取失败'; status.className = 'badge badge-error'; }
      const help = this.mount.querySelector<HTMLElement>('[data-access-help]');
      if (help) help.textContent = error instanceof Error ? error.message : '无法读取访问配置';
    }
  }

  private renderProfileOptions(): void {
    const select = this.mount.querySelector<HTMLSelectElement>('[data-access-profile]');
    if (!select) return;
    const selected = select.value;
    const profiles = this.serialize();
    select.innerHTML = '<option value="">全部节点</option>' + profiles.map(profile => `<option value="${escapeHTML(profile.name)}">${escapeHTML(profile.name)}</option>`).join('');
    if ([...select.options].some(option => option.value === selected)) select.value = selected;
  }

  private renderAccessOutput(): void {
    if (!this.access) return;
    const scheme = this.mount.querySelector<HTMLSelectElement>('[data-access-scheme]')?.value || 'http';
    const profile = this.mount.querySelector<HTMLSelectElement>('[data-access-profile]')?.value || '';
    const username = profile ? `${this.access.username}@${profile}` : this.access.username;
    const credentials = username ? `${encodeURIComponent(username)}:${encodeURIComponent(this.access.password)}@` : '';
    const host = this.access.host.includes(':') ? `[${this.access.host}]` : this.access.host;
    const uri = this.access.port ? `${scheme}://${credentials}${host}:${this.access.port}` : '';
    const curl = uri ? `curl --proxy '${uri}' https://api.ipify.org` : '';
    const uriField = this.mount.querySelector<HTMLInputElement>('[data-access-uri]');
    const curlField = this.mount.querySelector<HTMLInputElement>('[data-access-curl]');
    if (uriField) uriField.value = uri;
    if (curlField) curlField.value = curl;
    const help = this.mount.querySelector<HTMLElement>('[data-access-help]');
    if (help) help.textContent = this.access.username ? 'Profile 使用同一密码，用户名格式为 base@profile。' : '统一入口尚未启用认证；Named Profiles 需要先配置用户名和密码。';
  }

  private handleClick(event: Event): void {
    const target = event.target as Element;
    if (target.closest('[data-add-profile]')) {
      this.profiles = this.serialize();
      this.profiles.push({name:'', regions:[], name_regex:'', protocols:[], sources:[], min_quality:0});
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
      if (field?.value) void copyText(field.value).then(() => window.showToast?.('已复制'));
    }
  }
}

export function installProfilesModule(mount: HTMLElement | null): void {
  if (!mount) return;
  window.proxyFleetProfiles = new ProfileController(mount);
}
