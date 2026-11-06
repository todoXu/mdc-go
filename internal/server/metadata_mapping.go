package server

import "mdcng-go/internal/provider"

// metadataMapping 记录一次字段转换，例如演员映射表把某个演员名替换成另一个名字。
// 详情页「NFO 写入数据」用它标出被映射的值并显示原值。
type metadataMapping struct {
	Field string `json:"field"`          // 元数据字段名，如 actors
	From  string `json:"from"`           // 刮削得到的原值
	To    string `json:"to"`             // 实际写入的值
	Rule  string `json:"rule,omitempty"` // 来源规则，如 actor_mapping
}

// applyMetadataMappings 是演员映射表等转换规则的预留入口。
//
// 约定：传入数据源合并后的元数据（刮削记录的 metadata 保持这个原值），
// 返回用于命名与 NFO 的转换后元数据以及转换明细；明细保存到 scrapeJob.Mappings。
// 目前尚无映射表，原样返回，不做任何转换，也尚未接入整理流程。
func applyMetadataMappings(meta provider.Metadata, _ scrapeSettings) (provider.Metadata, []metadataMapping) {
	return meta, nil
}
