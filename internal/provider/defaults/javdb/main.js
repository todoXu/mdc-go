function fail(code, message) { throw {code: code, message: message}; }
function base() { return (config.BaseURL || manifest.BaseURL).replace(/\/$/, ""); }
function check(page) {
  if (page.status === 404) fail("not_found", "数据源没有找到这个番号");
  if (page.status === 403 || page.status === 429 || /cf-chl-|just a moment|captcha|driver-verify/i.test(page.body || "")) fail("verification_required", "JavDB 需要 Cookies：请在数据源配置中粘贴浏览器里的 Cookies，并填写与浏览器一致的 User-Agent");
  if (page.status !== 200) fail("upstream_error", "数据源返回 HTTP " + page.status);
}
function nodes(page, selector) { return host.select(page.body, selector); }
function first(page, selector) { return nodes(page, selector)[0] || null; }
// 详情页信息行形如 <strong>日期:</strong> <span class="value">...</span>
function labeled(page, names) {
  var rows = nodes(page, ".panel-block");
  for (var i=0; i<rows.length; i++) {
    var label = host.select(rows[i].html, "strong");
    if (!label.length) continue;
    var key = label[0].text.replace(/[:：\s]+$/, "");
    if (names.indexOf(key) < 0) continue;
    var value = host.select(rows[i].html, ".value");
    return value.length ? value[0].text.trim() : rows[i].text.replace(label[0].text, "").trim();
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
// 搜索页的每个结果都带有番号文本，只用来判断是否为同一番号。
function resultNumber(item) {
  var node = host.select(item.html, ".video-title strong");
  return node.length ? host.normalize(node[0].text) : "";
}
// bareKey 去掉连字符和前导数字后大写，用于比较番号：406FTHT-146 与 FTHT-146 视为同一作品。
function bareKey(value) { return value.replace(/-/g, "").replace(/^\d+(?=[A-Za-z])/, "").toUpperCase(); }
// searchJavdb 用 query 搜索，返回列表中番号与 number 完全相同的作品路径。
function searchJavdb(query, number) {
  var search = host.request(base() + "/search?q=" + encodeURIComponent(query) + "&f=all", {}); check(search);
  var items = nodes(search, ".movie-list .item"), paths = [];
  for (var i=0; i<items.length; i++) {
    var link = host.select(items[i].html, "a[href^='/v/']");
    if (!link.length) continue;
    var path = link[0].attrs.href;
    if (resultNumber(items[i]) === number && paths.indexOf(path) < 0) paths.push(path);
  }
  return paths;
}
function locate(input) {
  input = input.trim();
  if (/^https?:\/\//i.test(input)) {
    var prefix = base() + "/v/";
    if (input.indexOf(prefix) !== 0 || /[?#]/.test(input)) fail("invalid_number", "作品链接必须属于当前数据源，并指向 /v/ 详情页");
    return {number: "", path: "/v/" + input.slice(prefix.length).replace(/\/$/, "")};
  }
  var number = host.normalize(input);
  if (!number) fail("invalid_number", "请输入有效番号或作品详情链接");
  var paths = searchJavdb(number, number);
  // 站点列表的番号可能不带前导数字（406FTHT-146 收录为 FTHT-146），去掉后再搜一次。
  var bare = number.replace(/^\d+(?=[A-Z])/, "");
  if (!paths.length && bare !== number) paths = searchJavdb(bare, bare);
  if (!paths.length) fail("not_found", "搜索结果中没有找到完全匹配的番号");
  if (paths.length > 1) fail("ambiguous_number", "存在多个同番号作品，请直接输入作品详情链接");
  return {number: number, path: paths[0]};
}
// 评分行形如：<strong>評分:</strong> <span class="value">…&nbsp;4.5分, 由1893人評價</span>。
// 只取前面的分数，评价人数里的数字不能拼进去。
function score(page) {
  var value = labeled(page, ["評分", "评分", "Rating"]);
  var hit = String(value || "").match(/\d+(?:\.\d+)?/);
  return hit ? hit[0] : "";
}
var plugin = {
  test: function() {
    var page = host.request(base() + "/", {}); check(page);
    if (!nodes(page, ".movie-list, a[href^='/v/'], a[href^='/search']").length) fail("unexpected_page", "没有拿到 JavDB 的影片列表，通常是没有 Cookies 或 Cookies 已过期；请在数据源配置中填写 Cookies");
    return true;
  },
  fetch: function(input) {
    var work = locate(input);
    var page = host.request(host.resolve(base() + "/", work.path), {}); check(page);
    // 详情页标题区：<h2 class="title"><strong>番号</strong> <strong class="current-title">中文标题</strong> <span class="origin-title">原标题</span></h2>
    var numberNode = first(page, "h2.title strong"), titleNode = first(page, "h2.title strong.current-title");
    var originNode = first(page, "h2.title .origin-title"), coverNode = first(page, ".column-video-cover img");
    var trailerNode = first(page, "video source[src], video[src], meta[property='og:video:url'][content], meta[property='og:video'][content]");
    var number = numberNode ? host.normalize(numberNode.text) : "";
    if (!number || !titleNode) fail("parse_error", "页面缺少番号或标题；可能是验证页或站点结构变化");
    if (work.number && bareKey(number) !== bareKey(work.number)) fail("number_mismatch", "返回页面的番号与查询不一致");
    var cover = coverNode ? host.resolve(page.url, coverNode.attrs.src || "") : "";
    return {
      provider: manifest.ID, source_url: page.url, number: number,
      title: titleNode.text.trim(), original_title: originNode ? originNode.text.trim() : titleNode.text.trim(),
      cover: cover,
      trailer: trailerNode ? host.resolve(page.url, trailerNode.attrs.src || trailerNode.attrs.content || "") : "",
      release: labeled(page, ["日期", "Released Date", "Released"]).replace(/\//g, "-"),
      runtime: labeled(page, ["時長", "时长", "Duration"]).replace(/\D+/g, ""),
      rating: score(page),
      director: labeled(page, ["導演", "导演", "Director"]),
      studio: labeled(page, ["片商", "Maker"]),
      publisher: labeled(page, ["發行", "发行", "Publisher"]),
      series: labeled(page, ["系列", "Series"]),
      // 页面用 actor-female 标记女演员，男演员没有这个 class。
      actors: list(page, ".panel-block a.actor-female[href^='/actors/']"),
      tags: list(page, ".panel-block a[href^='/tags']"),
      // 页面底部的相关影片卡片也带 tile-item，只保留图片地址。
      samples: list(page, "a.tile-item", "href").filter(function(u) { return /\.(jpe?g|png|webp)(\?|$)/i.test(u); })
    };
  },
  image: function(address, referer) {
    var page = host.request(address, {binary: true, referer: referer});
    if (page.status !== 200) fail("image_upstream", "图片站点拒绝访问或图片不存在");
    return {body_base64: page.body_base64, content_type: page.content_type};
  }
};
