function copyTextWithLegacyAPI(text: string) {
  if (!document.body || typeof document.execCommand !== 'function') {
    throw new Error('当前浏览器不支持自动复制，请手动选择并复制');
  }

  const activeElement = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const textarea = document.createElement('textarea');
  textarea.value = text;
  textarea.readOnly = true;
  textarea.tabIndex = -1;
  textarea.setAttribute('aria-hidden', 'true');
  Object.assign(textarea.style, {
    position: 'fixed',
    inset: '0 auto auto 0',
    width: '1px',
    height: '1px',
    padding: '0',
    border: '0',
    fontSize: '16px',
    opacity: '0',
    pointerEvents: 'none',
  });

  document.body.appendChild(textarea);
  textarea.focus();
  textarea.select();
  textarea.setSelectionRange(0, text.length);

  try {
    if (!document.execCommand('copy')) {
      throw new Error('浏览器拒绝访问剪贴板，请手动选择并复制');
    }
  } finally {
    textarea.remove();
    if (activeElement?.isConnected) activeElement.focus();
  }
}

export async function copyText(text: string) {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch {
      // Clipboard API may be blocked even in a secure context; try the legacy API below.
    }
  }

  copyTextWithLegacyAPI(text);
}
