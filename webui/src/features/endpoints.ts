import type { ProfileConfig } from './profiles';

export interface EndpointConfig {
  name: string;
  enabled: boolean;
  address: string;
  port: number;
  username: string;
  password: string;
  profile: string;
  status?: string;
  message?: string;
}

export interface EndpointsModule {
  load(endpoints: EndpointConfig[], profiles?: ProfileConfig[]): void;
  setProfiles(profiles: ProfileConfig[]): void;
  serialize(): EndpointConfig[];
  validate(): boolean;
}

declare global {
  interface Window {
    proxyFleetEndpoints?: EndpointsModule;
    proxyFleetProfiles?: { serialize(): ProfileConfig[]; validate(): boolean; load(profiles: ProfileConfig[]): void };
    requestConfirmation?: (title: string, message: string, confirmLabel: string) => Promise<boolean>;
    showToast?: (message: string, type?: string) => void;
  }
}

const escapeHTML = (value: unknown): string => String(value ?? '').replace(/[&<>"']/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char] || char));
const endpointNamePattern = /^[a-z0-9][a-z0-9._-]{0,31}$/;

const isIPAddress = (value: string): boolean => {
  const address = value.trim().replace(/^\[|\]$/g, '');
  const octets = address.split('.');
  if (octets.length === 4 && octets.every(octet => /^\d{1,3}$/.test(octet) && Number(octet) <= 255)) return true;
  return address.includes(':') && /^[0-9a-f:.]+$/i.test(address);
};

const statusPresentation = (status = 'waiting'): { label: string; className: string } => ({
  running: {label: '运行中', className: 'badge-healthy'},
  waiting: {label: '等待节点', className: 'badge-warning'},
  disabled: {label: '已停用', className: 'badge-offline'},
  inactive: {label: '当前模式停用', className: 'badge-offline'},
  failed: {label: '启动失败', className: 'badge-error'},
  dirty: {label: '待保存', className: 'badge-warning'},
}[status] || {label: status, className: 'badge-offline'});

class EndpointController implements EndpointsModule {
  private endpoints: EndpointConfig[] = [];
  private profiles: ProfileConfig[] = [];

  constructor(private readonly mount: HTMLElement) {
    this.renderShell();
    this.mount.addEventListener('click', event => { void this.handleClick(event); });
    this.mount.addEventListener('input', event => this.markDirty(event));
    this.mount.addEventListener('change', event => this.markDirty(event));
  }

  load(endpoints: EndpointConfig[], profiles: ProfileConfig[] = []): void {
    this.profiles = Array.isArray(profiles) ? profiles : [];
    this.endpoints = Array.isArray(endpoints) ? endpoints.map(endpoint => ({
      name: String(endpoint.name || ''), enabled: endpoint.enabled !== false,
      address: String(endpoint.address || ''), port: Number(endpoint.port) || 0,
      username: String(endpoint.username || ''), password: String(endpoint.password || ''),
      profile: String(endpoint.profile || ''), status: String(endpoint.status || 'waiting'),
      message: String(endpoint.message || ''),
    })) : [];
    this.renderRows();
  }

  setProfiles(profiles: ProfileConfig[]): void {
    this.profiles = Array.isArray(profiles) ? profiles : [];
    this.refreshProfileOptions();
  }

  serialize(): EndpointConfig[] {
    return [...this.mount.querySelectorAll<HTMLElement>('[data-endpoint-row]')].map(row => ({
      name: this.value(row, 'name').toLowerCase(),
      enabled: row.querySelector<HTMLInputElement>('[data-field="enabled"]')?.checked !== false,
      address: this.value(row, 'address').replace(/^\[|\]$/g, ''),
      port: Number(this.value(row, 'port')) || 0,
      username: this.value(row, 'username'),
      password: row.querySelector<HTMLInputElement>('[data-field="password"]')?.value || '',
      profile: this.value(row, 'profile'),
    }));
  }

  validate(): boolean {
    const endpoints = this.serialize();
    if (!endpoints.length) return this.invalid('至少需要保留一个 Endpoint。');
    if (endpoints.length > 128) return this.invalid('Endpoint 数量不能超过 128 个。');
    const names = new Set<string>();
    const sockets = new Set<string>();
    const profiles = new Set((window.proxyFleetProfiles?.serialize() || this.profiles).map(profile => profile.name));
    for (const endpoint of endpoints) {
      if (!endpointNamePattern.test(endpoint.name)) return this.invalid('Endpoint 名称需为 1–32 位小写字母、数字、点、下划线或连字符。');
      if (names.has(endpoint.name)) return this.invalid(`Endpoint 名称重复：${endpoint.name}`);
      names.add(endpoint.name);
      if (!isIPAddress(endpoint.address)) return this.invalid(`Endpoint ${endpoint.name} 的监听地址必须是 IP 地址。`);
      if (endpoint.port < 1 || endpoint.port > 65535) return this.invalid(`Endpoint ${endpoint.name} 的端口必须在 1 到 65535 之间。`);
      const socket = `${endpoint.address.toLowerCase()}:${endpoint.port}`;
      if (sockets.has(socket)) return this.invalid(`监听地址与端口重复：${endpoint.address}:${endpoint.port}`);
      sockets.add(socket);
      if (Boolean(endpoint.username) !== Boolean(endpoint.password)) return this.invalid(`Endpoint ${endpoint.name} 的用户名和密码必须同时填写或同时留空。`);
      if (endpoint.profile && !profiles.has(endpoint.profile)) return this.invalid(`Endpoint ${endpoint.name} 引用了不存在的 Profile：${endpoint.profile}`);
    }
    return true;
  }

  private value(row: HTMLElement, field: string): string {
    return (row.querySelector<HTMLInputElement | HTMLSelectElement>(`[data-field="${field}"]`)?.value || '').trim();
  }

  private invalid(message: string): false {
    window.showToast?.(message, 'error');
    return false;
  }

  private renderShell(): void {
    this.mount.innerHTML = `
      <section class="panel endpoint-panel" style="margin-bottom:16px">
        <div class="panel-header endpoint-panel-header">
          <div><div class="panel-title">Endpoint Manager</div><div class="field-help">管理共享节点池的独立 HTTP / SOCKS5 入口；新增入口不会复制节点，也不会增加健康探测。</div></div>
          <button type="button" class="btn btn-sm" data-add-endpoint><svg class="icon"><use href="#i-plus"></use></svg><span>添加 Endpoint</span></button>
        </div>
        <div class="endpoint-mode-note">Endpoint 只在 pool / hybrid 模式启动；切换到 multi-port 时配置会保留，但监听保持停用。</div>
        <div class="endpoint-list" data-endpoint-list><div class="endpoint-empty">正在读取 Endpoint…</div></div>
      </section>`;
  }

  private renderRows(): void {
    const list = this.mount.querySelector<HTMLElement>('[data-endpoint-list]');
    if (!list) return;
    if (!this.endpoints.length) {
      list.innerHTML = '<div class="endpoint-empty">尚未配置 Endpoint。请添加一个池入口后保存。</div>';
      return;
    }
    list.innerHTML = this.endpoints.map((endpoint, index) => {
      const status = statusPresentation(endpoint.status);
      const id = `endpoint-${index}`;
      return `
        <article class="endpoint-row" data-endpoint-row data-status="${escapeHTML(endpoint.status || 'waiting')}">
          <div class="endpoint-row-head">
            <div class="endpoint-identity"><span class="endpoint-index">${String(index + 1).padStart(2, '0')}</span><strong>${escapeHTML(endpoint.name || '未命名 Endpoint')}</strong><span class="badge ${status.className}" data-endpoint-status title="${escapeHTML(endpoint.message)}">${status.label}</span></div>
            <div class="endpoint-actions">
              <label class="endpoint-enabled" for="${id}-enabled"><input id="${id}-enabled" type="checkbox" data-field="enabled" ${endpoint.enabled ? 'checked' : ''}><span>启用</span></label>
              <button type="button" class="btn btn-sm btn-danger" data-remove-endpoint="${index}" ${this.endpoints.length === 1 ? 'disabled title="至少保留一个 Endpoint"' : ''}><svg class="icon"><use href="#i-trash"></use></svg><span>删除</span></button>
            </div>
          </div>
          <div class="endpoint-fields">
            <div class="form-group"><label for="${id}-name">名称</label><input id="${id}-name" class="setting-input" data-field="name" value="${escapeHTML(endpoint.name)}" placeholder="default" autocomplete="off"></div>
            <div class="form-group"><label for="${id}-address">监听地址</label><input id="${id}-address" class="setting-input tt-mono" data-field="address" value="${escapeHTML(endpoint.address)}" placeholder="127.0.0.1" inputmode="decimal"></div>
            <div class="form-group"><label for="${id}-port">端口</label><input id="${id}-port" type="number" min="1" max="65535" class="setting-input" data-field="port" value="${endpoint.port || ''}" placeholder="2323"></div>
            <div class="form-group"><label for="${id}-profile">固定 Profile</label><select id="${id}-profile" class="setting-input" data-field="profile">${this.profileOptions(endpoint.profile)}</select><div class="field-help">留空时允许访问全部节点，并支持 base@profile。</div></div>
            <div class="form-group"><label for="${id}-username">用户名（可选）</label><input id="${id}-username" class="setting-input" data-field="username" value="${escapeHTML(endpoint.username)}" autocomplete="off"></div>
            <div class="form-group"><label for="${id}-password">密码（可选）</label><div class="sensitive-field"><input id="${id}-password" type="password" class="setting-input" data-field="password" value="${escapeHTML(endpoint.password)}" autocomplete="new-password"><button type="button" class="sensitive-toggle" data-toggle-endpoint-secret aria-label="显示密码" title="显示密码" aria-pressed="false"><svg class="icon"><use href="#i-eye"></use></svg></button></div></div>
          </div>
          ${endpoint.message ? `<div class="endpoint-message">${escapeHTML(endpoint.message)}</div>` : ''}
        </article>`;
    }).join('');
  }

  private profileOptions(selected: string): string {
    return '<option value="">全部节点</option>' + this.profiles.map(profile => `<option value="${escapeHTML(profile.name)}" ${profile.name === selected ? 'selected' : ''}>${escapeHTML(profile.name)}</option>`).join('');
  }

  private refreshProfileOptions(): void {
    this.mount.querySelectorAll<HTMLSelectElement>('[data-field="profile"]').forEach(select => {
      const selected = select.value;
      select.innerHTML = this.profileOptions(selected);
      if ([...select.options].some(option => option.value === selected)) select.value = selected;
    });
  }

  private markDirty(event: Event): void {
    const row = (event.target as Element).closest<HTMLElement>('[data-endpoint-row]');
    if (!row) return;
    const badge = row.querySelector<HTMLElement>('[data-endpoint-status]');
    const presentation = statusPresentation('dirty');
    if (badge) {
      badge.textContent = presentation.label;
      badge.className = `badge ${presentation.className}`;
      badge.title = '保存后由运行时重新确认状态';
    }
    row.dataset.status = 'dirty';
    const name = this.value(row, 'name');
    const heading = row.querySelector<HTMLElement>('.endpoint-identity strong');
    if (heading) heading.textContent = name || '未命名 Endpoint';
  }

  private async handleClick(event: Event): Promise<void> {
    const target = event.target as Element;
    if (target.closest('[data-add-endpoint]')) {
      const current = this.serialize();
      const usedNames = new Set(current.map(endpoint => endpoint.name));
      const usedPorts = new Set(current.map(endpoint => endpoint.port));
      let number = current.length + 1;
      while (usedNames.has(`endpoint-${number}`)) number += 1;
      let port = Math.max(2322, ...current.map(endpoint => endpoint.port)) + 1;
      while (usedPorts.has(port) && port < 65535) port += 1;
      this.endpoints = [...current, {name:`endpoint-${number}`, enabled:true, address:'127.0.0.1', port:Math.min(port, 65535), username:'', password:'', profile:'', status:'dirty'}];
      this.renderRows();
      this.mount.querySelector<HTMLInputElement>('[data-endpoint-row]:last-child [data-field="name"]')?.focus();
      return;
    }
    const remove = target.closest<HTMLButtonElement>('[data-remove-endpoint]');
    if (remove && !remove.disabled) {
      const current = this.serialize();
      const index = Number(remove.dataset.removeEndpoint);
      const endpoint = current[index];
      const confirmed = await (window.requestConfirmation?.('删除 Endpoint', `确定删除 ${endpoint?.name || '这个 Endpoint'}？保存后该监听将立即停止，现有连接可能中断。`, '删除') ?? Promise.resolve(false));
      if (!confirmed) return;
      this.endpoints = current.filter((_, itemIndex) => itemIndex !== index).map(item => ({...item, status:'dirty'}));
      this.renderRows();
      return;
    }
    const toggle = target.closest<HTMLButtonElement>('[data-toggle-endpoint-secret]');
    if (toggle) {
      const input = toggle.parentElement?.querySelector<HTMLInputElement>('input');
      if (!input) return;
      const reveal = input.type === 'password';
      input.type = reveal ? 'text' : 'password';
      toggle.setAttribute('aria-pressed', String(reveal));
      toggle.setAttribute('aria-label', reveal ? '隐藏密码' : '显示密码');
      toggle.title = reveal ? '隐藏密码' : '显示密码';
    }
  }
}

export function installEndpointsModule(mount: HTMLElement | null): void {
  if (!mount) return;
  window.proxyFleetEndpoints = new EndpointController(mount);
}
