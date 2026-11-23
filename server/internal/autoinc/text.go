package autoinc

import "strings"

// The status page's default language decides the wording, because visitors
// read these incidents. {names} is the affected services' public names.
type texts struct {
	Title, Investigating, AlsoAffected, Resolved string
}

var byLang = map[string]texts{
	"en": {"Problems with {names}", "We're investigating problems with {names}.", "We're also seeing problems with {names}.", "This is resolved and everything is working normally again."},
	"es": {"Problemas con {names}", "Estamos investigando problemas con {names}.", "También vemos problemas con {names}.", "Se ha resuelto y todo vuelve a funcionar con normalidad."},
	"fr": {"Problèmes avec {names}", "Nous enquêtons sur des problèmes avec {names}.", "Nous constatons aussi des problèmes avec {names}.", "Le problème est résolu et tout fonctionne de nouveau normalement."},
	"de": {"Probleme mit {names}", "Wir untersuchen Probleme mit {names}.", "Wir sehen auch Probleme mit {names}.", "Das Problem ist behoben und alles funktioniert wieder normal."},
	"pt": {"Problemas com {names}", "Estamos investigando problemas com {names}.", "Também estamos vendo problemas com {names}.", "O problema foi resolvido e tudo voltou a funcionar normalmente."},
	"it": {"Problemi con {names}", "Stiamo analizzando problemi con {names}.", "Riscontriamo problemi anche con {names}.", "Il problema è risolto e tutto funziona di nuovo normalmente."},
	"nl": {"Problemen met {names}", "We onderzoeken problemen met {names}.", "We zien ook problemen met {names}.", "Dit is opgelost en alles werkt weer normaal."},
	"pl": {"Problemy z: {names}", "Badamy problemy z: {names}.", "Widzimy też problemy z: {names}.", "Problem został rozwiązany i wszystko znów działa normalnie."},
	"da": {"Problemer med {names}", "Vi undersøger problemer med {names}.", "Vi ser også problemer med {names}.", "Det er løst, og alt fungerer normalt igen."},
	"sv": {"Problem med {names}", "Vi undersöker problem med {names}.", "Vi ser också problem med {names}.", "Det är löst och allt fungerar som vanligt igen."},
	"nb": {"Problemer med {names}", "Vi undersøker problemer med {names}.", "Vi ser også problemer med {names}.", "Det er løst, og alt fungerer normalt igjen."},
	"fi": {"Ongelmia: {names}", "Selvitämme ongelmia: {names}.", "Havaitsemme ongelmia myös: {names}.", "Ongelma on ratkaistu, ja kaikki toimii taas normaalisti."},
	"ja": {"{names}で問題が発生しています", "{names}で発生している問題を調査しています。", "{names}でも問題が発生しています。", "問題は解決し、すべて正常に動作しています。"},
	"zh": {"{names} 出现问题", "我们正在调查 {names} 的问题。", "我们还发现 {names} 出现问题。", "问题已解决，一切恢复正常。"},
	"ko": {"{names} 문제 발생", "{names}의 문제를 조사하고 있습니다.", "{names}에서도 문제가 발생했습니다.", "문제가 해결되어 모든 서비스가 정상적으로 작동합니다."},
}

func textsFor(lang string) texts {
	if t, ok := byLang[lang]; ok {
		return t
	}
	return byLang["en"]
}

func fill(s string, names []string) string {
	sep := ", "
	if len(names) > 0 && isCJK(names[0]) {
		sep = "、"
	}
	return strings.ReplaceAll(s, "{names}", strings.Join(names, sep))
}

func isCJK(s string) bool {
	for _, r := range s {
		if r >= 0x3000 {
			return true
		}
	}
	return false
}
