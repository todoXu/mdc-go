package server

import (
	"encoding/json"
	"fmt"
)

func validateDownloadConfig(raw json.RawMessage) error {
	var settings scrapeSettings
	if err := json.Unmarshal(raw, &settings.Download); err != nil {
		return fmt.Errorf("下载配置格式无效：%w", err)
	}
	d := settings.Download
	if d.Strategy != "" && d.Strategy != "Priority" && d.Strategy != "Quality" {
		return fmt.Errorf("缩略图下载策略必须是 Priority 或 Quality")
	}
	for _, method := range []string{d.CropRegular, d.CropFC2} {
		if method != "" && method != "None" && method != "Right" && method != "Center" && method != "Face" {
			return fmt.Errorf("海报裁剪方式无效：%s", method)
		}
	}
	if d.Aspect != "" && d.Aspect != "Full" && d.Aspect != "Emby" {
		return fmt.Errorf("海报裁剪比例必须是 Full 或 Emby")
	}
	if d.MinWidth < 0 || d.MinHeight < 0 || d.MinWidth > 20000 || d.MinHeight > 20000 {
		return fmt.Errorf("图片最小尺寸必须在 0～20000 像素之间")
	}
	return nil
}
