// JAVDBUI（javdb-web-ui 前端站点）是 Vite + React 的单页应用：页面 HTML 里没有
// 任何作品数据，全部走站点的 JSON 接口，所以插件直接调接口而不是抓 HTML。
//   搜索  GET <api>/v2/search?q=<番号>&type=movie&page=1&limit=30
//   详情  GET <api>/v4/movies/<id>
// 接口必须带 jdsignature 头：<unix 秒>.<key>.md5(<unix 秒> + <盐>)，缺了会返回
// InvalidSignature。接口默认地址是站点自己使用的镜像 <https://jdforrepam.com/api>，
// 可在数据源配置的 options.api_base 里改。站点本身有 Cloudflare 校验，抓图时建议
// 在数据源配置里填浏览器里的 Cookies 和一致的 User-Agent。
var SIG_KEY = "lpw6vgqzsp";
var SIG_SALT = "71cf27bb3c0bcdf207b64abecddc970098c7421ee7203b9cdae54478478a199e7d5a6e1a57691123c1a931c057842fb73ba3b3c83bcd69c17ccf174081e3d8aa";
var DEFAULT_API = "https://jdforrepam.com/api";

function fail(code, message) { throw {code: code, message: message}; }
function site() { return String(config.BaseURL || manifest.BaseURL || "").replace(/\/$/, ""); }
function api() {
  var options = config.Options || {};
  var custom = options.api_base;
  if (custom && typeof custom === "string") return custom.replace(/\/$/, "");
  return DEFAULT_API;
}
// 站点后来把搜索、详情接口改成要登录：未登录时返回
// {"success":0,"action":"JWTVerificationError","message":"請登錄帳號"}。
// 登录态存在浏览器 localStorage 的 javdb.auth.session 里（{"token":"eyJ...","user":{…}}），
// 前端把它原样放进 authorization 头，没有 Bearer 前缀。把 token 填到插件扩展配置即可：
//   options: {"token": "eyJ..."}
function token() {
  var options = config.Options || {};
  var value = options.token;
  return typeof value === "string" ? value.trim() : "";
}
function loginHint() {
  if (token()) return "登录凭证已失效：请在浏览器重新登录站点，再把 localStorage 里 javdb.auth.session 的 token 重新填到插件扩展配置";
  return "站点接口现在要求登录：请在浏览器打开站点并登录，然后把 localStorage 里 javdb.auth.session 的 token 填到插件扩展配置，格式 {\"token\":\"eyJ...\"}";
}

// ---------- MD5（goja 里没有 crypto，签名只能自己算） ----------
function utf8Bytes(text) {
  var encoded = encodeURIComponent(String(text)), out = [];
  for (var i = 0; i < encoded.length; i++) {
    if (encoded.charAt(i) === "%") {
      out.push(parseInt(encoded.substr(i + 1, 2), 16));
      i += 2;
    } else {
      out.push(encoded.charCodeAt(i));
    }
  }
  return out;
}
function md5(text) {
  var bytes = utf8Bytes(text), bits = bytes.length * 8;
  var low = bits >>> 0, high = Math.floor(bits / 4294967296);
  bytes.push(0x80);
  while (bytes.length % 64 !== 56) bytes.push(0);
  for (var i = 0; i < 4; i++) bytes.push((low >>> (8 * i)) & 0xff);
  for (var j = 0; j < 4; j++) bytes.push((high >>> (8 * j)) & 0xff);
  var shifts = [7, 12, 17, 22, 5, 9, 14, 20, 4, 11, 16, 23, 6, 10, 15, 21];
  var k = [];
  for (var n = 0; n < 64; n++) k[n] = Math.floor(Math.abs(Math.sin(n + 1)) * 4294967296) | 0;
  var a0 = 1732584193, b0 = -271733879, c0 = -1732584194, d0 = 271733878;
  for (var off = 0; off < bytes.length; off += 64) {
    var m = [];
    for (var w = 0; w < 16; w++) {
      m[w] = (bytes[off + w * 4]) | (bytes[off + w * 4 + 1] << 8) |
        (bytes[off + w * 4 + 2] << 16) | (bytes[off + w * 4 + 3] << 24);
    }
    var a = a0, b = b0, c = c0, d = d0;
    for (var r = 0; r < 64; r++) {
      var f, g;
      if (r < 16) { f = (b & c) | (~b & d); g = r; }
      else if (r < 32) { f = (d & b) | (~d & c); g = (5 * r + 1) % 16; }
      else if (r < 48) { f = b ^ c ^ d; g = (3 * r + 5) % 16; }
      else { f = c ^ (b | ~d); g = (7 * r) % 16; }
      f = (f + a + k[r] + m[g]) | 0;
      var s = shifts[(r >> 4) * 4 + (r % 4)];
      a = d; d = c; c = b;
      b = (b + ((f << s) | (f >>> (32 - s)))) | 0;
    }
    a0 = (a0 + a) | 0; b0 = (b0 + b) | 0; c0 = (c0 + c) | 0; d0 = (d0 + d) | 0;
  }
  var hex = "", words = [a0, b0, c0, d0];
  for (var q = 0; q < 4; q++) {
    for (var t = 0; t < 4; t++) {
      var byte = (words[q] >>> (8 * t)) & 0xff;
      hex += (byte < 16 ? "0" : "") + byte.toString(16);
    }
  }
  return hex;
}
function signature() {
  var ts = String(Math.floor(Date.now() / 1000));
  return ts + "." + SIG_KEY + "." + md5(ts + SIG_SALT);
}

