// 用时显示：处理中的记录由浏览器每 100 毫秒根据开始时间自行累加，不依赖服务端刷新。

// Go 输出的时间可能有 7 位小数，浏览器只稳定支持 3 位，先截断。
function parseStarted(value) {
  return Date.parse(String(value).replace(/(\.\d{3})\d+/, '$1'));
}

export function formatSeconds(seconds) {
  const value = Math.max(0, Number(seconds) || 0);
  if (value < 60) return `${value.toFixed(2)}s`;
  if (value < 3600) return `${Math.floor(value / 60)} 分 ${Math.floor(value % 60)} 秒`;
  return `${Math.floor(value / 3600)} 时 ${Math.floor((value % 3600) / 60)} 分`;
}

// 列表和详情页共用：处理中显示可自动增长的数字，完成后显示最终用时，未开始显示 —。
export function durationHTML(job) {
  if (!job.started_at) return '—';
  if (job.status === 1) {
    const started = parseStarted(job.started_at);
    if (!Number.isNaN(started)) {
      const elapsed = (Date.now() - started) / 1000;
      return `<span class="live-duration" data-job-id="${job.id}" data-live-start="${job.started_at}">${formatSeconds(elapsed)}</span>`;
    }
  }
  return formatSeconds((Math.max(0, job.duration_ms || 0)) / 1000);
}

function tickLiveDurations() {
  const now = Date.now();
  document.querySelectorAll('[data-live-start]').forEach((element) => {
    const started = parseStarted(element.dataset.liveStart);
    if (!Number.isNaN(started)) element.textContent = formatSeconds((now - started) / 1000);
  });
}

setInterval(tickLiveDurations, 100);

// 由 job-events.js 在收到 job-finished 时调用：把仍在动画中的用时换成服务端的最终值并停止计时。
export function applyFinishedDuration({ id, duration_ms: durationMS }) {
  document.querySelectorAll(`[data-live-start][data-job-id="${id}"]`).forEach((element) => {
    element.removeAttribute('data-live-start');
    element.classList.remove('live-duration');
    element.textContent = formatSeconds(Math.max(0, durationMS || 0) / 1000);
  });
}
