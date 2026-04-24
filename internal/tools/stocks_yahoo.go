package tools

import "strings"

// yahooAliases maps common index names and aliases to canonical ticker symbols.
// These canonical symbols (Yahoo Finance format) are used as cache keys
// regardless of the underlying data source.
var yahooAliases = map[string]string{
	"sp500":       "^GSPC",
	"s&p500":      "^GSPC",
	"s&p 500":     "^GSPC",
	"nasdaq":      "^IXIC",
	"dji":         "^DJI",
	"dow":         "^DJI",
	"dow jones":   "^DJI",
	"russell":     "^RUT",
	"russell2000": "^RUT",
}

// resolveYahooAlias converts common index names to canonical ticker symbols.
// Returns the input unchanged if no alias matches.
func resolveYahooAlias(ticker string) string {
	if resolved, ok := yahooAliases[strings.ToLower(ticker)]; ok {
		return resolved
	}
	return ticker
}
