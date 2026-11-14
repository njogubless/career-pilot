package listings

import (
	"net/url"
	"strings"
)

func parseQuery(q string) (url.Values, error) {
	if len(q) > 2000 {
		return nil, url.EscapeError("too long")
	}
	return url.ParseQuery(strings.TrimPrefix(q, "?"))
}
