import { tr } from '../i18n';

const messages: Record<string, string> = {
  '跳到主要内容':'Skip to main content', '主导航':'Main navigation', '打开导航':'Open navigation',
  '关闭导航':'Close navigation', '收起导航':'Collapse navigation', '展开导航':'Expand navigation',
  '看板更新':'Dashboard updated', '等待数据':'Waiting for data', '刷新看板':'Refresh dashboard',
  '监控数据更新失败，显示上次结果。':'Monitoring data could not be updated. Showing the last available results.',
  '重试看板更新':'Retry dashboard update', '修改节点配置后需重载核心生效。':'Reload the core to apply node configuration changes.',
  '外观':'Appearance', '浅色':'Light', '深色':'Dark', '地域连通率':'Region availability',
  '最优节点延迟':'Fastest nodes', '实时流量带宽':'Live bandwidth', '稳定性掉线排行':'Least stable nodes',
};

export function installShell(): void {
  window.proxyFleetI18n?.registerMessages?.(messages);
  const sidebar = document.getElementById('appSidebar')!;
  const slot = document.getElementById('sidebarSlot')!;
  const drawer = document.getElementById('navigationDrawer') as HTMLDialogElement;
  const opener = document.getElementById('menuToggle')!;
  const toggle = document.getElementById('sidebarToggle')!;
  const main = document.getElementById('mainContent')!;
  const media = window.matchMedia('(max-width: 900px)');
  let collapsed = false;
  let fromHistory = false;
  try { collapsed = localStorage.getItem('sidebarCollapsed') === 'true'; } catch { /* Navigation works without storage. */ }
  const active = (): string => document.querySelector<HTMLElement>('.nav-item.active')?.dataset.tab || 'dashboard';
  const valid = (route: string): boolean => [...sidebar.querySelectorAll<HTMLElement>('[data-tab]')].some(link => link.dataset.tab === route && !link.hidden);
  const sync = (): void => {
    document.body.classList.toggle('sidebar-collapsed', collapsed && !media.matches);
    toggle.setAttribute('aria-expanded', String(!collapsed));
    const label = collapsed ? '展开导航' : '收起导航';
    toggle.setAttribute('aria-label', tr(label));
    toggle.dataset.i18nAriaLabel = label;
    toggle.querySelector('span')!.textContent = tr(label);
    toggle.querySelector('use')!.setAttribute('href', collapsed ? '#i-chevron-right' : '#i-chevron-left');
    sidebar.querySelectorAll<HTMLElement>('[data-tab]').forEach(link => {
      // Same authoritative label in expanded, rail and mobile navigation.
      link.setAttribute('aria-label', link.querySelector('span')!.textContent!);
    });
    document.getElementById('mobileRoute')!.textContent = sidebar.querySelector('.active span')!.textContent;
  };
  const tooltip = document.createElement('div');
  tooltip.id = 'navigationTooltip'; tooltip.role = 'tooltip'; tooltip.hidden = true;
  document.body.append(tooltip);
  const hideTooltip = (): void => { tooltip.hidden = true; };
  const showTooltip = (target: EventTarget | null): void => {
    const link = target instanceof Element ? target.closest<HTMLElement>('a[data-tab]') : null;
    if (!link || !collapsed || media.matches) return;
    const box = link.getBoundingClientRect();
    tooltip.textContent = link.getAttribute('aria-label');
    tooltip.style.left = `${box.right + 12}px`; tooltip.style.top = `${box.top}px`;
    tooltip.hidden = false;
  };
  sidebar.addEventListener('mouseover', event => showTooltip(event.target));
  sidebar.addEventListener('focusin', event => showTooltip(event.target));
  // Pointer events caused by collapsing the rail must not dismiss a keyboard label.
  sidebar.addEventListener('mouseout', () => {
    hideTooltip();
    if (sidebar.contains(document.activeElement)) showTooltip(document.activeElement);
  });
  sidebar.addEventListener('focusout', hideTooltip);
  sidebar.addEventListener('keydown', event => { if (event.key === 'Escape') hideTooltip(); });
  sidebar.addEventListener('scroll', hideTooltip, true);
  const close = (): void => { if (drawer.open) drawer.close(); opener.setAttribute('aria-expanded','false'); };
  const layout = (): void => {
    const wasOpen = drawer.open;
    const sidebarHadFocus = sidebar.contains(document.activeElement);
    close(); hideTooltip();
    (media.matches ? drawer : slot).append(sidebar);
    sync();
    if (wasOpen || sidebarHadFocus) {
      if (media.matches) opener.focus();
      else (sidebar.querySelector<HTMLElement>('.active') || toggle).focus();
    }
    window.dispatchEvent(new Event('resize')); // Existing chart owners recalculate after the rail width changes.
  };
  toggle.addEventListener('click', () => {
    collapsed = !collapsed; hideTooltip();
    try { localStorage.setItem('sidebarCollapsed', String(collapsed)); } catch { /* Optional preference. */ }
    sync(); window.dispatchEvent(new Event('resize'));
  });
  opener.addEventListener('click', () => {
    drawer.showModal(); opener.setAttribute('aria-expanded','true');
    sidebar.querySelector<HTMLElement>('.active')?.focus();
  });
  document.getElementById('drawerClose')!.addEventListener('click', close);
  drawer.addEventListener('keydown', event => {
    if (event.key !== 'Tab') return;
    const controls = [...drawer.querySelectorAll<HTMLElement>('a[href], button:not(:disabled)')].filter(el => el.getClientRects().length > 0);
    const first = controls[0], last = controls[controls.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
  });
  document.addEventListener('proxyfleet:auth-required', close);
  drawer.addEventListener('close', () => opener.setAttribute('aria-expanded','false'));
  drawer.addEventListener('click', event => { if (event.target === drawer) close(); });
  // Close before the existing unsaved-draft confirmation opens; never stack a
  // custom alert below a native top-layer drawer.
  sidebar.addEventListener('click', event => {
    const link = (event.target as Element).closest<HTMLAnchorElement>('a[data-tab]');
    if (!link || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey || event.button !== 0) return;
    event.preventDefault(); close();
    void window.switchTab?.(link.dataset.tab!);
  });
  document.addEventListener('proxyfleet:navigation', () => {
    close(); hideTooltip(); sync();
    if (!fromHistory && location.hash !== `#${active()}`) history.pushState(null, '', `#${active()}`);
    main.scrollTop = 0;
    main.focus({preventScroll:true});
  });
  const restoreRoute = async (): Promise<void> => {
    if (document.body.dataset.appReady !== 'true') return;
    const route = location.hash.slice(1) || 'dashboard';
    fromHistory = true;
    try { if (valid(route) && route !== active()) await window.switchTab?.(route); }
    finally { fromHistory = false; history.replaceState(null, '', `#${active()}`); sync(); }
  };
  window.addEventListener('hashchange', () => { void restoreRoute(); });
  document.addEventListener('proxyfleet:ready', () => { void restoreRoute(); });
  document.addEventListener('proxyfleet:language', sync);
  media.addEventListener('change', layout);
  layout();
  if (document.body.dataset.appReady === 'true') void restoreRoute();
}
