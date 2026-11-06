import { api, escapeHTML as h } from './api.js';

// 日志等级：只记录所选等级及更严重的日志。设置保存在配置的 system.log_level。
const levels = [
  ['error', '错误'],
  ['warning', '警告'],
  ['info', '信息'],
  ['debug', '调试'],
];
// 自动刷新间隔（秒）。开关和间隔保存在 system.log_auto_refresh、system.log_refresh_interval。
const intervals = [1, 2, 3, 5, 10];

// 日志页：显示服务端内存中的最近日志，可导出，可设置日志等级。
export function logsPage(heading) {
  return {
    html: heading('日志', '显示程序运行日志（保存在内存中，最多 5000 条，重启后清空）。') + `<section class="panel logs-panel">
      <div class="logs-toolbar">
        <button id="logs-refresh" class="button small" type="button">刷新</button>
        <label class="logs-auto"><input id="logs-auto" class="switch" type="checkbox" disabled> 自动刷新</label>
        <label class="logs-interval">间隔
          <select id="logs-interval" aria-label="自动刷新间隔" disabled>
            ${intervals.map((seconds) => `<option value="${seconds}">${seconds} 秒</option>`).join('')}
          </select>
        </label>
        <label class="logs-level">日志等级
          <select id="logs-level" aria-describedby="logs-level-note" disabled>${levels.map(([value, name]) => `<option value="${value}">${name}</option>`).join('')}</select>
          <small id="logs-level-note" role="status">只记录所选等级及更严重的日志</small>
        </label>
        <button id="logs-file" class="button small" type="button" aria-pressed="false">写入本地文件</button>
        <a id="logs-export" class="button small primary" href="/api/system/logs/export" download>导出日志</a>
      </div>
      <p id="logs-file-status" class="muted logs-file-status" role="status"></p>
      <pre id="logs-view" class="logs-view" tabindex="0" aria-label="日志内容"></pre>
      <p id="logs-status" class="muted" role="status"></p>
    </section>`,
    bind() {
      const view = document.querySelector('#logs-view');
      const status = document.querySelector('#logs-status');
      const auto = document.querySelector('#logs-auto');
      let timer = null;

      const levelSelect = document.querySelector('#logs-level');
      const levelNote = document.querySelector('#logs-level-note');
      let levelSaving = false;
      const load = async () => {
        if (!view.isConnected) return stop();
        try {
          const data = await api('/api/system/logs?limit=500');
          const atBottom = view.scrollTop + view.clientHeight >= view.scrollHeight - 24;
          // 每行按等级着色；日志内容先转义再放入页面。
          const entries = data.data || [];
          view.innerHTML = entries.map((entry) => `<span class="log-line log-${h(entry.level)}">${h(entry.message)}</span>`).join('\n');
          status.textContent = entries.length
            ? `共 ${data.total_count} 条，显示最近 ${entries.length} 条`
            : '暂无日志。只记录所选等级及更严重的日志，可以在「日志等级」中调低等级。';
          showFile(data.file);
          // 读到当前等级之前下拉框保持禁用，避免先显示默认的第一项。
          if (data.level && !levelSaving) {
            levelSelect.value = data.level;
            levelSelect.dataset.saved = data.level;
            levelSelect.disabled = false;
          }
          if (atBottom) view.scrollTop = view.scrollHeight;
        } catch (error) {
          status.textContent = '读取日志失败：' + error.message;
        }
      };
      levelSelect.onchange = async () => {
        const next = levelSelect.value;
        levelSaving = true;
        levelSelect.disabled = true;
        levelNote.textContent = '正在保存…';
        try {
          await api('/api/config/system', { method: 'PUT', body: { log_level: next } });
          levelSelect.dataset.saved = next;
          levelNote.textContent = `已保存：只记录${levels.find(([value]) => value === next)?.[1] || next}及更严重的日志`;
        } catch (error) {
          levelSelect.value = levelSelect.dataset.saved || 'warning';
          levelNote.textContent = '保存失败：' + error.message;
        } finally {
          levelSaving = false;
          levelSelect.disabled = false;
        }
      };
      const stop = () => {
        clearInterval(timer);
        timer = null;
      };

      const fileButton = document.querySelector('#logs-file');
      const fileStatus = document.querySelector('#logs-file-status');
      const showFile = (file) => {
        fileButton.setAttribute('aria-pressed', String(Boolean(file?.enabled)));
        fileButton.textContent = file?.enabled ? '停止写入本地文件' : '写入本地文件';
        fileStatus.textContent = file?.enabled ? `正在写入：${file.path}（包含内存中已有的日志，之后的新日志持续追加）` : '';
      };
      fileButton.onclick = async () => {
        const enable = fileButton.getAttribute('aria-pressed') !== 'true';
        fileButton.disabled = true;
        try {
          const result = await api('/api/system/logs/file', { method: 'PUT', body: { enabled: enable } });
          showFile({ enabled: result.enabled, path: result.path });
        } catch (error) {
          fileStatus.textContent = error.message;
        } finally {
          fileButton.disabled = false;
        }
      };

      const interval = document.querySelector('#logs-interval');
      const start = () => {
        stop();
        if (auto.checked) timer = setInterval(load, Number(interval.value) * 1000);
      };
      // 页面上的选择与已保存的配置一致；保存失败时恢复为已保存的值。
      let savedRefresh = { log_auto_refresh: false, log_refresh_interval: 3 };
      const showRefresh = (settings) => {
        const seconds = Number(settings.log_refresh_interval) || 3;
        // 配置文件里可能写了列表之外的间隔，补一个选项，避免显示成别的值。
        if (![...interval.options].some((option) => Number(option.value) === seconds)) {
          interval.add(new Option(`${seconds} 秒`, String(seconds)));
        }
        auto.checked = Boolean(settings.log_auto_refresh);
        interval.value = String(seconds);
      };
      const saveRefresh = async () => {
        const next = { log_auto_refresh: auto.checked, log_refresh_interval: Number(interval.value) };
        start();
        auto.disabled = interval.disabled = true;
        try {
          await api('/api/config/system', { method: 'PUT', body: next });
          savedRefresh = next;
        } catch (error) {
          showRefresh(savedRefresh);
          start();
          status.textContent = '保存自动刷新设置失败：' + error.message;
        } finally {
          auto.disabled = interval.disabled = false;
        }
      };

      document.querySelector('#logs-refresh').onclick = load;
      auto.onchange = saveRefresh;
      interval.onchange = saveRefresh;
      load();
      // 读取已保存的自动刷新设置，读到之前开关和间隔保持禁用。
      api('/api/config/system')
        .then((settings) => {
          if (!view.isConnected) return;
          savedRefresh = { log_auto_refresh: Boolean(settings.log_auto_refresh), log_refresh_interval: Number(settings.log_refresh_interval) || 3 };
          showRefresh(savedRefresh);
          start();
        })
        .catch((error) => {
          status.textContent = '读取自动刷新设置失败：' + error.message;
        })
        .finally(() => {
          auto.disabled = interval.disabled = false;
        });
    },
  };
}
