import { api } from './api.js';

// 程序运行所在的系统（服务端的 runtime.GOOS），决定完整路径的写法和路径分隔符。
// 页面启动时由 loadPlatform 读取一次；读取前按浏览器所在系统猜测。
const platform = {
  os: /Windows/i.test(navigator.userAgent) ? 'windows' : 'linux',
  version: '',
};

export async function loadPlatform() {
  try {
    const info = await api('/api/version');
    if (info?.os) platform.os = info.os;
    if (info?.version) platform.version = info.version;
  } catch {
    // 读取失败时沿用猜测值；保存设置时服务端仍会按实际系统检查路径。
  }
  return platform;
}

export const isWindows = () => platform.os === 'windows';
export const platformVersion = () => platform.version;
export const pathSeparator = () => (isWindows() ? '\\' : '/');

// 平台名称，用于界面文字，例如「Windows 本地版」。
export function platformName() {
  return { windows: 'Windows', linux: 'Linux', darwin: 'macOS' }[platform.os] || platform.os;
}

// 是否为当前系统的完整路径：Windows 为 C:\… 或 \\服务器\共享\…，Linux 等为 /…。
export function isAbsolutePath(path) {
  const value = String(path || '').trim();
  return isWindows() ? /^(?:[a-zA-Z]:[\\/]|\\\\[^\\/]+[\\/][^\\/]+)/.test(value) : value.startsWith('/');
}

// 路径格式错误时的提示，例如「请填写完整的整理目录路径（例如 D:\Media\已整理）」。
export function absolutePathHint(what) {
  return `请填写完整的${what}路径（例如 ${isWindows() ? 'D:\\Media' : '/data/media'}）`;
}
