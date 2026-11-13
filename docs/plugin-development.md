# 数据源插件开发文档

数据源插件是一段 JavaScript，程序用它按番号抓取元数据和图片。插件只负责描述“去哪里取、怎么解析”，网络请求、超时、代理、Cookie 注入和重定向限制都由程序统一处理。

本文档对应插件协议 `api_version: 2`，引擎为 `javascript-v1`。

## 目录结构

每个插件是一个文件夹，文件夹名必须与 `plugin.json` 中的 `id` 相同：

```
javdb/
  plugin.json   插件清单（必需）
  main.js       插件逻辑（必需）
  config.json   运行时设置（由程序保存，内置插件才有默认值）
```

打包上传时，压缩包根目录只能包含 `plugin.json` 和 `main.js`。`config.json` 由程序在运行时生成和保存，不要放进压缩包。

## plugin.json

```json
{
  "api_version": 2,
  "id": "javdb",
  "name": "javdb",
  "version": "1.0.5",
  "engine": "javascript-v1",
  "base_url": "https://javdb.com",
  "input_hint": "需先在数据源配置中填写 Cookies"
}
```

| 字段 | 必需 | 说明 |
| --- | --- | --- |
| `api_version` | 是 | 固定为 `2`。 |
| `id` | 是 | 字母开头，只能包含字母、数字、`_`、`-`，最长 64 个字符。必须与文件夹名一致。 |
| `name` | 是 | 页面上显示的名称。 |
| `version` | 是 | 版本号。内置插件只有版本号变化时，程序才会覆盖已安装的文件。 |
| `engine` | 是 | 固定为 `javascript-v1`。 |
| `base_url` | 是 | 默认站点地址，必须是 `http` 或 `https`，不能带账户、查询参数或片段。 |
| `input_hint` | 否 | 在页面上显示的输入提示。 |

## config.json 与站点地址

`config.json` 保存用户在页面上设置的值。插件执行时，这些值以全局变量 `config` 传入：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `config.Enabled` | bool | 是否启用。 |
| `config.BaseURL` | string | 用户设置的站点地址。为空时使用 `manifest.BaseURL`。 |
| `config.Cookies` | string | Cookie 字符串。只会发送到与 `BaseURL` 同主机的请求。 |
| `config.UserAgent` | string | 为空时使用程序默认的 Chrome UA。 |
| `config.Proxy` | string | 代理地址，例如 `http://127.0.0.1:7890`。 |
| `config.CooldownSeconds` | int | 同一插件两次请求之间的最小间隔（秒），范围 0 到 60。 |
| `config.RequestTimeout` | int | 单次请求超时（秒），0 表示 20 秒。 |
| `config.TrailerTimeout` | int | 预告片下载超时（秒），0 表示 60 秒。 |
| `config.Options` | object | 插件自定义的选项，程序不解释其内容。 |

插件中建议这样取地址：

```js
function base() { return (config.BaseURL || manifest.BaseURL).replace(/\/$/, ""); }
```

`manifest` 是 `plugin.json` 的内容，字段名同样是 Go 结构体的字段名（如 `manifest.ID`、`manifest.BaseURL`）。

## 插件对象

`main.js` 必须定义一个全局变量 `plugin`，并实现下面的方法：

| 方法 | 调用时机 | 输入 | 返回 |
| --- | --- | --- | --- |
| `test()` | 页面点击“测试”时 | 无 | 任意值。抛出异常表示失败。 |
| `fetch(input)` | 按番号或链接刮削时 | 用户输入的番号或链接 | 元数据对象，见下文。 |
| `image(address, referer)` | 下载封面、海报、剧照时 | 图片地址、来源页地址 | `{body_base64, content_type}` |

`fetch` 返回的对象必须包含 `number`、`title`、`source_url`，`provider` 可以省略或与 `id` 相同。

### 返回的元数据字段

字段名对应 Go 结构体的 JSON 标签，全部为字符串，除非注明是数组：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `provider` | string | 数据源 ID，可省略。 |
| `source_url` | string | 作品页地址，必需。 |
| `number` | string | 番号，必需。大写字母、数字，用 `-` 连接，如 `ABP-123`。 |
| `title` | string | 标题，必需。 |
| `original_title` | string | 原始标题。 |
| `outline` | string | 简介。 |
| `publish_number` | string | 发行番号。 |
| `cover` | string | 封面图地址。 |
| `poster` | string | 海报图地址。 |
| `poster_from_cover` | bool | 海报是否直接取自封面。 |
| `trailer` | string | 预告片直链（MP4 或 WebM）。 |
| `release` | string | 发行日期，格式 `YYYY-MM-DD`。 |
| `runtime` | string | 时长（分钟）。 |
| `director` | string | 导演。 |
| `studio` | string | 片商。 |
| `publisher` | string | 发行商。 |
| `series` | string | 系列。 |
| `rating` | string | 评分。 |
| `actors` | 数组 | 演员名。 |
| `male_actors` | 数组 | 男演员名。 |
| `tags` | 数组 | 标签。 |
| `samples` | 数组 | 剧照图片地址。 |
| `has_subtitle` | bool | 来源是否标明字幕。 |

没有值的字段可以省略或返回空字符串、空数组。程序会把缺失的数组补成空数组。

## 宿主提供的 `host` 接口

`host` 是程序注入的全局对象，插件只能通过它访问网络和解析 HTML。

### host.request(url, options)

发起一次 GET 请求，返回对象：

