// 每个数据源按 ID 固定一种颜色，在优先级设置、监控目录设置和刮削记录中保持一致。
// 使用 FNV-1a 哈希取色相，比简单累加分布更散，常见插件之间颜色区分明显。
export function providerHue(id) {
  let hash = 2166136261;
  for (const char of String(id || '')) {
    hash ^= char.codePointAt(0);
    hash = Math.imul(hash, 16777619) >>> 0;
  }
  return hash % 360;
}

// 用于元素的 style 属性，配合 CSS 中的 --provider-hue 变量。
export const providerStyle = (id) => `--provider-hue:${providerHue(id)}`;
