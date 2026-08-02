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

  let copyEventHandled = false;
  const handleCopy = (event: ClipboardEvent) => {
    if (!event.clipboardData) return;
    try {
      event.clipboardData.setData('text/plain', text);
      event.preventDefault();
      copyEventHandled = true;
    } catch {
      // The command result below remains unsuccessful when clipboard data cannot be set.
    }
  };
  document.addEventListener('copy', handleCopy);

  try {
    if (!document.execCommand('copy') || !copyEventHandled) {
      throw new Error('浏览器拒绝访问剪贴板，请手动选择并复制');
    }
  } finally {
    document.removeEventListener('copy', handleCopy);
    textarea.remove();
    if (activeElement?.isConnected) activeElement.focus();
  }
}

export async function copyText(text: string) {
  let legacyError: unknown;
  try {
    copyTextWithLegacyAPI(text);
    return;
  } catch (error) {
    legacyError = error;
    // The synchronous path preserves the button click's user activation. Use the modern API if it is unavailable.
  }

  if (!navigator.clipboard?.writeText) {
    if (legacyError instanceof Error) throw legacyError;
    throw new Error('当前浏览器不支持自动复制，请手动选择并复制');
  }
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    throw new Error('浏览器拒绝访问剪贴板，请手动选择并复制');
  }
}