// ---------- 接口请求 ----------
function check(page) {
  if (page.status === 404) fail("not_found", "数据源没有找到这个番号");
  var body = page.body || "";
  if (page.status === 403 || page.status === 429 || page.status === 503 ||
      /just a moment|cf-chl-|challenge-platform|security verification/i.test(body)) {
    fail("verification_required", "JAVDBUI 有 Cloudflare 校验：请在数据源配置里填写浏览器中该站点的 Cookies（含 cf_clearance）和 User-Agent");
  }
  if (page.status !== 200) fail("upstream_error", "数据源返回 HTTP " + page.status);
}
function getJSON(path, query) {
  var url = api() + path, parts = [];
  if (query) {
    for (var key in query) {
      if (query[key] === undefined || query[key] === null || query[key] === "") continue;
      parts.push(encodeURIComponent(key) + "=" + encodeURIComponent(String(query[key])));
    }
    if (parts.length) url += "?" + parts.join("&");
  }
  var headers = {accept: "application/json", jdsignature: signature()};
  var jwt = token();
  if (jwt) headers.authorization = jwt;
  var page = host.request(url, {
    accept_language: "zh-CN,zh;q=0.9,ja;q=0.8",
    headers: headers
  });
  check(page);
  var data;
  try {
    data = JSON.parse(page.body);
  } catch (e) {
    fail("parse_error", "数据源返回的不是 JSON；通常是站点接口地址变了");
  }
  if (!data || data.success !== 1) {
    var action = (data && data.action) || "", message = (data && data.message) || "";
    if (action === "InvalidSignature") fail("plugin_error", "接口签名被拒绝（InvalidSignature），站点可能更新了签名规则");
    if (/JWT/i.test(action)) fail("verification_required", loginHint());
    if (/NotFound|NoSuch/i.test(action)) fail("not_found", "数据源没有找到这个番号");
    fail("upstream_error", "数据源接口报错：" + (message || action || "未知错误"));
  }
  return data.data || {};
}

