export interface ProxyFleetI18n {
	registerMessages?(messages: Record<string,string>): void;
  tr(source: string, values?: Record<string, string | number>): string;
  translateTree(root: ParentNode): void;
  setText(element: Element | null, source: string, values?: Record<string, string | number>): void;
  setDate(element: Element | null, value: string, timeOnly?: boolean): void;
  localizedAPIMessage(message: unknown, fallback?: string): string;
}

declare global {
  interface Window {
    proxyFleetI18n?: ProxyFleetI18n;
  }
}

export const tr = (source: string, values?: Record<string, string | number>): string => {
  const translated = window.proxyFleetI18n?.tr(source, values);
  if (translated !== undefined) return translated;
  let result = source;
  return result.replace(/\{(\w+)\}/g, (placeholder, key) => values && Object.prototype.hasOwnProperty.call(values, key) ? String(values[key]) : placeholder);
};

export const setText = (element: Element | null, source: string, values?: Record<string, string | number>): void => {
  if (window.proxyFleetI18n) window.proxyFleetI18n.setText(element, source, values);
  else if (element) element.textContent = tr(source, values);
};

export const setDate = (element: Element | null, value: string, timeOnly = false): void => {
  if (window.proxyFleetI18n) window.proxyFleetI18n.setDate(element, value, timeOnly);
  else if (element) element.textContent = timeOnly ? new Date(value).toLocaleTimeString(document.documentElement.lang) : new Date(value).toLocaleString(document.documentElement.lang);
};

export const translateTree = (root: ParentNode): void => {
  window.proxyFleetI18n?.translateTree(root);
};

export const localizedAPIMessage = (message: unknown, fallback: string): string => (
  window.proxyFleetI18n?.localizedAPIMessage(message, fallback) || tr(fallback)
);
