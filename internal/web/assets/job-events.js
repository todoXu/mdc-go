// 全站共用一个 SSE 连接，接收任务和监控状态的变化。页面不再定时请求服务器。
// 连接由 SharedWorker（job-events-worker.js）持有，所有标签页共用，避免每个标签页各占一个连接
// 导致浏览器的连接数被占满。浏览器不支持 SharedWorker 时，退回到每个页面一个 EventSource。
// 事件类型：
//   jobs-changed(id)  任务被创建、修改、停止、删除或编辑状态变化
//   job-finished(id)  任务结束，同时校正用时动画的终值
//   watch-changed     监控扫描开始或结束
//   resync            连接断开后重新连上，或服务端丢了部分事件；页面应整体重新拉取
import { applyFinishedDuration } from './duration.js';

const listeners = new Set();
let port = null;
let source = null;
let opened = false;

// 监听函数收到 (kind, data)。data 对 jobs-changed、job-finished 为 {id, list}，其它事件为 {}。
// 没有监听器时发出的事件会被丢弃。页面切换时会先清空监听器，再异步加载数据，之后才重新绑定，
// 所以加载期间完成的任务不会被任何页面收到。missed 记录这种情况，绑定完成后由 flushMissedJobEvents 补救。
let missed = false;
function emit(kind, data = {}) {
  if (listeners.size === 0) missed = true;
  for (const listener of [...listeners]) listener(kind, data);
}

function handle(kind, data) {
  if (kind === 'job-finished') {
    applyFinishedDuration(data);
    emit('job-finished', { id: data.id, list: true });
    return;
  }
  emit(kind, data);
}

// 页面绑定监听器之后调用：如果加载期间有事件被丢弃，补发一次 resync，让页面整体重新拉取。
export function flushMissedJobEvents() {
  if (!missed) return;
  missed = false;
  emit('resync');
}

function parse(event) {
  try {
    return JSON.parse(event.data);
  } catch {
    return {};
  }
}

// 直连方式：每个页面一个 EventSource（仅在不支持 SharedWorker 时使用）。
function connectDirect() {
  if (source || !('EventSource' in window)) return;
  source = new EventSource('/api/scrape/events');
  source.addEventListener('open', () => {
    // 首次连接时页面刚加载完数据，不需要再拉一次；之后的重连要补齐断线期间的变化。
    if (opened) emit('resync');
    opened = true;
  });
  source.addEventListener('resync', () => emit('resync'));
  source.addEventListener('jobs-changed', (event) => handle('jobs-changed', parse(event)));
  source.addEventListener('job-finished', (event) => handle('job-finished', parse(event)));
  source.addEventListener('watch-changed', () => emit('watch-changed'));
}

function connect() {
  if (port || source) return;
  if (!('SharedWorker' in window)) {
    connectDirect();
    return;
  }
  const worker = new SharedWorker('/job-events-worker.js');
  port = worker.port;
  port.onmessage = (event) => handle(event.data.kind, event.data.data);
  port.start();
  // 页面关闭时通知 worker 移除本页的端口，避免端口一直累积。
  window.addEventListener('pagehide', () => port.postMessage('bye'));
}

// 应用启动时调用，让连接早于首次列表加载，避免加载期间的变化被漏掉。
export function connectJobEvents() {
  connect();
}

// 返回取消订阅的函数。
export function onJobEvents(listener) {
  connect();
  listeners.add(listener);
  return () => listeners.delete(listener);
}

// 切换页面时清空上一个页面的订阅，避免重复监听。
export function clearJobEvents() {
  listeners.clear();
}
