package proxy

import "strconv"

func parseHTTPStatus(b []byte) (int, bool) {
	const prefix = "HTTP/"
	if len(b) < len(prefix)+1 {
		return 0, false
	}
	if string(b[:len(prefix)]) != prefix {
		return 0, false
	}
	i := len(prefix)
	for i < len(b) && b[i] == '/' {
		i++
	}
	for i < len(b) && b[i] != ' ' {
		i++
	}
	if i >= len(b) || b[i] != ' ' {
		return 0, false
	}
	i++
	if i+3 > len(b) {
		return 0, false
	}
	code, err := strconv.Atoi(string(b[i : i+3]))
	if err != nil || code < 100 || code > 999 {
		return 0, false
	}
	return code, true
}

func isCooldownStatus(code int) bool {
	return code == 429 || (code >= 500 && code <= 599)
}
