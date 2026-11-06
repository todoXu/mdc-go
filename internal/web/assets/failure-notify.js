import { api, escapeHTML as h } from './api.js';
import { providerStyle } from './provider-color.js';

// 数据源设置里的「失败时特别提示」：勾选的数据源抓取失败时，成功的记录显示为黄色并标出失败的数据源数量。
// 勾选后立即保存；只提交 notify_failures，不会影响字段优先级。
export async function mountFailureNotify(container, sources, { toast }) {
  const { config } = await api('/api/providers/priorities');
  let saved = [...(config.notify_failures || [])];

  container.innerHTML = `<section class="panel notify-panel">
    <div class="panel-head"><div><h2>失败时特别提示</h2><p class="subtitle">勾选的数据源抓取失败时，即使记录整理成功，也显示为黄色并标出失败的数据源</p></div><span class="notify-status" role="status"></span></div>
    <div class="notify-source-list">${sources.length
      ? sources.map((source) => `<label class="notify-source provider-colored" style="${providerStyle(source.id)}"><input type="checkbox" data-notify-id="${h(source.id)}" ${saved.includes(source.id) ? 'checked' : ''}><span>${h(source.name)}</span>${source.enabled ? '' : '<small>未启用</small>'}</label>`).join('')
      : '<p class="pending">没有已安装的数据源。</p>'}</div>
  </section>`;

  const status = container.querySelector('.notify-status');
  const boxes = [...container.querySelectorAll('[data-notify-id]')];
  boxes.forEach((box) => {
    box.onchange = async () => {
      const list = boxes.filter((item) => item.checked).map((item) => item.dataset.notifyId);
      boxes.forEach((item) => { item.disabled = true; });
      status.textContent = '正在保存…';
      try {
        await api('/api/providers/priorities', { method: 'PUT', body: { notify_failures: list } });
        saved = list;
        status.textContent = '已保存';
      } catch (error) {
        // 保存失败时恢复到上次保存的状态。
        boxes.forEach((item) => { item.checked = saved.includes(item.dataset.notifyId); });
        status.textContent = '';
        toast(error.message, true);
      } finally {
        boxes.forEach((item) => { item.disabled = false; });
      }
    };
  });
}
