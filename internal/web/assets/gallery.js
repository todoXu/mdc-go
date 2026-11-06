import { escapeHTML as h } from './api.js';

function imageURL(value) {
  try {
    const url = new URL(value, location.origin);
    return ['http:', 'https:'].includes(url.protocol) ? url.href : '';
  } catch {
    return '';
  }
}

// Shared by single-source and field-priority query results.
export function imageGallery(values, label = '图片', previews = {}) {
  const urls = [
    ...new Set(
      (Array.isArray(values) ? values : [values]).filter(Boolean).map(imageURL).filter(Boolean),
    ),
  ];
  if (!urls.length) return '<span class="image-empty">暂无图片</span>';
  return `<div class="image-gallery">${urls
    .map((url, index) => {
      const display =
        typeof previews[url] === 'string' && (previews[url].startsWith('/api/images/') || /^\/api\/scrape\/jobs\/[^/]+\/images\/(poster|thumb|fanart)\.(jpg|png|webp)$/.test(previews[url]))
          ? previews[url]
          : url;
      return `<button type="button" class="image-thumb" data-preview-src="${h(display)}" data-preview-url="${h(url)}" data-preview-label="${h(label + ' ' + (index + 1))}" aria-label="${h('查看' + label + ' ' + (index + 1) + '原图')}"><img src="${h(display)}" alt="${h(label + ' ' + (index + 1))}" loading="lazy" decoding="async" referrerpolicy="no-referrer"><span>${h(label)} ${index + 1} · 查看原图</span><small class="image-failure" hidden>图片加载失败，点击查看原图</small></button>`;
    })
    .join('')}</div>`;
}

let viewer;
function openImage(url, label, trigger) {
  viewer?.close();
  const dialog = document.createElement('dialog');
  dialog.className = 'image-viewer';
  dialog.setAttribute('aria-label', label + '原图预览');
  // 只显示图片本身；点击任意位置或按 Esc 关闭。
  dialog.innerHTML = `<img alt="${h(label + '原图')}" referrerpolicy="no-referrer"><span class="image-viewer-status" role="status" hidden>无法加载图片</span>`;
  document.body.append(dialog);
  viewer = dialog;
  const image = dialog.querySelector('img'),
    status = dialog.querySelector('[role=status]');
  image.onerror = () => {
    image.hidden = true;
    status.hidden = false;
  };
  image.src = trigger.dataset.previewSrc || url;
  dialog.onclick = () => dialog.close();
  dialog.onclose = () => {
    dialog.remove();
    if (viewer === dialog) viewer = null;
    if (trigger.isConnected) trigger.focus();
  };
  dialog.showModal();
}
document.addEventListener('click', (event) => {
  const trigger = event.target.closest('[data-preview-url]');
  if (trigger) {
    const url = imageURL(trigger.dataset.previewUrl);
    if (url) openImage(url, trigger.dataset.previewLabel, trigger);
  }
});
document.addEventListener(
  'error',
  (event) => {
    if (event.target instanceof HTMLImageElement && event.target.closest('.image-thumb')) {
      event.target.hidden = true;
      event.target.closest('.image-thumb').querySelector('.image-failure').hidden = false;
    }
  },
  true,
);
