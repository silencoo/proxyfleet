import { tr } from './i18n';

export async function readJSON<T>(response: Response): Promise<T> {
  const payload = await response.json().catch(() => null) as ({ error?: unknown } & T) | null;
  if (!response.ok) throw new Error(typeof payload?.error === 'string' ? payload.error : `Request failed (${response.status})`);
  if (payload === null || typeof payload !== 'object' || Array.isArray(payload)) {
    throw new Error(tr('服务器响应格式无效，请重试。'));
  }
  return payload;
}

export async function copyText(value: string): Promise<void> {
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(value);
    return;
  }
  const textarea = document.createElement('textarea');
  textarea.value = value;
  textarea.style.position = 'fixed';
  textarea.style.opacity = '0';
  document.body.appendChild(textarea);
  textarea.select();
  document.execCommand('copy');
  textarea.remove();
}
