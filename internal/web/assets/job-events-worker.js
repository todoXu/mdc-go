// SharedWorker：所有标签页共用同一个 SSE 连接，把服务端事件转发给每个页面。
// 浏览器对同一站点的 HTTP/1.1 连接数有限（通常 6 个）。每个标签页各开一个 EventSource，
// 长连接会占满这些连接，其它请求（接口、图片）就一直排队卡住。
// 页面关闭时发送 'bye' 把端口移出列表。
const ports = new Set();
let source = null;
let opened = false;

function broadcast(kind, data) {
  for (const port of ports) port.postMessage({ kind, data });
}

function parse(event) {
  try {
    return JSON.parse(event.data);
  } catch {
    return {};
  }
}

function connect() {
  if (source) return;
  source = new EventSource('/api/scrape/events');
  source.addEventListener('open', () => {
    // 第一次连接时页面刚加载完数据，不需要再拉一次；之后的重连要补齐断线期间的变化。
    if (opened) broadcast('resync', {});
    opened = true;
  });
  source.addEventListener('resync', () => broadcast('resync', {}));
  source.addEventListener('jobs-changed', (event) => broadcast('jobs-changed', parse(event)));
  source.addEventListener('job-finished', (event) => broadcast('job-finished', parse(event)));
  source.addEventListener('watch-changed', () => broadcast('watch-changed', {}));
}

onconnect = (event) => {
  const port = event.ports[0];
  ports.add(port);
  port.onmessage = (message) => {
    if (message.data === 'bye') ports.delete(port);
  };
  port.start();
  connect();
};
