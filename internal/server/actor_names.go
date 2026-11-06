package server

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"html"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"
)

// 编辑演员名字：直接修改内置映射表 data/mapping_actor.xml。
// 使用远程仓库的自定义映射表时不允许修改（下次检查更新会被覆盖），需要到仓库中修改。

var actorMappingWriteMu sync.Mutex

// mappingElement 匹配映射表里的一个 <a … /> 条目，连同行首缩进。
var mappingElement = regexp.MustCompile(`(?m)^([ \t]*)<a\s[^>]*?/>`)

type actorNamesInput struct {
	JP      string   `json:"jp"`
	ZhCN    string   `json:"zh_cn"`
	ZhTW    string   `json:"zh_tw"`
	Aliases []string `json:"aliases"`
}

func cleanActorName(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func (input *actorNamesInput) validate() error {
	input.JP, input.ZhCN, input.ZhTW = cleanActorName(input.JP), cleanActorName(input.ZhCN), cleanActorName(input.ZhTW)
	if input.JP == "" {
		return errors.New("日文名不能为空")
	}
	if input.ZhCN == "" {
		input.ZhCN = input.JP
	}
	if input.ZhTW == "" {
		input.ZhTW = input.ZhCN
	}
	aliases := []string{}
	for _, alias := range input.Aliases {
		if alias = cleanActorName(alias); alias != "" {
			aliases = append(aliases, alias)
		}
	}
	input.Aliases = aliases
	if len(aliases) > 100 {
		return errors.New("别名最多 100 个")
	}
	for _, name := range append([]string{input.JP, input.ZhCN, input.ZhTW}, aliases...) {
		if utf8.RuneCountInString(name) > 100 {
			return errors.New("名字不能超过 100 个字符")
		}
		// 别名在映射表中用英文逗号分隔，名字里不能出现。
		if strings.ContainsAny(name, ",\r\n<>\"") {
			return errors.New("名字不能包含英文逗号、换行、尖括号或双引号")
		}
	}
	return nil
}

// element 生成映射表条目。keyword 按映射表的习惯包含全部名字和别名，去重。
func (input actorNamesInput) element(indent string) string {
	seen := map[string]bool{}
	var keywords []string
	for _, name := range append(append([]string{}, input.Aliases...), input.JP, input.ZhCN, input.ZhTW) {
		if key := normalizeActorName(name); key != "" && !seen[key] {
			seen[key] = true
			keywords = append(keywords, name)
		}
	}
	attr := func(value string) string { return html.EscapeString(value) }
	return indent + `<a zh_cn="` + attr(input.ZhCN) + `" zh_tw="` + attr(input.ZhTW) + `" jp="` + attr(input.JP) + `" keyword="` + attr(strings.Join(keywords, ",")) + `" />`
}

// elementNames 解析一个 <a … /> 条目的全部名字（已规范化）。
func elementNames(element []byte) map[string]bool {
	var item struct {
		ZhCN    string `xml:"zh_cn,attr"`
		ZhTW    string `xml:"zh_tw,attr"`
		JP      string `xml:"jp,attr"`
		Keyword string `xml:"keyword,attr"`
	}
	names := map[string]bool{}
	if xml.Unmarshal(element, &item) != nil {
		return names
	}
	for _, name := range append([]string{item.ZhCN, item.ZhTW, item.JP}, strings.Split(item.Keyword, ",")...) {
		if key := normalizeActorName(name); key != "" {
			names[key] = true
		}
	}
	return names
}

// updateActorMappingFile 修改映射表里包含 embyName 的条目；没有这样的条目时在末尾新增一条。
func updateActorMappingFile(path, embyName string, input actorNamesInput) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	newline := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		newline = "\r\n"
	}
	target := normalizeActorName(embyName)
	var next []byte
	added := false
	for _, match := range mappingElement.FindAllSubmatchIndex(data, -1) {
		if elementNames(data[match[0]:match[1]])[target] {
			indent := string(data[match[2]:match[3]])
			next = append(append(append([]byte{}, data[:match[0]]...), input.element(indent)...), data[match[1]:]...)
			break
		}
	}
	if next == nil {
		closing := bytes.LastIndex(data, []byte("</actor>"))
		if closing < 0 {
			return false, errors.New("映射表格式不正确：找不到 </actor>")
		}
		line := input.element("  ") + newline
		next = append(append(append([]byte{}, data[:closing]...), line...), data[closing:]...)
		added = true
	}
	if _, err := parseActorMapping(next); err != nil {
		return false, err
	}
	return added, writeFileAtomic(path, next)
}

// updateActorNames 提供 PUT /api/actors/{id}/names。
func (s *Server) updateActorNames(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var input actorNamesInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		fail(w, 400, "invalid_body", "请求格式错误")
		return
	}
	if err := input.validate(); err != nil {
		fail(w, 400, "invalid_names", err.Error())
		return
	}
	if _, _, custom := s.actorMappingState(); custom {
		fail(w, 403, "mapping_read_only", "当前使用远程仓库的映射表，不能在这里修改，请到仓库中修改后检查更新")
		return
	}
	conn, err := s.embyConnection()
	if err != nil {
		embyFailure(w, err)
		return
	}
	if _, _, err := s.actorDir.load(r.Context(), conn, false); err != nil {
		embyFailure(w, err)
		return
	}
	actor, ok := s.actorDir.get(r.PathValue("id"))
	if !ok {
		fail(w, 404, "not_found", "Emby 中没有这位演员")
		return
	}
	// 保存后要能通过 Emby 原名找到这一条，所以把原名加进别名。
	if !elementNamesOf(input)[normalizeActorName(actor.Name)] {
		input.Aliases = append(input.Aliases, actor.Name)
	}
	actorMappingWriteMu.Lock()
	added, err := updateActorMappingFile(filepath.Join(dataDirOf(s), builtinActorMappingFile), actor.Name, input)
	actorMappingWriteMu.Unlock()
	if err != nil {
		fail(w, 500, "save_failed", "保存映射表失败："+err.Error())
		return
	}
	slog.Info("[mapping] 已修改演员名字", "emby", actor.Name, "jp", input.JP, "added", added)
	writeJSON(w, 200, object{"success": true, "added": added})
}

func elementNamesOf(input actorNamesInput) map[string]bool {
	names := map[string]bool{}
	for _, name := range append([]string{input.JP, input.ZhCN, input.ZhTW}, input.Aliases...) {
		names[normalizeActorName(name)] = true
	}
	return names
}