function sameNumber(a, b) {
  return String(a || "").replace(/[^0-9a-z]/gi, "").toUpperCase() ===
    String(b || "").replace(/[^0-9a-z]/gi, "").toUpperCase();
}
// 只接受番号本身或作品详情链接（/movie/<id>、/v/<id>）。
function locate(input) {
  var text = String(input || "").trim();
  if (/^https?:\/\//i.test(text)) {
    var hit = text.match(/\/v\/([A-Za-z0-9]+)(?:[\/?#]|$)/) || text.match(/\/movie\/([A-Za-z0-9]+)(?:[\/?#]|$)/);
    if (!hit) fail("invalid_number", "作品链接必须指向 /movie/<id> 或 /v/<id> 详情页");
    return {number: "", id: hit[1]};
  }
  var number = host.normalize(text);
  if (!number) fail("invalid_number", "请输入有效番号或作品链接");
  return {number: number, id: ""};
}

function findMovie(number) {
  var found = searchMovie(number, number);
  // 站点的番号可能不带前导数字（406FTHT-146 收录为 FTHT-146），去掉后再搜一次。
  var bare = number.replace(/^\d+(?=[A-Z])/, "");
  if (!found && bare !== number) found = searchMovie(bare, bare);
  return found;
}
function searchMovie(query, number) {
  var data = getJSON("/v2/search", {q: query, type: "movie", page: 1, limit: 30});
  var movies = data.movies || [];
  for (var i = 0; i < movies.length; i++) {
    if (movies[i].number && sameNumber(movies[i].number, number)) return movies[i];
  }
  return null;
}

function text(value) { return value === undefined || value === null ? "" : String(value).trim(); }
function names(items) {
  var out = [];
  for (var i = 0; i < (items || []).length; i++) {
    var name = text(items[i] && items[i].name);
    if (name && out.indexOf(name) < 0) out.push(name);
  }
  return out;
}
function build(movie, fallbackNumber) {
  var id = text(movie.id);
  var number = host.normalize(text(movie.number)) || fallbackNumber;
  if (!number) fail("parse_error", "接口没有返回番号");
  // 接口的 gender：0 女、1 男，其余视为未知（按演员保留）。
  var actors = [], maleActors = [];
  var cast = movie.actors || [];
  for (var i = 0; i < cast.length; i++) {
    var name = text(cast[i] && cast[i].name);
    if (!name) continue;
    var target = cast[i].gender === 1 ? maleActors : actors;
    if (target.indexOf(name) < 0) target.push(name);
  }
  if (!actors.length && maleActors.length) {
    actors = maleActors;
    maleActors = [];
  }
  var samples = [];
  var previews = movie.preview_images || [];
  for (var j = 0; j < previews.length; j++) {
    var url = text(previews[j] && (previews[j].large_url || previews[j].thumb_url));
    if (url && samples.indexOf(url) < 0) samples.push(url);
  }
  var trailer = text(movie.preview_video_url);
  // 站点预告片是 m3u8 播放列表，下载不了，只保留直链 MP4。
  if (!/\.mp4(\?|$)/i.test(trailer)) trailer = "";
  var cover = text(movie.cover_url) || text(movie.thumb_url);
  return {
    provider: manifest.ID,
    source_url: site() + "/movie/" + id,
    number: number,
    title: text(movie.title) || number,
    original_title: text(movie.origin_title) || text(movie.title) || number,
    outline: text(movie.summary),
    cover: cover,
    poster: cover,
    release: text(movie.release_date).replace(/\//g, "-"),
    runtime: text(movie.duration).replace(/\D+/g, ""),
    director: text(movie.director_name),
    studio: text(movie.maker_name),
    publisher: text(movie.publisher_name),
    series: text(movie.series_name),
    rating: text(movie.score),
    actors: actors,
    male_actors: maleActors,
    tags: names(movie.tags),
    samples: samples,
    trailer: trailer
  };
}

// ---------- 图片解码 ----------
// 图床（tp.spfcas.com）返回的不是原始图片：整份数据做了单字节 XOR，第 0 字节就是
// 密钥，其余每个字节都与它异或。例如 c8 37 10 37 28 → 37^c8=ff, 10^c8=d8 → JPEG。
// 站点前端的 useDecodedImage 也做同样处理（名为 prefix-byte-xor），此外还兼容固定
// 0x7F 密钥并跳过前 1~3 字节的情况，这里一并照做。
var B64CHARS = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

function base64ToBytes(text) {
  var clean = String(text || "").replace(/[^A-Za-z0-9+\/]/g, ""), out = [], buffer = 0, bits = 0;
  for (var i = 0; i < clean.length; i++) {
    buffer = (buffer << 6) | B64CHARS.indexOf(clean.charAt(i));
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      out.push((buffer >> bits) & 0xff);
    }
  }
  return out;
}
function bytesToBase64(bytes) {
  var out = "", buffer = 0, bits = 0;
  for (var i = 0; i < bytes.length; i++) {
    buffer = (buffer << 8) | bytes[i];
    bits += 8;
    while (bits >= 6) {
      bits -= 6;
      out += B64CHARS.charAt((buffer >> bits) & 0x3f);
    }
  }
  if (bits > 0) out += B64CHARS.charAt((buffer << (6 - bits)) & 0x3f);
  while (out.length % 4) out += "=";
  return out;
}
function startsWith(bytes, pattern) {
  if (bytes.length < pattern.length) return false;
  for (var i = 0; i < pattern.length; i++) {
    if (bytes[i] !== pattern[i]) return false;
  }
  return true;
}
function sniffImage(bytes) {
  if (startsWith(bytes, [0xff, 0xd8, 0xff])) return "image/jpeg";
  if (startsWith(bytes, [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])) return "image/png";
  if (startsWith(bytes, [0x47, 0x49, 0x46, 0x38, 0x37, 0x61]) ||
      startsWith(bytes, [0x47, 0x49, 0x46, 0x38, 0x39, 0x61])) return "image/gif";
  if (startsWith(bytes, [0x42, 0x4d])) return "image/bmp";
  if (startsWith(bytes, [0x52, 0x49, 0x46, 0x46]) && bytes.length > 11 &&
      bytes[8] === 0x57 && bytes[9] === 0x45 && bytes[10] === 0x42 && bytes[11] === 0x50) return "image/webp";
  return "";
}
function xorFrom(bytes, key, skip) {
  var out = [];
  for (var i = skip; i < bytes.length; i++) out.push(bytes[i] ^ key);
  return out;
}
function decodeImage(bytes) {
  var kind = sniffImage(bytes);
  if (kind) return {bytes: bytes, kind: kind};
  if (bytes.length > 1) {
    var payload = xorFrom(bytes, bytes[0], 1);
    kind = sniffImage(payload);
    if (kind) return {bytes: payload, kind: kind};
  }
  for (var skip = 0; skip < 3; skip++) {
    var alt = xorFrom(bytes, 0x7f, skip);
    kind = sniffImage(alt);
    if (kind) return {bytes: alt, kind: kind};
  }
  return null;
}

var plugin = {
  test: function() {
    var data = getJSON("/v2/search", {q: "SSNI-716", type: "movie", page: 1, limit: 1});
    if (!(data.movies || []).length) fail("unexpected_page", "接口能连上但搜索没有结果，请检查 options.api_base 是否指向正确的接口地址");
    return true;
  },
  fetch: function(input) {
    var work = locate(input);
    var id = work.id, number = work.number;
    if (!id) {
      var found = findMovie(number);
      if (!found) fail("not_found", "数据源没有收录这个番号");
      id = text(found.id);
      if (!id) fail("parse_error", "接口没有返回作品 ID");
    }
    var data = getJSON("/v4/movies/" + encodeURIComponent(id), null);
    var movie = data.movie || data;
    if (!movie || !movie.id) fail("parse_error", "接口没有返回作品详情，站点结构可能变了");
    if (!number) number = host.normalize(text(movie.number));
    return build(movie, number);
  },
  image: function(address, referer) {
    var page = host.request(address, {binary: true, referer: referer});
    if (page.status !== 200) fail("image_upstream", "图片站点拒绝访问或图片不存在（HTTP " + page.status + "）");
    var decoded = decodeImage(base64ToBytes(page.body_base64));
    if (!decoded) fail("invalid_image", "图床返回的数据无法识别为图片（" + (page.content_type || "未知类型") + "）");
    return {body_base64: bytesToBase64(decoded.bytes), content_type: decoded.kind};
  }
};
