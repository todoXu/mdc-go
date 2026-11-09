function fail(code, message) { throw {code: code, message: message}; }
function base() { return (config.BaseURL || manifest.BaseURL).replace(/\/$/, ""); }
function request(address, options) {
  options = options || {};
  options.accept_language = "zh-CN,zh;q=0.9,en;q=0.8";
  return host.request(address, options);
}
function check(page) {
  if (page.status === 404) fail("not_found", "数据源没有找到这个番号");
  if (page.status === 403 || page.status === 429 || /cf-chl-|just a moment|captcha|driver-verify/i.test(page.body || "")) fail("verification_required", "JavBus 返回验证页面，请确认插件使用中文 Accept-Language");
  if (page.status !== 200) fail("upstream_error", "数据源返回 HTTP " + page.status);
}
function nodes(page, selector) { return host.select(page.body, selector); }
function first(page, selector) { return nodes(page, selector)[0] || null; }
function value(page, names) {
  var rows = nodes(page, ".info p");
  for (var i=0; i<rows.length; i++) {
    var headers = host.select(rows[i].html, ".header");
    var label = headers.length ? headers[0].text.replace(/[：:\s]+$/, "") : "";
    if (names.indexOf(label) >= 0) return rows[i].text.replace(headers[0].text, "").trim();
  }
  return "";
}
function list(page, selector, attribute) {
  var found = nodes(page, selector), output = [];
  for (var i=0; i<found.length; i++) {
    var item = attribute ? found[i].attrs[attribute] : found[i].text;
    if (attribute && item) item = host.resolve(page.url, item);
    if (item && output.indexOf(item) < 0) output.push(item);
  }
  return output;
}
var plugin = {
  test: function() {
    var page = request(base() + "/", {}); check(page);
    if (!nodes(page, ".movie-box, .bigImage").length) fail("unexpected_page", "已连接网站，但没有找到预期内容；请检查验证状态");
    return true;
  },
  fetch: function(input) {
    var pageURL = "";
    if (/^https?:\/\//i.test(input)) {
      var prefix = (config.BaseURL || manifest.BaseURL).replace(/\/$/, "") + "/";
      if (input.indexOf(prefix) !== 0 || /[?#]/.test(input)) fail("invalid_number", "作品链接必须属于当前数据源");
      input = decodeURIComponent(input.slice(prefix.length));
      if (input.indexOf("/") >= 0) fail("invalid_number", "请输入作品详情链接");
      // 网址路径可能带日期等后缀（NTRD-019_2015-01-29），必须直接请求原网址；番号只取下划线前面的部分。
      pageURL = prefix + encodeURIComponent(input);
      input = input.replace(/_.*$/, "");
    }
    var number = host.normalize(input);
    if (!number) fail("invalid_number", "请输入有效番号，不要填写网址或文件路径");
    var page = request(pageURL || (base() + "/" + encodeURIComponent(number)), {});
    // 站点的页面地址不带前导数字（406FTHT-146 是 /FTHT-146），找不到时去掉后再请求一次；网址直接请求时不改。
    var bare = number.replace(/^\d+(?=[A-Z])/, "");
    if (!pageURL && page.status === 404 && bare !== number) { number = bare; page = request(base() + "/" + encodeURIComponent(number), {}); }
    check(page);
    var titleNode = first(page, "h3"), coverNode = first(page, ".bigImage img");
    var trailerNode = first(page, "video source[src], video[src], meta[property='og:video:url'][content], meta[property='og:video'][content]");
    var found = host.normalize(value(page, ["識別碼", "识别码", "ID"]));
    if (!found || !titleNode) fail("parse_error", "页面缺少番号或标题；可能是验证页或站点结构变化");
    if (found.replace(/-/g, "") !== number.replace(/-/g, "")) fail("number_mismatch", "返回页面的番号与查询不一致");
    var title = titleNode.text.replace(new RegExp("^" + found + "\\s*", "i"), "").trim();
    return {
      provider: manifest.ID, source_url: page.url, number: found,
      title: title, original_title: title,
      cover: coverNode ? host.resolve(page.url, coverNode.attrs.src || "") : "",
      trailer: trailerNode ? host.resolve(page.url, trailerNode.attrs.src || trailerNode.attrs.content || "") : "",
      release: value(page, ["發行日期", "发行日期", "Released"]).replace(/\//g, "-"),
      runtime: value(page, ["長度", "长度", "Length"]),
      director: value(page, ["導演", "导演", "Director"]),
      studio: value(page, ["製作商", "制作商", "Studio"]),
      publisher: value(page, ["發行商", "发行商", "Label"]),
      series: value(page, ["系列", "Series"]),
      actors: list(page, ".star-name a"),
      tags: list(page, ".genre a[href*='genre/']"),
      samples: list(page, ".sample-box", "href")
    };
  },
  image: function(address, referer) {
    var page = request(address, {binary: true, referer: referer});
    if (page.status !== 200) fail("image_upstream", "图片站点拒绝访问或图片不存在");
    return {body_base64: page.body_base64, content_type: page.content_type};
  }
};
