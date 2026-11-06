export function escapeHTML(value) {
  return String(value ?? '').replace(
    /[&<>"']/g,
    (char) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char],
  );
}

export async function api(path, { method = 'GET', body, timeout = 10000 } = {}) {
  const response = await fetch(path, {
    method,
    headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(timeout),
  });
  if (!response) throw Error('请求未返回响应，请重新加载页面后重试。');
  const text = await response.text();
  let value;
  try {
    value = JSON.parse(text);
  } catch {
    value = text;
  }
  if (!response.ok) {
    if (response.status === 501) throw Error('这项功能尚未实现，当前不会执行实际操作。');
    const error = Error(value?.message || `请求失败（${response.status}）`);
    error.code = value?.error;
    error.details = value;
    throw error;
  }
  return value;
}
