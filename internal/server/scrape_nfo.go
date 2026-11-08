package server

import (
	"encoding/xml"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const maxRecordNFOBytes = 1 << 20

// NFO 标签来源。除 scraped 外均为 NFO 附加标签设置生成。
const (
	nfoTagScraped    = "scraped"    // 数据源刮削的原标签
	nfoTagResolution = "resolution" // 按影片实际分辨率生成；在 Dropped 中表示被替换的刮削分辨率标签
	nfoTagCustom     = "custom"     // 文件名命中的自定义规则
)

// excludedScrapedTags 是数据源标签中不写入 NFO 的破解描述。键为小写并去掉首尾空白后的标签。
// 破解类型只由文件名识别的 hack_type 标签表示，不使用数据源的描述。
var excludedScrapedTags = map[string]bool{"無碼破解": true, "无码破解": true}

// nfoTagEntry 是一个 NFO 标签及其来源：scraped、serial_name、actor、resolution、
// mosaic、hack_type、subtitle、series、studio、publisher。
type nfoTagEntry struct {
	Tag    string `json:"tag"`
	Origin string `json:"origin"`
}

// nfoTagTrace 在生成 NFO 时记录，避免事后按文字比对猜测标签来源。
type nfoTagTrace struct {
	Tags    []nfoTagEntry `json:"tags"`              // 写入 NFO 的标签，顺序与文件一致
	Dropped []nfoTagEntry `json:"dropped,omitempty"` // 未写入的刮削标签及原因
}

// recordNFOFields 是 NFO 文件解析后返回给前端的字段，名称与 NFO 标签一致。
type recordNFOFields struct {
	Title         string   `json:"title"`
	OriginalTitle string   `json:"originaltitle"`
	SortTitle     string   `json:"sorttitle"`
	Tagline       string   `json:"tagline"`
	Num           string   `json:"num"`
	PublishNumber string   `json:"publishnumber"`
	Actors        []string `json:"actors"`
	Director      string   `json:"director"`
	Studio        string   `json:"studio"`
	Maker         string   `json:"maker"`
	Publisher     string   `json:"publisher"`
	Label         string   `json:"label"`
	Series        string   `json:"series"`
	Sets          []string `json:"sets"`
	Outline       string   `json:"outline"`
	Plot          string   `json:"plot"`
	OriginalPlot  string   `json:"originalplot"`
	Premiered     string   `json:"premiered"`
	ReleaseDate   string   `json:"releasedate"`
	Release       string   `json:"release"`
	Year          string   `json:"year"`
	Runtime       string   `json:"runtime"`
	Rating        string   `json:"rating"`
	CriticRating  string   `json:"criticrating"`
	Votes         string   `json:"votes"`
	Country       string   `json:"country"`
	MPAA          string   `json:"mpaa"`
	CustomRating  string   `json:"customrating"`
	Poster        string   `json:"poster"`
	Thumb         string   `json:"thumb"`
	Fanart        string   `json:"fanart"`
	Trailer       string   `json:"trailer"`
	Website       string   `json:"website"`
	Tags          []string `json:"tags"`
	Genres        []string `json:"genres"`
}

type recordNFO struct {
	Enabled  bool              `json:"enabled"`         // 该记录执行时 NFO 是否开启
	Exists   bool              `json:"exists"`          // 磁盘上是否存在 NFO 文件
	Name     string            `json:"name,omitempty"`  // NFO 文件名
	Error    string            `json:"error,omitempty"` // 读取或解析失败原因
	Raw      string            `json:"raw,omitempty"`   // 原始 XML
	Fields   *recordNFOFields  `json:"fields,omitempty"`
	Mappings []metadataMapping `json:"mappings"`
	// TagOrigins 是生成该 NFO 时记录的标签来源；旧记录或 NFO 被外部修改前生成的记录可能为空。
	TagOrigins *nfoTagTrace `json:"tag_origins,omitempty"`
}

// getScrapeJobNFO 读取成功记录输出文件旁实际写入的 NFO；只访问由记录输出路径推导出的文件。
func (s *Server) getScrapeJobNFO(w http.ResponseWriter, r *http.Request) {
	s.jobsMu.RLock()
	var job scrapeJob
	found := false
	for _, item := range s.jobs {
		if item.ID == r.PathValue("id") {
			job, found = item, true
			break
		}
	}
	mappings := append([]metadataMapping{}, job.Mappings...)
	var tagOrigins *nfoTagTrace
	if job.NFOTags != nil {
		copied := nfoTagTrace{Tags: append([]nfoTagEntry{}, job.NFOTags.Tags...), Dropped: append([]nfoTagEntry{}, job.NFOTags.Dropped...)}
		tagOrigins = &copied
	}
	s.jobsMu.RUnlock()
	if !found {
		fail(w, 404, "not_found", "记录不存在")
		return
	}
	result := recordNFO{Mappings: mappings, TagOrigins: tagOrigins}
	if job.Settings != nil {
		result.Enabled = job.Settings.NFO.Enabled
	}
	if job.Status != 2 || job.Output == "" {
		writeJSON(w, 200, result)
		return
	}
	path := strings.TrimSuffix(job.Output, filepath.Ext(job.Output)) + ".nfo"
	result.Name = filepath.Base(path)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		writeJSON(w, 200, result)
		return
	}
	result.Exists = true
	file, err := os.Open(path)
	if err != nil {
		result.Error = "无法读取 NFO 文件"
		writeJSON(w, 200, result)
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxRecordNFOBytes+1))
	if err != nil || len(raw) > maxRecordNFOBytes {
		result.Error = "NFO 文件读取失败或超过 1 MB"
		writeJSON(w, 200, result)
		return
	}
	result.Raw = string(raw)
	var movie nfoMovie
	if err := xml.Unmarshal(raw, &movie); err != nil {
		result.Error = "NFO 解析失败：" + err.Error()
		writeJSON(w, 200, result)
		return
	}
	actors := make([]string, 0, len(movie.Actors))
	for _, actor := range movie.Actors {
		actors = append(actors, actor.Name)
	}
	result.Fields = &recordNFOFields{
		Title: movie.Title, OriginalTitle: movie.OriginalTitle, SortTitle: movie.SortTitle, Tagline: movie.Tagline,
		Num: movie.Num, PublishNumber: movie.PublishNumber, Actors: actors, Director: movie.Director,
		Studio: movie.Studio, Maker: movie.Maker, Publisher: movie.Publisher, Label: movie.Label,
		Series: movie.Series, Sets: nonNil(movie.Sets), Outline: movie.Outline, Plot: movie.Plot,
		OriginalPlot: movie.OriginalPlot, Premiered: movie.Premiered, ReleaseDate: movie.ReleaseDate,
		Release: movie.Release, Year: movie.Year, Runtime: movie.Runtime, Rating: movie.Rating,
		CriticRating: movie.CriticRating, Votes: movie.Votes, Country: movie.Country, MPAA: movie.MPAA,
		CustomRating: movie.CustomRating, Poster: movie.Poster, Thumb: movie.Thumb, Fanart: movie.Fanart,
		Trailer: movie.Trailer, Website: movie.Website, Tags: nonNil(movie.Tags), Genres: nonNil(movie.Genres),
	}
	writeJSON(w, 200, result)
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
