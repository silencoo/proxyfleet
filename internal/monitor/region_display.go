package monitor

import "strings"

type regionNamePattern struct {
	region   string
	keywords []string
	tokens   []string
}

var regionNamePatterns = []regionNamePattern{
	{region: "resident", keywords: []string{"家宽", "家庭宽带", "住宅", "原生", "residential", "resident"}, tokens: []string{"isp", "home"}},
	{region: "hk", keywords: []string{"香港", "hong kong", "hongkong", "🇭🇰"}, tokens: []string{"hk"}},
	{region: "jp", keywords: []string{"日本", "东京", "大阪", "埼玉", "japan", "🇯🇵"}, tokens: []string{"jp"}},
	{region: "kr", keywords: []string{"韩国", "南韩", "首尔", "仁川", "south korea", "korea", "🇰🇷"}, tokens: []string{"kr"}},
	{region: "us", keywords: []string{"美国", "圣何塞", "洛杉矶", "阿什本", "united states", "🇺🇸"}, tokens: []string{"us", "usa"}},
	{region: "tw", keywords: []string{"台湾", "台灣", "新北", "彰化", "taiwan", "🇹🇼"}, tokens: []string{"tw"}},
	{region: "sg", keywords: []string{"新加坡", "狮城", "獅城", "singapore", "🇸🇬"}, tokens: []string{"sg"}},
}

// ResolveDisplayRegion keeps an explicit GeoIP classification and falls back
// to subscription metadata when GeoIP is disabled or cannot classify it. It is
// shared by the API and named pool profiles so both expose identical regions.
func ResolveDisplayRegion(name, tag, country, reportedRegion string) string {
	reported := strings.ToLower(strings.TrimSpace(reportedRegion))
	if reported != "" && reported != "other" && reported != "unknown" {
		return reported
	}

	searchable := strings.ToLower(strings.Join([]string{name, tag, country}, " "))
	for _, pattern := range regionNamePatterns {
		for _, keyword := range pattern.keywords {
			if strings.Contains(searchable, keyword) {
				return pattern.region
			}
		}
		for _, token := range pattern.tokens {
			if containsASCIIToken(searchable, token) {
				return pattern.region
			}
		}
	}
	return "other"
}

func displayRegion(snapshot Snapshot) string {
	return ResolveDisplayRegion(snapshot.Name, snapshot.Tag, snapshot.Country, snapshot.Region)
}

func containsASCIIToken(text, token string) bool {
	for offset := 0; offset <= len(text)-len(token); {
		index := strings.Index(text[offset:], token)
		if index < 0 {
			return false
		}
		index += offset
		beforeOK := index == 0 || !isASCIILetter(text[index-1])
		after := index + len(token)
		afterOK := after == len(text) || !isASCIILetter(text[after])
		if beforeOK && afterOK {
			return true
		}
		offset = index + 1
	}
	return false
}

func isASCIILetter(value byte) bool {
	return value >= 'a' && value <= 'z'
}