- 文本模式：`{status, url, content_type, body}`
- 二进制模式（`binary: true`）：`{status, url, content_type, body_base64}`

`url` 是重定向后的最终地址。

`options` 可用字段：

| 字段 | 说明 |
| --- | --- |
| `headers` | 对象，附加请求头。`Host`、`Cookie`、`User-Agent`、`Accept-Language`、`Content-Length` 不能通过这里设置。 |
| `accept_language` | 覆盖默认的 `zh-CN,zh;q=0.9,en;q=0.8`。 |
| `referer` | 来源页地址，只接受 `http` 或 `https`。 |
| `binary` | `true` 时返回 `body_base64`，图片或视频应使用此模式。 |
| `media` | 设为 `"trailer"` 时，按预告片的大小上限和超时处理。 |

限制：

- 普通请求体上限 4 MB，二进制 20 MB，预告片 64 MB。
- 最多跟随 5 次重定向，且不能跨主机。
- Cookie 只发送到与 `BaseURL` 同主机的请求。

### host.select(html, selector)

用 CSS 选择器解析 HTML，返回节点数组。每个节点是：

```js
{ text: "去掉多余空白后的文本", html: "外层 HTML", attrs: { href: "...", src: "..." } }
```

选择器语法与 CSS 相同，并支持 `:has()`、`:not()`、`:contains()` 等 goquery 扩展。选择器不合法时会报错。

### host.resolve(baseUrl, reference)

把相对地址解析为绝对地址。只允许 `http` 和 `https`，其他情况返回空字符串。

### host.normalize(text)

把番号整理为标准格式（去空格、下划线换成连字符、转大写）。无效时返回空字符串。

## 错误处理

出错时抛出对象，格式为 `{code: "...", message: "..."}`。`message` 会显示在页面上，应使用中文说明原因。

```js
function fail(code, message) { throw {code: code, message: message}; }
```

常用错误码（程序也会使用这些码）：

| 错误码 | 含义 |
| --- | --- |
| `not_found` | 站点没有这个番号。 |
| `verification_required` | 站点需要验证，例如 Cloudflare 或年龄确认。 |
| `upstream_error` | 站点返回了非预期的 HTTP 状态码。 |
| `parse_error` | 页面结构变化，关键字段缺失。 |
| `invalid_number` | 输入的番号或链接无效。 |
| `ambiguous_number` | 搜索到多个同番号作品。 |
| `number_mismatch` | 页面番号与查询番号不一致。 |
| `image_upstream` | 图片下载失败。 |

程序自己也会返回 `network_error`、`timeout`、`invalid_page`、`invalid_image`、`plugin_error` 等码。

## 示例

下面是一个最小可用的插件。它访问一个假设的站点，从页面中取标题和封面。

```js
function fail(code, message) { throw {code: code, message: message}; }
function base() { return (config.BaseURL || manifest.BaseURL).replace(/\/$/, ""); }

var plugin = {
  test: function() {
    var page = host.request(base() + "/", {});
    if (page.status !== 200) fail("upstream_error", "数据源返回 HTTP " + page.status);
    return true;
  },

  fetch: function(input) {
    var number = host.normalize(input);
    if (!number) fail("invalid_number", "请输入有效番号");

    var page = host.request(base() + "/video/" + encodeURIComponent(number), {});
    if (page.status === 404) fail("not_found", "数据源没有找到这个番号");
    if (page.status !== 200) fail("upstream_error", "数据源返回 HTTP " + page.status);

    var title = host.select(page.body, "h1");
    var cover = host.select(page.body, "img.cover");
    if (!title.length) fail("parse_error", "页面缺少标题；可能是验证页或站点结构变化");

    var coverURL = cover.length ? host.resolve(page.url, cover[0].attrs.src || "") : "";
    return {
      provider: manifest.ID,
      source_url: page.url,
      number: number,
      title: title[0].text,
      cover: coverURL,
      poster: coverURL
    };
  },

  image: function(address, referer) {
    var page = host.request(address, {binary: true, referer: referer});
    if (page.status !== 200) fail("image_upstream", "图片站点拒绝访问或图片不存在");
    return {body_base64: page.body_base64, content_type: page.content_type};
  }
};
```

实际插件可以参考内置的 `internal/provider/defaults/avbase/main.js`，它包含搜索、番号比对、验证页检测和预告片等完整逻辑。

## 调试与发布

1. 在 `plugins` 目录中新建文件夹，放入 `plugin.json` 和 `main.js`。重启程序后页面会显示该插件。
2. 在数据源配置中填写地址和 Cookie，点击“测试”。
3. 用一个已知番号测试刮削，查看任务日志中的数据源报错。
4. 发布时把 `plugin.json` 和 `main.js` 打成压缩包上传。只有这两个文件在压缩包根目录，其他文件会被拒绝。

## 注意事项

- 插件只能发 `GET` 请求，不能提交表单或登录。需要登录态时，让用户在浏览器中获取 Cookie 后填入配置。
- 不要在 `main.js` 中写入 Cookie、密码或 API Key，这些应放在用户的 `config.json` 中。
- 同一番号不同写法（如带前导数字 `406FTHT-146` 和 `FTHT-146`）可能指向同一作品，比较时应去掉前导数字后再判断。
- 插件无法直接处理需要播放器脚本的预告片（HLS 播放列表或网页）。只能返回直接可下载的 MP4 或 WebM 地址。
- 版本号变化后，内置插件会覆盖 `main.js` 和 `plugin.json`。自己修改内置插件时，记得不要改版本号，否则下次升级会覆盖。
