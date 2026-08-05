export interface ProxyFleetI18n {
  tr(source: string, values?: Record<string, string | number>): string;
  translateTree(root: ParentNode): void;
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
  Object.entries(values || {}).forEach(([key, value]) => {
    result = result.replace(`{${key}}`, String(value));
  });
  return result;
};

export const translateTree = (root: ParentNode): void => {
  window.proxyFleetI18n?.translateTree(root);
};

export const localizedAPIMessage = (message: unknown, fallback: string): string => (
  window.proxyFleetI18n?.localizedAPIMessage(message, fallback) || tr(fallback)
);
