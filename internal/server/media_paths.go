package server

import (
	"fmt"
	"path/filepath"
)

func scrapeAssetPath(base, kind, extension string, imageWithPrefix bool) string {
	if imageWithPrefix {
		return base + "-" + kind + extension
	}
	return filepath.Join(filepath.Dir(base), kind+extension)
}

func scrapeExtraFanartPath(base string, index int, extension string, imageWithPrefix bool) string {
	if imageWithPrefix {
		return fmt.Sprintf("%s-extrafanart-%03d%s", base, index, extension)
	}
	return filepath.Join(filepath.Dir(base), fmt.Sprintf("extrafanart-%03d%s", index, extension))
}

func scrapeAssetCandidates(base, kind, extension string) []string {
	return []string{
		scrapeAssetPath(base, kind, extension, false),
		scrapeAssetPath(base, kind, extension, true),
	}
}

func scrapeAssetCandidateOrder(base, kind, extension string, imageWithPrefix bool) []string {
	return []string{
		scrapeAssetPath(base, kind, extension, imageWithPrefix),
		scrapeAssetPath(base, kind, extension, !imageWithPrefix),
	}
}

func scrapeExtraFanartCandidates(base string, index int, extension string) []string {
	return []string{
		scrapeExtraFanartPath(base, index, extension, false),
		scrapeExtraFanartPath(base, index, extension, true),
	}
}

func scrapeExtraFanartCandidateOrder(base string, index int, extension string, imageWithPrefix bool) []string {
	return []string{
		scrapeExtraFanartPath(base, index, extension, imageWithPrefix),
		scrapeExtraFanartPath(base, index, extension, !imageWithPrefix),
	}
}

func scrapeNamedAssetCandidateOrder(base, name string, imageWithPrefix bool) []string {
	prefixed := base + "-" + name
	unprefixed := filepath.Join(filepath.Dir(base), name)
	if imageWithPrefix {
		return []string{prefixed, unprefixed}
	}
	return []string{unprefixed, prefixed}
}
