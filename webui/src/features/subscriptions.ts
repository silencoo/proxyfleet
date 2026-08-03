import { copyText, readJSON } from '../api';

export interface SubscriptionSource {
  name: string;
  url: string;
  enabled: boolean;
  refresh_interval: string;
  has_headers?: boolean;
}

export interface SubscriptionSourceStatus {
  name: string;
  enabled: boolean;
  is_refreshing: boolean;
  using_fallback: boolean;
  node_count: number;
  last_attempt?: string;
  last_success?: string;
  next_refresh?: string;
  duration_ms?: number;
  last_error?: string;
}

export interface SubscriptionsModule {
  load(sources: SubscriptionSource[], statuses?: SubscriptionSourceStatus[]): void;
  serialize(): SubscriptionSource[];
  validate(): boolean;
  setStatuses(statuses: SubscriptionSourceStatus[]): void;
}

declare global {
  interface Window {
    proxyFleetSubscriptions?: SubscriptionsModule;
    requestConfirmation?: (title: string, message: string, confirmLabel: string) => Promise<boolean>;
    showToast?: (message: string, type?: string) => void;
  }
}

const escapeHTML = (value: unknown): string => String(value ?? '').replace(/[&<>"']/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char] || char));
const sourceNamePattern = /^[a-z0-9][a-z0-9._-]{0,31}$/;

const durationMilliseconds = (value: string): number => {
  const normalized = value.trim().toLowerCase();
  if (!normalized) return 0;
  let consumed = '';
  let total = 0;
  const expression = /(\d+(?:\.\d+)?)(ms|h|m|s)/g;
  for (const match of normalized.matchAll(expression)) {
    if (match.index !== consumed.length) return Number.NaN;
    consumed += match[0];
    const multipliers: Record<string, number> = {ms: 1, s: 1_000, m: 60_000, h: 3_600_000};
    total += Number(match[1]) * multipliers[match[2]];
  }
  return consumed === normalized && total > 0 ? total : Number.NaN;
};

const formatDate = (value?: string): string => {
  if (!value || value.startsWith('0001-01-01')) return '—';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString();
};

class SubscriptionController implements SubscriptionsModule {
  private sources: SubscriptionSource[] = [];
  private statuses = new Map<string, SubscriptionSourceStatus>();

  constructor(private readonly mount: HTMLElement) {
    this.renderShell();
    this.mount.addEventListener('click', event => { void this.handleClick(event); });
    this.mount.addEventListener('input', event => this.markDirty(event));
    this.mount.addEventListener('change', event => this.markDirty(event));
  }

  load(sources: SubscriptionSource[], statuses: SubscriptionSourceStatus[] = []): void {
    this.sources = Array.isArray(sources) ? sources.map((source, index) => ({
      name: String(source.name || `source-${index + 1}`).trim().toLowerCase(),
      url: String(source.url || '').trim(), enabled: source.enabled !== false,
      refresh_interval: String(source.refresh_interval || '').trim(), has_headers: source.has_headers === true,
    })) : [];
    this.setStatusMap(statuses);
    this.renderRows();
  }

  serialize(): SubscriptionSource[] {
    return [...this.mount.querySelectorAll<HTMLElement>('[data-subscription-source]')].map(row => ({
      name: this.value(row, 'name').toLowerCase(), url: this.value(row, 'url'),
      enabled: row.querySelector<HTMLInputElement>('[data-field="enabled"]')?.checked !== false,
      refresh_interval: this.value(row, 'refresh_interval'),
    }));
  }

  validate(): boolean {
    const sources = this.serialize();
    if (sources.length > 128) return this.invalid('订阅源数量不能超过 128 个。');
    const names = new Set<string>();
    const urls = new Set<string>();
    for (const source of sources) {
      if (!sourceNamePattern.test(source.name)) return this.invalid('订阅源名称需为 1–32 位小写字母、数字、点、下划线或连字符。');
      if (names.has(source.name)) return this.invalid(`订阅源名称重复：${source.name}`);
      names.add(source.name);
      try {
        const parsed = new URL(source.url);
        if (!['http:', 'https:'].includes(parsed.protocol)) throw new Error();
        const key = parsed.toString();
        if (urls.has(key)) return this.invalid(`订阅地址重复：${source.name}`);
        urls.add(key);
      } catch { return this.invalid(`订阅源 ${source.name} 的 URL 无效，仅支持 HTTP/HTTPS。`); }
      if (source.refresh_interval) {
        const duration = durationMilliseconds(source.refresh_interval);
        if (!Number.isFinite(duration) || duration < 300_000) return this.invalid(`订阅源 ${source.name} 的独立周期格式无效或小于 5 分钟。`);
      }
    }
    return true;
  }

  setStatuses(statuses: SubscriptionSourceStatus[]): void {
    this.setStatusMap(statuses);
    this.renderRows();
  }

  private setStatusMap(statuses: SubscriptionSourceStatus[]): void {
    this.statuses = new Map((Array.isArray(statuses) ? statuses : []).map(status => [String(status.name || '').toLowerCase(), status]));
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
      <div class="subscription-source-heading">
        <div><strong>Subscription Sources</strong><div class="field-help">每个来源可独立启停、设置刷新周期并查看最近一次结果；留空独立周期时继承上方全局值。</div></div>
        <button type="button" class="btn btn-sm" data-add-source><svg class="icon"><use href="#i-plus"></use></svg><span>添加订阅源</span></button>
      </div>
      <div class="subscription-source-list" data-source-list><div class="subscription-source-empty">正在读取订阅源…</div></div>`;
  }

  private renderRows(): void {
    const list = this.mount.querySelector<HTMLElement>('[data-source-list]');
    if (!list) return;
    if (!this.sources.length) {
      list.innerHTML = '<div class="subscription-source-empty">尚未配置订阅源。可以保留为空，仅使用手动节点。</div>';
      return;
    }
    list.innerHTML = this.sources.map((source, index) => {
      const status = this.statuses.get(source.name);
      const state = !source.enabled ? ['已停用', 'badge-offline'] : status?.is_refreshing ? ['刷新中', 'badge-warning'] : status?.last_error ? ['刷新失败', 'badge-error'] : status?.using_fallback ? ['使用缓存', 'badge-warning'] : status?.last_success ? ['正常', 'badge-healthy'] : ['等待刷新', 'badge-offline'];
      const id = `subscription-source-${index}`;
      return `<article class="subscription-source-row" data-subscription-source data-dirty="false">
        <div class="subscription-source-head">
          <div class="subscription-source-title"><span class="subscription-source-index">${String(index + 1).padStart(2, '0')}</span><strong>${escapeHTML(source.name)}</strong><span class="badge ${state[1]}" data-source-state>${state[0]}</span>${source.has_headers ? '<span class="badge badge-offline" title="请求头已保存在配置中；WebUI 不会回显敏感值">自定义 Header</span>' : ''}</div>
          <div class="subscription-source-actions">
            <label for="${id}-enabled"><input id="${id}-enabled" type="checkbox" data-field="enabled" ${source.enabled ? 'checked' : ''}> 启用</label>
            <button type="button" class="btn btn-sm" data-refresh-source="${index}" ${!source.enabled || !status ? 'disabled title="保存并完成一次全量刷新后可独立刷新"' : ''}><svg class="icon"><use href="#i-refresh"></use></svg><span>刷新此源</span></button>
            <button type="button" class="btn btn-sm btn-danger" data-remove-source="${index}"><svg class="icon"><use href="#i-trash"></use></svg><span>删除</span></button>
          </div>
        </div>
        <div class="subscription-source-fields">
          <div class="form-group"><label for="${id}-name">名称</label><input id="${id}-name" class="setting-input" data-field="name" value="${escapeHTML(source.name)}" placeholder="provider-a" autocomplete="off"></div>
          <div class="form-group subscription-source-url"><label for="${id}-url">URL</label><div class="sensitive-field"><input id="${id}-url" type="password" class="setting-input tt-mono" data-field="url" value="${escapeHTML(source.url)}" placeholder="https://example.com/sub?token=…" autocomplete="off"><button type="button" class="sensitive-toggle" data-toggle-source-url aria-label="显示 URL" title="显示 URL" aria-pressed="false"><svg class="icon"><use href="#i-eye"></use></svg></button></div><button type="button" class="btn btn-sm subscription-source-copy" data-copy-source-url title="复制完整 URL"><svg class="icon"><use href="#i-copy"></use></svg><span>复制</span></button></div>
          <div class="form-group"><label for="${id}-interval">独立刷新周期</label><input id="${id}-interval" class="setting-input" data-field="refresh_interval" value="${escapeHTML(source.refresh_interval)}" placeholder="继承全局，例如 30m"><div class="field-help">支持 30m、1h、1h30m，最短 5m。</div></div>
        </div>
        <div class="subscription-source-status">
          <span>节点 <strong>${Number(status?.node_count) || 0}</strong></span><span>耗时 <strong>${Number(status?.duration_ms) >= 0 ? `${Number(status?.duration_ms) || 0} ms` : '—'}</strong></span><span>上次成功 <strong>${formatDate(status?.last_success)}</strong></span><span>下次刷新 <strong>${source.enabled ? formatDate(status?.next_refresh) : '—'}</strong></span>
        </div>
        ${status?.last_error ? `<div class="subscription-source-error">${escapeHTML(status.last_error)}</div>` : ''}
      </article>`;
    }).join('');
  }

  private markDirty(event: Event): void {
    const row = (event.target as Element).closest<HTMLElement>('[data-subscription-source]');
    if (!row) return;
    row.dataset.dirty = 'true';
    const state = row.querySelector<HTMLElement>('[data-source-state]');
    if (state) { state.textContent = '待保存'; state.className = 'badge badge-warning'; }
    row.querySelector<HTMLButtonElement>('[data-refresh-source]')?.setAttribute('disabled', 'true');
    const heading = row.querySelector<HTMLElement>('.subscription-source-title strong');
    if (heading) heading.textContent = this.value(row, 'name') || '未命名订阅源';
  }

  private async handleClick(event: Event): Promise<void> {
    const target = event.target as Element;
    if (target.closest('[data-add-source]')) {
      const current = this.serialize();
      const names = new Set(current.map(source => source.name));
      let number = current.length + 1;
      while (names.has(`source-${number}`)) number += 1;
      this.sources = [...current, {name:`source-${number}`, url:'', enabled:true, refresh_interval:''}];
      this.renderRows();
      const row = this.mount.querySelector<HTMLElement>('[data-subscription-source]:last-child');
      if (row) row.dataset.dirty = 'true';
      row?.querySelector<HTMLInputElement>('[data-field="name"]')?.focus();
      return;
    }
    const remove = target.closest<HTMLButtonElement>('[data-remove-source]');
    if (remove) {
      const current = this.serialize();
      const index = Number(remove.dataset.removeSource);
      const source = current[index];
      const confirmed = await (window.requestConfirmation?.('删除订阅源', `确定删除 ${source?.name || '这个订阅源'}？保存后该来源的缓存节点会从候选池移除。`, '删除') ?? Promise.resolve(false));
      if (!confirmed) return;
      this.sources = current.filter((_, itemIndex) => itemIndex !== index);
      this.renderRows();
      return;
    }
    const toggle = target.closest<HTMLButtonElement>('[data-toggle-source-url]');
    if (toggle) {
      const input = toggle.parentElement?.querySelector<HTMLInputElement>('input');
      if (!input) return;
      const reveal = input.type === 'password';
      input.type = reveal ? 'text' : 'password';
      toggle.setAttribute('aria-pressed', String(reveal));
      toggle.title = reveal ? '隐藏 URL' : '显示 URL';
      toggle.setAttribute('aria-label', toggle.title);
      return;
    }
    const copy = target.closest<HTMLButtonElement>('[data-copy-source-url]');
    if (copy) {
      const value = copy.closest<HTMLElement>('[data-subscription-source]')?.querySelector<HTMLInputElement>('[data-field="url"]')?.value || '';
      if (!value) return;
      try { await copyText(value); window.showToast?.('订阅 URL 已复制'); } catch { window.showToast?.('复制失败', 'error'); }
      return;
    }
    const refresh = target.closest<HTMLButtonElement>('[data-refresh-source]');
    if (refresh && !refresh.disabled) {
      const row = refresh.closest<HTMLElement>('[data-subscription-source]');
      const name = row ? this.value(row, 'name').toLowerCase() : '';
      refresh.disabled = true;
      try {
        const result = await readJSON<{sources: SubscriptionSourceStatus[]}>(await fetch('/api/subscription/sources/refresh', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({name})}));
        this.setStatusMap(result.sources || []);
        this.sources = this.serialize();
        this.renderRows();
        window.showToast?.(`${name} 刷新成功`);
      } catch (error) {
        window.showToast?.(error instanceof Error ? error.message : '订阅源刷新失败', 'error');
        refresh.disabled = false;
      }
    }
  }
}

export function installSubscriptionsModule(mount: HTMLElement | null): void {
  if (!mount) return;
  window.proxyFleetSubscriptions = new SubscriptionController(mount);
}
